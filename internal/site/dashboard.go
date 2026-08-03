package site

import (
	"html/template"
	"sort"
	"strconv"
	"time"

	"github.com/jcrussell/angr-memories/internal/chart"
	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/internal/prune"
)

// Dashboard holds the pre-rendered SVG charts for the site's /dashboard/
// page. Each field is already-rendered template.HTML (not chart data) so
// the template stays a thin layout -- all the chart-shape logic lives here
// and in internal/chart, not in html/template markup.
type Dashboard struct {
	CorpusTimeline    template.HTML
	PrefixSizes       template.HTML
	SurvivalHistogram template.HTML
	TopCited          template.HTML
}

const topCitedLimit = 15

func buildDashboard(records []*memory.Memory) *Dashboard {
	s := memory.Aggregate(records)
	return &Dashboard{
		CorpusTimeline:    chart.LineChart(corpusTimeline(records), formatCount),
		PrefixSizes:       chart.HorizontalBars(prefixSizeRows(s), formatCount),
		SurvivalHistogram: chart.HorizontalBars(survivalHistogramRows(s.SurvivalDurations), formatCount),
		TopCited:          chart.HorizontalBars(topCitedRows(records), formatCount),
	}
}

func formatCount(v float64) string { return strconv.Itoa(int(v)) }

// corpusTimeline reconstructs remembered-count-over-time from every
// record's FirstSeen (+1, opened) and DecidedAt (-1, closed) events,
// collapsed to one point per day so a single bulk sync/apply doesn't
// produce thousands of indistinguishable points.
func corpusTimeline(records []*memory.Memory) []chart.Point {
	type event struct {
		at    time.Time
		delta int
	}
	var events []event
	for _, m := range records {
		if t, err := time.Parse(time.RFC3339, m.FirstSeen); err == nil {
			events = append(events, event{t, +1})
		}
		if m.DecidedAt != "" {
			if t, err := time.Parse(time.RFC3339, m.DecidedAt); err == nil {
				events = append(events, event{t, -1})
			}
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].at.Before(events[j].at) })

	var points []chart.Point
	running := 0
	lastDay := ""
	for _, e := range events {
		running += e.delta
		day := e.at.Format("2006-01-02")
		if day == lastDay && len(points) > 0 {
			points[len(points)-1].Value = float64(running)
			continue
		}
		points = append(points, chart.Point{Label: day, Value: float64(running)})
		lastDay = day
	}
	return points
}

func prefixSizeRows(s memory.Summary) []chart.Row {
	names := make([]string, 0, len(s.ByPrefix))
	for name := range s.ByPrefix {
		names = append(names, name)
	}
	sort.Strings(names)

	rows := make([]chart.Row, len(names))
	for i, name := range names {
		pc := s.ByPrefix[name]
		rows[i] = chart.Row{
			Label: name,
			Segments: []chart.Segment{
				{Value: float64(pc.Remembered), Class: "remembered"},
				{Value: float64(pc.Forgotten), Class: "forgotten"},
			},
		}
	}
	return rows
}

// survivalBins are the buckets forgotten-memory survival durations sort
// into, upper-bound inclusive.
var survivalBins = []struct {
	label string
	max   time.Duration
}{
	{"0-7d", 7 * 24 * time.Hour},
	{"8-30d", 30 * 24 * time.Hour},
	{"31-90d", 90 * 24 * time.Hour},
	{"91-180d", 180 * 24 * time.Hour},
	{"181-365d", 365 * 24 * time.Hour},
	{"365d+", 1<<63 - 1},
}

func survivalHistogramRows(durations []time.Duration) []chart.Row {
	counts := make([]int, len(survivalBins))
	for _, d := range durations {
		for i, b := range survivalBins {
			if d <= b.max {
				counts[i]++
				break
			}
		}
	}
	rows := make([]chart.Row, len(survivalBins))
	for i, b := range survivalBins {
		rows[i] = chart.Row{Label: b.label, Segments: []chart.Segment{{Value: float64(counts[i])}}}
	}
	return rows
}

// topCitedRows ranks remembered keys by how many other remembered memories
// cite them -- a proxy for "load-bearing, risky to forget" that's cheaper
// to read at a glance than a full citation-graph node/edge diagram.
func topCitedRows(records []*memory.Memory) []chart.Row {
	g := prune.BuildGraph(records)

	type kv struct {
		key string
		n   int
	}
	all := make([]kv, 0, len(g.CitedBy))
	for k, citers := range g.CitedBy {
		all = append(all, kv{k, len(citers)})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return all[i].key < all[j].key
	})
	if len(all) > topCitedLimit {
		all = all[:topCitedLimit]
	}

	rows := make([]chart.Row, len(all))
	for i, e := range all {
		rows[i] = chart.Row{Label: e.key, Segments: []chart.Segment{{Value: float64(e.n)}}}
	}
	return rows
}
