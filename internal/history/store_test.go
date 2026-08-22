package history

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maheshrijal/mysq/internal/model"
)

func TestSaveLatestPairAndList(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "history"))
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 22, 1, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		ctx := &model.Context{Fingerprint: "abc", CollectedAt: base.Add(time.Duration(i) * time.Hour), Server: model.Server{Host: "db", Port: 3306, Database: "app"}}
		if _, err := store.Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := store.Latest("abc")
	if err != nil || !latest.CollectedAt.Equal(base.Add(2*time.Hour)) {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
	previous, current, err := store.Pair("abc", 90*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if previous == nil || current == nil || !previous.CollectedAt.Equal(base) {
		t.Fatalf("unexpected pair previous=%+v current=%+v", previous, current)
	}
	items, err := store.List()
	if err != nil || len(items) != 1 || items[0].Count != 3 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestOpenMigratesLegacyDefaultStore(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	legacySnapshots := filepath.Join(stateHome, "mysqldot", "snapshots")
	if err := os.MkdirAll(legacySnapshots, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(legacySnapshots, "marker")
	if err := os.WriteFile(marker, []byte("snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(stateHome, "mysq", "snapshots")
	if store.Root != wantRoot {
		t.Fatalf("root = %q, want %q", store.Root, wantRoot)
	}
	if _, err := os.Stat(filepath.Join(wantRoot, "marker")); err != nil {
		t.Fatalf("legacy snapshot was not migrated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateHome, "mysqldot")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy state directory still exists: %v", err)
	}
}
