package topics

import "math"

// cutTree truncates the dendrogram described by merges (over n leaves) to k
// clusters. It returns the truncated tree rooted at merge id 2n-2 and, in
// dendrogram leaf order (Left before Right), the doc indices under each
// truncated leaf. Members are returned unsorted.
//
// With cut = (n-1) - (k-1), a node is a truncated leaf iff it is a doc or
// its merge index is < cut; the k-1 merges at or above cut form the internal
// nodes. When k == n every doc is its own cluster; when k == 1 the root
// itself is the single leaf.
func cutTree(merges []Merge, n, k int) (*Node, [][]int) {
	cut := (n - 1) - (k - 1)
	members := make([][]int, 0, k)

	isLeaf := func(id int) bool { return id < n || id-n < cut }

	var walk func(id int) *Node
	walk = func(id int) *Node {
		if isLeaf(id) {
			mem := leavesUnder(merges, n, id)
			nd := &Node{Leaf: len(members), Size: len(mem)}
			members = append(members, mem)
			return nd
		}
		m := merges[id-n]
		nd := &Node{Height: m.Height, Size: m.Size, Leaf: -1}
		nd.Left = walk(m.A)
		nd.Right = walk(m.B)
		return nd
	}
	root := walk(2*n - 2)
	return root, members
}

// leavesUnder gathers the doc ids under node id of the dendrogram described
// by merges over n leaves (A before B, depth first).
func leavesUnder(merges []Merge, n, id int) []int {
	var out []int
	stack := []int{id}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur < n {
			out = append(out, cur)
			continue
		}
		m := merges[cur-n]
		stack = append(stack, m.B, m.A)
	}
	return out
}

// cutMinSize cuts the dendrogram described by merges (over n leaves) by
// minimum topic size instead of by count: starting at the root, a node is
// split into its two children whenever both hold at least minSize docs.
// When one child is smaller than that -- a splinter -- the walk continues
// into the larger child and the splinter's docs are afterwards folded into
// whichever of that child's leaves has the nearest centroid (Euclidean,
// ties to the earlier leaf), so no cluster ever falls below minSize and no
// doc is dropped. A node below 2·minSize is a leaf outright.
//
// The returned tree has the same shape as cutTree's: internal nodes carry
// the merge Height, leaves carry Leaf = position in leaf order; Size on
// every node is the number of docs under it after splinters were folded
// in. Members are returned unsorted, one slice per leaf in leaf order.
//
// Why this and not a flat K: on a self-similar tree (merge heights that
// climb smoothly, no gap) every global criterion for K is monotone or
// depends on the range swept, while a reader's requirement is local and
// simple -- a topic must be big enough to name and small enough to read.
// Splitting while both halves clear minSize is exactly that requirement,
// and K comes out of it.
func cutMinSize(merges []Merge, n, minSize int, docs []Doc) (*Node, [][]int) {
	if minSize < 1 {
		minSize = 1
	}
	size := func(id int) int {
		if id < n {
			return 1
		}
		return merges[id-n].Size
	}
	collect := func(id int) []int { return leavesUnder(merges, n, id) }

	var members [][]int
	type splinter struct {
		docs  []int
		under *Node // the subtree whose leaves may absorb it
	}
	var splinters []splinter

	var walk func(id int) *Node
	walk = func(id int) *Node {
		if id < n || size(id) < 2*minSize {
			nd := &Node{Leaf: len(members), Size: size(id)}
			members = append(members, collect(id))
			return nd
		}
		m := merges[id-n]
		a, b := m.A, m.B
		if size(a) >= minSize && size(b) >= minSize {
			nd := &Node{Height: m.Height, Size: m.Size, Leaf: -1}
			nd.Left = walk(a)
			nd.Right = walk(b)
			return nd
		}
		small, big := a, b
		if size(a) > size(b) {
			small, big = b, a
		}
		sub := walk(big)
		splinters = append(splinters, splinter{docs: collect(small), under: sub})
		return sub
	}
	root := walk(2*n - 2)

	// Fold each splinter into the nearest leaf under the subtree it was
	// split from. Leaf centroids are taken over the leaf's own members at
	// the time, so the outcome is deterministic in walk order. The first
	// leaf seen is the fallback, so a non-finite vector (every distance NaN)
	// still lands somewhere instead of indexing members[-1].
	dims := len(docs[0].Vec)
	centroid := func(ids []int) []float64 {
		c := make([]float64, dims)
		for _, di := range ids {
			for j, v := range docs[di].Vec {
				c[j] += float64(v)
			}
		}
		inv := 1 / float64(len(ids))
		for j := range c {
			c[j] *= inv
		}
		return c
	}
	for _, sp := range splinters {
		sc := centroid(sp.docs)
		best, bestD := -1, math.Inf(1)
		var leaves func(nd *Node)
		leaves = func(nd *Node) {
			if nd.Left == nil && nd.Right == nil {
				if d := sqDist(sc, centroid(members[nd.Leaf])); best < 0 || d < bestD {
					best, bestD = nd.Leaf, d
				}
				return
			}
			leaves(nd.Left)
			leaves(nd.Right)
		}
		leaves(sp.under)
		members[best] = append(members[best], sp.docs...)
	}

	// Sizes: leaves grew by their splinters, so recompute bottom-up.
	var resize func(nd *Node) int
	resize = func(nd *Node) int {
		if nd.Left == nil && nd.Right == nil {
			nd.Size = len(members[nd.Leaf])
			return nd.Size
		}
		nd.Size = resize(nd.Left) + resize(nd.Right)
		return nd.Size
	}
	resize(root)
	return root, members
}
