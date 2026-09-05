package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/maheshrijal/mysq/internal/model"
)

func healthStateColor(state string) lipgloss.TerminalColor {
	switch state {
	case "HEALTHY":
		return green
	case "CRITICAL":
		return red
	default:
		return yellow
	}
}

func (m Model) inInvestigation() bool { return m.findingDetailID != "" || m.blockerDetail }

func (m Model) findingByID(id string) *model.Finding {
	if m.snapshot != nil {
		for i := range m.snapshot.Findings {
			if m.snapshot.Findings[i].ID == id {
				return &m.snapshot.Findings[i]
			}
		}
	}
	return nil
}

func (m Model) selectedFindingID() string {
	// Findings filtering belongs to its own view, independent of the active tab.
	if m.snapshot == nil {
		return ""
	}
	items := m.filteredFindings()
	if m.findingIndex < 0 || m.findingIndex >= len(items) {
		return ""
	}
	return items[m.findingIndex].ID
}

func (m Model) filteredFindings() []model.Finding {
	if m.snapshot == nil {
		return nil
	}
	result := []model.Finding{}
	for _, f := range m.snapshot.Findings {
		values := append([]string{f.ID, string(f.Severity), f.Subsystem, f.Title, f.Summary, f.Recommendation}, f.Objects...)
		if containsFold(m.filters[4], values...) {
			result = append(result, f)
		}
	}
	return result
}

func (m *Model) restoreFindingSelection(id string) {
	items := m.filteredFindings()
	for i, f := range items {
		if f.ID == id {
			m.findingIndex = i
			return
		}
	}
	m.findingIndex = min(max(0, m.findingIndex), max(0, len(items)-1))
}

func (m *Model) scrollFindingList(msg tea.KeyMsg) bool {
	if tabs[m.tab] != "Findings" || m.inInvestigation() {
		return false
	}
	target := m.findingIndex
	page := max(1, m.viewport.Height/3-1)
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
		target = len(m.filteredFindings()) - 1
	default:
		return false
	}
	m.findingIndex = min(max(0, target), max(0, len(m.filteredFindings())-1))
	m.rebuild()
	m.ensureFindingSelectionVisible()
	return true
}

func (m *Model) ensureFindingSelectionVisible() {
	if tabs[m.tab] != "Findings" || m.inInvestigation() || m.help {
		return
	}
	line := m.findingIndex*3 + 2
	if line < m.viewport.YOffset {
		m.viewport.SetYOffset(line)
	} else if line+1 >= m.viewport.YOffset+m.viewport.Height {
		m.viewport.SetYOffset(max(0, line+2-m.viewport.Height))
	}
	m.viewOffsets[m.tab] = m.viewport.YOffset
}

func findingList(ctx *model.Context, width, selected int) string {
	if len(ctx.Findings) == 0 {
		return empty("No findings in captured evidence. Check coverage before concluding the server is healthy.")
	}
	var out strings.Builder
	out.WriteString(sectionTitle("FINDINGS · ENTER TO INVESTIGATE") + "\n\n")
	for i, f := range ctx.Findings {
		marker := "  "
		if i == selected {
			marker = "› "
		}
		title := fmt.Sprintf("%s%s  %s", marker, strings.ToUpper(string(f.Severity)), f.Title)
		out.WriteString(lipgloss.NewStyle().Foreground(severityColor(f.Severity)).Bold(i == selected).Render(compact(title, width)) + "\n")
		out.WriteString(lipgloss.NewStyle().Foreground(muted).Render(compact("  "+f.Summary, width)) + "\n\n")
	}
	return out.String()
}

