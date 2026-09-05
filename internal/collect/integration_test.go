package collect

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"sync"

	"github.com/maheshrijal/mysq/internal/model"
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

// A busy pool must neither hide executing sessions behind idle ones nor limit
// digest attribution to the first 100 executions shown in Connections.
func TestFixtureActiveUsersBeyondSessionLimit(t *testing.T) {
	loadDSN, monitorDSN := os.Getenv("MYSQ_E2E_LOAD_DSN"), os.Getenv("MYSQ_E2E_MONITOR_DSN")
	if loadDSN == "" || monitorDSN == "" {
		t.Skip("requires disposable e2e fixture")
	}
	for _, dsn := range []string{loadDSN, monitorDSN} {
		target, err := ResolveConnection(dsn)
		if err != nil || target.Host != "127.0.0.1" || target.Database != "app" {
			t.Fatal("integration test requires loopback app fixture")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	load, err := sql.Open("mysql", loadDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer load.Close()
	load.SetMaxOpenConns(110)
	load.SetMaxIdleConns(0)
	monitor, err := sql.Open("mysql", monitorDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer monitor.Close()
	monitor.SetMaxIdleConns(0)
	conn, err := monitor.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	const statement = "SELECT SLEEP(60)"
	var digest, monitorUser string
	if err := conn.QueryRowContext(ctx, "SELECT STATEMENT_DIGEST(?), SUBSTRING_INDEX(CURRENT_USER(),'@',1)", statement).Scan(&digest, &monitorUser); err != nil {
		t.Fatal(err)
	}
	collector := New("integration")
	for _, scenario := range []string{"idle pool", "more than 100 executions"} {
		t.Run(scenario, func(t *testing.T) {
			sampleCtx, stop := context.WithCancel(ctx)
			var running sync.WaitGroup
			defer func() { stop(); running.Wait() }()
			start := func(db *sql.DB) uint64 {
				t.Helper()
				c, err := db.Conn(sampleCtx)
				if err != nil {
					t.Fatal(err)
				}
				var id uint64
				if err := c.QueryRowContext(sampleCtx, "SELECT CONNECTION_ID()").Scan(&id); err != nil {
					c.Close()
					t.Fatal(err)
				}
				running.Add(1)
				go func() {
					defer running.Done()
					defer c.Close()
					_, _ = c.ExecContext(sampleCtx, statement)
				}()
				return id
			}
			waitActive := func(id uint64) {
				t.Helper()
				for {
					var active int
					if err := conn.QueryRowContext(sampleCtx, `SELECT COUNT(*) FROM performance_schema.threads t
 JOIN performance_schema.events_statements_current es ON es.THREAD_ID=t.THREAD_ID
 WHERE t.PROCESSLIST_ID=? AND t.PROCESSLIST_COMMAND='Query' AND es.DIGEST=? AND es.END_EVENT_ID IS NULL`, id, digest).Scan(&active); err != nil {
						t.Fatal(err)
					}
					if active == 1 {
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			var selectedID uint64
			if scenario == "idle pool" {
				for i := 0; i < 105; i++ {
					idle, err := load.Conn(sampleCtx)
					if err != nil {
						t.Fatal(err)
					}
					defer idle.Close()
				}
				// PROCESSLIST_TIME has whole-second resolution.
				time.Sleep(1100 * time.Millisecond)
				selectedID = start(load)
			} else {
				for i := 0; i < 101; i++ {
					waitActive(start(load))
				}
				// This user's execution falls beyond the display cap, even when
				// every displayed row is active.
				selectedID = start(monitor)
			}
			waitActive(selectedID)
			processes, err := collector.collectProcesses(sampleCtx, conn)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, p := range processes {
				found = found || p.ID == selectedID
			}
			if len(processes) != 100 || found != (scenario == "idle pool") {
				t.Fatalf("unexpected process sample: rows=%d selected visible=%t", len(processes), found)
			}
			queries := []model.Query{{Schema: "app", Digest: digest}, {Schema: "other_schema", Digest: digest}}
			if err := collector.collectActiveQueryUsers(sampleCtx, conn, queries); err != nil {
				t.Fatal(err)
			}
			want := "loadgen"
			if scenario != "idle pool" {
				want += "," + monitorUser
			}
			if got := strings.Join(queries[0].ActiveUsers, ","); got != want {
				t.Fatalf("active users = %q, want %q", got, want)
			}
			if len(queries[1].ActiveUsers) != 0 {
				t.Fatalf("users leaked across schemas: %v", queries[1].ActiveUsers)
			}
		})
	}
}
