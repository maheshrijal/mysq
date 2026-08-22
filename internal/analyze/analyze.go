package analyze

import (
	"fmt"
	"sort"
	"strings"

	"github.com/maheshrijal/mysq/internal/model"
)

var subsystems = []string{
	"connections", "workload", "queries", "indexes", "tables", "locks",
	"buffer pool", "temporary tables", "replication", "durability", "instrumentation",
}

func Apply(ctx *model.Context) {
	findings := make([]model.Finding, 0)
	add := func(f model.Finding) { findings = append(findings, f) }

	analyzeConnections(ctx, add)
	analyzeWorkload(ctx, add)
	analyzeQueries(ctx, add)
	analyzeIndexes(ctx, add)
	analyzeTables(ctx, add)
	analyzeLocks(ctx, add)
	analyzeBufferPool(ctx, add)
	analyzeTemporaryTables(ctx, add)
	analyzeReplication(ctx, add)
	analyzeDurability(ctx, add)
	analyzeInstrumentation(ctx, add)

	sort.SliceStable(findings, func(i, j int) bool {
		a, b := severityRank(findings[i].Severity), severityRank(findings[j].Severity)
		if a != b {
			return a < b
		}
		if findings[i].Subsystem != findings[j].Subsystem {
			return findings[i].Subsystem < findings[j].Subsystem
		}
		return findings[i].ID < findings[j].ID
	})
	ctx.Findings = findings
	ctx.Health = score(findings)
}

func analyzeConnections(ctx *model.Context, add func(model.Finding)) {
	pct := ctx.Metrics.ConnectionsUsedPercent
	if pct >= 90 {
		add(finding("connection_saturation", model.SeverityCritical, "connections",
			"Connection capacity is nearly exhausted",
			fmt.Sprintf("%d of %d connections are in use (%.1f%%).", ctx.Metrics.ConnectionsCurrent, ctx.Metrics.ConnectionsMax, pct),
			"Reduce leaked or idle connections and verify pool limits before increasing max_connections.",
			map[string]any{"used": ctx.Metrics.ConnectionsCurrent, "max": ctx.Metrics.ConnectionsMax, "percent": pct}))
	} else if pct >= 75 {
		add(finding("connection_pressure", model.SeverityWarning, "connections",
			"Connection usage has little headroom",
			fmt.Sprintf("%d of %d connections are in use (%.1f%%).", ctx.Metrics.ConnectionsCurrent, ctx.Metrics.ConnectionsMax, pct),
			"Inspect application pool sizing and connection lifetime before capacity is exhausted.",
			map[string]any{"used": ctx.Metrics.ConnectionsCurrent, "max": ctx.Metrics.ConnectionsMax, "percent": pct}))
	}
	if ctx.Metrics.AbortedConnectsPerSec > 0.5 {
		add(finding("aborted_connects", model.SeverityWarning, "connections",
			"Clients are failing to connect",
			fmt.Sprintf("Aborted connections increased at %.2f/s during the sample.", ctx.Metrics.AbortedConnectsPerSec),
			"Check authentication failures, client timeouts, network resets, and max_connect_errors.",
			map[string]any{"per_second": ctx.Metrics.AbortedConnectsPerSec}))
	}
}

func analyzeWorkload(ctx *model.Context, add func(model.Finding)) {
	longest := uint64(0)
	count := 0
	objects := make([]string, 0)
	for _, process := range ctx.Processes {
		// MySQL exposes long-lived server threads such as event_scheduler in
		// processlist with Command=Daemon. They are not statements. Requiring
		// normalized statement text avoids turning server uptime into a false
		// critical finding while still covering Query/Execute-style work.
		if process.Seconds < 5 || strings.TrimSpace(process.Statement) == "" ||
			strings.EqualFold(process.Command, "Sleep") || strings.EqualFold(process.Command, "Daemon") {
			continue
		}
		count++
		if process.Seconds > longest {
			longest = process.Seconds
		}
		if len(objects) < 5 {
			objects = append(objects, fmt.Sprintf("connection:%d", process.ID))
		}
	}
	if count == 0 {
		return
	}
	severity := model.SeverityWarning
	if longest >= 30 {
		severity = model.SeverityCritical
	}
	f := finding("long_running_statements", severity, "workload",
		"Long-running statements are active",
		fmt.Sprintf("%d non-sleeping statement(s) have run for at least 5s; the longest is %ds.", count, longest),
		"Inspect the normalized statements and their execution plans; cancel only after checking transaction and application impact.",
		map[string]any{"count": count, "longest_seconds": longest})
	f.Objects = objects
	add(f)
}

