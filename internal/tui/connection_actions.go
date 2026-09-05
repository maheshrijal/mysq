package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/maheshrijal/mysq/internal/control"
	"github.com/maheshrijal/mysq/internal/model"
	"github.com/maheshrijal/mysq/internal/sanitize"
)

type ConnectionController interface {
	Connection(context.Context, string, model.Process) (control.Connection, error)
	KillConnection(context.Context, control.Connection, string) error
}

type connectionMessage struct {
	request uint64
	target  control.Connection
	err     error
}

func (m Model) filteredConnections() []model.Process {
	if m.snapshot == nil {
		return nil
	}
	var items []model.Process
	for _, p := range m.snapshot.Processes {
		if m.filters[1] == "" || containsFold(m.filters[1], fmt.Sprint(p.ID), p.User, p.Host, p.Database, p.Command, p.State, p.Digest, p.WaitEvent, p.Statement) {
			items = append(items, p)
		}
	}
	return items
}

func (m Model) selectedConnection() (model.Process, bool) {
	items := m.filteredConnections()
	if m.connectionIndex < 0 || m.connectionIndex >= len(items) {
		return model.Process{}, false
	}
	return items[m.connectionIndex], true
}

func (m *Model) restoreConnectionSelection(p model.Process) {
	for i, item := range m.filteredConnections() {
		if p.ID != 0 && item.ID == p.ID && item.ThreadID == p.ThreadID && item.User == p.User && item.Host == p.Host {
			m.connectionIndex = i
			return
		}
	}
	m.connectionIndex = min(max(0, m.connectionIndex), max(0, len(m.filteredConnections())-1))
	m.connectionDetail = false
}

func (m *Model) loadConnection() tea.Cmd {
	p, ok := m.selectedConnection()
	if !ok {
		return nil
	}
	controller, ok := m.queryControl.(ConnectionController)
	if !ok {
		m.setStatus("Live connection control is unavailable", true)
		return nil
	}
	m.saveCurrentOffset()
	m.live.request++
	m.live.connection = true
	m.live.stage = "lookup"
	m.live.err, m.live.result = nil, ""
	m.live.connectionTarget = control.Connection{}
	request, ctx, uuid := m.live.request, m.ctx, m.snapshot.Server.UUID
	m.rebuild()
	return func() tea.Msg {
		target, err := controller.Connection(ctx, uuid, p)
		return connectionMessage{request: request, target: target, err: err}
	}
}

func (m Model) receiveConnection(msg connectionMessage) (tea.Model, tea.Cmd) {
	if msg.request != m.live.request || !m.live.connection || m.live.stage != "lookup" || tabs[m.tab] != "Connections" {
		return m, nil
	}
	if msg.err != nil {
		m.live.stage, m.live.err = "result", msg.err
		m.live.result = "Nothing was sent: " + sanitize.Text(msg.err.Error())
		m.rebuild()
		return m, nil
	}
	m.live.connectionTarget = msg.target
	return m, m.beginKillConfirmation()
}

func (m *Model) scrollConnections(msg tea.KeyMsg) bool {
	if tabs[m.tab] != "Connections" || m.connectionDetail || m.inInvestigation() {
		return false
	}
	count := len(m.filteredConnections())
	if count == 0 {
		return false
	}
	target, page := m.connectionIndex, max(1, m.viewport.Height-3)
	switch {
	case key.Matches(msg, m.keys.Up):
		target--
	case key.Matches(msg, m.keys.Down):
		target++
	case key.Matches(msg, m.keys.PageUp):
		target -= page
	case key.Matches(msg, m.keys.PageDown):
		target += page
	case key.Matches(msg, m.keys.HalfPageUp):
		target -= max(1, page/2)
	case key.Matches(msg, m.keys.HalfPageDown):
		target += max(1, page/2)
	case key.Matches(msg, m.keys.Top):
		target = 0
	case key.Matches(msg, m.keys.Bottom):
		target = count - 1
	default:
		return false
	}
	// At either end, continue scrolling the surrounding breakdown and lock evidence.
	if (target < 0 && m.connectionIndex == 0) || (target >= count && m.connectionIndex == count-1) {
		return m.scroll(msg, false)
	}
	m.connectionIndex = min(max(0, target), count-1)
	m.rebuild()
	return true
}

