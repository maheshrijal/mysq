package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/maheshrijal/mysq/internal/model"
)

type benchmarkCase struct {
	name string
	args []string
}

type result struct {
	name                  string
	median, p95, min, max time.Duration
}

type invocation struct {
	elapsed        time.Duration
	sampleEvidence bool
}

const benchmarkSampleInterval = 100 * time.Millisecond

func main() {
	binary := flag.String("binary", "", "mysq binary to benchmark")
	baseline := flag.String("baseline", "", "optional baseline mysq binary for paired comparison")
	runs := flag.Int("runs", 50, "measured runs per command")
	warmup := flag.Int("warmup", 5, "unmeasured warmup runs per command")
	flag.Parse()

	if *binary == "" {
		fatal("--binary is required")
	}
	if *runs < 20 || *warmup < 0 {
		fatal("--runs must be at least 20 for p95 and --warmup cannot be negative")
	}
	dsn := strings.TrimSpace(os.Getenv("MYSQ_BENCHMARK_DSN"))
	if dsn == "" {
		fatal("MYSQ_BENCHMARK_DSN is required")
	}

	cases := []benchmarkCase{
		{name: "inspect-full", args: []string{"inspect", "--full", "--format", "json", "--no-store", "--interval", benchmarkSampleInterval.String()}},
		{name: "queries", args: []string{"queries", "--json", "--interval", benchmarkSampleInterval.String()}},
		{name: "tables", args: []string{"tables", "--json", "--interval", benchmarkSampleInterval.String()}},
		{name: "variables", args: []string{"variables", "--json", "--interval", benchmarkSampleInterval.String()}},
		{name: "waits", args: []string{"waits", "--json", "--interval", benchmarkSampleInterval.String()}},
		{name: "io", args: []string{"io", "--json", "--interval", benchmarkSampleInterval.String()}},
		{name: "errors", args: []string{"errors", "--json", "--interval", benchmarkSampleInterval.String()}},
		{name: "engine", args: []string{"engine", "--json", "--interval", benchmarkSampleInterval.String()}},
	}

	fmt.Printf("Docker MySQL command benchmark (%d runs after %d warmups)\n\n", *runs, *warmup)
	if *baseline == "" {
		fmt.Println("| command | median | p95 | min | max |")
		fmt.Println("|---|---:|---:|---:|---:|")
		for _, current := range cases {
			measured, err := run(*binary, dsn, current, *warmup, *runs)
			if err != nil {
				fatal("%v", err)
			}
			fmt.Printf("| %s | %s | %s | %s | %s |\n", measured.name,
				milliseconds(measured.median), milliseconds(measured.p95),
				milliseconds(measured.min), milliseconds(measured.max))
		}
		return
	}

	fmt.Println("| command | baseline median | candidate median | change | baseline p95 | candidate p95 |")
	fmt.Println("|---|---:|---:|---:|---:|---:|")
	for _, current := range cases {
		base, candidate, err := runPaired(*baseline, *binary, dsn, current, *warmup, *runs)
		if err != nil {
			fatal("%v", err)
		}
		fmt.Printf("| %s | %s | %s | %+.1f%% | %s | %s |\n", current.name,
			milliseconds(base.median), milliseconds(candidate.median), percentChange(base.median, candidate.median),
			milliseconds(base.p95), milliseconds(candidate.p95))
	}
}

func run(binary, dsn string, current benchmarkCase, warmup, runs int) (result, error) {
	for range warmup {
		if _, err := invokeTimed(binary, dsn, current, true); err != nil {
			return result{}, err
		}
	}

	samples := make([]time.Duration, 0, runs)
	hasSampleEvidence := false
	for range runs {
		measured, err := invokeTimed(binary, dsn, current, true)
		if err != nil {
			return result{}, err
		}
		samples = append(samples, measured.elapsed)
		hasSampleEvidence = hasSampleEvidence || measured.sampleEvidence
	}
	if err := requireSampleEvidence(current.name, hasSampleEvidence, runs); err != nil {
		return result{}, err
	}
	return summarize(current.name, samples), nil
}

func runPaired(baseline, candidate, dsn string, current benchmarkCase, warmup, runs int) (result, result, error) {
	for index := range warmup {
		if _, _, err := invokePair(baseline, candidate, dsn, current, index); err != nil {
			return result{}, result{}, err
		}
	}

	baseSamples := make([]time.Duration, 0, runs)
	candidateSamples := make([]time.Duration, 0, runs)
	baseHasSampleEvidence := false
	candidateHasSampleEvidence := false
	for index := range runs {
		first, second, err := invokePair(baseline, candidate, dsn, current, index)
		if err != nil {
			return result{}, result{}, err
		}
		baseSamples = append(baseSamples, first.elapsed)
		candidateSamples = append(candidateSamples, second.elapsed)
		baseHasSampleEvidence = baseHasSampleEvidence || first.sampleEvidence
		candidateHasSampleEvidence = candidateHasSampleEvidence || second.sampleEvidence
	}
	if err := requireSampleEvidence("baseline "+current.name, baseHasSampleEvidence, runs); err != nil {
		return result{}, result{}, err
	}
	if err := requireSampleEvidence("candidate "+current.name, candidateHasSampleEvidence, runs); err != nil {
		return result{}, result{}, err
	}
	return summarize(current.name, baseSamples), summarize(current.name, candidateSamples), nil
}

