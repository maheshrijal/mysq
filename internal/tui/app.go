package tui

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/maheshrijal/mysq/internal/model"
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

var tabs = []string{"Overview", "Connections", "Queries", "Engine", "Findings", "Tables", "Config"}

type Model struct {
	ctx         context.Context
	inspect     Inspector
	export      Exporter
	snapshot    *model.Context
	viewport    viewport.Model
	spinner     spinner.Model
	width       int
	height      int
	tab         int
	queryIndex  int
	queryDetail bool
	loading     bool
	exporting   bool
	help        bool
	status      string
	exportPath  string
	err         error
	refreshed   time.Time
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
	updateViewport := true
	switch msg := message.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "tab", "right", "l":
			if m.queryDetail {
				break
			}
			m.tab = (m.tab + 1) % len(tabs)
			m.rebuild()
			updateViewport = false
		case "shift+tab", "left", "h":
			if m.queryDetail {
				break
			}
			m.tab = (m.tab + len(tabs) - 1) % len(tabs)
			m.rebuild()
			updateViewport = false
		case "down":
			if tabs[m.tab] == "Queries" && !m.queryDetail && m.snapshot != nil && len(m.snapshot.Queries) > 0 {
				m.queryIndex = min(m.queryIndex+1, len(m.snapshot.Queries)-1)
				m.rebuild()
				m.ensureQuerySelectionVisible()
				updateViewport = false
			}
		case "up":
			if tabs[m.tab] == "Queries" && !m.queryDetail && m.snapshot != nil && len(m.snapshot.Queries) > 0 {
				m.queryIndex = max(0, m.queryIndex-1)
				m.rebuild()
				m.ensureQuerySelectionVisible()
				updateViewport = false
			}
		case "enter":
			if tabs[m.tab] == "Queries" && !m.queryDetail && m.snapshot != nil && len(m.snapshot.Queries) > 0 {
				m.queryDetail = true
				m.rebuild()
				updateViewport = false
			}
		case "1", "2", "3", "4", "5", "6", "7":
			m.queryDetail = false
			m.tab = int(msg.Runes[0] - '1')
			m.rebuild()
			updateViewport = false
		case "r":
			if !m.loading && !m.exporting {
				m.exportPath = ""
				m.resizeViewport()
				m.loading = true
				m.status = "Refreshing every diagnostic probe…"
				commands = append(commands, m.inspectCommand(), m.spinner.Tick)
			}
		case "e":
			if !m.loading && !m.exporting && m.exportPath == "" && m.snapshot != nil {
				m.exportPath = ""
				m.resizeViewport()
				m.exporting = true
				m.status = "Writing agent bundle…"
				commands = append(commands, m.exportCommand())
			}
		case "esc":
			if m.queryDetail {
				m.queryDetail = false
				m.rebuild()
				m.ensureQuerySelectionVisible()
				updateViewport = false
			} else if m.exportPath != "" {
				m.exportPath = ""
				m.resizeViewport()
				m.rebuild()
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
			if len(m.snapshot.Queries) == 0 {
				m.queryIndex = 0
				m.queryDetail = false
			} else {
				m.queryIndex = min(m.queryIndex, len(m.snapshot.Queries)-1)
			}
			m.refreshed = time.Now()
			m.status = fmt.Sprintf("Refreshed %s · snapshot %s", m.refreshed.Format("15:04:05"), msg.context.Fingerprint)
			m.rebuild()
		}
	case exportMessage:
		m.exporting = false
		if msg.err != nil {
			m.exportPath = ""
			m.status = "Export failed: " + compact(msg.err.Error(), max(20, m.width-20))
		} else {
			m.exportPath = msg.path
			m.status = "Agent bundle exported: " + msg.path
		}
		m.resizeViewport()
		m.rebuild()
	}

	var command tea.Cmd
	m.spinner, command = m.spinner.Update(message)
	commands = append(commands, command)
	if updateViewport {
		m.viewport, command = m.viewport.Update(message)
		commands = append(commands, command)
	}
	return m, tea.Batch(commands...)
}

func (m Model) View() string {
	if m.width == 0 {
		return "Starting mysq…"
	}
	canvas := lipgloss.NewStyle().Foreground(text).Width(m.width).Height(m.height)
	if m.width < 52 || m.height < 18 {
		return canvas.Render(m.tooSmall())
	}
	header := m.header()
	footer := m.footer()
	bodyHeight := max(8, m.height-2-m.footerHeight())
	return canvas.Render(lipgloss.JoinVertical(lipgloss.Left, header, m.tabBar(), m.contentPanel(m.width, bodyHeight), footer))
}

func (m Model) tooSmall() string {
	message := lipgloss.NewStyle().Foreground(cyan).Bold(true).Render("◆ MYSQ") + "\n\n" +
		lipgloss.NewStyle().Foreground(text).Bold(true).Render("A little more room, please") + "\n" +
		lipgloss.NewStyle().Foreground(muted).Render(fmt.Sprintf("Need 52×18 · current %d×%d", m.width, m.height)) + "\n\n" +
		keyHint("q", "quit")
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(1, 2).Width(max(8, m.width-2)).Render(message)
}

