package tui

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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

const totalViews = 7

var tabs = [totalViews]string{"Overview", "Connections", "Queries", "Engine", "Findings", "Tables", "Config"}

type Model struct {
	ctx                         context.Context
	inspect                     Inspector
	export                      Exporter
	queryControl                QueryController
	live                        liveQueries
	snapshot                    *model.Context
	viewport                    viewport.Model
	spinner                     spinner.Model
	keyHelp                     help.Model
	keys                        navigationKeyMap
	filterInput                 textinput.Model
	width                       int
	height                      int
	tab                         int
	viewOffsets                 [totalViews]int
	queryIndex                  int
	queryDetail                 bool
	queryDetailOffset           int
	findingIndex                int
	findingDetailID             string
	blockerDetail               bool
	investigationOffset         int
	loading                     bool
	exporting                   bool
	help                        bool
	filtering                   bool
	filters                     [totalViews]string
	filterBefore                string
	filterOffsetBefore          int
	filterQueryBefore           int
	filterQueryIdentityBefore   string
	filterFindingIdentityBefore string
	status                      string
	statusOverridesFilter       bool
	exportPath                  string
	err                         error
	refreshed                   time.Time
}

var (
	// Ghostty resolves default colors and ANSI slots against its current theme,
	// including already-painted cells. Never cache light/dark RGB colors here.
	// Keep all body text on the terminal's own high-contrast foreground/background.
	surface    = lipgloss.NoColor{}
	surfaceAlt = lipgloss.NoColor{}
	border     = lipgloss.Color("8")
	muted      = lipgloss.NoColor{}
	text       = lipgloss.NoColor{}
	green      = lipgloss.Color("2")
	yellow     = lipgloss.Color("3")
	red        = lipgloss.Color("1")
	cyan       = lipgloss.Color("4")
	identity   = lipgloss.Color("6")
	number     = lipgloss.Color("5")
)