func invokePair(baseline, candidate, dsn string, current benchmarkCase, index int) (invocation, invocation, error) {
	if index%2 == 0 {
		baselineResult, err := invokeTimed(baseline, dsn, current, false)
		if err != nil {
			return invocation{}, invocation{}, err
		}
		candidateResult, err := invokeTimed(candidate, dsn, current, true)
		return baselineResult, candidateResult, err
	}
	candidateResult, err := invokeTimed(candidate, dsn, current, true)
	if err != nil {
		return invocation{}, invocation{}, err
	}
	baselineResult, err := invokeTimed(baseline, dsn, current, false)
	return baselineResult, candidateResult, err
}

func summarize(name string, samples []time.Duration) result {
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	median := samples[len(samples)/2]
	if len(samples)%2 == 0 {
		lower := samples[len(samples)/2-1]
		upper := samples[len(samples)/2]
		median = lower + (upper-lower)/2
	}
	return result{
		name:   name,
		median: median,
		p95:    samples[int(math.Ceil(float64(len(samples))*0.95))-1],
		min:    samples[0],
		max:    samples[len(samples)-1],
	}
}

func invokeTimed(binary, dsn string, current benchmarkCase, requireCurrentSchema bool) (invocation, error) {
	var output bytes.Buffer
	started := time.Now()
	if err := invoke(binary, dsn, current, &output); err != nil {
		return invocation{}, err
	}
	elapsed := time.Since(started)
	if err := validateOutput(current.name, output.Bytes(), elapsed, requireCurrentSchema); err != nil {
		return invocation{}, fmt.Errorf("%s produced invalid diagnostics: %w", current.name, err)
	}
	return invocation{elapsed: elapsed, sampleEvidence: derivedSampleEvidence(current.name, output.Bytes())}, nil
}

func requireSampleEvidence(name string, found bool, runs int) error {
	if (strings.HasSuffix(name, "waits") || strings.HasSuffix(name, "io") ||
		strings.HasSuffix(name, "errors") || strings.HasSuffix(name, "engine")) && !found {
		return fmt.Errorf("%s produced no derived sample delta or rate across %d measured runs", name, runs)
	}
	return nil
}

func invoke(binary, dsn string, current benchmarkCase, stdout io.Writer) error {
	command := exec.Command(binary, current.args...)
	command.Env = benchmarkEnvironment(dsn)
	command.Stdout = stdout
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return fmt.Errorf("%s failed: %w: %s", current.name, err, detail)
		}
		return fmt.Errorf("%s failed: %w", current.name, err)
	}
	return nil
}

func validateOutput(name string, data []byte, elapsed time.Duration, requireCurrentSchema bool) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("empty output")
	}
	switch name {
	case "inspect-full":
		var context model.Context
		if err := decodeStrictJSON(data, &context); err != nil {
			return fmt.Errorf("parse full inspection JSON: %w", err)
		}
		return validateFullInspection(context, elapsed, requireCurrentSchema)
	case "queries":
		var queries []model.Query
		if err := decodeStrictJSON(data, &queries); err != nil {
			return fmt.Errorf("parse queries JSON: %w", err)
		}
		return validateQueries(queries)
	case "tables":
		var tables []model.Table
		if err := decodeStrictJSON(data, &tables); err != nil {
			return fmt.Errorf("parse tables JSON: %w", err)
		}
		return validateTables(tables)
	case "waits":
		if err := validateSampleDuration(name, elapsed); err != nil {
			return err
		}
		var waits []model.WaitEvent
		if err := decodeStrictJSON(data, &waits); err != nil {
			return fmt.Errorf("parse waits JSON: %w", err)
		}
		return validateWaits(waits)
	case "io":
		if err := validateSampleDuration(name, elapsed); err != nil {
			return err
		}
		var items []model.FileIO
		if err := decodeStrictJSON(data, &items); err != nil {
			return fmt.Errorf("parse file I/O JSON: %w", err)
		}
		return validateFileIO(items)
	case "errors":
		if err := validateSampleDuration(name, elapsed); err != nil {
			return err
		}
		var items []model.ServerError
		if err := decodeStrictJSON(data, &items); err != nil {
			return fmt.Errorf("parse server errors JSON: %w", err)
		}
		return validateServerErrors(items)
	case "variables":
		var variables map[string]string
		if err := decodeStrictJSON(data, &variables); err != nil {
			return fmt.Errorf("parse variables JSON: %w", err)
		}
		if !strings.EqualFold(variables["performance_schema"], "ON") {
			return errors.New("performance_schema variable is absent or disabled")
		}
	case "engine":
		if err := validateSampleDuration(name, elapsed); err != nil {
			return err
		}
		var metrics model.Metrics
		if err := decodeStrictJSON(data, &metrics); err != nil {
			return fmt.Errorf("parse engine JSON: %w", err)
		}
		return validateEngine(metrics)
	default:
		return fmt.Errorf("no validator for %q", name)
	}
	return nil
}

func decodeStrictJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}

func validateFullInspection(context model.Context, elapsed time.Duration, requireCurrentSchema bool) error {
	if err := validateSampleDuration("inspect-full", elapsed); err != nil {
		return err
	}
	if context.IntervalMillis < benchmarkSampleInterval.Milliseconds() {
		return fmt.Errorf("full inspection interval is %dms, want at least %dms", context.IntervalMillis, benchmarkSampleInterval.Milliseconds())
	}
	if requireCurrentSchema {
		intervals := context.SampleIntervals
		if context.IntervalMillis != intervals.GlobalStatus {
			return fmt.Errorf("legacy interval is %dms, want global status interval %dms", context.IntervalMillis, intervals.GlobalStatus)
		}
		for name, interval := range map[string]int64{
			"global status": intervals.GlobalStatus, "wait events": intervals.WaitEvents, "file I/O": intervals.FileIO,
			"server errors": intervals.ServerErrors, "statement digests": intervals.StatementDigests, "statement counters": intervals.StatementCounters,
		} {
			if interval < benchmarkSampleInterval.Milliseconds() {
				return fmt.Errorf("%s interval is %dms, want at least %dms", name, interval, benchmarkSampleInterval.Milliseconds())
			}
		}
	}
	if (requireCurrentSchema && context.SchemaVersion != model.SchemaVersion) || (!requireCurrentSchema && !supportedBaselineSchema(context.SchemaVersion)) || context.Server.Flavor != "MySQL" || context.Server.Version == "" {
		return errors.New("missing full inspection server identity")
	}
	if err := validateQueries(context.Queries); err != nil {
		return err
	}
	if err := validateTables(context.Tables); err != nil {
		return err
	}
	if len(context.Indexes) == 0 || len(context.Processes) == 0 || len(context.ConnectionGroups) == 0 || len(context.Findings) == 0 {
		return errors.New("full inspection is missing index, process, connection-group, or finding evidence")
	}
	if context.Locks == nil || context.Transactions == nil || context.MetadataLocks == nil {
		return errors.New("full inspection is missing lock or transaction collections")
	}
	if err := validateWaits(context.WaitEvents); err != nil {
		return fmt.Errorf("full inspection: %w", err)
	}
	if len(context.FileIO) == 0 || context.FileIO[0].Name == "" ||
		len(context.ServerErrors) == 0 || context.ServerErrors[0].Number == 0 ||
		len(context.MemoryConsumers) == 0 || context.MemoryConsumers[0].Name == "" {
		return errors.New("full inspection is missing file I/O, server-error, or memory evidence")
	}
	if err := validateEngine(context.Metrics); err != nil {
		return fmt.Errorf("full inspection: %w", err)
	}
	if context.Instrumentation.DigestCapacity == 0 || !strings.EqualFold(context.Variables["performance_schema"], "ON") ||
		len(context.GlobalStatus) == 0 || strings.TrimSpace(context.InnoDBStatus) == "" {
		return errors.New("full inspection is missing instrumentation, variables, status, or InnoDB evidence")
	}
	capabilities := make(map[string]model.Capability, len(context.Capabilities))
	for _, capability := range context.Capabilities {
		capabilities[capability.Name] = capability
	}
	for _, name := range []string{
		"statement digests", "table statistics", "index statistics", "process list",
		"row lock waits", "active transactions", "metadata locks", "statement latency histogram",
		"instrumentation coverage", "memory consumers", "replication", "InnoDB monitor",
		"wait events", "file I/O", "server errors", "statement database time", "statement counters",
	} {
		capability, ok := capabilities[name]
		if !ok {
			return fmt.Errorf("full inspection is missing %q probe capability", name)
		}
		if !capability.Available {
			return fmt.Errorf("full inspection probe %q is unavailable: %s", name, strings.TrimSpace(capability.Reason))
		}
	}
	return nil
}

func supportedBaselineSchema(version string) bool {
	switch version {
	case "1.0.0", "1.1.0", "1.2.0", "1.3.0", model.SchemaVersion:
		return true
	default:
		return false
	}
}

