package topics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// fakeResult builds a Result whose clusters are the given key groups; the
// first key of each group is remembered, the rest alternate.
func fakeResult(groups ...[]string) *Result {
	res := &Result{}
	for ci, g := range groups {
		c := Cluster{Index: ci + 1}
		for i, k := range g {
			res.Docs = append(res.Docs, Doc{Key: k, Remembered: i%2 == 0})
			c.Members = append(c.Members, len(res.Docs)-1)
		}
		res.Clusters = append(res.Clusters, c)
	}
	return res
}

func TestAssign_PluralityAndThreshold(t *testing.T) {
	res := fakeResult(
		[]string{"a1", "a2", "a3", "a4"},
		[]string{"b1", "b2", "b3", "b4"},
		[]string{"c1", "c2"},
	)
	labels := []Label{
		{Text: "A", Anchors: []string{"a1", "a2", "a3", "b1"}},      // 3 hits on 0, 1 on 1
		{Text: "weak", Anchors: []string{"b1", "b2", "zzz", "yyy"}}, // 2 hits: below threshold
		{Text: "C", Anchors: []string{"c1", "c2", "a4"}},            // never reaches 3
		{Text: "B", Anchors: []string{"b2", "b3", "b4", "b4"}},      // duplicate anchor counts once -> 3
	}
	by, orphaned := Assign(labels, res)
	if len(by) != 3 {
		t.Fatalf("len(byCluster) = %d", len(by))
	}
	if by[0].Label == nil || by[0].Label.Text != "A" || by[0].Hits != 3 {
		t.Errorf("cluster 0: %+v", by[0])
	}
	if by[1].Label == nil || by[1].Label.Text != "B" || by[1].Hits != 3 {
		t.Errorf("cluster 1: %+v", by[1])
	}
	if by[2].Label != nil {
		t.Errorf("cluster 2 should be unlabeled, got %+v", by[2])
	}
	wantOrphans := []Label{labels[1], labels[2]}
	if diff := cmp.Diff(wantOrphans, orphaned); diff != "" {
		t.Errorf("orphaned (-want +got):\n%s", diff)
	}
	// Returned labels are copies, so later edits to the file cannot shift them.
	if by[0].Label == &labels[0] {
		t.Errorf("cluster 0 label pointer must not alias the input slice")
	}
	if diff := cmp.Diff(labels[0], *by[0].Label); diff != "" {
		t.Errorf("cluster 0 label (-want +got):\n%s", diff)
	}
}

