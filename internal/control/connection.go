package control

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/maheshrijal/mysq/internal/model"
	"github.com/maheshrijal/mysq/internal/sanitize"
)

// Connection is a freshly observed session, including sleeping sessions. Its
// private anchor prevents an old confirmation from surviving a reconnect.
type Connection struct {
	model.Process
	ServerUUID string
	anchor     *sql.Conn
}

const connectionSelect = `SELECT @@server_uuid, t.PROCESSLIST_ID, t.THREAD_ID,
 COALESCE(t.PROCESSLIST_USER,''), COALESCE(t.PROCESSLIST_HOST,''),
 COALESCE(t.PROCESSLIST_DB,''), COALESCE(t.PROCESSLIST_COMMAND,''),
 COALESCE(t.PROCESSLIST_TIME,0), COALESCE(t.PROCESSLIST_STATE,''),
 COALESCE(t.PROCESSLIST_INFO,'')
 FROM performance_schema.threads t
 WHERE t.TYPE='FOREGROUND' AND t.PROCESSLIST_USER IS NOT NULL
 AND t.PROCESSLIST_ID <> CONNECTION_ID() AND t.PROCESSLIST_ID=?`

func readConnection(ctx context.Context, conn *sql.Conn, id uint64) (Connection, error) {
	var c Connection
	err := conn.QueryRowContext(ctx, connectionSelect, id).Scan(&c.ServerUUID, &c.ID, &c.ThreadID,
		&c.User, &c.Host, &c.Database, &c.Command, &c.Seconds, &c.State, &c.Statement)
	c.Statement = sanitize.SQL(c.Statement)
	return c, err
}

func sameConnection(a, b Connection) bool {
	return a.ServerUUID == b.ServerUUID && a.ID == b.ID && a.ThreadID == b.ThreadID &&
		a.User == b.User && a.Host == b.Host
}

func completeConnection(c Connection) bool {
	return c.ID != 0 && c.ThreadID != 0 && c.ServerUUID != "" && c.User != ""
}

// Connection resolves a snapshot selection to live evidence before confirmation.
// Statement instrumentation is unnecessary: the action targets the whole session.
func (q *Queries) Connection(ctx context.Context, serverUUID string, process model.Process) (Connection, error) {
	selected := Connection{Process: process, ServerUUID: serverUUID}
	if !completeConnection(selected) {
		return Connection{}, errors.New("connection identity is incomplete; refresh the snapshot")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.conn == nil {
		var err error
		q.conn, q.closeConn, err = q.connect(ctx)
		if err != nil {
			return Connection{}, err
		}
	}
	current, err := readConnection(ctx, q.conn, selected.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return Connection{}, errors.New("connection ended or is no longer visible; refresh the snapshot")
	}
	if err != nil {
		q.release()
		return Connection{}, fmt.Errorf("read live connection: %w", err)
	}
	if !sameConnection(selected, current) {
		return Connection{}, errors.New("connection changed; refresh the snapshot and select it again")
	}
	current.anchor = q.conn
	return current, nil
}

// KillConnection sends one KILL CONNECTION on the session that observed the
// target, after rechecking its identity. It never reconnects or retries a send.
func (q *Queries) KillConnection(ctx context.Context, target Connection, confirmation string) error {
	if confirmation != "kill" {
		return errors.New("type exactly kill to confirm")
	}
	if !completeConnection(target) {
		return errors.New("connection identity is incomplete; refresh the snapshot")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.conn == nil || target.anchor == nil || target.anchor != q.conn {
		return errors.New("control connection changed; nothing was sent. Refresh and select it again")
	}
	current, err := readConnection(ctx, q.conn, target.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("connection ended or is no longer visible; nothing was sent")
	}
	if err != nil {
		q.release()
		return fmt.Errorf("could not recheck connection; nothing was sent: %w", err)
	}
	if !sameConnection(target, current) {
		return errors.New("connection changed; nothing was sent. Refresh and select it again")
	}
	_, err = q.conn.ExecContext(ctx, fmt.Sprintf("KILL CONNECTION %d", target.ID))
	if err != nil {
		q.release()
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) {
			switch mysqlErr.Number {
			case 1094:
				return errors.New("connection ended before termination; refresh the snapshot")
			case 1095, 1227:
				return errors.New("MySQL denied termination. Other users' connections require CONNECTION_ADMIN (or SUPER); SYSTEM_USER targets also require SYSTEM_USER")
			}
		}
		return fmt.Errorf("termination outcome unknown; refresh before trying again: %w", err)
	}
	return nil
}
