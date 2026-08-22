package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/maheshrijal/mysq/internal/model"
)

func TestTextRendersFindingsFirstWithoutANSI(t *testing.T) {
	ctx := &model.Context{
		SchemaVersion: "1.0.0", Fingerprint: "abc", Server: model.Server{Host: "127.0.0.1", Port: 3306, Database: "app", Flavor: "MySQL", Version: "8.4.0"},
		Health:   model.Health{Score: 80, Warnings: 1},
		Metrics:  model.Metrics{ConnectionsCurrent: 2, ConnectionsMax: 100, BufferPoolHitPercent: 99.9},
		Findings: []model.Finding{{Severity: model.SeverityWarning, Title: "Watch this", Summary: "Evidence."}},
	}
	var out bytes.Buffer
	if err := Text(&out, ctx, Options{Width: 96}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"mysq", "Database health", "WARNING", "Watch this", "Agent bundle"} {
		if !strings.Contains(out.String(), expected) {
			t.Fatalf("output missing %q:\n%s", expected, out.String())
		}
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Fatal("color-disabled output contains ANSI escape")
	}
}
