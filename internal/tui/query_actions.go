package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/maheshrijal/mysq/internal/control"
	"github.com/maheshrijal/mysq/internal/sanitize"
)

type QueryController interface {
	Sessions(context.Context, string, string) ([]control.Execution, error)
	Kill(context.Context, control.Execution, string) error
}

type liveQueries struct {
	request  uint64
	identity string
	items    []control.Execution
	index    int
	at       time.Time
	loading  bool
	err      error
	stage    string // choose, confirm, sending, result; empty outside the modal
	target   control.Execution
	input    textinput.Model
	result   string
}

type sessionsMessage struct {
	request uint64
	items   []control.Execution
	err     error
}

type killMessage struct{ err error }

func (m *Model) loadSessions(choose bool) tea.Cmd {
	queries := m.filteredQueries()
	if m.queryIndex < 0 || m.queryIndex >= len(queries) {
		return nil
	}
	if m.queryControl == nil {
		if choose {
			m.setStatus("Live query control is unavailable", true)
		}
		return nil
	}
	query := queries[m.queryIndex]
	m.live.request++
	m.live.identity = m.selectedQueryIdentity()
	m.live.items, m.live.err = nil, nil
	m.live.index, m.live.loading = 0, true
	if choose {
		m.saveCurrentOffset()
		m.live.stage = "choose"
		m.live.result = ""
	}
	m.rebuild()
	request, controller, ctx := m.live.request, m.queryControl, m.ctx
	return func() tea.Msg {
		items, err := controller.Sessions(ctx, query.Schema, query.Digest)
		return sessionsMessage{request: request, items: items, err: err}
	}
}

func (m Model) receiveSessions(msg sessionsMessage) (tea.Model, tea.Cmd) {
	if msg.request != m.live.request || tabs[m.tab] != "Queries" ||
		(!m.queryDetail && m.live.stage != "choose") || m.selectedQueryIdentity() != m.live.identity {
		return m, nil
	}
	m.live.loading, m.live.err = false, msg.err
	m.live.items, m.live.at = msg.items, time.Now()
	m.rebuild()
	return m, nil
}

func (m Model) handleQueryAction(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Once dispatched, Esc cannot imply that an in-flight request was undone.
	if m.live.stage == "sending" {
		return m, nil
	}
	if msg.String() == "esc" || (m.live.stage == "result" && msg.String() == "enter") {
		m.live.stage = ""
		m.live.input.Blur()
		m.live.request++ // discard any result from an abandoned session lookup
		m.rebuild()
		if m.queryDetail {
			return m, m.loadSessions(false)
		}
		return m, nil
	}
	switch m.live.stage {
	case "choose":
		switch msg.String() {
		case "up", "k":
			m.live.index = max(0, m.live.index-1)
		case "down", "j":
			m.live.index = min(max(0, len(m.live.items)-1), m.live.index+1)
		case "enter":
			if m.live.loading || m.live.err != nil || len(m.live.items) == 0 {
				return m, nil
			}
			m.live.target = m.live.items[m.live.index] // freeze this execution
			m.live.stage = "confirm"
			m.live.input = textinput.New()
			m.live.input.Prompt = "Type kill: "
			m.live.input.Width = 6
			m.live.input.CharLimit = 32
			m.live.input.PromptStyle = lipgloss.NewStyle().Foreground(red).Bold(true)
			m.live.input.TextStyle = lipgloss.NewStyle().Foreground(text).Bold(true)
			m.live.input.Cursor.Style = lipgloss.NewStyle().Foreground(red)
			cmd := m.live.input.Focus()
			m.rebuild()
			return m, cmd
		}
	case "confirm":
		if msg.String() == "enter" {
			if m.live.input.Value() != "kill" {
				m.live.result = "Type exactly kill (lowercase) to confirm."
				m.rebuild()
				return m, nil
			}
			m.live.stage, m.live.result = "sending", ""
			m.live.input.Blur()
			target, controller, ctx := m.live.target, m.queryControl, m.ctx
			m.rebuild()
			return m, func() tea.Msg { return killMessage{err: controller.Kill(ctx, target, "kill")} }
		}
		var cmd tea.Cmd
		m.live.input, cmd = m.live.input.Update(msg)
		m.live.result = ""
		m.rebuild()
		return m, cmd
	}
	m.rebuild()
	return m, nil
}

