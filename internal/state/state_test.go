package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreMarkAndLoadExported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	store, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if store.IsExported("base-1") {
		t.Fatal("base-1 should not be exported yet")
	}
	if store.IsDeleted("base-1") {
		t.Fatal("base-1 should not be deleted yet")
	}
	if err := store.MarkExported("base-1", 12); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeleted("base-1", 2); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.IsExported("base-1") {
		t.Fatal("base-1 should be exported after reload")
	}
	if !loaded.IsDeleted("base-1") {
		t.Fatal("base-1 should be deleted after reload")
	}
	if got := loaded.Data.Exported["base-1"].SpanCount; got != 12 {
		t.Fatalf("span count = %d, want 12", got)
	}
	if got := loaded.Data.Exported["base-1"].Deleted; got != 2 {
		t.Fatalf("deleted = %d, want 2", got)
	}
}

func TestStorePersistsScanCursor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	store, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetScanCursor("42"); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Data.ScanCursor != "42" {
		t.Fatalf("scan cursor = %q, want 42", loaded.Data.ScanCursor)
	}
}

func TestStoreMarksFailedExportAndBacksOffRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	store, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	if err := store.MarkExportFailed("base-1", "helper get_report timed out", now, 30*time.Minute, 3); err != nil {
		t.Fatal(err)
	}
	if store.CanRetryExport("base-1", now.Add(10*time.Minute)) {
		t.Fatal("base-1 should be in retry backoff")
	}
	if !store.CanRetryExport("base-1", now.Add(31*time.Minute)) {
		t.Fatal("base-1 should be retryable after backoff")
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	failed := loaded.Data.Failed["base-1"]
	if failed.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", failed.Attempts)
	}
	if failed.LastError != "helper get_report timed out" {
		t.Fatalf("last error = %q", failed.LastError)
	}
}