func validateSampleDuration(name string, elapsed time.Duration) error {
	if elapsed < benchmarkSampleInterval {
		return fmt.Errorf("%s completed in %s, shorter than the %s sample contract", name, elapsed, benchmarkSampleInterval)
	}
	return nil
}

func validateWaits(waits []model.WaitEvent) error {
	if len(waits) == 0 {
		return errors.New("waits array is empty or null")
	}
	for _, wait := range waits {
		if wait.Name == "" || wait.Class == "" {
			return errors.New("wait evidence is missing name or class")
		}
	}
	return nil
}

func validateEngine(metrics model.Metrics) error {
	if metrics.ConnectionsMax == 0 || metrics.RedoCapacityBytes == 0 || metrics.BufferPoolDataBytes == 0 {
		return errors.New("engine capacity metrics are absent")
	}
	return nil
}

func validateFileIO(items []model.FileIO) error {
	if len(items) == 0 {
		return errors.New("file I/O array is empty or null")
	}
	for _, item := range items {
		if item.Name == "" || item.Class == "" {
			return errors.New("file I/O evidence is missing name or class")
		}
	}
	return nil
}

func validateServerErrors(items []model.ServerError) error {
	if len(items) == 0 {
		return errors.New("server errors array is empty or null")
	}
	for _, item := range items {
		if item.Number == 0 || item.Name == "" || item.SQLState == "" {
			return errors.New("server error evidence is missing number, name, or SQL state")
		}
	}
	return nil
}

func derivedSampleEvidence(name string, data []byte) bool {
	switch name {
	case "waits":
		var waits []model.WaitEvent
		if json.Unmarshal(data, &waits) != nil {
			return false
		}
		for _, wait := range waits {
			if wait.SampleCount > 0 || wait.SampleLatencyMillis > 0 ||
				wait.EventsPerSecond > 0 || wait.WaitMillisPerSecond > 0 {
				return true
			}
		}
	case "engine":
		var metrics model.Metrics
		if json.Unmarshal(data, &metrics) != nil {
			return false
		}
		return metrics.QueriesPerSecond > 0 || metrics.TransactionsPerSecond > 0 ||
			metrics.RowsReadPerSecond > 0 || metrics.RowsWrittenPerSecond > 0 ||
			metrics.NetworkInBytesPerSec > 0 || metrics.NetworkOutBytesPerSec > 0 ||
			metrics.DataReadsPerSecond > 0 || metrics.DataWritesPerSecond > 0 ||
			metrics.RedoBytesPerSecond > 0
	case "io":
		var items []model.FileIO
		if json.Unmarshal(data, &items) != nil {
			return false
		}
		for _, item := range items {
			if item.ReadsPerSecond > 0 || item.WritesPerSecond > 0 ||
				item.ReadBytesPerSecond > 0 || item.WriteBytesPerSecond > 0 ||
				item.WaitMillisPerSecond > 0 {
				return true
			}
		}
	case "errors":
		var items []model.ServerError
		if json.Unmarshal(data, &items) != nil {
			return false
		}
		for _, item := range items {
			if item.SampleRaised > 0 || item.RaisedPerSecond > 0 {
				return true
			}
		}
	}
	return false
}

func validateQueries(queries []model.Query) error {
	if len(queries) == 0 {
		return errors.New("queries array is empty or null")
	}
	for _, query := range queries {
		if query.Digest == "" || query.Statement == "" {
			return errors.New("query evidence is missing digest or statement")
		}
	}
	return nil
}

func validateTables(tables []model.Table) error {
	if len(tables) == 0 {
		return errors.New("tables array is empty or null")
	}
	for _, table := range tables {
		if table.Schema == "" || table.Name == "" || table.Engine == "" {
			return errors.New("table evidence is missing schema, name, or engine")
		}
	}
	return nil
}

func benchmarkEnvironment(dsn string) []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, value := range os.Environ() {
		if strings.HasPrefix(value, "MYSQ_DATABASE_URL=") ||
			strings.HasPrefix(value, "MYSQLDOT_DATABASE_URL=") ||
			strings.HasPrefix(value, "DATABASE_URL=") {
			continue
		}
		environment = append(environment, value)
	}
	return append(environment, "MYSQ_DATABASE_URL="+dsn)
}

func milliseconds(value time.Duration) string {
	return fmt.Sprintf("%.2f ms", float64(value)/float64(time.Millisecond))
}

func percentChange(baseline, candidate time.Duration) float64 {
	return (float64(candidate)/float64(baseline) - 1) * 100
}

func fatal(format string, values ...any) {
	fmt.Fprintf(os.Stderr, "benchmark: "+format+"\n", values...)
	os.Exit(1)
}
