package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		{name: "inspect-full", data: `{"schema_version":"1.3.0","queries":[],"tables":[]}`},
		{name: "inspect-full", data: `{"schema_version":123,"server":{"flavor":"MySQL","version":"8.4"},"queries":[{"digest":"abc","statement":"SELECT ?"}],"tables":[{"schema":"app","name":"orders","engine":"InnoDB"}]}`},
		{name: "inspect-full", data: `{"schema_version":"1.3.0","server":{"flavor":"MySQL","version":"8.4"},"queries":[{}],"tables":[{}]}`},
	} {
		t.Run(test.name+"_"+strings.ReplaceAll(test.data, " ", "_"), func(t *testing.T) {
			if err := validateOutput(test.name, []byte(test.data)); err == nil {
				t.Fatalf("validateOutput(%q, %q) unexpectedly passed", test.name, test.data)
			}
		})
	}
}

func TestValidateOutputAcceptsFixtureShapes(t *testing.T) {
	for _, test := range []struct {
		name, data string
	}{
		{name: "inspect-full", data: `{"schema_version":"1.3.0","server":{"flavor":"MySQL","version":"8.4.6"},"queries":[{"digest":"abc","statement":"SELECT ?"}],"tables":[{"schema":"app","name":"orders","engine":"InnoDB"}]}`},
		{name: "queries", data: `[{"digest":"abc","statement":"SELECT ?"}]`},
		{name: "tables", data: `[{"schema":"app","name":"orders","engine":"InnoDB"}]`},
		{name: "waits", data: `[{"name":"wait/io/file","class":"io/file"}]`},
		{name: "variables", data: `{"performance_schema":"ON"}`},
		{name: "engine", data: `{"connections_max":151,"redo_capacity_bytes":1048576}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateOutput(test.name, []byte(test.data)); err != nil {
				t.Fatalf("validateOutput(%q): %v", test.name, err)
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
	if result.p95 != 19*time.Millisecond || result.max != 20*time.Millisecond {
		t.Fatalf("p95=%s max=%s", result.p95, result.max)
	}
}
