package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/maheshrijal/mysq/internal/model"
	"github.com/muesli/termenv"
)

func TestPrimaryTablesKeepSemanticColorsAndPlainLayout(t *testing.T) {
	profile := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	ctx := &model.Context{
		ConnectionGroups: []model.ConnectionGroup{{Kind: "user", Key: "alice", Total: 2, Active: 1}},
		Processes: []model.Process{{ID: 42, User: "alice", Host: "web-1", Seconds: 12,
			State: "Waiting for table metadata lock", Statement: "SELECT * FROM `accounts`"}},
		MetadataLocks: []model.MetadataLock{{User: "alice", Status: "PENDING", Schema: "app", Object: "accounts"}},
		Queries: []model.Query{
			{Statement: "SELECT ?", Calls: 1},
			{Statement: "UPDATE `accounts` SET balance = ?", ActiveUsers: []string{"alice"}, Calls: 12, TotalLatencyMillis: 5000},
		},
		WaitEvents: []model.WaitEvent{{Name: "wait/io/table/sql/handler", EventsPerSecond: 12, SampleSharePercent: 50}},
		Tables:     []model.Table{{Schema: "app", Name: "accounts", TotalBytes: 1024, EstimatedRows: 12}},
	}
	for _, width := range []int{48, 76, 100, 160} {
		for _, view := range []struct {
			name   string
			render func() string
			colors []string
		}{
			{"connections", func() string { return connections(ctx, width) }, []string{"36", "35", "33"}},
			{"queries", func() string { return queries(ctx, width, 0, 5000) }, []string{"36", "35", "34"}},
			{"engine", func() string { return engine(ctx, width) }, []string{"36", "35"}},
			{"tables", func() string { return tablesView(ctx, width) }, []string{"36", "35", "33"}},
		} {
			t.Run(fmt.Sprintf("%s/%d", view.name, width), func(t *testing.T) {
				lipgloss.SetColorProfile(termenv.Ascii)
				plain := view.render()
				lipgloss.SetColorProfile(termenv.TrueColor)
				colored := view.render()
				if ansi.Strip(colored) != plain {
					t.Fatal("color changed table content, truncation, or alignment")
				}
				for _, color := range view.colors {
					if !strings.Contains(colored, "\x1b["+color+"m") {
						t.Fatalf("missing semantic color %s", color)
					}
				}
			})
		}
	}
}

func TestKillConfirmationRemainsStableAcrossCursorFrames(t *testing.T) {
	profile := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	lipgloss.SetColorProfile(termenv.TrueColor)
	m, _ := queryActionModel()
	deliverAction(&m, actionKey(&m, "K"))
	actionKey(&m, "enter")
	for _, value := range []string{"", "k", "ki", "kil", "kill"} {
		m.live.input.SetValue(value)
		for pos := 0; pos <= len(value); pos++ {
			m.live.input.SetCursor(pos)
			var previous string
			for _, blink := range []bool{false, true} {
				m.live.input.Cursor.Blink = blink
				m.rebuild()
				frame := ansi.Strip(m.View())
				if !strings.Contains(frame, "Type kill: "+value) || strings.Contains(frame, "[0m") || strings.Contains(frame, "[1;") {
					t.Fatalf("corrupted confirmation for %q at cursor %d, blink %t:\n%s", value, pos, blink, frame)
				}
				if previous != "" && previous != frame {
					t.Fatal("cursor blink changed visible text or layout")
				}
				previous = frame
			}
		}
	}
}

// Ghostty changes its default colors and ANSI palette at runtime. The same
// frame must remain valid even if Lip Gloss cached the opposite appearance.
func TestTerminalPaletteDoesNotDependOnCachedAppearance(t *testing.T) {
	profile, dark := lipgloss.ColorProfile(), lipgloss.HasDarkBackground()
	t.Cleanup(func() { lipgloss.SetColorProfile(profile); lipgloss.SetHasDarkBackground(dark) })
	lipgloss.SetColorProfile(termenv.TrueColor)
	m, _ := queryActionModel()
	deliverAction(&m, actionKey(&m, "enter"))
	check := func() {
		t.Helper()
		lipgloss.SetHasDarkBackground(false)
		m.rebuild()
		light := m.View()
		lipgloss.SetHasDarkBackground(true)
		m.rebuild()
		if light != m.View() {
			t.Fatal("cached appearance changed the terminal-native frame")
		}
		if strings.Contains(light, "38;2;") || strings.Contains(light, "48;2;") {
			t.Fatal("fixed RGB color bypasses Ghostty's theme")
		}
	}
	check()
	// Distinct roles: SQL keywords/navigation blue; identifiers cyan; metrics magenta.
	frame := m.View()
	for _, role := range []string{"\x1b[34m", "\x1b[36m", "\x1b[35m"} {
		if !strings.Contains(frame, role) {
			t.Fatalf("missing semantic color %q", role)
		}
	}
	deliverAction(&m, actionKey(&m, "K"))
	check()
	actionKey(&m, "enter")
	check()
	m.live.stage, m.queryDetail = "", false
	for tab := 0; tab < totalViews; tab++ {
		m.tab = tab
		check()
	}
	// The plain body foreground and selection follow the terminal defaults.
	if got := lipgloss.NewStyle().Foreground(text).Render("body"); got != "body" {
		t.Fatalf("body overrides native foreground: %q", got)
	}
}
