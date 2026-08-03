package prune

import (
	"fmt"
	"time"

	"github.com/jcrussell/angr-memories/internal/memory"
)

// Outcome is what happened to one manifest row during Apply.
type Outcome struct {
	Row      Row
	Deferred bool   // blocked by the citation orphan guard
	Why      string // deferral reason, set only when Deferred
}

// Apply applies a validated manifest to records in place (mutating
// DecidedAt/Reason/MergedInto/Body on the matching *memory.Memory),
// mirroring bd_prune.py's do_forget/do_merge_canonical/do_merge_subordinate
// but writing directly into the archive instead of shelling out to
// `bd forget`/`bd remember`. Promote rows are never mutated here -- see the
// "Promote scope cut" note in the plan this ported from: auto-editing an
// external repo's docs is out of scope, so promote rows pass through as
// non-deferred outcomes for a human to action manually.
//
// Citation orphan guard: before forgetting or merging away a key, checks
// whether any OTHER remembered memory -- one that is not itself being
// forgotten or rewritten by this same manifest -- still cites it. Such
// rows come back as Deferred instead of applied, mirroring bd_prune.py's
// validate() cited-by guard and bd_apply_with_defer.py's iteration.
// force=true skips the guard entirely.
func Apply(records []*memory.Memory, rows []Row, citations *Graph, force bool, at time.Time) ([]Outcome, error) {
	// Last-write-wins per key: if a key has multiple incarnations (forgotten
	// and later recreated), this keeps whichever occurs last in `records`.
	// That's correct as long as records came from memory.Load reading a
	// file memory.Save last wrote, since Save always sorts by
	// (key, first_seen) ascending -- the last incarnation in file order is
	// the newest one, which is exactly the one a manifest key should refer
	// to.
	byKey := make(map[string]*memory.Memory, len(records))
	for _, m := range records {
		byKey[m.Key] = m
	}

	// goneOrRewritten = keys whose remembered record disappears (forget,
	// merge subordinates) or whose body is replaced (merge canonicals)
	// once this manifest applies. A citation FROM one of these keys can't
	// vouch for another forget's safety -- it survives, but not with the
	// same content, and a subordinate's citations vanish outright.
	goneOrRewritten := map[string]bool{}
	// removed = the subset actually disappearing -- what the orphan guard
	// protects.
	removed := map[string]bool{}
	for _, r := range rows {
		switch {
		case r.Verdict == VerdictForget:
			removed[r.Key] = true
			goneOrRewritten[r.Key] = true
		case r.Verdict == VerdictMerge && !r.Canonical:
			removed[r.Key] = true
			goneOrRewritten[r.Key] = true
		case r.Verdict == VerdictMerge && r.Canonical:
			goneOrRewritten[r.Key] = true
		}
	}

	deferred := map[string]string{}
	if !force {
		for key := range removed {
			for _, citer := range citations.CitedBy[key] {
				if goneOrRewritten[citer] {
					continue // citer is gone/rewritten too; doesn't vouch for key
				}
				deferred[key] = fmt.Sprintf(
					"cited by %q, which survives this manifest unchanged", citer)
				break
			}
		}
	}

	ts := at.UTC().Format(time.RFC3339)

	outcomes := make([]Outcome, 0, len(rows))
	for _, r := range rows {
		if why, ok := deferred[r.Key]; ok {
			outcomes = append(outcomes, Outcome{Row: r, Deferred: true, Why: why})
			continue
		}

		m := byKey[r.Key]
		if m == nil {
			return nil, fmt.Errorf("apply: manifest key %q not found in memories.jsonl", r.Key)
		}

		switch r.Verdict {
		case VerdictForget:
			if m.Remembered() { // idempotent rerun: don't clobber a real DecidedAt with "now"
				m.DecidedAt = ts
				m.Reason = r.Reason
			}
		case VerdictMerge:
			if r.Canonical {
				m.Body = r.MergedBody // re-applying the same merged body is a harmless no-op
			} else if m.Remembered() {
				m.DecidedAt = ts
				m.Reason = r.Reason
				m.MergedInto = r.MergedInto
			}
		case VerdictPromote, VerdictKeep:
			// no-op: promote is reported, not applied (see doc comment);
			// keep is genuinely a no-op.
		}
		outcomes = append(outcomes, Outcome{Row: r})
	}

	return outcomes, nil
}
