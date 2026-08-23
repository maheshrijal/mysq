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
)

type benchmarkCase struct {
	name string
	args []string
}

type result struct {
	name                  string
	median, p95, min, max time.Duration
}

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
		{name: "inspect-full", args: []string{"inspect", "--full", "--format", "json", "--no-store", "--interval", "100ms"}},
		{name: "queries", args: []string{"queries", "--json", "--interval", "100ms"}},
		{name: "tables", args: []string{"tables", "--json", "--interval", "100ms"}},
		{name: "variables", args: []string{"variables", "--json", "--interval", "100ms"}},
		{name: "waits", args: []string{"waits", "--json", "--interval", "100ms"}},
		{name: "engine", args: []string{"engine", "--json", "--interval", "100ms"}},
	}

	fmt.Printf("Docker MySQL command benchmark (%d runs after %d warmups)\n\n", *runs, *warmup)
	if *baseline == "" {
		fmt.Println("| command | median | p95 | min | max |")
		fmt.Println("|---|---:|---:|---:|---:|")
		for _, current := range cases {
			measured := run(*binary, dsn, current, *warmup, *runs)
			fmt.Printf("| %s | %s | %s | %s | %s |\n", measured.name,
				milliseconds(measured.median), milliseconds(measured.p95),
				milliseconds(measured.min), milliseconds(measured.max))
		}
		return
	}

	fmt.Println("| command | baseline median | candidate median | change | baseline p95 | candidate p95 |")
	fmt.Println("|---|---:|---:|---:|---:|---:|")
	for _, current := range cases {
		base, candidate := runPaired(*baseline, *binary, dsn, current, *warmup, *runs)
		fmt.Printf("| %s | %s | %s | %+.1f%% | %s | %s |\n", current.name,
			milliseconds(base.median), milliseconds(candidate.median), percentChange(base.median, candidate.median),
			milliseconds(base.p95), milliseconds(candidate.p95))
	}
}

func run(binary, dsn string, current benchmarkCase, warmup, runs int) result {
	validateInvocation(binary, dsn, current)
	for range warmup {
		invokeTimed(binary, dsn, current)
	}

	samples := make([]time.Duration, 0, runs)
	for range runs {
		samples = append(samples, invokeTimed(binary, dsn, current))
	}
	return summarize(current.name, samples)
}

func runPaired(baseline, candidate, dsn string, current benchmarkCase, warmup, runs int) (result, result) {
	validateInvocation(baseline, dsn, current)
	validateInvocation(candidate, dsn, current)
	for index := range warmup {
		invokePair(baseline, candidate, dsn, current, index)
	}

	baseSamples := make([]time.Duration, 0, runs)
	candidateSamples := make([]time.Duration, 0, runs)
	for index := range runs {
		first, second := invokePair(baseline, candidate, dsn, current, index)
		baseSamples = append(baseSamples, first)
		candidateSamples = append(candidateSamples, second)
	}
	return summarize(current.name, baseSamples), summarize(current.name, candidateSamples)
}

func invokePair(baseline, candidate, dsn string, current benchmarkCase, index int) (time.Duration, time.Duration) {
	if index%2 == 0 {
		return invokeTimed(baseline, dsn, current), invokeTimed(candidate, dsn, current)
	}
	candidateElapsed := invokeTimed(candidate, dsn, current)
	baselineElapsed := invokeTimed(baseline, dsn, current)
	return baselineElapsed, candidateElapsed
}

func summarize(name string, samples []time.Duration) result {
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return result{
		name:   name,
		median: samples[len(samples)/2],
		p95:    samples[int(math.Ceil(float64(len(samples))*0.95))-1],
		min:    samples[0],
		max:    samples[len(samples)-1],
	}
}

func invokeTimed(binary, dsn string, current benchmarkCase) time.Duration {
	started := time.Now()
	if err := invoke(binary, dsn, current, io.Discard); err != nil {
		fatal("%v", err)
	}
	return time.Since(started)
}

func validateInvocation(binary, dsn string, current benchmarkCase) {
	var output bytes.Buffer
	if err := invoke(binary, dsn, current, &output); err != nil {
		fatal("%v", err)
	}
	if err := validateOutput(current.name, output.Bytes()); err != nil {
		fatal("%s produced invalid diagnostics: %v", current.name, err)
	}
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

func validateOutput(name string, data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("empty output")
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("parse JSON: %w", err)
	}
	switch name {
	case "inspect-full":
		object, ok := value.(map[string]any)
		if !ok || object["schema_version"] == "" || arrayLength(object["queries"]) == 0 || arrayLength(object["tables"]) == 0 {
			return errors.New("missing full inspection identity or evidence")
		}
	case "queries", "tables", "waits":
		if arrayLength(value) == 0 {
			return fmt.Errorf("%s array is empty or null", name)
		}
	case "variables":
		object, ok := value.(map[string]any)
		if !ok || !strings.EqualFold(fmt.Sprint(object["performance_schema"]), "ON") {
			return errors.New("performance_schema variable is absent or disabled")
		}
	case "engine":
		object, ok := value.(map[string]any)
		if !ok || number(object["connections_max"]) <= 0 || number(object["redo_capacity_bytes"]) <= 0 {
			return errors.New("engine capacity metrics are absent")
		}
	default:
		return fmt.Errorf("no validator for %q", name)
	}
	return nil
}

func arrayLength(value any) int {
	items, _ := value.([]any)
	return len(items)
}

func number(value any) float64 {
	number, _ := value.(float64)
	return number
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
