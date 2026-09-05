package collect

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Uses only the disposable loopback fixture. The application sessions below
// deliberately share credentials with another observer in the real deployment;
// exclusion must depend on client identity, never on usernames or SQL shape.
func TestFixtureProcessEvidence(t *testing.T) {
	ctx, c, target, observer := parallelFixture(t)
	loadTarget, err := ResolveConnection(os.Getenv("MYSQ_E2E_LOAD_DSN"))
	if err != nil || loadTarget.Host != "127.0.0.1" || loadTarget.Database != "app" || loadTarget.Port != target.Port {
		t.Fatal("requires same loopback app fixture")
	}
	app, err := sql.Open("mysql", loadTarget.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	// A normal application connection with the monitor's credentials must remain visible.
	sameUser, err := sql.Open("mysql", target.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer sameUser.Close()
	var sameUserID uint64
	if err := sameUser.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&sameUserID); err != nil {
		t.Fatal(err)
	}
	tagged, err := OpenDatabase(target)
	if err != nil {
		t.Fatal(err)
	}
	defer tagged.Close()
	var taggedID uint64
	if err := tagged.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&taggedID); err != nil {
		t.Fatal(err)
	}
	var attr string
	if err := observer.QueryRowContext(ctx, "SELECT ATTR_VALUE FROM performance_schema.session_connect_attrs WHERE PROCESSLIST_ID=? AND ATTR_NAME='program_name'", taggedID).Scan(&attr); err != nil || attr != "mysq" {
		t.Fatal("client tag missing", attr, err)
	}
	initial, err := c.collectProcesses(ctx, observer)
	if err != nil {
		t.Fatal(err)
	}
	foundSameUser := false
	for _, p := range initial {
		if p.ID == taggedID {
			t.Fatal("mysq session displayed")
		}
		if p.ID == sameUserID {
			foundSameUser = true
		}
	}
	if !foundSameUser {
		t.Fatal("application using monitor credentials was hidden")
	}
	// More than 100 sessions, with nested idle/socket waits enabled by the test harness.
	// The limit must apply to distinct connections, not joined wait rows.
	for range 105 {
		conn, err := app.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
	}
	var nested int
	if err := observer.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT THREAD_ID FROM performance_schema.events_waits_current WHERE END_EVENT_ID IS NULL GROUP BY THREAD_ID HAVING COUNT(*)>1) w`).Scan(&nested); err != nil {
		t.Fatal(err)
	}
	if nested == 0 {
		t.Fatal("fixture did not reproduce nested current waits")
	}
	items, err := c.collectProcesses(ctx, observer)
	if err != nil {
		t.Fatal(err)
	}
	unique := map[uint64]bool{}
	for _, p := range items {
		if unique[p.ID] {
			t.Fatal("duplicate connection", p.ID)
		}
		if p.ID == taggedID {
			t.Fatal("tagged diagnostic consumed a display slot")
		}
		unique[p.ID] = true
	}
	if len(unique) != 100 {
		t.Fatalf("got %d distinct connections, want 100", len(unique))
	}
	// Live values are retained only for an explicitly requested TUI capture.
	running, err := app.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer running.Close()
	var runningID uint64
	if err := running.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&runningID); err != nil {
		t.Fatal(err)
	}
	queryCtx, cancel := context.WithCancel(ctx)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		var v string
		_ = running.QueryRowContext(queryCtx, "SELECT 'tui-literal-94731' WHERE SLEEP(10)=0").Scan(&v)
	}()
	defer func() {
		// Close only the statement owned by this fixture, and join it before the
		// subsequent idle-database assertions run.
		cleanupCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_, _ = app.ExecContext(cleanupCtx, fmt.Sprintf("KILL QUERY %d", runningID))
		cancel()
		<-finished
	}()
	c.LiveSQL = true
	found := false
	for ctx.Err() == nil {
		items, err = c.collectProcesses(ctx, observer)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range items {
			if p.ID != runningID || p.Command != "Query" {
				continue
			}
			if !strings.Contains(p.LiveStatement, "'tui-literal-94731'") || strings.Contains(p.Statement, "94731") {
				t.Fatal("live/redacted evidence mixed", p.ID)
			}
			encoded, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "94731") {
				t.Fatal("literal entered serialized evidence")
			}
			found = true
		}
		if found {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !found {
		t.Fatal("live SQL did not become visible")
	}
	c.LiveSQL = false
	items, err = c.collectProcesses(ctx, observer)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range items {
		if p.LiveStatement != "" || strings.Contains(p.Statement, "94731") {
			t.Fatal("CLI retained a literal")
		}
	}
}