func (m Model) header() string {
	brand := lipgloss.NewStyle().Foreground(cyan).Bold(true).Render("◆ MYSQ")
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
	available := max(1, m.width-2)
	all := make([]int, len(tabs))
	for i := range tabs {
		all[i] = i
	}
	if m.tabsWidth(all) <= available {
		return lipgloss.NewStyle().Background(surfaceAlt).Padding(0, 1).Width(max(1, m.width)).Render(m.renderTabs(all, false))
	}

	window := uniqueTabIndices((m.tab+len(tabs)-1)%len(tabs), m.tab, (m.tab+1)%len(tabs))
	if m.tabsWidth(window)+4 <= available {
		line := "‹ " + m.renderTabs(window, false) + " ›"
		return lipgloss.NewStyle().Background(surfaceAlt).Padding(0, 1).Width(max(1, m.width)).Render(line)
	}
	label := fmt.Sprintf("‹  %d/%d  %s", m.tab+1, len(tabs), tabs[m.tab])
	if count := m.tabCount(m.tab); count != "" {
		label += "  (" + count + ")"
	}
	label += "  ›"
	return lipgloss.NewStyle().Background(surfaceAlt).Foreground(cyan).Bold(true).Padding(0, 1).Width(max(1, m.width)).Render(label)
}

func (m Model) renderTabs(indices []int, measureOnly bool) string {
	items := make([]string, 0, len(indices))
	for _, index := range indices {
		label := fmt.Sprintf("%d %s", index+1, tabs[index])
		if count := m.tabCount(index); count != "" {
			label += " (" + count + ")"
		}
		if measureOnly {
			items = append(items, " "+label+" ")
			continue
		}
		style := lipgloss.NewStyle().Foreground(muted).Padding(0, 1)
		if index == m.tab {
			label = fmt.Sprintf("● %d %s", index+1, strings.ToUpper(tabs[index]))
			if count := m.tabCount(index); count != "" {
				label += " (" + count + ")"
			}
			style = style.Foreground(cyan).Background(surface).Bold(true)
		}
		items = append(items, style.Render(label))
	}
	divider := lipgloss.NewStyle().Foreground(border).Render("│")
	if measureOnly {
		divider = "│"
	}
	return strings.Join(items, divider)
}

func (m Model) tabsWidth(indices []int) int {
	return lipgloss.Width(m.renderTabs(indices, true))
}

func (m Model) tabCount(index int) string {
	if m.snapshot == nil {
		return ""
	}
	switch tabs[index] {
	case "Findings":
		return fmt.Sprint(len(m.snapshot.Findings))
	case "Queries":
		return fmt.Sprint(len(m.snapshot.Queries))
	case "Engine":
		return fmt.Sprint(len(m.snapshot.WaitEvents))
	case "Tables":
		return fmt.Sprint(len(m.snapshot.Tables))
	case "Connections":
		return fmt.Sprint(len(m.snapshot.Processes))
	default:
		return ""
	}
}

