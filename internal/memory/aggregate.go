package memory

import (
	"sort"
	"time"
)

// PrefixCount is remembered/forgotten counts for one key-prefix family.
type PrefixCount struct {
	Remembered int
	Forgotten  int
}

// Summary is the aggregate view over a full record set, computed once and
// shared by `stats` (text table) and the site's dashboard (SVG charts) so
// both read from the same numbers instead of two drifting implementations.
type Summary struct {
	TotalRemembered int
	TotalForgotten  int
	Recreated       int // keys with more than one incarnation
	ByPrefix        map[string]*PrefixCount

	// SurvivalDurations is how long each forgotten record lasted
	// (DecidedAt - FirstSeen), sorted ascending. Only includes records
	// with a parseable FirstSeen and DecidedAt.
	SurvivalDurations []time.Duration
}

// Aggregate computes a Summary over every record (all incarnations,
// remembered and forgotten).
func Aggregate(records []*Memory) Summary {
	s := Summary{ByPrefix: map[string]*PrefixCount{}}

	incarnationsPerKey := map[string]int{}
	for _, m := range records {
		incarnationsPerKey[m.Key]++

		p := PrefixOf(m.Key)
		pc := s.ByPrefix[p]
		if pc == nil {
			pc = &PrefixCount{}
			s.ByPrefix[p] = pc
		}

		if m.Remembered() {
			pc.Remembered++
			s.TotalRemembered++
			continue
		}
		pc.Forgotten++
		s.TotalForgotten++
		if fs, err := time.Parse(time.RFC3339, m.FirstSeen); err == nil {
			if da, err := time.Parse(time.RFC3339, m.DecidedAt); err == nil {
				s.SurvivalDurations = append(s.SurvivalDurations, da.Sub(fs))
			}
		}
	}

	for _, n := range incarnationsPerKey {
		if n > 1 {
			s.Recreated++
		}
	}

	sort.Slice(s.SurvivalDurations, func(i, j int) bool { return s.SurvivalDurations[i] < s.SurvivalDurations[j] })
	return s
}
