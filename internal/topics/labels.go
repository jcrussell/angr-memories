package topics

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/jcrussell/angr-memories/internal/atomicfile"
)

// Label is a human- or LLM-written name for a topic, anchored to the member
// keys that were in the cluster when it was labeled. Anchors, not cluster
// indices, let a label follow its topic across the small re-clusterings a
// sync causes. EmbedModel records which embedding model produced the tree
// the label was written against: anchors mean nothing under a different
// model's geometry, so Assign treats such labels as orphaned rather than
// letting a chance overlap of a few keys carry an old name onto a new
// cluster (which is how the first set of labels, written for a TF-IDF
// prototype, ended up naming clusters they did not describe).
type Label struct {
	Text       string   `json:"label"`
	Anchors    []string `json:"anchors"`
	LabeledBy  string   `json:"labeled_by"`            // "human" or "llm:<model>"
	At         string   `json:"at"`                    // RFC3339
	EmbedModel string   `json:"embed_model,omitempty"` // embeddings.jsonl header model; "" = unknown (legacy)
}

// MinHits is the number of a label's anchors that must be members of a
// cluster for the label to attach to it: at least half of them, rounded up
// (5 of the usual 10; 3 of 6 for a cluster with few remembered members).
// Half keeps the label on the bulk of what it described, where a flat 3 let
// it follow a minority of its old members into a cluster about something
// else, and a flat 5 would demand nearly all of a short anchor list.
func MinHits(nAnchors int) int { return max(1, (nAnchors+1)/2) }

// Assignment is a cluster's attached label (nil when unlabeled) and how many
// of its anchors the cluster contains.
type Assignment struct {
	Label *Label
	Hits  int
}

// Assign attaches labels to clusters. For every (label, cluster) pair it
// counts anchors ∩ member keys and keeps pairs with hits >= MinHits(anchors);
// pairs are sorted by hits desc, then hits/len(anchors) desc, then label
// order, then cluster index, and taken greedily while both the label and
// the cluster are still free. A label whose EmbedModel is set and differs
// from embedModel (the current embeddings file's model) never attaches. It
// returns one Assignment per cluster (nil Label when unlabeled) and the
// labels that attached nowhere, in input order.
func Assign(labels []Label, res *Result, embedModel string) (byCluster []Assignment, orphaned []Label) {
	byCluster = make([]Assignment, len(res.Clusters))

	memberKeys := make([]map[string]bool, len(res.Clusters))
	for ci, c := range res.Clusters {
		m := make(map[string]bool, len(c.Members))
		for _, di := range c.Members {
			m[res.Docs[di].Key] = true
		}
		memberKeys[ci] = m
	}

	type pair struct {
		li, ci int
		hits   int
		frac   float64
	}
	var pairs []pair
	for li, l := range labels {
		if !l.ForModel(embedModel) {
			continue
		}
		anchors := uniqueStrings(l.Anchors)
		if len(anchors) == 0 {
			continue
		}
		for ci := range res.Clusters {
			hits := 0
			for _, a := range anchors {
				if memberKeys[ci][a] {
					hits++
				}
			}
			if hits >= MinHits(len(anchors)) {
				pairs = append(pairs, pair{li, ci, hits, float64(hits) / float64(len(anchors))})
			}
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		a, b := pairs[i], pairs[j]
		if a.hits != b.hits {
			return a.hits > b.hits
		}
		if a.frac != b.frac {
			return a.frac > b.frac
		}
		// Same evidence: a person's label beats a model draft, then the
		// more recently written one wins, then file order -- never the
		// alphabetical accident of WriteLabels' sort.
		if ah, bh := isHuman(labels[a.li]), isHuman(labels[b.li]); ah != bh {
			return ah
		}
		if labels[a.li].At != labels[b.li].At {
			return labels[a.li].At > labels[b.li].At
		}
		if a.li != b.li {
			return a.li < b.li
		}
		return a.ci < b.ci
	})

	labelUsed := make([]bool, len(labels))
	clusterUsed := make([]bool, len(res.Clusters))
	for _, p := range pairs {
		if labelUsed[p.li] || clusterUsed[p.ci] {
			continue
		}
		labelUsed[p.li] = true
		clusterUsed[p.ci] = true
		l := labels[p.li] // a copy: callers may edit the labels file afterwards
		byCluster[p.ci] = Assignment{Label: &l, Hits: p.hits}
	}
	for li := range labels {
		if !labelUsed[li] {
			orphaned = append(orphaned, labels[li])
		}
	}
	return byCluster, orphaned
}

// ForModel reports whether l's anchors are meaningful under embedModel: its
// EmbedModel is unset (written before models were recorded) or equal.
func (l Label) ForModel(embedModel string) bool {
	return l.EmbedModel == "" || l.EmbedModel == embedModel
}

// maxAnchors caps the anchors recorded when a cluster is labeled.
const maxAnchors = 10

