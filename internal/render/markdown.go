package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/maheshrijal/mysqldot/internal/model"
)

func Markdown(w io.Writer, ctx *model.Context) error {
	var out strings.Builder
	fmt.Fprintf(&out, "# mysqldot report\n\n")
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
	if len(ctx.Findings) == 0 {
		out.WriteString("No actionable findings were detected in the collected signals.\n")
	} else {
		out.WriteString("## Findings\n\n")
		for _, finding := range ctx.Findings {
			fmt.Fprintf(&out, "### %s: %s\n\n%s\n\nRecommended: %s\n\n", strings.ToUpper(string(finding.Severity)), finding.Title, finding.Summary, finding.Recommendation)
		}
	}
	if len(ctx.Queries) > 0 {
		out.WriteString("## Top statements\n\n| Total | Calls | Mean | Digest | Statement |\n|---:|---:|---:|---|---|\n")
		for _, query := range ctx.Queries[:min(10, len(ctx.Queries))] {
			fmt.Fprintf(&out, "| %s | %d | %.2fms | `%s` | `%s` |\n", duration(query.TotalLatencyMillis), query.Calls, query.MeanLatencyMillis, query.Digest, escapeMarkdown(query.Statement))
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
	out.WriteString("All statement text is normalized and literal values are redacted. Counter-derived conclusions are scoped to the server uptime and sample interval shown in `context.json`.\n")
	_, err := io.WriteString(w, out.String())
	return err
}

func escapeMarkdown(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	return strings.ReplaceAll(value, "`", "'")
}
