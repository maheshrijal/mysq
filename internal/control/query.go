// Package control contains explicit, confirmed operator actions. Diagnostic
// collection remains read-only and never calls this package.
package control

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/maheshrijal/mysq/internal/collect"
	"github.com/maheshrijal/mysq/internal/model"
	"github.com/maheshrijal/mysq/internal/sanitize"
)

// Execution is ephemeral live evidence, never a historical cancellation target.
type Execution struct {
	model.Process
	ServerUUID   string
	EventID      uint64
	rawStatement string
	anchor       *sql.Conn
}

// Queries owns one pinned control session for the lifetime of the TUI. Query
// and connection selections are bound to it; neither kill operation reconnects.
type Queries struct {
	Target    collect.Target
	mu        sync.Mutex
	conn      *sql.Conn
	closeConn func()
}

func (q *Queries) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.release()
}

func (q *Queries) release() {
	if q.closeConn != nil {
		q.closeConn()
	}
	q.conn, q.closeConn = nil, nil
}

const executionSelect = `SELECT @@server_uuid, t.PROCESSLIST_ID, t.THREAD_ID, es.EVENT_ID,
 COALESCE(t.PROCESSLIST_USER,''), COALESCE(t.PROCESSLIST_HOST,''),
 COALESCE(es.CURRENT_SCHEMA,''), t.PROCESSLIST_COMMAND, COALESCE(t.PROCESSLIST_TIME,0),
 COALESCE(t.PROCESSLIST_STATE,''), COALESCE(es.DIGEST,''), COALESCE(es.TIMER_WAIT,0)/1000000000,
 COALESCE(es.DIGEST_TEXT,t.PROCESSLIST_INFO,'')
 FROM performance_schema.threads t
 JOIN performance_schema.events_statements_current es ON es.THREAD_ID=t.THREAD_ID
 JOIN performance_schema.setup_instruments i ON i.NAME=es.EVENT_NAME AND i.ENABLED='YES'
 WHERE t.TYPE='FOREGROUND' AND t.PROCESSLIST_ID IS NOT NULL
 AND t.INSTRUMENTED='YES'
 AND t.PROCESSLIST_ID <> CONNECTION_ID() AND t.PROCESSLIST_COMMAND IN ('Query','Execute')
 AND es.END_EVENT_ID IS NULL`

