package chart

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func fmtInt(v float64) string { return strconv.Itoa(int(v)) }

func TestHorizontalBars_empty(t *testing.T) {
	got := string(HorizontalBars(nil, fmtInt))
	if !strings.Contains(got, "no data") {
		t.Errorf("expected an empty-state message, got %q", got)
	}
}

func TestHorizontalBars_oneRectPerSegment(t *testing.T) {
	rows := []Row{
		{Label: "invariant", Segments: []Segment{{Value: 10, Class: "remembered"}, {Value: 3, Class: "forgotten"}}},
		{Label: "avoid", Segments: []Segment{{Value: 5, Class: "remembered"}}},
	}
	got := string(HorizontalBars(rows, fmtInt))

	if n := strings.Count(got, "<rect"); n != 3 {
		t.Errorf("got %d <rect> elements, want 3 (one per non-empty segment)", n)
	}
	if !strings.Contains(got, `class="chart-bar-remembered"`) || !strings.Contains(got, `class="chart-bar-forgotten"`) {
		t.Errorf("expected segment classes in output, got %q", got)
	}
	if !strings.Contains(got, "invariant") || !strings.Contains(got, "avoid") {
		t.Errorf("expected both labels in output, got %q", got)
	}
	if !strings.Contains(got, "13") { // invariant's total (10+3)
		t.Errorf("expected the stacked total's formatted value, got %q", got)
	}
}

func TestHorizontalBars_zeroValueSegmentEmitsNoRect(t *testing.T) {
	rows := []Row{{Label: "empty", Segments: []Segment{{Value: 0, Class: "remembered"}}}}
	got := string(HorizontalBars(rows, fmtInt))
	if strings.Contains(got, "<rect") {
		t.Errorf("expected no <rect> for a zero-value segment, got %q", got)
	}
}

func TestHorizontalBars_allZeroDoesNotDivideByZero(t *testing.T) {
	rows := []Row{
		{Label: "a", Segments: []Segment{{Value: 0}}},
		{Label: "b", Segments: []Segment{{Value: 0}}},
	}
	// Must not panic (division by zero on maxTotal) and must still render.
	got := string(HorizontalBars(rows, fmtInt))
	if !strings.Contains(got, "<svg") {
		t.Errorf("expected a valid svg even when every value is zero, got %q", got)
	}
}

func TestHorizontalBars_defaultClassWhenUnset(t *testing.T) {
	rows := []Row{{Label: "a", Segments: []Segment{{Value: 1}}}}
	got := string(HorizontalBars(rows, fmtInt))
	if !strings.Contains(got, `class="chart-bar-default"`) {
		t.Errorf("expected chart-bar-default for an unset Segment.Class, got %q", got)
	}
}

func TestHorizontalBars_escapesLabels(t *testing.T) {
	rows := []Row{{Label: `<script>alert(1)</script>`, Segments: []Segment{{Value: 1}}}}
	got := string(HorizontalBars(rows, fmtInt))
	if strings.Contains(got, "<script>") {
		t.Errorf("expected the label to be HTML-escaped, got %q", got)
	}
}

func TestHorizontalBars_truncatesLongLabels(t *testing.T) {
	long := strings.Repeat("x", maxLabelLen*2)
	rows := []Row{{Label: long, Segments: []Segment{{Value: 1}}}}
	got := string(HorizontalBars(rows, fmtInt))

	visible := regexp.MustCompile(`<text x="0"[^>]*>(.*?)<title>`).FindStringSubmatch(got)
	if visible == nil {
		t.Fatalf("expected a chart-label <text> element with a nested <title>, got %q", got)
	}
	if visible[1] == long {
		t.Errorf("expected the visible label text to be truncated, got the full label %q", visible[1])
	}
	if !strings.HasSuffix(visible[1], "…") {
		t.Errorf("expected the truncated visible label to end in an ellipsis, got %q", visible[1])
	}
}

func TestHorizontalBars_titleCarriesFullUntruncatedLabel(t *testing.T) {
	long := strings.Repeat("x", maxLabelLen*2)
	rows := []Row{{Label: long, Segments: []Segment{{Value: 1}}}}
	got := string(HorizontalBars(rows, fmtInt))

	want := "<title>" + long + "</title>"
	if !strings.Contains(got, want) {
		t.Errorf("expected the full untruncated label in a <title> element, got %q", got)
	}
}

func TestHorizontalBars_truncationIsRuneSafe(t *testing.T) {
	long := strings.Repeat("é", maxLabelLen*2)
	rows := []Row{{Label: long, Segments: []Segment{{Value: 1}}}}
	got := string(HorizontalBars(rows, fmtInt))

	if !utf8.ValidString(got) {
		t.Errorf("expected valid UTF-8 output, truncation split a multi-byte rune")
	}
}
