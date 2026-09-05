package analyze

import (
	"testing"

	"github.com/maheshrijal/mysq/internal/model"
)

func TestApplyFindsCriticalConnectionPressureAndMissingPrimaryKey(t *testing.T) {
	ctx := &model.Context{
		Server:    model.Server{PerformanceSchema: true},
		Metrics:   model.Metrics{ConnectionsCurrent: 95, ConnectionsMax: 100, ConnectionsUsedPercent: 95, BufferPoolHitPercent: 100},
		Variables: map[string]string{"innodb_flush_log_at_trx_commit": "1", "log_bin": "OFF", "skip_name_resolve": "ON"},
		Tables:    []model.Table{{Schema: "app", Name: "events", Engine: "InnoDB", HasPrimaryKey: false}},
	}
	Apply(ctx)
	if ctx.Health.Critical != 1 {
		t.Fatalf("critical = %d, want 1; findings=%+v", ctx.Health.Critical, ctx.Findings)
	}
	if ctx.Findings[0].ID != "connection_saturation" {
		t.Fatalf("first finding = %q, want connection_saturation", ctx.Findings[0].ID)
	}
	foundPK := false
	for _, finding := range ctx.Findings {
		foundPK = foundPK || finding.ID == "tables_without_primary_key"
	}
	if !foundPK {
		t.Fatal("expected tables_without_primary_key finding")
	}
}

func TestApplyTreatsUnusedIndexesAsReviewOnly(t *testing.T) {
	ctx := &model.Context{
		Server:    model.Server{PerformanceSchema: true, UptimeSeconds: 3600},
		Metrics:   model.Metrics{BufferPoolHitPercent: 100},
		Variables: map[string]string{"innodb_flush_log_at_trx_commit": "1", "skip_name_resolve": "ON"},
		Indexes:   []model.Index{{Schema: "app", Table: "orders", Name: "idx_status", Reads: 0, Writes: 20}},
	}
	Apply(ctx)
	for _, finding := range ctx.Findings {
		if finding.ID == "unused_indexes" && finding.Severity != model.SeverityNote {
			t.Fatalf("unused index severity = %s, want note", finding.Severity)
		}
	}
}

func TestApplyIgnoresLongLivedServerDaemons(t *testing.T) {
	ctx := &model.Context{
		Server:    model.Server{PerformanceSchema: true},
		Metrics:   model.Metrics{BufferPoolHitPercent: 100},
		Variables: map[string]string{"innodb_flush_log_at_trx_commit": "1", "skip_name_resolve": "ON"},
		Processes: []model.Process{
			{ID: 5, User: "event_scheduler", Command: "Daemon", State: "Waiting on empty queue", Seconds: 21_600},
			{ID: 9, User: "app", Command: "Query", Statement: "SELECT SLEEP(?)", Seconds: 10},
		},
	}
	Apply(ctx)
	for _, finding := range ctx.Findings {
		if finding.ID != "long_running_statements" {
			continue
		}
		if finding.Severity != model.SeverityWarning {
			t.Fatalf("severity = %s, want warning; daemon must not make it critical", finding.Severity)
		}
		if got := finding.Evidence["count"]; got != 1 {
			t.Fatalf("count = %v, want 1", got)
		}
		return
	}
	t.Fatal("expected long_running_statements finding for active query")
}

func TestUnavailableReplicationCannotBeHealthy(t *testing.T) {
	ctx := &model.Context{Server: model.Server{PerformanceSchema: true}, Metrics: model.Metrics{BufferPoolHitPercent: 100}, Capabilities: []model.Capability{{Name: "replication", Available: false, Reason: "access denied"}}}
	Apply(ctx)
	state := ctx.Health.Subsystem("replication")
	if state.Complete || state.Status != "unknown" || ctx.Health.State() == "HEALTHY" || ctx.Health.Warnings == 0 {
		t.Fatalf("%+v", ctx.Health)
	}
}

func TestCheapDominantQueryDoesNotWarn(t *testing.T) {
	ctx := &model.Context{Server: model.Server{PerformanceSchema: true}, Metrics: model.Metrics{BufferPoolHitPercent: 100}, Queries: []model.Query{{Schema: "app", Digest: "a", Calls: 100, MeanLatencyMillis: 0.01, TotalLatencyMillis: 1, NoIndexUsed: 100, RowsExamined: 100}}}
	Apply(ctx)
	for _, f := range ctx.Findings {
		if f.Subsystem == "queries" {
			t.Fatalf("cheap query triggered %+v", f)
		}
	}
}