func analyzeQueries(ctx *model.Context, add func(model.Finding)) {
	var total float64
	for _, query := range ctx.Queries {
		total += query.TotalLatencyMillis
	}
	if total == 0 {
		return
	}
	for _, query := range ctx.Queries {
		share := query.TotalLatencyMillis * 100 / total
		if query.Calls < 5 || (share < 35 && query.MeanLatencyMillis < 250) {
			continue
		}
		severity := model.SeverityNote
		if share >= 50 || query.MeanLatencyMillis >= 500 {
			severity = model.SeverityWarning
		}
		f := finding("expensive_query_"+shortDigest(query.Digest), severity, "queries",
			"A statement dominates database time",
			fmt.Sprintf("Digest %s accounts for %.1f%% of captured statement latency across %d calls (%.2fms mean).", shortDigest(query.Digest), share, query.Calls, query.MeanLatencyMillis),
			"Review EXPLAIN for the normalized statement and compare rows examined with rows returned.",
			map[string]any{"digest": query.Digest, "share_percent": share, "calls": query.Calls, "mean_latency_ms": query.MeanLatencyMillis})
		f.Objects = []string{"digest:" + query.Digest}
		add(f)
		break
	}
	for _, query := range ctx.Queries {
		if query.NoIndexUsed == 0 || query.Calls < 5 {
			continue
		}
		f := finding("query_no_index_"+shortDigest(query.Digest), model.SeverityWarning, "queries",
			"A recurring statement is not using an index",
			fmt.Sprintf("Digest %s reported %d executions without an index.", shortDigest(query.Digest), query.NoIndexUsed),
			"Use EXPLAIN to confirm access paths; add or reshape an index only after validating selectivity and write cost.",
			map[string]any{"digest": query.Digest, "no_index_used": query.NoIndexUsed, "calls": query.Calls})
		f.Objects = []string{"digest:" + query.Digest}
		add(f)
		break
	}
}

func analyzeIndexes(ctx *model.Context, add func(model.Finding)) {
	type key struct{ schema, table, columns string }
	seen := map[key]model.Index{}
	duplicates := make([]string, 0)
	unused := make([]string, 0)
	for _, index := range ctx.Indexes {
		object := index.Schema + "." + index.Table + "." + index.Name
		k := key{index.Schema, index.Table, strings.ToLower(index.Columns)}
		if previous, ok := seen[k]; ok && index.Name != "PRIMARY" && previous.Name != "PRIMARY" {
			duplicates = append(duplicates, previous.Name+" / "+object)
		} else {
			seen[k] = index
		}
		if index.Name != "PRIMARY" && index.Reads == 0 && index.Writes > 0 {
			unused = append(unused, object)
		}
	}
	if len(duplicates) > 0 {
		f := finding("duplicate_indexes", model.SeverityWarning, "indexes",
			"Duplicate index definitions add write cost",
			fmt.Sprintf("%d duplicate index definition(s) were found.", len(duplicates)),
			"Confirm constraints, prefix lengths, and workload use before removing any duplicate index.",
			map[string]any{"count": len(duplicates)})
		f.Objects = firstN(duplicates, 10)
		add(f)
	}
	if len(unused) > 0 {
		f := finding("unused_indexes", model.SeverityNote, "indexes",
			"Indexes receive writes but recorded no reads",
			fmt.Sprintf("%d secondary index(es) have writes but zero reads since Performance Schema counters reset.", len(unused)),
			"Treat this as a review list, not a drop list; verify uptime, replicas, reporting jobs, and a representative workload window.",
			map[string]any{"count": len(unused), "uptime_seconds": ctx.Server.UptimeSeconds})
		f.Objects = firstN(unused, 10)
		add(f)
	}
}

func analyzeTables(ctx *model.Context, add func(model.Finding)) {
	missing := make([]string, 0)
	for _, table := range ctx.Tables {
		if !table.HasPrimaryKey {
			missing = append(missing, table.Schema+"."+table.Name)
		}
	}
	if len(missing) > 0 {
		f := finding("tables_without_primary_key", model.SeverityWarning, "tables",
			"InnoDB tables are missing primary keys",
			fmt.Sprintf("%d table(s) have no primary key, causing hidden row IDs and making row-based replication less efficient.", len(missing)),
			"Choose stable, narrow primary keys based on domain identity; validate duplicates before adding constraints.",
			map[string]any{"count": len(missing)})
		f.Objects = firstN(missing, 10)
		add(f)
	}
}

