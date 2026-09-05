package analyze

import (
	"strings"

	"github.com/maheshrijal/mysq/internal/model"
)

func assessCoverage(ctx *model.Context) {
	requirements := map[string][]string{
		"connections":      {"global variables"},
		"workload":         {"process list", "statement counters"},
		"queries":          {"statement digests", "statement database time", "instrumentation coverage"},
		"indexes":          {"index statistics", "instrumentation coverage"},
		"tables":           {"table statistics"},
		"locks":            {"row lock waits", "active transactions", "metadata locks"},
		"buffer pool":      {"InnoDB monitor"},
		"temporary tables": {},
		"replication":      {"replication"},
		"durability":       {"global variables"},
		"instrumentation":  {"instrumentation coverage"},
	}
	capabilities := map[string]model.Capability{}
	for _, capability := range ctx.Capabilities {
		capabilities[capability.Name] = capability
	}
	ctx.Health.Healthy = 0
	for _, name := range subsystems {
		assessment := model.SubsystemHealth{Name: name, Status: "ok", Complete: true}
		gaps := []string{}
		for _, probe := range requirements[name] {
			capability, found := capabilities[probe]
			if !found {
				gaps = append(gaps, probe+": not recorded")
			} else if !capability.Available {
				gaps = append(gaps, probe+": "+capability.Reason)
			}
		}
		switch name {
		case "connections", "workload", "buffer pool", "temporary tables":
			if len(ctx.GlobalStatus) == 0 {
				gaps = append(gaps, "global status: not recorded")
			}
		}
		for _, consumer := range ctx.Instrumentation.DisabledConsumers {
			if consumer == "global_instrumentation" || consumer == "thread_instrumentation" ||
				(consumer == "statements_digest" && name == "queries") ||
				((consumer == "events_statements_current" || consumer == "events_waits_current") && name == "workload") {
				if name != "connections" && name != "durability" && name != "temporary tables" {
					gaps = append(gaps, consumer+": disabled")
				}
			}
		}
		switch name {
		case "tables":
			if len(ctx.Tables) == 0 {
				gaps = append(gaps, "no visible tables; an empty catalog cannot establish object visibility")
			}
			if len(ctx.Tables) >= 100 {
				gaps = append(gaps, "table collection reached its 100-row limit")
			}
		case "indexes":
			if len(ctx.Indexes) == 0 {
				gaps = append(gaps, "no visible index definitions; usage cannot be assessed")
			}
		case "queries":
			if len(ctx.Queries) == 0 {
				gaps = append(gaps, "no captured statement digests; workload cannot be assessed")
			}
		case "workload":
			if len(ctx.Processes) >= 100 {
				gaps = append(gaps, "process collection reached its 100-row limit")
			}
		case "locks":
			if len(ctx.Transactions) >= 100 || len(ctx.MetadataLocks) >= 100 {
				gaps = append(gaps, "transaction or metadata-lock collection reached its 100-row limit")
			}
		}
		if name == "queries" && ctx.Instrumentation.TotalLost > 0 {
			gaps = append(gaps, "Performance Schema lost events; attribution may be incomplete")
		}
		if len(gaps) > 0 {
			assessment.Status = "unknown"
			assessment.Complete = false
			assessment.Reason = strings.Join(gaps, "; ")
			ctx.Health.Unknown++
		}
		if assessment.Complete && name == "replication" && ctx.Replication == nil {
			assessment.Status = "not_applicable"
			assessment.Reason = "No asynchronous replication channel reported"
		}
		for _, finding := range ctx.Findings {
			if finding.Subsystem != name {
				continue
			}
			if finding.Severity == model.SeverityCritical {
				assessment.Status = "fail"
				break
			}
			if finding.Severity == model.SeverityWarning {
				assessment.Status = "warn"
			} else if assessment.Status == "ok" {
				assessment.Status = "note"
			}
		}
		if assessment.Status == "ok" {
			ctx.Health.Healthy++
		}
		ctx.Health.Subsystems = append(ctx.Health.Subsystems, assessment)
	}
}
