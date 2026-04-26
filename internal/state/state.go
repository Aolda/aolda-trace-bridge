package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Store struct {
	path string
	Data Data
}

type Data struct {
	Exported map[string]Record `json:"exported"`
}

type Record struct {
	ExportedAt time.Time  `json:"exported_at"`
	SpanCount  int        `json:"span_count"`
	DeletedAt  *time.Time `json:"deleted_at,omitempty"`
	Deleted    int        `json:"deleted,omitempty"`
}

func Load(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("state path is required")
	}
	store := &Store{
		path: path,
		Data: Data{Exported: map[string]Record{}},
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	if len(data) == 0 {
		return store, nil
	}
	if err := json.Unmarshal(data, &store.Data); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	if store.Data.Exported == nil {
		store.Data.Exported = map[string]Record{}
	}
	return store, nil
}

func (s *Store) IsExported(baseID string) bool {
	_, ok := s.Data.Exported[baseID]
	return ok
}

func (s *Store) IsDeleted(baseID string) bool {
	record, ok := s.Data.Exported[baseID]
	return ok && record.DeletedAt != nil && !record.DeletedAt.IsZero()
}

func (s *Store) MarkExported(baseID string, spanCount int) error {
	if baseID == "" {
		return errors.New("base_id is required")
	}
	record := s.Data.Exported[baseID]
	record.ExportedAt = time.Now().UTC()
	record.SpanCount = spanCount
	s.Data.Exported[baseID] = record
	return s.Save()
}

func (s *Store) MarkDeleted(baseID string, deleted int) error {
	if baseID == "" {
		return errors.New("base_id is required")
	}
	record := s.Data.Exported[baseID]
	if record.ExportedAt.IsZero() {
		record.ExportedAt = time.Now().UTC()
	}
	now := time.Now().UTC()
	record.DeletedAt = &now
	record.Deleted = deleted
	s.Data.Exported[baseID] = record
	return s.Save()
}

func (s *Store) Save() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	data, err := json.MarshalIndent(s.Data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create state temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write state temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close state temp file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	return nil
}
