// Package topics clusters memory embedding vectors into topics.
//
// It runs Ward-linkage hierarchical clustering (an NN-chain over centroids,
// so no n² distance matrix is ever materialised), cuts the dendrogram to K
// clusters, summarises each cluster (members, exemplars nearest the
// centroid, key-prefix counts, remembered/forgotten tallies) and offers a
// key-anchored labelling mechanism so human- or LLM-written labels survive
// re-clustering: a label is attached to whichever cluster still contains
// enough of its anchor keys.
//
// The linkage output is scipy-compatible (same row layout, same height
// scale, same tie-breaking as scipy.cluster.hierarchy.linkage(..., 'ward')).
package topics

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Doc is one embedded memory. Vec is a unit-norm embedding; every Doc passed
// to Run must have the same number of dimensions.
type Doc struct {
	Key        string
	Remembered bool
	Vec        []float32
}

// Options tunes Run. K<=0 selects DefaultK.
type Options struct {
	K int
}

// DefaultK is the number of clusters Run cuts to when Options.K <= 0.
const DefaultK = 48

// DefaultOptions returns the default Options.
func DefaultOptions() Options { return Options{K: DefaultK} }

// Merge is one scipy-style linkage row. Leaves have ids 0..n-1; merge i has
// id n+i. A < B always.
type Merge struct {
	A, B   int
	Height float64
	Size   int
}

// Node is a node of the dendrogram truncated at K clusters. Internal nodes
// carry the merge Height and Size and have Leaf == -1; truncated leaves have
// nil Left/Right, Height 0, Size equal to the member count, and Leaf set to
// the 0-based cluster index (its position in dendrogram leaf order).
type Node struct {
	Height      float64
	Size        int
	Left, Right *Node
	Leaf        int
}

// PrefixCount is the number of cluster members whose key starts with Prefix
// (the raw first key segment: the text before the first '-').
// KeptPercent is the rounded remembered share used everywhere a cluster's
// retention is shown (table, pages, dendrogram).
func KeptPercent(remembered, total int) int {
	if total <= 0 {
		return 0
	}
	return int(math.Round(100 * float64(remembered) / float64(total)))
}

type PrefixCount struct {
	Prefix string
	Count  int
}

// Cluster is one of the K clusters with its summary.
type Cluster struct {
	Index      int           // 1-based position in dendrogram leaf order
	Members    []int         // doc indices, remembered first then key asc
	Exemplars  []int         // up to 6 doc indices nearest the centroid, nearest first
	Prefixes   []PrefixCount // top 4 prefixes, count desc then prefix asc
	Remembered int
	Forgotten  int
}

// Result is the output of Run.
type Result struct {
	Docs     []Doc
	Merges   []Merge   // n-1 rows, sorted by height like scipy
	Clusters []Cluster // leaf order; Clusters[i].Index == i+1
	Root     *Node     // truncated dendrogram
}

// ErrTooSmall is returned by Run when there is nothing to cluster.
var ErrTooSmall = errors.New("topics: corpus too small to cluster")

// maxExemplars is the number of exemplars recorded per cluster.
const maxExemplars = 6

// maxPrefixes is the number of prefix counts recorded per cluster.
const maxPrefixes = 4

// Run clusters docs into K topics. It returns ErrTooSmall when there are
// fewer than two docs or the vectors are empty. K is clamped to len(docs).
func Run(docs []Doc, opts Options) (*Result, error) {
	n := len(docs)
	if n < 2 {
		return nil, ErrTooSmall
	}
	dims := len(docs[0].Vec)
	if dims == 0 {
		return nil, ErrTooSmall
	}
	for i, d := range docs {
		if len(d.Vec) != dims {
			return nil, fmt.Errorf("topics: doc %d (%q) has %d dims, want %d", i, d.Key, len(d.Vec), dims)
		}
	}
	k := opts.K
	if k <= 0 {
		k = DefaultK
	}
	if k > n {
		k = n
	}

	merges := wardLinkage(docs)
	root, members := cutTree(merges, n, k)

	clusters := make([]Cluster, len(members))
	for i, mem := range members {
		clusters[i] = summarize(i+1, mem, docs)
	}
	return &Result{Docs: docs, Merges: merges, Clusters: clusters, Root: root}, nil
}

// summarize builds the Cluster summary for one leaf's members.
func summarize(index int, mem []int, docs []Doc) Cluster {
	c := Cluster{Index: index}
	dims := len(docs[0].Vec)

	// Members: remembered first, then key asc, then doc index for stability.
	members := append([]int(nil), mem...)
	sort.SliceStable(members, func(i, j int) bool {
		a, b := docs[members[i]], docs[members[j]]
		if a.Remembered != b.Remembered {
			return a.Remembered
		}
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		return members[i] < members[j]
	})
	c.Members = members

	// Centroid of member vectors (float64).
	cent := make([]float64, dims)
	for _, di := range members {
		for j, v := range docs[di].Vec {
			cent[j] += float64(v)
		}
	}
	inv := 1 / float64(len(members))
	for j := range cent {
		cent[j] *= inv
	}

	// Exemplars: members by dot(vec, centroid) desc, ties by key then index.
	type scored struct {
		idx int
		dot float64
	}
	sc := make([]scored, len(members))
	for i, di := range members {
		var s float64
		for j, v := range docs[di].Vec {
			s += float64(v) * cent[j]
		}
		sc[i] = scored{di, s}
	}
	sort.SliceStable(sc, func(i, j int) bool {
		if sc[i].dot != sc[j].dot {
			return sc[i].dot > sc[j].dot
		}
		a, b := docs[sc[i].idx].Key, docs[sc[j].idx].Key
		if a != b {
			return a < b
		}
		return sc[i].idx < sc[j].idx
	})
	ex := len(sc)
	if ex > maxExemplars {
		ex = maxExemplars
	}
	c.Exemplars = make([]int, ex)
	for i := 0; i < ex; i++ {
		c.Exemplars[i] = sc[i].idx
	}

	// Prefix counts and remembered/forgotten tallies.
	counts := map[string]int{}
	for _, di := range members {
		d := docs[di]
		counts[keyPrefix(d.Key)]++
		if d.Remembered {
			c.Remembered++
		} else {
			c.Forgotten++
		}
	}
	pcs := make([]PrefixCount, 0, len(counts))
	for p, cnt := range counts {
		pcs = append(pcs, PrefixCount{Prefix: p, Count: cnt})
	}
	sort.Slice(pcs, func(i, j int) bool {
		if pcs[i].Count != pcs[j].Count {
			return pcs[i].Count > pcs[j].Count
		}
		return pcs[i].Prefix < pcs[j].Prefix
	})
	if len(pcs) > maxPrefixes {
		pcs = pcs[:maxPrefixes]
	}
	c.Prefixes = pcs
	return c
}

// keyPrefix returns the text before the first '-' in key (the whole key if
// it has no '-').
func keyPrefix(key string) string {
	if i := strings.IndexByte(key, '-'); i >= 0 {
		return key[:i]
	}
	return key
}
