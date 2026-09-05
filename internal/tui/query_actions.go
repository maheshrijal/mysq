package tui

import (
	"context"
	"fmt"
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
			m.live.input.Width = 12
			m.live.input.CharLimit = 32
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

func executionSummary(e control.Execution) string {
	return fmt.Sprintf("Connection %d · user %s · host %s\nDatabase %s · %s · %ds · state %s\nThread %d · event %d · statement %s",
		e.ID, sanitize.Text(e.User), sanitize.Text(e.Host), fallback(sanitize.Text(e.Database), "(none)"),
		e.Command, e.Seconds, fallback(sanitize.Text(e.State), "(none)"), e.ThreadID, e.EventID, duration(e.StatementLatencyMillis))
}

func (m Model) liveExecutionView() string {
	if m.queryControl == nil {
		return ""
	}
	var out strings.Builder
	out.WriteString(sectionTitle("CURRENT EXECUTIONS") + "\n")
	if m.live.loading {
		return out.String() + "Reading live sessions…\n"
	}
	if m.live.err != nil {
		return out.String() + "Unavailable: " + sanitize.Text(m.live.err.Error()) + "\n"
	}
	if m.live.identity != m.selectedQueryIdentity() {
		return out.String() + "Press r to load executions for this query.\n"
	}
	if len(m.live.items) == 0 {
		return out.String() + "No matching instrumented execution visible now.\nHistorical digests can outlive their sessions.\n"
	}
	out.WriteString(fmt.Sprintf("Checked %s · %d visible (limit 100) · K select a query to kill\n", m.live.at.Format("15:04:05"), len(m.live.items)))
	for _, e := range m.live.items {
		out.WriteString(executionSummary(e) + "\n\n")
	}
	return lipgloss.NewStyle().Width(max(20, m.viewport.Width-2)).Render(out.String())
}

func (m Model) queryActionView() string {
	width := max(20, m.viewport.Width-2)
	wrap := lipgloss.NewStyle().Width(width).Render
	if m.live.stage == "choose" {
		heading := sectionTitle("SELECT ONE EXECUTION TO KILL") + "\n"
		if m.live.loading {
			return heading + "Reading live sessions…\nEsc cancel"
		}
		if m.live.err != nil {
			return heading + wrap("Unavailable: "+sanitize.Text(m.live.err.Error())) + "\nEsc cancel"
		}
		if len(m.live.items) == 0 {
			return heading + wrap("No matching instrumented execution visible now. Historical digests cannot be killed. Esc to return.")
		}
		e := m.live.items[m.live.index]
		return heading + fmt.Sprintf("Execution %d of %d visible (limit 100) · ↑/↓ select\n\n", m.live.index+1, len(m.live.items)) +
			wrap(executionSummary(e)) + "\n\n" + wrap(compact(e.Statement, width*2)) + "\n\nEnter review cancellation · Esc cancel"
	}
	if m.live.stage == "sending" {
		return sectionTitle("CANCELLATION IN PROGRESS") + "\n" + wrap(fmt.Sprintf("Rechecking and sending KILL QUERY %d…", m.live.target.ID))
	}
	if m.live.stage == "result" {
		return sectionTitle("CANCELLATION RESULT") + "\n\n" + wrap(m.live.result) + "\n\nEnter / Esc return · snapshot refresh requested"
	}
	e := m.live.target
	// Keep confirmation and consequences visible even at the minimum 52×18.
	return sectionTitle(fmt.Sprintf("CONFIRM KILL QUERY %d", e.ID)) + "\n" +
		compact("Server "+e.ServerUUID, width) + "\n" +
		compact("User "+sanitize.Text(e.User)+" · host "+sanitize.Text(e.Host), width) + "\n" +
		compact("Database "+sanitize.Text(e.Database)+fmt.Sprintf(" · %ds · ", e.Seconds)+sanitize.Text(e.State), width) + "\n" +
		compact(e.Statement, width) + "\n" +
		wrap("Connection stays open; transaction locks may remain.") + "\n" +
		wrap("Rechecked before sending; a new statement can race the kill.") + "\n" +
		m.live.input.View() + "  Enter confirm · Esc cancel\n" + wrap(m.live.result)
}
