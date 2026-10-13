package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/internal/topics"
)

// Three planted groups in 3 dims; invariant-* is large enough to be chunked
// when defaultChunkCap is lowered, but here we just check the flat case plus
// one chunked family via a long key list.
func topicsFixture(t *testing.T) (string, Options) {
	t.Helper()
	var recs []*memory.Memory
	vecs := map[string][]float32{}
	add := func(key string, remembered bool, v []float32) {
		m := &memory.Memory{Key: key, Body: "b " + key, FirstSeen: "2026-06-01T00:00:00Z"}
		if !remembered {
			m.DecidedAt = "2026-06-02T00:00:00Z"
			m.Reason = "r"
		}
		recs = append(recs, m)
		vecs[key] = v
	}
	add("invariant-a", true, []float32{1, 0, 0})
	add("invariant-b", false, []float32{0.99, 0.1, 0})
	add("z3-c", true, []float32{0, 1, 0})
	add("z3-d", true, []float32{0.1, 0.99, 0})
	add("z3-g", true, []float32{0.05, 0.99, 0.05})
	add("avoid-e", false, []float32{0, 0, 1})
	add("avoid-f", true, []float32{0, 0.1, 0.99})
	recs = append(recs, &memory.Memory{Key: "avoid-novector", Body: "x", FirstSeen: "2026-06-01T00:00:00Z"})
	path := writeMemoriesFixture(t, recs)
	labels := []topics.Label{
		{Text: "Z3 things", Anchors: []string{"z3-c", "z3-d", "z3-g"}, LabeledBy: "human", At: "2026-10-01T00:00:00Z"},
		{Text: "Orphan label", Anchors: []string{"gone-1", "gone-2", "gone-3"}, LabeledBy: "llm:x"},
	}
	return path, Options{TopicK: 3, Labels: labels, Vectors: vecs, Model: "fake"}
}

func TestWalkWith_BuildsTopicsAndMemberPages(t *testing.T) {
	path, opts := topicsFixture(t)
	s, err := WalkWith(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	if s.Topics == nil || len(s.Topics.Clusters) != 3 || s.Topics.Docs != 7 {
		t.Fatalf("Topics = %+v", s.Topics)
	}
	seen := map[string]int{}
	var z3 *Topic
	for _, tp := range s.Topics.Clusters {
		for _, m := range tp.Members {
			seen[m.Key]++
			if m.Page != "/prefix/"+memory.PrefixOf(m.Key)+"/" {
				t.Errorf("member %s page = %q", m.Key, m.Page)
			}
		}
		if tp.Label == "Z3 things" {
			z3 = tp
		}
		if tp.Size == 2 && tp.Members[0].Remembered == false && tp.Members[1].Remembered == true {
			t.Errorf("members of %q not remembered-first: %+v", tp.Label, tp.Members)
		}
	}
	for _, k := range []string{"invariant-a", "invariant-b", "z3-c", "z3-d", "z3-g", "avoid-e", "avoid-f"} {
		if seen[k] != 1 {
			t.Errorf("key %s appears in %d clusters, want 1", k, seen[k])
		}
	}
	if seen["avoid-novector"] != 0 {
		t.Error("a key without a vector must not be clustered")
	}
	if z3 == nil || z3.Unlabeled || z3.LabeledBy != "human" {
		t.Fatalf("Z3 label not attached: %+v", z3)
	}
	if s.Topics.Unlabeled != 2 {
		t.Errorf("Unlabeled = %d, want 2", s.Topics.Unlabeled)
	}
	if len(s.Topics.Orphaned) != 1 || s.Topics.Orphaned[0] != "Orphan label" {
		t.Errorf("Orphaned = %v", s.Topics.Orphaned)
	}
}

func TestWalk_WithoutVectorsHasNoTopics(t *testing.T) {
	path, _ := topicsFixture(t)
	s, err := Walk(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Topics != nil {
		t.Error("Walk without vectors should leave Topics nil")
	}
}

func TestRender_TopicsPagesAndLinks(t *testing.T) {
	path, opts := topicsFixture(t)
	s, err := WalkWith(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "site")
	if err := Render(s, out); err != nil {
		t.Fatal(err)
	}
	idx, err := os.ReadFile(filepath.Join(out, "topics", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(idx)
	for _, want := range []string{`<svg class="dendro"`, `href="../topics/1/index.html"`, `Z3 things`, `2 of 3 clusters are unlabeled`, `<em>Orphan label</em>`, `href="../dashboard/index.html"`} {
		if !strings.Contains(page, want) {
			t.Errorf("topics page missing %q", want)
		}
	}
	var z3Index string
	for _, tp := range s.Topics.Clusters {
		if tp.Label == "Z3 things" {
			z3Index = tp.Path
		}
	}
	tp, err := os.ReadFile(filepath.Join(out, z3Index, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tp), `href="../../prefix/z3/index.html#z3-c"`) {
		t.Errorf("topic page member link wrong:\n%s", tp)
	}
	if !strings.Contains(string(tp), "Written by human") {
		t.Error("topic page should show label provenance")
	}

	s.BaseURL = "/x"
	if err := Render(s, out); err != nil {
		t.Fatal(err)
	}
	idx, _ = os.ReadFile(filepath.Join(out, "topics", "index.html"))
	if !strings.Contains(string(idx), `href="/x/topics/1/"`) {
		t.Errorf("base-url topics link missing:\n%s", idx)
	}
}

func TestRender_TopicsEmptyStateWhenNil(t *testing.T) {
	path, _ := topicsFixture(t)
	s, _ := Walk(path)
	out := filepath.Join(t.TempDir(), "site")
	if err := Render(s, out); err != nil {
		t.Fatal(err)
	}
	idx, err := os.ReadFile(filepath.Join(out, "topics", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(idx), "No topic clusters yet") {
		t.Error("expected empty state")
	}
}

func TestWalkWith_ClusteringErrorFailsTheBuild(t *testing.T) {
	path, opts := topicsFixture(t)
	opts.Vectors["z3-c"] = []float32{1, 0} // wrong size: a real error, not "too small"
	if _, err := WalkWith(path, opts); err == nil {
		t.Fatal("a vector-size mismatch must fail the build, not render the empty state")
	}
	// Fewer than two docs with vectors is the legitimate empty state.
	opts.Vectors = map[string][]float32{"z3-c": {0, 1, 0}}
	s, err := WalkWith(path, opts)
	if err != nil || s.Topics != nil {
		t.Errorf("single doc: err=%v topics=%v, want nil/nil", err, s.Topics)
	}
}