func TestAssign_GreedyConflict(t *testing.T) {
	res := fakeResult(
		[]string{"a1", "a2", "a3", "a4", "a5"},
		[]string{"b1", "b2", "b3"},
	)
	labels := []Label{
		{Text: "three", Anchors: []string{"a1", "a2", "a3"}},
		{Text: "four", Anchors: []string{"a1", "a2", "a3", "a4"}},
	}
	by, orphaned := Assign(labels, res)
	if by[0].Label == nil || by[0].Label.Text != "four" || by[0].Hits != 4 {
		t.Errorf("cluster 0: %+v, want 'four' with 4 hits", by[0])
	}
	if by[1].Label != nil {
		t.Errorf("cluster 1 should be unlabeled")
	}
	if diff := cmp.Diff([]Label{labels[0]}, orphaned); diff != "" {
		t.Errorf("orphaned (-want +got):\n%s", diff)
	}

	// Equal hits: higher fraction wins; equal fraction: earlier label wins.
	labels = []Label{
		{Text: "diluted", Anchors: []string{"a1", "a2", "a3", "x", "y"}},
		{Text: "tight", Anchors: []string{"a1", "a2", "a3"}},
	}
	by, orphaned = Assign(labels, res)
	if by[0].Label == nil || by[0].Label.Text != "tight" {
		t.Errorf("cluster 0: %+v, want 'tight'", by[0])
	}
	if len(orphaned) != 1 || orphaned[0].Text != "diluted" {
		t.Errorf("orphaned = %+v", orphaned)
	}

	labels = []Label{
		{Text: "first", Anchors: []string{"a1", "a2", "a3"}},
		{Text: "second", Anchors: []string{"a3", "a4", "a5"}},
	}
	by, orphaned = Assign(labels, res)
	if by[0].Label == nil || by[0].Label.Text != "first" {
		t.Errorf("cluster 0: %+v, want 'first'", by[0])
	}
	if len(orphaned) != 1 || orphaned[0].Text != "second" {
		t.Errorf("orphaned = %+v", orphaned)
	}

	// One label matching two clusters attaches to the one with more hits.
	res = fakeResult(
		[]string{"a1", "a2", "a3"},
		[]string{"a4", "a5", "a6", "a7"},
	)
	labels = []Label{{Text: "spread", Anchors: []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7"}}}
	by, orphaned = Assign(labels, res)
	if by[0].Label != nil || by[1].Label == nil || by[1].Hits != 4 || len(orphaned) != 0 {
		t.Errorf("spread: by=%+v orphaned=%+v", by, orphaned)
	}
}

func TestAssign_Empty(t *testing.T) {
	res := fakeResult([]string{"a1"}, []string{"b1"})
	by, orphaned := Assign(nil, res)
	if len(by) != 2 || by[0].Label != nil || by[1].Label != nil || len(orphaned) != 0 {
		t.Errorf("by=%+v orphaned=%+v", by, orphaned)
	}
	by, orphaned = Assign([]Label{{Text: "none"}}, res)
	if len(by) != 2 || len(orphaned) != 1 {
		t.Errorf("by=%+v orphaned=%+v", by, orphaned)
	}
}

func TestAnchorsFor(t *testing.T) {
	var docs []Doc
	var c Cluster
	for i := 0; i < 20; i++ {
		docs = append(docs, Doc{Key: "k" + string(rune('a'+i)), Remembered: i%2 == 0})
		c.Members = append(c.Members, i)
	}
	// Exemplars: three keys, one of which (kb) is forgotten, in a deliberate order.
	c.Exemplars = []int{5, 1, 0} // kf, kb, ka
	got := AnchorsFor(c, docs)
	want := []string{"kf", "kb", "ka", "kc", "ke", "kg", "ki", "kk", "km", "ko"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("anchors (-want +got):\n%s", diff)
	}

	// Small cluster: fewer than the cap, remembered members follow exemplars,
	// forgotten non-exemplars are excluded.
	small := Cluster{Members: []int{0, 2, 1, 3}, Exemplars: []int{1, 2}}
	got = AnchorsFor(small, docs)
	if diff := cmp.Diff([]string{"kb", "kc", "ka"}, got); diff != "" {
		t.Errorf("small anchors (-want +got):\n%s", diff)
	}

	// Exemplars alone exceed the cap.
	big := Cluster{Members: c.Members, Exemplars: []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}}
	if got := AnchorsFor(big, docs); len(got) != 10 || got[9] != "kj" {
		t.Errorf("capped anchors = %v", got)
	}
}

func TestLabelsFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "labels.jsonl")

	missing, err := LoadLabels(path)
	if err != nil {
		t.Fatalf("LoadLabels(missing): %v", err)
	}
	if diff := cmp.Diff(&LabelsFile{SchemaVersion: 1}, missing); diff != "" {
		t.Errorf("missing file (-want +got):\n%s", diff)
	}

	in := &LabelsFile{SchemaVersion: 1, Labels: []Label{
		{Text: "zeta <b>&", Anchors: []string{"z1", "z2", "z3"}, LabeledBy: "human", At: "2026-10-12T00:00:00Z"},
		{Text: "alpha", Anchors: []string{"a1"}, LabeledBy: "llm:test", At: "2026-10-12T00:00:01Z"},
		{Text: "mid", Anchors: nil, LabeledBy: "human", At: "2026-10-12T00:00:02Z"},
		{Text: "alpha", Anchors: []string{"a2"}, LabeledBy: "human", At: "2026-10-12T00:00:03Z"},
	}}
	if err := WriteLabels(path, in); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if lines[0] != `{"schema_version":1}` {
		t.Errorf("header = %q", lines[0])
	}
	if len(lines) != 5 {
		t.Fatalf("got %d lines: %q", len(lines), lines)
	}
	if !strings.Contains(lines[4], "<b>&") {
		t.Errorf("HTML escaped: %q", lines[4])
	}
	if !strings.Contains(lines[3], `"anchors":[]`) {
		t.Errorf("nil anchors not written as []: %q", lines[3])
	}
	// Input is not reordered.
	if in.Labels[0].Text != "zeta <b>&" {
		t.Errorf("WriteLabels mutated input order")
	}

	out, err := LoadLabels(path)
	if err != nil {
		t.Fatal(err)
	}
	want := &LabelsFile{SchemaVersion: 1, Labels: []Label{
		in.Labels[1], in.Labels[3], // stable: alpha/llm before alpha/human
		{Text: "mid", Anchors: []string{}, LabeledBy: "human", At: "2026-10-12T00:00:02Z"},
		in.Labels[0],
	}}
	if diff := cmp.Diff(want, out); diff != "" {
		t.Errorf("round trip (-want +got):\n%s", diff)
	}

	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, err = %v", st.Mode(), err)
	}
}

