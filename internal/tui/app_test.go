package tui

import (
	"context"
	"fmt"
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
	if view := m.View(); !strings.Contains(view, "12.0 qps") || !strings.Contains(view, "DATABASE POSTURE") ||
		!strings.Contains(view, "PRIORITY SIGNAL") || !strings.Contains(view, "CURRENT MYSQL LOAD") ||
		!strings.Contains(view, "QUERY HEALTH") || !strings.Contains(view, "CONTENTION") {
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

func TestOverviewPrioritizesCurrentMySQLInvestigationSignals(t *testing.T) {
	lag := int64(2)
	ctx := &model.Context{
		Server: model.Server{Flavor: "MySQL", Version: "8.4.0"},
		Health: model.Health{Score: 76, Warnings: 1},
		Metrics: model.Metrics{
			ConnectionsMax: 100, BufferPoolHitPercent: 99.9, RedoCheckpointAgePct: 67,
			FullScansPerSecond: 4.2, TempDiskTablePercent: 12.5, HistoryListLength: 4200,
		},
		StatementSamples: []model.StatementSample{{Statement: "SELECT * FROM orders WHERE account_id = ?", DatabaseTimeSharePercent: 62.5}},
		Locks:            []model.LockWait{{BlockingTransaction: "trx-1", Schema: "app", Table: "orders"}, {BlockingTransaction: "trx-1", Schema: "app", Table: "orders"}},
		Transactions:     []model.Transaction{{ID: "trx-1", User: "checkout", AgeSeconds: 31}},
		Replication:      &model.Replication{IORunning: "Yes", SQLRunning: "Yes", ApplierState: "ON", SecondsBehind: &lag, Workers: []model.ReplicationWorker{{WorkerID: 1}}},
		Instrumentation:  model.Instrumentation{DigestRows: 20, DigestCapacity: 100, DisabledConsumers: []string{"events_waits_current"}},
	}
	view := overview(ctx, 150)
	for _, expected := range []string{"Redo checkpoint", "TOP SQL", "62.5%", "FULL SCANS / DISK TEMP", "BLOCKER", "checkout", "app.orders", "4.2k", "REPLICATION STATUS", "LAG", "2s", "DATA COVERAGE", "events_waits_current"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("overview missing %q:\n%s", expected, view)
		}
	}
	if strings.Contains(view, "Buffer pool used") || strings.Contains(view, "MAX ") {
		t.Fatalf("overview retained low-value pressure or latency signal:\n%s", view)
	}
}

func TestEngineExplainsPerFamilySampleWindows(t *testing.T) {
	ctx := &model.Context{Metrics: model.Metrics{ConnectionsMax: 100}}
	view := engine(ctx, 100)
	if !strings.Contains(view, "per-family sample windows") || strings.Contains(view, "the collection interval") {
		t.Fatalf("engine retained ambiguous sampling guidance:\n%s", view)
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
			t.Fatalf("%dx%d rendered %d lines:\n%s", size.Width, size.Height, len(lines), view)
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

func TestHorizontalArrowsNavigateViewsAndVerticalKeysScrollContent(t *testing.T) {
	ctx := &model.Context{Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100, BufferPoolHitPercent: 100}}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	m.tab = 3
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 30})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	if m.tab != 4 {
		t.Fatalf("right selected tab %d, want 4", m.tab)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(Model)
	if m.tab != 3 {
		t.Fatalf("left selected tab %d, want 3", m.tab)
	}

	m.viewport.SetContent(strings.Repeat("scrollable line\n", 100))
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.tab != 3 || m.viewport.YOffset != 1 {
		t.Fatalf("down selected tab=%d offset=%d, want tab=3 offset=1", m.tab, m.viewport.YOffset)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.tab != 3 || m.viewport.YOffset != 0 {
		t.Fatalf("up selected tab=%d offset=%d, want tab=3 offset=0", m.tab, m.viewport.YOffset)
	}
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
	path := "/workspace/mysq-export-20260822-154220.547"
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

func TestExportConfirmationKeepsSelectedQueryVisible(t *testing.T) {
	ctx := &model.Context{Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100}}
	for i := 0; i < 30; i++ {
		ctx.Queries = append(ctx.Queries, model.Query{Statement: fmt.Sprintf("SELECT %d FROM orders", i)})
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	m.tab = 2
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 18})
	m = updated.(Model)

	for i := 0; i < m.viewport.Height-1; i++ {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(Model)
	}
	selectedLine := m.queryIndex + 2
	if selectedLine >= m.viewport.YOffset+m.viewport.Height {
		t.Fatalf("test setup left selected line %d outside viewport [%d,%d)", selectedLine, m.viewport.YOffset, m.viewport.YOffset+m.viewport.Height)
	}

	updated, _ = m.Update(exportMessage{path: "/workspace/bundle"})
	m = updated.(Model)
	if selectedLine < m.viewport.YOffset || selectedLine >= m.viewport.YOffset+m.viewport.Height {
		t.Fatalf("export confirmation left selected line %d outside viewport [%d,%d)", selectedLine, m.viewport.YOffset, m.viewport.YOffset+m.viewport.Height)
	}
	selectedStatement := fmt.Sprintf("SELECT %d FROM orders", m.queryIndex)
	if !strings.Contains(m.viewport.View(), selectedStatement) {
		t.Fatalf("export confirmation clipped selected statement %q:\n%s", selectedStatement, m.viewport.View())
	}
}

func TestExportKeyIsIgnoredWhileExportIsRunning(t *testing.T) {
	ctx := &model.Context{Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100}}
	m := New(context.Background(), nil, func(*model.Context) (string, error) { return "bundle", nil })
	m.loading = false
	m.snapshot = ctx

	updated, first := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m = updated.(Model)
	if !m.exporting || first == nil {
		t.Fatal("first export did not enter the exporting state")
	}
	updated, second := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m = updated.(Model)
	if !m.exporting || second != nil {
		t.Fatal("repeated export launched another command")
	}
	updated, _ = m.Update(exportMessage{path: "bundle"})
	m = updated.(Model)
	if m.exporting {
		t.Fatal("completed export did not clear the exporting state")
	}
	updated, repeated := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m = updated.(Model)
	if repeated != nil || m.exportPath != "bundle" {
		t.Fatal("export confirmation allowed a repeated export")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	updated, afterDismiss := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m = updated.(Model)
	if !m.exporting || afterDismiss == nil {
		t.Fatal("dismissing confirmation did not allow a new export")
	}
}

func TestExportStateBlocksCompetingOverlays(t *testing.T) {
	ctx := &model.Context{
		Health:  model.Health{Score: 100},
		Metrics: model.Metrics{ConnectionsMax: 100},
		Queries: []model.Query{{Statement: "SELECT id FROM orders"}},
	}
	m := New(context.Background(), nil, func(*model.Context) (string, error) { return "bundle", nil })
	m.loading = false
	m.snapshot = ctx
	m.tab = 2

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m = updated.(Model)
	for _, pressed := range []rune{'/', '?'} {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{pressed}})
		m = updated.(Model)
		if m.filtering || m.help {
			t.Fatalf("%q opened an overlay while export was running: filtering=%v help=%v", pressed, m.filtering, m.help)
		}
	}

	updated, _ = m.Update(exportMessage{path: "/workspace/bundle"})
	m = updated.(Model)
	for _, pressed := range []rune{'/', '?'} {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{pressed}})
		m = updated.(Model)
		if m.filtering || m.help {
			t.Fatalf("%q opened an overlay behind export confirmation: filtering=%v help=%v", pressed, m.filtering, m.help)
		}
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.exportPath != "" {
		t.Fatalf("escape did not dismiss export confirmation: %q", m.exportPath)
	}
}

