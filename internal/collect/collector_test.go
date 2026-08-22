package collect

import (
	"math"
	"strings"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"

	"github.com/maheshrijal/mysq/internal/model"
)

func TestResolveConnectionURLPreservesDriverOptions(t *testing.T) {
	target, err := ResolveConnection("mysql://observer:p%40ss@db.example:3307/app%20db?tls=true&timeout=3s&charset=utf8mb4")
	if err != nil {
		t.Fatal(err)
	}
	if target.Host != "db.example" || target.Port != 3307 || target.Database != "app db" {
		t.Fatalf("unexpected target: %+v", target)
	}
	cfg, err := mysqlDriver.ParseDSN(target.DSN)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Passwd != "p@ss" || cfg.TLSConfig != "true" || cfg.Timeout != 3*time.Second || !strings.Contains(target.DSN, "charset=utf8mb4") {
		t.Fatalf("unexpected DSN config: %+v", cfg)
	}
}

func TestResolveConnectionRejectsInvalidPort(t *testing.T) {
	if _, err := ResolveConnection("mysql://observer@localhost:70000/app"); err == nil {
		t.Fatal("expected invalid port error")
	}
}

func TestResolveConnectionEnvironmentCompatibility(t *testing.T) {
	t.Setenv("MYSQ_DATABASE_URL", "")
	t.Setenv("MYSQLDOT_DATABASE_URL", "legacy@tcp(legacy-db:3306)/app")
	t.Setenv("DATABASE_URL", "fallback@tcp(fallback-db:3306)/app")

	target, err := ResolveConnection("")
	if err != nil {
		t.Fatal(err)
	}
	if target.Host != "legacy-db" {
		t.Fatalf("legacy environment variable was not honored: %+v", target)
	}

	t.Setenv("MYSQ_DATABASE_URL", "current@tcp(current-db:3306)/app")
	target, err = ResolveConnection("")
	if err != nil {
		t.Fatal(err)
	}
	if target.Host != "current-db" {
		t.Fatalf("MYSQ_DATABASE_URL did not take precedence: %+v", target)
	}
}

func TestDeriveMetricsRemovesSamplingQuery(t *testing.T) {
	first := map[string]string{"Questions": "100", "Innodb_rows_read": "10", "Innodb_data_reads": "50", "Innodb_os_log_written": "1000"}
	second := map[string]string{
		"Questions": "101", "Innodb_rows_read": "30", "Innodb_data_reads": "54", "Innodb_os_log_written": "2024",
		"Innodb_redo_log_current_lsn": "5000", "Innodb_redo_log_checkpoint_lsn": "4000",
		"Innodb_redo_log_capacity_resized": "10000", "Innodb_data_pending_reads": "2",
	}
	metrics := deriveMetrics(first, second, map[string]string{}, time.Second)
	if metrics.QueriesPerSecond != 0 || metrics.RowsReadPerSecond != 20 || metrics.DataReadsPerSecond != 4 ||
		metrics.RedoBytesPerSecond != 1024 || metrics.RedoCheckpointAgeBytes != 1000 || metrics.RedoCheckpointAgePct != 10 || metrics.PendingReads != 2 {
		t.Fatalf("unexpected rates: %+v", metrics)
	}
}

func TestAttributeActiveUsersToDigestWithoutGuessing(t *testing.T) {
	queries := []model.Query{{Digest: "A"}, {Digest: "B"}}
	processes := []model.Process{
		{Digest: "A", User: "worker", Command: "Query"},
		{Digest: "A", User: "api", Command: "Query"},
		{Digest: "A", User: "api", Command: "Query"},
		{Digest: "B", User: "reporting", Command: "Sleep"},
	}
	attributeActiveUsers(queries, processes)
	if got := strings.Join(queries[0].ActiveUsers, ","); got != "api,worker" {
		t.Fatalf("active users = %q, want api,worker", got)
	}
	if len(queries[1].ActiveUsers) != 0 {
		t.Fatalf("sleeping user was attributed to digest: %+v", queries[1].ActiveUsers)
	}
}