func uniqueTabIndices(values ...int) []int {
	seen := make(map[int]bool, len(values))
	result := make([]int, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func (m Model) footer() string {
	if m.exportPath != "" {
		title := lipgloss.NewStyle().Foreground(green).Bold(true).Render("✓ Agent bundle exported:")
		dismiss := keyHint("esc", "dismiss")
		path := lipgloss.NewStyle().Foreground(text).Render("↳ " + compactPath(m.exportPath, max(12, m.width-4)))
		lines := padBetween(title, dismiss, max(1, m.width-2)) + "\n" + path
		return lipgloss.NewStyle().Background(surfaceAlt).Padding(0, 1).Width(max(1, m.width)).Render(lines)
	}
	keys := keyHint("←/→", "views") + "  " + keyHint("↑/↓", "scroll") + "  " + keyHint("j/k", "scroll") + "  " + keyHint("r", "refresh") + "  " + keyHint("e", "export") + "  " + keyHint("?", "help") + "  " + keyHint("q", "quit")
	if tabs[m.tab] == "Queries" {
		keys = keyHint("↑/↓", "select") + "  " + keyHint("enter", "open") + "  " + keyHint("←/→", "views") + "  " + keyHint("r", "refresh") + "  " + keyHint("q", "quit")
		if m.queryDetail {
			keys = keyHint("esc", "queries") + "  " + keyHint("↑/↓", "scroll") + "  " + keyHint("j/k", "scroll") + "  " + keyHint("r", "refresh") + "  " + keyHint("q", "quit")
		}
	}
	status := m.status
	if status == "" {
		status = "Read-only · SQL literals redacted"
	}
	if m.help {
		if m.width < 96 {
			keys = keyHint("1–7", "jump") + "  " + keyHint("g/G", "ends") + "  " + keyHint("pgup/dn", "page") + "  " + keyHint("e", "export")
		} else {
			keys = keyHint("1–7", "jump") + "  " + keyHint("g/G", "top/bottom") + "  " + keyHint("pgup/dn", "page") + "  " + keyHint("e", "agent bundle")
		}
	} else if m.width < 96 {
		if tabs[m.tab] == "Queries" {
			keys = keyHint("↑/↓", "select") + "  " + keyHint("enter", "open") + "  " + keyHint("←/→", "views")
			if m.queryDetail {
				keys = keyHint("esc", "queries") + "  " + keyHint("↑/↓", "scroll")
			}
		} else {
			keys = keyHint("←/→", "view") + "  " + keyHint("↑/↓", "scroll") + "  " + keyHint("q", "quit")
		}
	}
	line := padBetween(lipgloss.NewStyle().Foreground(muted).Render(compact(status, max(16, m.width/2))), keys, max(1, m.width-2))
	return lipgloss.NewStyle().Background(surfaceAlt).Padding(0, 1).Width(max(1, m.width)).Render(line)
}

func (m Model) footerHeight() int {
	if m.exportPath != "" {
		return 2
	}
	return 1
}

func (m *Model) resizeViewport() {
	bodyHeight := max(8, m.height-2-m.footerHeight())
	m.viewport.Width = max(24, m.width-4)
	m.viewport.Height = max(4, bodyHeight-2)
}

func (m Model) contentPanel(width, height int) string {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(0, 1).Width(width - 2).Height(max(1, height-2)).Render(m.viewport.View())
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
		if m.queryDetail {
			content = queryDetail(m.snapshot, m.viewport.Width, m.queryIndex)
		} else {
			content = queries(m.snapshot, m.viewport.Width, m.queryIndex)
		}
	case "Engine":
		content = engine(m.snapshot, m.viewport.Width)
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

func (m *Model) ensureQuerySelectionVisible() {
	// The query header occupies two lines. Keep the selected statement inside
	// the viewport as the engineer walks a long digest list.
	line := m.queryIndex + 2
	if line < m.viewport.YOffset {
		m.viewport.SetYOffset(line)
	} else if line >= m.viewport.YOffset+m.viewport.Height {
		m.viewport.SetYOffset(max(0, line-m.viewport.Height+1))
	}
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
		gaugeLine("Redo checkpoint", ctx.Metrics.RedoCheckpointAgePct, fmt.Sprintf("%.1f%%", ctx.Metrics.RedoCheckpointAgePct), colorForPercent(ctx.Metrics.RedoCheckpointAgePct, 60, 80), pressureInner),
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
			lipgloss.NewStyle().Foreground(cyan).Render("Press 5 for remediation")
	}
	findingBox := panelBox("PRIORITY SIGNAL", topFinding, findingWidth)
	lower := lipgloss.JoinHorizontal(lipgloss.Top, pressureBox, " ", findingBox)
	if width < 82 {
		lower = lipgloss.JoinVertical(lipgloss.Left, pressureBox, findingBox)
	}

	identity := lipgloss.NewStyle().Foreground(muted).Render(fmt.Sprintf("%s %s · uptime %s · %.1fs collection window",
		ctx.Server.Flavor, ctx.Server.Version, humanDuration(ctx.Server.UptimeSeconds), float64(ctx.IntervalMillis)/1000))
	result := postureBox + "\n" + cards + "\n" + lower + "\n" + mysqlInvestigationPanels(ctx, width)
	if conditional := overviewConditionalPanels(ctx, width); conditional != "" {
		result += "\n" + conditional
	}
	return result + "\n" + identity
}

func mysqlInvestigationPanels(ctx *model.Context, width int) string {
	loadWidth, queryWidth, contentionWidth := width, width, width
	if width >= 96 {
		loadWidth = width / 3
		queryWidth = width / 3
		contentionWidth = width - loadWidth - queryWidth - 2
	}
	load := summarizeCurrentLoad(ctx)
	loadBody := lipgloss.NewStyle().Foreground(cyan).Bold(true).Render(fmt.Sprintf("%d active", load.active)) +
		lipgloss.NewStyle().Foreground(muted).Render(fmt.Sprintf("  ·  %d executing  ·  %d waiting", load.executing, load.waiting)) + "\n" +
		labelValue("TOP SQL", summarizeTopSQL(ctx, max(12, loadWidth-14))) + "\n" +
		labelValue("TOP WAIT", load.topWait) + "\n" + labelValue("TOP USER", load.topUser)

	queryBody := labelValue("P95 / P99", duration(ctx.StatementLatency.P95Millis)+" / "+duration(ctx.StatementLatency.P99Millis)) + "\n" +
		labelValue("ERRORS / WARNINGS", fmt.Sprintf("%.2f/s / %.2f/s", ctx.Metrics.StatementErrorsPerSec, ctx.Metrics.StatementWarningsPerSec)) + "\n" +
		labelValue("FULL SCANS / DISK TEMP", fmt.Sprintf("%.2f/s / %.1f%%", ctx.Metrics.FullScansPerSecond, ctx.Metrics.TempDiskTablePercent)) + "\n" +
		labelValue("SLOW / BUFFER WAITS", fmt.Sprintf("%.2f/s / %.2f/s", ctx.Metrics.SlowQueriesPerSecond, ctx.Metrics.BufferPoolWaitsPerSec))

	pendingMetadata := 0
	for _, lock := range ctx.MetadataLocks {
		if strings.EqualFold(lock.Status, "PENDING") {
			pendingMetadata++
		}
	}
	oldest := uint64(0)
	for _, transaction := range ctx.Transactions {
		oldest = maxUint64(oldest, transaction.AgeSeconds)
	}
	contentionBody := labelValue("ROW / METADATA WAITERS", fmt.Sprintf("%d / %d", len(ctx.Locks), pendingMetadata)) + "\n" +
		labelValue("BLOCKER", summarizeTopBlocker(ctx, max(12, contentionWidth-14))) + "\n" +
		labelValue("OLDEST / PURGE HISTORY", humanDuration(oldest)+" / "+humanCount(ctx.Metrics.HistoryListLength)) + "\n" +
		labelValue("DEADLOCKS / TIMEOUTS", fmt.Sprintf("%.2f/s / %.2f/s", ctx.Metrics.DeadlocksPerSecond, ctx.Metrics.LockTimeoutsPerSecond))

	if width < 96 {
		return panelBox("CURRENT MYSQL LOAD", loadBody, width) + "\n" +
			panelBox("QUERY HEALTH", queryBody, width) + "\n" + panelBox("CONTENTION", contentionBody, width)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top,
		panelBox("CURRENT MYSQL LOAD", loadBody, loadWidth), " ",
		panelBox("QUERY HEALTH", queryBody, queryWidth), " ",
		panelBox("CONTENTION", contentionBody, contentionWidth))
}

func summarizeTopSQL(ctx *model.Context, width int) string {
	if len(ctx.StatementSamples) == 0 {
		return "none in sample"
	}
	top := ctx.StatementSamples[0]
	prefix := fmt.Sprintf("%.1f%% · ", top.DatabaseTimeSharePercent)
	return prefix + compact(top.Statement, max(8, width-lipgloss.Width(prefix)))
}

func summarizeTopBlocker(ctx *model.Context, width int) string {
	if len(ctx.Locks) == 0 {
		return "none active"
	}
	counts := make(map[string]int)
	objects := make(map[string]string)
	for _, lock := range ctx.Locks {
		counts[lock.BlockingTransaction]++
		if objects[lock.BlockingTransaction] == "" {
			objects[lock.BlockingTransaction] = strings.Trim(strings.TrimSpace(lock.Schema+"."+lock.Table), ".")
		}
	}
	blocker, waiters := "", 0
	for transaction, count := range counts {
		if count > waiters || (count == waiters && transaction < blocker) {
			blocker, waiters = transaction, count
		}
	}
	identity := "trx " + blocker
	age := uint64(0)
	for _, transaction := range ctx.Transactions {
		if transaction.ID == blocker {
			age = transaction.AgeSeconds
			if transaction.User != "" {
				identity = transaction.User
			}
			break
		}
	}
	parts := []string{identity, fmt.Sprintf("%dx", waiters)}
	if object := objects[blocker]; object != "" {
		parts = append(parts, object)
	} else if age > 0 {
		parts = append(parts, humanDuration(age))
	}
	return compact(strings.Join(parts, " · "), width)
}

func overviewConditionalPanels(ctx *model.Context, width int) string {
	panels := make([]string, 0, 2)
	if ctx.Replication != nil {
		r := ctx.Replication
		lag := "unknown"
		if r.SecondsBehind != nil {
			lag = fmt.Sprintf("%ds", *r.SecondsBehind)
		}
		workerErrors := 0
		for _, worker := range r.Workers {
			if worker.LastErrorNumber != 0 {
				workerErrors++
			}
		}
		state, color := "healthy", green
		if !strings.EqualFold(r.IORunning, "Yes") || !strings.EqualFold(r.SQLRunning, "Yes") || workerErrors > 0 || r.LastIOError != "" || r.LastSQLError != "" {
			state, color = "attention", red
		}
		body := lipgloss.NewStyle().Foreground(color).Bold(true).Render("● "+state) + "  " +
			labelValue("IO / SQL / APPLIER", r.IORunning+" / "+r.SQLRunning+" / "+fallback(r.ApplierState, "unknown")) + "  ·  " +
			labelValue("LAG", lag) + "  ·  " + labelValue("WORKERS / ERRORS", fmt.Sprintf("%d / %d", len(r.Workers), workerErrors)) + "  ·  " +
			labelValue("RETRIES", humanCount(r.TransactionRetries))
		panels = append(panels, panelBox("REPLICATION STATUS", body, width))
	}
	if ctx.Instrumentation.TotalLost > 0 || len(ctx.Instrumentation.DisabledConsumers) > 0 {
		body := lipgloss.NewStyle().Foreground(yellow).Bold(true).Render("● degraded") + "  " +
			labelValue("DIGESTS", fmt.Sprintf("%d/%d", ctx.Instrumentation.DigestRows, ctx.Instrumentation.DigestCapacity)) + "  ·  " +
			labelValue("LOST", humanCount(ctx.Instrumentation.TotalLost))
		if len(ctx.Instrumentation.DisabledConsumers) > 0 {
			body += "\n" + labelValue("DISABLED", compact(strings.Join(ctx.Instrumentation.DisabledConsumers, ", "), max(12, width-14)))
		}
		panels = append(panels, panelBox("DATA COVERAGE", body, width))
	}
	return strings.Join(panels, "\n")
}

type currentLoadSummary struct {
	active, executing, waiting int
	topWait, topUser           string
}

func summarizeCurrentLoad(ctx *model.Context) currentLoadSummary {
	result := currentLoadSummary{topWait: "none observed", topUser: "none active"}
	users := make(map[string]int)
	for _, process := range ctx.Processes {
		if strings.EqualFold(process.Command, "Sleep") || strings.EqualFold(process.Command, "Daemon") {
			continue
		}
		result.active++
		if process.WaitEvent != "" {
			result.waiting++
		} else {
			result.executing++
		}
		if process.User != "" {
			users[process.User]++
		}
	}
	if len(ctx.WaitEvents) > 0 && ctx.WaitEvents[0].SampleLatencyMillis > 0 {
		result.topWait = fmt.Sprintf("%s  %.1f%%", ctx.WaitEvents[0].Class, ctx.WaitEvents[0].SampleSharePercent)
	}
	result.topUser = topCount(users)
	return result
}

func topCount(values map[string]int) string {
	name, count := "", 0
	for candidate, candidateCount := range values {
		if candidateCount > count || (candidateCount == count && candidate < name) {
			name, count = candidate, candidateCount
		}
	}
	if name == "" {
		return "none active"
	}
	return fmt.Sprintf("%s  %d session(s)", name, count)
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

func queries(ctx *model.Context, width, selected int) string {
	if len(ctx.Queries) == 0 {
		return empty("No statement digests available. Check Performance Schema consumers and privileges.")
	}
	var total float64
	for _, query := range ctx.Queries {
		total += query.TotalLatencyMillis
	}
	var out strings.Builder
	wide := width >= 96
	compactLayout := width < 68
	widths := []int{2, 9, 8, 9, 11, 14, max(24, width-53)}
	headings := []string{"", "DB TIME", "CALLS", "P95", "ROWS EXAM", "USER", "QUERY"}
	if compactLayout {
		widths = []int{2, 9, 12, max(20, width-23)}
		headings = []string{"", "DB TIME", "USER", "QUERY"}
	} else if !wide {
		widths = []int{2, 9, 8, 9, 13, max(24, width-41)}
		headings = []string{"", "DB TIME", "CALLS", "P95", "USER", "QUERY"}
	}
	out.WriteString(row(headings, widths, true) + "\n")
	for index, query := range ctx.Queries {
		users := "—"
		if len(query.ActiveUsers) > 0 {
			users = strings.Join(query.ActiveUsers, ",")
		}
		marker := " "
		if index == selected {
			marker = "›"
		}
		values := []string{marker, duration(query.TotalLatencyMillis), humanCount(query.Calls), duration(query.P95LatencyMillis),
			humanCount(query.RowsExamined), users, query.Statement}
		if compactLayout {
			values = []string{marker, duration(query.TotalLatencyMillis), users, query.Statement}
		} else if !wide {
			values = []string{marker, duration(query.TotalLatencyMillis), humanCount(query.Calls), duration(query.P95LatencyMillis), users, query.Statement}
		}
		out.WriteString(selectableRow(values, widths, index == selected) + "\n")
	}
	out.WriteString("\n" + lipgloss.NewStyle().Foreground(muted).Render(fmt.Sprintf("Sorted by database time  ·  user is point-in-time  ·  selected share %.1f%%  ·  literals removed", queryShare(ctx.Queries, selected, total))))
	return out.String()
}

func queryShare(queries []model.Query, selected int, total float64) float64 {
	if selected < 0 || selected >= len(queries) || total <= 0 {
		return 0
	}
	return queries[selected].TotalLatencyMillis * 100 / total
}

func queryDetail(ctx *model.Context, width, selected int) string {
	if selected < 0 || selected >= len(ctx.Queries) {
		return empty("The selected query is no longer available. Press Esc to return to Queries.")
	}
	query := ctx.Queries[selected]
	users := "not active in this snapshot"
	if len(query.ActiveUsers) > 0 {
		users = strings.Join(query.ActiveUsers, ", ")
	}
	var total float64
	for _, item := range ctx.Queries {
		total += item.TotalLatencyMillis
	}

	important := strings.Join([]string{
		labelValue("USER", users),
		labelValue("DATABASE", fallback(query.Schema, "all databases")),
		labelValue("DB TIME", fmt.Sprintf("%s (%.1f%%)", duration(query.TotalLatencyMillis), queryShare(ctx.Queries, selected, total))),
		labelValue("CALLS", humanCount(query.Calls)),
		labelValue("P95", duration(query.P95LatencyMillis)),
	}, "  ·  ")
	var out strings.Builder
	out.WriteString(panelBox(fmt.Sprintf("QUERY %d OF %d", selected+1, len(ctx.Queries)),
		lipgloss.NewStyle().Width(max(20, width-4)).Render(important), width))
	out.WriteString("\n" + sectionTitle("NORMALIZED SQL") + "\n")
	out.WriteString(lipgloss.NewStyle().Foreground(text).Bold(true).Width(max(20, width-2)).Render(query.Statement) + "\n")

	evidence := []string{
		labelValue("AVG / P99 / MAX", duration(query.MeanLatencyMillis)+" / "+duration(query.P99LatencyMillis)+" / "+duration(query.MaxLatencyMillis)),
		labelValue("ROWS EXAMINED", humanCount(query.RowsExamined)),
		labelValue("ROWS SENT", humanCount(query.RowsSent)),
		labelValue("ERRORS / WARNINGS", humanCount(query.Errors)+" / "+humanCount(query.Warnings)),
		labelValue("NO INDEX CALLS", humanCount(query.NoIndexUsed)),
		labelValue("FULL SCANS", humanCount(query.FullScans)),
		labelValue("TEMP TABLES", humanCount(query.TmpTables)),
		labelValue("TEMP ON DISK", humanCount(query.TmpDiskTables)),
	}
	out.WriteString("\n" + sectionTitle("EXECUTION EVIDENCE") + "\n" +
		lipgloss.NewStyle().Width(max(20, width-2)).Render(strings.Join(evidence, "  ·  ")) + "\n")
	if query.FirstSeen != "" || query.LastSeen != "" {
		out.WriteString("\n" + lipgloss.NewStyle().Width(max(20, width-2)).Render(labelValue("OBSERVED", fallback(query.FirstSeen, "—")+" → "+fallback(query.LastSeen, "—"))) + "\n")
	}
	if query.Digest != "" {
		out.WriteString("\n" + lipgloss.NewStyle().Foreground(muted).Width(max(20, width-2)).Render("DIGEST  "+query.Digest))
	}
	return out.String()
}

func labelValue(label, value string) string {
	return lipgloss.NewStyle().Foreground(muted).Bold(true).Render(label+" ") +
		lipgloss.NewStyle().Foreground(text).Render(value)
}

func engine(ctx *model.Context, width int) string {
	var out strings.Builder
	currentLoad := summarizeCurrentLoad(ctx)
	load := fmt.Sprintf("active %d  ·  executing %d  ·  waiting %d  ·  top wait %s  ·  top user %s",
		currentLoad.active, currentLoad.executing, currentLoad.waiting, currentLoad.topWait, currentLoad.topUser)
	out.WriteString(panelBox("CURRENT DATABASE LOAD", lipgloss.NewStyle().Foreground(text).Bold(true).Render(load), width) + "\n")

	out.WriteString(sectionTitle("INNODB I/O AND REDO") + "\n")
	metricWidths := []int{max(24, width/3), max(18, width/5), max(24, width-width/3-width/5)}
	metricRows := [][]string{
		{"data reads / writes", fmt.Sprintf("%.1f / %.1f ops/s", ctx.Metrics.DataReadsPerSecond, ctx.Metrics.DataWritesPerSecond), "fsync " + fmt.Sprintf("%.1f/s", ctx.Metrics.DataFsyncsPerSecond)},
		{"pending reads / writes", fmt.Sprintf("%d / %d", ctx.Metrics.PendingReads, ctx.Metrics.PendingWrites), fmt.Sprintf("pending fsync %d", ctx.Metrics.PendingFsyncs)},
		{"redo generated", humanBytes(uint64(ctx.Metrics.RedoBytesPerSecond)) + "/s", fmt.Sprintf("writes %.1f/s · fsync %.1f/s", ctx.Metrics.RedoWritesPerSecond, ctx.Metrics.RedoFsyncsPerSecond)},
		{"checkpoint age", humanBytes(ctx.Metrics.RedoCheckpointAgeBytes), fmt.Sprintf("%.2f%% of %s", ctx.Metrics.RedoCheckpointAgePct, humanBytes(ctx.Metrics.RedoCapacityBytes))},
		{"buffer pool data / dirty", humanBytes(ctx.Metrics.BufferPoolDataBytes) + " / " + humanBytes(ctx.Metrics.BufferPoolDirtyBytes), fmt.Sprintf("waits %.2f/s", ctx.Metrics.BufferPoolWaitsPerSec)},
		{"network in / out", humanBytes(uint64(ctx.Metrics.NetworkInBytesPerSec)) + "/s / " + humanBytes(uint64(ctx.Metrics.NetworkOutBytesPerSec)) + "/s", fmt.Sprintf("scans %.2f/s · sort merges %.2f/s", ctx.Metrics.FullScansPerSecond, ctx.Metrics.SortMergePassesPerSec)},
	}
	out.WriteString(rows(metricRows, []string{"SIGNAL", "VALUE", "RELATED"}, metricWidths) + "\n")

	if len(ctx.WaitEvents) > 0 {
		out.WriteString(sectionTitle("SAMPLED WAIT PRESSURE") + "\n")
		waitWidths := []int{8, 12, 10, 12, max(28, width-42)}
		out.WriteString(row([]string{"SHARE", "WAIT/S", "EVENTS/S", "CUM TOTAL", "EVENT"}, waitWidths, true) + "\n")
		for _, wait := range ctx.WaitEvents {
			out.WriteString(row([]string{fmt.Sprintf("%.1f%%", wait.SampleSharePercent), duration(wait.WaitMillisPerSecond) + "/s",
				fmt.Sprintf("%.1f", wait.EventsPerSecond), duration(wait.TotalLatencyMillis), wait.Name}, waitWidths, false) + "\n")
		}
	}

	if len(ctx.FileIO) > 0 {
		out.WriteString("\n" + sectionTitle("MYSQL FILE I/O") + "\n")
		ioWidths := []int{10, 10, 12, 12, max(28, width-44)}
		out.WriteString(row([]string{"READ/S", "WRITE/S", "READ LAT", "WRITE LAT", "FILE INSTRUMENT"}, ioWidths, true) + "\n")
		for _, item := range ctx.FileIO[:min(12, len(ctx.FileIO))] {
			out.WriteString(row([]string{fmt.Sprintf("%.1f", item.ReadsPerSecond), fmt.Sprintf("%.1f", item.WritesPerSecond),
				duration(item.MeanReadLatencyMillis), duration(item.MeanWriteLatencyMillis), item.Name}, ioWidths, false) + "\n")
		}
	}

	if len(ctx.ServerErrors) > 0 {
		out.WriteString("\n" + sectionTitle("MYSQL ERRORS AND WARNINGS") + "\n")
		errorWidths := []int{9, 10, 10, 20, max(28, width-49)}
		out.WriteString(row([]string{"ERROR", "SAMPLE/S", "TOTAL", "LAST SEEN", "NAME"}, errorWidths, true) + "\n")
		for _, item := range ctx.ServerErrors[:min(10, len(ctx.ServerErrors))] {
			out.WriteString(row([]string{fmt.Sprint(item.Number), fmt.Sprintf("%.2f", item.RaisedPerSecond), humanCount(item.Raised),
				item.LastSeen, item.Name}, errorWidths, false) + "\n")
		}
	}

	if ctx.Replication != nil {
		replication := ctx.Replication
		lag := "unknown"
		if replication.SecondsBehind != nil {
			lag = fmt.Sprintf("%ds", *replication.SecondsBehind)
		}
		workerErrors := 0
		for _, worker := range replication.Workers {
			if worker.LastErrorNumber != 0 {
				workerErrors++
			}
		}
		body := labelValue("SOURCE", fmt.Sprintf("%s:%d", replication.SourceHost, replication.SourcePort)) + "  ·  " +
			labelValue("IO / SQL / APPLIER", replication.IORunning+" / "+replication.SQLRunning+" / "+replication.ApplierState) + "\n" +
			labelValue("LAG", lag) + "  ·  " + labelValue("RETRIES", humanCount(replication.TransactionRetries)) + "  ·  " +
			labelValue("WORKERS / ERRORS", fmt.Sprintf("%d / %d", len(replication.Workers), workerErrors))
		out.WriteString("\n" + panelBox("REPLICATION", body, width) + "\n")
	}

	coverageState := "complete"
	coverageColor := green
	if ctx.Instrumentation.TotalLost > 0 || len(ctx.Instrumentation.DisabledConsumers) > 0 {
		coverageState = "degraded"
		coverageColor = yellow
	}
	coverage := lipgloss.NewStyle().Foreground(coverageColor).Bold(true).Render("● "+coverageState) + "  " +
		labelValue("DIGESTS", fmt.Sprintf("%d/%d (%.1f%%)", ctx.Instrumentation.DigestRows, ctx.Instrumentation.DigestCapacity, ctx.Instrumentation.DigestUtilizationPercent)) + "  ·  " +
		labelValue("LOST", humanCount(ctx.Instrumentation.TotalLost))
	if len(ctx.Instrumentation.DisabledConsumers) > 0 {
		coverage += "\n" + labelValue("DISABLED", strings.Join(ctx.Instrumentation.DisabledConsumers, ", "))
	}
	out.WriteString("\n" + panelBox("INSTRUMENTATION COVERAGE", coverage, width) + "\n")

	if len(ctx.MemoryConsumers) > 0 {
		out.WriteString("\n" + sectionTitle("TOP MYSQL MEMORY CONSUMERS") + "\n")
		memoryWidths := []int{13, 13, 12, max(28, width-38)}
		out.WriteString(row([]string{"CURRENT", "HIGH WATER", "ALLOCATIONS", "CONSUMER"}, memoryWidths, true) + "\n")
		for _, consumer := range ctx.MemoryConsumers {
			out.WriteString(row([]string{humanBytes(consumer.CurrentBytes), humanBytes(consumer.HighBytes), humanCount(consumer.Allocations), consumer.Name}, memoryWidths, false) + "\n")
		}
	}
	out.WriteString("\n" + lipgloss.NewStyle().Foreground(muted).Render("Wait, file I/O, and error rates use the collection interval; cumulative totals are retained for forensic context."))
	return out.String()
}

func rows(values [][]string, headings []string, widths []int) string {
	var out strings.Builder
	out.WriteString(row(headings, widths, true) + "\n")
	for _, values := range values {
		out.WriteString(row(values, widths, false) + "\n")
	}
	return out.String()
}

func tablesView(ctx *model.Context, width int) string {
	if len(ctx.Tables) == 0 {
		return empty("No application tables are visible to the monitoring user.")
	}
	var out strings.Builder
	wide := width >= 110
	widths := []int{12, 11, 11, 11, 5, max(20, width-50)}
	headings := []string{"SIZE", "ROWS", "READS", "WRITES", "PK", "TABLE"}
	if wide {
		widths = []int{11, 10, 9, 11, 9, 11, 5, max(22, width-66)}
		headings = []string{"SIZE", "ROWS", "READS", "READ TIME", "WRITES", "WRITE TIME", "PK", "TABLE"}
	}
	out.WriteString(row(headings, widths, true) + "\n")
	for _, table := range ctx.Tables {
		pk := "yes"
		if !table.HasPrimaryKey {
			pk = "NO"
		}
		values := []string{humanBytes(table.TotalBytes), humanCount(table.EstimatedRows), humanCount(table.Reads), humanCount(table.Writes), pk, table.Schema + "." + table.Name}
		if wide {
			values = []string{humanBytes(table.TotalBytes), humanCount(table.EstimatedRows), humanCount(table.Reads), duration(table.ReadLatencyMillis),
				humanCount(table.Writes), duration(table.WriteLatencyMillis), pk, table.Schema + "." + table.Name}
		}
		out.WriteString(row(values, widths, false) + "\n")
	}
	if len(ctx.Indexes) > 0 {
		out.WriteString("\n" + sectionTitle("INDEX ACTIVITY") + "\n")
		indexWidths := []int{10, 10, 11, 10, max(24, width-41)}
		out.WriteString(row([]string{"READS", "WRITES", "CARDINALITY", "FLAGS", "INDEX AND COLUMNS"}, indexWidths, true) + "\n")
		for _, index := range ctx.Indexes {
			flags := ""
			if index.Unique {
				flags += "unique "
			}
			if !index.Visible {
				flags += "hidden"
			}
			out.WriteString(row([]string{humanCount(index.Reads), humanCount(index.Writes), humanCount(index.Cardinality), strings.TrimSpace(flags), index.Schema + "." + index.Table + "." + index.Name + " (" + index.Columns + ")"}, indexWidths, false) + "\n")
		}
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
		processWidths := []int{8, 12, 8, 18, max(22, width-46)}
		out.WriteString("\n" + row([]string{"ID", "USER", "TIME", "WAIT", "STATEMENT"}, processWidths, true) + "\n")
		for _, process := range ctx.Processes {
			out.WriteString(row([]string{fmt.Sprint(process.ID), process.User, fmt.Sprintf("%ds", process.Seconds), processActivity(process), process.Statement}, processWidths, false) + "\n")
		}
	} else {
		processWidths := []int{8, 13, 18, 8, 28, max(28, width-75)}
		out.WriteString("\n" + row([]string{"ID", "USER", "HOST", "TIME", "WAIT", "STATEMENT"}, processWidths, true) + "\n")
		for _, process := range ctx.Processes {
			out.WriteString(row([]string{fmt.Sprint(process.ID), process.User, process.Host, fmt.Sprintf("%ds", process.Seconds), processActivity(process), process.Statement}, processWidths, false) + "\n")
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
	if len(ctx.Transactions) > 0 {
		out.WriteString("\n\n" + sectionTitle("ACTIVE TRANSACTIONS") + "\n")
		transactionWidths := []int{11, 12, 8, 9, 10, max(28, width-50)}
		out.WriteString(row([]string{"TRX", "USER", "AGE", "LOCKED", "MODIFIED", "STATEMENT"}, transactionWidths, true) + "\n")
		for _, transaction := range ctx.Transactions {
			out.WriteString(row([]string{transaction.ID, transaction.User, fmt.Sprintf("%ds", transaction.AgeSeconds), humanCount(transaction.RowsLocked), humanCount(transaction.RowsModified), transaction.Statement}, transactionWidths, false) + "\n")
		}
	}
	if len(ctx.MetadataLocks) > 0 {
		out.WriteString("\n\n" + sectionTitle("METADATA LOCKS") + "\n")
		metadataWidths := []int{9, 12, 12, 13, 12, max(24, width-58)}
		out.WriteString(row([]string{"STATUS", "USER", "TYPE", "DURATION", "OBJECT TYPE", "OBJECT"}, metadataWidths, true) + "\n")
		for _, lock := range ctx.MetadataLocks {
			object := strings.TrimPrefix(lock.Schema+"."+lock.Object, ".")
			out.WriteString(row([]string{lock.Status, lock.User, lock.LockType, lock.Duration, lock.ObjectType, object}, metadataWidths, false) + "\n")
		}
	}
	return out.String()
}

func processActivity(process model.Process) string {
	if process.WaitEvent != "" {
		return process.WaitEvent
	}
	if process.State != "" {
		return process.State
	}
	if strings.EqualFold(process.Command, "Sleep") || strings.EqualFold(process.Command, "Daemon") {
		return "idle"
	}
	return "CPU / uninstrumented"
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

func selectableRow(values []string, widths []int, selected bool) string {
	style := lipgloss.NewStyle().Foreground(text)
	if selected {
		style = style.Foreground(cyan).Background(surfaceAlt).Bold(true)
	}
	var out strings.Builder
	for i, value := range values {
		out.WriteString(style.Width(widths[i]).MaxWidth(widths[i]).Render(compact(strings.ReplaceAll(value, "\n", " "), widths[i]-1)))
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

func compactMiddle(value string, width int) string {
	runes := []rune(value)
	if width <= 1 || len(runes) <= width {
		return value
	}
	left := (width - 1) / 2
	right := width - 1 - left
	return string(runes[:left]) + "…" + string(runes[len(runes)-right:])
}

func compactPath(value string, width int) string {
	if len([]rune(value)) <= width {
		return value
	}
	base := filepath.Base(value)
	baseWidth := len([]rune(base))
	if baseWidth+2 >= width {
		return compactMiddle(value, width)
	}
	directory := strings.TrimSuffix(value, base)
	return compactMiddle(directory, width-baseWidth) + base
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

func maxUint64(a, b uint64) uint64 {
	if a > b {
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
