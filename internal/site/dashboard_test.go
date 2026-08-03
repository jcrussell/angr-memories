package site

import (
	"strings"
	"testing"
	"time"

	"github.com/jcrussell/angr-memories/internal/memory"
)

func TestWalk_PopulatesDashboard(t *testing.T) {
	path := writeMemoriesFixture(t, []*memory.Memory{
		{Key: "invariant-alpha", Body: "sees invariant-bravo in its body", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "invariant-bravo", Body: "long enough to be a citation target", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "avoid-c", Body: "x", FirstSeen: "2026-06-01T00:00:00Z", DecidedAt: "2026-06-08T00:00:00Z", Reason: "r"},
	})
	s, err := Walk(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Dashboard == nil {
		t.Fatal("expected Walk to populate Site.Dashboard")
	}
	for name, html := range map[string]string{
		"CorpusTimeline":    string(s.Dashboard.CorpusTimeline),
		"PrefixSizes":       string(s.Dashboard.PrefixSizes),
		"SurvivalHistogram": string(s.Dashboard.SurvivalHistogram),
		"TopCited":          string(s.Dashboard.TopCited),
	} {
		if !strings.Contains(html, "<svg") && !strings.Contains(html, "no data") && !strings.Contains(html, "not enough data") {
			t.Errorf("Dashboard.%s doesn't look like a rendered chart or an empty-state message: %q", name, html)
		}
	}
}

func TestCorpusTimeline_reconstructsRunningCount(t *testing.T) {
	records := []*memory.Memory{
		{Key: "a", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "b", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "c", FirstSeen: "2026-06-02T00:00:00Z", DecidedAt: "2026-06-03T00:00:00Z"}, // opens and closes
	}
	points := corpusTimeline(records)

	// Expect three distinct days: 06-01 (+2 -> 2), 06-02 (+1 -> 3), 06-03 (-1 -> 2).
	if len(points) != 3 {
		t.Fatalf("got %d points, want 3 distinct days, got %+v", len(points), points)
	}
	if points[0].Label != "2026-06-01" || points[0].Value != 2 {
		t.Errorf("points[0] = %+v, want {2026-06-01 2}", points[0])
	}
	if points[1].Label != "2026-06-02" || points[1].Value != 3 {
		t.Errorf("points[1] = %+v, want {2026-06-02 3}", points[1])
	}
	if points[2].Label != "2026-06-03" || points[2].Value != 2 {
		t.Errorf("points[2] = %+v, want {2026-06-03 2}", points[2])
	}
}

func TestCorpusTimeline_collapsesMultipleEventsPerDay(t *testing.T) {
	records := []*memory.Memory{
		{Key: "a", FirstSeen: "2026-06-01T01:00:00Z"},
		{Key: "b", FirstSeen: "2026-06-01T02:00:00Z"},
		{Key: "c", FirstSeen: "2026-06-01T03:00:00Z"},
	}
	points := corpusTimeline(records)
	if len(points) != 1 {
		t.Fatalf("expected same-day events collapsed into one point, got %+v", points)
	}
	if points[0].Value != 3 {
		t.Errorf("collapsed point value = %v, want 3 (final running total for the day)", points[0].Value)
	}
}

func TestPrefixSizeRows_oneRowPerFamilyWithBothSegments(t *testing.T) {
	s := memory.Aggregate([]*memory.Memory{
		{Key: "invariant-a", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "invariant-b", FirstSeen: "2026-06-01T00:00:00Z", DecidedAt: "2026-06-02T00:00:00Z", Reason: "r"},
	})
	rows := prefixSizeRows(s)
	if len(rows) != 1 || rows[0].Label != "invariant" {
		t.Fatalf("expected one 'invariant' row, got %+v", rows)
	}
	if len(rows[0].Segments) != 2 || rows[0].Segments[0].Class != "remembered" || rows[0].Segments[1].Class != "forgotten" {
		t.Errorf("expected [remembered, forgotten] segments, got %+v", rows[0].Segments)
	}
}

func TestSurvivalHistogramRows_bucketsCorrectly(t *testing.T) {
	rows := survivalHistogramRows([]time.Duration{
		2 * 24 * time.Hour,   // 0-7d
		20 * 24 * time.Hour,  // 8-30d
		400 * 24 * time.Hour, // 365d+
	})
	byLabel := map[string]float64{}
	for _, r := range rows {
		byLabel[r.Label] = r.Segments[0].Value
	}
	if byLabel["0-7d"] != 1 || byLabel["8-30d"] != 1 || byLabel["365d+"] != 1 {
		t.Errorf("unexpected bucket counts: %+v", byLabel)
	}
	if byLabel["31-90d"] != 0 {
		t.Errorf("expected empty buckets to be present with count 0, got %+v", byLabel)
	}
}

func TestTopCitedRows_rankedByCitationCountDescending(t *testing.T) {
	records := []*memory.Memory{
		{Key: "invariant-popular-target-key", Body: "no citations here"},
		{Key: "invariant-citer-one", Body: "mentions invariant-popular-target-key"},
		{Key: "invariant-citer-two", Body: "also mentions invariant-popular-target-key"},
		{Key: "invariant-lonely-key-nobody-cites", Body: "nothing special"},
	}
	rows := topCitedRows(records)
	if len(rows) != 1 {
		t.Fatalf("expected exactly one cited key, got %+v", rows)
	}
	if rows[0].Label != "invariant-popular-target-key" || rows[0].Segments[0].Value != 2 {
		t.Errorf("got %+v, want invariant-popular-target-key cited by 2", rows[0])
	}
}

func TestTopCitedRows_respectsLimit(t *testing.T) {
	var records []*memory.Memory
	// One citer key that mentions every target -- gives every target
	// exactly one citer, so the ranking is a stable tie-break by key name.
	var citerBody string
	for i := 0; i < topCitedLimit+5; i++ {
		key := "invariant-target-" + string(rune('a'+i))
		records = append(records, &memory.Memory{Key: key, Body: "target body"})
		citerBody += key + " "
	}
	records = append(records, &memory.Memory{Key: "invariant-citer-of-everything", Body: citerBody})

	rows := topCitedRows(records)
	if len(rows) != topCitedLimit {
		t.Errorf("got %d rows, want capped at %d", len(rows), topCitedLimit)
	}
}
