package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

func TestNewCmdSearch_runFOverride(t *testing.T) {
	var captured *Options
	cmd := NewCmdSearch(&cmdutil.Factory{IOStreams: iostreams.System()}, func(o *Options) error {
		captured = o
		return nil
	})
	cmd.SetArgs([]string{"solver eval", "--memories", "m.jsonl", "--prefix", "invariant", "--all", "--fuzzy", "--limit", "5"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if captured.Query != "solver eval" || captured.MemoriesPath != "m.jsonl" || captured.Prefix != "invariant" ||
		!captured.All || !captured.Fuzzy || captured.Limit != 5 {
		t.Errorf("flags not wired: %+v", captured)
	}
}

func TestOptionsValidate(t *testing.T) {
	if err := (&Options{Limit: 0}).Validate(); err == nil {
		t.Error("expected --limit=0 to be rejected")
	}
	if err := (&Options{Limit: -1}).Validate(); err == nil {
		t.Error("expected a negative --limit to be rejected")
	}
	if err := (&Options{Limit: 20}).Validate(); err != nil {
		t.Errorf("expected a positive --limit to pass, got %v", err)
	}
}

func writeMemories(t *testing.T, records []*memory.Memory) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memories.jsonl")
	if err := memory.Save(path, records); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSearchRun_findsRelevantMatchByKeyword(t *testing.T) {
	memPath := writeMemories(t, []*memory.Memory{
		{Key: "invariant-solver-eval-timing", Body: "The solver evaluates constraints lazily to avoid blowup.", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "avoid-unrelated-topic", Body: "Completely unrelated content about file permissions.", FirstSeen: "2026-06-01T00:00:00Z"},
	})

	io, _, out, _ := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, Query: "solver constraints", Limit: 20}
	if err := searchRun(opts); err != nil {
		t.Fatalf("searchRun: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "invariant-solver-eval-timing") {
		t.Errorf("expected the relevant key in results, got:\n%s", got)
	}
	if strings.Contains(got, "avoid-unrelated-topic") {
		t.Errorf("expected the unrelated key excluded, got:\n%s", got)
	}
}

func TestSearchRun_excludesForgottenByDefault(t *testing.T) {
	memPath := writeMemories(t, []*memory.Memory{
		{Key: "invariant-forgotten-topic", Body: "forgotten solver details", FirstSeen: "2026-06-01T00:00:00Z", DecidedAt: "2026-06-02T00:00:00Z", Reason: "r"},
	})

	io, _, out, _ := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, Query: "solver", Limit: 20}
	if err := searchRun(opts); err != nil {
		t.Fatalf("searchRun: %v", err)
	}
	if !strings.Contains(out.String(), "no matches") {
		t.Errorf("expected a forgotten record to be excluded by default, got:\n%s", out.String())
	}

	io2, _, outBuf2, _ := iostreams.Test()
	opts2 := &Options{IO: io2, MemoriesPath: memPath, Query: "solver", Limit: 20, All: true}
	if err := searchRun(opts2); err != nil {
		t.Fatalf("searchRun: %v", err)
	}
	if !strings.Contains(outBuf2.String(), "invariant-forgotten-topic") {
		t.Errorf("expected --all to include the forgotten record, got:\n%s", outBuf2.String())
	}
	if !strings.Contains(outBuf2.String(), "forgotten") {
		t.Errorf("expected the forgotten status label in output, got:\n%s", outBuf2.String())
	}
}

func TestSearchRun_prefixFilter(t *testing.T) {
	memPath := writeMemories(t, []*memory.Memory{
		{Key: "invariant-solver-topic", Body: "solver details here", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "avoid-solver-topic", Body: "solver details here too", FirstSeen: "2026-06-01T00:00:00Z"},
	})

	io, _, out, _ := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, Query: "solver", Limit: 20, Prefix: "avoid"}
	if err := searchRun(opts); err != nil {
		t.Fatalf("searchRun: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "avoid-solver-topic") {
		t.Errorf("expected the avoid-prefixed key, got:\n%s", got)
	}
	if strings.Contains(got, "invariant-solver-topic") {
		t.Errorf("expected the invariant-prefixed key excluded by --prefix, got:\n%s", got)
	}
}

func TestSearchRun_fuzzyMatchesATypo(t *testing.T) {
	memPath := writeMemories(t, []*memory.Memory{
		{Key: "invariant-concretization-notes", Body: "concretization details", FirstSeen: "2026-06-01T00:00:00Z"},
	})

	io, _, out, _ := iostreams.Test()
	// One-character typo ("concretisation" vs "concretization").
	opts := &Options{IO: io, MemoriesPath: memPath, Query: "concretisation", Limit: 20, Fuzzy: true}
	if err := searchRun(opts); err != nil {
		t.Fatalf("searchRun: %v", err)
	}
	if !strings.Contains(out.String(), "invariant-concretization-notes") {
		t.Errorf("expected a fuzzy match despite the typo, got:\n%s", out.String())
	}
}

func TestSearchRun_limitCapsResults(t *testing.T) {
	var records []*memory.Memory
	for i := 0; i < 10; i++ {
		records = append(records, &memory.Memory{
			Key:       "invariant-topic-" + string(rune('a'+i)),
			Body:      "shared keyword appears in every record here",
			FirstSeen: "2026-06-01T00:00:00Z",
		})
	}
	memPath := writeMemories(t, records)

	io, _, out, _ := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, Query: "shared keyword", Limit: 3}
	if err := searchRun(opts); err != nil {
		t.Fatalf("searchRun: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Errorf("got %d result lines, want 3 (--limit)", len(lines))
	}
}

func TestBuildIndex_indexesOnlyLatestIncarnation(t *testing.T) {
	dir := t.TempDir()
	memPath := filepath.Join(dir, "memories.jsonl")
	// Write two incarnations of the same key directly, oldest first, as
	// memory.Save would order them.
	f, err := os.Create(memPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		`{"key":"invariant-recreated-key","body":"old body","first_seen":"2026-06-01T00:00:00Z","decided_at":"2026-06-02T00:00:00Z","reason":"r"}`,
		`{"key":"invariant-recreated-key","body":"new body","first_seen":"2026-07-01T00:00:00Z"}`,
	} {
		if _, err := f.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()

	records, err := memory.Load(memPath)
	if err != nil {
		t.Fatal(err)
	}
	_, byKey, err := buildIndex(records)
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	if got := byKey["invariant-recreated-key"]; got.Body != "new body" {
		t.Errorf("indexed body = %q, want the latest incarnation's body", got.Body)
	}
}
