package main

import (
	"testing"

	"github.com/maheshrijal/mysq/internal/model"
)

func TestIdleContextRejectsOneQuestion(t *testing.T) {
	ctx := model.Context{SampleIntervals: model.SampleIntervals{GlobalStatus: 250}}
	if err := idleContextError(ctx); err != nil {
		t.Fatalf("zero-question idle context failed: %v", err)
	}

	ctx.Metrics.QueriesPerSecond = 4
	if err := idleContextError(ctx); err == nil {
		t.Fatal("one sampled question passed the idle-context verifier")
	}
}
