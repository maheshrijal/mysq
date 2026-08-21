package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/maheshrijal/mysqldot/internal/model"
)

type Inspector func(context.Context) (*model.Context, error)
type Exporter func(*model.Context) (string, error)

type inspectMessage struct {
	context *model.Context
	err     error
}

type exportMessage struct {
	path string
	err  error
}

var tabs = []string{"Overview", "Findings", "Queries", "Tables", "Connections", "Config"}

type Model struct {
	ctx       context.Context
	inspect   Inspector
	export    Exporter
	snapshot  *model.Context
	viewport  viewport.Model
	spinner   spinner.Model
	width     int
	height    int
	tab       int
	loading   bool
	help      bool
	status    string
	err       error
	refreshed time.Time
}

var (
	background = lipgloss.Color("#0B1018")
	surface    = lipgloss.Color("#121A26")
	border     = lipgloss.Color("#2A3546")
	muted      = lipgloss.Color("#758094")
	text       = lipgloss.Color("#E7EDF5")
	green      = lipgloss.Color("#59E39D")
	yellow     = lipgloss.Color("#F4C95D")
	red        = lipgloss.Color("#FF6B6B")
	cyan       = lipgloss.Color("#62D0FF")
	purple     = lipgloss.Color("#B7A4FF")
)

func Run(ctx context.Context, inspect Inspector, export Exporter) error {
	model := New(ctx, inspect, export)
	program := tea.NewProgram(model, tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func New(ctx context.Context, inspect Inspector, export Exporter) Model {
	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = lipgloss.NewStyle().Foreground(cyan)
	return Model{
		ctx: ctx, inspect: inspect, export: export, spinner: spin,
		viewport: viewport.New(80, 20), loading: true,
	}
}

func (m Model) Init() tea.Cmd { return tea.Batch(m.spinner.Tick, m.inspectCommand()) }

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd
	switch msg := message.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "tab", "right", "l":
			m.tab = (m.tab + 1) % len(tabs)
			m.rebuild()
		case "shift+tab", "left", "h":
			m.tab = (m.tab + len(tabs) - 1) % len(tabs)
			m.rebuild()
		case "1", "2", "3", "4", "5", "6":
			m.tab = int(msg.Runes[0] - '1')
			m.rebuild()
		case "r":
			if !m.loading {
				m.loading = true
				m.status = "Refreshing every diagnostic probe…"
				commands = append(commands, m.inspectCommand(), m.spinner.Tick)
			}
		case "e":
			if !m.loading && m.snapshot != nil {
				m.status = "Writing agent bundle…"
				commands = append(commands, m.exportCommand())
			}
		case "?":
			m.help = !m.help
		case "g":
			m.viewport.GotoTop()
		case "G":
			m.viewport.GotoBottom()
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = max(40, msg.Width-4)
		m.viewport.Height = max(8, msg.Height-11)
		m.rebuild()
	case inspectMessage:
		m.loading = false
		m.err = msg.err
		if msg.err != nil {
			m.status = "Refresh failed: " + compact(msg.err.Error(), max(20, m.width-20))
		} else {
			m.snapshot = msg.context
			m.refreshed = time.Now()
			m.status = fmt.Sprintf("Refreshed %s · snapshot %s", m.refreshed.Format("15:04:05"), msg.context.Fingerprint)
			m.rebuild()
		}
	case exportMessage:
		if msg.err != nil {
			m.status = "Export failed: " + compact(msg.err.Error(), max(20, m.width-20))
		} else {
			m.status = "Agent bundle exported: " + msg.path
		}
	}

	var command tea.Cmd
	m.spinner, command = m.spinner.Update(message)
	commands = append(commands, command)
	m.viewport, command = m.viewport.Update(message)
	commands = append(commands, command)
	return m, tea.Batch(commands...)
}

func (m Model) View() string {
	if m.width == 0 {
		return "Starting mysqldot…"
	}
	canvas := lipgloss.NewStyle().Background(background).Foreground(text).Width(m.width).Height(m.height)
	header := m.header()
	tabBar := m.tabBar()
	content := m.viewport.View()
	footer := m.footer()
	return canvas.Render(lipgloss.JoinVertical(lipgloss.Left, header, tabBar, content, footer))
}