func Run(ctx context.Context, inspect Inspector, export Exporter, control ...QueryController) error {
	model := New(ctx, inspect, export, control...)
	program := tea.NewProgram(model, tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func New(ctx context.Context, inspect Inspector, export Exporter, control ...QueryController) Model {
	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = lipgloss.NewStyle().Foreground(cyan)
	keyHelp := help.New()
	keyHelp.Styles.ShortKey = lipgloss.NewStyle().Foreground(cyan).Bold(true)
	keyHelp.Styles.ShortDesc = lipgloss.NewStyle().Foreground(muted)
	keyHelp.Styles.ShortSeparator = lipgloss.NewStyle().Foreground(border)
	keyHelp.Styles.FullKey = lipgloss.NewStyle().Foreground(cyan).Bold(true)
	keyHelp.Styles.FullDesc = lipgloss.NewStyle().Foreground(text)
	keyHelp.Styles.FullSeparator = lipgloss.NewStyle().Foreground(border)
	keyHelp.Styles.Ellipsis = lipgloss.NewStyle().Foreground(muted)
	filterInput := textinput.New()
	filterInput.Prompt = "/ "
	filterInput.Placeholder = "filter current view"
	filterInput.PromptStyle = lipgloss.NewStyle().Foreground(cyan).Bold(true)
	filterInput.TextStyle = lipgloss.NewStyle().Foreground(text)
	filterInput.PlaceholderStyle = lipgloss.NewStyle().Foreground(muted)
	filterInput.Cursor.Style = lipgloss.NewStyle().Foreground(cyan)
	var queryControl QueryController
	if len(control) > 0 {
		queryControl = control[0]
	}
	return Model{
		ctx: ctx, inspect: inspect, export: export, spinner: spin,
		queryControl: queryControl,
		viewport:     viewport.New(80, 20), keyHelp: keyHelp, keys: defaultNavigationKeyMap(), filterInput: filterInput,
		loading: true,
	}
}

func (m Model) Init() tea.Cmd { return tea.Batch(m.spinner.Tick, m.inspectCommand()) }

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd
	switch msg := message.(type) {
	case sessionsMessage:
		return m.receiveSessions(msg)
	case killMessage:
		return m.receiveKill(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.saveCurrentOffset()
		m.width = msg.Width
		m.height = msg.Height
		m.resizeViewport()
		m.rebuild()
		if tabs[m.tab] == "Queries" && !m.queryDetail && !m.inInvestigation() && !m.help {
			m.ensureQuerySelectionVisible()
		}
	case inspectMessage:
		m.loading = false
		m.err = msg.err
		if msg.err != nil {
			m.setStatus("Refresh failed: "+compact(msg.err.Error(), max(20, m.width-20)), true)
			m.rebuild()
		} else {
			selectedQuery := m.selectedQueryIdentity()
			selectedFinding := m.selectedFindingID()
			wasQueryDetail := m.queryDetail
			m.snapshot = msg.context
			m.restoreFindingSelection(selectedFinding)
			if m.findingDetailID != "" && m.findingByID(m.findingDetailID) == nil {
				m.findingDetailID = ""
			}
			m.investigationOffset = 0
			if !m.restoreQuerySelection(selectedQuery) {
				m.clampQuerySelection()
				if wasQueryDetail {
					m.queryDetail = false
				}
			} else if wasQueryDetail {
				m.queryDetailOffset = 0
			}
			m.refreshed = time.Now()
			m.setStatus(fmt.Sprintf("Refreshed %s · snapshot %s", m.refreshed.Format("15:04:05"), msg.context.Fingerprint), false)
			m.rebuild()
			if tabs[m.tab] == "Queries" && !m.queryDetail && !m.inInvestigation() && !m.help {
				m.ensureQuerySelectionVisible()
			}
		}
	case exportMessage:
		m.exporting = false
		if msg.err != nil {
			m.exportPath = ""
			m.setStatus("Export failed: "+compact(msg.err.Error(), max(20, m.width-20)), true)
		} else {
			m.exportPath = msg.path
			m.setStatus("Agent bundle exported: "+msg.path, false)
		}
		m.resizeViewport()
		m.rebuild()
		if tabs[m.tab] == "Queries" && !m.queryDetail && !m.inInvestigation() && !m.help {
			m.ensureQuerySelectionVisible()
		}
	}

	var command tea.Cmd
	m.spinner, command = m.spinner.Update(message)
	commands = append(commands, command)
	m.viewport, command = m.viewport.Update(message)
	commands = append(commands, command)
	if m.filtering {
		before := m.filterInput.Value()
		m.filterInput, command = m.filterInput.Update(message)
		commands = append(commands, command)
		m.applyFilterInputChange(before)
	}
	if m.live.stage == "confirm" {
		m.live.input, command = m.live.input.Update(message)
		commands = append(commands, command)
		m.rebuild()
	}
	return m, tea.Batch(commands...)
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.width > 0 && (m.width < 52 || m.height < 18) {
		if msg.String() == "esc" && m.live.stage != "sending" {
			m.live.stage = ""
		}
		if key.Matches(msg, m.keys.Quit) {
			return m, tea.Quit
		}
		return m, nil
	}
	if m.live.stage != "" {
		return m.handleQueryAction(msg)
	}
	if m.filtering {
		return m.updateFilter(msg)
	}
	if m.help {
		switch {
		case key.Matches(msg, m.keys.Help, m.keys.Back):
			m.help = false
			m.rebuild()
			if tabs[m.tab] == "Queries" && !m.queryDetail && !m.inInvestigation() {
				m.ensureQuerySelectionVisible()
			}
			return m, nil
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case m.scroll(msg, false):
			return m, nil
		default:
			return m, nil
		}
	}

	switch {
	case key.Matches(msg, m.keys.KillQuery):
		if tabs[m.tab] == "Queries" && !m.inInvestigation() && !m.loading && !m.exporting && m.exportPath == "" {
			return m, m.loadSessions(true)
		}
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Back):
		switch {
		case m.exportPath != "":
			m.exportPath = ""
			m.resizeViewport()
			m.rebuild()
		case m.inInvestigation():
			m.findingDetailID = ""
			m.blockerDetail = false
			m.rebuild()
			m.ensureFindingSelectionVisible()
		case m.queryDetail:
			m.saveCurrentOffset()
			m.queryDetail = false
			m.rebuild()
			m.ensureQuerySelectionVisible()
		case m.activeFilter() != "":
			m.clearFilter()
		}
		return m, nil
	case key.Matches(msg, m.keys.Help):
		if !m.exporting && m.exportPath == "" {
			m.saveCurrentOffset()
			m.help = true
			m.rebuild()
		}
		return m, nil
	case key.Matches(msg, m.keys.Filter):
		if !m.loading && !m.exporting && m.exportPath == "" && m.filterable() && !m.queryDetail && !m.inInvestigation() && m.snapshot != nil {
			return m, m.startFilter()
		}
		return m, nil
	case key.Matches(msg, m.keys.NextView):
		m.switchView((m.tab + 1) % len(tabs))
		return m, nil
	case key.Matches(msg, m.keys.PreviousView):
		m.switchView((m.tab + len(tabs) - 1) % len(tabs))
		return m, nil
	case key.Matches(msg, m.keys.Jump):
		if len(msg.Runes) == 1 {
			m.saveCurrentOffset()
			m.queryDetail = false
			m.findingDetailID = ""
			m.blockerDetail = false
			m.tab = int(msg.Runes[0] - '1')
			if strings.HasPrefix(m.status, "Filter ") || m.status == "Filter cleared" {
				m.setStatus("", false)
			}
			m.rebuild()
			if tabs[m.tab] == "Queries" {
				m.ensureQuerySelectionVisible()
			}
		}
		return m, nil
	case key.Matches(msg, m.keys.Blockers):
		if m.snapshot != nil && !m.exporting && m.exportPath == "" {
			m.saveCurrentOffset()
			m.blockerDetail = true
			m.findingDetailID = ""
			m.investigationOffset = 0
			m.rebuild()
		}
		return m, nil
	case key.Matches(msg, m.keys.Open):
		if m.inInvestigation() {
			return m, nil
		}
		if tabs[m.tab] == "Overview" || tabs[m.tab] == "Findings" || tabs[m.tab] == "Connections" {
			if m.snapshot == nil {
				return m, nil
			}
			m.saveCurrentOffset()
			m.investigationOffset = 0
			if tabs[m.tab] == "Connections" {
				m.blockerDetail = true
			} else if tabs[m.tab] == "Overview" && len(m.snapshot.Findings) > 0 {
				m.findingDetailID = m.snapshot.Findings[0].ID
			} else {
				m.findingDetailID = m.selectedFindingID()
			}
			m.rebuild()
			return m, nil
		}
		visible := m.filteredContext()
		if tabs[m.tab] == "Queries" && !m.queryDetail && visible != nil && len(visible.Queries) > 0 {
			m.saveCurrentOffset()
			m.queryDetailOffset = 0
			m.queryDetail = true
			m.rebuild()
			return m, m.loadSessions(false)
		}
		return m, nil
	case m.scrollFindingList(msg):
		return m, nil
	case m.scroll(msg, tabs[m.tab] == "Queries" && !m.queryDetail && !m.inInvestigation()):
		return m, nil
	case key.Matches(msg, m.keys.Refresh):
		if !m.loading && !m.exporting {
			m.exportPath = ""
			m.resizeViewport()
			m.loading = true
			m.setStatus("Refreshing every diagnostic probe…", true)
			var sessions tea.Cmd
			if m.queryDetail {
				sessions = m.loadSessions(false)
			}
			return m, tea.Batch(m.inspectCommand(), m.spinner.Tick, sessions)
		}
		return m, nil
	case key.Matches(msg, m.keys.Export):
		if !m.loading && !m.exporting && m.exportPath == "" && m.snapshot != nil {
			m.resizeViewport()
			m.exporting = true
			m.setStatus("Writing agent bundle…", true)
			return m, m.exportCommand()
		}
	}
	return m, nil
}

func (m *Model) switchView(next int) {
	m.saveCurrentOffset()
	m.queryDetail = false
	m.findingDetailID = ""
	m.blockerDetail = false
	m.tab = next
	if strings.HasPrefix(m.status, "Filter ") || m.status == "Filter cleared" {
		m.setStatus("", false)
	}
	m.rebuild()
	if tabs[m.tab] == "Queries" {
		m.ensureQuerySelectionVisible()
	}
}

func (m *Model) scroll(msg tea.KeyMsg, selectQueries bool) bool {
	visible := m.filteredContext()
	queryCount := 0
	if visible != nil {
		queryCount = len(visible.Queries)
	}
	page := max(1, m.viewport.Height-3)
	halfPage := max(1, page/2)
	queryTarget := m.queryIndex

	switch {
	case key.Matches(msg, m.keys.Up):
		if selectQueries {
			queryTarget--
		} else {
			m.viewport.LineUp(1)
		}
	case key.Matches(msg, m.keys.Down):
		if selectQueries {
			queryTarget++
		} else {
			m.viewport.LineDown(1)
		}
	case key.Matches(msg, m.keys.PageUp):
		if selectQueries {
			queryTarget -= page
		} else {
			m.viewport.PageUp()
		}
	case key.Matches(msg, m.keys.PageDown):
		if selectQueries {
			queryTarget += page
		} else {
			m.viewport.PageDown()
		}
	case key.Matches(msg, m.keys.HalfPageUp):
		if selectQueries {
			queryTarget -= halfPage
		} else {
			m.viewport.HalfPageUp()
		}
	case key.Matches(msg, m.keys.HalfPageDown):
		if selectQueries {
			queryTarget += halfPage
		} else {
			m.viewport.HalfPageDown()
		}
	case key.Matches(msg, m.keys.Top):
		if selectQueries {
			queryTarget = 0
		} else {
			m.viewport.GotoTop()
		}
	case key.Matches(msg, m.keys.Bottom):
		if selectQueries {
			queryTarget = queryCount - 1
		} else {
			m.viewport.GotoBottom()
		}
	default:
		return false
	}

	if selectQueries {
		if queryCount == 0 {
			m.queryIndex = 0
			return true
		}
		m.saveCurrentOffset()
		m.queryIndex = min(max(0, queryTarget), queryCount-1)
		m.rebuild()
		m.ensureQuerySelectionVisible()
	}
	if !m.help {
		m.saveCurrentOffset()
	}
	return true
}

func (m *Model) saveCurrentOffset() {
	if m.help || len(m.viewOffsets) != len(tabs) || m.tab < 0 || m.tab >= len(tabs) {
		return
	}
	if m.inInvestigation() {
		m.investigationOffset = m.viewport.YOffset
		return
	}
	if m.queryDetail {
		m.queryDetailOffset = m.viewport.YOffset
		return
	}
	m.viewOffsets[m.tab] = m.viewport.YOffset
}

func (m Model) currentOffset() int {
	if m.inInvestigation() {
		return m.investigationOffset
	}
	if m.queryDetail {
		return m.queryDetailOffset
	}
	if len(m.viewOffsets) == len(tabs) && m.tab >= 0 && m.tab < len(tabs) {
		return m.viewOffsets[m.tab]
	}
	return 0
}

func (m Model) filterable() bool {
	return tabs[m.tab] == "Connections" || tabs[m.tab] == "Queries" || tabs[m.tab] == "Findings" || tabs[m.tab] == "Tables"
}

func (m Model) activeFilter() string {
	if len(m.filters) != len(tabs) || m.tab < 0 || m.tab >= len(tabs) {
		return ""
	}
	return m.filters[m.tab]
}

func (m *Model) startFilter() tea.Cmd {
	m.saveCurrentOffset()
	m.filterBefore = m.activeFilter()
	m.filterOffsetBefore = m.currentOffset()
	m.filterQueryBefore = m.queryIndex
	m.filterQueryIdentityBefore = m.selectedQueryIdentity()
	m.filterFindingIdentityBefore = m.selectedFindingID()
	m.filterInput.SetValue(m.filterBefore)
	m.filterInput.CursorEnd()
	m.filtering = true
	m.setStatus("Filtering "+strings.ToLower(tabs[m.tab])+"…", false)
	return m.filterInput.Focus()
}

func (m Model) updateFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		value := strings.TrimSpace(m.filterInput.Value())
		m.filterInput.SetValue(value)
		m.filters[m.tab] = value
		m.filtering = false
		m.filterInput.Blur()
		m.updateFilterStatus()
		return m, nil
	case "esc":
		m.filters[m.tab] = m.filterBefore
		m.restoreFindingSelection(m.filterFindingIdentityBefore)
		m.viewOffsets[m.tab] = m.filterOffsetBefore
		if !m.restoreQuerySelection(m.filterQueryIdentityBefore) {
			m.queryIndex = m.filterQueryBefore
			m.clampQuerySelection()
		}
		m.filtering = false
		m.filterInput.Blur()
		m.updateFilterStatus()
		m.rebuild()
		if tabs[m.tab] == "Queries" {
			m.ensureQuerySelectionVisible()
		}
		return m, nil
	}

	before := m.filterInput.Value()
	var command tea.Cmd
	m.filterInput, command = m.filterInput.Update(msg)
	m.applyFilterInputChange(before)
	return m, command
}

