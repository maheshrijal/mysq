package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/maheshrijal/mysq/internal/control"
	"github.com/maheshrijal/mysq/internal/model"
)

type fakeQueryController struct {
	items  []control.Execution
	killed []control.Execution
	err    error
}

func (f *fakeQueryController) Sessions(context.Context, string, string) ([]control.Execution, error) {
	return f.items, f.err
}
func (f *fakeQueryController) Kill(_ context.Context, e control.Execution, confirmation string) error {
	if confirmation != "kill" {
		panic("unconfirmed kill")
	}
	f.killed = append(f.killed, e)
	return f.err
}

func queryActionModel() (Model, *fakeQueryController) {
	f := &fakeQueryController{items: []control.Execution{
		{Process: model.Process{ID: 11, ThreadID: 21, User: "alice", Host: "web-1", Database: "app", Digest: "digest", State: "executing", Seconds: 30, Statement: "SELECT SLEEP(?)"}, EventID: 31, ServerUUID: "server"},
		{Process: model.Process{ID: 12, ThreadID: 22, User: "bob", Host: "web-2", Database: "app", Digest: "digest", State: "executing", Seconds: 20, Statement: "SELECT SLEEP(?)"}, EventID: 32, ServerUUID: "server"},
	}}
	m := New(context.Background(), nil, nil, f)
	m.loading = false
	m.snapshot = &model.Context{Queries: []model.Query{{Digest: "digest", Schema: "app", Statement: "SELECT SLEEP(?)"}}}
	m.tab = 2
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(Model), f
}

func actionKey(m *Model, name string) tea.Cmd {
	k := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
	switch name {
	case "enter":
		k = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		k = tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		k = tea.KeyMsg{Type: tea.KeyDown}
	case "tab":
		k = tea.KeyMsg{Type: tea.KeyTab}
	}
	next, cmd := m.Update(k)
	*m = next.(Model)
	return cmd
}

func deliverAction(m *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	next, _ := m.Update(cmd())
	*m = next.(Model)
}

func TestKillOnlyQueriesAndNoOtherModal(t *testing.T) {
	for tab := 0; tab < totalViews; tab++ {
		if tab == 2 {
			continue
		}
		m, f := queryActionModel()
		m.tab = tab
		if cmd := actionKey(&m, "K"); cmd != nil || m.live.stage != "" || len(f.killed) != 0 {
			t.Fatalf("kill available on tab %d", tab)
		}
		if strings.Contains(m.helpBindings().ShortHelp()[0].Help().Desc, "kill") {
			t.Fatal("kill hint outside Queries")
		}
	}
	for _, mode := range []string{"finding", "blocker", "help", "filter", "loading", "export"} {
		m, _ := queryActionModel()
		switch mode {
		case "finding":
			m.findingDetailID = "finding"
		case "blocker":
			m.blockerDetail = true
		case "help":
			m.help = true
		case "filter":
			m.filtering = true
		case "loading":
			m.loading = true
		case "export":
			m.exportPath = "bundle"
		}
		actionKey(&m, "K")
		if m.live.stage != "" {
			t.Fatalf("kill available in %s", mode)
		}
	}
}

func TestQueryDetailFetchesExecutingUsersAndConnections(t *testing.T) {
	m, _ := queryActionModel()
	deliverAction(&m, actionKey(&m, "enter"))
	view := ansi.Strip(m.View())
	for _, want := range []string{"CURRENT EXECUTIONS", "alice", "bob", "web-1", "web-2", "CONNECTION", "11", "DATABASE app", "30s", "executing"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q: %s", want, view)
		}
	}
}

func TestKillRequiresExactConfirmationAndFreezesOneSession(t *testing.T) {
	for _, bad := range []string{"", "yes", "KILL", "kill ", " kill"} {
		m, f := queryActionModel()
		deliverAction(&m, actionKey(&m, "K"))
		actionKey(&m, "enter")
		m.live.input.SetValue(bad)
		if cmd := actionKey(&m, "enter"); cmd != nil || len(f.killed) != 0 || m.live.stage != "confirm" {
			t.Fatalf("accepted %q", bad)
		}
	}
	m, f := queryActionModel()
	deliverAction(&m, actionKey(&m, "K"))
	actionKey(&m, "down")
	actionKey(&m, "enter")
	for _, key := range []string{"3", "tab", "r", "B"} {
		actionKey(&m, key)
	}
	if m.tab != 2 || m.live.target.ID != 12 || m.live.stage != "confirm" {
		t.Fatal("target changed during confirmation")
	}
	m.live.input.SetValue("")
	for _, key := range []string{"k", "i", "l", "l"} {
		actionKey(&m, key)
	}
	cmd := actionKey(&m, "enter")
	if len(f.killed) != 0 || cmd == nil || m.live.stage != "sending" {
		t.Fatal("dispatch must be asynchronous")
	}
	if actionKey(&m, "enter") != nil {
		t.Fatal("duplicate kill while sending")
	}
	deliverAction(&m, cmd)
	if len(f.killed) != 1 || f.killed[0].ID != 12 {
		t.Fatalf("wrong executions killed: %+v", f.killed)
	}
	if !strings.Contains(m.View(), "accepted") || !strings.Contains(m.View(), "locks may remain") {
		t.Fatal(m.View())
	}
}

func TestKillEscapeAndLateLookupDoNothing(t *testing.T) {
	m, f := queryActionModel()
	cmd := actionKey(&m, "K")
	actionKey(&m, "esc")
	deliverAction(&m, cmd)
	if m.live.stage != "" || len(m.live.items) != 0 {
		t.Fatal("late lookup resurrected picker")
	}
	deliverAction(&m, actionKey(&m, "K"))
	actionKey(&m, "enter")
	m.live.input.SetValue("kill")
	actionKey(&m, "esc")
	if len(f.killed) != 0 || m.live.stage != "" {
		t.Fatal("escape sent a kill")
	}
}

func TestKillEmptyDeniedAndSmallTerminal(t *testing.T) {
	m, f := queryActionModel()
	f.items = nil
	deliverAction(&m, actionKey(&m, "K"))
	if actionKey(&m, "enter") != nil || len(f.killed) != 0 {
		t.Fatal("empty picker killed something")
	}
	m, f = queryActionModel()
	deliverAction(&m, actionKey(&m, "K"))
	actionKey(&m, "enter")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 52, Height: 18})
	m = updated.(Model)
	if !strings.Contains(ansi.Strip(m.View()), "Type kill:") {
		t.Fatal("confirmation hidden at 52x18: " + m.View())
	}
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m = updated.(Model)
	m.live.input.SetValue("kill")
	if actionKey(&m, "enter") != nil || len(f.killed) != 0 {
		t.Fatal("hidden confirmation dispatched")
	}
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	f.err = errors.New("permission denied")
	deliverAction(&m, actionKey(&m, "enter"))
	if !strings.Contains(m.View(), "permission denied") || strings.Contains(m.View(), "accepted") {
		t.Fatal(m.View())
	}
}
