package render

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/maheshrijal/mysqldot/internal/model"
)

type Options struct {
	Full  bool
	Color bool
	Width int
}

type palette struct {
	muted, text, green, yellow, red, cyan, border lipgloss.Style
}

func Text(w io.Writer, ctx *model.Context, options Options) error {
	if options.Width < 60 {
		options.Width = 96
	}
	p := newPalette(options.Color)
	var out strings.Builder

	title := p.cyan.Bold(true).Render("◆ MYSQLDOT") + p.muted.Render("  MySQL intelligence, from the terminal")
	out.WriteString(title + "\n")
	out.WriteString(p.border.Render(strings.Repeat("─", min(options.Width, 104))) + "\n")
	access := "primary"
	if ctx.Server.ReadOnly || ctx.Server.SuperReadOnly {
		access = "read-only server"
	}
	identity := fmt.Sprintf("connected · %s:%d/%s · %s %s · %s · %.1fs sample",
		ctx.Server.Host, ctx.Server.Port, fallback(ctx.Server.Database, "(no database)"),
		ctx.Server.Flavor, ctx.Server.Version, access, float64(ctx.IntervalMillis)/1000)
	out.WriteString(p.muted.Render(identity) + "\n\n")

	scoreStyle := p.green
	if ctx.Health.Critical > 0 {
		scoreStyle = p.red
	} else if ctx.Health.Warnings > 0 {
		scoreStyle = p.yellow
	}
	score := fmt.Sprintf("Database health  %s  %3d/100", healthBar(ctx.Health.Score, 24, scoreStyle, p.muted), ctx.Health.Score)
	out.WriteString(scoreStyle.Bold(true).Render(score) + "\n")
	out.WriteString(renderMetricStrip(ctx, p) + "\n\n")

	if len(ctx.Findings) == 0 {
		out.WriteString(p.green.Bold(true).Render("GOOD") + "\n")
		out.WriteString(p.green.Render("● No actionable findings in the collected signals.") + "\n")
	} else {
		for _, severity := range []model.Severity{model.SeverityCritical, model.SeverityWarning, model.SeverityNote} {
			items := findingsBySeverity(ctx.Findings, severity)
			if len(items) == 0 {
				continue
			}
			style, label := severityStyle(p, severity)
			out.WriteString(style.Bold(true).Render(label) + "\n")
			for _, finding := range items {
				out.WriteString(style.Render("● "+finding.Title) + p.muted.Render("  "+finding.Summary) + "\n")
				if options.Full {
					out.WriteString(p.muted.Render("  ↳ "+finding.Recommendation) + "\n")
				}
			}
			out.WriteString("\n")
		}
	}

	healthy := healthySubsystems(ctx.Findings)
	if len(healthy) > 0 {
		out.WriteString(p.green.Bold(true).Render("GOOD") + "\n")
		out.WriteString(p.green.Render("● ") + p.muted.Render(strings.Join(healthy, " · ")) + "\n\n")
	}

	if options.Full {
		out.WriteString(renderFull(ctx, p, options.Width))
	} else {
		out.WriteString(p.muted.Render("Details: mysqldot inspect --full   ·   Agent bundle: mysqldot export   ·   Interactive: mysqldot tui") + "\n")
	}
	_, err := io.WriteString(w, out.String())
	return err
}

func newPalette(color bool) palette {
	style := func(light, dark string) lipgloss.Style {
		result := lipgloss.NewStyle()
		if color {
			result = result.Foreground(lipgloss.AdaptiveColor{Light: light, Dark: dark})
		}
		return result
	}
	return palette{
		muted:  style("#7C7F93", "#737AA2"),
		text:   style("#4C4F69", "#C0CAF5"),
		green:  style("#40A02B", "#9ECE6A"),
		yellow: style("#DF8E1D", "#E0AF68"),
		red:    style("#D20F39", "#F7768E"),
		cyan:   style("#1E66F5", "#7AA2F7"),
		border: style("#BCC0CC", "#3B4261"),
	}
}

func renderMetricStrip(ctx *model.Context, p palette) string {
	connections := fmt.Sprintf("connections %d/%d", ctx.Metrics.ConnectionsCurrent, ctx.Metrics.ConnectionsMax)
	values := []string{
		fmt.Sprintf("qps %.1f", ctx.Metrics.QueriesPerSecond),
		fmt.Sprintf("tps %.1f", ctx.Metrics.TransactionsPerSecond),
		fmt.Sprintf("running %d", ctx.Metrics.ThreadsRunning),
		connections,
		fmt.Sprintf("cache %.2f%%", ctx.Metrics.BufferPoolHitPercent),
	}
	return p.cyan.Render(strings.Join(values, "  ·  "))
}

