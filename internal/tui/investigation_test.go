package tui

import (
	"context"
	"errors"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/maheshrijal/mysq/internal/analyze"
	"github.com/maheshrijal/mysq/internal/model"
	"strings"
	"testing"
	"time"
)

func investigationFixture() Model {
	ctx := &model.Context{CollectedAt: time.Unix(100, 0), Server: model.Server{Host: "db", Port: 3306, PerformanceSchema: true}, Metrics: model.Metrics{BufferPoolHitPercent: 100},
		Locks:        []model.LockWait{{BlockingTransaction: "root", WaitingTransaction: "waiter", Schema: "app", Table: "orders"}},
		Transactions: []model.Transaction{{ID: "root", ProcessID: 42, User: "checkout-worker", Host: "app", Statement: "UPDATE orders SET status=?", AgeSeconds: 40}, {ID: "waiter"}}}
	analyze.Apply(ctx)
	m := New(context.Background(), nil, nil)
	m.snapshot = ctx
	m.loading = false
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(Model)
}

func TestOverviewOpensBlockingEvidenceAboveFold(t *testing.T) {
	m := investigationFixture()
	if view := m.View(); !strings.Contains(view, "Transactions are blocked") || !strings.Contains(view, "enter") {
		t.Fatalf("priority issue hidden:\n%s", view)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.inInvestigation() || !strings.Contains(m.viewport.View(), "FINDING EVIDENCE") {
		t.Fatal("enter did not open finding")
	}
	if content := findingInvestigation(m.snapshot, m.snapshot.Findings[0], 76); !strings.Contains(content, "checkout-worker") || !strings.Contains(content, "root → waiter") || !strings.Contains(content, "UPDATE orders") {
		t.Fatal(content)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.inInvestigation() || m.tab != 0 {
		t.Fatal("back lost overview")
	}
}

func TestFindingSelectionFilterRefreshAndBack(t *testing.T) {
	m := investigationFixture()
	m.tab = 4
	for i := 0; i < 30; i++ {
		m.snapshot.Findings = append(m.snapshot.Findings, model.Finding{ID: fmt.Sprint(i), Title: fmt.Sprintf("Finding %d", i), Severity: model.SeverityNote})
	}
	m.rebuild()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = updated.(Model)
	selected := m.selectedFindingID()
	offset := m.viewport.YOffset
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.findingDetailID != selected {
		t.Fatal("wrong detail opened")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.selectedFindingID() != selected || m.viewport.YOffset != offset {
		t.Fatal("back lost selection")
	}
	next := *m.snapshot
	next.Findings = append([]model.Finding{{ID: "new"}}, m.snapshot.Findings...)
	updated, _ = m.Update(inspectMessage{context: &next})
	m = updated.(Model)
	if m.selectedFindingID() != selected {
		t.Fatal("refresh lost identity")
	}
}

func TestStaleHeaderAndBlockerOverlayNavigation(t *testing.T) {
	m := investigationFixture()
	updated, _ := m.Update(inspectMessage{err: errors.New("connection lost")})
	m = updated.(Model)
	if !strings.Contains(m.header(), "STALE") || !strings.Contains(m.View(), "REFRESH FAILED") {
		t.Fatal(m.View())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'B'}})
	m = updated.(Model)
	if !m.blockerDetail || !strings.Contains(m.View(), "BLOCKING CHAINS") {
		t.Fatal(m.View())
	}
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = updated.(Model)
	assertViewWidth(t, m.View(), 60)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	if m.inInvestigation() || m.tab != 1 {
		t.Fatal("tab navigation trapped in overlay")
	}
}