func (m *Model) clampQuerySelection() {
	queries := m.filteredQueries()
	if len(queries) == 0 {
		m.queryIndex = 0
		m.queryDetail = false
		return
	}
	m.queryIndex = min(max(0, m.queryIndex), len(queries)-1)
}

func (m Model) selectedQueryIdentity() string {
	queries := m.filteredQueries()
	if m.queryIndex < 0 || m.queryIndex >= len(queries) {
		return ""
	}
	return queryIdentity(queries[m.queryIndex])
}

func (m *Model) restoreQuerySelection(identity string) bool {
	if identity == "" {
		return false
	}
	for index, query := range m.filteredQueries() {
		if queryIdentity(query) == identity {
			m.queryIndex = index
			return true
		}
	}
	return false
}

func queryIdentity(query model.Query) string {
	if query.Digest != "" {
		return query.Schema + "\x00" + query.Digest
	}
	return query.Schema + "\x00" + query.Statement
}

func (m Model) filteredQueries() []model.Query {
	if m.snapshot == nil || m.filters[2] == "" {
		if m.snapshot == nil {
			return nil
		}
		return m.snapshot.Queries
	}
	filtered := make([]model.Query, 0, len(m.snapshot.Queries))
	for _, query := range m.snapshot.Queries {
		values := []string{query.Digest, query.Schema, query.Statement}
		values = append(values, query.ActiveUsers...)
		if containsFold(m.filters[2], values...) {
			filtered = append(filtered, query)
		}
	}
	return filtered
}

func (m *Model) applyFilterInputChange(before string) {
	if m.filterInput.Value() == before {
		return
	}
	m.filters[m.tab] = m.filterInput.Value()
	m.findingIndex = 0
	m.viewOffsets[m.tab] = 0
	if tabs[m.tab] == "Queries" {
		m.queryIndex = 0
	}
	m.rebuild()
}

func (m *Model) clearFilter() {
	m.filters[m.tab] = ""
	m.findingIndex = 0
	m.filterInput.Reset()
	m.viewOffsets[m.tab] = 0
	if tabs[m.tab] == "Queries" {
		m.queryIndex = 0
	}
	if !m.loading && !m.exporting && !m.statusOverridesFilter {
		m.setStatus("Filter cleared", false)
	}
	m.rebuild()
}

func (m *Model) updateFilterStatus() {
	if m.activeFilter() == "" {
		m.setStatus("Filter cleared", false)
		return
	}
	matched, total := m.filterCounts()
	m.setStatus(fmt.Sprintf("Filter %q · %d/%d matches", m.activeFilter(), matched, total), false)
}

func (m *Model) setStatus(status string, overridesFilter bool) {
	m.status = status
	m.statusOverridesFilter = overridesFilter
}

func (m Model) filteredContext() *model.Context {
	if m.snapshot == nil || m.activeFilter() == "" {
		return m.snapshot
	}
	filter := m.activeFilter()
	filtered := *m.snapshot
	switch tabs[m.tab] {
	case "Queries":
		filtered.Queries = m.filteredQueries()
	case "Tables":
		filtered.Indexes = make([]model.Index, 0, len(m.snapshot.Indexes))
		indexTables := make(map[string]bool)
		for _, index := range m.snapshot.Indexes {
			if containsFold(filter, index.Schema, index.Table, index.Name, index.Columns, index.Schema+"."+index.Table+"."+index.Name) {
				filtered.Indexes = append(filtered.Indexes, index)
				indexTables[index.Schema+"\x00"+index.Table] = true
			}
		}
		filtered.Tables = make([]model.Table, 0, len(m.snapshot.Tables))
		for _, table := range m.snapshot.Tables {
			if containsFold(filter, table.Schema, table.Name, table.Engine, table.Schema+"."+table.Name) || indexTables[table.Schema+"\x00"+table.Name] {
				filtered.Tables = append(filtered.Tables, table)
			}
		}
	case "Connections":
		filtered.Processes = make([]model.Process, 0, len(m.snapshot.Processes))
		for _, process := range m.snapshot.Processes {
			if containsFold(filter, fmt.Sprint(process.ID), process.User, process.Host, process.Database, process.Command, process.State, process.Digest, process.WaitEvent, process.Statement) {
				filtered.Processes = append(filtered.Processes, process)
			}
		}
		groups := renderableConnectionGroups(m.snapshot.ConnectionGroups)
		filtered.ConnectionGroups = make([]model.ConnectionGroup, 0, len(groups))
		for _, group := range groups {
			if containsFold(filter, group.Kind, group.Key) {
				filtered.ConnectionGroups = append(filtered.ConnectionGroups, group)
			}
		}
		filtered.Locks = make([]model.LockWait, 0, len(m.snapshot.Locks))
		for _, lock := range m.snapshot.Locks {
			if containsFold(filter, lock.WaitingTransaction, lock.BlockingTransaction, lock.Schema, lock.Table, lock.Index, lock.LockType, lock.LockMode) {
				filtered.Locks = append(filtered.Locks, lock)
			}
		}
		filtered.Transactions = make([]model.Transaction, 0, len(m.snapshot.Transactions))
		for _, transaction := range m.snapshot.Transactions {
			if containsFold(filter, transaction.ID, transaction.State, fmt.Sprint(transaction.ProcessID), transaction.User, transaction.Host, transaction.Statement) {
				filtered.Transactions = append(filtered.Transactions, transaction)
			}
		}
		filtered.MetadataLocks = make([]model.MetadataLock, 0, len(m.snapshot.MetadataLocks))
		for _, lock := range m.snapshot.MetadataLocks {
			if containsFold(filter, fmt.Sprint(lock.ThreadID), fmt.Sprint(lock.ProcessID), lock.User, lock.Host, lock.ObjectType, lock.Schema, lock.Object, lock.LockType, lock.Duration, lock.Status) {
				filtered.MetadataLocks = append(filtered.MetadataLocks, lock)
			}
		}
	case "Findings":
		filtered.Findings = make([]model.Finding, 0, len(m.snapshot.Findings))
		for _, finding := range m.snapshot.Findings {
			values := []string{finding.ID, string(finding.Severity), finding.Subsystem, finding.Title, finding.Summary, finding.Recommendation}
			values = append(values, finding.Objects...)
			if containsFold(filter, values...) {
				filtered.Findings = append(filtered.Findings, finding)
			}
		}
	}
	return &filtered
}

