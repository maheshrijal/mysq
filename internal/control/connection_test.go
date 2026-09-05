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

	"github.com/maheshrijal/mysq/internal/collect"
	"github.com/maheshrijal/mysq/internal/model"
)

func TestConnectionIdentityAndConfirmation(t *testing.T) {
	q := Queries{Target: collect.Target{DSN: "invalid"}}
	c := Connection{Process: model.Process{ID: 1, ThreadID: 2, User: "user", Host: "host"}, ServerUUID: "server"}
	for _, confirmation := range []string{"", "KILL", "kill ", "yes"} {
		if err := q.KillConnection(context.Background(), c, confirmation); err == nil || !strings.Contains(err.Error(), "exactly kill") {
			t.Fatal(confirmation, err)
		}
	}
	if err := q.KillConnection(context.Background(), Connection{}, "kill"); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatal(err)
	}
	if _, err := q.Connection(context.Background(), "", c.Process); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatal(err)
	}
	if err := q.KillConnection(context.Background(), c, "kill"); err == nil || !strings.Contains(err.Error(), "nothing was sent") {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Connection){
		func(c *Connection) { c.ID++ }, func(c *Connection) { c.ThreadID++ }, func(c *Connection) { c.ServerUUID = "other" }, func(c *Connection) { c.User = "other" }, func(c *Connection) { c.Host = "other" },
	} {
		changed := c
		mutate(&changed)
		if sameConnection(c, changed) {
			t.Fatal("accepted changed identity", changed)
		}
	}
	changed := c
	changed.Command = "Sleep"
	changed.Database = "other"
	changed.Statement = "SELECT ?"
	changed.Digest = "other"
	if !sameConnection(c, changed) {
		t.Fatal("statement changes must not invalidate connection identity")
	}
}

