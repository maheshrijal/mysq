package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/maheshrijal/mysq/internal/control"
	"github.com/maheshrijal/mysq/internal/model"
)

type fakeConnectionController struct {
	fakeQueryController
	lookedUp   []model.Process
	terminated []control.Connection
}

func (f *fakeConnectionController) Connection(_ context.Context, uuid string, p model.Process) (control.Connection, error) {
	f.lookedUp = append(f.lookedUp, p)
	p.Command = "Sleep"
	return control.Connection{Process: p, ServerUUID: uuid}, f.err
}
func (f *fakeConnectionController) KillConnection(_ context.Context, c control.Connection, confirmation string) error {
	if confirmation != "kill" {
		panic("unconfirmed connection kill")
	}
	f.terminated = append(f.terminated, c)
	return f.err
}
func connectionActionModel() (Model, *fakeConnectionController) {
	f := &fakeConnectionController{}
	m := New(context.Background(), nil, nil, f)
	m.loading, m.tab = false, 1
	m.snapshot = &model.Context{Server: model.Server{UUID: "server"}}
	for i := 0; i < 35; i++ {
		m.snapshot.Processes = append(m.snapshot.Processes, model.Process{ID: uint64(101 + i), ThreadID: uint64(201 + i), User: fmt.Sprintf("user%d", i), Host: "web-1", Database: "app", Statement: "SELECT ? FROM a_table_with_a_long_name"})
	}
	m.snapshot.ConnectionGroups = []model.ConnectionGroup{{Kind: "user", Key: "long-user-name-that-wraps-across-lines", Total: 35}}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(Model), f
}
func TestConnectionNavigationFilterRefreshAndDetail(t *testing.T) {
	m, _ := connectionActionModel()
	for _, name := range []string{"j", "down", "j"} {
		actionKey(&m, name)
	}
	if p, _ := m.selectedConnection(); p.ID != 104 {
		t.Fatal(p)
	}
	actionKey(&m, "enter")
	if !m.connectionDetail || !strings.Contains(ansi.Strip(m.View()), "Connection 104") {
		t.Fatal(m.View())
	}
	actionKey(&m, "esc")
	actionKey(&m, "/")
	actionKey(&m, "user20")
	if p, _ := m.selectedConnection(); p.ID != 121 {
		t.Fatal(p)
	}
	actionKey(&m, "esc")
	if p, _ := m.selectedConnection(); p.ID != 104 {
		t.Fatal("cancel filter lost selection", p)
	}
	actionKey(&m, "/")
	actionKey(&m, "user20")
	actionKey(&m, "enter")
	if p, _ := m.selectedConnection(); p.ID != 121 {
		t.Fatal(p)
	}
	actionKey(&m, "esc")
	actionKey(&m, "G")
	p, _ := m.selectedConnection()
	if p.ID != 135 || !strings.Contains(ansi.Strip(m.viewport.View()), "135") {
		t.Fatal("last selection offscreen", m.View())
	}
	actionKey(&m, "g")
	if p, _ := m.selectedConnection(); p.ID != 101 {
		t.Fatal(p)
	}
	actionKey(&m, "f")
	if m.connectionIndex <= 1 {
		t.Fatal("paging did not select rows")
	}
	selected, _ := m.selectedConnection()
	next := *m.snapshot
	next.Processes = append([]model.Process{selected}, m.snapshot.Processes[:2]...)
	updated, _ := m.Update(inspectMessage{context: &next})
	m = updated.(Model)
	if p, _ := m.selectedConnection(); p.ID != selected.ID || m.connectionIndex != 0 {
		t.Fatal("refresh lost identity", p)
	}
	actionKey(&m, "enter")
	removed := next
	removed.Processes = next.Processes[1:]
	updated, _ = m.Update(inspectMessage{context: &removed})
	m = updated.(Model)
	if m.connectionDetail {
		t.Fatal("ended connection detail retargeted")
	}
	actionKey(&m, "B")
	if !m.blockerDetail {
		t.Fatal("blocking chains lost")
	}
}