func (m Model) receiveKill(msg killMessage) (tea.Model, tea.Cmd) {
	if m.live.stage != "sending" {
		return m, nil
	}
	m.live.stage = "result"
	m.live.items = nil
	m.live.err = msg.err
	if msg.err != nil {
		m.live.result = "Cancellation not confirmed: " + sanitize.Text(msg.err.Error())
	} else {
		m.live.result = fmt.Sprintf("KILL QUERY %d accepted. MySQL interrupts asynchronously; the connection stays open and transaction locks may remain.", m.live.target.ID)
	}
	m.rebuild()
	if m.inspect != nil {
		m.loading = true
		return m, tea.Batch(m.inspectCommand(), m.spinner.Tick)
	}
	return m, nil
}

// Keep SQL structure distinct from identifiers and redacted values. This only
// styles already-normalized/redacted SQL; it never changes what is displayed.
var sqlTokens = regexp.MustCompile("`[^`]*`|[A-Za-z_]+|[?]")

func highlightedSQL(statement string) string {
	return sqlTokens.ReplaceAllStringFunc(statement, func(token string) string {
		if token == "?" {
			return lipgloss.NewStyle().Foreground(muted).Render(token)
		}
		switch strings.ToUpper(token) {
		case "SELECT", "FROM", "WHERE", "UPDATE", "SET", "INSERT", "INTO", "VALUES", "DELETE", "JOIN", "LEFT", "RIGHT", "INNER", "OUTER", "ON", "AND", "OR", "NOT", "IN", "IS", "NULL", "AS", "ORDER", "BY", "GROUP", "HAVING", "LIMIT", "OFFSET", "DISTINCT", "WITH", "UNION", "ALL", "CASE", "WHEN", "THEN", "ELSE", "END", "ASC", "DESC", "FOR", "LOCK", "SHARE":
			return lipgloss.NewStyle().Foreground(cyan).Render(token)
		}
		return lipgloss.NewStyle().Foreground(identity).Render(token)
	})
}

func quiet(value string) string { return lipgloss.NewStyle().Foreground(muted).Render(value) }

func executionState(e control.Execution) string {
	color := green
	if strings.Contains(strings.ToLower(e.State), "wait") || strings.Contains(strings.ToLower(e.State), "lock") {
		color = yellow
	}
	return lipgloss.NewStyle().Foreground(color).Render(fallback(sanitize.Text(e.State), e.Command))
}