func TestNarrowTerminalShowsHelpKeys(t *testing.T) {
	ctx := &model.Context{Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100}}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = updated.(Model)
	view := m.View()
	for _, expected := range []string{"KEYBOARD HELP", "1–7", "jump to view", "pgup/b", "home/g", "export bundle", "esc/?", "close help"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("narrow help omitted %q:\n%s", expected, view)
		}
	}
	assertViewWidth(t, view, 60)
}

func TestHelpScrollsAndEscapeRestoresViewPosition(t *testing.T) {
	ctx := &model.Context{Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100}}
	for i := 0; i < 30; i++ {
		ctx.WaitEvents = append(ctx.WaitEvents, model.WaitEvent{Name: "wait/event/" + strings.Repeat("x", i%5+1)})
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	m.tab = 3
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = updated.(Model)
	wantOffset := m.viewport.YOffset
	if wantOffset == 0 {
		t.Fatal("engine page down did not scroll")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = updated.(Model)
	if !m.help || !strings.Contains(m.View(), "KEYBOARD HELP") {
		t.Fatalf("question mark did not open contextual help:\n%s", m.View())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	m = updated.(Model)
	if !strings.Contains(m.View(), "q/ctrl-c") {
		t.Fatalf("help could not scroll to global actions:\n%s", m.View())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.help || m.viewport.YOffset != wantOffset {
		t.Fatalf("escape restored help=%v offset=%d, want false/%d", m.help, m.viewport.YOffset, wantOffset)
	}
}

func TestHelpResizeAndRefreshPreserveQueryListVisibility(t *testing.T) {
	ctx := &model.Context{Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100}}
	for i := 0; i < 30; i++ {
		ctx.Queries = append(ctx.Queries, model.Query{Digest: fmt.Sprintf("digest-%d", i), Schema: "app", Statement: fmt.Sprintf("SELECT %d FROM orders", i)})
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	m.tab = 2
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 18})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = updated.(Model)
	wantOffset := m.viewOffsets[m.tab]
	if wantOffset == 0 {
		t.Fatal("deep query selection did not scroll")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 96, Height: 20})
	m = updated.(Model)
	updated, _ = m.Update(inspectMessage{context: ctx})
	m = updated.(Model)
	if !m.help || m.viewOffsets[m.tab] != wantOffset {
		t.Fatalf("help resize/refresh changed underlying offset to %d, want %d", m.viewOffsets[m.tab], wantOffset)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	selectedLine := m.queryIndex + 2
	if m.help || selectedLine < m.viewport.YOffset || selectedLine >= m.viewport.YOffset+m.viewport.Height {
		t.Fatalf("closing help left selected line %d outside viewport [%d,%d)", selectedLine, m.viewport.YOffset, m.viewport.YOffset+m.viewport.Height)
	}
}

func TestKeyboardHelpIncludesEveryGlobalActionAtResponsiveWidths(t *testing.T) {
	ctx := &model.Context{
		Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100},
		Queries: []model.Query{{Statement: "SELECT id FROM orders"}},
	}
	for tab := range tabs {
		for width := 52; width <= 150; width++ {
			m := New(context.Background(), nil, nil)
			m.loading = false
			m.snapshot = ctx
			m.tab = tab
			updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
			m = updated.(Model)
			help := m.keyboardHelp()
			compactHelp := strings.Join(strings.Fields(help), " ")
			for _, expected := range []string{"r refresh", "e export bundle", "? keyboard help", "q/ctrl-c quit"} {
				if !strings.Contains(compactHelp, expected) {
					t.Fatalf("%s help at width %d omitted %q:\n%s", tabs[tab], width, expected, help)
				}
			}
		}
	}
}

