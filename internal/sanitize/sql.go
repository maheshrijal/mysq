package sanitize

import (
	"github.com/charmbracelet/x/ansi"
	"strings"
	"unicode"
)

// SQL retains identifiers and operators, replacing literals and comments. Two
// lexical passes cover both MySQL backslash-escape modes; their redaction spans
// are combined so ambiguous or truncated input loses detail rather than values.
func SQL(value string) string {
	return strings.Join(strings.Fields(redact(value, true)), " ")
}

func redact(value string, numbers bool) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, ansi.Strip(value))
	masked := make([]bool, len(value))
	mark := func(start, end int) {
		for i := start; i < end; i++ {
			masked[i] = true
		}
	}
	for _, escapes := range []bool{true, false} {
		for i := 0; i < len(value); {
			start := i
			switch {
			case value[i] == '\'' || value[i] == '"':
				quote := value[i]
				i++
				for i < len(value) {
					if escapes && value[i] == '\\' {
						i++
						if i < len(value) {
							i++
						}
						continue
					}
					if value[i] == quote {
						i++
						if i < len(value) && value[i] == quote {
							i++
							continue
						}
						break
					}
					i++
				}
				mark(start, i)
			case value[i] == '`':
				i++
				for i < len(value) {
					if value[i] == '`' {
						i++
						if i < len(value) && value[i] == '`' {
							i++
							continue
						}
						break
					}
					i++
				}
			case value[i] == '#' || (strings.HasPrefix(value[i:], "--") && (i+2 == len(value) || value[i+2] <= ' ')):
				for i < len(value) && value[i] != '\n' {
					i++
				}
				mark(start, i)
			case strings.HasPrefix(value[i:], "/*"):
				i += 2
				for i < len(value) && !strings.HasPrefix(value[i:], "*/") {
					i++
				}
				if i < len(value) {
					i += 2
				}
				mark(start, i)
			case numbers && ((value[i] >= '0' && value[i] <= '9') || (value[i] == '.' && i+1 < len(value) && value[i+1] >= '0' && value[i+1] <= '9')) && (i == 0 || !identifierByte(value[i-1])):
				i++
				for i < len(value) {
					b := value[i]
					if identifierByte(b) || b == '.' {
						i++
						continue
					}
					if (b == '+' || b == '-') && (value[i-1] == 'e' || value[i-1] == 'E') {
						i++
						continue
					}
					break
				}
				mark(start, i)
			default:
				i++
			}
		}
	}
	var out strings.Builder
	for i := 0; i < len(value); {
		if !masked[i] {
			out.WriteByte(value[i])
			i++
			continue
		}
		out.WriteString("?")
		for i < len(value) && masked[i] {
			i++
		}
	}
	return out.String()
}

func identifierByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '$' || b >= 128
}

// Text preserves monitor counters but removes quoted values, comments, terminal
// controls, and InnoDB record dumps. Infrastructure identifiers remain sensitive.
func Text(value string) string {
	lines := strings.Split(redact(value, false), "\n")
	for i, line := range lines {
		upper := strings.ToUpper(strings.TrimSpace(line))
		if strings.Contains(upper, "PHYSICAL RECORD") || strings.Contains(upper, "; HEX ") || strings.Contains(upper, "; ASC ") {
			lines[i] = "[record data omitted]"
			continue
		}
		for _, prefix := range []string{"SELECT ", "INSERT ", "UPDATE ", "DELETE ", "REPLACE ", "WITH ", "CALL ", "EXPLAIN ", "SET "} {
			if strings.HasPrefix(upper, prefix) {
				lines[i] = SQL(line)
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

// SensitiveName identifies server variables that may contain credentials.
func SensitiveName(name string) bool {
	name = strings.ToLower(name)
	for _, marker := range []string{"password", "passwd", "credential", "secret", "access_token", "api_token"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

// TerminalSQL preserves SQL literals and comments for an interactive display,
// while removing ANSI escapes and terminal control characters.
func TerminalSQL(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, ansi.Strip(value))
}
