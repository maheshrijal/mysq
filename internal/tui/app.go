package tui

import (
	"context"
	"fmt"
	"math"
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
	// The palette deliberately leaves the terminal background alone. Adaptive
	// foregrounds keep the UI legible in both light and dark terminals while the
	// Tokyo Night/Catppuccin-inspired accents remain restrained and semantic.
	surface    = lipgloss.AdaptiveColor{Light: "#E6E9EF", Dark: "#24283B"}
	surfaceAlt = lipgloss.AdaptiveColor{Light: "#DCE0E8", Dark: "#1F2335"}
	border     = lipgloss.AdaptiveColor{Light: "#BCC0CC", Dark: "#3B4261"}
	muted      = lipgloss.AdaptiveColor{Light: "#7C7F93", Dark: "#737AA2"}
	text       = lipgloss.AdaptiveColor{Light: "#4C4F69", Dark: "#C0CAF5"}
	green      = lipgloss.AdaptiveColor{Light: "#40A02B", Dark: "#9ECE6A"}
	yellow     = lipgloss.AdaptiveColor{Light: "#DF8E1D", Dark: "#E0AF68"}
	red        = lipgloss.AdaptiveColor{Light: "#D20F39", Dark: "#F7768E"}
	cyan       = lipgloss.AdaptiveColor{Light: "#1E66F5", Dark: "#7AA2F7"}
)

const (
	wideBreakpoint = 110
	sidebarWidth   = 23
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
		m.resizeViewport()
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
	canvas := lipgloss.NewStyle().Foreground(text).Width(m.width).Height(m.height)
	if m.width < 52 || m.height < 18 {
		return canvas.Render(m.tooSmall())
	}
	header := m.header()
	footer := m.footer()
	if m.width >= wideBreakpoint {
		bodyHeight := max(8, m.height-2)
		mainWidth := max(50, m.width-sidebarWidth-1)
		body := lipgloss.JoinHorizontal(lipgloss.Top, m.sidebar(bodyHeight), " ", m.contentPanel(mainWidth, bodyHeight))
		return canvas.Render(lipgloss.JoinVertical(lipgloss.Left, header, body, footer))
	}
	bodyHeight := max(8, m.height-3)
	return canvas.Render(lipgloss.JoinVertical(lipgloss.Left, header, m.tabBar(), m.contentPanel(m.width, bodyHeight), footer))
}

func (m Model) tooSmall() string {
	message := lipgloss.NewStyle().Foreground(cyan).Bold(true).Render("◆ MYSQLDOT") + "\n\n" +
		lipgloss.NewStyle().Foreground(text).Bold(true).Render("A little more room, please") + "\n" +
		lipgloss.NewStyle().Foreground(muted).Render(fmt.Sprintf("Need 52×18 · current %d×%d", m.width, m.height)) + "\n\n" +
		keyHint("q", "quit")
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(1, 2).Width(max(8, m.width-2)).Render(message)
}

func (m Model) header() string {
	brand := lipgloss.NewStyle().Foreground(cyan).Bold(true).Render("◆ MYSQLDOT")
	target := " MySQL intelligence"
	if m.snapshot != nil {
		target = fmt.Sprintf(" %s:%d/%s", m.snapshot.Server.Host, m.snapshot.Server.Port, fallback(m.snapshot.Server.Database, "all databases"))
	}
	left := brand + lipgloss.NewStyle().Foreground(muted).Render(target)
	right := lipgloss.NewStyle().Foreground(muted).Render("starting")
	if m.loading {
		right = m.spinner.View() + lipgloss.NewStyle().Foreground(muted).Render(" collecting")
	} else if m.snapshot != nil {
		scoreColor := green
		if m.snapshot.Health.Critical > 0 {
			scoreColor = red
		} else if m.snapshot.Health.Warnings > 0 {
			scoreColor = yellow
		}
		state := "HEALTHY"
		if m.snapshot.Health.Critical > 0 {
			state = "CRITICAL"
		} else if m.snapshot.Health.Warnings > 0 {
			state = "ATTENTION"
		}
		right = lipgloss.NewStyle().Foreground(scoreColor).Bold(true).Render(fmt.Sprintf("● %s  %d", state, m.snapshot.Health.Score))
	}
	line := padBetween(left, right, max(1, m.width-2))
	return lipgloss.NewStyle().Background(surfaceAlt).Padding(0, 1).Width(max(1, m.width)).Render(line)
}