func TestTabSwitchAndResizePreservePerViewScroll(t *testing.T) {
	ctx := &model.Context{Health: model.Health{Score: 70}, Metrics: model.Metrics{ConnectionsMax: 100}}
	for i := 0; i < 24; i++ {
		ctx.Findings = append(ctx.Findings, model.Finding{ID: fmt.Sprintf("finding-%02d", i), Severity: model.SeverityWarning, Title: "Finding", Summary: "Evidence", Recommendation: "Review"})
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	m.tab = 4
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 26})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = updated.(Model)
	wantOffset := m.viewport.YOffset
	if wantOffset == 0 {
		t.Fatal("findings page down did not scroll")
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(Model)
	if m.tab != 4 || m.viewport.YOffset != wantOffset {
		t.Fatalf("tab return restored tab=%d offset=%d, want 4/%d", m.tab, m.viewport.YOffset, wantOffset)
	}
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 92, Height: 24})
	m = updated.(Model)
	if m.viewport.YOffset != wantOffset {
		t.Fatalf("resize reset offset to %d, want %d", m.viewport.YOffset, wantOffset)
	}
}

func TestQueryPagerKeysMoveSelectionAndKeepItVisible(t *testing.T) {
	ctx := &model.Context{Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100}}
	for i := 0; i < 30; i++ {
		ctx.Queries = append(ctx.Queries, model.Query{Statement: fmt.Sprintf("SELECT %d FROM orders", i), TotalLatencyMillis: float64(30 - i)})
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	m.tab = 2
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 22})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = updated.(Model)
	if m.queryIndex == 0 {
		t.Fatalf("page down left query selection=%d offset=%d", m.queryIndex, m.viewport.YOffset)
	}
	selectedLine := m.queryIndex + 2
	if selectedLine < m.viewport.YOffset || selectedLine >= m.viewport.YOffset+m.viewport.Height {
		t.Fatalf("selected line %d is outside viewport [%d,%d)", selectedLine, m.viewport.YOffset, m.viewport.YOffset+m.viewport.Height)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = updated.(Model)
	if m.viewport.YOffset == 0 {
		t.Fatalf("second page down did not advance viewport for query %d", m.queryIndex)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = updated.(Model)
	if m.queryIndex != len(ctx.Queries)-1 {
		t.Fatalf("end selected query %d, want %d", m.queryIndex, len(ctx.Queries)-1)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m = updated.(Model)
	if m.queryIndex != 0 {
		t.Fatalf("home selected query %d, want 0", m.queryIndex)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = updated.(Model)
	if m.queryIndex != 1 {
		t.Fatalf("j selected query %d, want 1", m.queryIndex)
	}
}

func TestWalkingUpKeepsSelectedQueryVisible(t *testing.T) {
	ctx := &model.Context{Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100}}
	for i := 0; i < 30; i++ {
		ctx.Queries = append(ctx.Queries, model.Query{Statement: fmt.Sprintf("SELECT %d FROM orders", i)})
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	m.tab = 2
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 18})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = updated.(Model)
	for i := 0; i < 20; i++ {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
		m = updated.(Model)
		selectedLine := m.queryIndex + 2
		if selectedLine < m.viewport.YOffset || selectedLine >= m.viewport.YOffset+m.viewport.Height {
			t.Fatalf("query %d line %d is outside viewport [%d,%d)", m.queryIndex, selectedLine, m.viewport.YOffset, m.viewport.YOffset+m.viewport.Height)
		}
	}
}

func TestResizeKeepsSelectedQueryVisible(t *testing.T) {
	ctx := &model.Context{Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100}}
	for i := 0; i < 30; i++ {
		ctx.Queries = append(ctx.Queries, model.Query{Statement: fmt.Sprintf("SELECT %d FROM orders", i)})
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	m.tab = 2
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = updated.(Model)
	for i := 0; i < 20; i++ {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(Model)
	}
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 18})
	m = updated.(Model)
	selectedLine := m.queryIndex + 2
	if selectedLine < m.viewport.YOffset || selectedLine >= m.viewport.YOffset+m.viewport.Height {
		t.Fatalf("selected line %d is outside resized viewport [%d,%d)", selectedLine, m.viewport.YOffset, m.viewport.YOffset+m.viewport.Height)
	}
	if m.viewOffsets[m.tab] != m.viewport.YOffset {
		t.Fatalf("resized viewport offset %d was not persisted: %d", m.viewport.YOffset, m.viewOffsets[m.tab])
	}
	updated, _ = m.Update(inspectMessage{context: ctx})
	m = updated.(Model)
	if selectedLine < m.viewport.YOffset || selectedLine >= m.viewport.YOffset+m.viewport.Height {
		t.Fatalf("selected line %d is outside refreshed viewport [%d,%d)", selectedLine, m.viewport.YOffset, m.viewport.YOffset+m.viewport.Height)
	}
}

