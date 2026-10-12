package topics

import (
	"sort"

	"github.com/jcrussell/angr-memories/internal/memory"
)

// DocsFrom builds the clustering input shared by the site build and the
// `topics` command: one Doc per key that has a vector, using the latest
// incarnation's remembered state, sorted by key so both callers see the
// same cluster indices for the same inputs.
func DocsFrom(records []*memory.Memory, vectors map[string][]float32) []Doc {
	latest := map[string]*memory.Memory{}
	for _, m := range records {
		if cur, ok := latest[m.Key]; !ok || m.FirstSeen > cur.FirstSeen {
			latest[m.Key] = m
		}
	}
	docs := make([]Doc, 0, len(vectors))
	for key, m := range latest {
		v, ok := vectors[key]
		if !ok {
			continue
		}
		docs = append(docs, Doc{Key: key, Remembered: m.Remembered(), Vec: v})
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Key < docs[j].Key })
	return docs
}