func (m Model) tabBar() string {
	if m.width < 82 {
		current := fmt.Sprintf("‹  %d/%d  %s  ›", m.tab+1, len(tabs), tabs[m.tab])
		return lipgloss.NewStyle().Foreground(cyan).Bold(true).Padding(0, 1).Width(max(1, m.width)).Render(current)
	}
	items := make([]string, 0, len(tabs))
	for i, name := range tabs {
		label := fmt.Sprintf("%d %s", i+1, name)
		style := lipgloss.NewStyle().Foreground(muted).Padding(0, 1)
		if i == m.tab {
			label = "● " + name
			style = style.Foreground(cyan).Bold(true)
		}
		items = append(items, style.Render(label))
	}
	return lipgloss.NewStyle().Padding(0, 1).Width(max(1, m.width)).Render(strings.Join(items, " "))
}

func (m Model) footer() string {
	keys := keyHint("tab", "views") + "  " + keyHint("j/k", "scroll") + "  " + keyHint("r", "refresh") + "  " + keyHint("e", "export") + "  " + keyHint("?", "help") + "  " + keyHint("q", "quit")
	if m.help {
		keys = keyHint("1–6", "jump") + "  " + keyHint("g/G", "top/bottom") + "  " + keyHint("pgup/dn", "page") + "  " + keyHint("e", "agent bundle")
	}
	status := m.status
	if status == "" {
		status = "Read-only · SQL literals redacted"
	}
	if m.width < 96 {
		keys = keyHint("tab", "view") + "  " + keyHint("r", "refresh") + "  " + keyHint("e", "export") + "  " + keyHint("q", "quit")
	}
	line := padBetween(lipgloss.NewStyle().Foreground(muted).Render(compact(status, max(16, m.width/2))), keys, max(1, m.width-2))
	return lipgloss.NewStyle().Background(surfaceAlt).Padding(0, 1).Width(max(1, m.width)).Render(line)
}

func (m *Model) resizeViewport() {
	bodyHeight := max(8, m.height-3)
	mainWidth := m.width
	if m.width >= wideBreakpoint {
		bodyHeight = max(8, m.height-2)
		mainWidth = max(50, m.width-sidebarWidth-1)
	}
	m.viewport.Width = max(24, mainWidth-4)
	m.viewport.Height = max(4, bodyHeight-4)
}

