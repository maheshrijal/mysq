package sanitize

import (
	"regexp"
	"strings"
)

var (
	singleQuoted = regexp.MustCompile(`'(?:''|\\.|[^'])*'`)
	doubleQuoted = regexp.MustCompile(`"(?:""|\\.|[^"])*"`)
	numbers      = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
	spaces       = regexp.MustCompile(`\s+`)
)

// SQL removes literal values while preserving enough statement shape for diagnosis.
func SQL(value string) string {
	value = singleQuoted.ReplaceAllString(value, "?")
	value = doubleQuoted.ReplaceAllString(value, "?")
	value = numbers.ReplaceAllString(value, "?")
	return strings.TrimSpace(spaces.ReplaceAllString(value, " "))
}

// Text applies the same conservative literal redaction to diagnostic text.
func Text(value string) string {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		// Diagnostic errors can embed values even when they are not themselves SQL
		// statements (for example duplicate-entry replication failures).
		line = singleQuoted.ReplaceAllString(line, "?")
		line = doubleQuoted.ReplaceAllString(line, "?")
		upper := strings.ToUpper(strings.TrimSpace(line))
		if strings.HasPrefix(upper, "SELECT ") || strings.HasPrefix(upper, "INSERT ") ||
			strings.HasPrefix(upper, "UPDATE ") || strings.HasPrefix(upper, "DELETE ") ||
			strings.HasPrefix(upper, "REPLACE ") {
			line = SQL(line)
		}
		lines[i] = line
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
