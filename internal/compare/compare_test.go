package compare

import (
	"testing"
	"time"

	"github.com/maheshrijal/mysq/internal/model"
)

func TestBuildFindsNewAndResolvedFindings(t *testing.T) {
	baseline := &model.Context{CollectedAt: time.Unix(0, 0), Health: model.Health{Score: 90}, Findings: []model.Finding{{ID: "old"}}}
	current := &model.Context{CollectedAt: time.Unix(60, 0), Health: model.Health{Score: 80}, Findings: []model.Finding{{ID: "new"}}}
	report := Build(baseline, current)
	if report.HealthScoreDelta != -10 || len(report.NewFindings) != 1 || len(report.ResolvedFindings) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
}
