package state

import (
	"encoding/json"
	"os"
	"sync"
)

type tombstoneFile struct {
	Sources map[string]map[string]bool `json:"sources"`
}

type TombstoneStore struct {
	mu   sync.Mutex
	path string
	data tombstoneFile
}

func OpenTombstoneStore(path string) (*TombstoneStore, error) {
	s := &TombstoneStore{path: path, data: tombstoneFile{Sources: map[string]map[string]bool{}}}
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
		s.data.Sources = map[string]map[string]bool{}
	}
	return s, nil
}

func (s *TombstoneStore) Add(sourceKey, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Sources[sourceKey] == nil {
		s.data.Sources[sourceKey] = map[string]bool{}
	}
	s.data.Sources[sourceKey][id] = true
	return s.persistLocked()
}

func (s *TombstoneStore) Contains(sourceKey, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Sources[sourceKey][id]
}

func (s *TombstoneStore) Remove(sourceKey, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Sources[sourceKey] == nil {
		return nil
	}
	delete(s.data.Sources[sourceKey], id)
	return s.persistLocked()
}

func (s *TombstoneStore) persistLocked() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(s.path, raw, 0o644)
}
