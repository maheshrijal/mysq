package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maheshrijal/mysq/internal/model"
)

func TestValidateOutputRejectsEmptyNullAndWrongShapes(t *testing.T) {
	for _, test := range []struct {
		name, data string
	}{
		{name: "queries", data: ""},
		{name: "queries", data: "null"},
		{name: "queries", data: "[]"},
		{name: "queries", data: `[null]`},
		{name: "queries", data: `[{}]`},
		{name: "queries", data: `[1]`},
		{name: "tables", data: `[{}]`},
		{name: "waits", data: `[{}]`},
		{name: "variables", data: `{}`},
		{name: "engine", data: `{"connections_max":0,"redo_capacity_bytes":1}`},
		{name: "inspect-full", data: `{"schema_version":"1.4.0","queries":[],"tables":[]}`},
		{name: "inspect-full", data: `{"schema_version":123,"server":{"flavor":"MySQL","version":"8.4"},"queries":[{"digest":"abc","statement":"SELECT ?"}],"tables":[{"schema":"app","name":"orders","engine":"InnoDB"}]}`},
		{name: "inspect-full", data: `{"schema_version":"1.4.0","server":{"flavor":"MySQL","version":"8.4"},"queries":[{}],"tables":[{}]}`},
	} {
		t.Run(test.name+"_"+strings.ReplaceAll(test.data, " ", "_"), func(t *testing.T) {
			if err := validateOutput(test.name, []byte(test.data), benchmarkSampleInterval); err == nil {
				t.Fatalf("validateOutput(%q, %q) unexpectedly passed", test.name, test.data)
			}
		})
	}
}