func containsFold(filter string, values ...string) bool {
	needle := strings.ToLower(strings.TrimSpace(filter))
	if needle == "" {
		return true
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), needle) {
			return true
		}
	}
	return false
}

func (m Model) filterCounts() (int, int) {
	if m.snapshot == nil {
		return 0, 0
	}
	filtered := m.filteredContext()
	switch tabs[m.tab] {
	case "Queries":
		return len(filtered.Queries), len(m.snapshot.Queries)
	case "Tables":
		return len(filtered.Tables) + len(filtered.Indexes), len(m.snapshot.Tables) + len(m.snapshot.Indexes)
	case "Connections":
		return len(filtered.Processes) + len(renderableConnectionGroups(filtered.ConnectionGroups)) + len(filtered.Locks) + len(filtered.Transactions) + len(filtered.MetadataLocks),
			len(m.snapshot.Processes) + len(renderableConnectionGroups(m.snapshot.ConnectionGroups)) + len(m.snapshot.Locks) + len(m.snapshot.Transactions) + len(m.snapshot.MetadataLocks)
	case "Findings":
		return len(filtered.Findings), len(m.snapshot.Findings)
	default:
		return 0, 0
	}
}

func (m Model) helpBindings() contextualHelp {
	if m.live.stage == "sending" {
		return contextualHelp{}
	}
	if m.live.stage != "" {
		bindings := []key.Binding{key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel / back"))}
		if m.live.stage == "choose" {
			bindings = append(bindings, m.keys.Up, m.keys.Down, m.keys.Open)
		} else if m.live.stage == "confirm" {
			bindings = append(bindings, key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "confirm exact kill")))
		}
		return contextualHelp{short: bindings, full: [][]key.Binding{bindings}}
	}
	paging := []key.Binding{m.keys.PageUp, m.keys.PageDown, m.keys.HalfPageUp, m.keys.HalfPageDown, m.keys.Top, m.keys.Bottom}
	navigation := []key.Binding{m.keys.PreviousView, m.keys.NextView, m.keys.Jump}
	actions := []key.Binding{m.keys.Blockers, m.keys.Refresh, m.keys.Export, m.keys.Help, m.keys.Quit}
	context := []key.Binding{m.keys.Up, m.keys.Down}
	short := []key.Binding{m.keys.PreviousView, m.keys.NextView, m.keys.Up, m.keys.Down}

	if m.inInvestigation() {
		context = []key.Binding{m.keys.Back, m.keys.Up, m.keys.Down}
		short = context
	} else if (tabs[m.tab] == "Queries" && !m.queryDetail) || tabs[m.tab] == "Findings" {
		context = []key.Binding{m.keys.Up, m.keys.Down, m.keys.Open, m.keys.Filter}
		short = []key.Binding{m.keys.Up, m.keys.Down, m.keys.Open, m.keys.Filter}
	} else if m.queryDetail {
		context = []key.Binding{m.keys.Back, m.keys.Up, m.keys.Down, m.keys.PageUp, m.keys.PageDown}
		short = []key.Binding{m.keys.Back, m.keys.Up, m.keys.Down, m.keys.PageUp, m.keys.PageDown}
	} else if tabs[m.tab] == "Overview" || tabs[m.tab] == "Connections" {
		context = append([]key.Binding{m.keys.Open, m.keys.Blockers}, context...)
		if tabs[m.tab] == "Connections" {
			context = append(context, m.keys.Filter)
		}
		short = context
	} else if m.filterable() {
		context = append(context, m.keys.Filter)
		short = append(short, m.keys.Filter)
	}
	if m.activeFilter() != "" && !m.queryDetail {
		context = append(context, m.keys.Back)
	}
	if tabs[m.tab] == "Queries" && !m.inInvestigation() {
		context = append(context, m.keys.KillQuery)
		short = append([]key.Binding{m.keys.KillQuery}, short...)
	}
	short = append(short, m.keys.Refresh, m.keys.Export, m.keys.Help, m.keys.Quit)

	groups := [][]key.Binding{context, navigation, paging, actions}
	probe := m.keyHelp
	probe.ShowAll = true
	probe.Width = 0
	available := max(20, m.viewport.Width-2)
	if lipgloss.Width(probe.View(contextualHelp{full: groups})) > available {
		flattened := make([]key.Binding, 0, len(context)+len(navigation)+len(paging)+len(actions))
		for _, group := range groups {
			flattened = append(flattened, group...)
		}
		groups = [][]key.Binding{flattened}
	}
	return contextualHelp{short: short, full: groups}
}

