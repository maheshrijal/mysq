package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/maheshrijal/mysqldot/internal/model"
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
	if view := m.View(); !strings.Contains(view, "12.0 qps") || !strings.Contains(view, "TOP FINDING") {
		t.Fatalf("overview missing content:\n%s", view)
	}
	for _, expected := range []string{"Test finding", "SELECT * FROM t", "app.t", "No other connections", "performance_schema"} {
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
		if strings.Contains(line, "mysqldot") && strings.Contains(line, "health") {
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