func TestValidateOutputAcceptsFixtureShapes(t *testing.T) {
	for _, test := range []struct {
		name, data string
	}{
		{name: "queries", data: `[{"digest":"abc","statement":"SELECT ?"}]`},
		{name: "tables", data: `[{"schema":"app","name":"orders","engine":"InnoDB"}]`},
		{name: "waits", data: `[{"name":"wait/io/file","class":"io/file","sample_count":1,"events_per_second":10}]`},
		{name: "io", data: `[{"name":"file","class":"io/file","writes_per_second":1}]`},
		{name: "errors", data: `[{"number":1062,"name":"ER_DUP_ENTRY","sql_state":"23000","sample_raised":1}]`},
		{name: "variables", data: `{"performance_schema":"ON"}`},
		{name: "engine", data: `{"connections_max":151,"redo_capacity_bytes":1048576,"buffer_pool_data_bytes":4096,"queries_per_second":10,"transactions_per_second":2,"rows_written_per_second":1}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateOutput(test.name, []byte(test.data), benchmarkSampleInterval); err != nil {
				t.Fatalf("validateOutput(%q): %v", test.name, err)
			}
		})
	}
	if err := validateOutput("inspect-full", marshalJSON(t, validFullContext()), benchmarkSampleInterval); err != nil {
		t.Fatalf("validateOutput(inspect-full): %v", err)
	}
}

func TestValidateOutputRejectsIncompleteFullInspection(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*model.Context)
	}{
		{name: "short interval marker", mutate: func(context *model.Context) { context.IntervalMillis = 99 }},
		{name: "missing family interval", mutate: func(context *model.Context) { context.SampleIntervals.WaitEvents = 0 }},
		{name: "indexes", mutate: func(context *model.Context) { context.Indexes = nil }},
		{name: "locks collection", mutate: func(context *model.Context) { context.Locks = nil }},
		{name: "file io", mutate: func(context *model.Context) { context.FileIO = nil }},
		{name: "engine capacity", mutate: func(context *model.Context) { context.Metrics.BufferPoolDataBytes = 0 }},
		{name: "instrumentation", mutate: func(context *model.Context) { context.Instrumentation.DigestCapacity = 0 }},
		{name: "unavailable capability with reason", mutate: func(context *model.Context) {
			context.Capabilities[0].Available = false
			context.Capabilities[0].Reason = "fixture probe failed"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			context := validFullContext()
			test.mutate(&context)
			if err := validateOutput("inspect-full", marshalJSON(t, context), benchmarkSampleInterval); err == nil {
				t.Fatal("incomplete full inspection unexpectedly passed")
			}
		})
	}
}

func TestValidateOutputAllowsEmptySampleResultsWhenProbesSucceeded(t *testing.T) {
	context := validFullContext()
	context.StatementSamples = nil
	context.StatementLatency = model.StatementLatency{}
	if err := validateOutput("inspect-full", marshalJSON(t, context), benchmarkSampleInterval); err != nil {
		t.Fatalf("legitimate empty sample window was rejected: %v", err)
	}
}

func TestDerivedSampleEvidenceDistinguishesQuietAndActiveWindows(t *testing.T) {
	quietWaits := []byte(`[{"name":"wait/io/file","class":"io/file","count":100,"total_latency_ms":25}]`)
	activeWaits := []byte(`[{"name":"wait/io/file","class":"io/file","sample_count":1}]`)
	quietEngine := []byte(`{"connections_max":151,"redo_capacity_bytes":1048576,"buffer_pool_data_bytes":4096}`)
	activeEngine := []byte(`{"connections_max":151,"redo_capacity_bytes":1048576,"buffer_pool_data_bytes":4096,"network_out_bytes_per_second":1}`)
	quietIO := []byte(`[{"name":"file","class":"io/file"}]`)
	activeIO := []byte(`[{"name":"file","class":"io/file","writes_per_second":1}]`)
	quietErrors := []byte(`[{"number":1062,"name":"ER_DUP_ENTRY","sql_state":"23000"}]`)
	activeErrors := []byte(`[{"number":1062,"name":"ER_DUP_ENTRY","sql_state":"23000","sample_raised":1}]`)
	if derivedSampleEvidence("waits", quietWaits) || !derivedSampleEvidence("waits", activeWaits) {
		t.Fatal("wait sample evidence classification is wrong")
	}
	if derivedSampleEvidence("engine", quietEngine) || !derivedSampleEvidence("engine", activeEngine) {
		t.Fatal("engine sample evidence classification is wrong")
	}
	if derivedSampleEvidence("io", quietIO) || !derivedSampleEvidence("io", activeIO) {
		t.Fatal("file I/O sample evidence classification is wrong")
	}
	if derivedSampleEvidence("errors", quietErrors) || !derivedSampleEvidence("errors", activeErrors) {
		t.Fatal("server error sample evidence classification is wrong")
	}
	if err := requireSampleEvidence("candidate waits", false, 20); err == nil {
		t.Fatal("a measured run with no derived wait evidence unexpectedly passed")
	}
	if err := requireSampleEvidence("candidate waits", true, 20); err != nil {
		t.Fatalf("a measured run with derived wait evidence failed: %v", err)
	}
}

func TestValidateOutputEnforcesSampleDuration(t *testing.T) {
	waits := []byte(`[{"name":"wait/io/file","class":"io/file","sample_count":1}]`)
	fileIO := []byte(`[{"name":"file","class":"io/file","writes_per_second":1}]`)
	serverErrors := []byte(`[{"number":1062,"name":"ER_DUP_ENTRY","sql_state":"23000","sample_raised":1}]`)
	engine := []byte(`{"connections_max":151,"redo_capacity_bytes":1048576,"buffer_pool_data_bytes":4096,"queries_per_second":10,"transactions_per_second":2,"rows_written_per_second":1}`)
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "inspect-full", data: marshalJSON(t, validFullContext())},
		{name: "waits", data: waits},
		{name: "io", data: fileIO},
		{name: "errors", data: serverErrors},
		{name: "engine", data: engine},
	} {
		t.Run(test.name+" duration", func(t *testing.T) {
			if err := validateOutput(test.name, test.data, benchmarkSampleInterval-time.Nanosecond); err == nil {
				t.Fatal("short sample duration unexpectedly passed")
			}
		})
	}
}

func TestRunValidatesEveryInvocation(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "fake-mysq")
	state := filepath.Join(directory, "invoked")
	script := fmt.Sprintf(`#!/usr/bin/env bash
if [[ -f %q ]]; then
  printf '[]\n'
else
  : > %q
  printf '[{"digest":"abc","statement":"SELECT ?"}]\n'
fi
`, state, state)
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := run(binary, "unused", benchmarkCase{name: "queries"}, 0, 2); err == nil {
		t.Fatal("run accepted invalid evidence from a measured invocation")
	}
}

func TestSummarizeUsesRealP95OrderStatistic(t *testing.T) {
	samples := make([]time.Duration, 20)
	for index := range samples {
		samples[index] = time.Duration(index+1) * time.Millisecond
	}
	result := summarize("test", samples)
	if result.median != 10*time.Millisecond+500*time.Microsecond || result.p95 != 19*time.Millisecond || result.max != 20*time.Millisecond {
		t.Fatalf("median=%s p95=%s max=%s", result.median, result.p95, result.max)
	}
}

func TestSummarizeEvenMedianDoesNotOverflow(t *testing.T) {
	result := summarize("test", []time.Duration{time.Duration(math.MaxInt64 - 2), time.Duration(math.MaxInt64)})
	if result.median != time.Duration(math.MaxInt64-1) {
		t.Fatalf("median=%s", result.median)
	}
}

func validFullContext() model.Context {
	return model.Context{
		SchemaVersion:  model.SchemaVersion,
		IntervalMillis: benchmarkSampleInterval.Milliseconds(),
		SampleIntervals: model.SampleIntervals{
			GlobalStatus: benchmarkSampleInterval.Milliseconds(), WaitEvents: benchmarkSampleInterval.Milliseconds(),
			FileIO: benchmarkSampleInterval.Milliseconds(), ServerErrors: benchmarkSampleInterval.Milliseconds(),
			StatementDigests: benchmarkSampleInterval.Milliseconds(), StatementCounters: benchmarkSampleInterval.Milliseconds(),
		},
		Server:    model.Server{Flavor: "MySQL", Version: "8.4.6"},
		Findings:  []model.Finding{{ID: "fixture"}},
		Queries:   []model.Query{{Digest: "abc", Statement: "SELECT ?"}},
		Tables:    []model.Table{{Schema: "app", Name: "orders", Engine: "InnoDB"}},
		Indexes:   []model.Index{{Schema: "app", Table: "orders", Name: "PRIMARY"}},
		Processes: []model.Process{{ID: 1}},
		ConnectionGroups: []model.ConnectionGroup{
			{Kind: "user", Key: "loadgen", Total: 1},
		},
		Locks:         []model.LockWait{},
		Transactions:  []model.Transaction{},
		MetadataLocks: []model.MetadataLock{},
		WaitEvents: []model.WaitEvent{
			{Name: "wait/io/file", Class: "io/file", SampleCount: 1, EventsPerSecond: 10},
		},
		FileIO:          []model.FileIO{{Name: "innodb_data_file"}},
		ServerErrors:    []model.ServerError{{Number: 1000}},
		MemoryConsumers: []model.MemoryConsumer{{Name: "memory/sql"}},
		StatementSamples: []model.StatementSample{
			{Digest: "abc", Calls: 1, CallsPerSecond: 10},
		},
		StatementLatency: model.StatementLatency{P95Millis: 1},
		Metrics: model.Metrics{
			QueriesPerSecond:      10,
			TransactionsPerSecond: 2,
			RowsWrittenPerSecond:  1,
			ConnectionsMax:        151,
			BufferPoolDataBytes:   4096,
			RedoCapacityBytes:     1048576,
		},
		Instrumentation: model.Instrumentation{DigestCapacity: 100},
		Variables:       map[string]string{"performance_schema": "ON"},
		GlobalStatus:    map[string]string{"Uptime": "1"},
		InnoDBStatus:    "Status",
		Capabilities:    fullCapabilities(),
	}
}

func fullCapabilities() []model.Capability {
	names := []string{
		"statement digests", "table statistics", "index statistics", "process list",
		"row lock waits", "active transactions", "metadata locks", "statement latency histogram",
		"instrumentation coverage", "memory consumers", "replication", "InnoDB monitor",
		"wait events", "file I/O", "server errors", "statement database time", "statement counters",
	}
	capabilities := make([]model.Capability, 0, len(names))
	for _, name := range names {
		capabilities = append(capabilities, model.Capability{Name: name, Available: true})
	}
	return capabilities
}

func marshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
