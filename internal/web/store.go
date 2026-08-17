package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Connection is an OpenList server entry.
type Connection struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	BaseURL      string `json:"base_url"`
	AuthType     string `json:"auth_type"` // token | password
	Token        string `json:"token"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	DownloadMode string `json:"download_mode"` // direct | proxy
}

// Task is a sync job bound to a connection.
type Task struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	ConnectionID string    `json:"connection_id"`
	RemotePath   string    `json:"remote_path"`
	LocalDir     string    `json:"local_dir"`
	Direction    string    `json:"direction"` // both | pull | push
	Cleanup      string    `json:"cleanup"`   // none | local | remote | both
	Conflict     string    `json:"conflict"`  // newest | remote | local | skip
	IncludeExt   []string  `json:"include_ext"`
	ExcludeExt   []string  `json:"exclude_ext"`
	Types        []string  `json:"types"`
	Enabled      bool      `json:"enabled"`
	Interval     string    `json:"interval"` // override, empty => global
	RateLimit    int64     `json:"rate_limit"`
	LastRun      time.Time `json:"last_run"`
	LastStatus   string    `json:"last_status"` // ok | error | running | null
	LastError    string    `json:"last_error"`
	LastDetail   string    `json:"last_detail"`
}

// Settings are global defaults.
type Settings struct {
	Interval    string `json:"interval"`
	Concurrency int    `json:"concurrency"`
	RateLimit   int64  `json:"rate_limit"`
	Retries     int    `json:"retries"`
}

// State is the full persisted web configuration.
type State struct {
	Version     int           `json:"version"`
	Connections []*Connection `json:"connections"`
	Tasks       []*Task       `json:"tasks"`
	Settings    Settings      `json:"settings"`
}

func defaultState() State {
	return State{
		Version:     1,
		Settings:    Settings{Interval: "1h", Concurrency: 4, RateLimit: 0, Retries: 3},
		Connections: []*Connection{},
		Tasks:       []*Task{},
	}
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// Store persists State to a JSON file with a mutex.
type Store struct {
	mu    sync.RWMutex
	path  string
	state State
}

func LoadStore(path string) (*Store, error) {
	s := &Store{path: path}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &s.state); err != nil {
			return nil, fmt.Errorf("store %s: %w", path, err)
		}
	}
	if s.state.Version == 0 {
		s.state = defaultState()
	}
	if s.state.Settings.Interval == "" {
		s.state.Settings.Interval = "1h"
	}
	if s.state.Settings.Concurrency == 0 {
		s.state.Settings.Concurrency = 4
	}
	if s.state.Settings.Retries == 0 {
		s.state.Settings.Retries = 3
	}
	return s, nil
}

func (s *Store) Save() error {
	s.mu.RLock()
	data, err := json.MarshalIndent(&s.state, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	dir := dirOf(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			if i == 0 {
				return "/"
			}
			return p[:i]
		}
	}
	return "."
}

func (s *Store) Authorized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return true
}

func (s *Store) Connection(id string) *Connection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.state.Connections {
		if c.ID == id {
			return c
		}
	}
	return nil
}

func (s *Store) Task(id string) *Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.state.Tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func (s *Store) UpsertConnection(c *Connection) {
	if c.ID == "" {
		c.ID = newID()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, o := range s.state.Connections {
		if o.ID == c.ID {
			s.state.Connections[i] = c
			return
		}
	}
	s.state.Connections = append(s.state.Connections, c)
}

func (s *Store) DeleteConnection(id string) (deleted bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var kept []*Connection
	for _, c := range s.state.Connections {
		if c.ID != id {
			kept = append(kept, c)
		}
	}
	deleted = len(kept) != len(s.state.Connections)
	s.state.Connections = kept
	// drop tasks referencing the removed connection
	var tasks []*Task
	for _, t := range s.state.Tasks {
		if t.ConnectionID != id {
			tasks = append(tasks, t)
		}
	}
	s.state.Tasks = tasks
	return
}

func (s *Store) UpsertTask(t *Task) {
	if t.ID == "" {
		t.ID = newID()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, o := range s.state.Tasks {
		if o.ID == t.ID {
			s.state.Tasks[i] = t
			return
		}
	}
	s.state.Tasks = append(s.state.Tasks, t)
}

func (s *Store) DeleteTask(id string) (deleted bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var kept []*Task
	for _, t := range s.state.Tasks {
		if t.ID != id {
			kept = append(kept, t)
		}
	}
	deleted = len(kept) != len(s.state.Tasks)
	s.state.Tasks = kept
	return
}

func (s *Store) SetSettings(st Settings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Settings = st
}

// Snapshot returns a deep copy for API responses.
func (s *Store) Snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, _ := json.Marshal(&s.state)
	var out State
	_ = json.Unmarshal(data, &out)
	if out.Connections == nil {
		out.Connections = []*Connection{}
	}
	if out.Tasks == nil {
		out.Tasks = []*Task{}
	}
	return out
}

func (s *Store) UpdateTaskStatus(id, status, errMsg, detail string, t time.Time) *Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, tsk := range s.state.Tasks {
		if tsk.ID == id {
			tsk.LastStatus = status
			tsk.LastError = errMsg
			tsk.LastDetail = detail
			tsk.LastRun = t
			return tsk
		}
	}
	return nil
}