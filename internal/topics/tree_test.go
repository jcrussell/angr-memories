package topics

import (
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func sortedMembers(ms [][]int) [][]int {
	out := make([][]int, len(ms))
	for i, m := range ms {
		c := append([]int(nil), m...)
		sort.Ints(c)
		out[i] = c
	}
	return out
}

func TestCutTree_SixPoints(t *testing.T) {
	docs := pointDocs(sixPoints, 2)
	merges := wardLinkage(docs)
	n := len(docs)

	t.Run("K=3", func(t *testing.T) {
		root, members := cutTree(merges, n, 3)
		want := [][]int{{4, 5}, {0, 1}, {2, 3}} // root = (8,9): 8={4,5} left, 9=(6,7)
		if diff := cmp.Diff(want, sortedMembers(members)); diff != "" {
			t.Errorf("members mismatch (-want +got):\n%s", diff)
		}
		wantTree := &Node{
			Height: merges[4].Height, Size: 6, Leaf: -1,
			Left: &Node{Size: 2, Leaf: 0},
			Right: &Node{
				Height: merges[3].Height, Size: 4, Leaf: -1,
				Left:  &Node{Size: 2, Leaf: 1},
				Right: &Node{Size: 2, Leaf: 2},
			},
		}
		if diff := cmp.Diff(wantTree, root); diff != "" {
			t.Errorf("tree mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("K=n", func(t *testing.T) {
		root, members := cutTree(merges, n, n)
		want := [][]int{{4}, {5}, {0}, {1}, {2}, {3}}
		if diff := cmp.Diff(want, members); diff != "" {
			t.Errorf("members mismatch (-want +got):\n%s", diff)
		}
		// Full tree: 5 internal nodes, 6 leaves, sizes consistent.
		var internal, leaves int
		var walk func(nd *Node) int
		walk = func(nd *Node) int {
			if nd.Left == nil {
				leaves++
				if nd.Size != 1 || nd.Height != 0 {
					t.Errorf("leaf %d: size %d height %g", nd.Leaf, nd.Size, nd.Height)
				}
				return 1
			}
			internal++
			s := walk(nd.Left) + walk(nd.Right)
			if s != nd.Size {
				t.Errorf("internal node size %d, children sum %d", nd.Size, s)
			}
			return s
		}
		walk(root)
		if internal != 5 || leaves != 6 {
			t.Errorf("internal=%d leaves=%d, want 5/6", internal, leaves)
		}
	})

	t.Run("K=1", func(t *testing.T) {
		root, members := cutTree(merges, n, 1)
		want := [][]int{{0, 1, 2, 3, 4, 5}}
		if diff := cmp.Diff(want, sortedMembers(members)); diff != "" {
			t.Errorf("members mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff(&Node{Size: 6, Leaf: 0}, root); diff != "" {
			t.Errorf("root mismatch (-want +got):\n%s", diff)
		}
	})
}

func TestCutTree_TwelvePointsSizes(t *testing.T) {
	docs := pointDocs(twelvePoints, 3)
	merges := wardLinkage(docs)
	for k := 1; k <= len(docs); k++ {
		root, members := cutTree(merges, len(docs), k)
		if len(members) != k {
			t.Fatalf("K=%d: %d leaves", k, len(members))
		}
		total := 0
		var walk func(nd *Node) int
		walk = func(nd *Node) int {
			if nd.Left == nil {
				total += nd.Size
				if nd.Size != len(members[nd.Leaf]) {
					t.Errorf("K=%d leaf %d: size %d vs %d members", k, nd.Leaf, nd.Size, len(members[nd.Leaf]))
				}
				return nd.Size
			}
			if nd.Leaf != -1 {
				t.Errorf("K=%d: internal node has Leaf=%d", k, nd.Leaf)
			}
			if s := walk(nd.Left) + walk(nd.Right); s != nd.Size {
				t.Errorf("K=%d: internal size %d, children %d", k, nd.Size, s)
			}
			return nd.Size
		}
		walk(root)
		if total != len(docs) {
			t.Errorf("K=%d: leaves cover %d docs", k, total)
		}
	}
}

func TestCutMinSize_SixPoints(t *testing.T) {
	docs := pointDocs(sixPoints, 2)
	merges := wardLinkage(docs)
	n := len(docs)

	// Three pairs, minimum 2: identical to the K=3 cut, tree included.
	root, members := cutMinSize(merges, n, 2, docs)
	wantRoot, wantMembers := cutTree(merges, n, 3)
	if diff := cmp.Diff(sortedMembers(wantMembers), sortedMembers(members)); diff != "" {
		t.Errorf("members (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantRoot, root); diff != "" {
		t.Errorf("tree (-want +got):\n%s", diff)
	}

	// Minimum 3: the far pair {4,5} is a splinter at the root, the other
	// four are below 2*3 so they are one leaf; everything ends up in it.
	root, members = cutMinSize(merges, n, 3, docs)
	if len(members) != 1 || len(members[0]) != 6 {
		t.Fatalf("members = %v", members)
	}
	if diff := cmp.Diff(&Node{Size: 6, Leaf: 0}, root); diff != "" {
		t.Errorf("tree (-want +got):\n%s", diff)
	}

	// Minimum 1 never folds anything: one leaf per point.
	_, members = cutMinSize(merges, n, 1, docs)
	if len(members) != n {
		t.Errorf("minSize=1: %d leaves", len(members))
	}
	// Minimum 0 is treated as 1.
	_, members = cutMinSize(merges, n, 0, docs)
	if len(members) != n {
		t.Errorf("minSize=0: %d leaves", len(members))
	}
}

// TestCutMinSize_SplinterGoesToNearestLeaf: a splinter is folded into the
// nearest leaf *under its sibling*, not into just any leaf.
func TestCutMinSize_SplinterGoesToNearestLeaf(t *testing.T) {
	// Two tight groups of 3 at x=0 and x=10; a lone point at x=12 that the
	// linkage attaches last. With minSize 3 the lone point is a splinter at
	// the root and must land in the x=10 group.
	pts := [][]float64{{0, 0}, {0, 1}, {0, 2}, {10, 0}, {10, 1}, {10, 2}, {12, 30}}
	docs := pointDocs(pts, 2)
	merges := wardLinkage(docs)
	root, members := cutMinSize(merges, len(docs), 3, docs)
	if len(members) != 2 {
		t.Fatalf("members = %v", sortedMembers(members))
	}
	for _, m := range members {
		s := append([]int(nil), m...)
		sort.Ints(s)
		if s[0] == 3 && !cmp.Equal(s, []int{3, 4, 5, 6}) {
			t.Errorf("splinter 6 should join the x=10 group: %v", s)
		}
		if s[0] == 0 && !cmp.Equal(s, []int{0, 1, 2}) {
			t.Errorf("x=0 group changed: %v", s)
		}
	}
	if root.Size != 7 || root.Left == nil || root.Left.Size+root.Right.Size != 7 {
		t.Errorf("root sizes: %+v", root)
	}
}
