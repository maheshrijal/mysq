package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/maheshrijal/mysq/internal/model"
)

func TestTUIRendersAndNavigatesAllViews(t *testing.T) {
	ctx := &model.Context{
		Fingerprint: "abc", Server: model.Server{Host: "127.0.0.1", Port: 3306, Database: "app", Flavor: "MySQL", Version: "8.4.0", UptimeSeconds: 100},
		Health: model.Health{Score: 92, Warnings: 1}, Metrics: model.Metrics{QueriesPerSecond: 12, ConnectionsCurrent: 2, ConnectionsMax: 100, BufferPoolHitPercent: 99.9},
		Findings: []model.Finding{{Severity: model.SeverityWarning, Title: "Test finding", Summary: "Evidence", Recommendation: "Review it."}},
		Queries:  []model.Query{{Statement: "SELECT * FROM t WHERE id = ?", Calls: 10, TotalLatencyMillis: 100}},
		Tables:   []model.Table{{Schema: "app", Name: "t", HasPrimaryKey: true}}, Variables: map[string]string{"performance_schema": "ON"},
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 38})
	m = updated.(Model)
	if view := m.View(); !strings.Contains(view, "12.0 qps") || !strings.Contains(view, "DATABASE POSTURE") || !strings.Contains(view, "PRIORITY SIGNAL") {
		t.Fatalf("overview missing content:\n%s", view)
	}
	for _, expected := range []string{"No other connections", "SELECT * FROM t", "INNODB I/O AND REDO", "Test finding", "app.t", "performance_schema"} {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = updated.(Model)
		if view := m.View(); !strings.Contains(view, expected) {
			t.Fatalf("tab %s missing %q:\n%s", tabs[m.tab], expected, view)
		}
	}
}

func TestNarrowTerminalKeepsHeaderAndTablesHorizontal(t *testing.T) {
	ctx := &model.Context{
		Fingerprint: "abc", Server: model.Server{Host: "db", Port: 3306, Database: "app", Flavor: "MySQL", Version: "8.4.0"},
		Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100, BufferPoolHitPercent: 100},
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = updated.(Model)
	view := m.View()
	foundHeader := false
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > 80 {
			t.Fatalf("line width %d exceeds terminal:\n%s", lipgloss.Width(line), line)
		}
		if strings.Contains(line, "MYSQ") && strings.Contains(line, "HEALTHY") {
			foundHeader = true
		}
	}
	if !foundHeader {
		t.Fatalf("brand and health wrapped onto separate lines:\n%s", view)
	}
	header := row([]string{"TOTAL", "SHARE", "CALLS"}, []int{10, 10, 10}, true)
	firstLine := strings.Split(header, "\n")[0]
	if !strings.Contains(firstLine, "TOTAL") || !strings.Contains(firstLine, "SHARE") || !strings.Contains(firstLine, "CALLS") {
		t.Fatalf("table header wrapped vertically: %q", header)
	}
}

func TestResponsiveChromeNeverExceedsTerminal(t *testing.T) {
	ctx := &model.Context{
		Fingerprint: "abc", Server: model.Server{Host: "database.internal", Port: 3306, Database: "application", Flavor: "MySQL", Version: "8.4.0"},
		Health: model.Health{Score: 76, Warnings: 2}, Metrics: model.Metrics{ConnectionsCurrent: 12, ConnectionsMax: 100, BufferPoolHitPercent: 99.7},
		Findings: []model.Finding{{ID: "schema.missing-primary-key", Severity: model.SeverityWarning, Subsystem: "schema", Title: "A long finding title remains readable", Summary: "Evidence remains readable without forcing the layout beyond the terminal edge.", Recommendation: "Add an explicit primary key."}},
	}
	for _, size := range []tea.WindowSizeMsg{{Width: 60, Height: 24}, {Width: 80, Height: 30}, {Width: 109, Height: 32}, {Width: 110, Height: 32}, {Width: 150, Height: 42}} {
		m := New(context.Background(), nil, nil)
		m.loading = false
		m.snapshot = ctx
		updated, _ := m.Update(size)
		view := updated.(Model).View()
		lines := strings.Split(view, "\n")
		if len(lines) > size.Height {
			t.Fatalf("%dx%d rendered %d lines", size.Width, size.Height, len(lines))
		}
		for _, line := range lines {
			if got := lipgloss.Width(line); got > size.Width {
				t.Fatalf("%dx%d rendered line width %d:\n%s", size.Width, size.Height, got, line)
			}
		}
	}
}

