package site

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jcrussell/angr-memories/internal/memory"
)

func writeMemoriesFixture(t *testing.T, records []*memory.Memory) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "memories.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestWalk_GroupsIncarnationsByKeySortedByFirstSeen(t *testing.T) {
	path := writeMemoriesFixture(t, []*memory.Memory{
		{Key: "invariant-x", Body: "v2", FirstSeen: "2026-07-04T00:00:00Z"},
		{Key: "invariant-x", Body: "v1", FirstSeen: "2026-06-04T00:00:00Z", DecidedAt: "2026-06-05T00:00:00Z", Reason: "forget"},
	})
	s, err := Walk(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Prefixes) != 1 || s.Prefixes[0].Name != "invariant" {
		t.Fatalf("expected one 'invariant' prefix, got %+v", s.Prefixes)
	}
	pi := s.Prefixes[0]
	if len(pi.Groups) != 1 {
		t.Fatalf("expected one group (one distinct key), got %d", len(pi.Groups))
	}
	g := pi.Groups[0]
	if !g.Recreated() {
		t.Error("expected Recreated() true for a key with two incarnations")
	}
	if len(g.Incarnations) != 2 {
		t.Fatalf("expected 2 incarnations, got %d", len(g.Incarnations))
	}
	// Regardless of file order, oldest incarnation (by FirstSeen) must be first.
	if g.Incarnations[0].FirstSeen != "2026-06-04T00:00:00Z" || g.Incarnations[1].FirstSeen != "2026-07-04T00:00:00Z" {
		t.Errorf("incarnations not ordered by FirstSeen: %q then %q", g.Incarnations[0].FirstSeen, g.Incarnations[1].FirstSeen)
	}
	if !g.Current().Remembered() {
		t.Error("Current() should be the later (remembered) incarnation")
	}
	if s.TotalRemembered != 1 || s.TotalForgotten != 0 {
		t.Errorf("totals count by key (current state), got remembered=%d forgotten=%d, want 1/0",
			s.TotalRemembered, s.TotalForgotten)
	}
}

func TestWalk_SingleIncarnationIsNotRecreated(t *testing.T) {
	path := writeMemoriesFixture(t, []*memory.Memory{
		{Key: "avoid-x", Body: "v1", FirstSeen: "2026-06-04T00:00:00Z"},
	})
	s, err := Walk(path)
	if err != nil {
		t.Fatal(err)
	}
	g := s.Prefixes[0].Groups[0]
	if g.Recreated() {
		t.Error("a key with one incarnation should not be Recreated()")
	}
}

func TestWalk_BucketsByPrefixFamily(t *testing.T) {
	path := writeMemoriesFixture(t, []*memory.Memory{
		{Key: "invariant-a", Body: "x", FirstSeen: "2026-06-04T00:00:00Z"},
		{Key: "avoid-b", Body: "x", FirstSeen: "2026-06-04T00:00:00Z"},
		{Key: "totally-unknown-prefix", Body: "x", FirstSeen: "2026-06-04T00:00:00Z"},
	})
	s, err := Walk(path)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, pi := range s.Prefixes {
		names[pi.Name] = true
	}
	for _, want := range []string{"invariant", "avoid", "catch"} {
		if !names[want] {
			t.Errorf("expected a %q prefix bucket, got %v", want, names)
		}
	}
}

func TestWalk_LargeFamilyGetsChunkedSmallDoesNot(t *testing.T) {
	var records []*memory.Memory
	// One family well over defaultChunkCap...
	for i := 0; i < defaultChunkCap*3; i++ {
		records = append(records, &memory.Memory{
			Key:       "invariant-" + string(rune('a'+i%26)) + string(rune(i)),
			FirstSeen: "2026-06-04T00:00:00Z",
		})
	}
	// ...and one well under it.
	records = append(records, &memory.Memory{Key: "avoid-only-one", FirstSeen: "2026-06-04T00:00:00Z"})

	path := writeMemoriesFixture(t, records)
	s, err := Walk(path)
	if err != nil {
		t.Fatal(err)
	}

	var invariant, avoid *PrefixIndex
	for _, pi := range s.Prefixes {
		switch pi.Name {
		case "invariant":
			invariant = pi
		case "avoid":
			avoid = pi
		}
	}
	if invariant == nil || avoid == nil {
		t.Fatalf("expected both invariant and avoid prefixes, got %+v", s.Prefixes)
	}

	if !invariant.Chunked || len(invariant.Groups) != 0 {
		t.Errorf("large family should be Chunked with Groups empty, got Chunked=%v len(Groups)=%d",
			invariant.Chunked, len(invariant.Groups))
	}
	seen := map[string]int{}
	for _, c := range invariant.Chunks {
		if len(c.Groups) == 0 {
			t.Errorf("chunk %q is empty", c.Label)
		}
		for _, g := range c.Groups {
			seen[g.Key]++
		}
	}
	if len(seen) != defaultChunkCap*3 {
		t.Errorf("chunks cover %d distinct keys, want %d", len(seen), defaultChunkCap*3)
	}
	for k, n := range seen {
		if n != 1 {
			t.Errorf("key %q appears in %d chunks, want exactly 1", k, n)
		}
	}

	if avoid.Chunked || len(avoid.Chunks) != 0 || len(avoid.Groups) != 1 {
		t.Errorf("small family should stay flat, got Chunked=%v len(Chunks)=%d len(Groups)=%d",
			avoid.Chunked, len(avoid.Chunks), len(avoid.Groups))
	}
}
