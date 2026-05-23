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
	Exported   map[string]Record  `json:"exported"`
	Failed     map[string]Failure `json:"failed,omitempty"`
	ScanCursor string             `json:"scan_cursor,omitempty"`
}

type Record struct {
	ExportedAt time.Time  `json:"exported_at"`
	SpanCount  int        `json:"span_count"`
	DeletedAt  *time.Time `json:"deleted_at,omitempty"`
	Deleted    int        `json:"deleted,omitempty"`
}

type Failure struct {
	Attempts     int       `json:"attempts"`
	LastError    string    `json:"last_error"`
	LastFailedAt time.Time `json:"last_failed_at"`
	NextRetryAt  time.Time `json:"next_retry_at"`
	GiveUp       bool      `json:"give_up,omitempty"`
}

func Load(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("state path is required")
	}
	store := &Store{
		path: path,
		Data: Data{Exported: map[string]Record{}, Failed: map[string]Failure{}},
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
	if store.Data.Failed == nil {
		store.Data.Failed = map[string]Failure{}
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
	delete(s.Data.Failed, baseID)
	return s.Save()
}

func (s *Store) CanRetryExport(baseID string, now time.Time) bool {
	if baseID == "" {
		return false
	}
	failure, ok := s.Data.Failed[baseID]
	if !ok {
		return true
	}
	if failure.GiveUp {
		return false
	}
	return !now.Before(failure.NextRetryAt)
}

func (s *Store) MarkExportFailed(baseID string, message string, now time.Time, retryInterval time.Duration, maxAttempts int) error {
	if baseID == "" {
		return errors.New("base_id is required")
	}
	if retryInterval < 0 {
		retryInterval = 0
	}
	failure := s.Data.Failed[baseID]
	failure.Attempts++
	failure.LastError = message
	failure.LastFailedAt = now.UTC()
	failure.NextRetryAt = now.Add(retryInterval).UTC()
	if maxAttempts > 0 && failure.Attempts >= maxAttempts {
		failure.GiveUp = true
	}
	s.Data.Failed[baseID] = failure
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

func (s *Store) SetScanCursor(cursor string) error {
	s.Data.ScanCursor = cursor
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
