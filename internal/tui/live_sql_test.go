package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/maheshrijal/mysq/internal/model"
	"github.com/muesli/termenv"
)

func TestConnectionRowsStayCompactAndDetailsShowLiterals(t *testing.T) {
	profile := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	lipgloss.SetColorProfile(termenv.Ascii)
	for _, width := range []int{48, 76, 100, 126, 200} {
		m, _ := connectionActionModel()
		m.snapshot.Processes = m.snapshot.Processes[:2]
		m.snapshot.ConnectionGroups = nil
		full := "SELECT 'blue9', 42 FROM t WHERE " + strings.Repeat("id=123 OR ", 40) + "id=456"
		for i := range m.snapshot.Processes {
			m.snapshot.Processes[i].Statement = "SELECT ?, ? FROM t WHERE id=?"
			m.snapshot.Processes[i].LiveStatement = full
		}
		content, lines := connectionReport(m.snapshot, width, 1)
		if len(lines) != 2 || lines[1]-lines[0] != 1 {
			t.Fatalf("width %d: expanded process rows %v", width, lines)
		}
		if strings.Count(content, "›") != 1 || strings.Contains(content, full) || strings.Contains(content, "↳ ID") {
			t.Fatal("ambiguous or expanded list", content)
		}
		for _, line := range strings.Split(content, "\n") {
			if lipgloss.Width(line) > width {
				t.Fatal("row overflow", width, line)
			}
		}
		updated, _ := m.Update(tea.WindowSizeMsg{Width: width + 4, Height: 24})
		m = updated.(Model)
		actionKey(&m, "j")
		actionKey(&m, "enter")
		detail := ansi.Strip(m.viewport.View())
		if !strings.Contains(detail, "'blue9'") || !strings.Contains(detail, "42") {
			t.Fatal("detail redacted literals", detail)
		}
		if m.snapshot.Processes[1].Statement != "SELECT ?, ? FROM t WHERE id=?" {
			t.Fatal("display mutated persisted SQL")
		}
	}
}

func TestLiveSQLInQueryAndBlockerViews(t *testing.T) {
	m, f := queryActionModel()
	f.items[0].LiveStatement = "SELECT 'blue9', SLEEP(15)"
	deliverAction(&m, actionKey(&m, "enter"))
	if !strings.Contains(ansi.Strip(m.liveExecutionView()), "'blue9'") {
		t.Fatal("query detail lost literals")
	}
	deliverAction(&m, actionKey(&m, "K"))
	if !strings.Contains(ansi.Strip(m.queryActionView()), "'blue9'") {
		t.Fatal("execution picker lost literals")
	}
	actionKey(&m, "enter")
	if !strings.Contains(ansi.Strip(m.queryActionView()), "'blue9'") {
		t.Fatal("query confirmation lost literals")
	}
	trx := model.Transaction{Statement: "UPDATE t SET name=?", LiveStatement: "UPDATE t SET name='blue9'"}
	if transactionSQL(trx) != trx.LiveStatement {
		t.Fatal("transaction view redacted live SQL")
	}
}