func TestSummarizeProcessesByUserHostAndPair(t *testing.T) {
	groups := summarizeProcesses([]model.Process{
		{User: "app", Host: "10.0.0.1:5000", Command: "Query", Statement: "SELECT ?"},
		{User: "app", Host: "10.0.0.1:5001", Command: "Sleep"},
		{User: "worker", Host: "10.0.0.2:6000", Command: "Connect"},
	})
	want := map[string]model.ConnectionGroup{
		"user:app":                  {Kind: "user", Key: "app", Total: 2, Active: 1, Sleeping: 1},
		"host:10.0.0.1":             {Kind: "host", Key: "10.0.0.1", Total: 2, Active: 1, Sleeping: 1},
		"user_host:worker@10.0.0.2": {Kind: "user_host", Key: "worker@10.0.0.2", Total: 1, Other: 1},
	}
	for _, group := range groups {
		key := group.Kind + ":" + group.Key
		if expected, ok := want[key]; ok {
			if group != expected {
				t.Fatalf("group %s = %+v, want %+v", key, group, expected)
			}
			delete(want, key)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing groups: %+v", want)
	}
}

func TestDeriveWaitEventsUsesSampleIntervalAndRanksCurrentPressure(t *testing.T) {
	first := map[string]waitCounter{
		"wait/io/file/innodb/data": {Name: "wait/io/file/innodb/data", Class: "io/file", Count: 100, TotalMillis: 1000},
		"wait/synch/mutex/sql/x":   {Name: "wait/synch/mutex/sql/x", Class: "synch/mutex", Count: 1000, TotalMillis: 9000},
	}
	second := map[string]waitCounter{
		"wait/io/file/innodb/data": {Name: "wait/io/file/innodb/data", Class: "io/file", Count: 110, TotalMillis: 1200},
		"wait/synch/mutex/sql/x":   {Name: "wait/synch/mutex/sql/x", Class: "synch/mutex", Count: 1001, TotalMillis: 9010},
	}
	waits := deriveWaitEvents(first, second, 2*time.Second)
	if len(waits) != 2 || waits[0].Class != "io/file" || waits[0].SampleCount != 10 || waits[0].EventsPerSecond != 5 || waits[0].WaitMillisPerSecond != 100 {
		t.Fatalf("unexpected sampled waits: %+v", waits)
	}
	if math.Abs(waits[0].SampleSharePercent-95.238) > 0.01 {
		t.Fatalf("sample share = %.3f", waits[0].SampleSharePercent)
	}
}

func TestDeriveFileIOCalculatesLatencyFromCounterDeltas(t *testing.T) {
	first := map[string]fileIOCounter{"data": {Name: "data", Reads: 10, Writes: 5, BytesRead: 1000, BytesWritten: 500, ReadMillis: 20, WriteMillis: 10}}
	second := map[string]fileIOCounter{"data": {Name: "data", Reads: 14, Writes: 7, BytesRead: 5000, BytesWritten: 2500, ReadMillis: 28, WriteMillis: 16}}
	items := deriveFileIO(first, second, 2*time.Second)
	if len(items) != 1 || items[0].ReadsPerSecond != 2 || items[0].WritesPerSecond != 1 ||
		items[0].ReadBytesPerSecond != 2000 || items[0].MeanReadLatencyMillis != 2 || items[0].MeanWriteLatencyMillis != 3 {
		t.Fatalf("unexpected file I/O: %+v", items)
	}
}

func TestDeriveServerErrorsAndInstrumentationLoss(t *testing.T) {
	first := map[uint64]errorCounter{1062: {Number: 1062, Name: "ER_DUP_ENTRY", Raised: 10}}
	second := map[uint64]errorCounter{1062: {Number: 1062, Name: "ER_DUP_ENTRY", Raised: 14, LastSeen: "now"}}
	errors := deriveServerErrors(first, second, 2*time.Second)
	if len(errors) != 1 || errors[0].SampleRaised != 4 || errors[0].RaisedPerSecond != 2 {
		t.Fatalf("unexpected errors: %+v", errors)
	}
	coverage := model.Instrumentation{}
	applyInstrumentationStatus(&coverage, map[string]string{"Performance_schema_digest_lost": "3", "Threads_running": "5"})
	if coverage.TotalLost != 3 || coverage.Lost["Performance_schema_digest_lost"] != 3 {
		t.Fatalf("unexpected coverage: %+v", coverage)
	}
}