// Sessions resolves schema + digest from at most 100 current candidates. Prepared
// executions may lack an event digest; MySQL's parser resolves their current SQL.
func (q *Queries) Sessions(ctx context.Context, schema, digest string) ([]Execution, error) {
	if digest == "" {
		return nil, errors.New("this query has no digest; live executions cannot be identified")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.conn == nil {
		var err error
		q.conn, q.closeConn, err = q.connect(ctx)
		if err != nil {
			return nil, err
		}
	}
	conn := q.conn
	complete := false
	defer func() {
		if !complete {
			q.release()
		}
	}()
	if err := checkVisibility(ctx, conn); err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(ctx, executionSelect+` AND COALESCE(es.CURRENT_SCHEMA,'')=? AND (es.DIGEST=? OR (es.DIGEST IS NULL AND t.PROCESSLIST_COMMAND='Execute')) ORDER BY t.PROCESSLIST_TIME DESC, t.PROCESSLIST_ID LIMIT 100`, schema, digest)
	if err != nil {
		return nil, fmt.Errorf("read live executions: %w", err)
	}
	defer rows.Close()
	var candidates []Execution
	for rows.Next() {
		e, err := scanExecution(rows)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var result []Execution
	for _, e := range candidates {
		if err := resolveDigest(ctx, conn, &e); err != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 3677 {
				continue
			} // truncated/unparseable SQL is unidentifiable
			return nil, fmt.Errorf("identify current execution: %w", err)
		}
		if e.Digest == digest {
			e.anchor = conn
			result = append(result, e)
		}
	}
	complete = true
	return result, nil
}

type scanner interface{ Scan(...any) error }

func scanExecution(row scanner) (Execution, error) {
	var e Execution
	err := row.Scan(&e.ServerUUID, &e.ID, &e.ThreadID, &e.EventID, &e.User, &e.Host,
		&e.Database, &e.Command, &e.Seconds, &e.State, &e.Digest, &e.StatementLatencyMillis, &e.rawStatement)
	e.Statement = sanitize.SQL(e.rawStatement)
	return e, err
}

func resolveDigest(ctx context.Context, conn *sql.Conn, e *Execution) error {
	defer func() { e.rawStatement = "" }()
	if e.Digest != "" || e.Command != "Execute" || e.rawStatement == "" {
		return nil
	}
	// Parameters contain SQL as data, never SQL to execute. Normalization happens
	// on the server; no literals cross the control interface into TUI state.
	err := conn.QueryRowContext(ctx, `SELECT STATEMENT_DIGEST(?), STATEMENT_DIGEST_TEXT(?)`, e.rawStatement, e.rawStatement).Scan(&e.Digest, &e.Statement)
	e.Statement = sanitize.SQL(e.Statement)
	return err
}

// Kill sends exactly one KILL QUERY after checking the same server, thread and
// statement event. MySQL has no atomic compare-and-kill operation: a statement
// can still change between this check and dispatch. Never retry a failed send.
func (q *Queries) Kill(ctx context.Context, target Execution, confirmation string) error {
	if confirmation != "kill" {
		return errors.New("type exactly kill to confirm")
	}
	if target.ID == 0 || target.ThreadID == 0 || target.EventID == 0 || target.ServerUUID == "" || target.Digest == "" {
		return errors.New("execution identity is incomplete; refresh live executions")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.conn == nil || target.anchor == nil || target.anchor != q.conn {
		return errors.New("control connection changed; nothing was sent. Refresh and select it again")
	}
	conn := q.conn
	if err := checkVisibility(ctx, conn); err != nil {
		q.release()
		return fmt.Errorf("control connection or visibility lost; nothing was sent: %w", err)
	}
	current, err := scanExecution(conn.QueryRowContext(ctx, executionSelect+` AND t.PROCESSLIST_ID=?`, target.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("execution ended or is no longer visible; nothing was sent")
	}
	if err != nil {
		q.release()
		return fmt.Errorf("could not recheck execution; nothing was sent: %w", err)
	}
	if err := resolveDigest(ctx, conn, &current); err != nil {
		q.release()
		return fmt.Errorf("could not identify execution; nothing was sent: %w", err)
	}
	if !sameExecution(target, current) {
		return errors.New("execution changed; nothing was sent. Refresh and select it again")
	}
	// The ID is an unsigned integer, never user-supplied SQL. Use the pinned
	// connection rather than sql.DB.Exec, which may retry on ErrBadConn.
	_, err = conn.ExecContext(ctx, fmt.Sprintf("KILL QUERY %d", target.ID))
	if err != nil {
		q.release()
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) {
			switch mysqlErr.Number {
			case 1094:
				return errors.New("connection ended before cancellation; refresh live executions")
			case 1095, 1227:
				return errors.New("MySQL denied cancellation. Other users' queries require CONNECTION_ADMIN (or SUPER); SYSTEM_USER targets also require SYSTEM_USER")
			}
		}
		return fmt.Errorf("cancellation outcome unknown; refresh before trying again: %w", err)
	}
	return nil
}

func checkVisibility(ctx context.Context, conn *sql.Conn) error {
	var enabled int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM performance_schema.setup_consumers
	 WHERE NAME IN ('global_instrumentation','thread_instrumentation','events_statements_current') AND ENABLED='YES'`).Scan(&enabled); err != nil {
		return fmt.Errorf("check current-statement visibility: %w", err)
	}
	if enabled != 3 {
		return errors.New("current-statement instrumentation is disabled; live executions are unavailable")
	}
	return nil
}

func sameExecution(a, b Execution) bool {
	return a.ServerUUID == b.ServerUUID && a.ID == b.ID && a.ThreadID == b.ThreadID &&
		a.EventID == b.EventID && a.Digest == b.Digest && a.Database == b.Database &&
		a.User == b.User && a.Host == b.Host
}

func (q *Queries) connect(ctx context.Context) (*sql.Conn, func(), error) {
	db, err := sql.Open("mysql", q.Target.DSN)
	if err != nil {
		return nil, nil, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	return conn, func() { _ = conn.Close(); _ = db.Close() }, nil
}
