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
)

type deployRequest struct {
	projectKey string
	payload    GitLabPushEvent
	timestamp  time.Time
}

type App struct {
	cfg      Config
	notifier Notifier
	status   *statusStore
	store    *Store

	mu             sync.Mutex
	pendingDeploys map[string]*deployRequest
	deployTimers   map[string]*time.Timer

	// per-project locks: concurrent deploys of different projects are allowed;
	// same-project deploys are serialised.
	projectLocks sync.Map // string -> *sync.Mutex
}

// New initialises the application, connecting to any enabled notifiers.
func New(cfg Config) (*App, error) {
	tgn, err := NewTelegramNotifier(cfg.Telegram)
	if err != nil {
		return nil, err
	}
	ntfyn := NewNtfyNotifier(cfg.Ntfy)
	notifier := buildNotifier(cfg.Notify, map[string]Notifier{
		"telegram": tgn,
		"ntfy":     ntfyn,
	})

	storagePath := cfg.Server.StoragePath
	if storagePath == "" {
		storagePath = "deploys.jsonl"
	}
	store, err := OpenStore(storagePath, cfg.Server.HistorySize)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}

	return &App{
		cfg:            cfg,
		notifier:       notifier,
		status:         newStatusStore(store),
		store:          store,
		pendingDeploys: make(map[string]*deployRequest),
		deployTimers:   make(map[string]*time.Timer),
	}, nil
}

// Close flushes and closes the persistent store.
func (a *App) Close() error {
	return a.store.Close()
}

func (a *App) DeployHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Incoming webhook from %s", r.RemoteAddr)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	secret := r.Header.Get(a.cfg.DeployHeaderKey)
	if secret == "" {
		http.Error(w, "missing secret header", http.StatusForbidden)
		return
	}

	projectCfg, ok := a.cfg.Projects[secret]
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
		log.Printf("Expected ref %s, got %s — ignoring", expectedRef, payload.Ref)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	if payload.SHA() == "" {
		http.Error(w, "missing sha", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	a.scheduleDeploy(secret, payload, projectCfg)
}

func (a *App) scheduleDeploy(projectKey string, payload GitLabPushEvent, cfg ProjectConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.pendingDeploys[projectKey] = &deployRequest{
		projectKey: projectKey,
		payload:    payload,
		timestamp:  time.Now(),
	}
	a.status.setPending(projectKey, cfg.Name)

	if timer, exists := a.deployTimers[projectKey]; exists {
		timer.Stop()
	}

	debounce := cfg.GetDebounceDuration()
	a.deployTimers[projectKey] = time.AfterFunc(debounce, func() {
		a.executeDeploy(projectKey, cfg)
	})

	log.Printf("Scheduled deploy for %s (SHA: %s) with %v debounce", cfg.Name, payload.SHA(), debounce)
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

	a.runDeploy(req, cfg)
}

func (a *App) runDeploy(req *deployRequest, cfg ProjectConfig) {
	mu := a.projectLock(req.projectKey)
	mu.Lock()
	defer mu.Unlock()

	start := time.Now()
	p := req.payload
	info := DeployInfo{
		ProjectName:    cfg.Name,
		SHA:            p.SHA(),
		Branch:         p.Branch(),
		Author:         p.AuthorName(),
		CommitURL:      p.CommitURL(),
		ProjectURL:     p.ProjectURL(),
		CommitsSummary: p.CommitsSummary(5),
		Steps:          cfg.DeploySteps,
	}

	log.Printf("Executing deploy for %s (SHA: %s)", cfg.Name, info.SHA)
	a.status.setRunning(req.projectKey, info)
	handle := a.notifier.Start(info)

	if err := a.checkoutSHA(info.SHA, cfg.WorkingDir, cfg.SSHKeyPath); err != nil {
		info.Err = err
		info.Duration = time.Since(start)
		handle.Fail(info)
		a.status.setDone(req.projectKey, info)
		return
	}

	for i, step := range cfg.DeploySteps {
		handle.Progress(i, stepRunning)
		if err := runCmd(cfg.WorkingDir, cfg.SSHKeyPath, step.Cmd, step.Args...); err != nil {
			info.Err = err
			info.Duration = time.Since(start)
			handle.Progress(i, stepFailed)
			handle.Fail(info)
			a.status.setDone(req.projectKey, info)
			return
		}
	}

	info.Duration = time.Since(start)
	handle.Progress(len(cfg.DeploySteps), stepDone)
	handle.Success(info)
	a.status.setDone(req.projectKey, info)
	log.Printf("Deploy %s (SHA: %s) done in %s", cfg.Name, info.SHA, info.Duration.Round(time.Second))
}

func (a *App) projectLock(key string) *sync.Mutex {
	v, _ := a.projectLocks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
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

func runCmd(workingDir, sshKeyPath, cmd string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	c := exec.CommandContext(ctx, cmd, args...)
	c.Dir = workingDir

	if sshKeyPath != "" {
		c.Env = append(os.Environ(),
			fmt.Sprintf("GIT_SSH_COMMAND=ssh -i %s -o StrictHostKeyChecking=no", sshKeyPath),
			"DOCKER_BUILDKIT=1",
		)
	}

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	if err := c.Run(); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("%w: %s", err, stderr.String())
		}
		return err
	}
	log.Printf("Executed: %s %v", cmd, args)
	return nil
}

func decodePayload(r *http.Request) (GitLabPushEvent, error) {
	defer r.Body.Close()
	var p GitLabPushEvent
	err := json.NewDecoder(r.Body).Decode(&p)
	return p, err
}
