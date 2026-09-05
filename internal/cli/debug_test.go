package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebugLogPreservesOutputAndCapturesErrors(t *testing.T) {
	for _, args := range [][]string{{"init", "--user", "observer"}, {"inspect", "--format", "invalid"}} {
		var plain, logged bytes.Buffer
		command := newRoot("test", &plain, &plain)
		command.SetArgs(args)
		plainErr := command.Execute()
		path := filepath.Join(t.TempDir(), "debug.jsonl")
		command = newRoot("test", &logged, &logged)
		command.SetArgs(append(append([]string{}, args...), "--debug-log", path))
		loggedErr := command.Execute()
		if (plainErr == nil) != (loggedErr == nil) || plain.String() != logged.String() {
			t.Fatal("debug logging changed command behavior")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) != 4 {
			t.Fatalf("missing lifecycle events: %s", data)
		}
		for _, line := range lines {
			if !json.Valid([]byte(line)) {
				t.Fatalf("invalid JSON: %s", line)
			}
		}
		want := `"status":"ok"`
		if plainErr != nil {
			want = `"status":"error"`
		}
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing result: %s", data)
		}
		command = newRoot("test", &logged, &logged)
		command.SetArgs([]string{"init", "--debug-log", path})
		before := logged.String()
		if err := command.Execute(); err == nil {
			t.Fatal("existing log accepted")
		}
		if logged.String() != before {
			t.Fatal("command ran despite unavailable log")
		}
	}
}