func TestCurrentViewFilterSelectsVisibleQueryAndEscapeClears(t *testing.T) {
	ctx := &model.Context{
		Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100},
		Queries: []model.Query{
			{Statement: "SELECT id FROM users", TotalLatencyMillis: 10},
			{Statement: "SELECT id FROM orders", TotalLatencyMillis: 9},
		},
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	m.tab = 2
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 26})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	if !m.filtering {
		t.Fatal("slash did not focus the current-view filter")
	}
	for _, r := range "orders" {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	view := m.View()
	if m.filtering || !strings.Contains(view, "SELECT id FROM orders") || strings.Contains(view, "SELECT id FROM users") {
		t.Fatalf("applied filter did not narrow queries:\n%s", view)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.queryDetail || !strings.Contains(m.View(), "SELECT id FROM orders") {
		t.Fatalf("enter opened a query outside the filtered result:\n%s", m.View())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.activeFilter() != "" || !strings.Contains(m.View(), "SELECT id FROM users") {
		t.Fatalf("escape did not clear the active filter:\n%s", m.View())
	}
}

func TestFilterCancelAfterRefreshClampsRestoredQuerySelection(t *testing.T) {
	old := &model.Context{Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100}}
	for i := 0; i < 30; i++ {
		old.Queries = append(old.Queries, model.Query{Statement: fmt.Sprintf("SELECT %d FROM orders", i)})
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = old
	m.tab = 2
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updated.(Model)
	for i := 0; i < 20; i++ {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(Model)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	updated, _ = m.Update(inspectMessage{context: &model.Context{
		Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100},
		Queries: []model.Query{{Statement: "SELECT only FROM orders"}},
	}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.queryIndex != 0 || !strings.Contains(m.View(), "SELECT only FROM orders") {
		t.Fatalf("filter cancel restored stale selection %d for refreshed query list:\n%s", m.queryIndex, m.View())
	}
}

func TestFilterCancelAfterRefreshRestoresQueryIdentity(t *testing.T) {
	old := &model.Context{
		Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100},
		Queries: []model.Query{
			{Digest: "first", Schema: "app", Statement: "SELECT first FROM orders"},
			{Digest: "second", Schema: "app", Statement: "SELECT second FROM orders"},
		},
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = old
	m.tab = 2
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	updated, _ = m.Update(inspectMessage{context: &model.Context{
		Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100},
		Queries: []model.Query{
			{Digest: "second", Schema: "app", Statement: "SELECT second FROM orders"},
			{Digest: "first", Schema: "app", Statement: "SELECT first FROM orders"},
		},
	}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.queryIndex != 0 || !strings.Contains(m.View(), "SELECT second FROM orders") {
		t.Fatalf("filter cancel lost query identity at index %d:\n%s", m.queryIndex, m.View())
	}
}

func TestRefreshPreservesQueryIdentityAndResetsDetailOffset(t *testing.T) {
	ctx := &model.Context{
		Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100},
		Queries: []model.Query{
			{Digest: "shared", Schema: "first_db", Statement: "SELECT first FROM orders"},
			{Digest: "shared", Schema: "second_db", Statement: "SELECT second FROM orders"},
		},
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	m.tab = 2
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 18})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	m.queryDetailOffset = 5

	updated, _ = m.Update(inspectMessage{context: &model.Context{
		Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100},
		Queries: []model.Query{
			{Digest: "shared", Schema: "second_db", Statement: "SELECT second FROM orders"},
			{Digest: "shared", Schema: "first_db", Statement: "SELECT first FROM orders"},
		},
	}})
	m = updated.(Model)
	if !m.queryDetail || m.queryIndex != 0 || m.queryDetailOffset != 0 || !strings.Contains(m.View(), "SELECT second FROM orders") {
		t.Fatalf("refresh lost query identity or retained stale detail offset: detail=%v index=%d offset=%d\n%s",
			m.queryDetail, m.queryIndex, m.queryDetailOffset, m.View())
	}
}

func TestRefreshOffQueriesPreservesFilteredQueryIdentity(t *testing.T) {
	ctx := &model.Context{
		Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100},
		Queries: []model.Query{
			{Digest: "other", Schema: "app", Statement: "SELECT other"},
			{Digest: "first", Schema: "app", Statement: "SELECT match first"},
			{Digest: "second", Schema: "app", Statement: "SELECT match second"},
		},
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	m.tab = 2
	m.filters[2] = "match"
	m.queryIndex = 1
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	updated, _ = m.Update(inspectMessage{context: &model.Context{
		Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100},
		Queries: []model.Query{
			{Digest: "second", Schema: "app", Statement: "SELECT match second"},
			{Digest: "first", Schema: "app", Statement: "SELECT match first"},
			{Digest: "other", Schema: "app", Statement: "SELECT other"},
		},
	}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(Model)
	if m.tab != 2 || m.queryIndex != 0 {
		t.Fatalf("off-tab refresh restored tab=%d query=%d, want Queries/0", m.tab, m.queryIndex)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !strings.Contains(m.View(), "SELECT match second") {
		t.Fatalf("off-tab refresh lost filtered query identity:\n%s", m.View())
	}
}

func TestFilterConsumesPrintableGlobalKeysAsText(t *testing.T) {
	ctx := &model.Context{
		Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100},
		Queries: []model.Query{{Statement: "SELECT id FROM queue", TotalLatencyMillis: 1}},
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	m.tab = 2
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 26})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = updated.(Model)
	if !m.filtering || m.activeFilter() != "q" {
		t.Fatalf("filter treated q as a global action: filtering=%v value=%q", m.filtering, m.activeFilter())
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

func TestQueryDetailAllowsDocumentedViewNavigation(t *testing.T) {
	ctx := &model.Context{
		Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100},
		Queries: []model.Query{{Digest: "query", Schema: "app", Statement: "SELECT id FROM orders"}},
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	m.tab = 2
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.tab != 3 || m.queryDetail {
		t.Fatalf("tab from query detail selected tab=%d detail=%v, want Engine/false", m.tab, m.queryDetail)
	}
}

func TestOpeningDifferentQueryStartsDetailAtTop(t *testing.T) {
	ctx := &model.Context{
		Health: model.Health{Score: 100}, Metrics: model.Metrics{ConnectionsMax: 100},
		Queries: []model.Query{
			{
				Digest: strings.Repeat("a", 64), Schema: "application",
				Statement:          "SELECT " + strings.Repeat("very_long_column, ", 16) + "id FROM first_table WHERE account_id = ?",
				TotalLatencyMillis: 2, ActiveUsers: []string{"checkout-worker", "analytics-worker"},
				FirstSeen: "2026-08-25T10:00:00Z", LastSeen: "2026-08-25T11:00:00Z",
			},
			{Statement: "SELECT second FROM orders", TotalLatencyMillis: 1},
		},
	}
	m := New(context.Background(), nil, nil)
	m.loading = false
	m.snapshot = ctx
	m.tab = 2
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 18})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = updated.(Model)
	if m.viewport.YOffset == 0 {
		t.Fatal("long first query detail did not scroll")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.viewport.YOffset != 0 || !strings.Contains(m.viewport.View(), "QUERY 2 OF 2") {
		t.Fatalf("second query detail opened at offset %d:\n%s", m.viewport.YOffset, m.viewport.View())
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