func (m Model) sidebar(height int) string {
	innerWidth := sidebarWidth - 4
	var content strings.Builder
	content.WriteString(lipgloss.NewStyle().Foreground(muted).Bold(true).Render("VIEWS") + "\n\n")
	for i, name := range tabs {
		prefix := "  "
		style := lipgloss.NewStyle().Foreground(muted).Width(innerWidth)
		if i == m.tab {
			prefix = "▌ "
			style = style.Foreground(cyan).Background(surface).Bold(true)
		}
		content.WriteString(style.Render(fmt.Sprintf("%s%d  %s", prefix, i+1, name)) + "\n")
	}
	if m.snapshot != nil && height >= 24 {
		content.WriteString("\n" + lipgloss.NewStyle().Foreground(muted).Bold(true).Render("TARGET") + "\n\n")
		content.WriteString(lipgloss.NewStyle().Foreground(text).Bold(true).Render(fallback(m.snapshot.Server.Database, "all databases")) + "\n")
		content.WriteString(lipgloss.NewStyle().Foreground(muted).Render(compact(m.snapshot.Server.Flavor+" "+m.snapshot.Server.Version, innerWidth)) + "\n")
		content.WriteString(lipgloss.NewStyle().Foreground(muted).Render("uptime "+humanDuration(m.snapshot.Server.UptimeSeconds)) + "\n")
	}
	if height >= 31 {
		content.WriteString("\n" + lipgloss.NewStyle().Foreground(muted).Bold(true).Render("SAFETY") + "\n\n")
		content.WriteString(lipgloss.NewStyle().Foreground(green).Render("● read-only session") + "\n")
		content.WriteString(lipgloss.NewStyle().Foreground(muted).Render("  literals redacted"))
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(0, 1).Width(sidebarWidth - 2).Height(max(1, height-2)).Render(content.String())
}

func (m Model) contentPanel(width, height int) string {
	innerWidth := max(24, width-4)
	title := lipgloss.NewStyle().Foreground(cyan).Bold(true).Render("● " + strings.ToUpper(tabs[m.tab]))
	subtitle := viewSubtitle(m)
	titleLine := padBetween(title, lipgloss.NewStyle().Foreground(muted).Render(subtitle), innerWidth)
	rule := lipgloss.NewStyle().Foreground(border).Render(strings.Repeat("─", innerWidth))
	content := lipgloss.JoinVertical(lipgloss.Left, titleLine, rule, m.viewport.View())
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(0, 1).Width(width - 2).Height(max(1, height-2)).Render(content)
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
		content = findings(m.snapshot, m.viewport.Width)
	case "Queries":
		content = queries(m.snapshot, m.viewport.Width)
	case "Tables":
		content = tablesView(m.snapshot, m.viewport.Width)
	case "Connections":
		content = connections(m.snapshot, m.viewport.Width)
	case "Config":
		content = config(m.snapshot, m.viewport.Width)
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
	healthColor := green
	posture := "Operational"
	postureNote := "No critical conditions detected"
	if ctx.Health.Critical > 0 {
		healthColor = red
		posture = "Critical"
		postureNote = "Immediate action is recommended"
	} else if ctx.Health.Warnings > 0 {
		healthColor = yellow
		posture = "Needs attention"
		postureNote = fmt.Sprintf("%d warning signals need review", ctx.Health.Warnings)
	}
	postureLine := lipgloss.NewStyle().Foreground(healthColor).Bold(true).Render("● "+posture) +
		lipgloss.NewStyle().Foreground(muted).Render("  "+postureNote)
	score := lipgloss.NewStyle().Foreground(healthColor).Bold(true).Render(fmt.Sprintf("%d/100", ctx.Health.Score))
	postureBox := panelBox("DATABASE POSTURE", padBetween(postureLine, score, max(20, width-4)), width)

	cardColumns := 4
	if width < 88 {
		cardColumns = 2
	}
	cardWidth := max(20, (width-(cardColumns-1))/cardColumns)
	throughput := kpiCard("Throughput", fmt.Sprintf("%.1f qps", ctx.Metrics.QueriesPerSecond), fmt.Sprintf("%.1f tx/s", ctx.Metrics.TransactionsPerSecond), cyan, cardWidth)
	connectionCard := kpiCard("Connections", fmt.Sprintf("%d / %d", ctx.Metrics.ConnectionsCurrent, ctx.Metrics.ConnectionsMax), fmt.Sprintf("%.1f%% capacity", ctx.Metrics.ConnectionsUsedPercent), colorForPercent(ctx.Metrics.ConnectionsUsedPercent, 75, 90), cardWidth)
	bufferCard := kpiCard("Buffer pool", fmt.Sprintf("%.2f%% hit", ctx.Metrics.BufferPoolHitPercent), fmt.Sprintf("%.1f%% used · %.1f%% dirty", ctx.Metrics.BufferPoolUsedPercent, ctx.Metrics.BufferPoolDirtyPercent), colorForLow(ctx.Metrics.BufferPoolHitPercent, 99, 95), cardWidth)
	concurrency := kpiCard("Concurrency", fmt.Sprintf("%d running", ctx.Metrics.ThreadsRunning), fmt.Sprintf("%d row-lock waits", len(ctx.Locks)), colorForCount(len(ctx.Locks)), cardWidth)
	cards := lipgloss.JoinHorizontal(lipgloss.Top, throughput, " ", connectionCard, " ", bufferCard, " ", concurrency)
	if cardColumns == 2 {
		cards = lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.JoinHorizontal(lipgloss.Top, throughput, " ", connectionCard),
			lipgloss.JoinHorizontal(lipgloss.Top, bufferCard, " ", concurrency),
		)
	}

	pressureWidth := width
	findingWidth := width
	if width >= 82 {
		pressureWidth = (width - 1) * 56 / 100
		findingWidth = width - pressureWidth - 1
	}
	pressureInner := max(20, pressureWidth-4)
	pressure := strings.Join([]string{
		gaugeLine("Connection slots", ctx.Metrics.ConnectionsUsedPercent, fmt.Sprintf("%.1f%%", ctx.Metrics.ConnectionsUsedPercent), colorForPercent(ctx.Metrics.ConnectionsUsedPercent, 75, 90), pressureInner),
		gaugeLine("Buffer pool used", ctx.Metrics.BufferPoolUsedPercent, fmt.Sprintf("%.1f%%", ctx.Metrics.BufferPoolUsedPercent), cyan, pressureInner),
		gaugeLine("Dirty pages", ctx.Metrics.BufferPoolDirtyPercent, fmt.Sprintf("%.1f%%", ctx.Metrics.BufferPoolDirtyPercent), colorForPercent(ctx.Metrics.BufferPoolDirtyPercent, 40, 75), pressureInner),
		gaugeLine("Disk temp tables", ctx.Metrics.TempDiskTablePercent, fmt.Sprintf("%.1f%%", ctx.Metrics.TempDiskTablePercent), colorForPercent(ctx.Metrics.TempDiskTablePercent, 10, 25), pressureInner),
	}, "\n")
	pressureBox := panelBox("WORKLOAD PRESSURE", pressure, pressureWidth)

	topFinding := lipgloss.NewStyle().Foreground(green).Render("● No actionable findings") + "\n" +
		lipgloss.NewStyle().Foreground(muted).Render("All collected subsystems are within policy.")
	if len(ctx.Findings) > 0 {
		f := ctx.Findings[0]
		color := severityColor(f.Severity)
		topFinding = lipgloss.NewStyle().Foreground(color).Bold(true).Render("▌ "+strings.ToUpper(string(f.Severity))) + "  " +
			lipgloss.NewStyle().Foreground(text).Bold(true).Render(f.Title) + "\n" +
			lipgloss.NewStyle().Foreground(muted).Width(max(16, findingWidth-4)).Render(f.Summary) + "\n" +
			lipgloss.NewStyle().Foreground(cyan).Render("Press 2 for remediation")
	}
	findingBox := panelBox("PRIORITY SIGNAL", topFinding, findingWidth)
	lower := lipgloss.JoinHorizontal(lipgloss.Top, pressureBox, " ", findingBox)
	if width < 82 {
		lower = lipgloss.JoinVertical(lipgloss.Left, pressureBox, findingBox)
	}

	identity := lipgloss.NewStyle().Foreground(muted).Render(fmt.Sprintf("%s %s · uptime %s · %.1fs collection window",
		ctx.Server.Flavor, ctx.Server.Version, humanDuration(ctx.Server.UptimeSeconds), float64(ctx.IntervalMillis)/1000))
	return postureBox + "\n" + cards + "\n" + lower + "\n" + identity
}