func TestConnectionKillRequiresExactConfirmationAndFrozenTarget(t *testing.T) {
	for _, bad := range []string{"", "yes", "KILL", "kill ", " kill"} {
		m, f := connectionActionModel()
		deliverAction(&m, actionKey(&m, "K"))
		m.live.input.SetValue(bad)
		if cmd := actionKey(&m, "enter"); cmd != nil || len(f.terminated) != 0 || m.live.stage != "confirm" {
			t.Fatalf("accepted %q", bad)
		}
	}
	m, f := connectionActionModel()
	actionKey(&m, "j")
	deliverAction(&m, actionKey(&m, "K"))
	if len(f.lookedUp) != 1 || f.lookedUp[0].ID != 102 || m.live.connectionTarget.Command != "Sleep" {
		t.Fatal("wrong live lookup", m.live)
	}
	for _, name := range []string{"3", "tab", "r", "B", "j"} {
		actionKey(&m, name)
	}
	if m.tab != 1 || m.live.connectionTarget.ID != 102 || m.live.stage != "confirm" {
		t.Fatal("navigation changed target")
	}
	// A background refresh must not retarget the confirmation.
	next := *m.snapshot
	next.Processes = nil
	updated, _ := m.Update(inspectMessage{context: &next})
	m = updated.(Model)
	m.live.input.SetValue("")
	for _, name := range []string{"k", "i", "l", "l"} {
		actionKey(&m, name)
	}
	cmd := actionKey(&m, "enter")
	if cmd == nil || len(f.terminated) != 0 || m.live.stage != "sending" {
		t.Fatal("dispatch not async")
	}
	if actionKey(&m, "enter") != nil || actionKey(&m, "esc") != nil || m.live.stage != "sending" {
		t.Fatal("duplicate/canceled in-flight kill")
	}
	deliverAction(&m, cmd)
	if len(f.terminated) != 1 || f.terminated[0].ID != 102 || len(f.killed) != 0 {
		t.Fatal("wrong action", f)
	}
	if !strings.Contains(m.View(), "KILL CONNECTION 102 accepted") {
		t.Fatal(m.View())
	}
}

func TestConnectionKillCancelLateLookupAndUnavailable(t *testing.T) {
	m, f := connectionActionModel()
	cmd := actionKey(&m, "K")
	actionKey(&m, "esc")
	deliverAction(&m, cmd)
	if m.live.stage != "" {
		t.Fatal("late lookup reopened modal")
	}
	deliverAction(&m, actionKey(&m, "K"))
	m.live.input.SetValue("kill")
	actionKey(&m, "esc")
	if len(f.terminated) != 0 {
		t.Fatal("escape sent kill")
	}
	f.err = errors.New("connection ended")
	deliverAction(&m, actionKey(&m, "K"))
	if m.live.stage != "result" || !strings.Contains(m.View(), "Nothing was sent") {
		t.Fatal(m.View())
	}
	if actionKey(&m, "enter") != nil || len(f.terminated) != 0 {
		t.Fatal("lookup error dispatched")
	}
	m.snapshot.Processes = nil
	if actionKey(&m, "K") != nil || m.live.stage != "" {
		t.Fatal("empty list action")
	}
}

func TestConnectionKillModalGuardsAndRefresh(t *testing.T) {
	for _, mode := range []string{"help", "filter", "loading", "export", "exporting", "blocker"} {
		m, f := connectionActionModel()
		switch mode {
		case "help":
			m.help = true
		case "filter":
			m.filtering = true
		case "loading":
			m.loading = true
		case "export":
			m.exportPath = "bundle"
		case "exporting":
			m.exporting = true
		case "blocker":
			m.blockerDetail = true
		}
		actionKey(&m, "K")
		if m.live.stage != "" || len(f.lookedUp) != 0 {
			t.Fatal("action in", mode)
		}
	}
	m, f := connectionActionModel()
	deliverAction(&m, actionKey(&m, "K"))
	m.live.input.SetValue("kill")
	f.err = errors.New("MySQL denied termination")
	m.inspect = func(context.Context) (*model.Context, error) { return m.snapshot, nil }
	cmd := actionKey(&m, "enter")
	updated, refresh := m.Update(cmd())
	m = updated.(Model)
	if refresh == nil || !m.loading || m.live.stage != "result" || !strings.Contains(m.View(), "denied termination") {
		t.Fatal("no result/refresh", m.View())
	}
}

func TestConnectionSelectionAndConfirmationAtSupportedSizes(t *testing.T) {
	for _, size := range [][2]int{{52, 18}, {80, 24}, {130, 38}} {
		m, f := connectionActionModel()
		updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = updated.(Model)
		actionKey(&m, "G")
		if !strings.Contains(ansi.Strip(m.viewport.View()), "135") {
			t.Fatal("selection offscreen", m.View())
		}
		deliverAction(&m, actionKey(&m, "K"))
		view := ansi.Strip(m.View())
		for _, want := range []string{"Kill connection 135?", "Type kill:", "open transaction", "Esc"} {
			if !strings.Contains(view, want) {
				t.Fatalf("%v missing %q: %s", size, want, view)
			}
		}
		updated, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
		m = updated.(Model)
		m.live.input.SetValue("kill")
		if actionKey(&m, "enter") != nil || len(f.terminated) != 0 {
			t.Fatal("hidden confirmation dispatched")
		}
	}
}
