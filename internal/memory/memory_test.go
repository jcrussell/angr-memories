package memory

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPrefixOf(t *testing.T) {
	cases := map[string]string{
		"invariant-solver-eval-timing": "invariant",
		"avoid-cargo-fmt-scope-creep":  "avoid",
		"z3-check-site-count":          "z3",
		"rust-state-proxy":             "rust",
		"benchmark-peak-memory-gate":   "benchmark",
		"bench-cow-fork-scaling":       "benchmark", // "bench" and "benchmark" share a family
		"bd-memory-path-audit":         "ops",
		"claude-md-diff":               "ops",
		"v5a5-slice-4c":                "v5a5",
		"inspect-bp-writeback":         "inspect",
		"syscall-access-handler":       "syscall",
		"prefetch-disabled-by-default": "catch", // no known prefix -> catch-all
		"nodash":                       "catch", // no "-" at all
	}
	for key, want := range cases {
		if got := PrefixOf(key); got != want {
			t.Errorf("PrefixOf(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestAlphaSplit(t *testing.T) {
	t.Run("under cap returns one chunk", func(t *testing.T) {
		keys := []string{"c", "a", "b"}
		chunks := AlphaSplit(keys, 10)
		if len(chunks) != 1 {
			t.Fatalf("got %d chunks, want 1", len(chunks))
		}
		if !reflect.DeepEqual(chunks[0], []string{"a", "b", "c"}) {
			t.Errorf("chunk not sorted: %v", chunks[0])
		}
	})

	t.Run("splits evenly and covers every key exactly once", func(t *testing.T) {
		keys := make([]string, 0, 100)
		for i := 0; i < 100; i++ {
			keys = append(keys, string(rune('a'+i%26))+string(rune(i)))
		}
		chunks := AlphaSplit(keys, 30)
		if len(chunks) != 4 { // ceil(100/30) = 4
			t.Fatalf("got %d chunks, want 4", len(chunks))
		}
		seen := map[string]int{}
		for _, c := range chunks {
			if len(c) == 0 {
				t.Error("found an empty chunk")
			}
			for _, k := range c {
				seen[k]++
			}
		}
		if len(seen) != len(keys) {
			t.Fatalf("got %d distinct keys across chunks, want %d", len(seen), len(keys))
		}
		for k, n := range seen {
			if n != 1 {
				t.Errorf("key %q appeared in %d chunks, want 1", k, n)
			}
		}
	})

	t.Run("chunk sizes never differ by more than one", func(t *testing.T) {
		keys := make([]string, 205)
		for i := range keys {
			keys[i] = string(rune('a' + i%26))
		}
		chunks := AlphaSplit(keys, 70)
		min, max := len(chunks[0]), len(chunks[0])
		for _, c := range chunks {
			if len(c) < min {
				min = len(c)
			}
			if len(c) > max {
				max = len(c)
			}
		}
		if max-min > 1 {
			t.Errorf("chunk sizes range from %d to %d, want at most 1 apart", min, max)
		}
	})
}

func TestDiff(t *testing.T) {
	at := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	ts := at.UTC().Format(time.RFC3339)

	t.Run("new live key with no open record is opened", func(t *testing.T) {
		opened, closed, edited := Diff(nil, map[string]string{"new-key": "body"}, at)
		if len(closed) != 0 || len(edited) != 0 {
			t.Fatalf("expected no closed/edited, got closed=%d edited=%d", len(closed), len(edited))
		}
		if len(opened) != 1 || opened[0].Key != "new-key" || opened[0].Body != "body" || opened[0].FirstSeen != ts {
			t.Fatalf("unexpected opened record: %+v", opened)
		}
	})

	t.Run("open key missing from live is closed with a reason breadcrumb", func(t *testing.T) {
		m := &Memory{Key: "gone", Body: "old body"}
		opened, closed, edited := Diff([]*Memory{m}, map[string]string{}, at)
		if len(opened) != 0 || len(edited) != 0 {
			t.Fatalf("expected no opened/edited, got opened=%d edited=%d", len(opened), len(edited))
		}
		if len(closed) != 1 || closed[0] != m {
			t.Fatalf("expected m to be closed in place, got %+v", closed)
		}
		if m.DecidedAt != ts {
			t.Errorf("DecidedAt = %q, want %q", m.DecidedAt, ts)
		}
		if m.Reason == "" {
			t.Error("Reason should be set on an auto-close, not left empty (indistinguishable from a curated close otherwise)")
		}
	})

	t.Run("open key still live with same body is untouched", func(t *testing.T) {
		m := &Memory{Key: "steady", Body: "same"}
		opened, closed, edited := Diff([]*Memory{m}, map[string]string{"steady": "same"}, at)
		if len(opened) != 0 || len(closed) != 0 || len(edited) != 0 {
			t.Fatalf("expected no changes, got opened=%d closed=%d edited=%d", len(opened), len(closed), len(edited))
		}
	})

	t.Run("open key still live with changed body is edited in place, not closed", func(t *testing.T) {
		m := &Memory{Key: "changed", Body: "old"}
		opened, closed, edited := Diff([]*Memory{m}, map[string]string{"changed": "new"}, at)
		if len(opened) != 0 || len(closed) != 0 {
			t.Fatalf("expected no opened/closed, got opened=%d closed=%d", len(opened), len(closed))
		}
		if len(edited) != 1 || edited[0] != m || m.Body != "new" {
			t.Fatalf("expected m.Body updated in place to %q, got %+v", "new", m)
		}
		if m.UpdatedAt != ts {
			t.Errorf("UpdatedAt = %q, want %q (needed so --delta-since can find edited, not just new, keys)", m.UpdatedAt, ts)
		}
	})

	t.Run("a previously-closed key reappearing live needs no special-casing", func(t *testing.T) {
		// Recreation: a key forgotten in the past has no open record, so a
		// reappearance in `live` falls into the same "opened" branch as a
		// never-before-seen key -- this is the behavior the whole
		// incarnation design depends on.
		opened, closed, edited := Diff(nil, map[string]string{"recreated": "new body"}, at)
		if len(closed) != 0 || len(edited) != 0 {
			t.Fatalf("expected no closed/edited, got closed=%d edited=%d", len(closed), len(edited))
		}
		if len(opened) != 1 || opened[0].Key != "recreated" {
			t.Fatalf("expected a fresh incarnation opened, got %+v", opened)
		}
	})

	t.Run("mixed batch: separates opened/closed/edited/untouched correctly", func(t *testing.T) {
		untouched := &Memory{Key: "untouched", Body: "same"}
		toClose := &Memory{Key: "toClose", Body: "old"}
		toEdit := &Memory{Key: "toEdit", Body: "old"}
		live := map[string]string{
			"untouched": "same",
			"toEdit":    "new",
			"toOpen":    "brand new",
		}
		opened, closed, edited := Diff([]*Memory{untouched, toClose, toEdit}, live, at)
		if len(opened) != 1 || opened[0].Key != "toOpen" {
			t.Errorf("opened = %+v, want just toOpen", opened)
		}
		if len(closed) != 1 || closed[0] != toClose {
			t.Errorf("closed = %+v, want just toClose", closed)
		}
		if len(edited) != 1 || edited[0] != toEdit {
			t.Errorf("edited = %+v, want just toEdit", edited)
		}
	})
}

func TestParse(t *testing.T) {
	t.Run("parses one record per line, skipping blank lines", func(t *testing.T) {
		input := `{"key":"a","body":"x","first_seen":"2026-06-04T00:00:00Z"}` + "\n\n" +
			`{"key":"b","body":"y","first_seen":"2026-06-05T00:00:00Z"}` + "\n"
		got, err := Parse(strings.NewReader(input))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if len(got) != 2 || got[0].Key != "a" || got[1].Key != "b" {
			t.Errorf("got %+v, want records for keys a and b", got)
		}
	})

	t.Run("Load is Parse over the file's bytes -- same result either way", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "memories.jsonl")
		want := []*Memory{{Key: "solo", Body: "b", FirstSeen: "2026-06-04T00:00:00Z"}}
		if err := Save(path, want); err != nil {
			t.Fatal(err)
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fromParse, err := Parse(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		fromLoad, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !reflect.DeepEqual(fromParse, fromLoad) {
			t.Errorf("Parse(bytes) = %+v, Load(path) = %+v, want equal", fromParse, fromLoad)
		}
	})

	t.Run("malformed JSON line errors", func(t *testing.T) {
		if _, err := Parse(strings.NewReader("not json\n")); err == nil {
			t.Error("expected an error for a malformed line")
		}
	})
}

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memories.jsonl")

	records := []*Memory{
		{Key: "b-key", Body: "b body", FirstSeen: "2026-07-04T00:00:00Z"},
		{Key: "a-key", Body: "a body", FirstSeen: "2026-06-04T00:00:00Z", DecidedAt: "2026-06-05T00:00:00Z", Reason: "forgotten"},
		{Key: "a-key", Body: "a body v2", FirstSeen: "2026-07-04T00:00:00Z"}, // recreated incarnation
	}
	if err := Save(path, records); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d records, want 3", len(got))
	}
	// Save sorts by key then first_seen -- a-key's two incarnations should
	// come before b-key, oldest incarnation first.
	wantOrder := []string{"a-key", "a-key", "b-key"}
	for i, k := range wantOrder {
		if got[i].Key != k {
			t.Errorf("record %d: key = %q, want %q", i, got[i].Key, k)
		}
	}
	if got[0].FirstSeen >= got[1].FirstSeen {
		t.Errorf("a-key incarnations not in first_seen order: %q then %q", got[0].FirstSeen, got[1].FirstSeen)
	}
}

func TestAggregate(t *testing.T) {
	records := []*Memory{
		{Key: "invariant-a", Body: "x", FirstSeen: "2026-06-04T00:00:00Z"},
		{Key: "invariant-b", Body: "x", FirstSeen: "2026-06-04T00:00:00Z", DecidedAt: "2026-06-11T00:00:00Z", Reason: "r"},
		{Key: "avoid-c", Body: "x1", FirstSeen: "2026-06-04T00:00:00Z", DecidedAt: "2026-06-05T00:00:00Z", Reason: "r"},
		{Key: "avoid-c", Body: "x2", FirstSeen: "2026-07-04T00:00:00Z"}, // recreated
	}
	s := Aggregate(records)

	if s.TotalRemembered != 2 || s.TotalForgotten != 2 {
		t.Errorf("TotalRemembered=%d TotalForgotten=%d, want 2 and 2", s.TotalRemembered, s.TotalForgotten)
	}
	if s.Recreated != 1 {
		t.Errorf("Recreated = %d, want 1", s.Recreated)
	}
	if got := s.ByPrefix["invariant"]; got == nil || got.Remembered != 1 || got.Forgotten != 1 {
		t.Errorf("ByPrefix[invariant] = %+v, want {Remembered:1 Forgotten:1}", got)
	}
	if got := s.ByPrefix["avoid"]; got == nil || got.Remembered != 1 || got.Forgotten != 1 {
		t.Errorf("ByPrefix[avoid] = %+v, want {Remembered:1 Forgotten:1}", got)
	}
	if len(s.SurvivalDurations) != 2 {
		t.Fatalf("got %d survival durations, want 2 (one per forgotten record)", len(s.SurvivalDurations))
	}
	// sorted ascending: avoid-c survived 1 day, invariant-b survived 7 days.
	if s.SurvivalDurations[0] != 24*time.Hour || s.SurvivalDurations[1] != 7*24*time.Hour {
		t.Errorf("SurvivalDurations = %v, want [24h, 168h] ascending", s.SurvivalDurations)
	}
}

func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memories.jsonl")

	original := []*Memory{{Key: "k", Body: "original", FirstSeen: "2026-06-04T00:00:00Z"}}
	if err := Save(path, original); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	// A second Save should leave no stray temp files behind on success...
	updated := []*Memory{{Key: "k", Body: "updated", FirstSeen: "2026-06-04T00:00:00Z"}}
	if err := Save(path, updated); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "memories.jsonl" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected exactly one file (memories.jsonl) after Save, got %v", names)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].Body != "updated" {
		t.Fatalf("expected updated content, got %+v", got)
	}

	// ...and Save must never truncate the target before the new content is
	// fully written and valid -- verified structurally in Save via
	// temp-file + rename (this test only proves no temp litter and correct
	// final content; the crash-mid-write case can't be simulated cheaply
	// in a unit test, but the temp+rename shape is what makes it safe).
}
