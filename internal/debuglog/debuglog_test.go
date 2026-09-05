package debuglog

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentSpansAndPrivateErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.jsonl")
	ctx, closeLog, err := Open(context.Background(), path, "test")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { defer Start(ctx, "probe")() })
	}
	wg.Wait()
	Result(ctx, "probe", errors.New("password=do-not-log SELECT secret"))
	Result(ctx, "probe", context.DeadlineExceeded)
	late := Start(ctx, "pending")
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}
	late() // Async TUI work may finish after shutdown.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "password") {
		t.Fatal("raw error leaked")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
	}
	starts, ends := map[uint64]bool{}, map[uint64]bool{}
	statuses := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if e.Time.IsZero() {
			t.Fatal("missing timestamp")
		}
		if e.Operation == "probe" {
			switch e.Phase {
			case "start":
				starts[e.ID] = true
			case "end":
				ends[e.ID] = true
				if e.DurationMS < 0 {
					t.Fatal("negative duration")
				}
			case "result":
				statuses[e.Status] = true
			}
		}
	}
	if len(starts) != 20 || len(ends) != 20 {
		t.Fatalf("unpaired spans: %v / %v", starts, ends)
	}
	for id := range starts {
		if !ends[id] {
			t.Fatalf("missing end %d", id)
		}
	}
	if !statuses["error"] || !statuses["timeout"] {
		t.Fatalf("statuses = %v", statuses)
	}
	if _, _, err := Open(ctx, path, "test"); err == nil {
		t.Fatal("overwrote existing log")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(ctx, link, "test"); err == nil {
		t.Fatal("followed symlink")
	}
}

func TestDisabled(t *testing.T) {
	Start(context.Background(), "disabled")()
	Start(nil, "disabled")()
	Result(context.Background(), "disabled", errors.New("secret"))
}
