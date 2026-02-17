package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type deployRequest struct {
	projectKey string
	payload    GitLabPushEvent // весь payload с коммитами, автором и т.д.
	timestamp  time.Time
}

type App struct {
	Cfg            Config
	Bot            *tgbotapi.BotAPI
	deployMutex    sync.Mutex
	pendingDeploys map[string]*deployRequest // projectKey -> последний запрос
	deployTimers   map[string]*time.Timer    // projectKey -> таймер debounce
	mu             sync.Mutex                // защита для maps
}

func NewApp(cfg Config, bot *tgbotapi.BotAPI) *App {
	return &App{
		Cfg:            cfg,
		Bot:            bot,
		pendingDeploys: make(map[string]*deployRequest),
		deployTimers:   make(map[string]*time.Timer),
	}
}

func (a *App) DeployHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Incoming webhook from %s", r.RemoteAddr)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	secret := r.Header.Get(a.Cfg.DeployHeaderKey)
	if secret == "" {
		http.Error(w, "missing secret header", http.StatusForbidden)
		return
	}

	projectCfg, ok := a.Cfg.Projects[secret]
	if !ok {
		http.Error(w, "unknown project", http.StatusForbidden)
		return
	}

	payload, err := decodePayload(r)
	if err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}

	log.Printf("Incoming deploy for %s (SHA: %s)", payload.ProjectURL(), payload.SHA())
	expectedRef := "refs/heads/" + projectCfg.GetDeployBranch()
	if payload.Ref != expectedRef {
		log.Printf("Expected ref %s, got %s", expectedRef, payload.Ref)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	sha := payload.SHA()
	if sha == "" {
		http.Error(w, "missing sha", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	a.scheduleDeploy(secret, payload, projectCfg)
}

func (a *App) scheduleDeploy(projectKey string, payload GitLabPushEvent, cfg ProjectConfig) {
	log.Printf("Scheduling deploy for %s (SHA: %s)", projectKey, payload.SHA())
	a.mu.Lock()
	defer a.mu.Unlock()

	// Сохраняем весь payload - в нём есть всё: коммиты, автор, URL'ы
	a.pendingDeploys[projectKey] = &deployRequest{
		projectKey: projectKey,
		payload:    payload,
		timestamp:  time.Now(),
	}

	// Если таймер уже есть — останавливаем его
	if timer, exists := a.deployTimers[projectKey]; exists {
		timer.Stop()
	}

	debounce := cfg.GetDebounceDuration()
	a.deployTimers[projectKey] = time.AfterFunc(debounce, func() {
		a.executeDeploy(projectKey, cfg)
	})

	log.Printf("Scheduled deploy for %s (SHA: %s) with %v debounce", projectKey, payload.SHA(), debounce)
}

func (a *App) executeDeploy(projectKey string, cfg ProjectConfig) {
	a.mu.Lock()
	req, ok := a.pendingDeploys[projectKey]
	if !ok {
		a.mu.Unlock()
		return
	}
	delete(a.pendingDeploys, projectKey)
	delete(a.deployTimers, projectKey)
	a.mu.Unlock()

	log.Printf("Executing deploy for %s (SHA: %s)", projectKey, req.payload.SHA())
	a.runDeploy(req, cfg)
}

func (a *App) runDeploy(req *deployRequest, cfg ProjectConfig) {
	a.deployMutex.Lock()
	defer a.deployMutex.Unlock()

	start := time.Now()
	payload := req.payload

	// Извлекаем данные из payload
	sha := payload.SHA()
	branch := payload.Branch()
	author := payload.AuthorName()
	commitURL := payload.CommitURL()
	projectURL := payload.ProjectURL()
	commitsSummary := escape(payload.CommitsSummary(5)) // последние 5 коммитов

	// Отправляем стартовое сообщение
	startMsgID := a.sendTelegramAndGetID(
		msgStarted(cfg.Name, sha, branch, author, commitURL, projectURL, commitsSummary),
	)

	// Checkout SHA
	if err := a.checkoutSHA(sha, cfg.WorkingDir, cfg.SSHKeyPath); err != nil {
		a.sendTelegramAndGetID(
			msgFailed(cfg.Name, sha, branch, author, commitURL, projectURL, time.Since(start), err, commitsSummary),
		)
		return
	}

	// Прогресс по шагам
	totalSteps := len(cfg.DeploySteps)
	progressMsgID := a.sendTelegramAndGetID(
		msgSteps(cfg.Name, cfg.DeploySteps, 0, totalSteps, stepRunning),
	)

	for i, step := range cfg.DeploySteps {
		a.editTelegram(progressMsgID,
			msgSteps(cfg.Name, cfg.DeploySteps, i, totalSteps, stepRunning),
		)

		if err := runCmd(cfg.WorkingDir, cfg.SSHKeyPath, step.Cmd, step.Args...); err != nil {
			a.editTelegram(progressMsgID,
				msgSteps(cfg.Name, cfg.DeploySteps, i, totalSteps, stepFailed),
			)
			a.sendTelegramAndGetID(
				msgFailed(cfg.Name, sha, branch, author, commitURL, projectURL, time.Since(start), err, commitsSummary),
			)
			return
		}
	}

	// Успешный деплой
	a.editTelegram(progressMsgID,
		msgSteps(cfg.Name, cfg.DeploySteps, totalSteps, totalSteps, stepDone),
	)

	// Редактируем стартовое сообщение на успешное
	if startMsgID > 0 {
		a.editTelegram(startMsgID,
			msgSuccess(cfg.Name, sha, branch, author, commitURL, time.Since(start), projectURL, commitsSummary),
		)
	} else {
		a.sendTelegramAndGetID(
			msgSuccess(cfg.Name, sha, branch, author, commitURL, time.Since(start), projectURL, commitsSummary),
		)
	}

	log.Printf("Deploy for %s (SHA: %s) completed successfully", req.projectKey, sha)
}

func (a *App) checkoutSHA(sha, workingDir, sshKeyPath string) error {
	steps := [][]string{
		{"git", "fetch", "origin", sha},
		{"git", "checkout", "--detach", sha},
	}
	for _, s := range steps {
		if err := runCmd(workingDir, sshKeyPath, s[0], s[1:]...); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) sendTelegramAndGetID(msg string) int {
	return sendTelegramAndGetID(msg, Telegram(a.Cfg.Telegram), a.Bot)
}

func runCmd(workingDir, sshKeyPath, cmd string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	c := exec.CommandContext(ctx, cmd, args...)
	c.Dir = workingDir

	if sshKeyPath != "" {
		c.Env = append(os.Environ(),
			fmt.Sprintf("GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=no", sshKeyPath),
		)
	}

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()

	if err != nil && stderr.Len() > 0 {
		return fmt.Errorf("%w: %s", err, stderr.String())
	}

	return nil
}

func decodePayload(r *http.Request) (GitLabPushEvent, error) {
	defer r.Body.Close()
	var p GitLabPushEvent
	err := json.NewDecoder(r.Body).Decode(&p)
	return p, err
}
