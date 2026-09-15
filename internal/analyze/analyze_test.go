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

func TestReadAmplificationFinding(t *testing.T) {
	apply := func(queries ...model.Query) map[string]model.Finding {
		ctx := &model.Context{Server: model.Server{PerformanceSchema: true}, Metrics: model.Metrics{BufferPoolHitPercent: 100}, Queries: queries}
		Apply(ctx)
		out := map[string]model.Finding{}
		for _, f := range ctx.Findings {
			out[f.ID] = f
		}
		return out
	}
	query := func(digest string, examined, sent uint64, statement string) model.Query {
		return model.Query{Schema: "app", Digest: digest, Calls: 10, TotalLatencyMillis: 100, RowsExamined: examined, RowsSent: sent, Statement: statement}
	}

	f, ok := apply(query("a", 5000000, 10, "SELECT `id` FROM `orders` WHERE `email` = ?"))["query_read_amplification_app_a"]
	if !ok || f.Severity != model.SeverityWarning || f.Evidence["rows_examined_per_returned"] != 500000.0 {
		t.Fatalf("expected warning read amplification finding, got %+v", f)
	}
	if f := apply(query("b", 50000, 100, "SELECT `id` FROM `orders` WHERE `email` = ?"))["query_read_amplification_app_b"]; f.Severity != model.SeverityNote {
		t.Fatalf("expected note for ratio 500 at 5000 rows per call, got %+v", f)
	}
	if f := apply(query("c", 2000000, 4000, "SELECT `id` FROM `orders` WHERE `email` = ?"))["query_read_amplification_app_c"]; f.Severity != model.SeverityWarning {
		t.Fatalf("expected warning for 200000 rows per call even at ratio 500, got %+v", f)
	}
	ratioOnly := query("ro", 2000000, 1000, "SELECT `id` FROM `orders` WHERE `email` = ?")
	ratioOnly.Calls = 1000
	if f := apply(ratioOnly)["query_read_amplification_app_ro"]; f.Severity != model.SeverityWarning {
		t.Fatalf("expected warning for ratio 2000 at 2000 rows per call, got %+v", f)
	}

	// The most wasted rows wins, not the first digest by database time.
	first := query("first", 100000, 100, "SELECT `id` FROM `orders` WHERE `email` = ?")
	first.TotalLatencyMillis = 1000
	worst := query("worst", 5000000, 10, "SELECT `id` FROM `orders` WHERE `token` = ?")
	findings := apply(first, worst)
	if _, ok := findings["query_read_amplification_app_worst"]; !ok {
		t.Fatalf("expected the worst digest to be reported, got %v", findings)
	}
	if _, ok := findings["query_read_amplification_app_first"]; ok {
		t.Fatal("reported more than one read amplification digest")
	}

	// The digest query_no_index reports is not scored twice, but a second
	// no-index digest that query_no_index did not report is still eligible.
	noIndex := query("n", 5000000, 10, "SELECT `id` FROM `orders` WHERE `note` = ?")
	noIndex.NoIndexUsed = 10
	noIndex.TotalLatencyMillis = 1000
	second := query("n2", 500000, 10, "SELECT `id` FROM `orders` WHERE `token` = ?")
	second.NoIndexUsed = 1
	findings = apply(noIndex, second)
	if _, ok := findings["query_no_index_app_n"]; !ok {
		t.Fatal("expected query_no_index")
	}
	if _, ok := findings["query_read_amplification_app_n"]; ok {
		t.Fatal("read amplification duplicated query_no_index for the same digest")
	}
	if _, ok := findings["query_read_amplification_app_n2"]; !ok {
		t.Fatalf("second no-index digest was reported by nothing: %v", findings)
	}

	// A digest under a system default database is exempt without a schema prefix.
	system := query("sd", 5000000, 10, "SELECT COUNT ( * ) FROM `setup_instruments` WHERE `ENABLED` = ?")
	system.Schema = "performance_schema"
	if _, ok := apply(system)["query_read_amplification_performance_schema_sd"]; ok {
		t.Fatal("system-schema default database should be exempt")
	}

	for name, q := range map[string]model.Query{
		"writes return nothing":                 query("w", 5000000, 0, "UPDATE `orders` SET `status` = ? WHERE `id` = ?"),
		"ratio below 100":                       query("r", 500000, 10000, "SELECT `id` FROM `orders` WHERE `email` = ?"),
		"ratio exactly 99":                      query("e", 990000, 10000, "SELECT `id` FROM `orders` WHERE `email` = ?"),
		"under 1000 examined per call":          query("p", 9990, 1, "SELECT `id` FROM `orders` WHERE `email` = ?"),
		"unfiltered group by":                   query("g", 5000000, 10, "SELECT `status` , `id` FROM `orders` GROUP BY `status`"),
		"unfiltered aggregate":                  query("s", 5000000, 10, "SELECT SUM ( `amount` ) FROM `orders`"),
		"unfiltered group_concat":               query("gc", 5000000, 10, "SELECT GROUP_CONCAT ( `tag` ) FROM `tags`"),
		"performance schema read":               query("ps", 5000000, 10, "SELECT `ERROR_NUMBER` FROM `performance_schema` . `events_errors_summary_global_by_error` WHERE `SUM_ERROR_RAISED` > ?"),
		"mysql schema read":                     query("ms", 5000000, 10, "SELECT `User` FROM `mysql` . `user` WHERE `Host` = ?"),
		"unfiltered join rollup":                query("j", 5000000, 10, "SELECT `d` . `name` , SUM ( `f` . `amt` ) FROM `dim` `d` JOIN `fact` `f` ON `f` . `dim_id` = `d` . `id` GROUP BY `d` . `name`"),
		"window function":                       query("wf", 5000000, 10, "SELECT `id` , ROW_NUMBER ( ) OVER ( ORDER BY `created` ) FROM `fact` LIMIT ?"),
		"column named count":                    query("cc", 5000000, 10, "SELECT `count` , SUM ( `amount` ) FROM `orders`"),
		"unfiltered join rollup with limit":     query("jl", 5000000, 10, "SELECT `d` . `name` , SUM ( `f` . `amt` ) FROM `dim` `d` JOIN `fact` `f` ON `f` . `dim_id` = `d` . `id` GROUP BY `d` . `name` ORDER BY ? LIMIT ?"),
		"named window":                          query("nw", 5000000, 10, "SELECT `id` , ROW_NUMBER ( ) OVER `w` FROM `fact` WINDOW `w` AS ( ORDER BY `created` ) LIMIT ?"),
		"unfiltered aggregate before intersect": query("is", 5000000, 10, "SELECT COUNT ( * ) FROM `a` JOIN `b` ON `a` . `id` = `b` . `a_id` INTERSECT SELECT ? FROM `c`"),
		"fewer than five calls": func() model.Query {
			q := query("f", 5000000, 10, "SELECT `id` FROM `orders` WHERE `email` = ?")
			q.Calls = 4
			return q
		}(),
	} {
		if _, ok := apply(q)["query_read_amplification_app_"+q.Digest]; ok {
			t.Fatalf("%s should not trigger read amplification", name)
		}
	}
	for name, q := range map[string]model.Query{
		"filtered aggregate can use a better index": query("fa", 5000000, 10, "SELECT SUM ( `amount` ) FROM `orders` WHERE `status` = ?"),
		"aggregate in a subquery":                   query("sq", 5000000, 10, "SELECT `id` FROM `orders` WHERE `total` > ( SELECT AVG ( `total` ) FROM `orders` )"),
		"function name ending in an aggregate name": query("cs", 5000000, 10, "SELECT `id` FROM `orders` WHERE CHECKSUM ( `blob` ) = ?"),
		"distinct scan":                             query("d", 5000000, 10, "SELECT DISTINCTROW `user_id` FROM `events`"),
		"aggregate name inside another function":    query("bc", 5000000, 10, "SELECT `id` FROM `t` ORDER BY BIT_COUNT ( `mask` )"),
		"column named count without aggregate":      query("cn", 5000000, 10, "SELECT `count` FROM `t` ORDER BY `count` LIMIT ?"),
		"filter inside a join condition":            query("on", 5000000, 10, "SELECT `d` . `name` , SUM ( `f` . `amt` ) FROM `dim` `d` JOIN `fact` `f` ON `f` . `dim_id` = `d` . `id` AND `f` . `status` = ? GROUP BY `d` . `name`"),
		"filter inside a second join condition":     query("on2", 5000000, 10, "SELECT COUNT ( * ) FROM `a` JOIN `b` ON `a` . `id` = `b` . `a_id` JOIN `c` ON `c` . `b_id` = `b` . `id` AND `c` . `kind` = ? GROUP BY `a` . `id`"),
		"alias containing the word over":            query("ov", 5000000, 10, "SELECT `id` AS `hits over time` FROM `t` ORDER BY `id` LIMIT ?"),
	} {
		if _, ok := apply(q)["query_read_amplification_app_"+q.Digest]; !ok {
			t.Fatalf("%s should trigger read amplification", name)
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

func TestDisabledPerformanceSchemaCannotVerifyEmptySuccessfulProbes(t *testing.T) {
	ctx := &model.Context{Metrics: model.Metrics{BufferPoolHitPercent: 100}, Variables: map[string]string{"performance_schema": "OFF"}, GlobalStatus: map[string]string{"Uptime": "100"}}
	for _, name := range []string{"global variables", "process list", "statement counters", "statement digests", "statement database time", "instrumentation coverage", "index statistics", "table statistics", "row lock waits", "active transactions", "metadata locks", "InnoDB monitor", "replication"} {
		ctx.Capabilities = append(ctx.Capabilities, model.Capability{Name: name, Available: true})
	}
	Apply(ctx)
	for _, name := range []string{"workload", "queries", "indexes", "tables", "locks", "instrumentation"} {
		if ctx.Health.Subsystem(name).Complete {
			t.Errorf("disabled instrumentation verified %s", name)
		}
	}
}
