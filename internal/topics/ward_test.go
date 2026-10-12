package topics

import (
	"math"
	"testing"
)

// planar builds docs from 2-D/3-D points, padded with zeros to dims.
func pointDocs(pts [][]float64, dims int) []Doc {
	docs := make([]Doc, len(pts))
	for i, p := range pts {
		v := make([]float32, dims)
		for j, x := range p {
			v[j] = float32(x)
		}
		docs[i] = Doc{Key: "p-" + string(rune('a'+i)), Vec: v}
	}
	return docs
}

var sixPoints = [][]float64{{0, 0}, {0, 1}, {10, 0}, {10, 1}, {0, 20}, {0, 21}}

// From: /tmp/memviz/bin/python -I -c "import numpy as np; from scipy.cluster.hierarchy import linkage; print(linkage(np.array([[0,0],[0,1],[10,0],[10,1],[0,20],[0,21]],float),'ward'))"
var sixLinkage = []Merge{
	{0, 1, 1, 2},
	{2, 3, 1, 2},
	{4, 5, 1, 2},
	{6, 7, 14.142135623730949, 4},
	{8, 9, 33.665016461206925, 6},
}

// np.round(np.random.RandomState(7).uniform(-5, 5, size=(12, 3))*8)/8 -- a 1/8
// grid so every coordinate is exact in float32 (Doc.Vec) as well as float64.
var twelvePoints = [][]float64{
	{-4.25, 2.75, -0.625},
	{2.25, 4.75, 0.375},
	{0, -4.25, -2.375},
	{0, 1.75, 3},
	{-1.25, -4.375, -2.125},
	{4.125, -2.875, -0.5},
	{4.25, -4.75, 1},
	{4.5, -2.75, 0.5},
	{4.125, -3.625, 0.25},
	{2.5, 1.75, -0.375},
	{-3, -0.125, -1.25},
	{-0.25, -1.375, 3.375},
}

// scipy.cluster.hierarchy.linkage(twelvePoints, 'ward')
var twelveLinkage = []Merge{
	{7, 8, 0.98425098425147639, 2},
	{5, 12, 1.0944937947136413, 3},
	{2, 4, 1.2808688457449497, 2},
	{6, 13, 2.3296101247490606, 4},
	{1, 9, 3.1024184114977142, 2},
	{3, 11, 3.1573327350787723, 2},
	{0, 10, 3.1966779631361053, 2},
	{16, 17, 7.1817912111116122, 4},
	{18, 19, 8.8143467332147392, 6},
	{14, 15, 9.0909753785462044, 6},
	{20, 21, 15.461393452726051, 12},
}

func assertLinkage(t *testing.T, got, want []Merge, tol float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d merges, want %d", len(got), len(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.A != w.A || g.B != w.B || g.Size != w.Size {
			t.Errorf("merge %d: got (%d,%d,size %d), want (%d,%d,size %d)", i, g.A, g.B, g.Size, w.A, w.B, w.Size)
		}
		if math.Abs(g.Height-w.Height) > tol {
			t.Errorf("merge %d: height %.17g, want %.17g", i, g.Height, w.Height)
		}
	}
}

func TestWardLinkage_SixPointsMatchesScipy(t *testing.T) {
	for _, dims := range []int{2, 5, 16} {
		got := wardLinkage(pointDocs(sixPoints, dims))
		assertLinkage(t, got, sixLinkage, 1e-9)
	}
}

func TestWardLinkage_TwelvePointsMatchesScipy(t *testing.T) {
	got := wardLinkage(pointDocs(twelvePoints, 3))
	assertLinkage(t, got, twelveLinkage, 1e-9)
}

func TestWardLinkage_Invariants(t *testing.T) {
	docs := pointDocs(twelvePoints, 3)
	n := len(docs)
	merges := wardLinkage(docs)
	seen := make([]bool, 2*n-1)
	for i, m := range merges {
		if m.A >= m.B {
			t.Errorf("merge %d: A=%d not < B=%d", i, m.A, m.B)
		}
		if m.B >= n+i {
			t.Errorf("merge %d: child id %d not yet created", i, m.B)
		}
		if seen[m.A] || seen[m.B] {
			t.Errorf("merge %d: child reused", i)
		}
		seen[m.A], seen[m.B] = true, true
		if i > 0 && m.Height < merges[i-1].Height {
			t.Errorf("merge %d: heights not monotone", i)
		}
	}
	if merges[n-2].Size != n {
		t.Errorf("root size %d, want %d", merges[n-2].Size, n)
	}
}
