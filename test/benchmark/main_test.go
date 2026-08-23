package main

import (
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
		{name: "variables", data: `{}`},
		{name: "engine", data: `{"connections_max":0,"redo_capacity_bytes":1}`},
		{name: "inspect-full", data: `{"schema_version":"1.3.0","queries":[],"tables":[]}`},
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
		{name: "inspect-full", data: `{"schema_version":"1.3.0","queries":[{}],"tables":[{}]}`},
		{name: "queries", data: `[{"digest":"abc"}]`},
		{name: "tables", data: `[{"name":"orders"}]`},
		{name: "waits", data: `[{"name":"wait/io/file"}]`},
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
