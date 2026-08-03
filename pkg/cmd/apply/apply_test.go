package apply

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/internal/prune"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

func TestNewCmdApply_runFOverride(t *testing.T) {
	var captured *Options
	cmd := NewCmdApply(&cmdutil.Factory{IOStreams: iostreams.System()}, func(o *Options) error {
		captured = o
		return nil
	})
	cmd.SetArgs([]string{"--round", "R5", "--apply", "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if captured.Round != "R5" || !captured.Apply || !captured.Force {
		t.Errorf("flags not wired: %+v", captured)
	}
}

func TestOptionsValidate_rejectsBadRoundName(t *testing.T) {
	if err := (&Options{Round: "bad/round"}).Validate(); err == nil {
		t.Error("expected a slash in --round to be rejected")
	}
}

// setupApplyFixture writes a memories.jsonl with two remembered keys and a
// round with one batch manifest that forgets one of them, returning the
// paths needed to drive applyRun directly.
func setupApplyFixture(t *testing.T, rows []prune.Row) (memPath, roundsDir string) {
	t.Helper()
	dir := t.TempDir()
	memPath = filepath.Join(dir, "memories.jsonl")
	if err := memory.Save(memPath, []*memory.Memory{
		{Key: "invariant-alpha", Body: "solver body", FirstSeen: "2026-06-01T00:00:00Z"},
		{Key: "invariant-bravo", Body: "sees invariant-alpha in its own body", FirstSeen: "2026-06-01T00:00:00Z"},
	}); err != nil {
		t.Fatal(err)
	}

	roundsDir = filepath.Join(dir, "rounds")
	roundDir := filepath.Join(roundsDir, "R5")
	batchDir := filepath.Join(roundDir, "batches", "R5-INVARIANT")
	if err := os.MkdirAll(batchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	index := map[string][]string{"R5-INVARIANT": {"invariant-alpha", "invariant-bravo"}}
	idxBytes, _ := json.Marshal(index)
	if err := os.WriteFile(filepath.Join(roundDir, "batch-index.json"), idxBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prune.SaveManifest(filepath.Join(batchDir, "manifest.jsonl"), rows); err != nil {
		t.Fatal(err)
	}
	return memPath, roundsDir
}

func TestApplyRun_dryRunDoesNotMutateMemories(t *testing.T) {
	memPath, roundsDir := setupApplyFixture(t, []prune.Row{
		{Key: "invariant-alpha", Verdict: prune.VerdictForget, Reason: "stale"},
		{Key: "invariant-bravo", Verdict: prune.VerdictKeep},
	})

	io, _, out, _ := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R5", KeepListPath: filepath.Join(roundsDir, "no-keep.txt")}
	if err := applyRun(opts); err != nil {
		t.Fatalf("applyRun (dry-run): %v", err)
	}
	if !strings.Contains(out.String(), "dry-run") {
		t.Errorf("expected dry-run mode in output, got:\n%s", out.String())
	}

	records, err := memory.Load(memPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range records {
		if !m.Remembered() {
			t.Errorf("dry-run must not mutate memories.jsonl, but %q was forgotten", m.Key)
		}
	}
}

func TestApplyRun_orphanGuardDefersAndSkipsMutation(t *testing.T) {
	// invariant-bravo's body cites invariant-alpha, and bravo is kept (not
	// forgotten in this manifest) -- so forgetting alpha should be deferred.
	memPath, roundsDir := setupApplyFixture(t, []prune.Row{
		{Key: "invariant-alpha", Verdict: prune.VerdictForget, Reason: "stale"},
		{Key: "invariant-bravo", Verdict: prune.VerdictKeep},
	})

	io, _, out, _ := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R5", Apply: true, KeepListPath: filepath.Join(roundsDir, "no-keep.txt")}
	if err := applyRun(opts); err != nil {
		t.Fatalf("applyRun: %v", err)
	}
	if !strings.Contains(out.String(), "deferred=1") {
		t.Errorf("expected one deferral, got:\n%s", out.String())
	}

	records, err := memory.Load(memPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range records {
		if m.Key == "invariant-alpha" && !m.Remembered() {
			t.Error("deferred forget must not have been applied to memories.jsonl")
		}
	}
}

func TestApplyRun_appliesAndWritesDecisionsLog(t *testing.T) {
	memPath, roundsDir := setupApplyFixture(t, []prune.Row{
		{Key: "invariant-alpha", Verdict: prune.VerdictForget, Reason: "stale"},
		{Key: "invariant-bravo", Verdict: prune.VerdictForget, Reason: "also stale"},
	})

	io, _, out, _ := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R5", Apply: true, KeepListPath: filepath.Join(roundsDir, "no-keep.txt")}
	if err := applyRun(opts); err != nil {
		t.Fatalf("applyRun: %v", err)
	}
	if !strings.Contains(out.String(), "applied: forget=2") {
		t.Errorf("expected both forgets applied (no citer survives), got:\n%s", out.String())
	}

	records, err := memory.Load(memPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range records {
		if m.Remembered() {
			t.Errorf("expected %q to be forgotten after apply", m.Key)
		}
	}

	decisionsPath := filepath.Join(roundsDir, "R5", "decisions.jsonl")
	raw, err := os.ReadFile(decisionsPath)
	if err != nil {
		t.Fatalf("reading decisions.jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 decision records, got %d: %s", len(lines), raw)
	}
	var rec decisionRecord
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("parsing decision record: %v", err)
	}
	if rec.Round != "R5" || rec.Batch != "R5-INVARIANT" {
		t.Errorf("decision record missing provenance: %+v", rec)
	}
}

func TestApplyRun_mandatoryKeepViolationFailsBeforeAnyMutation(t *testing.T) {
	memPath, roundsDir := setupApplyFixture(t, []prune.Row{
		{Key: "invariant-alpha", Verdict: prune.VerdictForget, Reason: "stale"},
		{Key: "invariant-bravo", Verdict: prune.VerdictKeep},
	})
	keepListPath := filepath.Join(roundsDir, "keep.txt")
	if err := os.WriteFile(keepListPath, []byte("invariant-alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	io, _, _, _ := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R5", Apply: true, KeepListPath: keepListPath}
	if err := applyRun(opts); err == nil {
		t.Fatal("expected apply to refuse a manifest that forgets a mandatory-keep key")
	}

	records, err := memory.Load(memPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range records {
		if !m.Remembered() {
			t.Error("a failed validation must not mutate memories.jsonl at all")
		}
	}
}
