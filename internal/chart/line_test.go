package chart

import (
	"strings"
	"testing"
)

func TestLineChart_tooFewPoints(t *testing.T) {
	for _, points := range [][]Point{nil, {{Label: "only", Value: 1}}} {
		got := string(LineChart(points, fmtInt))
		if !strings.Contains(got, "not enough data") {
			t.Errorf("points=%v: expected an empty-state message, got %q", points, got)
		}
	}
}

func TestLineChart_rendersAPolylineWithOnePointPerSample(t *testing.T) {
	points := []Point{
		{Label: "2026-06-01", Value: 10},
		{Label: "2026-06-02", Value: 20},
		{Label: "2026-06-03", Value: 15},
	}
	got := string(LineChart(points, fmtInt))
	if !strings.Contains(got, "<polyline") {
		t.Fatalf("expected a <polyline>, got %q", got)
	}
	// Extract the points="..." attribute and count coordinate pairs.
	start := strings.Index(got, `points="`) + len(`points="`)
	end := strings.Index(got[start:], `"`)
	coords := strings.Fields(got[start : start+end])
	if len(coords) != len(points) {
		t.Errorf("got %d coordinate pairs, want %d (one per point)", len(coords), len(points))
	}
}

func TestLineChart_flatSeriesDoesNotDivideByZero(t *testing.T) {
	points := []Point{{Label: "a", Value: 5}, {Label: "b", Value: 5}, {Label: "c", Value: 5}}
	got := string(LineChart(points, fmtInt))
	if !strings.Contains(got, "<svg") {
		t.Errorf("expected a valid svg for a flat (min==max) series, got %q", got)
	}
}

func TestLineChart_escapesLabels(t *testing.T) {
	points := []Point{{Label: `<script>`, Value: 1}, {Label: "b", Value: 2}}
	got := string(LineChart(points, fmtInt))
	if strings.Contains(got, "<script>") {
		t.Errorf("expected the label to be HTML-escaped, got %q", got)
	}
}