func (m Model) header() string {
	brand := lipgloss.NewStyle().Foreground(cyan).Bold(true).Render("◆ mysqldot")
	tagline := ""
	if m.width >= 100 {
		tagline = lipgloss.NewStyle().Foreground(muted).Render("  live MySQL intelligence")
	}
	left := brand + tagline
	right := ""
	if m.loading {
		right = m.spinner.View() + " collecting"
	} else if m.snapshot != nil {
		scoreColor := green
		if m.snapshot.Health.Critical > 0 {
			scoreColor = red
		} else if m.snapshot.Health.Warnings > 0 {
			scoreColor = yellow
		}
		right = lipgloss.NewStyle().Foreground(scoreColor).Bold(true).Render(fmt.Sprintf("health %d/100", m.snapshot.Health.Score))
	}
	gap := strings.Repeat(" ", max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right)-4))
	return lipgloss.NewStyle().Background(surface).BorderBottom(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(border).
		Padding(1, 2).Width(max(1, m.width-4)).Render(left + gap + right)
}

func (m Model) tabBar() string {
	items := make([]string, 0, len(tabs))
	for i, name := range tabs {
		style := lipgloss.NewStyle().Foreground(muted).Padding(0, 1)
		if i == m.tab {
			style = style.Foreground(background).Background(cyan).Bold(true)
		}
		items = append(items, style.Render(fmt.Sprintf("%d %s", i+1, name)))
	}
	return lipgloss.NewStyle().Background(background).Padding(1, 2, 0).Render(strings.Join(items, " "))
}

func (m Model) footer() string {
	keys := "tab/←→ navigate  ↑↓ scroll  r refresh  e export  ? help  q quit"
	if m.help {
		keys = "1–6 jump to view  g/G top/bottom  pgup/pgdn page  r reruns every probe  e writes JSON/Markdown/CSV/TXT bundle"
	}
	status := m.status
	if status == "" {
		status = "Read-only collection · SQL literals redacted before display or export"
	}
	return lipgloss.NewStyle().Background(surface).BorderTop(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(border).
		Foreground(muted).Padding(0, 2).Width(max(1, m.width-4)).Render(compact(status, max(20, m.width-4)) + "\n" + keys)
}

func (m *Model) rebuild() {
	if m.snapshot == nil {
		if m.err != nil {
			m.viewport.SetContent(errorView(m.err))
		} else {
			m.viewport.SetContent("\n  " + m.spinner.View() + " Collecting server identity, counters, statements, tables, indexes, locks, and configuration…")
		}
		return
	}
	var content string
	switch tabs[m.tab] {
	case "Overview":
		content = overview(m.snapshot, m.viewport.Width)
	case "Findings":
		content = findings(m.snapshot)
	case "Queries":
		content = queries(m.snapshot, m.viewport.Width)
	case "Tables":
		content = tablesView(m.snapshot)
	case "Connections":
		content = connections(m.snapshot, m.viewport.Width)
	case "Config":
		content = config(m.snapshot)
	}
	m.viewport.SetContent(content)
	m.viewport.GotoTop()
}

func (m Model) inspectCommand() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 45*time.Second)
		defer cancel()
		result, err := m.inspect(ctx)
		return inspectMessage{context: result, err: err}
	}
}

func (m Model) exportCommand() tea.Cmd {
	snapshot := m.snapshot
	return func() tea.Msg {
		path, err := m.export(snapshot)
		return exportMessage{path: path, err: err}
	}
}

