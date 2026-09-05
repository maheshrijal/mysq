package tui

import "github.com/charmbracelet/bubbles/key"

type navigationKeyMap struct {
	NextView     key.Binding
	PreviousView key.Binding
	Up           key.Binding
	Down         key.Binding
	PageUp       key.Binding
	PageDown     key.Binding
	HalfPageUp   key.Binding
	HalfPageDown key.Binding
	Top          key.Binding
	Bottom       key.Binding
	Open         key.Binding
	Back         key.Binding
	Filter       key.Binding
	Blockers     key.Binding
	KillQuery    key.Binding
	Refresh      key.Binding
	PauseTrends  key.Binding
	Export       key.Binding
	Help         key.Binding
	Quit         key.Binding
	Jump         key.Binding
}

func defaultNavigationKeyMap() navigationKeyMap {
	return navigationKeyMap{
		NextView: key.NewBinding(
			key.WithKeys("tab", "right", "l"),
			key.WithHelp("tab/→/l", "next view"),
		),
		PreviousView: key.NewBinding(
			key.WithKeys("shift+tab", "left", "h"),
			key.WithHelp("shift-tab/←/h", "previous view"),
		),
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("pgup", "b"),
			key.WithHelp("pgup/b", "page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("pgdown", " ", "f"),
			key.WithHelp("pgdn/space/f", "page down"),
		),
		HalfPageUp: key.NewBinding(
			key.WithKeys("ctrl+u", "u"),
			key.WithHelp("ctrl-u/u", "half page up"),
		),
		HalfPageDown: key.NewBinding(
			key.WithKeys("ctrl+d", "d"),
			key.WithHelp("ctrl-d/d", "half page down"),
		),
		Top: key.NewBinding(
			key.WithKeys("home", "g"),
			key.WithHelp("home/g", "first entry"),
		),
		Bottom: key.NewBinding(
			key.WithKeys("end", "G"),
			key.WithHelp("end/G", "last entry"),
		),
		Open: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "open"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back / clear filter"),
		),
		Filter: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "filter current view"),
		),
		Blockers:    key.NewBinding(key.WithKeys("B"), key.WithHelp("B", "blocking chains")),
		KillQuery:   key.NewBinding(key.WithKeys("K"), key.WithHelp("K", "kill query…")),
		PauseTrends: key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "pause/resume trends")),
		Refresh: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "refresh"),
		),
		Export: key.NewBinding(
			key.WithKeys("e"),
			key.WithHelp("e", "export bundle"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "keyboard help"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q/ctrl-c", "quit"),
		),
		Jump: key.NewBinding(
			key.WithKeys("1", "2", "3", "4", "5", "6", "7"),
			key.WithHelp("1–7", "jump to view"),
		),
	}
}

type contextualHelp struct {
	short []key.Binding
	full  [][]key.Binding
}

func (h contextualHelp) ShortHelp() []key.Binding  { return h.short }
func (h contextualHelp) FullHelp() [][]key.Binding { return h.full }