func isHuman(l Label) bool { return !strings.HasPrefix(l.LabeledBy, "llm:") }

// AnchorsFor derives the anchor keys recorded when cluster c is labeled: its
// exemplar keys, then remembered members not already present, up to
// maxAnchors in total.
func AnchorsFor(c Cluster, docs []Doc) []string {
	out := make([]string, 0, maxAnchors)
	seen := map[string]bool{}
	add := func(key string) bool {
		if len(out) >= maxAnchors {
			return false
		}
		if seen[key] {
			return true
		}
		seen[key] = true
		out = append(out, key)
		return true
	}
	for _, di := range c.Exemplars {
		if !add(docs[di].Key) {
			return out
		}
	}
	for _, di := range c.Members {
		if !docs[di].Remembered {
			continue
		}
		if !add(docs[di].Key) {
			return out
		}
	}
	return out
}

// uniqueStrings returns ss without duplicates, preserving first-seen order.
func uniqueStrings(ss []string) []string {
	out := make([]string, 0, len(ss))
	seen := make(map[string]bool, len(ss))
	for _, s := range ss {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// LabelsSchemaVersion is the current labels-file schema version.
const LabelsSchemaVersion = 1

// LabelsFile is the parsed labels file: a header line {"schema_version":1}
// followed by one Label JSON object per line.
type LabelsFile struct {
	SchemaVersion int
	Labels        []Label
}

type labelsHeader struct {
	SchemaVersion *int `json:"schema_version"`
}

// ReadLabels parses a labels file. Empty input yields an empty file at the
// current schema version. A header newer than LabelsSchemaVersion is an
// error (upgrade the tool); a missing header is an error.
func ReadLabels(r io.Reader) (*LabelsFile, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	f := &LabelsFile{SchemaVersion: LabelsSchemaVersion}
	if len(bytes.TrimSpace(data)) == 0 {
		return f, nil
	}

	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	lineNo := 0
	sawHeader := false
	for sc.Scan() {
		lineNo++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if !sawHeader {
			var h labelsHeader
			if err := json.Unmarshal(line, &h); err != nil || h.SchemaVersion == nil {
				return nil, fmt.Errorf("topics: labels file line %d: missing %q header", lineNo, `{"schema_version":1}`)
			}
			if *h.SchemaVersion > LabelsSchemaVersion {
				return nil, fmt.Errorf("topics: labels file schema_version %d is newer than supported version %d; upgrade the tool", *h.SchemaVersion, LabelsSchemaVersion)
			}
			f.SchemaVersion = *h.SchemaVersion
			sawHeader = true
			continue
		}
		var l Label
		if err := json.Unmarshal(line, &l); err != nil {
			return nil, fmt.Errorf("topics: labels file line %d: %w", lineNo, err)
		}
		f.Labels = append(f.Labels, l)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return f, nil
}

// LoadLabels reads the labels file at path. A missing file yields an empty
// file at the current schema version and a nil error.
func LoadLabels(path string) (*LabelsFile, error) {
	fh, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &LabelsFile{SchemaVersion: LabelsSchemaVersion}, nil
		}
		return nil, err
	}
	defer fh.Close()
	return ReadLabels(fh)
}

// WriteLabels writes f to path atomically: the header line, then the labels
// sorted by Text (stable). HTML escaping is disabled so label text reads as
// written.
func WriteLabels(path string, f *LabelsFile) error {
	var labels []Label
	if f != nil {
		labels = append([]Label(nil), f.Labels...)
	}
	sort.SliceStable(labels, func(i, j int) bool { return labels[i].Text < labels[j].Text })
	return atomicfile.WriteFunc(path, 0o644, func(w io.Writer) error {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(struct {
			SchemaVersion int `json:"schema_version"`
		}{LabelsSchemaVersion}); err != nil {
			return err
		}
		for _, l := range labels {
			if l.Anchors == nil {
				l.Anchors = []string{}
			}
			if err := enc.Encode(l); err != nil {
				return err
			}
		}
		return nil
	})
}

// Upsert adds l to f, replacing the label currently attached to the
// cluster being (re)labeled -- identified by its text, replaceText, as
// Assign reported it; "" when the cluster was unlabeled -- and any label
// whose text equals l.Text. Everything else is left alone: a label is
// never displaced just because some of its anchors happen to sit in this
// cluster, so relabeling one cluster cannot silently drop another's name.
func (f *LabelsFile) Upsert(l Label, replaceText string) {
	matches := func(e Label) bool {
		return e.Text == l.Text || (replaceText != "" && e.Text == replaceText)
	}
	replaced := false
	kept := make([]Label, 0, len(f.Labels)+1)
	for _, e := range f.Labels {
		if !matches(e) {
			kept = append(kept, e)
			continue
		}
		if !replaced {
			kept = append(kept, l)
			replaced = true
		}
	}
	if !replaced {
		kept = append(kept, l)
	}
	f.Labels = kept
}