func TestReadLabels_Edges(t *testing.T) {
	f, err := ReadLabels(strings.NewReader(""))
	if err != nil || f.SchemaVersion != 1 || len(f.Labels) != 0 {
		t.Errorf("empty: %+v, %v", f, err)
	}
	f, err = ReadLabels(strings.NewReader("\n  \n"))
	if err != nil || f.SchemaVersion != 1 || len(f.Labels) != 0 {
		t.Errorf("whitespace: %+v, %v", f, err)
	}
	f, err = ReadLabels(strings.NewReader(`{"schema_version":1}` + "\n"))
	if err != nil || len(f.Labels) != 0 {
		t.Errorf("header only: %+v, %v", f, err)
	}
	f, err = ReadLabels(strings.NewReader("{\"schema_version\":1}\n\n{\"label\":\"x\",\"anchors\":[\"a\"],\"labeled_by\":\"human\",\"at\":\"t\"}\n"))
	if err != nil || len(f.Labels) != 1 || f.Labels[0].Text != "x" {
		t.Errorf("blank line tolerated: %+v, %v", f, err)
	}

	_, err = ReadLabels(strings.NewReader(`{"schema_version":2}` + "\n"))
	if err == nil || !strings.Contains(err.Error(), "2") || !strings.Contains(err.Error(), "1") || !strings.Contains(err.Error(), "upgrade the tool") {
		t.Errorf("newer schema: err = %v", err)
	}
	_, err = ReadLabels(strings.NewReader(`{"label":"x","anchors":[],"labeled_by":"human","at":"t"}` + "\n"))
	if err == nil || !strings.Contains(err.Error(), "header") {
		t.Errorf("missing header: err = %v", err)
	}
	_, err = ReadLabels(strings.NewReader("not json\n"))
	if err == nil {
		t.Errorf("garbage header accepted")
	}
	_, err = ReadLabels(strings.NewReader("{\"schema_version\":1}\nnot json\n"))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("bad label line: err = %v", err)
	}
}

