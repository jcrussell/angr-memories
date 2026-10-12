package topics

import (
	"errors"
	"math"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// plantedDocs builds three planted groups of unit vectors in 4 dims: group g
// is one-hot e_g plus a small deterministic perturbation, normalised.
func plantedDocs() ([]Doc, [][]string) {
	groups := [][]string{
		{"alpha-a", "alpha-b", "gamma-c", "alpha-d", "delta-e"},
		{"beta-1", "beta-2", "beta-3", "nodash", "beta-5", "zeta-6", "beta-7"},
		{"omega-x", "omega-y", "omega-z"},
	}
	var docs []Doc
	for g, keys := range groups {
		for i, k := range keys {
			v := make([]float32, 4)
			v[g] = 1
			for j := range v {
				// Deterministic noise in [-0.1, 0.1], different per doc/dim.
				v[j] += float32(0.1 * math.Sin(float64(7*g+3*i+j+1)))
			}
			var norm float64
			for _, x := range v {
				norm += float64(x) * float64(x)
			}
			norm = math.Sqrt(norm)
			for j := range v {
				v[j] = float32(float64(v[j]) / norm)
			}
			docs = append(docs, Doc{Key: k, Remembered: i%2 == 0, Vec: v})
		}
	}
	return docs, groups
}

func keysOf(docs []Doc, idx []int) []string {
	out := make([]string, len(idx))
	for i, di := range idx {
		out[i] = docs[di].Key
	}
	return out
}

func findCluster(t *testing.T, res *Result, key string) Cluster {
	t.Helper()
	for _, c := range res.Clusters {
		for _, di := range c.Members {
			if res.Docs[di].Key == key {
				return c
			}
		}
	}
	t.Fatalf("no cluster contains %q", key)
	return Cluster{}
}

func TestRun_PlantedGroups(t *testing.T) {
	docs, groups := plantedDocs()
	res, err := Run(docs, Options{K: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Clusters) != 3 {
		t.Fatalf("got %d clusters", len(res.Clusters))
	}
	if len(res.Merges) != len(docs)-1 {
		t.Fatalf("got %d merges", len(res.Merges))
	}
	for i, c := range res.Clusters {
		if c.Index != i+1 {
			t.Errorf("cluster %d has Index %d", i, c.Index)
		}
	}

	// Each cluster is exactly one planted group.
	for _, g := range groups {
		c := findCluster(t, res, g[0])
		got := keysOf(docs, c.Members)
		sort.Strings(got)
		want := append([]string(nil), g...)
		sort.Strings(want)
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("group %v members (-want +got):\n%s", g, diff)
		}
	}

	// Group 0: alpha-a*, alpha-b, gamma-c*, alpha-d, delta-e*  (* = remembered)
	c0 := findCluster(t, res, "alpha-a")
	wantMembers := []string{"alpha-a", "delta-e", "gamma-c", "alpha-b", "alpha-d"}
	if diff := cmp.Diff(wantMembers, keysOf(docs, c0.Members)); diff != "" {
		t.Errorf("group 0 member order (-want +got):\n%s", diff)
	}
	wantPrefixes := []PrefixCount{{"alpha", 3}, {"delta", 1}, {"gamma", 1}}
	if diff := cmp.Diff(wantPrefixes, c0.Prefixes); diff != "" {
		t.Errorf("group 0 prefixes (-want +got):\n%s", diff)
	}
	if c0.Remembered != 3 || c0.Forgotten != 2 {
		t.Errorf("group 0 remembered/forgotten = %d/%d, want 3/2", c0.Remembered, c0.Forgotten)
	}

	// Group 1 has 7 members: exemplars capped at 6, prefixes capped at 4
	// with count desc then prefix asc ordering.
	c1 := findCluster(t, res, "beta-1")
	if len(c1.Exemplars) != 6 {
		t.Errorf("group 1 exemplars = %d, want 6", len(c1.Exemplars))
	}
	wantPrefixes1 := []PrefixCount{{"beta", 5}, {"nodash", 1}, {"zeta", 1}}
	if diff := cmp.Diff(wantPrefixes1, c1.Prefixes); diff != "" {
		t.Errorf("group 1 prefixes (-want +got):\n%s", diff)
	}
	if c1.Remembered != 4 || c1.Forgotten != 3 {
		t.Errorf("group 1 remembered/forgotten = %d/%d, want 4/3", c1.Remembered, c1.Forgotten)
	}

	// Exemplars are the members sorted by cosine to the centroid, nearest first.
	for _, c := range res.Clusters {
		cent := make([]float64, 4)
		for _, di := range c.Members {
			for j, v := range docs[di].Vec {
				cent[j] += float64(v) / float64(len(c.Members))
			}
		}
		dot := func(di int) float64 {
			var s float64
			for j, v := range docs[di].Vec {
				s += float64(v) * cent[j]
			}
			return s
		}
		want := append([]int(nil), c.Members...)
		sort.SliceStable(want, func(i, j int) bool { return dot(want[i]) > dot(want[j]) })
		if len(want) > 6 {
			want = want[:6]
		}
		if diff := cmp.Diff(want, c.Exemplars); diff != "" {
			t.Errorf("cluster %d exemplars (-want +got):\n%s", c.Index, diff)
		}
		// Every exemplar is a member.
		for _, e := range c.Exemplars {
			found := false
			for _, m := range c.Members {
				found = found || m == e
			}
			if !found {
				t.Errorf("cluster %d exemplar %d not a member", c.Index, e)
			}
		}
	}
}

func TestRun_PrefixCap(t *testing.T) {
	docs := make([]Doc, 6)
	for i := range docs {
		v := make([]float32, 4)
		v[0] = 1
		v[1] = float32(i) * 0.01
		docs[i] = Doc{Key: string(rune('a'+i)) + "-x", Vec: v}
	}
	res, err := Run(docs, Options{K: 1})
	if err != nil {
		t.Fatal(err)
	}
	want := []PrefixCount{{"a", 1}, {"b", 1}, {"c", 1}, {"d", 1}}
	if diff := cmp.Diff(want, res.Clusters[0].Prefixes); diff != "" {
		t.Errorf("prefixes (-want +got):\n%s", diff)
	}
}

func TestRun_TooSmall(t *testing.T) {
	if _, err := Run(nil, DefaultOptions()); !errors.Is(err, ErrTooSmall) {
		t.Errorf("0 docs: err = %v", err)
	}
	one := []Doc{{Key: "a", Vec: []float32{1, 0}}}
	if _, err := Run(one, DefaultOptions()); !errors.Is(err, ErrTooSmall) {
		t.Errorf("1 doc: err = %v", err)
	}
	empty := []Doc{{Key: "a"}, {Key: "b"}}
	if _, err := Run(empty, DefaultOptions()); !errors.Is(err, ErrTooSmall) {
		t.Errorf("0 dims: err = %v", err)
	}
	mismatch := []Doc{{Key: "a", Vec: []float32{1}}, {Key: "b", Vec: []float32{1, 0}}}
	if _, err := Run(mismatch, DefaultOptions()); err == nil || errors.Is(err, ErrTooSmall) {
		t.Errorf("mismatched dims: err = %v", err)
	}
}

func TestRun_KClamped(t *testing.T) {
	docs, _ := plantedDocs()
	for _, k := range []int{0, -1, 100, len(docs)} {
		res, err := Run(docs, Options{K: k})
		if err != nil {
			t.Fatalf("K=%d: %v", k, err)
		}
		if len(res.Clusters) != len(docs) {
			t.Errorf("K=%d: got %d clusters, want %d", k, len(res.Clusters), len(docs))
		}
		for _, c := range res.Clusters {
			if len(c.Members) != 1 || len(c.Exemplars) != 1 {
				t.Errorf("K=%d: cluster %d has %d members / %d exemplars", k, c.Index, len(c.Members), len(c.Exemplars))
			}
		}
	}
	res, err := Run(docs[:2], Options{K: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Clusters) != 1 || len(res.Clusters[0].Members) != 2 {
		t.Errorf("2 docs K=1: %+v", res.Clusters)
	}
	if DefaultOptions().K != DefaultK {
		t.Errorf("DefaultOptions().K = %d", DefaultOptions().K)
	}
}

func TestRun_Deterministic(t *testing.T) {
	docs, _ := plantedDocs()
	a, err := Run(docs, Options{K: 3})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Run(docs, Options{K: 3})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(a, b); diff != "" {
		t.Errorf("two runs differ (-a +b):\n%s", diff)
	}
}
