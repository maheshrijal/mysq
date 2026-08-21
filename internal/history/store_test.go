package history

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/maheshrijal/mysqldot/internal/model"
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
