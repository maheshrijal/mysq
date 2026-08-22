package compare

import (
	"fmt"
	"sort"
	"time"

	"github.com/maheshrijal/mysq/internal/model"
)

type Report struct {
	Fingerprint      string          `json:"fingerprint"`
	BaselineAt       time.Time       `json:"baseline_at"`
	CurrentAt        time.Time       `json:"current_at"`
	ElapsedSeconds   float64         `json:"elapsed_seconds"`
	HealthScoreDelta int             `json:"health_score_delta"`
	Metrics          []MetricDelta   `json:"metrics"`
	NewFindings      []model.Finding `json:"new_findings"`
	ResolvedFindings []model.Finding `json:"resolved_findings"`
	Queries          []QueryDelta    `json:"query_deltas"`
}

type MetricDelta struct {
	Name     string  `json:"name"`
	Baseline float64 `json:"baseline"`
	Current  float64 `json:"current"`
	Delta    float64 `json:"delta"`
	Unit     string  `json:"unit"`
}

type QueryDelta struct {
	Digest             string  `json:"digest"`
	Statement          string  `json:"statement"`
	CallsDelta         int64   `json:"calls_delta"`
	LatencyMillisDelta float64 `json:"latency_ms_delta"`
	MeanMillisDelta    float64 `json:"mean_ms_delta"`
}

func Build(baseline, current *model.Context) Report {
	report := Report{
		Fingerprint: current.Fingerprint, BaselineAt: baseline.CollectedAt, CurrentAt: current.CollectedAt,
		ElapsedSeconds:   current.CollectedAt.Sub(baseline.CollectedAt).Seconds(),
		HealthScoreDelta: current.Health.Score - baseline.Health.Score,
	}
	metrics := []struct {
		name, unit string
		a, b       float64
	}{
		{"queries_per_second", "qps", baseline.Metrics.QueriesPerSecond, current.Metrics.QueriesPerSecond},
		{"transactions_per_second", "tps", baseline.Metrics.TransactionsPerSecond, current.Metrics.TransactionsPerSecond},
		{"connections_used", "%", baseline.Metrics.ConnectionsUsedPercent, current.Metrics.ConnectionsUsedPercent},
		{"buffer_pool_hit", "%", baseline.Metrics.BufferPoolHitPercent, current.Metrics.BufferPoolHitPercent},
		{"temporary_tables_on_disk", "%", baseline.Metrics.TempDiskTablePercent, current.Metrics.TempDiskTablePercent},
		{"row_lock_waits", "waits/s", baseline.Metrics.RowLockWaitsPerSecond, current.Metrics.RowLockWaitsPerSecond},
	}
	for _, metric := range metrics {
		report.Metrics = append(report.Metrics, MetricDelta{Name: metric.name, Baseline: metric.a, Current: metric.b, Delta: metric.b - metric.a, Unit: metric.unit})
	}

	baseFindings := map[string]model.Finding{}
	currentFindings := map[string]model.Finding{}
	for _, finding := range baseline.Findings {
		baseFindings[finding.ID] = finding
	}
	for _, finding := range current.Findings {
		currentFindings[finding.ID] = finding
		if _, ok := baseFindings[finding.ID]; !ok {
			report.NewFindings = append(report.NewFindings, finding)
		}
	}
	for _, finding := range baseline.Findings {
		if _, ok := currentFindings[finding.ID]; !ok {
			report.ResolvedFindings = append(report.ResolvedFindings, finding)
		}
	}

	baseQueries := map[string]model.Query{}
	for _, query := range baseline.Queries {
		baseQueries[query.Digest] = query
	}
	for _, query := range current.Queries {
		previous, ok := baseQueries[query.Digest]
		if !ok {
			continue
		}
		report.Queries = append(report.Queries, QueryDelta{
			Digest: query.Digest, Statement: query.Statement,
			CallsDelta:         int64(query.Calls) - int64(previous.Calls),
			LatencyMillisDelta: query.TotalLatencyMillis - previous.TotalLatencyMillis,
			MeanMillisDelta:    query.MeanLatencyMillis - previous.MeanLatencyMillis,
		})
	}
	sort.Slice(report.Queries, func(i, j int) bool {
		return report.Queries[i].LatencyMillisDelta > report.Queries[j].LatencyMillisDelta
	})
	if len(report.Queries) > 20 {
		report.Queries = report.Queries[:20]
	}
	return report
}

func Text(report Report) string {
	result := fmt.Sprintf("mysq diff · %s → %s · %.0fs\n\nHealth score: %+d\n",
		report.BaselineAt.Format(time.RFC3339), report.CurrentAt.Format(time.RFC3339), report.ElapsedSeconds, report.HealthScoreDelta)
	for _, metric := range report.Metrics {
		result += fmt.Sprintf("  %-28s %9.2f → %-9.2f  (%+.2f %s)\n", metric.Name, metric.Baseline, metric.Current, metric.Delta, metric.Unit)
	}
	if len(report.NewFindings) > 0 {
		result += "\nNew findings:\n"
		for _, finding := range report.NewFindings {
			result += fmt.Sprintf("  + [%s] %s\n", finding.Severity, finding.Title)
		}
	}
	if len(report.ResolvedFindings) > 0 {
		result += "\nResolved findings:\n"
		for _, finding := range report.ResolvedFindings {
			result += fmt.Sprintf("  - [%s] %s\n", finding.Severity, finding.Title)
		}
	}
	return result
}