func findings(ctx *model.Context, width int) string {
	if len(ctx.Findings) == 0 {
		body := lipgloss.NewStyle().Foreground(green).Bold(true).Render("● All checked subsystems are healthy") + "\n" +
			lipgloss.NewStyle().Foreground(muted).Render("No critical, warning, or informational findings were produced by this snapshot.")
		return panelBox("CLEAR", body, width)
	}
	var out strings.Builder
	for index, finding := range ctx.Findings {
		color := severityColor(finding.Severity)
		meta := lipgloss.NewStyle().Foreground(color).Bold(true).Render(fmt.Sprintf("%02d  %s", index+1, strings.ToUpper(string(finding.Severity)))) +
			lipgloss.NewStyle().Foreground(muted).Render("  "+strings.ToUpper(finding.Subsystem)+"  ·  "+finding.ID)
		body := meta + "\n" + lipgloss.NewStyle().Foreground(text).Bold(true).Render(finding.Title) + "\n" +
			lipgloss.NewStyle().Foreground(muted).Width(max(16, width-4)).Render(finding.Summary) + "\n\n" +
			lipgloss.NewStyle().Foreground(cyan).Bold(true).Render("ACTION  ") +
			lipgloss.NewStyle().Foreground(text).Width(max(16, width-12)).Render(finding.Recommendation)
		if len(finding.Objects) > 0 {
			body += "\n" + lipgloss.NewStyle().Foreground(muted).Render("OBJECTS  "+strings.Join(finding.Objects, "  ·  "))
		}
		out.WriteString(panelBox("FINDING", body, width))
		if index < len(ctx.Findings)-1 {
			out.WriteString("\n")
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
	out.WriteString(row([]string{"TOTAL", "SHARE", "CALLS", "MEAN", "ROWS", "STATEMENT"}, []int{10, 8, 9, 11, 10, max(28, width-55)}, true) + "\n")
	for _, query := range ctx.Queries {
		share := 0.0
		if total > 0 {
			share = query.TotalLatencyMillis * 100 / total
		}
		out.WriteString(row([]string{duration(query.TotalLatencyMillis), fmt.Sprintf("%.1f%%", share), humanCount(query.Calls), fmt.Sprintf("%.2fms", query.MeanLatencyMillis), humanCount(query.RowsExamined), query.Statement}, []int{10, 8, 9, 11, 10, max(28, width-55)}, false) + "\n")
	}
	out.WriteString("\n" + lipgloss.NewStyle().Foreground(muted).Render("Sorted by database time  ·  digest-normalized  ·  SQL literals removed"))
	return out.String()
}

func tablesView(ctx *model.Context, width int) string {
	if len(ctx.Tables) == 0 {
		return empty("No application tables are visible to the monitoring user.")
	}
	var out strings.Builder
	widths := []int{12, 11, 11, 11, 5, max(20, width-50)}
	out.WriteString(row([]string{"SIZE", "ROWS", "READS", "WRITES", "PK", "TABLE"}, widths, true) + "\n")
	for _, table := range ctx.Tables {
		pk := "yes"
		if !table.HasPrimaryKey {
			pk = "NO"
		}
		out.WriteString(row([]string{humanBytes(table.TotalBytes), humanCount(table.EstimatedRows), humanCount(table.Reads), humanCount(table.Writes), pk, table.Schema + "." + table.Name}, widths, false) + "\n")
	}
	out.WriteString("\n" + lipgloss.NewStyle().Foreground(muted).Render("Rows are InnoDB estimates. I/O counters are since Performance Schema reset."))
	return out.String()
}

func connections(ctx *model.Context, width int) string {
	var out strings.Builder
	if len(ctx.ConnectionGroups) > 0 {
		out.WriteString(sectionTitle("CONNECTION BREAKDOWN") + "\n")
		groupWidths := []int{10, max(18, width-42), 8, 8, 8, 8}
		out.WriteString(row([]string{"GROUP", "VALUE", "TOTAL", "ACTIVE", "SLEEP", "OTHER"}, groupWidths, true) + "\n")
		shown := 0
		for _, group := range ctx.ConnectionGroups {
			if group.Kind == "user_host" || shown >= 12 {
				continue
			}
			out.WriteString(row([]string{group.Kind, group.Key, fmt.Sprint(group.Total), fmt.Sprint(group.Active), fmt.Sprint(group.Sleeping), fmt.Sprint(group.Other)}, groupWidths, false) + "\n")
			shown++
		}
		out.WriteString("\n" + sectionTitle("PROCESS SNAPSHOT") + "\n")
	}
	if width < 100 {
		processWidths := []int{8, 12, 8, 10, 16, max(22, width-54)}
		out.WriteString("\n" + row([]string{"ID", "USER", "TIME", "COMMAND", "STATE", "STATEMENT"}, processWidths, true) + "\n")
		for _, process := range ctx.Processes {
			out.WriteString(row([]string{fmt.Sprint(process.ID), process.User, fmt.Sprintf("%ds", process.Seconds), process.Command, process.State, process.Statement}, processWidths, false) + "\n")
		}
	} else {
		processWidths := []int{8, 13, 18, 8, 10, 18, max(28, width-75)}
		out.WriteString("\n" + row([]string{"ID", "USER", "HOST", "TIME", "COMMAND", "STATE", "STATEMENT"}, processWidths, true) + "\n")
		for _, process := range ctx.Processes {
			out.WriteString(row([]string{fmt.Sprint(process.ID), process.User, process.Host, fmt.Sprintf("%ds", process.Seconds), process.Command, process.State, process.Statement}, processWidths, false) + "\n")
		}
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

func config(ctx *model.Context, width int) string {
	important := []string{
		"innodb_buffer_pool_size", "innodb_buffer_pool_instances", "innodb_flush_log_at_trx_commit", "innodb_log_buffer_size",
		"innodb_redo_log_capacity", "sync_binlog", "binlog_format", "gtid_mode", "max_connections", "thread_cache_size",
		"table_open_cache", "table_definition_cache", "tmp_table_size", "max_heap_table_size", "sort_buffer_size", "join_buffer_size",
		"performance_schema", "skip_name_resolve", "slow_query_log", "long_query_time", "transaction_isolation", "sql_mode",
	}
	var out strings.Builder
	nameWidth := min(36, max(24, width/3))
	widths := []int{nameWidth, max(24, width-nameWidth)}
	out.WriteString(row([]string{"VARIABLE", "EFFECTIVE VALUE"}, widths, true) + "\n")
	for _, key := range important {
		if value, ok := ctx.Variables[key]; ok {
			out.WriteString(row([]string{key, value}, widths, false) + "\n")
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
		style = style.Foreground(cyan).Background(surfaceAlt).Bold(true)
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

func panelBox(title, body string, width int) string {
	heading := lipgloss.NewStyle().Foreground(muted).Bold(true).Render(title)
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(0, 1).Width(max(3, width-2)).Render(heading + "\n" + body)
}

func kpiCard(label, value, note string, color lipgloss.TerminalColor, width int) string {
	innerWidth := max(12, width-4)
	body := lipgloss.NewStyle().Foreground(muted).Bold(true).Render(strings.ToUpper(label)) + "\n" +
		lipgloss.NewStyle().Foreground(color).Bold(true).Render(value) + "\n" +
		lipgloss.NewStyle().Foreground(muted).Render(compact(note, innerWidth))
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(0, 1).Width(max(3, width-2)).Render(body)
}

func gaugeLine(label string, percent float64, value string, color lipgloss.TerminalColor, width int) string {
	labelWidth := min(18, max(10, width/3))
	valueWidth := max(6, lipgloss.Width(value))
	barWidth := max(5, width-labelWidth-valueWidth-2)
	return lipgloss.NewStyle().Foreground(muted).Width(labelWidth).Render(compact(label, labelWidth-1)) +
		miniBar(percent, barWidth, color) + "  " +
		lipgloss.NewStyle().Foreground(color).Bold(true).Width(valueWidth).Align(lipgloss.Right).Render(value)
}

func miniBar(percent float64, width int, color lipgloss.TerminalColor) string {
	percent = minFloat(100, maxFloat(0, percent))
	filled := int(math.Round(percent / 100 * float64(width)))
	return lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("━", filled)) +
		lipgloss.NewStyle().Foreground(border).Render(strings.Repeat("─", max(0, width-filled)))
}

func sectionTitle(value string) string {
	return lipgloss.NewStyle().Foreground(cyan).Bold(true).Render("▌ " + value)
}
func empty(value string) string { return "\n" + lipgloss.NewStyle().Foreground(muted).Render(value) }
func errorView(err error) string {
	return "\n" + lipgloss.NewStyle().Foreground(red).Bold(true).Render("Connection or collection failed") + "\n\n" +
		lipgloss.NewStyle().Foreground(muted).Render(err.Error()) + "\n\nPress r to retry or q to quit."
}

func severityColor(severity model.Severity) lipgloss.TerminalColor {
	switch severity {
	case model.SeverityCritical:
		return red
	case model.SeverityWarning:
		return yellow
	default:
		return cyan
	}
}

func colorForPercent(value, warning, critical float64) lipgloss.TerminalColor {
	if value >= critical {
		return red
	}
	if value >= warning {
		return yellow
	}
	return green
}

func colorForLow(value, warning, critical float64) lipgloss.TerminalColor {
	if value < critical {
		return red
	}
	if value < warning {
		return yellow
	}
	return green
}

func colorForCount(value int) lipgloss.TerminalColor {
	if value > 0 {
		return red
	}
	return green
}

func colorForHistory(value uint64) lipgloss.TerminalColor {
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

func padBetween(left, right string, width int) string {
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	if leftWidth+rightWidth >= width {
		available := max(1, width-rightWidth-1)
		left = compact(left, available)
		leftWidth = lipgloss.Width(left)
	}
	return left + strings.Repeat(" ", max(1, width-leftWidth-rightWidth)) + right
}

func keyHint(key, label string) string {
	keyStyle := lipgloss.NewStyle().Foreground(cyan).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(muted)
	return keyStyle.Render(key) + " " + labelStyle.Render(label)
}

func viewSubtitle(m Model) string {
	if m.loading {
		return "collecting live signals"
	}
	if m.snapshot == nil {
		return "waiting for a snapshot"
	}
	switch tabs[m.tab] {
	case "Overview":
		return fmt.Sprintf("%d findings · %.1fs sample", len(m.snapshot.Findings), float64(m.snapshot.IntervalMillis)/1000)
	case "Findings":
		return fmt.Sprintf("%d actionable signals", len(m.snapshot.Findings))
	case "Queries":
		return fmt.Sprintf("%d normalized digests", len(m.snapshot.Queries))
	case "Tables":
		return fmt.Sprintf("%d visible tables", len(m.snapshot.Tables))
	case "Connections":
		return fmt.Sprintf("%d sessions · %d waits", len(m.snapshot.Processes), len(m.snapshot.Locks))
	case "Config":
		return "effective runtime values"
	default:
		return ""
	}
}

func fallback(value, alternative string) string {
	if value == "" {
		return alternative
	}
	return value
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