func overview(ctx *model.Context, width int) string {
	cardWidth := max(15, (width-10)/4)
	if width < 110 {
		cardWidth = max(24, (width-5)/2)
	}
	card := func(label, value, note string, color lipgloss.Color) string {
		return lipgloss.NewStyle().Background(surface).Border(lipgloss.RoundedBorder()).BorderForeground(border).
			Padding(0, 1).Width(cardWidth).Render(
			lipgloss.NewStyle().Foreground(muted).Render(strings.ToUpper(label)) + "\n" +
				lipgloss.NewStyle().Foreground(color).Bold(true).Render(value) + "\n" +
				lipgloss.NewStyle().Foreground(muted).Render(note))
	}
	throughput := card("Throughput", fmt.Sprintf("%.1f qps", ctx.Metrics.QueriesPerSecond), fmt.Sprintf("%.1f transactions/s", ctx.Metrics.TransactionsPerSecond), cyan)
	connectionCard := card("Connections", fmt.Sprintf("%d / %d", ctx.Metrics.ConnectionsCurrent, ctx.Metrics.ConnectionsMax), fmt.Sprintf("%.1f%% used", ctx.Metrics.ConnectionsUsedPercent), colorForPercent(ctx.Metrics.ConnectionsUsedPercent, 75, 90))
	bufferCard := card("Buffer pool", fmt.Sprintf("%.2f%%", ctx.Metrics.BufferPoolHitPercent), fmt.Sprintf("%.1f%% used · %.1f%% dirty", ctx.Metrics.BufferPoolUsedPercent, ctx.Metrics.BufferPoolDirtyPercent), colorForLow(ctx.Metrics.BufferPoolHitPercent, 99, 95))
	concurrency := card("Concurrency", fmt.Sprintf("%d running", ctx.Metrics.ThreadsRunning), fmt.Sprintf("%d lock waits", len(ctx.Locks)), colorForCount(len(ctx.Locks)))
	cards := lipgloss.JoinHorizontal(lipgloss.Top, throughput, " ", connectionCard, " ", bufferCard, " ", concurrency)
	if width < 110 {
		cards = lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.JoinHorizontal(lipgloss.Top, throughput, " ", connectionCard),
			lipgloss.JoinHorizontal(lipgloss.Top, bufferCard, " ", concurrency),
		)
	}
	identity := lipgloss.NewStyle().Foreground(muted).Render(fmt.Sprintf("%s:%d/%s  ·  %s %s  ·  uptime %s  ·  %.1fs sample",
		ctx.Server.Host, ctx.Server.Port, ctx.Server.Database, ctx.Server.Flavor, ctx.Server.Version,
		humanDuration(ctx.Server.UptimeSeconds), float64(ctx.IntervalMillis)/1000))

	statusRows := []string{
		metricLine("Rows read", fmt.Sprintf("%.1f/s", ctx.Metrics.RowsReadPerSecond), cyan),
		metricLine("Rows written", fmt.Sprintf("%.1f/s", ctx.Metrics.RowsWrittenPerSecond), purple),
		metricLine("Temporary tables on disk", fmt.Sprintf("%.1f%%", ctx.Metrics.TempDiskTablePercent), colorForPercent(ctx.Metrics.TempDiskTablePercent, 10, 25)),
		metricLine("Table cache hit", fmt.Sprintf("%.2f%%", ctx.Metrics.TableCacheHitPercent), colorForLow(ctx.Metrics.TableCacheHitPercent, 99, 95)),
		metricLine("Open files", fmt.Sprintf("%.1f%%", ctx.Metrics.OpenFilesUsedPercent), colorForPercent(ctx.Metrics.OpenFilesUsedPercent, 75, 90)),
		metricLine("Purge history list", fmt.Sprintf("%d", ctx.Metrics.HistoryListLength), colorForHistory(ctx.Metrics.HistoryListLength)),
	}

	top := "No actionable findings."
	if len(ctx.Findings) > 0 {
		f := ctx.Findings[0]
		top = lipgloss.NewStyle().Foreground(severityColor(f.Severity)).Bold(true).Render(strings.ToUpper(string(f.Severity))+" · "+f.Title) + "\n" +
			lipgloss.NewStyle().Foreground(muted).Render(f.Summary)
	}
	return "\n" + identity + "\n\n" + cards + "\n\n" + sectionTitle("LIVE SIGNALS") + "\n" + strings.Join(statusRows, "\n") + "\n\n" + sectionTitle("TOP FINDING") + "\n" + top
}

func findings(ctx *model.Context) string {
	if len(ctx.Findings) == 0 {
		return "\n" + lipgloss.NewStyle().Foreground(green).Bold(true).Render("● All checked subsystems are healthy.")
	}
	var out strings.Builder
	for _, finding := range ctx.Findings {
		color := severityColor(finding.Severity)
		badge := lipgloss.NewStyle().Foreground(background).Background(color).Bold(true).Padding(0, 1).Render(strings.ToUpper(string(finding.Severity)))
		out.WriteString("\n" + badge + "  " + lipgloss.NewStyle().Foreground(text).Bold(true).Render(finding.Title) + "\n")
		out.WriteString(lipgloss.NewStyle().Foreground(muted).Render(finding.Summary) + "\n")
		out.WriteString(lipgloss.NewStyle().Foreground(cyan).Render("→ "+finding.Recommendation) + "\n")
		if len(finding.Objects) > 0 {
			out.WriteString(lipgloss.NewStyle().Foreground(muted).Render("objects: "+strings.Join(finding.Objects, ", ")) + "\n")
		}
	}
	return out.String()
}