func (m Model) keyboardHelp() string {
	keyHelp := m.keyHelp
	keyHelp.ShowAll = true
	keyHelp.Width = max(20, m.viewport.Width-2)
	title := lipgloss.NewStyle().Foreground(cyan).Bold(true).Render("KEYBOARD HELP")
	context := lipgloss.NewStyle().Foreground(muted).Render(tabs[m.tab] + " · arrows work everywhere; Vim and pager keys are aliases")
	closeHint := lipgloss.NewStyle().Foreground(muted).Render("Esc or ? closes help. Help scrolls when the terminal is short.")
	return title + "\n" + context + "\n\n" + keyHelp.View(m.helpBindings()) + "\n\n" + closeHint
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
	if m.height < 10 || m.width < 40 {
		fit := func(value string) string {
			if m.width <= 1 {
				return "q"
			}
			return compact(value, m.width)
		}
		if m.height <= 1 {
			return fit("q quit")
		}
		if m.height == 2 {
			return fit("◆ MYSQ") + "\n" + fit("q quit")
		}
		return fit("◆ MYSQ") + "\n" + fit(fmt.Sprintf("Need 52×18 · current %d×%d", m.width, m.height)) + "\n" + fit("q quit")
	}
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
		state := m.snapshot.Health.State()
		if m.err != nil {
			state = "STALE"
		}
		color := healthStateColor(state)
		right = lipgloss.NewStyle().Foreground(color).Bold(true).Render(fmt.Sprintf("● %s  %d", state, m.snapshot.Health.Score))
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
		active := index == m.tab
		if active {
			label = fmt.Sprintf("● %d %s", index+1, strings.ToUpper(tabs[index]))
			if count := m.tabCount(index); count != "" {
				label += " (" + count + ")"
			}
		}
		if measureOnly {
			items = append(items, " "+label+" ")
			continue
		}
		style := lipgloss.NewStyle().Foreground(muted).Padding(0, 1)
		if active {
			style = style.Foreground(text).Reverse(true).Bold(true)
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
	if m.filtering {
		input := m.filterInput.View()
		hints := keyHint("enter", "apply") + "  " + keyHint("esc", "cancel")
		line := padBetween(input, hints, max(1, m.width-2))
		return lipgloss.NewStyle().Background(surfaceAlt).Padding(0, 1).Width(max(1, m.width)).Render(line)
	}
	status := m.status
	if status == "" {
		status = "Diagnostics · SQL literals redacted"
	}
	if m.help {
		keys := keyHint("esc/?", "close help") + "  " + keyHint("↑/↓", "scroll") + "  " + keyHint("q", "quit")
		line := padBetween(lipgloss.NewStyle().Foreground(muted).Render("Contextual keys · "+tabs[m.tab]), keys, max(1, m.width-2))
		return lipgloss.NewStyle().Background(surfaceAlt).Padding(0, 1).Width(max(1, m.width)).Render(line)
	}
	if m.activeFilter() != "" && !m.statusOverridesFilter {
		matched, total := m.filterCounts()
		status = fmt.Sprintf("Filter %q · %d/%d", m.activeFilter(), matched, total)
	}
	keyHelp := m.keyHelp
	keyHelp.ShowAll = false
	statusWidth := min(max(16, m.width/3), max(16, m.width-28))
	keyHelp.Width = max(12, m.width-statusWidth-5)
	keys := lipgloss.NewStyle().Inline(true).MaxWidth(keyHelp.Width).Render(keyHelp.View(m.helpBindings()))
	line := padBetween(lipgloss.NewStyle().Foreground(muted).Render(compact(status, statusWidth)), keys, max(1, m.width-2))
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
	m.filterInput.Width = max(12, m.width-28)
}

func (m Model) contentPanel(width, height int) string {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(0, 1).Width(width - 2).Height(max(1, height-2)).Render(m.viewport.View())
}

func (m *Model) rebuild() {
	if m.live.stage != "" {
		m.viewport.SetContent(m.queryActionView())
		m.viewport.GotoTop()
		return
	}
	if m.help {
		m.viewport.SetContent(m.keyboardHelp())
		m.viewport.GotoTop()
		return
	}
	if m.snapshot == nil {
		if m.err != nil {
			m.viewport.SetContent(errorView(m.err))
		} else {
			m.viewport.SetContent("\n  " + m.spinner.View() + " Collecting server identity, counters, statements, tables, indexes, locks, and configuration…")
		}
		return
	}
	if m.inInvestigation() {
		content := blockerInvestigation(m.snapshot, m.viewport.Width)
		if !m.blockerDetail {
			if f := m.findingByID(m.findingDetailID); f != nil {
				content = findingInvestigation(m.snapshot, *f, m.viewport.Width)
			}
		}
		m.viewport.SetContent(content)
		m.viewport.SetYOffset(m.investigationOffset)
		return
	}
	visible := m.filteredContext()
	if m.activeFilter() != "" {
		matched, total := m.filterCounts()
		if total > 0 && matched == 0 {
			m.viewport.SetContent(empty(fmt.Sprintf("No %s match filter %q. Press Esc to clear it.", strings.ToLower(tabs[m.tab]), m.activeFilter())))
			m.viewport.GotoTop()
			return
		}
	}
	var content string
	switch tabs[m.tab] {
	case "Overview":
		content = overview(visible, m.viewport.Width, m.err != nil)
	case "Findings":
		m.findingIndex = min(max(0, m.findingIndex), max(0, len(visible.Findings)-1))
		content = findingList(visible, m.viewport.Width, m.findingIndex)
	case "Queries":
		totalLatency := totalQueryLatency(m.snapshot.Queries)
		if m.queryDetail {
			content = m.liveExecutionView() + "\n" + queryDetail(visible, m.viewport.Width, m.queryIndex, totalLatency)
		} else {
			content = queries(visible, m.viewport.Width, m.queryIndex, totalLatency)
		}
	case "Engine":
		content = engine(visible, m.viewport.Width)
	case "Tables":
		content = tablesView(visible, m.viewport.Width)
	case "Connections":
		content = connections(visible, m.viewport.Width)
	case "Config":
		content = config(visible, m.viewport.Width)
	}
	m.viewport.SetContent(content)
	m.viewport.SetYOffset(m.currentOffset())
	m.ensureFindingSelectionVisible()
}

func (m *Model) ensureQuerySelectionVisible() {
	if m.inInvestigation() {
		return
	}
	// The query header and underline occupy two lines. Keep the selected statement inside
	// the viewport as the engineer walks a long digest list.
	line := m.queryIndex + 2
	if line < m.viewport.YOffset {
		m.viewport.SetYOffset(line)
	} else if line >= m.viewport.YOffset+m.viewport.Height {
		m.viewport.SetYOffset(max(0, line-m.viewport.Height+1))
	}
	m.viewOffsets[m.tab] = m.viewport.YOffset
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

func overview(ctx *model.Context, width int, stale ...bool) string {
	state := ctx.Health.State()
	if len(stale) > 0 && stale[0] {
		state = "STALE · REFRESH FAILED"
	}
	coverage := fmt.Sprintf("%d unverified subsystems", ctx.Health.Unknown)
	if len(ctx.Health.Subsystems) == 0 {
		coverage = "coverage not recorded"
	}
	captured := "capture time unavailable"
	if !ctx.CollectedAt.IsZero() {
		captured = "Captured " + ctx.CollectedAt.Local().Format("15:04:05")
	}
	posture := lipgloss.NewStyle().Foreground(healthStateColor(state)).Bold(true).Render("● "+state) + "  ·  " + coverage
	result := sectionTitle("DATABASE POSTURE") + "\n" + posture + "\n" +
		lipgloss.NewStyle().Foreground(muted).Width(width).Render(fmt.Sprintf("%s · %.1fs status window · r refresh", captured, float64(ctx.IntervalMillis)/1000)) + "\n"
	body := "No actionable findings in the captured evidence."
	if ctx.Health.Unknown > 0 {
		body += " Some checks remain unverified; see Config."
	}
	if len(ctx.Findings) > 0 {
		f := ctx.Findings[0]
		body = lipgloss.NewStyle().Foreground(severityColor(f.Severity)).Bold(true).Width(max(16, width-4)).Render(f.Title) + "\n" +
			lipgloss.NewStyle().Width(max(16, width-4)).Render(f.Summary) + "\n" + keyHint("enter", "investigate") + "  " + keyHint("5", "all findings") + "  " + keyHint("B", "blocking chains")
	}
	result += "\n" + panelBox("PRIORITY SIGNAL", body, width) + "\n"
	metrics := fmt.Sprintf("%.1f qps · %d running · %d/%d connections · %.2f%% buffer hit", ctx.Metrics.QueriesPerSecond, ctx.Metrics.ThreadsRunning, ctx.Metrics.ConnectionsCurrent, ctx.Metrics.ConnectionsMax, ctx.Metrics.BufferPoolHitPercent)
	result += lipgloss.NewStyle().Foreground(cyan).Width(width).Render(metrics) + "\n\n" + mysqlInvestigationPanels(ctx, width)
	pressure := strings.Join([]string{
		gaugeLine("Connection slots", ctx.Metrics.ConnectionsUsedPercent, fmt.Sprintf("%.1f%%", ctx.Metrics.ConnectionsUsedPercent), colorForPercent(ctx.Metrics.ConnectionsUsedPercent, 75, 90), max(20, width-4)),
		gaugeLine("Redo checkpoint", ctx.Metrics.RedoCheckpointAgePct, fmt.Sprintf("%.1f%%", ctx.Metrics.RedoCheckpointAgePct), colorForPercent(ctx.Metrics.RedoCheckpointAgePct, 60, 80), max(20, width-4)),
		gaugeLine("Dirty pages", ctx.Metrics.BufferPoolDirtyPercent, fmt.Sprintf("%.1f%%", ctx.Metrics.BufferPoolDirtyPercent), colorForPercent(ctx.Metrics.BufferPoolDirtyPercent, 40, 75), max(20, width-4)),
		gaugeLine("Disk temp tables", ctx.Metrics.TempDiskTablePercent, fmt.Sprintf("%.1f%%", ctx.Metrics.TempDiskTablePercent), colorForPercent(ctx.Metrics.TempDiskTablePercent, 10, 25), max(20, width-4)),
	}, "\n")
	result += "\n" + panelBox("WORKLOAD PRESSURE", pressure, width)
	if conditional := overviewConditionalPanels(ctx, width); conditional != "" {
		result += "\n" + conditional
	}
	return result + "\n" + lipgloss.NewStyle().Foreground(muted).Render(fmt.Sprintf("%s %s · uptime %s · server-wide activity, visible catalog objects", ctx.Server.Flavor, ctx.Server.Version, humanDuration(ctx.Server.UptimeSeconds)))
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
		assessment := ctx.Health.Subsystem("replication")
		state := assessment.Status
		color := yellow
		if state == "ok" {
			color = green
		} else if state == "fail" {
			color = red
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

func queries(ctx *model.Context, width, selected int, totalLatency float64) string {
	if len(ctx.Queries) == 0 {
		return empty("No statement digests available. Check Performance Schema consumers and privileges.")
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
	out.WriteString("\n" + lipgloss.NewStyle().Foreground(muted).Render(fmt.Sprintf("Sorted by database time  ·  user is point-in-time  ·  selected share %.1f%%  ·  literals removed", queryShare(ctx.Queries, selected, totalLatency))))
	return out.String()
}

func queryShare(queries []model.Query, selected int, total float64) float64 {
	if selected < 0 || selected >= len(queries) || total <= 0 {
		return 0
	}
	return queries[selected].TotalLatencyMillis * 100 / total
}

func totalQueryLatency(queries []model.Query) float64 {
	var total float64
	for _, query := range queries {
		total += query.TotalLatencyMillis
	}
	return total
}

func queryDetail(ctx *model.Context, width, selected int, totalLatency float64) string {
	width = min(width, 108)
	if selected < 0 || selected >= len(ctx.Queries) {
		return empty("The selected query is no longer available. Press Esc to return to Queries.")
	}
	query := ctx.Queries[selected]
	users := "not active in this snapshot"
	if len(query.ActiveUsers) > 0 {
		users = strings.Join(query.ActiveUsers, ", ")
	}
	important := strings.Join([]string{
		labelValue("USER", users),
		labelValue("DATABASE", fallback(query.Schema, "all databases")),
		labelValue("DB TIME", fmt.Sprintf("%s (%.1f%%)", duration(query.TotalLatencyMillis), queryShare(ctx.Queries, selected, totalLatency))),
		labelValue("CALLS", humanCount(query.Calls)),
		labelValue("P95", duration(query.P95LatencyMillis)),
	}, "  ·  ")
	var out strings.Builder
	out.WriteString(panelBox(fmt.Sprintf("QUERY %d OF %d · SNAPSHOT", selected+1, len(ctx.Queries)),
		lipgloss.NewStyle().Width(max(20, width-4)).Render(important), width))
	out.WriteString("\n" + sectionTitle("NORMALIZED SQL") + "\n")
	out.WriteString(lipgloss.NewStyle().Width(max(20, width-2)).Render(highlightedSQL(query.Statement)) + "\n")

	evidence := [][2]string{
		{"AVG / P99 / MAX", duration(query.MeanLatencyMillis) + " / " + duration(query.P99LatencyMillis) + " / " + duration(query.MaxLatencyMillis)},
		{"ROWS EXAMINED", humanCount(query.RowsExamined)},
		{"ROWS SENT", humanCount(query.RowsSent)},
		{"ERRORS / WARNINGS", humanCount(query.Errors) + " / " + humanCount(query.Warnings)},
		{"NO INDEX CALLS", humanCount(query.NoIndexUsed)},
		{"FULL SCANS", humanCount(query.FullScans)},
		{"TEMP TABLES", humanCount(query.TmpTables)},
		{"TEMP ON DISK", humanCount(query.TmpDiskTables)},
	}
	out.WriteString("\n" + sectionTitle("EXECUTION EVIDENCE") + "\n")
	columns := 1
	if width >= 100 {
		columns = 2
	}
	cellWidth := (width - 2) / columns
	for i, item := range evidence {
		valueColor := lipgloss.TerminalColor(number)
		if item[1] == "0" || item[1] == "0 / 0" {
			valueColor = text
		}
		if i == 3 && (query.Errors > 0 || query.Warnings > 0) {
			valueColor = yellow
		}
		cell := lipgloss.NewStyle().Width(19).Render(item[0]) + lipgloss.NewStyle().Foreground(valueColor).Render(item[1])
		out.WriteString(lipgloss.NewStyle().Width(cellWidth).Render(cell))
		if (i+1)%columns == 0 {
			out.WriteByte('\n')
		}
	}
	if query.FirstSeen != "" || query.LastSeen != "" {
		out.WriteString("\n" + lipgloss.NewStyle().Width(max(20, width-2)).Render(labelValue("OBSERVED", fallback(query.FirstSeen, "—")+" → "+fallback(query.LastSeen, "—"))) + "\n")
	}
	if query.Digest != "" {
		out.WriteString("\n" + lipgloss.NewStyle().Foreground(muted).Width(max(20, width-2)).Render("DIGEST  "+query.Digest))
	}
	return out.String()
}

func labelValue(label, value string) string {
	var color lipgloss.TerminalColor = text
	switch strings.ToUpper(label) {
	case "USER", "DATABASE", "HOST", "SCHEMA":
		color = identity
	default:
		if len(value) > 0 && value[0] >= '0' && value[0] <= '9' {
			color = number
		}
	}
	return lipgloss.NewStyle().Foreground(muted).Render(label+" ") +
		lipgloss.NewStyle().Foreground(color).Render(value)
}

func engine(ctx *model.Context, width int) string {
	var out strings.Builder
	compactLayout := width < 90
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
	metricHeadings := []string{"SIGNAL", "VALUE", "RELATED"}
	if compactLayout {
		metricWidths = []int{max(22, width-18), 18}
		metricHeadings = []string{"SIGNAL", "VALUE"}
		for index := range metricRows {
			metricRows[index] = metricRows[index][:2]
		}
	}
	out.WriteString(rows(metricRows, metricHeadings, metricWidths) + "\n")

	if len(ctx.WaitEvents) > 0 {
		out.WriteString(sectionTitle("SAMPLED WAIT PRESSURE") + "\n")
		waitWidths := []int{8, 12, 10, 12, max(28, width-42)}
		waitHeadings := []string{"SHARE", "WAIT/S", "EVENTS/S", "CUM TOTAL", "EVENT"}
		if compactLayout {
			waitWidths = []int{max(22, width-18), 8, 10}
			waitHeadings = []string{"EVENT", "SHARE", "WAIT/S"}
		}
		out.WriteString(row(waitHeadings, waitWidths, true) + "\n")
		for _, wait := range ctx.WaitEvents {
			identityWidth := waitWidths[len(waitWidths)-1]
			values := []string{fmt.Sprintf("%.1f%%", wait.SampleSharePercent), duration(wait.WaitMillisPerSecond) + "/s",
				fmt.Sprintf("%.1f", wait.EventsPerSecond), duration(wait.TotalLatencyMillis), wait.Name}
			if compactLayout {
				identityWidth = waitWidths[0]
				values = []string{compactMiddle(wait.Name, waitWidths[0]-1), fmt.Sprintf("%.1f%%", wait.SampleSharePercent), duration(wait.WaitMillisPerSecond) + "/s"}
			}
			out.WriteString(row(values, waitWidths, false) + "\n")
			out.WriteString(identityContinuation(wait.Name, identityWidth, width))
		}
	}

	if len(ctx.FileIO) > 0 {
		out.WriteString("\n" + sectionTitle("MYSQL FILE I/O") + "\n")
		ioWidths := []int{10, 10, 12, 12, max(28, width-44)}
		ioHeadings := []string{"READ/S", "WRITE/S", "READ LAT", "WRITE LAT", "FILE INSTRUMENT"}
		if compactLayout {
			ioWidths = []int{max(22, width-18), 9, 9}
			ioHeadings = []string{"FILE INSTRUMENT", "READ/S", "WRITE/S"}
		}
		out.WriteString(row(ioHeadings, ioWidths, true) + "\n")
		for _, item := range ctx.FileIO[:min(12, len(ctx.FileIO))] {
			identityWidth := ioWidths[len(ioWidths)-1]
			values := []string{fmt.Sprintf("%.1f", item.ReadsPerSecond), fmt.Sprintf("%.1f", item.WritesPerSecond),
				duration(item.MeanReadLatencyMillis), duration(item.MeanWriteLatencyMillis), item.Name}
			if compactLayout {
				identityWidth = ioWidths[0]
				values = []string{compactPath(item.Name, ioWidths[0]-1), fmt.Sprintf("%.1f", item.ReadsPerSecond), fmt.Sprintf("%.1f", item.WritesPerSecond)}
			}
			out.WriteString(row(values, ioWidths, false) + "\n")
			out.WriteString(identityContinuation(item.Name, identityWidth, width))
		}
	}

	if len(ctx.ServerErrors) > 0 {
		out.WriteString("\n" + sectionTitle("MYSQL ERRORS AND WARNINGS") + "\n")
		errorWidths := []int{9, 10, 10, 20, max(28, width-49)}
		errorHeadings := []string{"ERROR", "SAMPLE/S", "TOTAL", "LAST SEEN", "NAME"}
		if compactLayout {
			errorWidths = []int{max(22, width-18), 8, 10}
			errorHeadings = []string{"NAME", "ERROR", "SAMPLE/S"}
		}
		out.WriteString(row(errorHeadings, errorWidths, true) + "\n")
		for _, item := range ctx.ServerErrors[:min(10, len(ctx.ServerErrors))] {
			identityWidth := errorWidths[len(errorWidths)-1]
			values := []string{fmt.Sprint(item.Number), fmt.Sprintf("%.2f", item.RaisedPerSecond), humanCount(item.Raised), item.LastSeen, item.Name}
			if compactLayout {
				identityWidth = errorWidths[0]
				values = []string{compactMiddle(item.Name, errorWidths[0]-1), fmt.Sprint(item.Number), fmt.Sprintf("%.2f", item.RaisedPerSecond)}
			}
			out.WriteString(row(values, errorWidths, false) + "\n")
			out.WriteString(identityContinuation(item.Name, identityWidth, width))
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
		memoryHeadings := []string{"CURRENT", "HIGH WATER", "ALLOCATIONS", "CONSUMER"}
		if compactLayout {
			memoryWidths = []int{max(20, width-24), 12, 12}
			memoryHeadings = []string{"CONSUMER", "CURRENT", "HIGH WATER"}
		}
		out.WriteString(row(memoryHeadings, memoryWidths, true) + "\n")
		for _, consumer := range ctx.MemoryConsumers {
			identityWidth := memoryWidths[len(memoryWidths)-1]
			values := []string{humanBytes(consumer.CurrentBytes), humanBytes(consumer.HighBytes), humanCount(consumer.Allocations), consumer.Name}
			if compactLayout {
				identityWidth = memoryWidths[0]
				values = []string{compactMiddle(consumer.Name, memoryWidths[0]-1), humanBytes(consumer.CurrentBytes), humanBytes(consumer.HighBytes)}
			}
			out.WriteString(row(values, memoryWidths, false) + "\n")
			out.WriteString(identityContinuation(consumer.Name, identityWidth, width))
		}
	}
	out.WriteString("\n" + lipgloss.NewStyle().Foreground(muted).Width(width).Render("Wait, file I/O, and error rates use their per-family sample windows; cumulative totals are retained for forensic context."))
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
	if len(ctx.Tables) == 0 && len(ctx.Indexes) == 0 {
		return empty("No application tables are visible to the monitoring user.")
	}
	var out strings.Builder
	wide := width >= 110
	compactLayout := width < 80
	if len(ctx.Tables) > 0 {
		widths := []int{12, 11, 11, 11, 5, max(20, width-50)}
		headings := []string{"SIZE", "ROWS", "READS", "WRITES", "PK", "TABLE"}
		if compactLayout {
			widths = []int{max(18, width-22), 8, 9, 5}
			headings = []string{"TABLE", "SIZE", "ROWS", "PK"}
		} else if wide {
			widths = []int{11, 10, 9, 11, 9, 11, 5, max(22, width-66)}
			headings = []string{"SIZE", "ROWS", "READS", "READ TIME", "WRITES", "WRITE TIME", "PK", "TABLE"}
		}
		out.WriteString(row(headings, widths, true) + "\n")
		for _, table := range ctx.Tables {
			identityWidth := widths[len(widths)-1]
			pk := "yes"
			if !table.HasPrimaryKey {
				pk = "NO"
			}
			values := []string{humanBytes(table.TotalBytes), humanCount(table.EstimatedRows), humanCount(table.Reads), humanCount(table.Writes), pk, table.Schema + "." + table.Name}
			if compactLayout {
				identityWidth = widths[0]
				values = []string{compactMiddle(table.Schema+"."+table.Name, widths[0]-1), humanBytes(table.TotalBytes), humanCount(table.EstimatedRows), pk}
			} else if wide {
				values = []string{humanBytes(table.TotalBytes), humanCount(table.EstimatedRows), humanCount(table.Reads), duration(table.ReadLatencyMillis),
					humanCount(table.Writes), duration(table.WriteLatencyMillis), pk, table.Schema + "." + table.Name}
			}
			out.WriteString(row(values, widths, false) + "\n")
			out.WriteString(identityContinuation(table.Schema+"."+table.Name, identityWidth, width))
		}
	}
	if len(ctx.Indexes) > 0 {
		if out.Len() > 0 {
			out.WriteString("\n")
		}
		out.WriteString(sectionTitle("INDEX ACTIVITY") + "\n")
		indexWidths := []int{10, 10, 11, 10, max(24, width-41)}
		indexHeadings := []string{"READS", "WRITES", "CARDINALITY", "FLAGS", "INDEX AND COLUMNS"}
		if compactLayout {
			indexWidths = []int{max(18, width-26), 8, 8, 10}
			indexHeadings = []string{"INDEX AND COLUMNS", "READS", "WRITES", "FLAGS"}
		}
		out.WriteString(row(indexHeadings, indexWidths, true) + "\n")
		for _, index := range ctx.Indexes {
			identityWidth := indexWidths[len(indexWidths)-1]
			flags := ""
			if index.Unique {
				flags += "unique "
			}
			if !index.Visible {
				flags += "hidden"
			}
			identity := index.Schema + "." + index.Table + "." + index.Name + " (" + index.Columns + ")"
			values := []string{humanCount(index.Reads), humanCount(index.Writes), humanCount(index.Cardinality), strings.TrimSpace(flags), identity}
			if compactLayout {
				identityWidth = indexWidths[0]
				compactIdentity := index.Schema + "." + index.Table + "." + index.Name
				values = []string{compactMiddle(compactIdentity, indexWidths[0]-1), humanCount(index.Reads), humanCount(index.Writes), strings.TrimSpace(flags)}
			}
			out.WriteString(row(values, indexWidths, false) + "\n")
			out.WriteString(identityContinuation(identity, identityWidth, width))
		}
	}
	out.WriteString("\n" + lipgloss.NewStyle().Foreground(muted).Width(width).Render("Rows are InnoDB estimates. I/O counters are since Performance Schema reset."))
	return out.String()
}

func connections(ctx *model.Context, width int) string {
	var out strings.Builder
	compactLayout := width < 82
	groups := renderableConnectionGroups(ctx.ConnectionGroups)
	if len(groups) > 0 {
		out.WriteString(sectionTitle("CONNECTION BREAKDOWN") + "\n")
		groupWidths := []int{10, max(18, width-42), 8, 8, 8, 8}
		groupHeadings := []string{"GROUP", "VALUE", "TOTAL", "ACTIVE", "SLEEP", "OTHER"}
		if compactLayout {
			groupWidths = []int{10, max(18, width-26), 8, 8}
			groupHeadings = []string{"GROUP", "VALUE", "TOTAL", "ACTIVE"}
		}
		out.WriteString(row(groupHeadings, groupWidths, true) + "\n")
		for _, group := range groups {
			values := []string{group.Kind, group.Key, fmt.Sprint(group.Total), fmt.Sprint(group.Active), fmt.Sprint(group.Sleeping), fmt.Sprint(group.Other)}
			if compactLayout {
				values = []string{group.Kind, compactMiddle(group.Key, groupWidths[1]-1), fmt.Sprint(group.Total), fmt.Sprint(group.Active)}
			}
			out.WriteString(row(values, groupWidths, false) + "\n")
			out.WriteString(identityContinuation(group.Key, groupWidths[1], width))
		}
		out.WriteString("\n" + sectionTitle("PROCESS SNAPSHOT") + "\n")
	}
	if compactLayout {
		processWidths := []int{7, max(16, width-32), 9, 7, 9}
		out.WriteString("\n" + row([]string{"ID", "STATEMENT", "USER", "TIME", "WAIT"}, processWidths, true) + "\n")
		for _, process := range ctx.Processes {
			out.WriteString(row([]string{compactMiddle(fmt.Sprint(process.ID), processWidths[0]-1), compactMiddle(process.Statement, processWidths[1]-1), process.User, fmt.Sprintf("%ds", process.Seconds), processActivity(process)}, processWidths, false) + "\n")
			out.WriteString(processContinuation(process, processWidths[0], processWidths[1], width))
		}
	} else if width < 103 {
		processWidths := []int{8, 12, 8, 18, max(22, width-46)}
		out.WriteString("\n" + row([]string{"ID", "USER", "TIME", "WAIT", "STATEMENT"}, processWidths, true) + "\n")
		for _, process := range ctx.Processes {
			out.WriteString(row([]string{fmt.Sprint(process.ID), process.User, fmt.Sprintf("%ds", process.Seconds), processActivity(process), process.Statement}, processWidths, false) + "\n")
			out.WriteString(processContinuation(process, processWidths[0], processWidths[len(processWidths)-1], width))
		}
	} else {
		processWidths := []int{8, 13, 18, 8, 28, max(28, width-75)}
		out.WriteString("\n" + row([]string{"ID", "USER", "HOST", "TIME", "WAIT", "STATEMENT"}, processWidths, true) + "\n")
		for _, process := range ctx.Processes {
			out.WriteString(row([]string{fmt.Sprint(process.ID), process.User, process.Host, fmt.Sprintf("%ds", process.Seconds), processActivity(process), process.Statement}, processWidths, false) + "\n")
			out.WriteString(processContinuation(process, processWidths[0], processWidths[len(processWidths)-1], width))
		}
	}
	if len(ctx.Processes) == 0 {
		out.WriteString("\nNo other connections are visible.")
	}
	if len(ctx.Locks) > 0 {
		out.WriteString("\n\n" + sectionTitle("ROW LOCK WAITS") + "\n")
		for _, lock := range ctx.Locks {
			line := fmt.Sprintf("%s waits for %s on %s.%s index %s (%s %s)", lock.WaitingTransaction, lock.BlockingTransaction, lock.Schema, lock.Table, lock.Index, lock.LockType, lock.LockMode)
			out.WriteString(lipgloss.NewStyle().Width(width).Render(line) + "\n")
		}
	}
	if len(ctx.Transactions) > 0 {
		out.WriteString("\n\n" + sectionTitle("ACTIVE TRANSACTIONS") + "\n")
		transactionWidths := []int{11, 12, 8, 9, 10, max(28, width-50)}
		transactionHeadings := []string{"TRX", "USER", "AGE", "LOCKED", "MODIFIED", "STATEMENT"}
		if compactLayout {
			transactionWidths = []int{max(18, width-28), 10, 10, 8}
			transactionHeadings = []string{"STATEMENT", "TRX", "USER", "AGE"}
		}
		out.WriteString(row(transactionHeadings, transactionWidths, true) + "\n")
		for _, transaction := range ctx.Transactions {
			identityWidth := transactionWidths[len(transactionWidths)-1]
			values := []string{transaction.ID, transaction.User, fmt.Sprintf("%ds", transaction.AgeSeconds), humanCount(transaction.RowsLocked), humanCount(transaction.RowsModified), transaction.Statement}
			if compactLayout {
				identityWidth = transactionWidths[0]
				values = []string{compactMiddle(transaction.Statement, transactionWidths[0]-1), transaction.ID, transaction.User, fmt.Sprintf("%ds", transaction.AgeSeconds)}
			}
			out.WriteString(row(values, transactionWidths, false) + "\n")
			out.WriteString(identityContinuation(transaction.Statement, identityWidth, width))
		}
	}
	if len(ctx.MetadataLocks) > 0 {
		out.WriteString("\n\n" + sectionTitle("METADATA LOCKS") + "\n")
		metadataWidths := []int{9, 12, 12, 13, 12, max(24, width-58)}
		metadataHeadings := []string{"STATUS", "USER", "TYPE", "DURATION", "OBJECT TYPE", "OBJECT"}
		if compactLayout {
			metadataWidths = []int{max(18, width-30), 10, 10, 10}
			metadataHeadings = []string{"OBJECT", "STATUS", "USER", "TYPE"}
		}
		out.WriteString(row(metadataHeadings, metadataWidths, true) + "\n")
		for _, lock := range ctx.MetadataLocks {
			object := strings.TrimPrefix(lock.Schema+"."+lock.Object, ".")
			identityWidth := metadataWidths[len(metadataWidths)-1]
			values := []string{lock.Status, lock.User, lock.LockType, lock.Duration, lock.ObjectType, object}
			if compactLayout {
				identityWidth = metadataWidths[0]
				values = []string{compactMiddle(object, metadataWidths[0]-1), lock.Status, lock.User, lock.LockType}
			}
			out.WriteString(row(values, metadataWidths, false) + "\n")
			out.WriteString(identityContinuation(object, identityWidth, width))
		}
	}
	return out.String()
}

func renderableConnectionGroups(groups []model.ConnectionGroup) []model.ConnectionGroup {
	result := make([]model.ConnectionGroup, 0, min(12, len(groups)))
	for _, group := range groups {
		if group.Kind == "user_host" {
			continue
		}
		result = append(result, group)
		if len(result) == 12 {
			break
		}
	}
	return result
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
		style = style.Bold(true)
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
		style = style.Reverse(true).Bold(true)
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
	return lipgloss.NewStyle().Foreground(cyan).Render("▌ ") + lipgloss.NewStyle().Foreground(text).Bold(true).Render(value)
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
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "…")
}

func compactMiddle(value string, width int) string {
	if width <= 0 {
		return ""
	}
	totalWidth := ansi.StringWidth(value)
	if totalWidth <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	left := (width - 1) / 2
	right := width - 1 - left
	return ansi.Cut(value, 0, left) + "…" + ansi.Cut(value, totalWidth-right, totalWidth)
}

func compactPath(value string, width int) string {
	if ansi.StringWidth(value) <= width {
		return value
	}
	base := filepath.Base(value)
	baseWidth := ansi.StringWidth(base)
	if baseWidth+2 >= width {
		return compactMiddle(value, width)
	}
	directory := strings.TrimSuffix(value, base)
	return compactMiddle(directory, width-baseWidth) + base
}

func identityContinuation(value string, cellWidth, rowWidth int) string {
	if ansi.StringWidth(value) <= max(1, cellWidth-1) {
		return ""
	}
	return lipgloss.NewStyle().Foreground(muted).Width(rowWidth).Render("↳ "+value) + "\n"
}

func processContinuation(process model.Process, idWidth, statementWidth, rowWidth int) string {
	id := fmt.Sprint(process.ID)
	if ansi.StringWidth(id) <= max(1, idWidth-1) && ansi.StringWidth(process.Statement) <= max(1, statementWidth-1) {
		return ""
	}
	value := fmt.Sprintf("↳ ID %s\n  %s", id, process.Statement)
	return lipgloss.NewStyle().Foreground(muted).Width(rowWidth).Render(value) + "\n"
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
