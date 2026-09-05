package control

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/maheshrijal/mysq/internal/collect"
	"github.com/maheshrijal/mysq/internal/model"
)

func TestKillRejectsUnconfirmedOrIncompleteTargetBeforeConnecting(t *testing.T) {
	q := Queries{Target: collect.Target{DSN: "invalid"}}
	for _, confirmation := range []string{"", "KILL", "kill ", "yes"} {
		if err := q.Kill(context.Background(), Execution{}, confirmation); err == nil || !strings.Contains(err.Error(), "exactly kill") {
			t.Fatalf("confirmation %q: %v", confirmation, err)
		}
	}
	if err := q.Kill(context.Background(), Execution{}, "kill"); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("incomplete target: %v", err)
	}
}

func TestExecutionIdentityRejectsReusedConnectionAndRepeatedDigest(t *testing.T) {
	a := Execution{Process: model.Process{ID: 1, ThreadID: 2, Digest: "digest", Database: "app", User: "user", Host: "host"}, EventID: 3, ServerUUID: "server"}
	if !sameExecution(a, a) {
		t.Fatal("identical execution differs")
	}
	for _, mutate := range []func(*Execution){
		func(e *Execution) { e.ID++ }, func(e *Execution) { e.ThreadID++ }, func(e *Execution) { e.EventID++ },
		func(e *Execution) { e.ServerUUID = "other" }, func(e *Execution) { e.Database = "other" },
		func(e *Execution) { e.Digest = "other" }, func(e *Execution) { e.User = "other" }, func(e *Execution) { e.Host = "other" },
	} {
		b := a
		mutate(&b)
		if sameExecution(a, b) {
			t.Fatalf("accepted changed execution: %+v", b)
		}
	}
}