func queries(ctx *model.Context, width int) string {
	if len(ctx.Queries) == 0 {
		return empty("No statement digests available. Check Performance Schema consumers and privileges.")
	}
	var total float64
	for _, query := range ctx.Queries {
		total += query.TotalLatencyMillis
	}
	var out strings.Builder
	out.WriteString("\n" + row([]string{"TOTAL", "SHARE", "CALLS", "MEAN", "ROWS", "STATEMENT"}, []int{10, 8, 9, 11, 10, max(28, width-55)}, true) + "\n")
	for _, query := range ctx.Queries {
		share := 0.0
		if total > 0 {
			share = query.TotalLatencyMillis * 100 / total
		}
		out.WriteString(row([]string{duration(query.TotalLatencyMillis), fmt.Sprintf("%.1f%%", share), humanCount(query.Calls), fmt.Sprintf("%.2fms", query.MeanLatencyMillis), humanCount(query.RowsExamined), query.Statement}, []int{10, 8, 9, 11, 10, max(28, width-55)}, false) + "\n")
	}
	out.WriteString("\n" + lipgloss.NewStyle().Foreground(muted).Render("Ranked by total latency. Statements are digest-normalized and contain no literals."))
	return out.String()
}

func tablesView(ctx *model.Context) string {
	if len(ctx.Tables) == 0 {
		return empty("No application tables are visible to the monitoring user.")
	}
	var out strings.Builder
	out.WriteString("\n" + row([]string{"SIZE", "ROWS", "READS", "WRITES", "PK", "TABLE"}, []int{12, 11, 11, 11, 5, 45}, true) + "\n")
	for _, table := range ctx.Tables {
		pk := "yes"
		if !table.HasPrimaryKey {
			pk = "NO"
		}
		out.WriteString(row([]string{humanBytes(table.TotalBytes), humanCount(table.EstimatedRows), humanCount(table.Reads), humanCount(table.Writes), pk, table.Schema + "." + table.Name}, []int{12, 11, 11, 11, 5, 45}, false) + "\n")
	}
	out.WriteString("\n" + lipgloss.NewStyle().Foreground(muted).Render("Rows are InnoDB estimates. I/O counters are since Performance Schema reset."))
	return out.String()
}

func connections(ctx *model.Context, width int) string {
	var out strings.Builder
	if len(ctx.ConnectionGroups) > 0 {
		out.WriteString("\n" + sectionTitle("CONNECTION BREAKDOWN") + "\n")
		out.WriteString(row([]string{"GROUP", "VALUE", "TOTAL", "ACTIVE", "SLEEPING", "OTHER"}, []int{12, 32, 9, 9, 10, 8}, true) + "\n")
		shown := 0
		for _, group := range ctx.ConnectionGroups {
			if group.Kind == "user_host" || shown >= 12 {
				continue
			}
			out.WriteString(row([]string{group.Kind, group.Key, fmt.Sprint(group.Total), fmt.Sprint(group.Active), fmt.Sprint(group.Sleeping), fmt.Sprint(group.Other)}, []int{12, 32, 9, 9, 10, 8}, false) + "\n")
			shown++
		}
		out.WriteString("\n" + sectionTitle("PROCESS SNAPSHOT") + "\n")
	}
	out.WriteString("\n" + row([]string{"ID", "USER", "HOST", "TIME", "COMMAND", "STATE", "STATEMENT"}, []int{8, 13, 18, 8, 10, 18, max(28, width-78)}, true) + "\n")
	for _, process := range ctx.Processes {
		out.WriteString(row([]string{fmt.Sprint(process.ID), process.User, process.Host, fmt.Sprintf("%ds", process.Seconds), process.Command, process.State, process.Statement}, []int{8, 13, 18, 8, 10, 18, max(28, width-78)}, false) + "\n")
	}
	if len(ctx.Processes) == 0 {
		out.WriteString("\nNo other connections are visible.")
	}
	if len(ctx.Locks) > 0 {
		out.WriteString("\n\n" + sectionTitle("ROW LOCK WAITS") + "\n")
		for _, lock := range ctx.Locks {
			fmt.Fprintf(&out, "%s waits for %s on %s.%s index %s (%s %s)\n", lock.WaitingTransaction, lock.BlockingTransaction, lock.Schema, lock.Table, lock.Index, lock.LockType, lock.LockMode)
		}
	}
	return out.String()
}

