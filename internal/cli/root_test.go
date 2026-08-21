package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/maheshrijal/mysqldot/internal/model"
)

func TestInitPrintsSQLButDoesNotAcceptUnsafeUsername(t *testing.T) {
	var out bytes.Buffer
	command := newRoot("test", &out, &out)
	command.SetArgs([]string{"init", "--user", "observer"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "CREATE USER") || !strings.Contains(out.String(), "GRANT PROCESS") {
		t.Fatalf("unexpected init output: %s", out.String())
	}
	command = newRoot("test", &out, &out)
	command.SetArgs([]string{"init", "--user", "bad'user"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "letters") {
		t.Fatalf("expected safe username error, got %v", err)
	}
}

func TestFailCode(t *testing.T) {
	// Kept in this package so exit-code semantics remain a tested contract.
	if got := failCode(testHealth(1, 0, 0), "critical"); got != 2 {
		t.Fatalf("critical code=%d", got)
	}
	if got := failCode(testHealth(0, 1, 0), "warning"); got != 1 {
		t.Fatalf("warning code=%d", got)
	}
}

func testHealth(critical, warnings, notes int) *model.Context {
	return &model.Context{Health: model.Health{Critical: critical, Warnings: warnings, Notes: notes}}
}
