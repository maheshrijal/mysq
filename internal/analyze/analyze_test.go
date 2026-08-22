package analyze

import (
	"testing"

	"github.com/maheshrijal/mysq/internal/model"
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

func TestApplyIgnoresLongLivedServerDaemons(t *testing.T) {
	ctx := &model.Context{
		Server:    model.Server{PerformanceSchema: true},
		Metrics:   model.Metrics{BufferPoolHitPercent: 100},
		Variables: map[string]string{"innodb_flush_log_at_trx_commit": "1", "skip_name_resolve": "ON"},
		Processes: []model.Process{
			{ID: 5, User: "event_scheduler", Command: "Daemon", State: "Waiting on empty queue", Seconds: 21_600},
			{ID: 9, User: "app", Command: "Query", Statement: "SELECT SLEEP(?)", Seconds: 10},
		},
	}
	Apply(ctx)
	for _, finding := range ctx.Findings {
		if finding.ID != "long_running_statements" {
			continue
		}
		if finding.Severity != model.SeverityWarning {
			t.Fatalf("severity = %s, want warning; daemon must not make it critical", finding.Severity)
		}
		if got := finding.Evidence["count"]; got != 1 {
			t.Fatalf("count = %v, want 1", got)
		}
		return
	}
	t.Fatal("expected long_running_statements finding for active query")
}
