package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Color columns by meaning, after truncating their plain text. This keeps
// compact and wide layouts consistent without inserting ANSI into source data.
func semanticRow(values, headings []string, widths []int, selected bool) string {
	if selected {
		// Native reverse video keeps every cell legible in either terminal theme.
		return selectableRow(values, widths, true)
	}
	var out strings.Builder
	for i, value := range values {
		visible := compact(strings.ReplaceAll(value, "\n", " "), widths[i]-1)
		color := tableCellColor(headings[i], value)
		if headings[0] == "GROUP" && headings[i] == "VALUE" {
			color = identity
		}
		if headings[i] == "STATEMENT" || headings[i] == "QUERY" {
			visible = highlightedSQL(visible)
		}
		out.WriteString(lipgloss.NewStyle().Foreground(color).Width(widths[i]).MaxWidth(widths[i]).Render(visible))
	}
	return out.String()
}

func tableCellColor(heading, value string) lipgloss.TerminalColor {
	switch heading {
	case "ID", "TRX", "USER", "ACTIVE USERS", "HOST", "TABLE", "OBJECT", "INDEX AND COLUMNS", "EVENT", "FILE INSTRUMENT", "CONSUMER", "NAME":
		return identity
	case "DB TIME", "CALLS", "P95", "ROWS EXAM", "TIME", "AGE", "TOTAL", "ACTIVE", "SLEEP", "OTHER",
		"LOCKED", "MODIFIED", "SIZE", "ROWS", "READS", "WRITES", "READ TIME", "WRITE TIME", "CARDINALITY",
		"VALUE", "RELATED", "SHARE", "WAIT/S", "EVENTS/S", "CUM TOTAL", "READ/S", "WRITE/S", "READ LAT", "WRITE LAT",
		"ERROR", "SAMPLE/S", "CURRENT", "HIGH WATER", "ALLOCATIONS":
		return number
	case "PK":
		if value == "NO" {
			return yellow
		}
		return green
	case "STATUS":
		if strings.EqualFold(value, "PENDING") {
			return yellow
		}
	case "FLAGS":
		if strings.Contains(value, "hidden") {
			return yellow
		}
	case "WAIT":
		state := strings.ToLower(value)
		if strings.Contains(state, "lock") && (strings.Contains(state, "wait") || strings.Contains(state, "pending")) {
			return yellow
		}
	}
	return text
}
