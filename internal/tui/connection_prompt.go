package tui

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/maheshrijal/mysq/internal/collect"
	"github.com/maheshrijal/mysq/internal/sanitize"
)

// PromptConnection collects only the endpoint. Credentials stay in environment
// variables, and validation does not open a database connection.
func PromptConnection(ctx context.Context) (collect.Target, bool, error) {
	if !term.IsTerminal(os.Stdin.Fd()) {
		return collect.Target{}, false, errors.New("no MySQL endpoint supplied: run mysq tui host[:port]/database or set MYSQ_DATABASE_URL")
	}
	program := tea.NewProgram(newConnectionPrompt(), tea.WithContext(ctx), tea.WithAltScreen())
	result, err := program.Run()
	if err != nil {
		return collect.Target{}, false, err
	}
	prompt := result.(connectionPrompt)
	return prompt.target, prompt.accepted, nil
}

type connectionPrompt struct {
	input    textinput.Model
	width    int
	target   collect.Target
	accepted bool
	err      error
}

func newConnectionPrompt() connectionPrompt {
	input := textinput.New()
	input.Prompt = "Host/database › "
	input.Placeholder = "db.example.com/app"
	input.CharLimit = 1024
	input.Width = 52
	input.PromptStyle = lipgloss.NewStyle().Foreground(cyan)
	input.TextStyle = lipgloss.NewStyle().Foreground(text)
	input.PlaceholderStyle = lipgloss.NewStyle().Foreground(text)
	input.Cursor.Style = lipgloss.NewStyle().Foreground(cyan)
	input.Focus()
	return connectionPrompt{input: input, width: 80}
}

func (m connectionPrompt) Init() tea.Cmd { return textinput.Blink }
func (m connectionPrompt) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width = max(24, msg.Width)
		m.input.Width = max(6, min(80, m.width-22))
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "ctrl+c":
			return m, tea.Quit
		case "enter":
			endpoint := strings.TrimSpace(m.input.Value())
			if endpoint == "" {
				m.err = errors.New("enter a host/database, for example db.example.com/app")
				return m, nil
			}
			// This form never asks users to type credentials into a visible field.
			if strings.ContainsAny(endpoint, "@()") {
				m.err = errors.New("enter only host[:port]/database; credentials come from DBOPS_MYSQL_USER and DBOPS_MYSQL_PWD")
				return m, nil
			}
			target, err := collect.ResolveConnection(endpoint)
			if err != nil {
				m.err = err
				return m, nil
			}
			m.target, m.accepted = target, true
			return m, tea.Quit
		}
		m.err = nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(message)
	return m, cmd
}
func (m connectionPrompt) View() string {
	width := max(20, m.width-4)
	body := sectionTitle("CONNECT TO MYSQL") + "\n\n"
	if user := os.Getenv("DBOPS_MYSQL_USER"); user != "" {
		body += "User " + lipgloss.NewStyle().Foreground(identity).Render(sanitize.Text(user)) + " · credentials from your shell\n"
	} else {
		body += "Export DBOPS_MYSQL_USER and DBOPS_MYSQL_PWD before connecting.\n"
	}
	body += "Enter host[:port]/database. Port defaults to 3306.\n\n" + m.input.View() + "\n\n"
	if m.err != nil {
		body += lipgloss.NewStyle().Foreground(yellow).Width(width).Render(sanitize.Text(m.err.Error())) + "\n\n"
	}
	body += keyHint("Enter", "connect") + "   " + keyHint("Esc", "cancel")
	return lipgloss.NewStyle().Padding(1, 2).Width(max(24, m.width)).Render(body)
}