// Only interrupts SELECT SLEEP statements created by this test in the disposable
// loopback fixture. Never chooses an arbitrary workload process to cancel.
func TestFixtureKillQuery(t *testing.T) {
	loadDSN, monitorDSN, controlDSN := os.Getenv("MYSQ_E2E_LOAD_DSN"), os.Getenv("MYSQ_E2E_MONITOR_DSN"), os.Getenv("MYSQ_E2E_CONTROL_DSN")
	if loadDSN == "" || monitorDSN == "" || controlDSN == "" {
		t.Skip("requires disposable e2e fixture")
	}
	targets := make([]collect.Target, 0, 3)
	for _, dsn := range []string{loadDSN, monitorDSN, controlDSN} {
		target, err := collect.ResolveConnection(dsn)
		if err != nil {
			t.Fatal(err)
		}
		if target.Host != "127.0.0.1" || target.Database != "app" {
			t.Fatal("requires loopback app fixture")
		}
		if len(targets) > 0 && targets[0].Port != target.Port {
			t.Fatal("fixture ports differ")
		}
		targets = append(targets, target)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", targets[0].DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	observer, err := sql.Open("mysql", targets[1].DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	operator, monitor := Queries{Target: targets[2], LiveSQL: true}, Queries{Target: targets[1]}
	defer operator.Close()
	defer monitor.Close()
	newConn := func() (*sql.Conn, uint64) {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		var id uint64
		if err := conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&id); err != nil {
			t.Fatal(err)
		}
		return conn, id
	}
	start := func(conn *sql.Conn, prepared bool) <-chan error {
		queryCtx, stop := context.WithCancel(ctx)
		done, finished := make(chan error, 1), make(chan struct{})
		go func() {
			var value int
			var err error
			if prepared {
				err = conn.QueryRowContext(queryCtx, "SELECT 1 WHERE SLEEP(?)=0", 15).Scan(&value)
			} else {
				err = conn.QueryRowContext(queryCtx, "SELECT 1 WHERE SLEEP(15)=0").Scan(&value)
			}
			done <- err
			close(finished)
		}()
		t.Cleanup(func() { stop(); <-finished })
		return done
	}
	find := func(id uint64) Execution {
		t.Helper()
		for ctx.Err() == nil {
			var digest string
			err := observer.QueryRowContext(ctx, `SELECT COALESCE(es.DIGEST,STATEMENT_DIGEST(t.PROCESSLIST_INFO)) FROM performance_schema.threads t JOIN performance_schema.events_statements_current es ON es.THREAD_ID=t.THREAD_ID WHERE t.PROCESSLIST_ID=? AND es.END_EVENT_ID IS NULL`, id).Scan(&digest)
			if err == nil && digest != "" {
				items, err := operator.Sessions(ctx, "app", digest)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range items {
					if item.ID == id {
						return item
					}
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("test query did not become visible")
		return Execution{}
	}
	assertInterrupted := func(done <-chan error) {
		t.Helper()
		select {
		case err := <-done:
			var mysqlErr *mysql.MySQLError
			if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1317 {
				t.Fatalf("expected query interrupted, got %v", err)
			}
		case <-ctx.Done():
			t.Fatal("query was not interrupted")
		}
	}
	conn, id := newConn()
	other, otherID := newConn()
	done, otherDone := start(conn, false), start(other, false)
	execution, otherExecution := find(id), find(otherID)
	if execution.Digest != otherExecution.Digest || execution.User != "loadgen" || execution.Database != "app" || execution.Host == "" || execution.EventID == 0 {
		t.Fatalf("incorrect live attribution: %+v / %+v", execution, otherExecution)
	}
	if !strings.Contains(execution.LiveStatement, "SLEEP(15)") {
		t.Fatal("TUI live execution lost literals")
	}
	if strings.Contains(execution.Statement, "15") || strings.Contains(execution.Statement, "7") {
		t.Fatal("SQL literals leaked")
	}
	monitorItems, err := monitor.Sessions(ctx, "app", execution.Digest)
	if err != nil {
		t.Fatal(err)
	}
	var monitorExecution Execution
	for _, item := range monitorItems {
		if item.ID == id {
			monitorExecution = item
		}
	}
	if monitorExecution.ID != id {
		t.Fatal("monitor did not find test execution")
	}
	if err := monitor.Kill(ctx, monitorExecution, "kill"); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("monitor cancellation: %v", err)
	}
	// A restart necessarily breaks this observer session. Invalidate the real
	// connection and prove Kill cannot reconnect, even while the target's entire
	// visible identity still matches. A new lookup must not revive the old token.
	if err := operator.conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("invalidate control session: %v", err)
	}
	if err := operator.Kill(ctx, execution, "kill"); err == nil || !strings.Contains(err.Error(), "nothing was sent") {
		t.Fatalf("lost observer allowed cancellation: %v", err)
	}
	select {
	case err := <-done:
		t.Fatalf("target stopped after observer loss: %v", err)
	default:
	}
	freshExecution := find(id)
	if !sameExecution(execution, freshExecution) {
		t.Fatal("test target identity changed unexpectedly")
	}
	if err := operator.Kill(ctx, execution, "kill"); err == nil || !strings.Contains(err.Error(), "connection changed") {
		t.Fatalf("reconnection revived old selection: %v", err)
	}
	execution = freshExecution
	if err := operator.Kill(ctx, execution, "kill"); err != nil {
		t.Fatal(err)
	}
	assertInterrupted(done)
	var sameID uint64
	if err := conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&sameID); err != nil || sameID != id {
		t.Fatalf("connection did not survive: %d %v", sameID, err)
	}
	select {
	case err := <-otherDone:
		t.Fatalf("unselected query stopped: %v", err)
	default:
	}
	if err := operator.Kill(ctx, execution, "kill"); err == nil || !strings.Contains(err.Error(), "ended") {
		t.Fatalf("idle target: %v", err)
	}
	// Same connection and digest, but a different statement event: refuse the old token.
	done = start(conn, true)
	newExecution := find(id)
	if newExecution.EventID == execution.EventID {
		t.Fatal("event identity did not change")
	}
	if newExecution.Command != "Execute" || newExecution.Digest != execution.Digest || newExecution.rawStatement != "" {
		t.Fatalf("prepared execution not identified and redacted: %+v", newExecution)
	}
	if err := operator.Kill(ctx, execution, "kill"); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale target: %v", err)
	}
	if err := operator.Kill(ctx, newExecution, "kill"); err != nil {
		t.Fatal(err)
	}
	assertInterrupted(done)
	if err := operator.Kill(ctx, find(otherID), "kill"); err != nil {
		t.Fatal(err)
	}
	assertInterrupted(otherDone)
}