func TestLabelsFile_Upsert(t *testing.T) {
	f := &LabelsFile{SchemaVersion: 1, Labels: []Label{
		{Text: "old", Anchors: []string{"a1", "a2", "a3"}},
		{Text: "neighbour", Anchors: []string{"a2", "a3", "n1"}}, // shares anchors with old's cluster but is a different label
	}}
	// Relabeling the cluster currently named "old" replaces exactly that
	// label, in place, and leaves the neighbour alone even though its
	// anchors overlap.
	f.Upsert(Label{Text: "new", Anchors: []string{"a2", "a3", "a4"}}, "old")
	if len(f.Labels) != 2 || f.Labels[0].Text != "new" || f.Labels[1].Text != "neighbour" {
		t.Fatalf("after replace: %+v", f.Labels)
	}
	// An unlabeled cluster ("" replaceText) appends.
	f.Upsert(Label{Text: "third", Anchors: []string{"c1", "c2", "c3"}}, "")
	if len(f.Labels) != 3 || f.Labels[2].Text != "third" {
		t.Fatalf("after append: %+v", f.Labels)
	}
	// Same text as an existing label replaces it wherever it is.
	f.Upsert(Label{Text: "third", Anchors: []string{"z1"}}, "")
	if len(f.Labels) != 3 || f.Labels[2].Anchors[0] != "z1" {
		t.Fatalf("after same-text replace: %+v", f.Labels)
	}
	// Replacing by one text while colliding with another collapses both.
	f.Upsert(Label{Text: "neighbour", Anchors: []string{"q"}}, "new")
	if len(f.Labels) != 2 || f.Labels[0].Text != "neighbour" || f.Labels[1].Text != "third" {
		t.Fatalf("after collapse: %+v", f.Labels)
	}
	// Upsert on an empty file appends.
	e := &LabelsFile{SchemaVersion: 1}
	e.Upsert(Label{Text: "x"}, "")
	if len(e.Labels) != 1 {
		t.Fatalf("empty: %+v", e.Labels)
	}
}

func TestAssign_TieBreakPrefersHumanThenNewer(t *testing.T) {
	docs := []Doc{{Key: "a"}, {Key: "b"}, {Key: "c"}, {Key: "d"}}
	res := &Result{Docs: docs, Clusters: []Cluster{{Index: 1, Members: []int{0, 1, 2, 3}}}}
	anchors := []string{"a", "b", "c"}
	llm := Label{Text: "Alpha draft", Anchors: anchors, LabeledBy: "llm:x", At: "2026-10-12T00:00:00Z"}
	human := Label{Text: "Zulu by hand", Anchors: anchors, LabeledBy: "human", At: "2026-10-01T00:00:00Z"}
	by, orphaned := Assign([]Label{llm, human}, res)
	if by[0].Label == nil || by[0].Label.Text != "Zulu by hand" || len(orphaned) != 1 {
		t.Errorf("human label should win the tie regardless of order/spelling: %+v orphaned=%v", by[0], orphaned)
	}
	older := Label{Text: "Older", Anchors: anchors, LabeledBy: "human", At: "2026-09-01T00:00:00Z"}
	newer := Label{Text: "Newer", Anchors: anchors, LabeledBy: "human", At: "2026-10-01T00:00:00Z"}
	by, _ = Assign([]Label{older, newer}, res)
	if by[0].Label.Text != "Newer" {
		t.Errorf("newer human label should win: %+v", by[0])
	}
}

func TestAssign_ResultsSurviveLaterFileEdits(t *testing.T) {
	docs := []Doc{{Key: "a"}, {Key: "b"}, {Key: "c"}}
	res := &Result{Docs: docs, Clusters: []Cluster{{Index: 1, Members: []int{0, 1, 2}}}}
	f := &LabelsFile{SchemaVersion: 1, Labels: []Label{{Text: "first", Anchors: []string{"a", "b", "c"}}}}
	by, _ := Assign(f.Labels, res)
	f.Upsert(Label{Text: "second", Anchors: []string{"a", "b", "c"}}, "first")
	if by[0].Label.Text != "first" {
		t.Errorf("assignment must not change under a later Upsert, got %q", by[0].Label.Text)
	}
}
