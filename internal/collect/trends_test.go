package collect

import (
	"context"
	"database/sql"
	"math"
	"os"
	"testing"
	"time"
)

func TestTrendDeltaUsesElapsedTimeAndRejectsDiscontinuities(t *testing.T) {
	before := TrendCounters{At: time.Unix(100, 0), ServerUUID: "server", Uptime: 100, SinceFlush: 100, Questions: 100, Running: 1, LockWaits: 10, ReadBytes: 1000, WriteBytes: 2000}
	after := before
	after.At = before.At.Add(2500 * time.Millisecond)
	after.Uptime += 2
	after.SinceFlush += 2
	after.Questions += 26
	after.Running = 4
	after.LockWaits += 5
	after.ReadBytes += 2500
	after.WriteBytes += 5000
	point, ok := TrendDelta(before, after)
	if !ok || point.Queries != 10 || point.Running != 3 || point.LockWaits != 2 || point.ReadBytes != 1000 || point.WriteBytes != 2000 {
		t.Fatalf("wrong rates: %+v, valid %t", point, ok)
	}
	for name, change := range map[string]func(*TrendCounters){
		"identity":  func(p *TrendCounters) { p.ServerUUID = "other" },
		"restart":   func(p *TrendCounters) { p.Uptime = 1 },
		"flush":     func(p *TrendCounters) { p.SinceFlush = 1 },
		"questions": func(p *TrendCounters) { p.Questions = 0 },
		"locks":     func(p *TrendCounters) { p.LockWaits = 0 },
		"reads":     func(p *TrendCounters) { p.ReadBytes = 0 },
		"writes":    func(p *TrendCounters) { p.WriteBytes = 0 },
		"duplicate": func(p *TrendCounters) { p.At = before.At },
		"clock":     func(p *TrendCounters) { p.At = before.At.Add(-time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := after
			change(&bad)
			if _, ok := TrendDelta(before, bad); ok {
				t.Fatal("accepted discontinuity")
			}
		})
	}
	after = before
	after.At = before.At.Add(time.Second)
	after.Questions++
	after.Running = 0
	point, ok = TrendDelta(before, after)
	if !ok || point.Queries != 0 || point.Running != 0 {
		t.Fatalf("idle sampler counted itself: %+v", point)
	}
}

// The disposable fixture is idle at this stage of run.sh. Verify real server
// counter semantics: sampling contributes no QPS; one application SELECT does.
func TestFixtureTrendSampler(t *testing.T) {
	monitor, load := os.Getenv("MYSQ_E2E_MONITOR_DSN"), os.Getenv("MYSQ_E2E_LOAD_DSN")
	if monitor == "" || load == "" {
		t.Skip("requires disposable e2e fixture")
	}
	target, err := ResolveConnection(monitor)
	if err != nil {
		t.Fatal(err)
	}
	if target.Host != "127.0.0.1" || target.Database != "app" {
		t.Fatal("requires loopback app fixture")
	}
	db, err := OpenTrendSampler(target)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, err := SampleTrends(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SampleTrends(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	point, ok := TrendDelta(first, second)
	if !ok || point.Queries != 0 {
		t.Fatalf("sampler polluted idle QPS: %+v", point)
	}
	app, err := sql.Open("mysql", load)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	// Establish the connection before measuring the application's statement.
	if err = app.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	first, err = SampleTrends(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var one int
	if err = app.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatal(err)
	}
	second, err = SampleTrends(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	point, ok = TrendDelta(first, second)
	if !ok || math.Abs(point.Queries*second.At.Sub(first.At).Seconds()-1) > 1e-6 {
		t.Fatalf("application SELECT not measured exactly once: %+v", point)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, err = SampleTrends(cancelled, db); err == nil {
		t.Fatal("ignored cancelled sampling context")
	}
}
