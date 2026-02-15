package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	FailedDeployMessage     = "❌ Deploy failed\nProject: %s\nSHA: %s\nError: %v\nDuration: %s"
	SuccessfulDeployMessage = "✅ Deploy successful\nProject: %s\nSHA: %s\nDuration: %s"
	StartedDeployMessage    = "🚀 Deploy started\nProject: %s\nSHA: %s"
)

type deployRequest struct {
	projectKey string
	sha        string
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

	// Извлекаем секрет из header'а
	secret := r.Header.Get(a.Cfg.DeployHeaderKey)
	if secret == "" {
		http.Error(w, "missing secret header", http.StatusForbidden)
		return
	}

	// Ищем проект по секрету
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

	log.Printf("Incoming deploy for %s (SHA: %s)", secret, payload.SHA())
	// Проверяем, что это нужная ветка
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

	// Debounce: откладываем деплой и обновляем последний SHA
	a.scheduleDeploy(secret, sha, projectCfg)
}

func (a *App) scheduleDeploy(projectKey, sha string, cfg ProjectConfig) {
	log.Printf("Scheduling deploy for %s (SHA: %s)", projectKey, sha)
	a.mu.Lock()
	defer a.mu.Unlock()

	// Обновляем pending request
	a.pendingDeploys[projectKey] = &deployRequest{
		projectKey: projectKey,
		sha:        sha,
		timestamp:  time.Now(),
	}

	// Если таймер уже есть — останавливаем его
	if timer, exists := a.deployTimers[projectKey]; exists {
		timer.Stop()
	}

	// Создаём новый таймер
	debounce := cfg.GetDebounceDuration()
	a.deployTimers[projectKey] = time.AfterFunc(debounce, func() {
		a.executeDeploy(projectKey, cfg)
	})

	log.Printf("Scheduled deploy for %s (SHA: %s) with %v debounce", projectKey, sha, debounce)
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

	log.Printf("Executing deploy for %s (SHA: %s)", projectKey, req.sha)
	a.runDeploy(projectKey, req.sha, cfg)
}

func (a *App) runDeploy(projectKey, sha string, cfg ProjectConfig) {
	a.deployMutex.Lock()
	defer a.deployMutex.Unlock()

	start := time.Now()
	a.sendTelegram(fmt.Sprintf(StartedDeployMessage, cfg.Name, sha))

	if err := a.checkoutSHA(sha, cfg.WorkingDir, cfg.SSHKeyPath); err != nil {
		a.failDeploy(start, cfg.Name, sha, err)
		return
	}

	if err := a.runConfiguredSteps(cfg); err != nil {
		a.failDeploy(start, cfg.Name, sha, err)
		return
	}

	a.sendTelegram(fmt.Sprintf(
		SuccessfulDeployMessage,
		cfg.Name,
		sha,
		time.Since(start),
	))
	log.Printf("Deploy for %s (SHA: %s) completed successfully", projectKey, sha)
}

func (a *App) failDeploy(start time.Time, projectName, sha string, err error) {
	a.sendTelegram(fmt.Sprintf(
		FailedDeployMessage,
		projectName,
		sha,
		err,
		time.Since(start),
	))
}

func (a *App) checkoutSHA(sha, workingDir string, sshKeyPath string) error {
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

func (a *App) runConfiguredSteps(cfg ProjectConfig) error {
	for _, step := range cfg.DeploySteps {
		if err := runCmd(cfg.WorkingDir, cfg.SSHKeyPath, step.Cmd, step.Args...); err != nil {
			return err
		}
	}
	return nil
}

func runCmd(workingDir, sshKeyPath, cmd string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	c := exec.CommandContext(ctx, cmd, args...)
	c.Dir = workingDir

	// Если указан SSH ключ, настраиваем git для его использования
	if sshKeyPath != "" {
		c.Env = append(os.Environ(),
			fmt.Sprintf("GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=no", sshKeyPath),
		)
	}

	out, err := c.CombinedOutput()
	log.Printf("CMD [%s]: %s %v\n%s\n", workingDir, cmd, args, string(out))
	return err
}

func (a *App) sendTelegram(text string) {
	if a.Cfg.Telegram.Enabled {
		if err := a.sendToThread(text, a.Cfg.Telegram.ChatID, a.Cfg.Telegram.ThreadID); err != nil {
			log.Println(err.Error())
		}
	}
}

func (a *App) sendToThread(text string, chatID int64, threadID int64) error {
	params := tgbotapi.Params{
		"chat_id":           strconv.Itoa(int(chatID)),
		"message_thread_id": strconv.Itoa(int(threadID)),
		"text":              text,
	}
	if _, err := a.Bot.MakeRequest("sendMessage", params); err != nil {
		return err
	}
	return nil
}

func decodePayload(r *http.Request) (GitLabPushEvent, error) {
	defer r.Body.Close()
	var p GitLabPushEvent
	err := json.NewDecoder(r.Body).Decode(&p)
	return p, err
}
