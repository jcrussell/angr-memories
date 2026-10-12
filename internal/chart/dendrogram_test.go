package chart

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func itoa(i int) string     { return strconv.Itoa(i) }
func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) }

// threeRowTree builds ((row0, row1) @ 1.0, row2) @ 2.5 -- two merges, three leaves.
func threeRowTree() *DendroNode {
	return &DendroNode{
		Height: 2.5, Size: 20,
		Left: &DendroNode{
			Height: 1.0, Size: 14,
			Left:  &DendroNode{Row: 0},
			Right: &DendroNode{Row: 1},
		},
		Right: &DendroNode{Row: 2},
	}
}

func threeRows() []DendroRow {
	return []DendroRow{
		{Label: "Solver & <Z3> discipline", Href: "/topics/solver/", Title: "solver, z3, constraints", Remembered: 9, Forgotten: 3},
		{Label: "cluster 7", Unlabeled: true, Remembered: 5, Forgotten: 0},
		{Label: "Paged memory", Href: "/topics/paged/", Remembered: 2, Forgotten: 4},
	}
}

func TestDendrogram_empty(t *testing.T) {
	if got := string(Dendrogram(nil, threeRows(), "x")); !strings.Contains(got, `class="chart-empty"`) {
		t.Errorf("nil root: expected chart-empty, got %q", got)
	}
	if got := string(Dendrogram(threeRowTree(), nil, "x")); !strings.Contains(got, `class="chart-empty"`) {
		t.Errorf("empty rows: expected chart-empty, got %q", got)
	}
}

func TestDendrogram_threeRows(t *testing.T) {
	got := string(Dendrogram(threeRowTree(), threeRows(), "Ward merge distance"))

	if n := strings.Count(got, `<path class="dendro-link"`); n != 2 {
		t.Errorf("got %d dendro-link paths, want 2", n)
	}
	if n := strings.Count(got, `<circle class="dendro-node"`); n != 2 {
		t.Errorf("got %d dendro-node circles, want 2", n)
	}
	if n := strings.Count(got, `<g class="dendro-row"`); n != 3 {
		t.Errorf("got %d dendro-row groups, want 3", n)
	}
	if n := strings.Count(got, `<a href="`); n != 2 {
		t.Errorf("got %d <a href> wrappers, want 2 (only rows with Href)", n)
	}
	if n := strings.Count(got, `</a>`); n != 2 {
		t.Errorf("got %d </a> closers, want 2", n)
	}
	if !strings.Contains(got, `<a href="/topics/solver/">`) {
		t.Errorf("expected the solver row's href, got %q", got)
	}
	if !strings.Contains(got, "Solver &amp; &lt;Z3&gt; discipline") {
		t.Errorf("expected the label to be HTML-escaped, got %q", got)
	}
	if strings.Contains(got, "<Z3>") {
		t.Errorf("found an unescaped label fragment in %q", got)
	}
	if !strings.Contains(got, `class="dendro-label unlabeled"`) {
		t.Errorf("expected the unlabeled row to carry the unlabeled class, got %q", got)
	}
	if n := strings.Count(got, `class="dendro-label unlabeled"`); n != 1 {
		t.Errorf("got %d unlabeled labels, want 1", n)
	}
	if n := strings.Count(got, `class="chart-bar-forgotten"`); n != 2 {
		t.Errorf("got %d forgotten rects, want 2 (row with Forgotten=0 emits none)", n)
	}
	if n := strings.Count(got, `class="chart-bar-remembered"`); n != 3 {
		t.Errorf("got %d remembered rects, want 3", n)
	}
	if !strings.Contains(got, ">12 · 75% kept<") {
		t.Errorf("expected value text '12 · 75%% kept' for 9/3, got %q", got)
	}
	if !strings.Contains(got, ">5 · 100% kept<") {
		t.Errorf("expected value text '5 · 100%% kept' for 5/0, got %q", got)
	}
	if !strings.Contains(got, ">6 · 33% kept<") {
		t.Errorf("expected value text '6 · 33%% kept' for 2/4, got %q", got)
	}
	// Every row and every node carries a <title>: 3 rows + 2 nodes.
	if n := strings.Count(got, "<title>"); n != 5 {
		t.Errorf("got %d <title> elements, want 5 (3 rows + 2 nodes)", n)
	}
	if !strings.Contains(got, "<title>Merges 14 memories at distance 1.00</title>") {
		t.Errorf("expected a node title for the first merge, got %q", got)
	}
	if !strings.Contains(got, "<title>solver, z3, constraints</title>") {
		t.Errorf("expected the row Title to be used verbatim, got %q", got)
	}
	if !strings.Contains(got, ">Ward merge distance<") {
		t.Errorf("expected the x-axis label in output, got %q", got)
	}
	wantH := dendroTop + dendroRow*3 + 40
	if !strings.Contains(got, `viewBox="0 0 `+itoa(dendroW)+` `+itoa(wantH)+`"`) {
		t.Errorf("expected viewBox height %d, got %q", wantH, got)
	}
	if !strings.Contains(got, `aria-label="topic dendrogram"`) {
		t.Errorf("expected the aria-label on the svg, got %q", got)
	}
}

