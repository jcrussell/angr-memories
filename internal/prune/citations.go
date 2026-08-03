// Package prune implements the pruning-round mechanics ported from the
// deleted tools/bd_*.py scripts (see git history before c53a642): heuristic
// triage tags, a citation graph used as an orphan guard, and manifest
// validation/apply. Unlike the original tools, everything here operates on
// memories.jsonl records directly instead of shelling out to `bd`.
package prune

import (
	"sort"
	"strings"

	"github.com/jcrussell/angr-memories/internal/memory"
)

// minCiteKeyLen guards against short keys matching as a substring of
// unrelated text -- ported from bd_triage.py's cites-other-memory check,
// which used the same length floor.
const minCiteKeyLen = 12

// Graph is a bidirectional citation index over remembered memory bodies:
// which keys are mentioned (as literal substrings) in which other bodies.
type Graph struct {
	Cites   map[string][]string // citer key -> cited keys
	CitedBy map[string][]string // cited key -> citer keys
}

// BuildGraph scans every remembered body for literal mentions of other
// remembered keys. Only remembered records are indexed -- a forgotten
// memory can't be a citation target a live decision needs to protect, and
// at most one remembered record exists per key at any time (memory.Diff's
// invariant), so no incarnation-dedup is needed here.
func BuildGraph(records []*memory.Memory) *Graph {
	remembered := make(map[string]*memory.Memory)
	for _, m := range records {
		if m.Remembered() {
			remembered[m.Key] = m
		}
	}

	g := &Graph{Cites: map[string][]string{}, CitedBy: map[string][]string{}}
	for key, m := range remembered {
		var cited []string
		for candidate := range remembered {
			if candidate == key || len(candidate) < minCiteKeyLen {
				continue
			}
			if strings.Contains(m.Body, candidate) {
				cited = append(cited, candidate)
			}
		}
		if len(cited) == 0 {
			continue
		}
		sort.Strings(cited)
		g.Cites[key] = cited
		for _, c := range cited {
			g.CitedBy[c] = append(g.CitedBy[c], key)
		}
	}
	for k := range g.CitedBy {
		sort.Strings(g.CitedBy[k])
	}
	return g
}
