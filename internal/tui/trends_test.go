package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/maheshrijal/mysq/internal/collect"
	"github.com/muesli/termenv"
)

func acceptTrend(m *Model, at time.Time, questions uint64, err error) {
	next, _ := m.Update(trendMessage{at: at, counters: collect.TrendCounters{At: at, ServerUUID: "test", Questions: questions, Running: 2}, err: err, generation: m.trends.generation})
	*m = next.(Model)
}

func TestTrendsPauseResumeFailuresAndBoundedHistory(t *testing.T) {
	m, _ := queryActionModel()
	m.trends.sample = func(context.Context) (collect.TrendCounters, error) { return collect.TrendCounters{}, nil }
	at := time.Unix(1000, 0)
	acceptTrend(&m, at, 10, nil)
	acceptTrend(&m, at.Add(2*time.Second), 15, nil)
	if !m.trends.points[1].valid || m.trends.points[1].Queries != 2 {
		t.Fatal("missing first measured rate")
	}
	actionKey(&m, "p")
	if !m.trends.paused || m.trends.previous != nil {
		t.Fatal("pause did not discard baseline")
	}
	frozen := m.trends.now
	next, _ := m.Update(trendTick(at.Add(4 * time.Second)))
	m = next.(Model)
	acceptTrend(&m, at.Add(4*time.Second), 20, nil)
	if len(m.trends.points) != 2 || !m.trends.now.Equal(frozen) {
		t.Fatal("paused history changed")
	}
	generation := m.trends.generation
	actionKey(&m, "p")
	next, _ = m.Update(trendMessage{generation: generation, at: at.Add(6 * time.Second)})
	m = next.(Model)
	if len(m.trends.points) != 2 {
		t.Fatal("late sample survived pause/resume")
	}
	acceptTrend(&m, at.Add(8*time.Second), 30, nil)
	if m.trends.points[2].valid {
		t.Fatal("connected rate across paused interval")
	}
	acceptTrend(&m, at.Add(10*time.Second), 35, nil)
	acceptTrend(&m, at.Add(12*time.Second), 0, errors.New("offline"))
	if m.trends.previous != nil || m.trends.points[4].valid || m.trends.err == nil {
		t.Fatal("failed sample became a value")
	}
	acceptTrend(&m, at.Add(14*time.Second), 40, nil)
	if m.trends.points[5].valid {
		t.Fatal("bridged outage")
	}
	for i := 8; i <= 250; i++ {
		acceptTrend(&m, at.Add(time.Duration(i)*2*time.Second), uint64(i*10), nil)
	}
	if len(m.trends.points) > 152 || m.trends.points[0].At.Before(m.trends.now.Add(-trendWindow)) {
		t.Fatal("history grew beyond five minutes")
	}
}

func TestTrendsDoNotOverlapOrDisturbQueryConfirmation(t *testing.T) {
	m, _ := queryActionModel()
	calls := 0
	m.trends.sample = func(context.Context) (collect.TrendCounters, error) {
		calls++
		return collect.TrendCounters{}, errors.New("offline")
	}
	next, cmd := m.Update(trendTick(time.Now()))
	m = next.(Model)
	batch := cmd().(tea.BatchMsg)
	if len(batch) != 2 || !m.trends.inFlight {
		t.Fatal("sample not scheduled")
	}
	next, _ = m.Update(trendTick(time.Now().Add(trendPeriod)))
	m = next.(Model)
	if calls != 0 || !m.trends.inFlight {
		t.Fatal("overlapped pending sample")
	}
	deliverAction(&m, actionKey(&m, "K"))
	actionKey(&m, "enter")
	actionKey(&m, "ki")
	frame := m.View()
	next, _ = m.Update(batch[1]())
	m = next.(Model)
	if calls != 1 || m.trends.inFlight || m.View() != frame || m.live.input.Value() != "ki" {
		t.Fatal("sample changed confirmation or repeated query")
	}
	actionKey(&m, "p")
	if m.trends.paused || m.live.input.Value() != "kip" {
		t.Fatal("pause shortcut intercepted confirmation input")
	}
}

func TestTrendChartsFitAndKeepGapsInColorAndPlainModes(t *testing.T) {
	profile := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	m, _ := queryActionModel()
	at := time.Unix(1000, 0)
	m.trends.started = at
	m.trends.now = at.Add(time.Minute)
	for i := 0; i <= 30; i++ {
		m.trends.points = append(m.trends.points, trendObservation{TrendPoint: collect.TrendPoint{At: at.Add(time.Duration(i) * 2 * time.Second), Queries: float64(i % 7), Running: float64(i % 3), ReadBytes: float64(i * 1024), WriteBytes: float64(i * 2048)}, valid: i < 10 || i > 20})
	}
	for _, width := range []int{48, 76, 100, 160} {
		for _, height := range []int{12, 30, 50} {
			lipgloss.SetColorProfile(termenv.Ascii)
			plain := m.trendView(width, height)
			lipgloss.SetColorProfile(termenv.TrueColor)
			colored := m.trendView(width, height)
			if ansi.Strip(colored) != plain {
				t.Fatal("color corrupted graph layout")
			}
			if lipgloss.Width(colored) > width {
				t.Fatalf("graph exceeds width %d: %d", width, lipgloss.Width(colored))
			}
			if width >= 100 && height >= 23 && lipgloss.Height(colored) > height {
				t.Fatalf("graph exceeds spare height %d: %d", height, lipgloss.Height(colored))
			}
			for _, label := range []string{"QUERIES / SEC", "RUNNING THREADS", "ROW-LOCK WAITS / SEC", "INNODB I/O", "p pause"} {
				if !strings.Contains(plain, label) {
					t.Fatalf("missing %s", label)
				}
			}
		}
	}
	series := trendSeries{value: func(p collect.TrendPoint) float64 { return p.Queries }}
	spark := []rune(trendSparkline(m.trends.points, series, at, m.trends.now, 61))
	for i := 20; i <= 40; i++ {
		if spark[i] != ' ' {
			t.Fatal("sparkline filled an outage")
		}
	}
	chart := trendChart{series: []trendSeries{{color: cyan, value: series.value}}}
	plot := ansi.Strip(trendPlot(chart, m.trends.points, at, m.trends.now, 69, 5))
	for _, line := range strings.Split(plot, "\n") {
		for _, r := range []rune(line)[29:47] {
			if r != ' ' {
				t.Fatal("line graph bridged an outage")
			}
		}
	}
	chart.bytes = true
	chart.series[0].value = func(collect.TrendPoint) float64 { return 876.6 * 1024 }
	if !strings.Contains(ansi.Strip(trendPlot(chart, m.trends.points, at, m.trends.now, 44, 5)), "876.6 KiB") {
		t.Fatal("I/O axis clipped its unit")
	}
}