func executionTable(items []control.Execution, selected, width int) string {
	// Keep the selected row in a compact window; the caller bounds its height.
	wide := width >= 76
	widths := []int{3, 12, 12, 8, width - 35}
	headings := []string{"", "CONNECTION", "USER", "ELAPSED", "STATE"}
	if wide {
		widths = []int{3, 12, 16, width - 61, 10, 20}
		headings = []string{"", "CONNECTION", "USER", "HOST", "ELAPSED", "STATE"}
	}
	var out strings.Builder
	for i, heading := range headings {
		out.WriteString(lipgloss.NewStyle().Foreground(muted).Width(widths[i]).Render(heading))
	}
	out.WriteByte('\n')
	for index, e := range items {
		var elapsedColor lipgloss.TerminalColor = number
		if e.Seconds >= 10 {
			elapsedColor = yellow
		}
		if e.Seconds >= 60 {
			elapsedColor = red
		}
		values := []string{"", fmt.Sprint(e.ID), sanitize.Text(e.User), fmt.Sprintf("%ds", e.Seconds), fallback(sanitize.Text(e.State), e.Command)}
		colors := []lipgloss.TerminalColor{cyan, identity, identity, elapsedColor, green}
		if wide {
			values = []string{"", fmt.Sprint(e.ID), sanitize.Text(e.User), sanitize.Text(e.Host), fmt.Sprintf("%ds", e.Seconds), fallback(sanitize.Text(e.State), e.Command)}
			colors = []lipgloss.TerminalColor{cyan, identity, identity, text, elapsedColor, green}
		}
		if strings.Contains(strings.ToLower(e.State), "wait") || strings.Contains(strings.ToLower(e.State), "lock") {
			colors[len(colors)-1] = yellow
		}
		if index == selected {
			values[0] = "›"
		}
		for i, value := range values {
			style := lipgloss.NewStyle().Foreground(colors[i]).Width(widths[i])
			if index == selected {
				style = style.Foreground(text).Reverse(true).Bold(i == 1 || i == 2)
			}
			out.WriteString(style.Render(compact(value, widths[i]-1)))
		}
		out.WriteByte('\n')
	}
	return strings.TrimSuffix(out.String(), "\n")
}

func (m Model) liveExecutionView() string {
	if m.queryControl == nil {
		return ""
	}
	width := max(42, min(104, m.viewport.Width-2))
	var out strings.Builder
	out.WriteString(sectionTitle("CURRENT EXECUTIONS") + "\n")
	if m.live.loading {
		return out.String() + quiet("Reading live sessions…") + "\n"
	}
	if m.live.err != nil {
		return out.String() + lipgloss.NewStyle().Foreground(yellow).Width(width).Render("Unavailable: "+sanitize.Text(m.live.err.Error())) + "\n"
	}
	if m.live.identity != m.selectedQueryIdentity() {
		return out.String() + quiet("Press r to load executions for this query.") + "\n"
	}
	if len(m.live.items) == 0 {
		return out.String() + quiet("No matching instrumented execution visible now.\nHistorical digests can outlive their sessions.") + "\n"
	}
	out.WriteString(quiet(fmt.Sprintf("Checked %s · %d visible", m.live.at.Format("15:04:05"), len(m.live.items))) + "\n\n")
	out.WriteString(executionTable(m.live.items, -1, width) + "\n")
	if width < 76 {
		for _, e := range m.live.items {
			out.WriteString(lipgloss.NewStyle().Width(width).Render(fmt.Sprintf("Connection %d · ", e.ID)+labelValue("Host", sanitize.Text(e.Host))) + "\n")
		}
	}
	out.WriteString("\n" + keyHint("K", "select an execution to cancel"))
	return out.String()
}

