package state

import (
	"path/filepath"
	"testing"
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
