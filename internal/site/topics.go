package site

import (
	"errors"
	"fmt"
	"html/template"
	"sort"
	"strings"

	"github.com/jcrussell/angr-memories/internal/chart"
	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/internal/topics"
)

// Topics is the /topics/ section: the corpus clustered by embedding
// similarity, one Topic per cluster, plus the pre-rendered dendrogram.
type Topics struct {
	K          int // number of clusters
	MinSize    int // minimum topic size the automatic cut honoured; 0 when K was fixed by flag
	Docs       int
	Model      string
	Clusters   []*Topic // dendrogram leaf order; Index = position+1
	Dendrogram template.HTML
	Orphaned   []string // label texts whose anchors no longer form a cluster
	Unlabeled  int

	root *topics.Node
}

// Topic is one cluster and its /topics/<n>/ page.
type Topic struct {
	Index      int
	Path       string
	Label      string // the assigned label, or an exemplar-key summary when Unlabeled
	Unlabeled  bool
	LabeledBy  string
	LabeledAt  string
	Size       int
	Remembered int
	Forgotten  int
	Prefixes   []topics.PrefixCount
	Exemplars  []string
	Members    []*TopicMember
}

// TopicMember links a member key to the page that holds its anchor.
type TopicMember struct {
	Key        string
	Remembered bool
	Page       string // "/prefix/<name>/" or "/prefix/<name>/<chunk>/"
}

// KeptPercent is the remembered share, rounded.
func (t *Topic) KeptPercent() int { return topics.KeptPercent(t.Remembered, t.Size) }

// PrefixMix renders Prefixes as "invariant 12 · z3 4".
func (t *Topic) PrefixMix() string {
	parts := make([]string, len(t.Prefixes))
	for i, p := range t.Prefixes {
		parts[i] = fmt.Sprintf("%s %d", p.Prefix, p.Count)
	}
	return strings.Join(parts, " · ")
}

// LabelCommand is the exact command a human runs to (re)label this topic.
func (t *Topic) LabelCommand() string {
	return fmt.Sprintf("angr-memories topics label %d \"<label>\"", t.Index)
}

// buildTopics clusters the keys that have vectors and attaches labels.
// Returns nil, nil when there is nothing to cluster (fewer than two docs
// with vectors), which the topics page renders as an empty state; any other
// clustering failure is a real error and fails the build.
func buildTopics(records []*memory.Memory, keyPage map[string]string, opts Options) (*Topics, error) {
	docs := topics.DocsFrom(records, opts.Vectors)
	res, err := topics.Run(docs, topics.Options{K: opts.TopicK, MinSize: opts.TopicMinSize})
	if errors.Is(err, topics.ErrTooSmall) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("clustering topics: %w", err)
	}
	assigned, orphaned := topics.Assign(opts.Labels, res, opts.Model)

	t := &Topics{K: len(res.Clusters), MinSize: res.MinSize, Docs: len(docs), Model: opts.Model, root: res.Root}
	for i, c := range res.Clusters {
		tp := &Topic{
			Index:      c.Index,
			Path:       fmt.Sprintf("/topics/%d/", c.Index),
			Size:       len(c.Members),
			Remembered: c.Remembered,
			Forgotten:  c.Forgotten,
			Prefixes:   c.Prefixes,
		}
		for _, di := range c.Exemplars {
			tp.Exemplars = append(tp.Exemplars, docs[di].Key)
		}
		for _, di := range c.Members {
			d := docs[di]
			tp.Members = append(tp.Members, &TopicMember{Key: d.Key, Remembered: d.Remembered, Page: keyPage[d.Key]})
		}
		if a := assigned[i]; a.Label != nil {
			tp.Label, tp.LabeledBy, tp.LabeledAt = a.Label.Text, a.Label.LabeledBy, a.Label.At
		} else {
			tp.Unlabeled = true
			tp.Label = autoLabel(tp.Exemplars)
			t.Unlabeled++
		}
		t.Clusters = append(t.Clusters, tp)
	}
	for _, l := range orphaned {
		t.Orphaned = append(t.Orphaned, l.Text)
	}
	sort.Strings(t.Orphaned)
	return t, nil
}

// autoLabel stands in for a missing label: the first three exemplar keys.
func autoLabel(exemplars []string) string {
	n := min(3, len(exemplars))
	if n == 0 {
		return "(unlabeled)"
	}
	return strings.Join(exemplars[:n], ", ")
}

// rowLabel is what the dendrogram row shows. Keys are long, so an
// unlabeled row is clipped harder than a written label; the full text is
// in the row's hover title either way.
func rowLabel(tp *Topic) string {
	const unlabeledMax = 34
	if !tp.Unlabeled {
		return tp.Label
	}
	return chart.Truncate(tp.Label, unlabeledMax)
}

// renderDendrogram turns the clustering tree into the page's SVG. hrefFor
// resolves a topic's root-relative path for the /topics/ page.
func (t *Topics) renderDendrogram(hrefFor func(target string) string) template.HTML {
	rows := make([]chart.DendroRow, len(t.Clusters))
	for i, tp := range t.Clusters {
		title := fmt.Sprintf("%d memories · %d remembered · %d forgotten (%d%% kept)", tp.Size, tp.Remembered, tp.Forgotten, tp.KeptPercent())
		if mix := tp.PrefixMix(); mix != "" {
			title += "\nPrefixes: " + mix
		}
		if len(tp.Exemplars) > 0 {
			title += "\nNearest centre: " + strings.Join(tp.Exemplars[:min(4, len(tp.Exemplars))], ", ")
		}
		if tp.Unlabeled {
			title = "unlabeled: " + tp.Label + "\n" + title
		}
		rows[i] = chart.DendroRow{
			Label: rowLabel(tp), Unlabeled: tp.Unlabeled, Href: hrefFor(tp.Path), Title: title,
			Remembered: float64(tp.Remembered), Forgotten: float64(tp.Forgotten),
		}
	}
	return chart.Dendrogram(toDendroNode(t.root), rows, "Ward merge distance")
}

func toDendroNode(n *topics.Node) *chart.DendroNode {
	if n == nil {
		return nil
	}
	d := &chart.DendroNode{Height: n.Height, Size: n.Size, Row: -1}
	if n.Left == nil && n.Right == nil {
		d.Row = n.Leaf
		return d
	}
	d.Left, d.Right = toDendroNode(n.Left), toDendroNode(n.Right)
	return d
}
