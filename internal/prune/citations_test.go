package prune

import (
	"reflect"
	"sort"
	"testing"

	"github.com/jcrussell/angr-memories/internal/memory"
)

func TestBuildGraph(t *testing.T) {
	t.Run("detects a literal key mention in another body", func(t *testing.T) {
		records := []*memory.Memory{
			{Key: "invariant-solver-eval-timing", Body: "some unrelated body"},
			{Key: "avoid-cargo-fmt-scope-creep", Body: "see invariant-solver-eval-timing for context"},
		}
		g := BuildGraph(records)

		if got, want := g.Cites["avoid-cargo-fmt-scope-creep"], []string{"invariant-solver-eval-timing"}; !reflect.DeepEqual(got, want) {
			t.Errorf("Cites = %v, want %v", got, want)
		}
		if got, want := g.CitedBy["invariant-solver-eval-timing"], []string{"avoid-cargo-fmt-scope-creep"}; !reflect.DeepEqual(got, want) {
			t.Errorf("CitedBy = %v, want %v", got, want)
		}
	})

	t.Run("ignores short keys to avoid unrelated substring matches", func(t *testing.T) {
		records := []*memory.Memory{
			{Key: "z3-eval", Body: "unrelated"}, // 7 chars, below minCiteKeyLen
			{Key: "avoid-something-long-enough", Body: "mentions z3-eval in passing"},
		}
		g := BuildGraph(records)
		if len(g.Cites) != 0 {
			t.Errorf("expected no citations for a key shorter than %d chars, got %v", minCiteKeyLen, g.Cites)
		}
	})

	t.Run("ignores forgotten memories on both sides", func(t *testing.T) {
		records := []*memory.Memory{
			{Key: "invariant-solver-eval-timing", Body: "body", DecidedAt: "2026-01-01T00:00:00Z"},
			{Key: "avoid-cargo-fmt-scope-creep", Body: "see invariant-solver-eval-timing"},
		}
		g := BuildGraph(records)
		if len(g.Cites) != 0 || len(g.CitedBy) != 0 {
			t.Errorf("expected no citations touching a forgotten key, got Cites=%v CitedBy=%v", g.Cites, g.CitedBy)
		}
	})

	t.Run("a key does not cite itself", func(t *testing.T) {
		records := []*memory.Memory{
			{Key: "invariant-solver-eval-timing", Body: "invariant-solver-eval-timing is about timing"},
		}
		g := BuildGraph(records)
		if len(g.Cites) != 0 {
			t.Errorf("expected no self-citation, got %v", g.Cites)
		}
	})

	t.Run("multiple citers of the same key are all recorded, sorted", func(t *testing.T) {
		records := []*memory.Memory{
			{Key: "invariant-solver-eval-timing", Body: "body"},
			{Key: "avoid-cargo-fmt-scope-creep", Body: "cites invariant-solver-eval-timing"},
			{Key: "z3-check-site-count-thing", Body: "also cites invariant-solver-eval-timing"},
		}
		g := BuildGraph(records)
		got := append([]string(nil), g.CitedBy["invariant-solver-eval-timing"]...)
		sort.Strings(got)
		want := []string{"avoid-cargo-fmt-scope-creep", "z3-check-site-count-thing"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("CitedBy = %v, want %v", got, want)
		}
	})
}