func analyzeLocks(ctx *model.Context, add func(model.Finding)) {
	if len(ctx.Locks) > 0 {
		objects := make([]string, 0, len(ctx.Locks))
		for _, lock := range ctx.Locks {
			objects = append(objects, lock.Schema+"."+lock.Table)
		}
		f := finding("active_lock_waits", model.SeverityCritical, "locks",
			"Transactions are blocked on row locks",
			fmt.Sprintf("%d active InnoDB lock wait(s) were captured.", len(ctx.Locks)),
			"Identify the blocking transaction and application owner before choosing whether to wait, cancel, or change transaction scope.",
			map[string]any{"count": len(ctx.Locks)})
		f.Objects = firstN(objects, 10)
		add(f)
	} else if ctx.Metrics.RowLockWaitsPerSecond >= 1 {
		add(finding("row_lock_churn", model.SeverityWarning, "locks",
			"Row lock waits are accumulating",
			fmt.Sprintf("InnoDB row lock waits increased at %.2f/s during the sample.", ctx.Metrics.RowLockWaitsPerSecond),
			"Inspect transaction duration, row access order, and the highest-contention statements.",
			map[string]any{"per_second": ctx.Metrics.RowLockWaitsPerSecond}))
	}
}

func analyzeBufferPool(ctx *model.Context, add func(model.Finding)) {
	hit := ctx.Metrics.BufferPoolHitPercent
	if hit < 95 {
		add(finding("low_buffer_pool_hit", model.SeverityCritical, "buffer pool",
			"Buffer pool misses are driving physical reads",
			fmt.Sprintf("The sampled InnoDB buffer pool hit ratio is %.2f%%.", hit),
			"Confirm the sample under sustained load, then inspect working-set size and memory allocation before resizing the buffer pool.",
			map[string]any{"hit_percent": hit}))
	} else if hit < 99 {
		add(finding("buffer_pool_misses", model.SeverityWarning, "buffer pool",
			"Buffer pool hit ratio is below the usual OLTP range",
			fmt.Sprintf("The sampled InnoDB buffer pool hit ratio is %.2f%%.", hit),
			"Correlate physical reads with workload changes and verify that the active working set fits memory.",
			map[string]any{"hit_percent": hit}))
	}
	if ctx.Metrics.BufferPoolDirtyPercent >= 75 {
		add(finding("dirty_page_pressure", model.SeverityWarning, "buffer pool",
			"Dirty pages occupy most of the buffer pool",
			fmt.Sprintf("Dirty pages are %.1f%% of buffer pool pages.", ctx.Metrics.BufferPoolDirtyPercent),
			"Check checkpoint age, redo capacity, storage latency, and page-cleaner throughput.",
			map[string]any{"dirty_percent": ctx.Metrics.BufferPoolDirtyPercent}))
	}
	if ctx.Metrics.HistoryListLength >= 1_000_000 {
		add(finding("purge_severely_behind", model.SeverityCritical, "buffer pool",
			"InnoDB purge history is severely backed up",
			fmt.Sprintf("History list length is %d.", ctx.Metrics.HistoryListLength),
			"Find old consistent reads and long transactions; avoid killing transactions without assessing rollback cost.",
			map[string]any{"history_list_length": ctx.Metrics.HistoryListLength}))
	} else if ctx.Metrics.HistoryListLength >= 100_000 {
		add(finding("purge_behind", model.SeverityWarning, "buffer pool",
			"InnoDB purge history is growing",
			fmt.Sprintf("History list length is %d.", ctx.Metrics.HistoryListLength),
			"Inspect long-running transactions and consistent reads that prevent purge progress.",
			map[string]any{"history_list_length": ctx.Metrics.HistoryListLength}))
	}
}

func analyzeTemporaryTables(ctx *model.Context, add func(model.Finding)) {
	if ctx.Metrics.TempDiskTablePercent >= 25 {
		add(finding("disk_temp_tables", model.SeverityWarning, "temporary tables",
			"Temporary tables are frequently spilling to disk",
			fmt.Sprintf("%.1f%% of temporary tables created during the sample were on disk.", ctx.Metrics.TempDiskTablePercent),
			"Inspect GROUP BY, ORDER BY, DISTINCT, BLOB/TEXT use, and per-session temp table limits before raising memory settings.",
			map[string]any{"disk_percent": ctx.Metrics.TempDiskTablePercent}))
	}
}

