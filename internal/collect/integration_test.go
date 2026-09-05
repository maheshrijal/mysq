package collect

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

// run.sh supplies credentials for its disposable loopback-only MySQL fixture.
// No application rows are changed: an idle read transaction blocks LOCK TABLES.
func TestFixtureSleepingMetadataOwner(t *testing.T) {
	loadDSN, monitorDSN := os.Getenv("MYSQ_E2E_LOAD_DSN"), os.Getenv("MYSQ_E2E_MONITOR_DSN")
	if loadDSN == "" || monitorDSN == "" {
		t.Skip("requires disposable e2e fixture")
	}
	target, err := ResolveConnection(monitorDSN)
	if err != nil {
		t.Fatal(err)
	}
	if target.Host != "127.0.0.1" || target.Database != "app" {
		t.Fatal("integration test requires loopback app fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", loadDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	owner, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	var ownerID uint64
	if err := owner.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ExecContext(ctx, "START TRANSACTION"); err != nil {
		t.Fatal(err)
	}
	defer owner.ExecContext(context.Background(), "ROLLBACK")
	var id uint64
	if err := owner.QueryRowContext(ctx, "SELECT id FROM app.accounts LIMIT 1").Scan(&id); err != nil {
		t.Fatal(err)
	}
	waiter, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer waiter.Close()
	waitCtx, stopWait := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, err := waiter.ExecContext(waitCtx, "LOCK TABLES app.accounts WRITE"); done <- err }()
	defer func() { stopWait(); <-done }()
	collector := New("integration")
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		evidence, err := collector.InspectSection(ctx, target, "blockers")
		if err != nil {
			t.Fatal(err)
		}
		pending, granted := false, false
		for _, lock := range evidence.MetadataLocks {
			if lock.Schema != "app" || lock.Object != "accounts" {
				continue
			}
			pending = pending || lock.Status == "PENDING"
			granted = granted || (lock.Status == "GRANTED" && lock.ProcessID == ownerID)
		}
		if pending && granted {
			processes, err := collector.InspectSection(ctx, target, "processes")
			if err != nil {
				t.Fatal(err)
			}
			for _, process := range processes.Processes {
				if process.ID == ownerID {
					if process.Command != "Sleep" || process.Statement != "" {
						t.Fatalf("sleeping owner has completed SQL attributed as current: %+v", process)
					}
					return
				}
			}
			t.Fatal("sleeping owner absent from process capture")
		}
		select {
		case <-ctx.Done():
			t.Fatal("pending lock and sleeping owner were not captured together")
		case <-ticker.C:
		}
	}
}