func (m Model) queryActionView() string {
	width := max(42, min(104, m.viewport.Width-2))
	wrap := lipgloss.NewStyle().Width(width).Render
	small := m.viewport.Height < 17
	gap := "\n\n"
	if small {
		gap = "\n"
	}
	if m.live.stage == "choose" {
		heading := padBetween(lipgloss.NewStyle().Foreground(text).Bold(true).Render("Choose an execution"), quiet(fallbackStep(small, "1")), width) + "\n"
		if m.live.loading {
			return heading + quiet("Reading live sessions…") + gap + keyHint("Esc", "cancel")
		}
		if m.live.err != nil {
			return heading + gap + lipgloss.NewStyle().Foreground(yellow).Width(width).Render("Unavailable: "+sanitize.Text(m.live.err.Error())) + gap + keyHint("Esc", "back")
		}
		if len(m.live.items) == 0 {
			return heading + gap + lipgloss.NewStyle().Foreground(yellow).Width(width).Render("No matching instrumented execution visible now.") + "\n" + wrap(quiet("This query is in history, but no live execution was identified.")) + gap + keyHint("Esc", "back to query")
		}
		e := m.live.items[m.live.index]
		count := fmt.Sprintf("%d of %d visible", m.live.index+1, len(m.live.items))
		instructions := "Only this execution will be cancelled."
		if len(m.live.items) > 1 {
			instructions = "↑/↓ choose · Only the selected execution will be cancelled."
		}
		limit := min(len(m.live.items), max(1, m.viewport.Height-14))
		start := min(max(0, m.live.index-limit/2), len(m.live.items)-limit)
		body := heading + quiet(compact(instructions, width)) + gap + executionTable(m.live.items[start:start+limit], m.live.index-start, width)
		body += "\n" + quiet(count+" · checked "+m.live.at.Format("15:04:05"))
		body += gap + labelValue("Database", fallback(sanitize.Text(e.Database), "(none)")) + "   " + labelValue("Host", compact(sanitize.Text(e.Host), max(12, width-30)))
		if !small {
			body += "\n" + quiet(fmt.Sprintf("Thread %d · event %d · statement %s", e.ThreadID, e.EventID, duration(e.StatementLatencyMillis)))
		}
		body += gap + quiet("SQL · literals redacted") + "\n" + wrap(highlightedSQL(compact(e.Statement, width*2)))
		body += gap + lipgloss.NewStyle().Foreground(cyan).Bold(true).Render("Enter  Review cancellation →") + "   " + keyHint("Esc", "back")
		return body
	}
	if m.live.stage == "sending" {
		return lipgloss.NewStyle().Foreground(yellow).Bold(true).Render("Sending cancellation…") + gap + wrap(quiet(fmt.Sprintf("Rechecking execution and sending KILL QUERY %d.", m.live.target.ID)))
	}
	if m.live.stage == "result" {
		color, title := green, "✓ Cancellation request accepted"
		if m.live.err != nil {
			color, title = yellow, "! Cancellation not confirmed"
		}
		return lipgloss.NewStyle().Foreground(color).Bold(true).Render(title) + gap + wrap(m.live.result) + gap + keyHint("Enter / Esc", "back to query") + "\n" + quiet("Diagnostic snapshot refresh requested.")
	}
	e := m.live.target
	// Compact variants retain the target, consequences, and input at 52×18.
	title := lipgloss.NewStyle().Foreground(red).Bold(true).Render(fmt.Sprintf("Cancel query on connection %d?", e.ID))
	body := padBetween(title, quiet(fallbackStep(small, "2")), width) + gap
	body += lipgloss.NewStyle().Foreground(text).Bold(true).Render(compact(sanitize.Text(e.User), width/3)) + quiet(" @ ") + quiet(compact(sanitize.Text(e.Host), width/2)) + "\n"
	body += labelValue("Database", compact(sanitize.Text(e.Database), width/3)) + "   " + lipgloss.NewStyle().Foreground(yellow).Bold(true).Render(fmt.Sprintf("%ds", e.Seconds)) + "   " + executionState(e) + "\n"
	body += quiet(compact("Server "+e.ServerUUID, width)) + gap
	body += wrap(highlightedSQL(compact(e.Statement, width))) + gap
	body += lipgloss.NewStyle().Foreground(yellow).Width(width).Render("Connection stays open; transaction locks may remain.") + "\n"
	body += wrap(quiet("Rechecked before sending; a new statement can race the kill.")) + gap
	// The input owns its ANSI styles. Lip Gloss's underline space styler splits
	// nested escape sequences into individual runes, exposing them as text.
	body += m.live.input.View() + "  " + lipgloss.NewStyle().Foreground(red).Bold(true).Render("Enter confirm") + "  " + keyHint("Esc", "cancel")
	if m.live.result != "" {
		body += "\n" + lipgloss.NewStyle().Foreground(red).Width(width).Render(m.live.result)
	}
	return body
}

func fallbackStep(small bool, step string) string {
	if small {
		return step + "/2"
	}
	return "STEP " + step + " / 2"
}
