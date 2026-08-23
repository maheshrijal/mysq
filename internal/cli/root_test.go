package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maheshrijal/mysq/internal/model"
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

func TestFocusedCommandMapsCollectionFailureToExitThree(t *testing.T) {
	var out bytes.Buffer
	probeErr := errors.New("invalid connection")
	app := &App{
		Out: &out,
		Err: &out,
		inspectSectionFn: func(context.Context, string, time.Duration, string) (*model.Context, error) {
			return nil, probeErr
		},
	}
	command := app.focusedCommand("tables")
	command.SetArgs([]string{"--json"})
	err := command.Execute()
	var exit ExitError
	if !errors.As(err, &exit) || exit.Code != 3 || !errors.Is(err, probeErr) {
		t.Fatalf("focused collection error = %v, want wrapped exit 3", err)
	}
	if out.Len() != 0 {
		t.Fatalf("failed focused command emitted output: %q", out.String())
	}
}

func TestFocusedReplicationRendersSuccessfulNonReplica(t *testing.T) {
	var out bytes.Buffer
	app := &App{
		Out: &out,
		Err: &out,
		inspectSectionFn: func(context.Context, string, time.Duration, string) (*model.Context, error) {
			return &model.Context{Replication: nil}, nil
		},
	}
	command := app.focusedCommand("replication")
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "not configured as a replica") {
		t.Fatalf("unexpected successful non-replica output: %q", out.String())
	}
}
