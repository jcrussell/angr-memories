package topics

import (
	"math"
	"sort"
)

// wardLinkage computes the Ward linkage of docs with the nearest-neighbour
// chain algorithm over cluster centroids, then post-processes the merges
// exactly like scipy (stable sort by height, union-find relabelling) so the
// result is row-for-row comparable with
// scipy.cluster.hierarchy.linkage(X, 'ward').
//
// Ward distance between clusters A and B is
//
//	d(A,B) = sqrt(2·|A||B| / (|A|+|B|)) · ‖cA − cB‖
//
// which is scipy's height scale: two singletons merge at their Euclidean
// distance.
//
// Tie-breaking mirrors scipy's nn_chain: an empty chain restarts from the
// lowest active slot, candidates are scanned in ascending slot order with a
// strict "<" so the first minimum wins, and the merged cluster is written
// into the larger of the two slots (scipy's y) with the smaller deactivated.
func wardLinkage(docs []Doc) []Merge {
	n := len(docs)
	d := len(docs[0].Vec)

	cent := make([]float64, n*d)
	size := make([]int, n) // 0 means the slot is inactive
	for i, doc := range docs {
		row := cent[i*d : (i+1)*d]
		for j, v := range doc.Vec {
			row[j] = float64(v)
		}
		size[i] = 1
	}

	// dist2 is the squared Ward distance between active slots x and y.
	dist2 := func(x, y int) float64 {
		cx := cent[x*d : (x+1)*d]
		cy := cent[y*d : (y+1)*d]
		sx, sy := float64(size[x]), float64(size[y])
		return 2 * sx * sy / (sx + sy) * sqDist(cx, cy)
	}

	type rawMerge struct {
		a, b int // slot indices (== leaf ids) at merge time
		h    float64
		size int
	}
	raw := make([]rawMerge, 0, n-1)
	chain := make([]int, 0, n)

	for k := 0; k < n-1; k++ {
		if len(chain) == 0 {
			for i := 0; i < n; i++ {
				if size[i] > 0 {
					chain = append(chain, i)
					break
				}
			}
		}
		var x, y int
		var best float64
		for {
			x = chain[len(chain)-1]
			if len(chain) > 1 {
				y = chain[len(chain)-2]
				best = dist2(x, y)
			} else {
				best = math.Inf(1)
			}
			for i := 0; i < n; i++ {
				if size[i] == 0 || i == x {
					continue
				}
				if dd := dist2(x, i); dd < best {
					best = dd
					y = i
				}
			}
			if len(chain) > 1 && y == chain[len(chain)-2] {
				break
			}
			chain = append(chain, y)
		}
		chain = chain[:len(chain)-2]
		if x > y {
			x, y = y, x
		}
		nx, ny := size[x], size[y]
		raw = append(raw, rawMerge{a: x, b: y, h: math.Sqrt(best), size: nx + ny})

		// Merged centroid goes into slot y (scipy's convention); x dies.
		fx, fy, ft := float64(nx), float64(ny), float64(nx+ny)
		rx := cent[x*d : (x+1)*d]
		ry := cent[y*d : (y+1)*d]
		ry = ry[:len(rx)]
		for i := range rx {
			ry[i] = (fx*rx[i] + fy*ry[i]) / ft
		}
		size[x] = 0
		size[y] = nx + ny
	}

	// scipy: stable sort by height, then relabel with a union-find so each
	// row's children are the current component ids.
	sort.SliceStable(raw, func(i, j int) bool { return raw[i].h < raw[j].h })

	parent := make([]int, 2*n-1)
	for i := range parent {
		parent[i] = i
	}
	find := func(x int) int {
		root := x
		for parent[root] != root {
			root = parent[root]
		}
		for parent[x] != root {
			nxt := parent[x]
			parent[x] = root
			x = nxt
		}
		return root
	}
	merges := make([]Merge, len(raw))
	next := n
	for i, m := range raw {
		xr, yr := find(m.a), find(m.b)
		if xr > yr {
			xr, yr = yr, xr
		}
		parent[xr] = next
		parent[yr] = next
		next++
		merges[i] = Merge{A: xr, B: yr, Height: m.h, Size: m.size}
	}
	return merges
}

// sqDist is the squared Euclidean distance between a and b (same length).
// Four independent accumulators break the floating-point add dependency
// chain; this loop dominates Run's cost.
func sqDist(a, b []float64) float64 {
	b = b[:len(a)]
	var s0, s1, s2, s3 float64
	i := 0
	for ; i+4 <= len(a); i += 4 {
		d0 := a[i] - b[i]
		d1 := a[i+1] - b[i+1]
		d2 := a[i+2] - b[i+2]
		d3 := a[i+3] - b[i+3]
		s0 += d0 * d0
		s1 += d1 * d1
		s2 += d2 * d2
		s3 += d3 * d3
	}
	for ; i < len(a); i++ {
		d := a[i] - b[i]
		s0 += d * d
	}
	return (s0 + s1) + (s2 + s3)
}
