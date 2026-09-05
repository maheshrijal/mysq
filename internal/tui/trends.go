package tui

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/maheshrijal/mysq/internal/collect"
	"github.com/maheshrijal/mysq/internal/sanitize"
)

const trendPeriod = 2 * time.Second
const trendWindow = 5 * time.Minute

type TrendSampler func(context.Context) (collect.TrendCounters, error)
type trendTick time.Time
type trendMessage struct {
	counters   collect.TrendCounters
	at         time.Time
	err        error
	generation uint64
}
type trendObservation struct {
	collect.TrendPoint
	valid bool
}
type trendHistory struct {
	sample           TrendSampler
	previous         *collect.TrendCounters
	points           []trendObservation
	started, now     time.Time
	paused, inFlight bool
	generation       uint64
	err              error
}

func (m Model) trendTickCommand() tea.Cmd {
	if m.trends.sample == nil {
		return nil
	}
	return tea.Tick(trendPeriod, func(at time.Time) tea.Msg { return trendTick(at) })
}

func (m Model) onTrendTick(at time.Time) (tea.Model, tea.Cmd) {
	if m.trends.sample == nil {
		return m, nil
	}
	next := m.trendTickCommand()
	if m.trends.paused {
		return m, next
	}
	m.trends.advance(at)
	m.rebuildTrends()
	if m.trends.inFlight {
		return m, next
	}
	m.trends.inFlight = true
	sample, ctx, generation := m.trends.sample, m.ctx, m.trends.generation
	command := func() tea.Msg {
		counters, err := sample(ctx)
		return trendMessage{counters: counters, at: time.Now(), err: err, generation: generation}
	}
	return m, tea.Batch(next, command)
}

func (m Model) onTrendSample(msg trendMessage) (tea.Model, tea.Cmd) {
	m.trends.inFlight = false
	if m.trends.paused || msg.generation != m.trends.generation {
		return m, nil
	}
	m.trends.advance(msg.at)
	m.trends.err = msg.err
	point := trendObservation{TrendPoint: collect.TrendPoint{At: msg.at}}
	if msg.err != nil {
		m.trends.previous = nil
	} else {
		if before := m.trends.previous; before != nil && msg.counters.At.Sub(before.At) <= 2*trendPeriod {
			point.TrendPoint, point.valid = collect.TrendDelta(*before, msg.counters)
		}
		if !point.valid {
			point.At = msg.counters.At
		}
		m.trends.previous = &msg.counters
	}
	m.trends.points = append(m.trends.points, point)
	// The visible five-minute window has at most 151 scheduled samples.
	if len(m.trends.points) > 152 {
		m.trends.points = m.trends.points[len(m.trends.points)-152:]
	}
	m.rebuildTrends()
	return m, nil
}

func (h *trendHistory) advance(at time.Time) {
	h.now = at
	if h.started.IsZero() {
		h.started = at
	}
	first := 0
	for first < len(h.points) && h.points[first].At.Before(at.Add(-trendWindow)) {
		first++
	}
	h.points = h.points[first:]
}

func (m *Model) rebuildTrends() {
	// Background telemetry never resets a filter, investigation, or kill input.
	if m.tab == 0 && !m.help && !m.inInvestigation() && m.live.stage == "" {
		m.saveCurrentOffset()
		m.rebuild()
	}
}

type trendSeries struct {
	name  string
	color lipgloss.TerminalColor
	value func(collect.TrendPoint) float64
}
type trendChart struct {
	title  string
	series []trendSeries
	bytes  bool
}

