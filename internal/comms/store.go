package comms

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// StateStore persists the dashboard message IDs across keel restarts so a
// dashboard edits its existing Discord message instead of posting a new one.
type StateStore struct {
	path string
	mu   sync.Mutex
	data stateData
}

type stateData struct {
	Messages map[string]string `json:"messages"`
}

// OpenStateStore loads the state file at path. A missing file returns an empty
// store ready for writes (it will create the file on first Set).
func OpenStateStore(path string) (*StateStore, error) {
	s := &StateStore{path: path, data: stateData{Messages: map[string]string{}}}

	buf, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read state: %w", err)
	}
	if len(buf) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(buf, &s.data); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	if s.data.Messages == nil {
		s.data.Messages = map[string]string{}
	}
	return s, nil
}

func (s *StateStore) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data.Messages[key]
	return v, ok
}

func (s *StateStore) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Messages[key] = value
	return s.writeLocked()
}

func (s *StateStore) writeLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return fmt.Errorf("mkdir state: %w", err)
	}
	buf, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0644); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return os.Rename(tmp, s.path)
}
