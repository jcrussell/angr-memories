package prune

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	batch := map[string]bool{"a": true, "b": true, "c": true}

	t.Run("valid manifest covering the batch exactly", func(t *testing.T) {
		rows := []Row{
			{Key: "a", Verdict: VerdictForget, Reason: "stale"},
			{Key: "b", Verdict: VerdictKeep},
			{Key: "c", Verdict: VerdictKeep},
		}
		if errs := Validate(rows, batch, nil); len(errs) != 0 {
			t.Errorf("expected no errors, got %v", errs)
		}
	})

	t.Run("missing and extra keys are both reported", func(t *testing.T) {
		rows := []Row{
			{Key: "a", Verdict: VerdictKeep},
			{Key: "not-in-batch", Verdict: VerdictKeep},
		}
		errs := Validate(rows, batch, nil)
		if !containsSubstring(errs, "missing keys") || !containsSubstring(errs, "extra keys") {
			t.Errorf("expected missing+extra errors, got %v", errs)
		}
	})

	t.Run("duplicate key in manifest is reported", func(t *testing.T) {
		rows := []Row{
			{Key: "a", Verdict: VerdictKeep},
			{Key: "a", Verdict: VerdictForget},
			{Key: "b", Verdict: VerdictKeep},
			{Key: "c", Verdict: VerdictKeep},
		}
		if errs := Validate(rows, batch, nil); !containsSubstring(errs, "duplicate keys") {
			t.Errorf("expected a duplicate-keys error, got %v", errs)
		}
	})

	t.Run("bad verdict value is reported", func(t *testing.T) {
		rows := []Row{
			{Key: "a", Verdict: "delete"},
			{Key: "b", Verdict: VerdictKeep},
			{Key: "c", Verdict: VerdictKeep},
		}
		if errs := Validate(rows, batch, nil); !containsSubstring(errs, `bad verdict`) {
			t.Errorf("expected a bad-verdict error, got %v", errs)
		}
	})

	t.Run("mandatory-keep key forgotten is rejected", func(t *testing.T) {
		rows := []Row{
			{Key: "a", Verdict: VerdictForget, Reason: "x"},
			{Key: "b", Verdict: VerdictKeep},
			{Key: "c", Verdict: VerdictKeep},
		}
		errs := Validate(rows, batch, map[string]bool{"a": true})
		if !containsSubstring(errs, "mandatory-keep violated") {
			t.Errorf("expected a mandatory-keep error, got %v", errs)
		}
	})

	t.Run("mandatory-keep key may be a merge canonical", func(t *testing.T) {
		rows := []Row{
			{Key: "a", Verdict: VerdictMerge, Canonical: true, MergedBody: "merged"},
			{Key: "b", Verdict: VerdictMerge, MergedInto: "a"},
			{Key: "c", Verdict: VerdictKeep},
		}
		errs := Validate(rows, batch, map[string]bool{"a": true})
		if len(errs) != 0 {
			t.Errorf("expected no errors (canonical satisfies mandatory-keep), got %v", errs)
		}
	})

	t.Run("mandatory-keep key cannot bypass the check with a stray canonical flag", func(t *testing.T) {
		rows := []Row{
			{Key: "a", Verdict: VerdictForget, Canonical: true, Reason: "x"},
			{Key: "b", Verdict: VerdictKeep},
			{Key: "c", Verdict: VerdictKeep},
		}
		errs := Validate(rows, batch, map[string]bool{"a": true})
		if !containsSubstring(errs, "mandatory-keep violated") {
			t.Errorf("expected a mandatory-keep error (canonical:true on a non-merge verdict must not exempt it), got %v", errs)
		}
	})

	t.Run("merge canonical without merged_body is rejected", func(t *testing.T) {
		rows := []Row{
			{Key: "a", Verdict: VerdictMerge, Canonical: true},
			{Key: "b", Verdict: VerdictKeep},
			{Key: "c", Verdict: VerdictKeep},
		}
		if errs := Validate(rows, batch, nil); !containsSubstring(errs, "canonical without merged_body") {
			t.Errorf("expected a canonical-without-merged_body error, got %v", errs)
		}
	})

	t.Run("merge subordinate pointing outside the batch is rejected", func(t *testing.T) {
		rows := []Row{
			{Key: "a", Verdict: VerdictMerge, MergedInto: "not-in-batch"},
			{Key: "b", Verdict: VerdictKeep},
			{Key: "c", Verdict: VerdictKeep},
		}
		if errs := Validate(rows, batch, nil); !containsSubstring(errs, "is not a canonical row") {
			t.Errorf("expected a merged_into-not-canonical error, got %v", errs)
		}
	})

	t.Run("promote missing promoted_to or body is rejected", func(t *testing.T) {
		rows := []Row{
			{Key: "a", Verdict: VerdictPromote, Reason: "durable design doc"},
			{Key: "b", Verdict: VerdictKeep},
			{Key: "c", Verdict: VerdictKeep},
		}
		if errs := Validate(rows, batch, nil); !containsSubstring(errs, "promote missing") {
			t.Errorf("expected a promote-missing error, got %v", errs)
		}
	})
}

func containsSubstring(errs []string, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}
