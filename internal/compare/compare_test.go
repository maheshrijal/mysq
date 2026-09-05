package compare

import (
	"testing"
	"time"

	"github.com/maheshrijal/mysq/internal/model"
)

func TestBuildFindsNewAndResolvedFindings(t *testing.T) {
	baseline := &model.Context{CollectedAt: time.Unix(0, 0), Health: model.Health{Score: 90}, Findings: []model.Finding{{ID: "old"}}}
	current := &model.Context{CollectedAt: time.Unix(60, 0), Health: model.Health{Score: 80, Subsystems: []model.SubsystemHealth{{Name: "", Complete: true}}}, Findings: []model.Finding{{ID: "new"}}}
	report := Build(baseline, current)
	if report.HealthScoreDelta != -10 || len(report.NewFindings) != 1 || len(report.ResolvedFindings) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestQueryDeltaScopesSchemasAndRejectsReset(t *testing.T) {
	base := &model.Context{CollectedAt: time.Unix(100, 0), Queries: []model.Query{{Schema: "a", Digest: "same", Calls: 100, TotalLatencyMillis: 100}, {Schema: "b", Digest: "same", Calls: 200}}}
	now := &model.Context{CollectedAt: time.Unix(120, 0), Queries: []model.Query{{Schema: "a", Digest: "same", Calls: 110, TotalLatencyMillis: 140}, {Schema: "b", Digest: "same", Calls: 1}}}
	report := Build(base, now)
	if len(report.Queries) != 1 || report.Queries[0].Schema != "a" || report.Queries[0].CallsDelta != 10 || report.Queries[0].IntervalMeanMillis == nil || *report.Queries[0].IntervalMeanMillis != 4 {
		t.Fatalf("%+v", report)
	}
	now.Fingerprint = "other"
	if Build(base, now).Comparable {
		t.Fatal("different target compared")
	}
}

func TestMissingCoverageDoesNotResolveFinding(t *testing.T) {
	base := &model.Context{CollectedAt: time.Unix(100, 0), Findings: []model.Finding{{ID: "replication_stopped", Subsystem: "replication"}}}
	now := &model.Context{CollectedAt: time.Unix(120, 0)}
	report := Build(base, now)
	if len(report.ResolvedFindings) != 0 || len(report.InconclusiveFindings) != 1 {
		t.Fatalf("%+v", report)
	}
}

func TestRestartOmitsCumulativeDeltasEvenIfCountersGrew(t *testing.T) {
	base := &model.Context{CollectedAt: time.Unix(100, 0), Server: model.Server{UptimeSeconds: 10}, Queries: []model.Query{{Digest: "a", Calls: 1}}}
	now := &model.Context{CollectedAt: time.Unix(200, 0), Server: model.Server{UptimeSeconds: 50}, Queries: []model.Query{{Digest: "a", Calls: 100}}}
	if report := Build(base, now); len(report.Queries) != 0 {
		t.Fatalf("%+v", report)
	}
}
