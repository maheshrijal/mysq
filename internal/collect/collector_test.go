package collect

import (
	"context"
	"encoding/json"
	"errors"
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

func TestSampledContextCancellationIsFatal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sampledContextError(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("sampled context error = %v, want context.Canceled", err)
	}
}

func TestServerErrorSampleRejectsEnclosedProbeFailure(t *testing.T) {
	probeErr := errors.New("unsupported optional probe")
	if err := sampledServerError(nil, nil, nil, probeErr); !errors.Is(err, probeErr) || !strings.Contains(err.Error(), "contaminated") {
		t.Fatalf("server-error sample contamination = %v", err)
	}
	if err := sampledServerError(nil, nil, nil); err != nil {
		t.Fatalf("clean server-error sample rejected: %v", err)
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

func TestAttributeActiveUsersScopesDigestBySchema(t *testing.T) {
	queries := []model.Query{
		{Schema: "app_a", Digest: "shared"},
		{Schema: "app_b", Digest: "shared"},
	}
	processes := []model.Process{
		{Database: "app_a", Digest: "shared", User: "worker_a", Command: "Query"},
		{Database: "app_b", Digest: "shared", User: "worker_b", Command: "Query"},
	}
	attributeActiveUsers(queries, processes)
	if got := strings.Join(queries[0].ActiveUsers, ","); got != "worker_a" {
		t.Fatalf("app_a active users = %q, want worker_a", got)
	}
	if got := strings.Join(queries[1].ActiveUsers, ","); got != "worker_b" {
		t.Fatalf("app_b active users = %q, want worker_b", got)
	}
}

func TestRequiredCollectionsMarshalAsArraysAfterDegradedProbe(t *testing.T) {
	result := newContext("test", Target{})
	result.Queries = nil
	result.WaitEvents = nil
	result.StatementSamples = nil
	result.Capabilities = nil
	normalizeRequiredCollections(result)
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"findings", "queries", "tables", "indexes", "processes", "connection_groups", "locks",
		"transactions", "metadata_locks", "wait_events", "file_io", "server_errors",
		"memory_consumers", "statement_samples", "capabilities",
	} {
		if got := string(object[field]); got != "[]" {
			t.Fatalf("required collection %q marshaled as %s, want []", field, got)
		}
	}
	for _, field := range []string{"variables", "global_status"} {
		if got := string(object[field]); got != "{}" {
			t.Fatalf("required map %q marshaled as %s, want {}", field, got)
		}
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

func TestDeriveStatementSamplesRanksCurrentDatabaseTime(t *testing.T) {
	first := map[string]statementDigestCounter{
		"slow": {Digest: "slow", Schema: "app", Statement: "SELECT * FROM orders WHERE id = ?", Count: 10, TotalMillis: 100},
		"fast": {Digest: "fast", Schema: "app", Statement: "SELECT * FROM users WHERE id = ?", Count: 20, TotalMillis: 200},
	}
	second := map[string]statementDigestCounter{
		"slow":               {Digest: "slow", Schema: "app", Statement: "SELECT * FROM orders WHERE id = ?", Count: 12, TotalMillis: 500},
		"fast":               {Digest: "fast", Schema: "app", Statement: "SELECT * FROM users WHERE id = ?", Count: 30, TotalMillis: 300},
		"self":               {Digest: "self", Statement: "SELECT `COUNT_STAR` , `SUM_TIMER_WAIT` FROM `performance_schema` . `events_statements_summary_by_digest`", Count: 1, TotalMillis: 1000},
		"status":             {Digest: "status", Statement: "SHOW GLOBAL STATUS", Count: 2, TotalMillis: 100},
		"statement-counters": {Digest: "statement-counters", Statement: "SELECT SUM(COUNT_STAR), SUM(SUM_ERRORS), SUM(SUM_WARNINGS) FROM performance_schema.events_statements_summary_global_by_event_name WHERE EVENT_NAME LIKE ?", Count: 2, TotalMillis: 100},
	}
	samples := deriveStatementSamples(first, second, 2*time.Second, 10)
	if len(samples) != 2 || samples[0].Digest != "slow" || samples[0].Calls != 2 || samples[0].CallsPerSecond != 1 ||
		samples[0].DatabaseTimeMillis != 400 || samples[0].DatabaseTimeMillisPerSecond != 200 {
		t.Fatalf("unexpected statement samples: %+v", samples)
	}
	if math.Abs(samples[0].DatabaseTimeSharePercent-80) > 0.01 {
		t.Fatalf("database time share = %.2f, want 80", samples[0].DatabaseTimeSharePercent)
	}
}

func TestDeriveStatementSamplesHandlesCounterReset(t *testing.T) {
	first := map[string]statementDigestCounter{"query": {Digest: "query", Count: 100, TotalMillis: 1000}}
	second := map[string]statementDigestCounter{"query": {Digest: "query", Count: 2, TotalMillis: 50}}
	samples := deriveStatementSamples(first, second, time.Second, 10)
	if len(samples) != 0 {
		t.Fatalf("counter reset was not handled: %+v", samples)
	}
}

func TestDeriveStatementSamplesScopesDigestBySchema(t *testing.T) {
	first := map[string]statementDigestCounter{
		statementDigestIdentity("app_a", "shared"): {Digest: "shared", Schema: "app_a", Statement: "SELECT id FROM orders", Count: 10, TotalMillis: 100},
		statementDigestIdentity("app_b", "shared"): {Digest: "shared", Schema: "app_b", Statement: "SELECT id FROM orders", Count: 20, TotalMillis: 200},
	}
	second := map[string]statementDigestCounter{
		statementDigestIdentity("app_a", "shared"): {Digest: "shared", Schema: "app_a", Statement: "SELECT id FROM orders", Count: 12, TotalMillis: 300},
		statementDigestIdentity("app_b", "shared"): {Digest: "shared", Schema: "app_b", Statement: "SELECT id FROM orders", Count: 25, TotalMillis: 260},
	}
	samples := deriveStatementSamples(first, second, time.Second, 10)
	if len(samples) != 2 {
		t.Fatalf("schema-scoped samples = %+v, want two rows", samples)
	}
	if samples[0].Schema != "app_a" || samples[0].Digest != "shared" || samples[0].Calls != 2 || samples[0].DatabaseTimeMillis != 200 {
		t.Fatalf("app_a sample = %+v", samples[0])
	}
	if samples[1].Schema != "app_b" || samples[1].Digest != "shared" || samples[1].Calls != 5 || samples[1].DatabaseTimeMillis != 60 {
		t.Fatalf("app_b sample = %+v", samples[1])
	}
}

func TestFullSampleIntervalsExposeEveryRateWindow(t *testing.T) {
	result := &model.Context{}
	recordFullSampleIntervals(result,
		1100*time.Millisecond,
		2100*time.Millisecond,
		2200*time.Millisecond,
		2300*time.Millisecond,
		2400*time.Millisecond,
		2500*time.Millisecond,
	)
	want := model.SampleIntervals{
		GlobalStatus: 1100, WaitEvents: 2100, FileIO: 2200, ServerErrors: 2300,
		StatementDigests: 2400, StatementCounters: 2500,
	}
	if result.IntervalMillis != want.GlobalStatus || result.SampleIntervals != want {
		t.Fatalf("sample intervals = legacy %d, families %+v; want legacy %d, families %+v",
			result.IntervalMillis, result.SampleIntervals, want.GlobalStatus, want)
	}

	first := map[string]waitCounter{"wait/io": {Name: "wait/io", Count: 10, TotalMillis: 100}}
	second := map[string]waitCounter{"wait/io": {Name: "wait/io", Count: 31, TotalMillis: 310}}
	waits := deriveWaitEvents(first, second, time.Duration(want.WaitEvents)*time.Millisecond)
	if len(waits) != 1 {
		t.Fatalf("derived waits = %+v", waits)
	}
	reconstructed := float64(waits[0].SampleCount) / waits[0].EventsPerSecond * 1000
	if math.Abs(reconstructed-float64(want.WaitEvents)) > 0.001 {
		t.Fatalf("published wait interval = %dms, reconstructed %.3fms", want.WaitEvents, reconstructed)
	}
}

func TestDeriveEngineMetricsUsesIndependentSampleWindows(t *testing.T) {
	first := map[string]string{"Questions": "100"}
	second := map[string]string{"Questions": "111"}
	metrics := deriveEngineMetrics(first, second, nil,
		statementCounter{Errors: 10, Warnings: 20}, statementCounter{Errors: 20, Warnings: 40},
		time.Second, 2*time.Second, true)

	// deriveMetrics removes the final SHOW GLOBAL STATUS from Questions.
	if metrics.QueriesPerSecond != 10 {
		t.Fatalf("status rate used wrong window: got %.2f qps", metrics.QueriesPerSecond)
	}
	if metrics.StatementErrorsPerSec != 5 || metrics.StatementWarningsPerSec != 10 {
		t.Fatalf("statement rates used wrong window: errors=%.2f warnings=%.2f",
			metrics.StatementErrorsPerSec, metrics.StatementWarningsPerSec)
	}
}

func TestFocusedProbePropagatesProbeAndContextErrors(t *testing.T) {
	collector := New("test")
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "driver", err: errors.New("invalid connection")},
		{name: "context", err: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := newContext("test", Target{})
			err := collector.focusedProbe(result, "table statistics", func() error { return test.err })
			if !errors.Is(err, test.err) {
				t.Fatalf("focused probe error = %v, want wrapped %v", err, test.err)
			}
			if len(result.Capabilities) != 1 || result.Capabilities[0].Available || len(result.Warnings) != 1 {
				t.Fatalf("failed probe was not recorded: capabilities=%+v warnings=%+v", result.Capabilities, result.Warnings)
			}
		})
	}
}

