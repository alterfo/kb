package state

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

type SourceState struct {
	Cursor     string    `json:"cursor"`
	LastSyncAt time.Time `json:"last_sync_at,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
}

type syncStateFile struct {
	Sources map[string]SourceState `json:"sources"`
}

type Store struct {
	mu   sync.Mutex
	path string
	data syncStateFile
}

func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, data: syncStateFile{Sources: map[string]SourceState{}}}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		return nil, err
	}
	if s.data.Sources == nil {
		s.data.Sources = map[string]SourceState{}
	}
	return s, nil
}

func (s *Store) Get(key string) (SourceState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.data.Sources[key]
	return st, ok
}

func (s *Store) Cursor(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Sources[key].Cursor
}

func (s *Store) Advance(key, cursor string, syncedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Sources[key] = SourceState{Cursor: cursor, LastSyncAt: syncedAt}
	return s.persistLocked()
}

func (s *Store) RecordError(key string, syncedAt time.Time, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.data.Sources[key]
	s.data.Sources[key] = SourceState{Cursor: prev.Cursor, LastSyncAt: syncedAt, LastError: errMsg}
	return s.persistLocked()
}

func (s *Store) persistLocked() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(s.path, raw, 0o644)
}
