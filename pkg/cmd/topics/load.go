package topics

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jcrussell/angr-memories/internal/embeddings"
	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/internal/topics"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

// corpus is the clustered state every topics subcommand works from. It is
// computed exactly as the site build computes it (same inputs, same cut), so
// the cluster indices printed here are the /topics/<n>/ indices.
type corpus struct {
	records  []*memory.Memory
	latest   map[string]*memory.Memory
	labels   *topics.LabelsFile
	res      *topics.Result
	assigned []topics.Assignment
	orphaned []topics.Label
	model    string
}

func load(io *iostreams.IOStreams, memPath, embPath, labelsPath string, k, minSize int) (*corpus, error) {
	records, err := memory.Load(memPath)
	if err != nil {
		return nil, err
	}
	f, err := embeddings.Load(embPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, cmdutil.WithHint(fmt.Errorf("%s not found", embPath), "run `angr-memories embed` first (needs a local Ollama)")
	}
	if err != nil {
		return nil, err
	}
	if st := embeddings.Check(records, f); !st.Clean() {
		fmt.Fprintf(io.ErrOut, "warning: %s is out of date (%d missing, %d stale); those keys are left out until you run `angr-memories embed`\n",
			embPath, len(st.Missing), len(st.Stale))
	}
	labels, err := topics.LoadLabels(labelsPath)
	if err != nil {
		return nil, err
	}
	docs := topics.DocsFrom(records, embeddings.Vectors(records, f, true))
	res, err := topics.Run(docs, topics.Options{K: k, MinSize: minSize})
	if err != nil {
		return nil, err
	}
	assigned, orphaned := topics.Assign(labels.Labels, res, f.Header.Model)
	return &corpus{records: records, latest: memory.Latest(records), labels: labels, res: res,
		assigned: assigned, orphaned: orphaned, model: f.Header.Model}, nil
}

// row is one cluster as the CLI shows it (table or JSON).
type row struct {
	Index      int      `json:"index"`
	Label      string   `json:"label"`
	Unlabeled  bool     `json:"unlabeled"`
	LabeledBy  string   `json:"labeled_by"`
	LabeledAt  string   `json:"labeled_at"`
	Size       int      `json:"size"`
	Remembered int      `json:"remembered"`
	Forgotten  int      `json:"forgotten"`
	Kept       int      `json:"kept"`
	Prefixes   []string `json:"prefixes"`
	Exemplars  []string `json:"exemplars"`
	Members    []string `json:"members"`
}

// jsonFields is the --json allowlist (byob-output.2: callers name the
// fields they want; there is no "everything" shorthand).
var jsonFields = []string{"index", "label", "unlabeled", "labeled_by", "labeled_at", "size", "remembered", "forgotten", "kept", "prefixes", "exemplars", "members"}

func (c *corpus) rows() []row {
	out := make([]row, len(c.res.Clusters))
	for i := range c.res.Clusters {
		out[i] = c.row(i)
	}
	return out
}

// row builds the view of the cluster at position i.
func (c *corpus) row(i int) row {
	cl := c.res.Clusters[i]
	r := row{Index: cl.Index, Size: len(cl.Members), Remembered: cl.Remembered, Forgotten: cl.Forgotten,
		Kept: topics.KeptPercent(cl.Remembered, len(cl.Members))}
	for _, p := range cl.Prefixes {
		r.Prefixes = append(r.Prefixes, fmt.Sprintf("%s %d", p.Prefix, p.Count))
	}
	for _, di := range cl.Exemplars {
		r.Exemplars = append(r.Exemplars, c.res.Docs[di].Key)
	}
	for _, di := range cl.Members {
		r.Members = append(r.Members, c.res.Docs[di].Key)
	}
	if a := c.assigned[i]; a.Label != nil {
		r.Label, r.LabeledBy, r.LabeledAt = a.Label.Text, a.Label.LabeledBy, a.Label.At
	} else {
		r.Unlabeled = true
		r.Label = "(unlabeled)"
	}
	return r
}

// currentLabel is the text of the label Assign attached to cluster
// position i, or "" when it is unlabeled. Captured before any Upsert,
// because Upsert compacts the labels slice in place.
func (c *corpus) currentLabel(i int) string {
	if a := c.assigned[i]; a.Label != nil {
		return a.Label.Text
	}
	return ""
}

// clusterAt resolves a 1-based index from the CLI.
func (c *corpus) clusterAt(index int) (topics.Cluster, error) {
	if index < 1 || index > len(c.res.Clusters) {
		return topics.Cluster{}, cmdutil.FlagErrorf("cluster index must be 1-%d, got %d", len(c.res.Clusters), index)
	}
	return c.res.Clusters[index-1], nil
}

// normLabel is the key used for duplicate detection.
func normLabel(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// takenLabels returns the texts of the labels currently attached to a
// cluster. Orphaned labels are deliberately not included: a model that
// re-proposes an orphaned name for the cluster it now fits simply
// re-anchors it (Upsert replaces the same-text row).
func (c *corpus) takenLabels() map[string]bool {
	m := map[string]bool{}
	for _, a := range c.assigned {
		if a.Label != nil {
			m[normLabel(a.Label.Text)] = true
		}
	}
	return m
}

func (c *corpus) warnOrphaned(io *iostreams.IOStreams) {
	if len(c.orphaned) == 0 {
		return
	}
	var texts, foreign []string
	for _, l := range c.orphaned {
		if !l.ForModel(c.model) {
			foreign = append(foreign, fmt.Sprintf("%s (%s)", l.Text, l.EmbedModel))
			continue
		}
		texts = append(texts, l.Text)
	}
	sort.Strings(texts)
	sort.Strings(foreign)
	if len(texts) > 0 {
		fmt.Fprintf(io.ErrOut, "warning: %d label(s) no longer match any cluster: %s\n", len(texts), strings.Join(texts, "; "))
	}
	if len(foreign) > 0 {
		fmt.Fprintf(io.ErrOut, "warning: %d label(s) were written against a different embedding model than %s and are ignored: %s\n", len(foreign), c.model, strings.Join(foreign, "; "))
	}
}
