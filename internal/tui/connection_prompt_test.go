package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/muesli/termenv"
)

func TestConnectionPromptAcceptsTypedEndpointAndDefaultPort(t *testing.T) {
	t.Setenv("DBOPS_MYSQL_USER", "monitor")
	t.Setenv("DBOPS_MYSQL_PWD", "hidden-password")
	for _, tc := range []struct {
		input string
		port  int
	}{
		{"crafto-prod-db.chdaftiaemhd.ap-south-1.rds.example.com/db", 3306},
		{"db.example:3307/app", 3307},
	} {
		m := newConnectionPrompt()
		m.Init()
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.input)})
		m = next.(connectionPrompt)
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(connectionPrompt)
		if !m.accepted || cmd == nil || m.target.Port != tc.port {
			t.Fatalf("typed endpoint was not accepted: %v", m.err)
		}
		cfg, err := mysqlDriver.ParseDSN(m.target.DSN)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.User != "monitor" || cfg.Passwd != "hidden-password" {
			t.Fatal("shell credentials not used")
		}
		if strings.Contains(m.View(), "hidden-password") {
			t.Fatal("password leaked into prompt")
		}
	}
}

func TestConnectionPromptValidationAndCancel(t *testing.T) {
	t.Setenv("DBOPS_MYSQL_USER", "monitor")
	for _, value := range []string{"", "host:70000/app", "user:password@host/app"} {
		m := newConnectionPrompt()
		m.input.SetValue(value)
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(connectionPrompt)
		if m.accepted || cmd != nil || m.err == nil {
			t.Fatalf("invalid endpoint accepted: %q", value)
		}
	}
	m := newConnectionPrompt()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if next.(connectionPrompt).accepted || cmd == nil {
		t.Fatal("cancel did not exit without connecting")
	}
}

func TestConnectionPromptColoredCursorKeepsTextStable(t *testing.T) {
	profile := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Setenv("DBOPS_MYSQL_USER", "monitor")
	for _, width := range []int{52, 80, 140} {
		m := newConnectionPrompt()
		next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		m = next.(connectionPrompt)
		m.input.SetValue("db.example/app")
		m.input.Cursor.Blink = false
		visible := ansi.Strip(m.View())
		m.input.Cursor.Blink = true
		if visible != ansi.Strip(m.View()) || strings.Contains(visible, "[0m") || !strings.Contains(visible, "db.example/app") {
			t.Fatal("cursor blink corrupted endpoint input")
		}
		if lipgloss.Width(m.View()) > width {
			t.Fatalf("prompt exceeds terminal width %d", width)
		}
	}
}