func TestDendrogram_escapesHrefAndAxisLabel(t *testing.T) {
	rows := threeRows()
	rows[0].Href = `/x?a=1&b="2"`
	got := string(Dendrogram(threeRowTree(), rows, `dist <&>`))
	if !strings.Contains(got, `<a href="/x?a=1&amp;b=&#34;2&#34;">`) {
		t.Errorf("expected the href to be escaped, got %q", got)
	}
	if !strings.Contains(got, ">dist &lt;&amp;&gt;<") {
		t.Errorf("expected the axis label to be escaped, got %q", got)
	}
}

func TestDendrogram_truncatedLabelGoesIntoTitle(t *testing.T) {
	long := strings.Repeat("x", maxLabelLen*2)
	rows := threeRows()
	rows[0].Label = long
	rows[0].Title = "terms"
	got := string(Dendrogram(threeRowTree(), rows, "d"))
	if !strings.Contains(got, "<title>"+long+"\nterms</title>") {
		t.Errorf("expected the full label prepended to the row title, got %q", got)
	}
	m := regexp.MustCompile(`<text class="dendro-label"[^>]*>([^<]*)</text>`).FindStringSubmatch(got)
	if m == nil || m[1] == long || !strings.HasSuffix(m[1], "…") {
		t.Errorf("expected a truncated visible label ending in an ellipsis, got %v", m)
	}
}

func TestDendrogram_degenerateHeightsDoNotDivideByZero(t *testing.T) {
	root := &DendroNode{
		Height: 0, Size: 3,
		Left:  &DendroNode{Height: 0, Size: 2, Left: &DendroNode{Row: 0}, Right: &DendroNode{Row: 1}},
		Right: &DendroNode{Row: 2},
	}
	rows := []DendroRow{{Label: "a"}, {Label: "b"}, {Label: "c"}} // all-zero bars too
	got := string(Dendrogram(root, rows, "d"))
	if !strings.Contains(got, "<svg") {
		t.Fatalf("expected an svg, got %q", got)
	}
	if strings.Contains(got, "NaN") || strings.Contains(got, "Inf") {
		t.Errorf("expected no NaN/Inf coordinates, got %q", got)
	}
	if !strings.Contains(got, ">0 · 0% kept<") {
		t.Errorf("expected '0 · 0%% kept' for an empty row, got %q", got)
	}
	if strings.Contains(got, "chart-bar-") {
		t.Errorf("expected no bar rects when every value is zero, got %q", got)
	}
}

func TestDendrogram_rowsFollowLeafOrder(t *testing.T) {
	// Leaf visit order is row 2, row 0, row 1.
	root := &DendroNode{
		Height: 3, Size: 9,
		Left: &DendroNode{Row: 2},
		Right: &DendroNode{
			Height: 1, Size: 6,
			Left:  &DendroNode{Row: 0},
			Right: &DendroNode{Row: 1},
		},
	}
	rows := []DendroRow{{Label: "zero"}, {Label: "one"}, {Label: "two"}}
	got := string(Dendrogram(root, rows, "d"))

	labels := regexp.MustCompile(`<text class="dendro-label"[^>]*y="([0-9.]+)"[^>]*>([^<]*)</text>`).FindAllStringSubmatch(got, -1)
	if len(labels) != 3 {
		t.Fatalf("got %d labels, want 3: %q", len(labels), got)
	}
	if order := labels[0][2] + "," + labels[1][2] + "," + labels[2][2]; order != "two,zero,one" {
		t.Errorf("rows rendered in order %q, want leaf order two,zero,one", order)
	}
	// The first-visited leaf sits in the first row slot: y = TOP + ROW/2 + 4 (text baseline offset).
	wantY := strings.TrimSuffix(strings.TrimSuffix(ftoa(dendroTop+dendroRow/2.0+4), "0"), ".")
	if gotY := strings.TrimSuffix(strings.TrimSuffix(labels[0][1], "0"), "."); gotY != wantY {
		t.Errorf("first label y = %q, want %q", labels[0][1], wantY)
	}
	// Sanity: the first-visited leaf is also the first row group in document order.
	if strings.Index(got, ">two<") > strings.Index(got, ">zero<") {
		t.Errorf("expected 'two' to precede 'zero' in document order")
	}
}