func config(ctx *model.Context) string {
	important := []string{
		"innodb_buffer_pool_size", "innodb_buffer_pool_instances", "innodb_flush_log_at_trx_commit", "innodb_log_buffer_size",
		"innodb_redo_log_capacity", "sync_binlog", "binlog_format", "gtid_mode", "max_connections", "thread_cache_size",
		"table_open_cache", "table_definition_cache", "tmp_table_size", "max_heap_table_size", "sort_buffer_size", "join_buffer_size",
		"performance_schema", "skip_name_resolve", "slow_query_log", "long_query_time", "transaction_isolation", "sql_mode",
	}
	var out strings.Builder
	out.WriteString("\n" + row([]string{"VARIABLE", "VALUE"}, []int{38, 70}, true) + "\n")
	for _, key := range important {
		if value, ok := ctx.Variables[key]; ok {
			out.WriteString(row([]string{key, value}, []int{38, 70}, false) + "\n")
		}
	}
	unavailable := make([]string, 0)
	for _, capability := range ctx.Capabilities {
		if !capability.Available {
			unavailable = append(unavailable, capability.Name+": "+capability.Reason)
		}
	}
	if len(unavailable) > 0 {
		out.WriteString("\n" + sectionTitle("DEGRADED COVERAGE") + "\n" + strings.Join(unavailable, "\n"))
	}
	return out.String()
}

func row(values []string, widths []int, header bool) string {
	style := lipgloss.NewStyle().Foreground(text)
	if header {
		style = style.Foreground(muted).Bold(true)
	}
	var out strings.Builder
	for i, value := range values {
		out.WriteString(style.Width(widths[i]).MaxWidth(widths[i]).Render(compact(strings.ReplaceAll(value, "\n", " "), widths[i]-1)))
	}
	if header {
		total := 0
		for _, width := range widths {
			total += width
		}
		out.WriteString("\n" + lipgloss.NewStyle().Foreground(border).Render(strings.Repeat("─", total)))
	}
	return out.String()
}

func metricLine(label, value string, color lipgloss.Color) string {
	return lipgloss.NewStyle().Foreground(muted).Width(32).Render(label) + lipgloss.NewStyle().Foreground(color).Bold(true).Render(value)
}

func sectionTitle(value string) string {
	return lipgloss.NewStyle().Foreground(cyan).Bold(true).Render(value)
}
func empty(value string) string { return "\n" + lipgloss.NewStyle().Foreground(muted).Render(value) }
func errorView(err error) string {
	return "\n" + lipgloss.NewStyle().Foreground(red).Bold(true).Render("Connection or collection failed") + "\n\n" +
		lipgloss.NewStyle().Foreground(muted).Render(err.Error()) + "\n\nPress r to retry or q to quit."
}

func severityColor(severity model.Severity) lipgloss.Color {
	switch severity {
	case model.SeverityCritical:
		return red
	case model.SeverityWarning:
		return yellow
	default:
		return cyan
	}
}

func colorForPercent(value, warning, critical float64) lipgloss.Color {
	if value >= critical {
		return red
	}
	if value >= warning {
		return yellow
	}
	return green
}

func colorForLow(value, warning, critical float64) lipgloss.Color {
	if value < critical {
		return red
	}
	if value < warning {
		return yellow
	}
	return green
}

func colorForCount(value int) lipgloss.Color {
	if value > 0 {
		return red
	}
	return green
}

func colorForHistory(value uint64) lipgloss.Color {
	if value >= 1_000_000 {
		return red
	}
	if value >= 100_000 {
		return yellow
	}
	return green
}

func humanDuration(seconds uint64) string {
	duration := time.Duration(seconds) * time.Second
	if duration >= 24*time.Hour {
		return fmt.Sprintf("%.1fd", duration.Hours()/24)
	}
	return duration.Round(time.Second).String()
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
		return fmt.Sprintf("%d B", value)
	}
	return fmt.Sprintf("%.1f %s", n, units[i])
}

func humanCount(value uint64) string {
	if value >= 1_000_000_000 {
		return fmt.Sprintf("%.1fB", float64(value)/1_000_000_000)
	}
	if value >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(value)/1_000_000)
	}
	if value >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	}
	return fmt.Sprint(value)
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

func compact(value string, width int) string {
	if width <= 1 || len([]rune(value)) <= width {
		return value
	}
	runes := []rune(value)
	return string(runes[:width-1]) + "…"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