// Every terminated session is created by this test in the disposable loopback
// fixture. No arbitrary process list candidate is used as a kill target.
func TestFixtureKillConnection(t *testing.T) {
	var targets []collect.Target
	for _, name := range []string{"MYSQ_E2E_LOAD_DSN", "MYSQ_E2E_MONITOR_DSN", "MYSQ_E2E_CONTROL_DSN"} {
		dsn := os.Getenv(name)
		if dsn == "" {
			t.Skip("requires disposable e2e fixture")
		}
		target, err := collect.ResolveConnection(dsn)
		if err != nil {
			t.Fatal(err)
		}
		if target.Host != "127.0.0.1" || target.Database != "app" || (len(targets) > 0 && targets[0].Port != target.Port) {
			t.Fatal("requires same loopback app fixture")
		}
		targets = append(targets, target)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
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
	newSession := func() (*sql.Conn, model.Process, string) {
		t.Helper()
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		var p model.Process
		var uuid string
		if err := c.QueryRowContext(ctx, "SELECT CONNECTION_ID(),@@server_uuid").Scan(&p.ID, &uuid); err != nil {
			t.Fatal(err)
		}
		if err := observer.QueryRowContext(ctx, `SELECT THREAD_ID,PROCESSLIST_USER,PROCESSLIST_HOST FROM performance_schema.threads WHERE PROCESSLIST_ID=?`, p.ID).Scan(&p.ThreadID, &p.User, &p.Host); err != nil {
			t.Fatal(err)
		}
		return c, p, uuid
	}
	lookup := func(p model.Process, uuid string) Connection {
		t.Helper()
		c, err := operator.Connection(ctx, uuid, p)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	assertAlive := func(c *sql.Conn) {
		t.Helper()
		var n int
		if err := c.QueryRowContext(ctx, "SELECT 1").Scan(&n); err != nil {
			t.Fatal("unselected session stopped", err)
		}
	}
	waitGone := func(id uint64) {
		t.Helper()
		for ctx.Err() == nil {
			var n int
			if err := observer.QueryRowContext(ctx, "SELECT COUNT(*) FROM performance_schema.threads WHERE PROCESSLIST_ID=?", id).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("connection did not end")
	}
	idle, p, uuid := newSession()
	other, _, _ := newSession()
	target := lookup(p, uuid)
	if target.Command != "Sleep" || target.Statement != "" {
		t.Fatal("idle target", target)
	}
	denied, err := monitor.Connection(ctx, uuid, p)
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.KillConnection(ctx, denied, "kill"); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatal("monitor kill", err)
	}
	assertAlive(idle)
	for _, mutate := range []func(*Connection){func(c *Connection) { c.ThreadID++ }, func(c *Connection) { c.ServerUUID = "wrong" }, func(c *Connection) { c.User = "wrong" }, func(c *Connection) { c.Host = "wrong" }} {
		wrong := target
		mutate(&wrong)
		if err := operator.KillConnection(ctx, wrong, "kill"); err == nil || !strings.Contains(err.Error(), "nothing was sent") {
			t.Fatal("changed target", err)
		}
	}
	if _, err := operator.Connection(ctx, "wrong", p); err == nil {
		t.Fatal("snapshot server mismatch accepted")
	}
	if err := operator.conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	if err := operator.KillConnection(ctx, target, "kill"); err == nil || !strings.Contains(err.Error(), "nothing was sent") {
		t.Fatal("lost observer", err)
	}
	fresh := lookup(p, uuid)
	if err := operator.KillConnection(ctx, target, "kill"); err == nil || !strings.Contains(err.Error(), "connection changed") {
		t.Fatal("reconnection revived target", err)
	}
	if err := operator.KillConnection(ctx, fresh, "kill"); err != nil {
		t.Fatal(err)
	}
	waitGone(p.ID)
	var n int
	if err := idle.QueryRowContext(ctx, "SELECT 1").Scan(&n); err == nil {
		t.Fatal("killed connection survived")
	}
	assertAlive(other)
	if err := operator.KillConnection(ctx, fresh, "kill"); err == nil || !strings.Contains(err.Error(), "ended") {
		t.Fatal("ended target", err)
	}
	// The token targets the connection even after it starts a different query.
	running, rp, ruuid := newSession()
	runningTarget := lookup(rp, ruuid)
	queryCtx, stopQuery := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { var n int; done <- running.QueryRowContext(queryCtx, "SELECT 1 WHERE SLEEP(20)=0").Scan(&n) }()
	t.Cleanup(func() { stopQuery() })
	for ctx.Err() == nil {
		current := lookup(rp, ruuid)
		if current.Command == "Query" {
			if !strings.Contains(current.LiveStatement, "SLEEP(20)") {
				t.Fatal("TUI live connection lost literals")
			}
			if strings.Contains(current.Statement, "20") {
				t.Fatal("SQL literal leaked")
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := operator.KillConnection(ctx, runningTarget, "kill"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("query completed instead of interrupted")
		}
	case <-ctx.Done():
		t.Fatal("running query did not end")
	}
	waitGone(rp.ID)
	assertAlive(other)
	// An idle open transaction is rolled back when its connection is terminated.
	transactional, tp, tuuid := newSession()
	if _, err := transactional.ExecContext(ctx, "START TRANSACTION"); err != nil {
		t.Fatal(err)
	}
	if _, err := transactional.ExecContext(ctx, "UPDATE accounts SET balance=balance+1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err := operator.KillConnection(ctx, lookup(tp, tuuid), "kill"); err != nil {
		t.Fatal(err)
	}
	waitGone(tp.ID)
	// Taking the same row lock proves cleanup released the killed transaction.
	if _, err := other.ExecContext(ctx, "SET SESSION innodb_lock_wait_timeout=2"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.ExecContext(ctx, "START TRANSACTION"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.ExecContext(ctx, "SELECT id FROM accounts WHERE id=1 FOR UPDATE"); err != nil {
		t.Fatal("transaction locks survived", err)
	}
	if _, err := other.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
}