func TestFocusedOptionalProbeDegradesOrdinaryFailure(t *testing.T) {
	collector := New("test")
	result := newContext("test", Target{})
	probeErr := errors.New("process list unavailable")

	if err := collector.focusedOptionalProbe(context.Background(), result, "process list", func() error { return probeErr }); err != nil {
		t.Fatalf("ordinary optional failure was not degraded: %v", err)
	}
	if len(result.Capabilities) != 1 || result.Capabilities[0].Available || len(result.Warnings) != 1 {
		t.Fatalf("optional failure was not recorded: capabilities=%+v warnings=%+v", result.Capabilities, result.Warnings)
	}
}

func TestFocusedOptionalProbePropagatesCancellation(t *testing.T) {
	collector := New("test")
	for _, contextErr := range []error{context.Canceled, context.DeadlineExceeded} {
		result := newContext("test", Target{})
		err := collector.focusedOptionalProbe(context.Background(), result, "process list", func() error { return contextErr })
		if !errors.Is(err, contextErr) {
			t.Fatalf("optional probe error = %v, want wrapped %v", err, contextErr)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := newContext("test", Target{})
	err := collector.focusedOptionalProbe(ctx, result, "process list", func() error { return errors.New("driver stopped") })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context was hidden by driver error: %v", err)
	}
}

func TestFocusedSampleProbePropagatesEitherEndpointFailure(t *testing.T) {
	collector := New("test")
	for _, failures := range [][]error{
		{errors.New("first sample failed"), nil},
		{nil, errors.New("second sample failed")},
	} {
		result := newContext("test", Target{})
		if err := collector.focusedSampleProbe(result, "wait events", failures...); err == nil {
			t.Fatal("focused sample unexpectedly accepted a failed endpoint")
		}
		if len(result.Capabilities) != 1 || result.Capabilities[0].Available {
			t.Fatalf("failed sample was not recorded once: %+v", result.Capabilities)
		}
	}
}

func TestFocusedSampleReturnsFirstEndpointFailureBeforeWaiting(t *testing.T) {
	collector := New("test")
	collector.Interval = time.Hour
	result := newContext("test", Target{})
	probeErr := errors.New("first sample failed")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if _, err := collector.waitForFocusedSample(ctx, result, "wait events", probeErr); !errors.Is(err, probeErr) {
		t.Fatalf("first endpoint error = %v, want immediate wrapped %v", err, probeErr)
	}
	if len(result.Capabilities) != 1 || result.Capabilities[0].Available {
		t.Fatalf("first endpoint failure was not recorded: %+v", result.Capabilities)
	}
}

func TestFocusedProbeAcceptsSuccessfulNilReplication(t *testing.T) {
	collector := New("test")
	result := newContext("test", Target{})
	if err := collector.focusedProbe(result, "replication", func() error {
		result.Replication = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if result.Replication != nil || len(result.Capabilities) != 1 || !result.Capabilities[0].Available {
		t.Fatalf("successful non-replica result was not preserved: replication=%+v capabilities=%+v", result.Replication, result.Capabilities)
	}
}

func TestLegacyReplicationFallbackOnlyForUnsupportedSyntax(t *testing.T) {
	if !legacyReplicationFallback(&mysqlDriver.MySQLError{Number: 1064, Message: "syntax error"}) {
		t.Fatal("unsupported SHOW REPLICA syntax should use the legacy fallback")
	}
	if legacyReplicationFallback(&mysqlDriver.MySQLError{Number: 1227, Message: "access denied"}) {
		t.Fatal("a privilege error must not be replaced by the legacy fallback")
	}
	if legacyReplicationFallback(context.Canceled) {
		t.Fatal("a context error must not use the legacy fallback")
	}
}
