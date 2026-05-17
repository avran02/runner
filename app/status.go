package app

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

type deployState string

const (
	stateIdle    deployState = "idle"
	statePending deployState = "pending"
	stateRunning deployState = "running"
)

type currentDeploy struct {
	name      string
	state     deployState
	sha       string
	branch    string
	author    string
	startedAt time.Time
}

// ProjectStatus is what the /status endpoint returns for one project.
type ProjectStatus struct {
	Name      string         `json:"name"`
	State     deployState    `json:"state"`
	StartedAt *time.Time     `json:"started_at,omitempty"` // present only while running
	History   []DeployRecord `json:"history"`              // oldest first
}

type statusStore struct {
	mu      sync.RWMutex
	current map[string]*currentDeploy // projectKey (secret) -> ephemeral state
	store   *Store
}

func newStatusStore(store *Store) *statusStore {
	return &statusStore{
		current: make(map[string]*currentDeploy),
		store:   store,
	}
}

func (s *statusStore) setPending(key, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getOrCreate(key, name).state = statePending
}

func (s *statusStore) setRunning(key string, info DeployInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cd := s.getOrCreate(key, info.ProjectName)
	cd.state = stateRunning
	cd.sha = info.SHA
	cd.branch = info.Branch
	cd.author = info.Author
	cd.startedAt = time.Now()
}

func (s *statusStore) setDone(key string, info DeployInfo) {
	s.mu.Lock()
	cd := s.getOrCreate(key, info.ProjectName)
	startedAt := cd.startedAt
	cd.state = stateIdle
	s.mu.Unlock()

	r := DeployRecord{
		Project:   info.ProjectName,
		SHA:       info.SHA,
		Branch:    info.Branch,
		Author:    info.Author,
		StartedAt: startedAt,
		Duration:  info.Duration.Round(time.Second).String(),
		Result:    "success",
	}
	if info.Err != nil {
		r.Result = "failed"
		r.Error = truncate(info.Err.Error(), 200)
	}
	if err := s.store.Append(r); err != nil {
		log.Printf("storage append error: %v", err)
	}
}

func (s *statusStore) getOrCreate(key, name string) *currentDeploy {
	if s.current[key] == nil {
		s.current[key] = &currentDeploy{name: name, state: stateIdle}
	}
	return s.current[key]
}

// list builds a ProjectStatus for every known project, combining persistent
// history with the current ephemeral state.
func (s *statusStore) list() []ProjectStatus {
	allHistory := s.store.AllHistory()

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Index current deploys by project name for quick lookup.
	byName := make(map[string]*currentDeploy, len(s.current))
	for _, cd := range s.current {
		byName[cd.name] = cd
	}

	seen := make(map[string]bool, len(allHistory))
	out := make([]ProjectStatus, 0, len(allHistory)+len(s.current))

	for name, records := range allHistory {
		seen[name] = true
		out = append(out, s.buildStatus(name, byName[name], records))
	}

	// Projects currently in-flight with no persisted history yet.
	for _, cd := range s.current {
		if !seen[cd.name] {
			out = append(out, s.buildStatus(cd.name, cd, nil))
		}
	}

	return out
}

func (s *statusStore) getByName(name string) (ProjectStatus, bool) {
	history := s.store.Recent(name)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var cd *currentDeploy
	for _, v := range s.current {
		if v.name == name {
			cd = v
			break
		}
	}

	if cd == nil && len(history) == 0 {
		return ProjectStatus{}, false
	}
	return s.buildStatus(name, cd, history), true
}

func (s *statusStore) buildStatus(name string, cd *currentDeploy, history []DeployRecord) ProjectStatus {
	ps := ProjectStatus{
		Name:    name,
		State:   stateIdle,
		History: history,
	}
	if cd != nil {
		ps.State = cd.state
		if cd.state == stateRunning {
			t := cd.startedAt
			ps.StartedAt = &t
		}
	}
	return ps
}

// StatusHandler serves deploy status as indented JSON.
//
//	GET /status           → all projects
//	GET /status?name=X    → one project by name
func (a *App) StatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	if name := r.URL.Query().Get("name"); name != "" {
		ps, ok := a.status.getByName(name)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			enc.Encode(map[string]string{"error": "project not found"})
			return
		}
		enc.Encode(ps)
		return
	}
	enc.Encode(a.status.list())
}
