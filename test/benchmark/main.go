package main

import (
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
	runs := flag.Int("runs", 15, "measured runs per command")
	warmup := flag.Int("warmup", 3, "unmeasured warmup runs per command")
	flag.Parse()

	if *binary == "" {
		fatal("--binary is required")
	}
	if *runs < 1 || *warmup < 0 {
		fatal("--runs must be positive and --warmup cannot be negative")
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
	fmt.Println("| command | median | p95 | min | max |")
	fmt.Println("|---|---:|---:|---:|---:|")
	for _, current := range cases {
		measured := run(*binary, dsn, current, *warmup, *runs)
		fmt.Printf("| %s | %s | %s | %s | %s |\n", measured.name,
			milliseconds(measured.median), milliseconds(measured.p95),
			milliseconds(measured.min), milliseconds(measured.max))
	}
}

func run(binary, dsn string, current benchmarkCase, warmup, runs int) result {
	for range warmup {
		invoke(binary, dsn, current)
	}

	samples := make([]time.Duration, 0, runs)
	for range runs {
		started := time.Now()
		invoke(binary, dsn, current)
		samples = append(samples, time.Since(started))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return result{
		name:   current.name,
		median: samples[len(samples)/2],
		p95:    samples[int(math.Ceil(float64(len(samples))*0.95))-1],
		min:    samples[0],
		max:    samples[len(samples)-1],
	}
}

func invoke(binary, dsn string, current benchmarkCase) {
	command := exec.Command(binary, current.args...)
	command.Env = benchmarkEnvironment(dsn)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		fatal("%s failed: %v", current.name, err)
	}
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

func fatal(format string, values ...any) {
	fmt.Fprintf(os.Stderr, "benchmark: "+format+"\n", values...)
	os.Exit(1)
}
