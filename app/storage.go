package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// DeployRecord is one completed deploy entry written to the JSONL file.
type DeployRecord struct {
	Project   string    `json:"project"`
	SHA       string    `json:"sha"`
	Branch    string    `json:"branch"`
	Author    string    `json:"author"`
	StartedAt time.Time `json:"started_at"`
	Duration  string    `json:"duration"` // e.g. "1m30s"
	Result    string    `json:"result"`   // "success" or "failed"
	Error     string    `json:"error,omitempty"`
}

// Store persists the last N deploy records per project in an append-only JSONL file.
// On open the file is compacted so it never exceeds maxPerProject lines per project.
type Store struct {
	mu            sync.Mutex
	path          string
	maxPerProject int
	file          *os.File
	history       map[string][]DeployRecord // project name -> last N
}

// OpenStore opens (or creates) the JSONL file and compacts it on startup.
func OpenStore(path string, maxPerProject int) (*Store, error) {
	if maxPerProject <= 0 {
		maxPerProject = 3
	}

	history := make(map[string][]DeployRecord)

	if f, err := os.Open(path); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var r DeployRecord
			if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
				log.Printf("storage: skipping malformed line: %v", err)
				continue
			}
			if r.Project != "" {
				history[r.Project] = append(history[r.Project], r)
			}
		}
		f.Close()
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("storage open: %w", err)
	}

	for name, records := range history {
		if len(records) > maxPerProject {
			history[name] = records[len(records)-maxPerProject:]
		}
	}

	s := &Store{path: path, maxPerProject: maxPerProject, history: history}
	if err := s.compact(); err != nil {
		return nil, fmt.Errorf("storage compact: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("storage open for append: %w", err)
	}
	s.file = f
	return s, nil
}

// Append writes a record to the JSONL file and updates the in-memory index.
func (s *Store) Append(r DeployRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err := s.file.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("storage write: %w", err)
	}

	records := append(s.history[r.Project], r)
	if len(records) > s.maxPerProject {
		records = records[len(records)-s.maxPerProject:]
	}
	s.history[r.Project] = records
	return nil
}

// Recent returns up to maxPerProject records for the given project, oldest first.
func (s *Store) Recent(project string) []DeployRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.history[project]
	if len(src) == 0 {
		return nil
	}
	out := make([]DeployRecord, len(src))
	copy(out, src)
	return out
}

// AllHistory returns a snapshot of every project's history.
func (s *Store) AllHistory() map[string][]DeployRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]DeployRecord, len(s.history))
	for k, v := range s.history {
		cp := make([]DeployRecord, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// compact rewrites the file atomically with only the trimmed history.
// Partial writes from a previous crash are discarded.
func (s *Store) compact() error {
	tmp := s.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(f)
	for _, records := range s.history {
		for _, r := range records {
			if err := enc.Encode(r); err != nil {
				f.Close()
				os.Remove(tmp)
				return err
			}
		}
	}

	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.path)
}

// Close flushes and closes the underlying file.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file != nil {
		return s.file.Close()
	}
	return nil
}
