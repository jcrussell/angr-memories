package topics

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/jcrussell/angr-memories/internal/atomicfile"
)

// Label is a human- or LLM-written name for a topic, anchored to the member
// keys that were in the cluster when it was labeled. Anchors, not cluster
// indices, let a label follow its topic across re-clusterings.
type Label struct {
	Text      string   `json:"label"`
	Anchors   []string `json:"anchors"`
	LabeledBy string   `json:"labeled_by"` // "human" or "llm:<model>"
	At        string   `json:"at"`         // RFC3339
}

// MinAnchorHits is the minimum number of a label's anchors that must be
// members of a cluster for the label to attach to it.
const MinAnchorHits = 3

// Assignment is a cluster's attached label (nil when unlabeled) and how many
// of its anchors the cluster contains.
type Assignment struct {
	Label *Label
	Hits  int
}

// Assign attaches labels to clusters. For every (label, cluster) pair it
// counts anchors ∩ member keys and keeps pairs with hits >= MinAnchorHits;
// pairs are sorted by hits desc, then hits/len(anchors) desc, then label
// order, then cluster index, and taken greedily while both the label and
// the cluster are still free. It returns one Assignment per cluster (nil
// Label when unlabeled) and the labels that attached nowhere, in input
// order.
func Assign(labels []Label, res *Result) (byCluster []Assignment, orphaned []Label) {
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
			if hits >= MinAnchorHits {
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
		byCluster[p.ci] = Assignment{Label: &labels[p.li], Hits: p.hits}
	}
	for li := range labels {
		if !labelUsed[li] {
			orphaned = append(orphaned, labels[li])
		}
	}
	return byCluster, orphaned
}

// maxAnchors caps the anchors recorded when a cluster is labeled.
const maxAnchors = 10

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

// Upsert adds l to f, replacing any existing label that describes the same
// cluster: one whose anchors overlap memberKeys by >= MinAnchorHits, or
// whose Text equals l.Text. When several existing labels match, the first
// is replaced and the rest are dropped. Otherwise l is appended.
func (f *LabelsFile) Upsert(l Label, memberKeys map[string]bool) {
	matches := func(e Label) bool {
		if e.Text == l.Text {
			return true
		}
		hits := 0
		for _, a := range uniqueStrings(e.Anchors) {
			if memberKeys[a] {
				hits++
			}
		}
		return hits >= MinAnchorHits
	}
	replaced := false
	kept := f.Labels[:0]
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
	// Clear any tail slots so dropped labels don't linger in the backing array.
	for i := len(kept); i < len(f.Labels); i++ {
		f.Labels[i] = Label{}
	}
	f.Labels = kept
}
