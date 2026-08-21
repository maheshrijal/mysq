package analyze

import (
	"testing"

	"github.com/maheshrijal/mysqldot/internal/model"
)

func TestApplyFindsCriticalConnectionPressureAndMissingPrimaryKey(t *testing.T) {
	ctx := &model.Context{
		Server:    model.Server{PerformanceSchema: true},
		Metrics:   model.Metrics{ConnectionsCurrent: 95, ConnectionsMax: 100, ConnectionsUsedPercent: 95, BufferPoolHitPercent: 100},
		Variables: map[string]string{"innodb_flush_log_at_trx_commit": "1", "log_bin": "OFF", "skip_name_resolve": "ON"},
		Tables:    []model.Table{{Schema: "app", Name: "events", HasPrimaryKey: false}},
	}
	Apply(ctx)
	if ctx.Health.Critical != 1 {
		t.Fatalf("critical = %d, want 1; findings=%+v", ctx.Health.Critical, ctx.Findings)
	}
	if ctx.Findings[0].ID != "connection_saturation" {
		t.Fatalf("first finding = %q, want connection_saturation", ctx.Findings[0].ID)
	}
	foundPK := false
	for _, finding := range ctx.Findings {
		foundPK = foundPK || finding.ID == "tables_without_primary_key"
	}
	if !foundPK {
		t.Fatal("expected tables_without_primary_key finding")
	}
}

func TestApplyTreatsUnusedIndexesAsReviewOnly(t *testing.T) {
	ctx := &model.Context{
		Server:    model.Server{PerformanceSchema: true, UptimeSeconds: 3600},
		Metrics:   model.Metrics{BufferPoolHitPercent: 100},
		Variables: map[string]string{"innodb_flush_log_at_trx_commit": "1", "skip_name_resolve": "ON"},
		Indexes:   []model.Index{{Schema: "app", Table: "orders", Name: "idx_status", Reads: 0, Writes: 20}},
	}
	Apply(ctx)
	for _, finding := range ctx.Findings {
		if finding.ID == "unused_indexes" && finding.Severity != model.SeverityNote {
			t.Fatalf("unused index severity = %s, want note", finding.Severity)
		}
	}
}
