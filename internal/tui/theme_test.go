package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

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