func TestTinyTerminalShowsResizeState(t *testing.T) {
	m := New(context.Background(), nil, nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	view := updated.(Model).View()
	if !strings.Contains(view, "A little more room") || !strings.Contains(view, "40×12") {
		t.Fatalf("missing resize state:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > 40 {
			t.Fatalf("tiny layout rendered width %d:\n%s", got, line)
		}
	}
}

func TestArrowKeysNavigateViewsAndJKScrollsContent(t *testing.T) {
	ctx := &model.Context{Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100, BufferPoolHitPercent: 100}}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 30})
	m = updated.(Model)

	for _, step := range []struct {
		key  tea.KeyType
		want int
	}{{tea.KeyRight, 1}, {tea.KeyDown, 2}, {tea.KeyLeft, 1}, {tea.KeyUp, 0}} {
		updated, _ = m.Update(tea.KeyMsg{Type: step.key})
		m = updated.(Model)
		if m.tab != step.want {
			t.Fatalf("%s selected tab %d, want %d", tea.KeyMsg{Type: step.key}.String(), m.tab, step.want)
		}
	}

	m.viewport.SetContent(strings.Repeat("scrollable line\n", 100))
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(Model)
	if m.viewport.YOffset != 1 {
		t.Fatalf("j scroll offset = %d, want 1", m.viewport.YOffset)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(Model)
	if m.viewport.YOffset != 0 {
		t.Fatalf("k scroll offset = %d, want 0", m.viewport.YOffset)
	}
}

func TestExportConfirmationKeepsDestinationVisible(t *testing.T) {
	ctx := &model.Context{Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100, BufferPoolHitPercent: 100}}
	path := "/Users/mahesh/code/mysq/mysq-export-20260822-154220.547"
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 30})
	m = updated.(Model)
	updated, _ = m.Update(exportMessage{path: path})
	m = updated.(Model)
	view := m.View()
	if !strings.Contains(view, "Agent bundle exported:") || !strings.Contains(view, path) {
		t.Fatalf("export confirmation omitted destination:\n%s", view)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if view := m.View(); strings.Contains(view, path) {
		t.Fatalf("escape did not dismiss export confirmation:\n%s", view)
	}

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = updated.(Model)
	updated, _ = m.Update(exportMessage{path: path})
	view = updated.(Model).View()
	if !strings.Contains(view, "…") || !strings.Contains(view, "mysq-export-20260822-154220.547") {
		t.Fatalf("narrow export confirmation lost bundle name:\n%s", view)
	}
}

