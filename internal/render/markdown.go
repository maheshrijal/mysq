package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/maheshrijal/mysq/internal/model"
)

func Markdown(w io.Writer, ctx *model.Context) error {
	var out strings.Builder
	fmt.Fprintf(&out, "# mysq report\n\n")
	fmt.Fprintf(&out, "Collected `%s` from `%s:%d/%s` running %s %s. Snapshot `%s`.\n\n",
		ctx.CollectedAt.Format("2006-01-02T15:04:05Z"), ctx.Server.Host, ctx.Server.Port,
		ctx.Server.Database, ctx.Server.Flavor, ctx.Server.Version, ctx.Fingerprint)
	fmt.Fprintf(&out, "## Health: %d/100\n\n", ctx.Health.Score)
	fmt.Fprintf(&out, "| QPS | TPS | Connections | Running | Buffer hit | Temp on disk |\n")
	fmt.Fprintf(&out, "|---:|---:|---:|---:|---:|---:|\n")
	fmt.Fprintf(&out, "| %.2f | %.2f | %d/%d | %d | %.2f%% | %.2f%% |\n\n",
		ctx.Metrics.QueriesPerSecond, ctx.Metrics.TransactionsPerSecond,
		ctx.Metrics.ConnectionsCurrent, ctx.Metrics.ConnectionsMax, ctx.Metrics.ThreadsRunning,
		ctx.Metrics.BufferPoolHitPercent, ctx.Metrics.TempDiskTablePercent)
	fmt.Fprintf(&out, "| Statement p95 | Statement p99 | Maximum | Errors/s | Warnings/s | Deadlocks/s |\n")
	fmt.Fprintf(&out, "|---:|---:|---:|---:|---:|---:|\n")
	fmt.Fprintf(&out, "| %s | %s | %s | %.2f | %.2f | %.2f |\n\n", duration(ctx.StatementLatency.P95Millis),
		duration(ctx.StatementLatency.P99Millis), duration(ctx.StatementLatency.MaxMillis),
		ctx.Metrics.StatementErrorsPerSec, ctx.Metrics.StatementWarningsPerSec, ctx.Metrics.DeadlocksPerSecond)
	fmt.Fprintf(&out, "| Data reads/s | Data writes/s | Fsyncs/s | Redo/s | Checkpoint age | Pending I/O |\n")
	fmt.Fprintf(&out, "|---:|---:|---:|---:|---:|---:|\n")
	fmt.Fprintf(&out, "| %.2f | %.2f | %.2f | %s | %s (%.2f%%) | %d/%d/%d |\n\n",
		ctx.Metrics.DataReadsPerSecond, ctx.Metrics.DataWritesPerSecond, ctx.Metrics.DataFsyncsPerSecond,
		humanBytes(uint64(ctx.Metrics.RedoBytesPerSecond)), humanBytes(ctx.Metrics.RedoCheckpointAgeBytes),
		ctx.Metrics.RedoCheckpointAgePct, ctx.Metrics.PendingReads, ctx.Metrics.PendingWrites, ctx.Metrics.PendingFsyncs)
	if len(ctx.Findings) == 0 {
		out.WriteString("No actionable findings were detected in the collected signals.\n")
	} else {
		out.WriteString("## Findings\n\n")
		for _, finding := range ctx.Findings {
			fmt.Fprintf(&out, "### %s: %s\n\n%s\n\nRecommended: %s\n\n", strings.ToUpper(string(finding.Severity)), finding.Title, finding.Summary, finding.Recommendation)
		}
	}
	if len(ctx.Queries) > 0 {
		out.WriteString("## Top statements\n\n| Total | Calls | Mean | p95 | p99 | Errors/warnings | Active user | Digest | Statement |\n|---:|---:|---:|---:|---:|---:|---|---|---|\n")
		for _, query := range ctx.Queries[:min(10, len(ctx.Queries))] {
			users := "—"
			if len(query.ActiveUsers) > 0 {
				users = strings.Join(query.ActiveUsers, ", ")
			}
			fmt.Fprintf(&out, "| %s | %d | %.2fms | %s | %s | %d/%d | %s | `%s` | `%s` |\n", duration(query.TotalLatencyMillis),
				query.Calls, query.MeanLatencyMillis, duration(query.P95LatencyMillis), duration(query.P99LatencyMillis),
				query.Errors, query.Warnings, users, query.Digest, escapeMarkdown(query.Statement))
		}
		out.WriteByte('\n')
	}
	if len(ctx.StatementSamples) > 0 {
		out.WriteString("## Current statement database time\n\n| Share | Database time/s | Calls/s | Schema | Statement |\n|---:|---:|---:|---|---|\n")
		for _, item := range ctx.StatementSamples[:min(10, len(ctx.StatementSamples))] {
			fmt.Fprintf(&out, "| %.1f%% | %s/s | %.2f | `%s` | `%s` |\n", item.DatabaseTimeSharePercent,
				duration(item.DatabaseTimeMillisPerSecond), item.CallsPerSecond, item.Schema, escapeMarkdown(item.Statement))
		}
		out.WriteByte('\n')
	}
	if len(ctx.WaitEvents) > 0 {
		out.WriteString("## Sampled wait pressure\n\n| Event | Sample share | Wait/s | Events/s | Cumulative total |\n|---|---:|---:|---:|---:|\n")
		for _, wait := range ctx.WaitEvents[:min(10, len(ctx.WaitEvents))] {
			fmt.Fprintf(&out, "| `%s` | %.1f%% | %s/s | %.2f | %s |\n", wait.Name, wait.SampleSharePercent,
				duration(wait.WaitMillisPerSecond), wait.EventsPerSecond, duration(wait.TotalLatencyMillis))
		}
		out.WriteByte('\n')
	}
	if len(ctx.FileIO) > 0 {
		out.WriteString("## MySQL file I/O\n\n| Instrument | Reads/s | Writes/s | Read latency | Write latency | Wait/s |\n|---|---:|---:|---:|---:|---:|\n")
		for _, item := range ctx.FileIO[:min(10, len(ctx.FileIO))] {
			fmt.Fprintf(&out, "| `%s` | %.2f | %.2f | %s | %s | %s/s |\n", item.Name, item.ReadsPerSecond,
				item.WritesPerSecond, duration(item.MeanReadLatencyMillis), duration(item.MeanWriteLatencyMillis), duration(item.WaitMillisPerSecond))
		}
		out.WriteByte('\n')
	}
	if len(ctx.ServerErrors) > 0 {
		out.WriteString("## MySQL errors and warnings\n\n| Error | Sample/s | Total | Last seen | Name |\n|---:|---:|---:|---|---|\n")
		for _, item := range ctx.ServerErrors[:min(10, len(ctx.ServerErrors))] {
			fmt.Fprintf(&out, "| %d | %.2f | %d | %s | `%s` |\n", item.Number, item.RaisedPerSecond, item.Raised, item.LastSeen, item.Name)
		}
		out.WriteByte('\n')
	}
	if len(ctx.Tables) > 0 {
		out.WriteString("## Largest tables\n\n| Table | Size | Rows | Reads | Writes | Primary key |\n|---|---:|---:|---:|---:|---|\n")
		for _, table := range ctx.Tables[:min(10, len(ctx.Tables))] {
			fmt.Fprintf(&out, "| `%s.%s` | %s | %d | %d | %d | %t |\n", table.Schema, table.Name, humanBytes(table.TotalBytes), table.EstimatedRows, table.Reads, table.Writes, table.HasPrimaryKey)
		}
		out.WriteByte('\n')
	}
	fmt.Fprintf(&out, "Instrumentation: %d/%d digest slots used (%.1f%%), %d lost events, disabled consumers: `%s`.\n\n",
		ctx.Instrumentation.DigestRows, ctx.Instrumentation.DigestCapacity, ctx.Instrumentation.DigestUtilizationPercent,
		ctx.Instrumentation.TotalLost, strings.Join(ctx.Instrumentation.DisabledConsumers, ", "))
	out.WriteString("All statement text is normalized and literal values are redacted. Counter-derived conclusions are scoped to the server uptime and per-family windows in `context.json.sample_intervals_ms`.\n")
	_, err := io.WriteString(w, out.String())
	return err
}

func escapeMarkdown(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	return strings.ReplaceAll(value, "`", "'")
}