func renderFull(ctx *model.Context, p palette, width int) string {
	var out strings.Builder
	section := func(title string) {
		out.WriteString("\n" + p.cyan.Bold(true).Render(title) + "\n")
		out.WriteString(p.border.Render(strings.Repeat("─", min(width, 104))) + "\n")
	}

	section("SUBSYSTEM BOARD")
	rows := [][]string{
		{"connections", statusFor(ctx, "connections"), fmt.Sprintf("%.1f%%", ctx.Metrics.ConnectionsUsedPercent), fmt.Sprintf("%d/%d open", ctx.Metrics.ConnectionsCurrent, ctx.Metrics.ConnectionsMax)},
		{"workload", statusFor(ctx, "workload"), fmt.Sprintf("%.1f qps", ctx.Metrics.QueriesPerSecond), fmt.Sprintf("%d threads running", ctx.Metrics.ThreadsRunning)},
		{"buffer pool", statusFor(ctx, "buffer pool"), fmt.Sprintf("%.2f%% hit", ctx.Metrics.BufferPoolHitPercent), fmt.Sprintf("%.1f%% dirty", ctx.Metrics.BufferPoolDirtyPercent)},
		{"temporary tables", statusFor(ctx, "temporary tables"), fmt.Sprintf("%.1f%% disk", ctx.Metrics.TempDiskTablePercent), "sampled creations"},
		{"locks", statusFor(ctx, "locks"), fmt.Sprintf("%d waiting", len(ctx.Locks)), fmt.Sprintf("%.2f waits/s", ctx.Metrics.RowLockWaitsPerSecond)},
		{"replication", statusFor(ctx, "replication"), replicationValue(ctx), replicationNote(ctx)},
		{"durability", statusFor(ctx, "durability"), "redo + binlog", "commit safety"},
		{"instrumentation", statusFor(ctx, "instrumentation"), capabilityValue(ctx), "probe coverage"},
	}
	out.WriteString(table([]string{"subsystem", "status", "value", "note"}, rows, []int{20, 10, 18, 35}, p))

	if len(ctx.Queries) > 0 {
		section("TOP STATEMENT DIGESTS")
		queryRows := make([][]string, 0, min(10, len(ctx.Queries)))
		var total float64
		for _, query := range ctx.Queries {
			total += query.TotalLatencyMillis
		}
		for _, query := range ctx.Queries[:min(10, len(ctx.Queries))] {
			share := 0.0
			if total > 0 {
				share = query.TotalLatencyMillis * 100 / total
			}
			queryRows = append(queryRows, []string{
				duration(query.TotalLatencyMillis), fmt.Sprintf("%.1f%%", share), humanCount(query.Calls),
				fmt.Sprintf("%.2fms", query.MeanLatencyMillis), truncate(query.Statement, 52),
			})
		}
		out.WriteString(table([]string{"total", "share", "calls", "mean", "statement"}, queryRows, []int{10, 8, 10, 11, 54}, p))
		out.WriteString(p.muted.Render("Statement text is normalized; literals are never exported.") + "\n")
	}

	if len(ctx.Tables) > 0 {
		section("LARGEST TABLES")
		tableRows := make([][]string, 0, min(10, len(ctx.Tables)))
		for _, item := range ctx.Tables[:min(10, len(ctx.Tables))] {
			pk := "yes"
			if !item.HasPrimaryKey {
				pk = "NO"
			}
			tableRows = append(tableRows, []string{
				humanBytes(item.TotalBytes), humanCount(item.EstimatedRows), humanCount(item.Reads),
				humanCount(item.Writes), pk, item.Schema + "." + item.Name,
			})
		}
		out.WriteString(table([]string{"size", "rows", "reads", "writes", "pk", "table"}, tableRows, []int{11, 10, 10, 10, 5, 42}, p))
	}

	active := activeProcesses(ctx.Processes)
	if len(active) > 0 {
		section("ACTIVE CONNECTIONS")
		processRows := make([][]string, 0, min(10, len(active)))
		for _, item := range active[:min(10, len(active))] {
			processRows = append(processRows, []string{
				fmt.Sprint(item.ID), item.User, fmt.Sprintf("%ds", item.Seconds), item.State, truncate(item.Statement, 48),
			})
		}
		out.WriteString(table([]string{"id", "user", "time", "state", "statement"}, processRows, []int{9, 14, 8, 20, 49}, p))
	}

	if len(ctx.Warnings) > 0 {
		section("COLLECTION NOTES")
		for _, warning := range ctx.Warnings {
			out.WriteString(p.muted.Render("• "+warning) + "\n")
		}
	}
	out.WriteString("\n" + p.muted.Render(fmt.Sprintf("snapshot %s · collected %s · schema %s", ctx.Fingerprint, ctx.CollectedAt.Format(time.RFC3339), ctx.SchemaVersion)) + "\n")
	return out.String()
}

