package collect

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/maheshrijal/mysq/internal/debuglog"
	"strconv"
	"strings"
	"time"
)

// TrendCounters are ephemeral telemetry; they never enter diagnostic snapshots.
type TrendCounters struct {
	At                                                                       time.Time
	ServerUUID                                                               string
	Uptime, SinceFlush, Questions, Running, LockWaits, ReadBytes, WriteBytes uint64
}

type TrendPoint struct {
	At                                                 time.Time
	Queries, Running, LockWaits, ReadBytes, WriteBytes float64
}

// OpenTrendSampler keeps one idle connection and performs one read per sample.
// sql.Open is lazy: unavailable telemetry must not prevent diagnostics opening.
func OpenTrendSampler(target Target) (*sql.DB, error) {
	db, err := sql.Open("mysql", target.DSN)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func SampleTrends(ctx context.Context, db *sql.DB) (TrendCounters, error) {
	defer debuglog.Start(ctx, "trends.sample")()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT @@server_uuid, VARIABLE_NAME, VARIABLE_VALUE
 FROM performance_schema.global_status WHERE VARIABLE_NAME IN
 ('Uptime','Uptime_since_flush_status','Questions','Threads_running',
  'Innodb_row_lock_waits','Innodb_data_read','Innodb_data_written')`)
	if err != nil {
		return TrendCounters{}, fmt.Errorf("sample live counters: %w", err)
	}
	defer rows.Close()
	result := TrendCounters{}
	fields := map[string]*uint64{"uptime": &result.Uptime, "uptime_since_flush_status": &result.SinceFlush,
		"questions": &result.Questions, "threads_running": &result.Running,
		"innodb_row_lock_waits": &result.LockWaits, "innodb_data_read": &result.ReadBytes, "innodb_data_written": &result.WriteBytes}
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&result.ServerUUID, &name, &value); err != nil {
			return TrendCounters{}, err
		}
		field, ok := fields[strings.ToLower(name)]
		if !ok {
			continue
		}
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return TrendCounters{}, fmt.Errorf("invalid live counter %s", name)
		}
		*field = parsed
		delete(fields, strings.ToLower(name))
	}
	if err := rows.Err(); err != nil {
		return TrendCounters{}, err
	}
	if len(fields) != 0 || result.ServerUUID == "" {
		return TrendCounters{}, fmt.Errorf("live counters unavailable")
	}
	result.At = time.Now()
	return result, nil
}

// TrendDelta uses actual elapsed time, refuses resets, and excludes this
// sampler's own SELECT and running thread. Other clients remain server-wide.
func TrendDelta(before, after TrendCounters) (TrendPoint, bool) {
	seconds := after.At.Sub(before.At).Seconds()
	if seconds <= 0 || before.ServerUUID == "" || before.ServerUUID != after.ServerUUID ||
		after.Uptime < before.Uptime || after.SinceFlush < before.SinceFlush ||
		after.Questions < before.Questions || after.LockWaits < before.LockWaits ||
		after.ReadBytes < before.ReadBytes || after.WriteBytes < before.WriteBytes {
		return TrendPoint{}, false
	}
	queries := after.Questions - before.Questions
	if queries > 0 {
		queries--
	}
	running := after.Running
	if running > 0 {
		running--
	}
	return TrendPoint{At: after.At, Queries: float64(queries) / seconds, Running: float64(running),
		LockWaits:  float64(after.LockWaits-before.LockWaits) / seconds,
		ReadBytes:  float64(after.ReadBytes-before.ReadBytes) / seconds,
		WriteBytes: float64(after.WriteBytes-before.WriteBytes) / seconds}, true
}