func (m *Model) ensureConnectionSelectionVisible(lines []int) {
	if m.connectionIndex < 0 || m.connectionIndex >= len(lines) {
		return
	}
	line := lines[m.connectionIndex]
	if line < m.viewport.YOffset {
		m.viewport.SetYOffset(line)
	} else if line >= m.viewport.YOffset+m.viewport.Height {
		m.viewport.SetYOffset(max(0, line-m.viewport.Height+1))
	}
	m.viewOffsets[m.tab] = m.viewport.YOffset
}

func (m Model) connectionDetailView() string {
	p, ok := m.selectedConnection()
	if !ok {
		return empty("No connection selected.")
	}
	width := max(42, m.viewport.Width-2)
	return sectionTitle(fmt.Sprintf("CONNECTION %d OF %d", m.connectionIndex+1, len(m.filteredConnections()))) + "\n\n" +
		lipgloss.NewStyle().Width(width).Render(connectionDescription(p)) + "\n\n" +
		quiet("SQL · snapshot, literals redacted") + "\n" + lipgloss.NewStyle().Width(width).Render(highlightedSQL(fallback(p.Statement, "(no current statement)"))) +
		"\n\n" + keyHint("K", "kill connection…") + "   " + keyHint("B", "blocking chains") + "   " + keyHint("Esc", "back")
}

func connectionDescription(p model.Process) string {
	return fmt.Sprintf("Connection %d · thread %d\n", p.ID, p.ThreadID) +
		labelValue("User", sanitize.Text(p.User)) + "   " + labelValue("Host", sanitize.Text(p.Host)) + "\n" +
		labelValue("Database", fallback(sanitize.Text(p.Database), "(none)")) + "   " + labelValue("State", fallback(sanitize.Text(p.State), sanitize.Text(p.Command))) + fmt.Sprintf(" · %ds", p.Seconds)
}

func (m Model) connectionActionView() string {
	width := max(42, min(104, m.viewport.Width-2))
	wrap := lipgloss.NewStyle().Width(width).Render
	gap := "\n\n"
	if m.viewport.Height < 17 {
		gap = "\n"
	}
	switch m.live.stage {
	case "lookup":
		return sectionTitle("Check live connection") + gap + quiet("Reading selected session…") + gap + keyHint("Esc", "cancel")
	case "sending":
		return sectionTitle("Sending termination…") + gap + wrap(fmt.Sprintf("Rechecking connection and sending KILL CONNECTION %d.", m.live.connectionTarget.ID))
	case "result":
		title, color := "✓ Connection termination accepted", green
		if m.live.err != nil {
			title, color = "! Connection termination not confirmed", yellow
		}
		return lipgloss.NewStyle().Foreground(color).Bold(true).Render(title) + gap + wrap(m.live.result) + gap + keyHint("Enter / Esc", "back to connections")
	}
	p := m.live.connectionTarget
	// Keep target, consequences and exact confirmation visible at 52×18.
	body := lipgloss.NewStyle().Foreground(red).Bold(true).Render(fmt.Sprintf("Kill connection %d?", p.ID)) + gap
	body += wrap(compact(sanitize.Text(p.User), width/3)+" @ "+compact(sanitize.Text(p.Host), width/2)) + "\n"
	body += wrap(labelValue("Database", compact(fallback(sanitize.Text(p.Database), "(none)"), width/3))+"   "+compact(fallback(sanitize.Text(p.State), sanitize.Text(p.Command)), width/3)) + "\n"
	body += quiet(compact("Server "+p.ServerUUID, width)) + gap
	body += lipgloss.NewStyle().Foreground(yellow).Width(width).Render("Closes the session; interrupts work and rolls back its open transaction. Cleanup may take time.") + "\n"
	body += wrap(quiet("Targets this connection even if its statement changes.")) + gap
	body += m.live.input.View() + "  " + lipgloss.NewStyle().Foreground(red).Bold(true).Render("Enter confirm") + "  " + keyHint("Esc", "cancel")
	if m.live.result != "" {
		body += "\n" + wrap(m.live.result)
	}
	return strings.TrimSpace(body)
}