func TestCapturedOperationalFailuresProduceFindings(t *testing.T) {
	ctx := &model.Context{Server: model.Server{PerformanceSchema: true}, Metrics: model.Metrics{BufferPoolHitPercent: 100, RedoWaitsPerSecond: 100, StatementErrorsPerSec: 100}, MetadataLocks: []model.MetadataLock{{Status: "PENDING"}}}
	Apply(ctx)
	ids := map[string]bool{}
	for _, f := range ctx.Findings {
		ids[f.ID] = true
	}
	for _, id := range []string{"metadata_lock_waits", "statement_errors", "innodb_flush_waits"} {
		if !ids[id] {
			t.Errorf("missing %s", id)
		}
	}
}

func TestReplicaLagAssessmentMatchesFinding(t *testing.T) {
	lag := int64(120)
	ctx := &model.Context{Server: model.Server{PerformanceSchema: true}, Metrics: model.Metrics{BufferPoolHitPercent: 100}, Replication: &model.Replication{IORunning: "Yes", SQLRunning: "Yes", SecondsBehind: &lag}, Capabilities: []model.Capability{{Name: "replication", Available: true}}}
	Apply(ctx)
	if state := ctx.Health.Subsystem("replication"); state.Status != "warn" || !state.Complete {
		t.Fatalf("%+v", state)
	}
	ctx.Replication = nil
	Apply(ctx)
	if ctx.Health.Subsystem("replication").Status != "not_applicable" {
		t.Fatal(ctx.Health)
	}
}

func TestBlockingChainCountsDistinctWaitersAndPreservesEdges(t *testing.T) {
	ctx := &model.Context{Locks: []model.LockWait{{BlockingTransaction: "1", WaitingTransaction: "2"}, {BlockingTransaction: "1", WaitingTransaction: "2"}, {BlockingTransaction: "2", WaitingTransaction: "3"}}, Transactions: []model.Transaction{{ID: "1", ProcessID: 10, User: "checkout"}, {ID: "2"}, {ID: "3"}}}
	chains := BlockingChains(ctx)
	if len(chains) != 1 || chains[0].RootTransaction != "1" || chains[0].WaiterCount != 2 || len(chains[0].Edges) != 3 || !chains[0].Complete {
		t.Fatalf("%+v", chains)
	}
	ctx.Transactions = nil
	if BlockingChains(ctx)[0].Complete {
		t.Fatal("missing owners considered complete")
	}
	ctx.Locks = append(ctx.Locks, model.LockWait{BlockingTransaction: "3", WaitingTransaction: "1"})
	chains = BlockingChains(ctx)
	if len(chains) != 1 || chains[0].Complete || len(chains[0].Edges) != 4 {
		t.Fatalf("cycle mishandled: %+v", chains)
	}
}

func TestCheapHighVolumeQueryStillSurfaces(t *testing.T) {
	ctx := &model.Context{Server: model.Server{PerformanceSchema: true}, Metrics: model.Metrics{BufferPoolHitPercent: 100}, Queries: []model.Query{{Schema: "app", Digest: "a", Calls: 100000, MeanLatencyMillis: 0.01, TotalLatencyMillis: 1000}}, StatementSamples: []model.StatementSample{{Schema: "app", Digest: "a", DatabaseTimeMillisPerSecond: 500}}}
	Apply(ctx)
	for _, f := range ctx.Findings {
		if f.Subsystem == "queries" {
			return
		}
	}
	t.Fatal("high-volume query was suppressed by mean latency alone")
}

func TestFailedVariableProbeIsNotMisreportedAsDisabled(t *testing.T) {
	ctx := &model.Context{Metrics: model.Metrics{BufferPoolHitPercent: 100}, Capabilities: []model.Capability{{Name: "global variables", Reason: "denied"}}}
	Apply(ctx)
	for _, f := range ctx.Findings {
		if f.ID == "performance_schema_disabled" {
			t.Fatal("unavailable variable treated as OFF")
		}
	}
}