func (m Model) trendView(width, available int) string {
	h := m.trends
	state := "LIVE · every 2s · p pause"
	if h.paused {
		state = "PAUSED · p resume"
	}
	if len(h.points) > 0 {
		state += " · sample " + h.points[len(h.points)-1].At.Local().Format("15:04:05")
	}
	valid := 0
	for _, point := range h.points {
		if point.valid {
			valid++
		}
	}
	if valid < 2 {
		state += " · collecting history"
	}
	if h.err != nil {
		state += " · sample failed"
	}
	out := sectionTitle("LIVE TRENDS") + "\n" + compact(state, width) + "\n"
	if h.err != nil {
		out += lipgloss.NewStyle().Foreground(yellow).Render(compact(sanitize.Text(h.err.Error()), width)) + "\n"
	}
	end := h.now
	if end.IsZero() {
		end = time.Now()
	}
	start := end.Add(-trendWindow)
	if h.started.After(start) {
		start = h.started
	}
	if end.Sub(start) < 10*time.Second {
		start = end.Add(-10 * time.Second)
	}
	charts := []trendChart{
		{title: "QUERIES / SEC", series: []trendSeries{{"", cyan, func(p collect.TrendPoint) float64 { return p.Queries }}}},
		{title: "RUNNING THREADS", series: []trendSeries{{"", number, func(p collect.TrendPoint) float64 { return p.Running }}}},
		{title: "ROW-LOCK WAITS / SEC", series: []trendSeries{{"", yellow, func(p collect.TrendPoint) float64 { return p.LockWaits }}}},
		{title: "INNODB I/O · BYTES / SEC", bytes: true, series: []trendSeries{
			{"read", identity, func(p collect.TrendPoint) float64 { return p.ReadBytes }},
			{"write", cyan, func(p collect.TrendPoint) float64 { return p.WriteBytes }},
		}},
	}
	if width >= 100 && available >= 23 {
		tileWidth := (width - 2) / 2
		plotHeight := min(12, max(4, (available-13)/2))
		for i := 0; i < len(charts); i += 2 {
			left := renderTrendChart(charts[i], h.points, start, end, tileWidth, plotHeight)
			right := renderTrendChart(charts[i+1], h.points, start, end, tileWidth, plotHeight)
			out += lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right) + "\n"
		}
	} else {
		for _, chart := range charts {
			if len(chart.series) == 1 {
				out += compact(chart.title+" · "+trendValues(chart, h.points), width) + "\n"
			} else {
				out += chart.title + "\n"
			}
			for _, series := range chart.series {
				if len(chart.series) > 1 {
					one := trendChart{series: []trendSeries{series}, bytes: chart.bytes}
					out += compact(trendValues(one, h.points), width) + "\n"
				}
				out += lipgloss.NewStyle().Foreground(series.color).Render(trendSparkline(h.points, series, start, end, width)) + "\n"
			}
		}
	}
	out += padBetween(start.Local().Format("15:04:05"), end.Local().Format("15:04:05"), width) + "\n"
	out += compact("Up to 5m · gaps = unobserved · physical InnoDB I/O · r refresh diagnostics", width)
	return out
}

func trendFormat(value float64, bytes bool) string {
	if bytes {
		return humanBytes(uint64(value)) + "/s"
	}
	return fmt.Sprintf("%.1f", value)
}

func trendValues(chart trendChart, points []trendObservation) string {
	values := []string{}
	for _, series := range chart.series {
		peak := 0.0
		observed := false
		for _, p := range points {
			if p.valid {
				observed = true
				peak = math.Max(peak, series.value(p.TrendPoint))
			}
		}
		current := "—"
		if len(points) > 0 && points[len(points)-1].valid {
			current = trendFormat(series.value(points[len(points)-1].TrendPoint), chart.bytes)
		}
		label := series.name
		if label != "" {
			label += " "
		}
		peakText := "—"
		if observed {
			peakText = trendFormat(peak, chart.bytes)
		}
		values = append(values, label+current+" · peak "+peakText)
	}
	return strings.Join(values, "  ")
}

func renderTrendChart(chart trendChart, points []trendObservation, start, end time.Time, width, height int) string {
	inner := width - 4
	// Each series has a colored value/legend. The I/O series share one y scale.
	body := ""
	for _, series := range chart.series {
		one := trendChart{series: []trendSeries{series}, bytes: chart.bytes}
		body += lipgloss.NewStyle().Foreground(series.color).Render(compact(trendValues(one, points), inner)) + "\n"
	}
	if len(chart.series) == 1 {
		body += "\n"
	}
	body += trendPlot(chart, points, start, end, inner, height)
	return panelBox(chart.title, body, width)
}

