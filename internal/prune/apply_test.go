package prune

import (
	"testing"
	"time"

	"github.com/jcrussell/angr-memories/internal/memory"
)

func TestApply(t *testing.T) {
	at := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	ts := at.UTC().Format(time.RFC3339)

	t.Run("forget mutates DecidedAt and Reason in place", func(t *testing.T) {
		m := &memory.Memory{Key: "stale-invariant-timing-check", Body: "old"}
		records := []*memory.Memory{m}
		citations := BuildGraph(records)

		outcomes, err := Apply(records, []Row{{Key: m.Key, Verdict: VerdictForget, Reason: "superseded"}}, citations, false, at)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if len(outcomes) != 1 || outcomes[0].Deferred {
			t.Fatalf("expected one non-deferred outcome, got %+v", outcomes)
		}
		if m.DecidedAt != ts || m.Reason != "superseded" {
			t.Errorf("m = %+v, want DecidedAt=%q Reason=%q", m, ts, "superseded")
		}
	})

	t.Run("merge subordinate is closed with merged_into set; canonical body is replaced", func(t *testing.T) {
		canon := &memory.Memory{Key: "invariant-solver-eval-timing", Body: "original"}
		sub := &memory.Memory{Key: "avoid-cargo-fmt-scope-creep", Body: "duplicate detail"}
		records := []*memory.Memory{canon, sub}
		citations := BuildGraph(records)

		rows := []Row{
			{Key: canon.Key, Verdict: VerdictMerge, Canonical: true, MergedBody: "merged content"},
			{Key: sub.Key, Verdict: VerdictMerge, MergedInto: canon.Key, Reason: "duplicate of canonical"},
		}
		if _, err := Apply(records, rows, citations, false, at); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if canon.Body != "merged content" || canon.DecidedAt != "" {
			t.Errorf("canonical = %+v, want Body updated and still remembered", canon)
		}
		if sub.DecidedAt != ts || sub.MergedInto != canon.Key {
			t.Errorf("subordinate = %+v, want DecidedAt=%q MergedInto=%q", sub, ts, canon.Key)
		}
	})

	t.Run("promote is reported but never mutates the record", func(t *testing.T) {
		m := &memory.Memory{Key: "rust-durable-architecture-note", Body: "long design doc..."}
		records := []*memory.Memory{m}
		citations := BuildGraph(records)

		rows := []Row{{Key: m.Key, Verdict: VerdictPromote, PromotedTo: "docs/advanced-topics/x.rst", Body: "rst content"}}
		outcomes, err := Apply(records, rows, citations, false, at)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if outcomes[0].Deferred {
			t.Fatalf("promote should never be deferred, got %+v", outcomes[0])
		}
		if m.DecidedAt != "" || m.Body != "long design doc..." {
			t.Errorf("promote must not mutate the record, got %+v", m)
		}
	})

	t.Run("orphan guard defers a forget cited by a surviving memory", func(t *testing.T) {
		target := &memory.Memory{Key: "invariant-solver-eval-timing", Body: "body"}
		citer := &memory.Memory{Key: "avoid-cargo-fmt-scope-creep", Body: "see invariant-solver-eval-timing"}
		records := []*memory.Memory{target, citer}
		citations := BuildGraph(records)

		rows := []Row{
			{Key: target.Key, Verdict: VerdictForget, Reason: "stale"},
			{Key: citer.Key, Verdict: VerdictKeep},
		}
		outcomes, err := Apply(records, rows, citations, false, at)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if !outcomes[0].Deferred {
			t.Fatalf("expected the forget to be deferred, got %+v", outcomes[0])
		}
		if target.DecidedAt != "" {
			t.Errorf("deferred forget must not mutate the record, got %+v", target)
		}
	})

	t.Run("orphan guard does not fire when the citer is forgotten in the same manifest", func(t *testing.T) {
		target := &memory.Memory{Key: "invariant-solver-eval-timing", Body: "body"}
		citer := &memory.Memory{Key: "avoid-cargo-fmt-scope-creep", Body: "see invariant-solver-eval-timing"}
		records := []*memory.Memory{target, citer}
		citations := BuildGraph(records)

		rows := []Row{
			{Key: target.Key, Verdict: VerdictForget, Reason: "stale"},
			{Key: citer.Key, Verdict: VerdictForget, Reason: "also stale"},
		}
		outcomes, err := Apply(records, rows, citations, false, at)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		for _, o := range outcomes {
			if o.Deferred {
				t.Errorf("expected no deferrals when the only citer is forgotten too, got %+v", o)
			}
		}
		if target.DecidedAt != ts {
			t.Errorf("target should have been forgotten, got %+v", target)
		}
	})

	t.Run("force skips the orphan guard entirely", func(t *testing.T) {
		target := &memory.Memory{Key: "invariant-solver-eval-timing", Body: "body"}
		citer := &memory.Memory{Key: "avoid-cargo-fmt-scope-creep", Body: "see invariant-solver-eval-timing"}
		records := []*memory.Memory{target, citer}
		citations := BuildGraph(records)

		rows := []Row{
			{Key: target.Key, Verdict: VerdictForget, Reason: "stale"},
			{Key: citer.Key, Verdict: VerdictKeep},
		}
		outcomes, err := Apply(records, rows, citations, true, at)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if outcomes[0].Deferred {
			t.Fatalf("force=true should skip the guard, got %+v", outcomes[0])
		}
		if target.DecidedAt != ts {
			t.Errorf("target should have been forgotten under force, got %+v", target)
		}
	})

	t.Run("rerunning forget on an already-forgotten record does not clobber the original DecidedAt", func(t *testing.T) {
		firstRun := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
		secondRun := firstRun.Add(24 * time.Hour)

		m := &memory.Memory{Key: "stale-invariant-timing-check", Body: "old"}
		records := []*memory.Memory{m}
		citations := BuildGraph(records)
		row := Row{Key: m.Key, Verdict: VerdictForget, Reason: "superseded"}

		if _, err := Apply(records, []Row{row}, citations, false, firstRun); err != nil {
			t.Fatalf("first Apply: %v", err)
		}
		firstDecidedAt := m.DecidedAt

		if _, err := Apply(records, []Row{row}, citations, false, secondRun); err != nil {
			t.Fatalf("second Apply: %v", err)
		}
		if m.DecidedAt != firstDecidedAt {
			t.Errorf("DecidedAt changed on rerun: %q -> %q, want unchanged", firstDecidedAt, m.DecidedAt)
		}
	})

	t.Run("unknown manifest key errors", func(t *testing.T) {
		records := []*memory.Memory{{Key: "known", Body: "b"}}
		citations := BuildGraph(records)
		_, err := Apply(records, []Row{{Key: "unknown", Verdict: VerdictKeep}}, citations, false, at)
		if err == nil {
			t.Fatal("expected an error for a manifest key absent from memories.jsonl")
		}
	})
}