func table(headers []string, rows [][]string, widths []int, p palette) string {
	var out strings.Builder
	line := func(values []string, style lipgloss.Style) {
		for i, value := range values {
			cell := truncate(value, widths[i]-1)
			out.WriteString(style.Render(fmt.Sprintf("%-*s", widths[i], cell)))
		}
		out.WriteByte('\n')
	}
	line(headers, p.muted.Bold(true))
	out.WriteString(p.border.Render(strings.Repeat("─", sum(widths))) + "\n")
	for _, row := range rows {
		line(row, p.text)
	}
	return out.String()
}

func severityStyle(p palette, severity model.Severity) (lipgloss.Style, string) {
	switch severity {
	case model.SeverityCritical:
		return p.red, "CRITICAL"
	case model.SeverityWarning:
		return p.yellow, "WARNING"
	default:
		return p.cyan, "NOTE"
	}
}

func findingsBySeverity(findings []model.Finding, severity model.Severity) []model.Finding {
	result := make([]model.Finding, 0)
	for _, finding := range findings {
		if finding.Severity == severity {
			result = append(result, finding)
		}
	}
	return result
}

var namedSubsystems = []string{"connections", "workload", "queries", "indexes", "tables", "locks", "buffer pool", "temporary tables", "replication", "durability", "instrumentation"}

func healthySubsystems(findings []model.Finding) []string {
	affected := map[string]bool{}
	for _, finding := range findings {
		affected[finding.Subsystem] = true
	}
	result := make([]string, 0)
	for _, subsystem := range namedSubsystems {
		if !affected[subsystem] {
			result = append(result, subsystem)
		}
	}
	return result
}

func statusFor(ctx *model.Context, subsystem string) string {
	status := "ok"
	for _, finding := range ctx.Findings {
		if finding.Subsystem != subsystem {
			continue
		}
		if finding.Severity == model.SeverityCritical {
			return "fail"
		}
		if finding.Severity == model.SeverityWarning {
			status = "warn"
		} else if status == "ok" {
			status = "note"
		}
	}
	return status
}

func replicationValue(ctx *model.Context) string {
	if ctx.Replication == nil {
		return "not a replica"
	}
	if ctx.Replication.SecondsBehind == nil {
		return "lag unknown"
	}
	return fmt.Sprintf("%ds lag", *ctx.Replication.SecondsBehind)
}

func replicationNote(ctx *model.Context) string {
	if ctx.Replication == nil {
		return "source or standalone"
	}
	return fmt.Sprintf("io %s · sql %s", ctx.Replication.IORunning, ctx.Replication.SQLRunning)
}

func capabilityValue(ctx *model.Context) string {
	available := 0
	for _, capability := range ctx.Capabilities {
		if capability.Available {
			available++
		}
	}
	return fmt.Sprintf("%d/%d probes", available, len(ctx.Capabilities))
}

func healthBar(score, width int, filled, empty lipgloss.Style) string {
	count := int(math.Round(float64(score) * float64(width) / 100))
	return filled.Render(strings.Repeat("━", count)) + empty.Render(strings.Repeat("┄", width-count))
}

func activeProcesses(processes []model.Process) []model.Process {
	result := make([]model.Process, 0)
	for _, process := range processes {
		if !strings.EqualFold(process.Command, "Sleep") {
			result = append(result, process)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Seconds > result[j].Seconds })
	return result
}

func humanBytes(value uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	n := float64(value)
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", value, units[i])
	}
	return fmt.Sprintf("%.1f %s", n, units[i])
}

func humanCount(value uint64) string {
	switch {
	case value >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(value)/1_000_000_000)
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(value)/1_000_000)
	case value >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	default:
		return fmt.Sprint(value)
	}
}

func duration(ms float64) string {
	if ms >= 60_000 {
		return fmt.Sprintf("%.1fm", ms/60_000)
	}
	if ms >= 1_000 {
		return fmt.Sprintf("%.1fs", ms/1_000)
	}
	return fmt.Sprintf("%.1fms", ms)
}

func truncate(value string, width int) string {
	value = strings.ReplaceAll(value, "\n", " ")
	if width <= 1 || len([]rune(value)) <= width {
		return value
	}
	runes := []rune(value)
	return string(runes[:width-1]) + "…"
}

func fallback(value, replacement string) string {
	if value == "" {
		return replacement
	}
	return value
}

func sum(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