func findingInvestigation(ctx *model.Context, f model.Finding, width int) string {
	wrap := func(value string) string { return lipgloss.NewStyle().Width(width).Render(value) }
	result := sectionTitle("FINDING EVIDENCE") + "\n" + wrap(string(f.Severity)+" · "+f.ID) + "\n\n" +
		wrap(f.Title) + "\n" + wrap(f.Summary) + "\n\n" + sectionTitle("NEXT STEP") + "\n" + wrap(f.Recommendation) + "\n"
	if len(f.Objects) > 0 {
		result += "\n" + sectionTitle("OBJECTS") + "\n" + wrap(strings.Join(f.Objects, " · ")) + "\n"
	}
	if len(f.Evidence) > 0 {
		data, _ := json.MarshalIndent(f.Evidence, "", "  ")
		result += "\n" + sectionTitle("MEASURED EVIDENCE") + "\n" + wrap(string(data)) + "\n"
	}
	coverage := ctx.Health.Subsystem(f.Subsystem)
	if !coverage.Complete {
		result += "\n" + sectionTitle("COVERAGE") + "\n" + wrap(coverage.Reason) + "\n"
	}
	if f.Subsystem == "locks" {
		result += "\n" + blockerInvestigation(ctx, width)
	}
	if digest, ok := f.Evidence["digest"].(string); ok {
		schema, _ := f.Evidence["schema"].(string)
		for i, q := range ctx.Queries {
			if q.Digest == digest && q.Schema == schema {
				result += "\n" + queryDetail(ctx, width, i, totalQueryLatency(ctx.Queries))
				break
			}
		}
	}
	return result + "\n\n" + keyHint("esc", "back") + "  " + keyHint("e", "export this snapshot")
}

func blockerInvestigation(ctx *model.Context, width int) string {
	wrap := func(value string) string { return lipgloss.NewStyle().Width(width).Render(value) }
	result := sectionTitle("BLOCKING CHAINS") + "\n" + wrap("Captured row-lock edges; arrows point from blocker to waiter. No cancellation or database changes are performed.") + "\n"
	if len(ctx.BlockingChains) == 0 {
		result += "\n" + wrap("No row-lock chain captured. Absence is not proof of no contention; review metadata locks and probe coverage below.") + "\n"
	}
	for _, chain := range ctx.BlockingChains {
		result += "\n" + sectionTitle(fmt.Sprintf("ROOT TRANSACTION %s · %d DISTINCT WAITERS", chain.RootTransaction, chain.WaiterCount)) + "\n"
		for _, trx := range chain.Transactions {
			kind := "Transaction"
			if trx.ID == chain.RootTransaction {
				kind = "Root owner"
			}
			result += wrap(fmt.Sprintf("%s %s · connection %d · %s@%s · age %s · %s", kind, trx.ID, trx.ProcessID, fallback(trx.User, "unknown"), fallback(trx.Host, "unknown"), humanDuration(trx.AgeSeconds), trx.State)) + "\n"
			result += wrap(fmt.Sprintf("  %d rows locked · %d modified · SQL: %s", trx.RowsLocked, trx.RowsModified, fallback(trx.Statement, "not currently executing / not captured"))) + "\n"
		}
		result += "\n"
		for _, edge := range chain.Edges {
			result += wrap(fmt.Sprintf("%s → %s  %s.%s · %s · %s", edge.BlockingTransaction, edge.WaitingTransaction, edge.Schema, edge.Table, edge.Index, edge.LockMode)) + "\n"
		}
		for _, note := range chain.Caveats {
			result += wrap("Note: "+note) + "\n"
		}
	}
	result += "\n" + sectionTitle("METADATA LOCKS · OWNERS ARE CANDIDATES") + "\n"
	if len(ctx.MetadataLocks) == 0 {
		result += wrap("No metadata-lock evidence captured.") + "\n"
	}
	for _, lock := range ctx.MetadataLocks {
		result += wrap(fmt.Sprintf("%s · %s.%s · %s · connection %d · %s@%s", lock.Status, lock.Schema, lock.Object, lock.LockType, lock.ProcessID, lock.User, lock.Host)) + "\n"
	}
	for _, capability := range ctx.Capabilities {
		if !capability.Available && (capability.Name == "row lock waits" || capability.Name == "active transactions" || capability.Name == "metadata locks") {
			result += "\n" + wrap("Unavailable: "+capability.Name+" — "+capability.Reason)
		}
	}
	return result + "\n\n" + wrap("Next: confirm the application owner and transaction intent; assess rollback cost before considering cancellation.")
}
