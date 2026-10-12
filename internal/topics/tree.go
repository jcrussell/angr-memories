package topics

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

	// collect gathers the doc ids under node id.
	collect := func(id int) []int {
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

	var walk func(id int) *Node
	walk = func(id int) *Node {
		if isLeaf(id) {
			mem := collect(id)
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