func TestTopTabsReclaimWidthAndWindowOnNarrowTerminals(t *testing.T) {
	statement := "SELECT account_id, status, amount FROM app.orders WHERE account_id = ?"
	ctx := &model.Context{
		Health:   model.Health{Score: 88, Warnings: 1},
		Metrics:  model.Metrics{ConnectionsMax: 100, BufferPoolHitPercent: 100},
		Findings: []model.Finding{{Severity: model.SeverityWarning}},
		Queries:  []model.Query{{Statement: statement, Calls: 10, TotalLatencyMillis: 100, ActiveUsers: []string{"checkout"}}},
		Tables:   []model.Table{{Schema: "app", Name: "orders", HasPrimaryKey: true}},
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	m.tab = 2
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 32})
	m = updated.(Model)
	view := m.View()
	if strings.Contains(view, "VIEWS") || !strings.Contains(view, "1 Overview") || !strings.Contains(view, "7 Config") {
		t.Fatalf("wide layout did not render the full top tab strip:\n%s", view)
	}
	if !strings.Contains(view, statement) {
		t.Fatalf("full-width query pane still truncated valuable SQL:\n%s", view)
	}
	if !strings.Contains(view, "checkout") {
		t.Fatalf("query pane omitted the currently observed database user:\n%s", view)
	}

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 72, Height: 28})
	m = updated.(Model)
	view = m.View()
	if !strings.Contains(view, "2 Connections") || !strings.Contains(view, "3 QUERIES") || !strings.Contains(view, "4 Engine") {
		t.Fatalf("narrow tab window omitted neighboring tabs:\n%s", view)
	}
	if strings.Contains(view, "1 Overview") || strings.Contains(view, "7 Config") {
		t.Fatalf("narrow tab window rendered off-screen tabs:\n%s", view)
	}
}

func TestQuerySelectionOpensDetailAndEscapeReturnsToList(t *testing.T) {
	ctx := &model.Context{
		Health:  model.Health{Score: 100},
		Metrics: model.Metrics{ConnectionsMax: 100, BufferPoolHitPercent: 100},
		Queries: []model.Query{
			{Statement: "SELECT id FROM users", Calls: 10, TotalLatencyMillis: 100, MeanLatencyMillis: 10, ActiveUsers: []string{"api"}},
			{Digest: "digest-2", Schema: "app", Statement: "UPDATE orders SET status = ? WHERE id = ?", Calls: 3, TotalLatencyMillis: 90, MeanLatencyMillis: 30, RowsExamined: 12, RowsSent: 1, NoIndexUsed: 1, TmpTables: 2, TmpDiskTables: 1, ActiveUsers: []string{"worker"}},
		},
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	m.tab = 2
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 32})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.tab != 2 || m.queryIndex != 1 {
		t.Fatalf("down selected tab=%d query=%d, want tab=2 query=1", m.tab, m.queryIndex)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	view := m.View()
	if !m.queryDetail || !strings.Contains(view, "NORMALIZED SQL") || !strings.Contains(view, "UPDATE orders SET status") || !strings.Contains(view, "USER") || !strings.Contains(view, "worker") {
		t.Fatalf("enter did not open useful query detail:\n%s", view)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	view = m.View()
	if m.queryDetail || m.queryIndex != 1 || !strings.Contains(view, "ROWS EXAM") || !strings.Contains(view, "USER") || !strings.Contains(view, "QUERY") {
		t.Fatalf("escape did not return to the selected query row:\n%s", view)
	}
	if strings.Contains(view, "TMP-D") || strings.Contains(view, "EXAM/SENT") {
		t.Fatalf("query list retained low-value headers:\n%s", view)
	}
}

func TestQueryListAndDetailStayWithinNarrowTerminal(t *testing.T) {
	ctx := &model.Context{
		Health:  model.Health{Score: 100},
		Metrics: model.Metrics{ConnectionsMax: 100, BufferPoolHitPercent: 100},
		Queries: []model.Query{{
			Digest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Schema: "application",
			Statement: "SELECT a_very_long_column_name FROM a_very_long_table_name WHERE tenant_id = ? AND status = ?",
			Calls:     1200, TotalLatencyMillis: 9000, MeanLatencyMillis: 7.5, RowsExamined: 999999, RowsSent: 100,
			ActiveUsers: []string{"checkout-worker"}, FirstSeen: "2026-08-22T10:00:00Z", LastSeen: "2026-08-22T11:00:00Z",
		}},
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	m.tab = 2
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 28})
	m = updated.(Model)
	assertViewWidth(t, m.View(), 60)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	assertViewWidth(t, m.View(), 60)
}

func assertViewWidth(t *testing.T, view string, width int) {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("rendered line width %d exceeds %d:\n%s", got, width, view)
		}
	}
}