func analyzeReplication(ctx *model.Context, add func(model.Finding)) {
	if ctx.Replication == nil {
		return
	}
	r := ctx.Replication
	if !strings.EqualFold(r.IORunning, "Yes") || !strings.EqualFold(r.SQLRunning, "Yes") {
		add(finding("replication_stopped", model.SeverityCritical, "replication",
			"Replica threads are not both running",
			fmt.Sprintf("I/O thread=%q, SQL thread=%q.", r.IORunning, r.SQLRunning),
			"Inspect the last I/O and SQL errors and preserve relay-log evidence before restarting replication.",
			map[string]any{"io_running": r.IORunning, "sql_running": r.SQLRunning, "last_io_error": r.LastIOError, "last_sql_error": r.LastSQLError}))
	} else if r.SecondsBehind != nil && *r.SecondsBehind > 60 {
		add(finding("replication_lag", model.SeverityWarning, "replication",
			"Replica lag is elevated",
			fmt.Sprintf("Replica reports %ds behind its source.", *r.SecondsBehind),
			"Check apply-thread utilization, long transactions, network throughput, and source write bursts.",
			map[string]any{"seconds_behind": *r.SecondsBehind}))
	}
}

func analyzeDurability(ctx *model.Context, add func(model.Finding)) {
	flush := ctx.Variables["innodb_flush_log_at_trx_commit"]
	if flush != "" && flush != "1" {
		add(finding("relaxed_innodb_durability", model.SeverityCritical, "durability",
			"Committed transactions may be lost after a crash",
			"innodb_flush_log_at_trx_commit is "+flush+", not 1.",
			"Use 1 when durable commits are required; change only with an explicit, documented data-loss tradeoff.",
			map[string]any{"innodb_flush_log_at_trx_commit": flush}))
	}
	if strings.EqualFold(ctx.Variables["log_bin"], "ON") {
		syncBinlog := ctx.Variables["sync_binlog"]
		if syncBinlog != "" && syncBinlog != "1" {
			add(finding("relaxed_binlog_durability", model.SeverityWarning, "durability",
				"Binary log durability is relaxed",
				"sync_binlog is "+syncBinlog+", not 1.",
				"Use 1 for crash-safe replication and point-in-time recovery, unless the durability tradeoff is intentional.",
				map[string]any{"sync_binlog": syncBinlog}))
		}
	}
	if strings.EqualFold(ctx.Variables["skip_name_resolve"], "OFF") {
		add(finding("name_resolution_enabled", model.SeverityNote, "connections",
			"Connection authentication may depend on DNS",
			"skip_name_resolve is OFF.",
			"Prefer IP-based host entries and enable skip_name_resolve where DNS lookup latency or outages are a risk.",
			map[string]any{"skip_name_resolve": "OFF"}))
	}
}

func analyzeInstrumentation(ctx *model.Context, add func(model.Finding)) {
	if !ctx.Server.PerformanceSchema {
		add(finding("performance_schema_disabled", model.SeverityWarning, "instrumentation",
			"Performance Schema is disabled",
			"Statement digests, index usage, waits, and table I/O cannot be inspected.",
			"Enable Performance Schema at server startup and size its consumers for the diagnostic coverage you need.", nil))
		return
	}
	unavailable := 0
	for _, capability := range ctx.Capabilities {
		if !capability.Available && capability.Name != "replication" {
			unavailable++
		}
	}
	if unavailable > 0 {
		add(finding("partial_visibility", model.SeverityNote, "instrumentation",
			"Some diagnostic probes were unavailable",
			fmt.Sprintf("%d probe(s) could not be collected; see collection_warnings for exact errors.", unavailable),
			"Grant only the documented monitoring privileges needed for the missing probes, or accept the explicitly reduced coverage.",
			map[string]any{"unavailable": unavailable}))
	}
}

func finding(id string, severity model.Severity, subsystem, title, summary, recommendation string, evidence map[string]any) model.Finding {
	return model.Finding{
		ID: id, Severity: severity, Subsystem: subsystem, Title: title,
		Summary: summary, Recommendation: recommendation, Evidence: evidence,
	}
}

func score(findings []model.Finding) model.Health {
	health := model.Health{Score: 100}
	affected := map[string]bool{}
	for _, finding := range findings {
		affected[finding.Subsystem] = true
		switch finding.Severity {
		case model.SeverityCritical:
			health.Critical++
			health.Score -= 20
		case model.SeverityWarning:
			health.Warnings++
			health.Score -= 8
		case model.SeverityNote:
			health.Notes++
			health.Score -= 2
		}
	}
	if health.Score < 0 {
		health.Score = 0
	}
	for _, subsystem := range subsystems {
		if !affected[subsystem] {
			health.Healthy++
		}
	}
	return health
}

func severityRank(value model.Severity) int {
	switch value {
	case model.SeverityCritical:
		return 0
	case model.SeverityWarning:
		return 1
	default:
		return 2
	}
}

func shortDigest(value string) string {
	if len(value) > 8 {
		return strings.ToLower(value[:8])
	}
	if value == "" {
		return "unknown"
	}
	return strings.ToLower(value)
}

func firstN(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return values[:n]
}
