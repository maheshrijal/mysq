package collect

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/maheshrijal/mysq/internal/model"
)

func parallelFixture(t *testing.T) (context.Context, *Collector, Target, *sql.Conn) {
	t.Helper()
	dsn := os.Getenv("MYSQ_E2E_MONITOR_DSN")
	if dsn == "" {
		t.Skip("requires disposable e2e fixture")
	}
	target, err := ResolveConnection(dsn)
	if err != nil || target.Host != "127.0.0.1" || target.Database != "app" {
		t.Fatal("requires loopback app fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	c := New("integration")
	db, conn, err := c.openConnection(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); db.Close() })
	return ctx, c, target, conn
}

func TestFixtureParallelProbes(t *testing.T) {
	ctx, c, target, conn := parallelFixture(t)
	var server model.Server
	if err := c.collectServer(ctx, conn, &server); err != nil {
		t.Fatal(err)
	}
	pool := &probePool{collector: c, target: target, server: server}
	defer pool.close()
	ids := make(chan uint64, optionalWorkers)
	gate := make(chan struct{})
	tasks := make([]probeTask, optionalWorkers)
	for i := range tasks {
		tasks[i] = probeTask{name: "safeguards", run: func(ctx context.Context, conn *sql.Conn) error {
			var id uint64
			var readOnly, limit int
			if err := conn.QueryRowContext(ctx, "SELECT CONNECTION_ID(), @@session.transaction_read_only, @@session.max_execution_time").Scan(&id, &readOnly, &limit); err != nil {
				return err
			}
			if readOnly != 1 || limit != int(optionalProbeTimeout.Milliseconds()) {
				return errors.New("worker lacks session safeguards")
			}
			ids <- id
			select {
			case <-gate:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}
	}
	done := make(chan error, 1)
	go func() { errs, err := pool.run(ctx, tasks); done <- errors.Join(append(errs, err)...) }()
	unique := map[uint64]bool{}
	for range optionalWorkers {
		select {
		case id := <-ids:
			unique[id] = true
		case <-ctx.Done():
			t.Fatal("workers did not overlap")
		}
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(unique) != optionalWorkers {
		t.Fatalf("worker sessions = %v", unique)
	}
	// Cancel a real in-flight read, then verify the next task can reconnect.
	short, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	_, err := pool.run(short, []probeTask{{name: "cancel", run: func(ctx context.Context, conn *sql.Conn) error {
		var value int
		return conn.QueryRowContext(ctx, "SELECT SLEEP(0.2)").Scan(&value)
	}}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation = %v", err)
	}
	errs, err := pool.run(ctx, tasks[:1])
	if err != nil || errs[0] != nil {
		t.Fatalf("replacement failed: %v / %v", err, errs)
	}
	pool.close()
	pool.server.UUID = "different server"
	called := false
	_, err = pool.run(ctx, []probeTask{{name: "identity", run: func(context.Context, *sql.Conn) error { called = true; return nil }}})
	if !errors.Is(err, errDifferentServer) || called {
		t.Fatalf("mismatched server accepted: %v / called=%t", err, called)
	}
	pool.close()
	c.Interval = 100 * time.Millisecond
	snapshot, err := c.Inspect(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Capabilities) != 19 {
		t.Fatalf("lost capabilities: %d", len(snapshot.Capabilities))
	}
	for _, capability := range snapshot.Capabilities {
		if !capability.Available {
			t.Fatalf("fixture lost %s: %s", capability.Name, capability.Reason)
		}
	}
	windows := snapshot.SampleIntervals
	for _, ms := range []int64{windows.GlobalStatus, windows.WaitEvents, windows.FileIO, windows.ServerErrors, windows.StatementDigests, windows.StatementCounters} {
		if ms < 100 {
			t.Fatalf("sample window shortened: %+v", windows)
		}
	}

}

func TestFixtureNullErrorNumber(t *testing.T) {
	ctx, _, _, conn := parallelFixture(t)
	rows, err := conn.QueryContext(ctx, `SELECT NULL AS ERROR_NUMBER, '' AS ERROR_NAME, '' AS SQL_STATE,
		7 AS SUM_ERROR_RAISED, 2 AS SUM_ERROR_HANDLED, '' AS FIRST_SEEN, '' AS LAST_SEEN
		UNION ALL SELECT 1146, 'ER_NO_SUCH_TABLE', '42S02', 3, 0, '', ''`)
	if err != nil {
		t.Fatal(err)
	}
	counters, err := readErrorCounters(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(counters) != 2 || counters[0].Raised != 7 || counters[0].Handled != 2 || counters[1146].Raised != 3 {
		t.Fatalf("lost error buckets: %+v", counters)
	}
}
