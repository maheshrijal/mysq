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
		if _, err := invokeTimed(binary, dsn, current); err != nil {
			return result{}, err
		}
	}

	samples := make([]time.Duration, 0, runs)
	for range runs {
		elapsed, err := invokeTimed(binary, dsn, current)
		if err != nil {
			return result{}, err
		}
		samples = append(samples, elapsed)
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
	for index := range runs {
		first, second, err := invokePair(baseline, candidate, dsn, current, index)
		if err != nil {
			return result{}, result{}, err
		}
		baseSamples = append(baseSamples, first)
		candidateSamples = append(candidateSamples, second)
	}
	return summarize(current.name, baseSamples), summarize(current.name, candidateSamples), nil
}

func invokePair(baseline, candidate, dsn string, current benchmarkCase, index int) (time.Duration, time.Duration, error) {
	if index%2 == 0 {
		baselineElapsed, err := invokeTimed(baseline, dsn, current)
		if err != nil {
			return 0, 0, err
		}
		candidateElapsed, err := invokeTimed(candidate, dsn, current)
		return baselineElapsed, candidateElapsed, err
	}
	candidateElapsed, err := invokeTimed(candidate, dsn, current)
	if err != nil {
		return 0, 0, err
	}
	baselineElapsed, err := invokeTimed(baseline, dsn, current)
	return baselineElapsed, candidateElapsed, err
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

func invokeTimed(binary, dsn string, current benchmarkCase) (time.Duration, error) {
	var output bytes.Buffer
	started := time.Now()
	if err := invoke(binary, dsn, current, &output); err != nil {
		return 0, err
	}
	elapsed := time.Since(started)
	if err := validateOutput(current.name, output.Bytes(), elapsed); err != nil {
		return 0, fmt.Errorf("%s produced invalid diagnostics: %w", current.name, err)
	}
	return elapsed, nil
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

func validateOutput(name string, data []byte, elapsed time.Duration) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("empty output")
	}
	switch name {
	case "inspect-full":
		var context model.Context
		if err := json.Unmarshal(data, &context); err != nil {
			return fmt.Errorf("parse full inspection JSON: %w", err)
		}
		return validateFullInspection(context, elapsed)
	case "queries":
		var queries []model.Query
		if err := json.Unmarshal(data, &queries); err != nil {
			return fmt.Errorf("parse queries JSON: %w", err)
		}
		return validateQueries(queries)
	case "tables":
		var tables []model.Table
		if err := json.Unmarshal(data, &tables); err != nil {
			return fmt.Errorf("parse tables JSON: %w", err)
		}
		return validateTables(tables)
	case "waits":
		if err := validateSampleDuration(name, elapsed); err != nil {
			return err
		}
		var waits []model.WaitEvent
		if err := json.Unmarshal(data, &waits); err != nil {
			return fmt.Errorf("parse waits JSON: %w", err)
		}
		return validateWaits(waits)
	case "variables":
		var variables map[string]string
		if err := json.Unmarshal(data, &variables); err != nil {
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
		if err := json.Unmarshal(data, &metrics); err != nil {
			return fmt.Errorf("parse engine JSON: %w", err)
		}
		return validateEngine(metrics)
	default:
		return fmt.Errorf("no validator for %q", name)
	}
	return nil
}

func validateFullInspection(context model.Context, elapsed time.Duration) error {
	if err := validateSampleDuration("inspect-full", elapsed); err != nil {
		return err
	}
	if context.IntervalMillis < benchmarkSampleInterval.Milliseconds() {
		return fmt.Errorf("full inspection interval is %dms, want at least %dms", context.IntervalMillis, benchmarkSampleInterval.Milliseconds())
	}
	if context.SchemaVersion != model.SchemaVersion || context.Server.Flavor != "MySQL" || context.Server.Version == "" {
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
		if !capability.Available && strings.TrimSpace(capability.Reason) == "" {
			return fmt.Errorf("full inspection unavailable probe %q has no reason", name)
		}
	}
	return nil
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
