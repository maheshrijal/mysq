package collect

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestParallelProbesBoundConcurrencyAndJoin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tasks := make([]probeTask, 9)
	entered := make(chan struct{}, len(tasks))
	release := make(chan struct{})
	var active, peak, completed atomic.Int32
	done := make(chan []error, 1)
	go func() {
		done <- runProbeTasks(ctx, tasks, 3, time.Second, func(ctx context.Context, _ int, _ probeTask) error {
			n := active.Add(1)
			defer active.Add(-1)
			for previous := peak.Load(); n > previous; previous = peak.Load() {
				if peak.CompareAndSwap(previous, n) {
					break
				}
			}
			entered <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			completed.Add(1)
			return nil
		})
	}()
	for range 3 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("probes did not overlap")
		}
	}
	if active.Load() != 3 {
		t.Fatalf("active = %d", active.Load())
	}
	close(release)
	errs := <-done
	if peak.Load() != 3 || active.Load() != 0 || completed.Load() != 9 {
		t.Fatalf("peak=%d active=%d complete=%d", peak.Load(), active.Load(), completed.Load())
	}
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestParallelProbeTimeoutDoesNotCancelSiblings(t *testing.T) {
	tasks := []probeTask{{name: "slow"}, {name: "good"}, {name: "after"}}
	errs := runProbeTasks(context.Background(), tasks, 2, 30*time.Millisecond, func(ctx context.Context, _ int, task probeTask) error {
		if task.name == "slow" {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	})
	if !errors.Is(errs[0], context.DeadlineExceeded) || errs[1] != nil || errs[2] != nil {
		t.Fatalf("results = %v", errs)
	}
}

func TestParallelProbeParentCancellationStopsQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	errs := runProbeTasks(ctx, make([]probeTask, 9), 3, time.Second, func(ctx context.Context, _ int, _ probeTask) error {
		calls.Add(1)
		cancel()
		<-ctx.Done()
		return ctx.Err()
	})
	if calls.Load() > 3 {
		t.Fatalf("started %d tasks after cancellation", calls.Load())
	}
	for _, err := range errs {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("result = %v", err)
		}
	}
}

func TestParallelSamplerStatementsExcludedFromWorkload(t *testing.T) {
	for _, statement := range []string{
		"SELECT EVENT_NAME, MAX_TIMER_WAIT, SUM_TIMER_WAIT FROM performance_schema.events_waits_summary_global_by_event_name",
		"SELECT EVENT_NAME, SUM_NUMBER_OF_BYTES_READ, SUM_TIMER_WRITE FROM performance_schema.file_summary_by_event_name",
		"SELECT ERROR_NUMBER, SUM_ERROR_RAISED, FIRST_SEEN FROM performance_schema.events_errors_summary_global_by_error",
		"SET SESSION MAX_EXECUTION_TIME = ?",
		"SET SESSION transaction_read_only = ON",
		"SELECT VERSION(), @@hostname, DATABASE(), @@server_uuid, @@server_id, @@read_only, @@super_read_only",
	} {
		if !internalStatementSample(statement) {
			t.Fatalf("collector statement leaked: %s", statement)
		}
	}
	if internalStatementSample("SELECT COUNT(*) FROM performance_schema.events_errors_summary_global_by_error") {
		t.Fatal("unrelated monitoring query hidden")
	}
	if internalStatementSample("SELECT * FROM app.orders") {
		t.Fatal("application statement hidden")
	}
}