// Sparklines keep time buckets (including gaps), and retain peaks when several
// observations share a terminal column. Zero is a visible baseline glyph.
func trendSparkline(points []trendObservation, series trendSeries, start, end time.Time, width int) string {
	values := make([]float64, width)
	seen := make([]bool, width)
	peak := 0.0
	previousX := -1
	previousValue := 0.0
	var previousAt time.Time
	for _, p := range points {
		if !p.valid || p.At.Before(start) || p.At.After(end) {
			previousX = -1
			continue
		}
		x := trendX(p.At, start, end, width)
		v := series.value(p.TrendPoint)
		values[x] = math.Max(values[x], v)
		seen[x] = true
		peak = math.Max(peak, v)
		if previousX >= 0 && p.At.Sub(previousAt) <= 2*trendPeriod {
			for column := previousX + 1; column < x; column++ {
				fraction := float64(column-previousX) / float64(x-previousX)
				values[column] = math.Max(values[column], previousValue+(v-previousValue)*fraction)
				seen[column] = true
			}
		}
		previousX, previousValue, previousAt = x, v, p.At
	}
	levels := []rune("▁▂▃▄▅▆▇█")
	var out strings.Builder
	for x, v := range values {
		if !seen[x] {
			out.WriteByte(' ')
			continue
		}
		level := 0
		if peak > 0 {
			level = int(math.Round(v / peak * 7))
		}
		out.WriteRune(levels[level])
	}
	return out.String()
}

func trendX(at, start, end time.Time, width int) int {
	if !end.After(start) {
		return 0
	}
	return min(width-1, max(0, int(at.Sub(start).Seconds()/end.Sub(start).Seconds()*float64(width-1))))
}

// Braille cells provide 2×4 addressable dots using ordinary terminal text.
// Join only adjacent successful samples; a missing interval stays blank.
func trendPlot(chart trendChart, points []trendObservation, start, end time.Time, width, height int) string {
	peak := 0.0
	for _, p := range points {
		if p.valid {
			for _, s := range chart.series {
				peak = math.Max(peak, s.value(p.TrendPoint))
			}
		}
	}
	scale := math.Max(peak, 1)
	axisTop := trendFormat(scale, chart.bytes)
	if chart.bytes {
		axisTop = humanBytes(uint64(scale))
	}
	labelWidth := min(width/3, max(8, lipgloss.Width(axisTop)+1))
	plotWidth := max(1, width-labelWidth)
	dotsW, dotsH := plotWidth*2, height*4
	masks := make([][]byte, len(chart.series))
	braille := [4][2]byte{{1, 8}, {2, 16}, {4, 32}, {64, 128}}
	for si, series := range chart.series {
		masks[si] = make([]byte, plotWidth*height)
		put := func(x, y int) { masks[si][(y/4)*plotWidth+x/2] |= braille[y%4][x%2] }
		prevX, prevY := -1, 0
		var prevAt time.Time
		for _, p := range points {
			if !p.valid || p.At.Before(start) || p.At.After(end) {
				prevX = -1
				continue
			}
			x := trendX(p.At, start, end, dotsW)
			y := dotsH - 1 - int(math.Round(series.value(p.TrendPoint)/scale*float64(dotsH-1)))
			put(x, y)
			if prevX >= 0 && p.At.Sub(prevAt) <= 2*trendPeriod {
				steps := max(x-prevX, int(math.Abs(float64(y-prevY))))
				for step := 1; step < steps; step++ {
					fraction := float64(step) / float64(steps)
					put(prevX+int(math.Round(float64(x-prevX)*fraction)), prevY+int(math.Round(float64(y-prevY)*fraction)))
				}
			}
			prevX, prevY, prevAt = x, y, p.At
		}
	}
	var out strings.Builder
	for y := 0; y < height; y++ {
		label := ""
		if y == 0 {
			label = axisTop
		} else if y == height-1 {
			label = "0"
		}
		out.WriteString(lipgloss.NewStyle().Width(labelWidth).Render(compact(label, labelWidth-1)))
		for x := 0; x < plotWidth; x++ {
			mask := byte(0)
			color := lipgloss.TerminalColor(text)
			for si, series := range chart.series {
				if bits := masks[si][y*plotWidth+x]; bits != 0 {
					mask |= bits
					color = series.color
				}
			}
			if mask == 0 {
				out.WriteByte(' ')
			} else {
				out.WriteString(lipgloss.NewStyle().Foreground(color).Render(string(rune(0x2800) + rune(mask))))
			}
		}
		if y < height-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}
