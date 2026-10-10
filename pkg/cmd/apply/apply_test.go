package apply

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	cmd.SetArgs([]string{"--round", "R5", "-f", "--skip-guard", "--source", "/tmp/angr"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if captured.Round != "R5" || !captured.Force || !captured.SkipGuard || captured.Source != "/tmp/angr" {
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
	opts := &Options{IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R5", Force: true, KeepListPath: filepath.Join(roundsDir, "no-keep.txt")}
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
	opts := &Options{IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R5", Force: true, KeepListPath: filepath.Join(roundsDir, "no-keep.txt")}
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
	opts := &Options{IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R5", Force: true, KeepListPath: keepListPath}
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

func TestApplyRun_backupOnlyWrittenOnRealApply(t *testing.T) {
	memPath, roundsDir := setupApplyFixture(t, []prune.Row{
		{Key: "invariant-alpha", Verdict: prune.VerdictForget, Reason: "stale"},
		{Key: "invariant-bravo", Verdict: prune.VerdictForget, Reason: "also stale"},
	})
	backupPath := filepath.Join(roundsDir, "R5", "memories.pre-apply.jsonl")
	original, err := os.ReadFile(memPath)
	if err != nil {
		t.Fatal(err)
	}

	io, _, _, _ := iostreams.Test()
	dryRunOpts := &Options{IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R5", KeepListPath: filepath.Join(roundsDir, "no-keep.txt")}
	if err := applyRun(dryRunOpts); err != nil {
		t.Fatalf("applyRun (dry-run): %v", err)
	}
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Errorf("dry-run must not write a backup, but %s exists (err=%v)", backupPath, err)
	}

	forceOpts := &Options{IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R5", Force: true, KeepListPath: filepath.Join(roundsDir, "no-keep.txt")}
	if err := applyRun(forceOpts); err != nil {
		t.Fatalf("applyRun (force): %v", err)
	}
	got, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("reading backup: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("backup content mismatch:\nwant %s\ngot  %s", original, got)
	}
}

// fakeBd returns a bdRunner backed by a fixed `bd memories --json` response
// and a per-key forget error map, plus a slice recording every call made --
// so live-push logic is testable without ever shelling out to a real bd.
func fakeBd(liveJSON []byte, forgetErrors map[string]error) (bdRunner, *[]string) {
	return fakeBdWithRemember(liveJSON, forgetErrors, nil)
}

// fakeBdWithRemember is fakeBd plus a per-key `bd remember --key` error map.
func fakeBdWithRemember(liveJSON []byte, forgetErrors, rememberErrors map[string]error) (bdRunner, *[]string) {
	var calls []string
	runner := func(ctx context.Context, dir string, timeout time.Duration, name string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if len(args) >= 2 && args[0] == "memories" && args[1] == "--json" {
			return liveJSON, nil
		}
		if len(args) == 2 && args[0] == "forget" {
			if err, ok := forgetErrors[args[1]]; ok {
				return nil, err
			}
			return nil, nil
		}
		if len(args) == 5 && args[0] == "remember" && args[1] == "--key" && args[3] == "--" {
			if err, ok := rememberErrors[args[2]]; ok {
				return nil, err
			}
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected bd call: %v", args)
	}
	return runner, &calls
}

func TestApplyRun_livePushOnlyWhenForceAndSourceBothSet(t *testing.T) {
	liveJSON := []byte(`{"invariant-alpha":"solver body","invariant-bravo":"sees invariant-alpha in its own body"}`)

	// Source set but not Force: dry-run preview only, no live calls, no files.
	memPath, roundsDir := setupApplyFixture(t, []prune.Row{
		{Key: "invariant-alpha", Verdict: prune.VerdictForget, Reason: "stale"},
		{Key: "invariant-bravo", Verdict: prune.VerdictForget, Reason: "also stale"},
	})
	runBd, calls := fakeBd(liveJSON, nil)
	io, _, out, _ := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R5", Source: "/fake/angr", KeepListPath: filepath.Join(roundsDir, "no-keep.txt"), runBd: runBd}
	if err := applyRun(opts); err != nil {
		t.Fatalf("applyRun: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("dry-run must not call bd, got calls: %v", *calls)
	}
	if !strings.Contains(out.String(), "live-push preview") {
		t.Errorf("expected a live-push preview line, got:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(roundsDir, "R5", "bd-live-backup.jsonl")); !os.IsNotExist(err) {
		t.Error("dry-run must not write bd-live-backup.jsonl")
	}

	// Force set but no Source: archive-only, no live push.
	memPath2, roundsDir2 := setupApplyFixture(t, []prune.Row{
		{Key: "invariant-alpha", Verdict: prune.VerdictForget, Reason: "stale"},
		{Key: "invariant-bravo", Verdict: prune.VerdictForget, Reason: "also stale"},
	})
	opts2 := &Options{IO: io, MemoriesPath: memPath2, RoundsDir: roundsDir2, Round: "R5", Force: true, KeepListPath: filepath.Join(roundsDir2, "no-keep.txt")}
	if err := applyRun(opts2); err != nil {
		t.Fatalf("applyRun: %v", err)
	}
	if _, err := os.Stat(filepath.Join(roundsDir2, "R5", "bd-live-backup.jsonl")); !os.IsNotExist(err) {
		t.Error("apply without --source must not write bd-live-backup.jsonl")
	}
}

func TestLivePush_backsUpThenForgetsAndIsRetrySafe(t *testing.T) {
	liveJSON := []byte(`{"invariant-alpha":"solver body","invariant-bravo":"sees invariant-alpha in its own body"}`)
	// alpha looks already-gone live (bd reports it as not found); bravo succeeds.
	runBd, _ := fakeBd(liveJSON, map[string]error{
		"invariant-alpha": fmt.Errorf("exit status 1 (stderr: memory not found: invariant-alpha)"),
	})

	memPath, roundsDir := setupApplyFixture(t, []prune.Row{
		{Key: "invariant-alpha", Verdict: prune.VerdictForget, Reason: "stale"},
		{Key: "invariant-bravo", Verdict: prune.VerdictForget, Reason: "also stale"},
	})
	io, _, out, _ := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R5", Force: true, Source: "/fake/angr", Timeout: time.Second, KeepListPath: filepath.Join(roundsDir, "no-keep.txt"), runBd: runBd}
	if err := applyRun(opts); err != nil {
		t.Fatalf("applyRun: %v, output:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "live-push (source=/fake/angr): rewrote=0 forgot=2 failed=0") {
		t.Errorf("expected both keys treated as pushed (not-found tolerated), got:\n%s", out.String())
	}

	backupRaw, err := os.ReadFile(filepath.Join(roundsDir, "R5", "bd-live-backup.jsonl"))
	if err != nil {
		t.Fatalf("reading bd-live-backup.jsonl: %v", err)
	}
	if !strings.Contains(string(backupRaw), "invariant-alpha") || !strings.Contains(string(backupRaw), "invariant-bravo") {
		t.Errorf("expected both keys' bodies backed up before forgetting, got:\n%s", backupRaw)
	}

	logRaw, err := os.ReadFile(filepath.Join(roundsDir, "R5", "live-push.jsonl"))
	if err != nil {
		t.Fatalf("reading live-push.jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(logRaw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 live-push records, got %d: %s", len(lines), logRaw)
	}
	for _, line := range lines {
		var rec livePushResult
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("parsing live-push record: %v", err)
		}
		if !rec.Success {
			t.Errorf("expected %q to be recorded as a successful push (already-gone tolerated), got: %+v", rec.Key, rec)
		}
	}
}

func TestLivePush_realFailureReturnsErrorButKeepsArchiveMutation(t *testing.T) {
	liveJSON := []byte(`{"invariant-alpha":"solver body","invariant-bravo":"sees invariant-alpha in its own body"}`)
	runBd, _ := fakeBd(liveJSON, map[string]error{
		"invariant-alpha": fmt.Errorf("exit status 1 (stderr: dolt server unreachable)"),
	})

	memPath, roundsDir := setupApplyFixture(t, []prune.Row{
		{Key: "invariant-alpha", Verdict: prune.VerdictForget, Reason: "stale"},
		{Key: "invariant-bravo", Verdict: prune.VerdictForget, Reason: "also stale"},
	})
	io, _, _, _ := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R5", Force: true, Source: "/fake/angr", Timeout: time.Second, KeepListPath: filepath.Join(roundsDir, "no-keep.txt"), runBd: runBd}
	err := applyRun(opts)
	if err == nil {
		t.Fatal("expected a non-nil error when a live push genuinely fails")
	}

	// The archive-side apply already completed and is not rolled back.
	records, loadErr := memory.Load(memPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	for _, m := range records {
		if m.Remembered() {
			t.Errorf("expected %q to stay forgotten locally despite the live-push failure", m.Key)
		}
	}
	if _, statErr := os.Stat(filepath.Join(roundsDir, "R5", "decisions.jsonl")); statErr != nil {
		t.Errorf("expected decisions.jsonl to exist despite the live-push failure: %v", statErr)
	}
}

// mergeRows merges invariant-bravo into invariant-alpha. bravo's body cites
// alpha, but bravo is itself being merged away, so the orphan guard does not
// defer anything.
func mergeRows() []prune.Row {
	return []prune.Row{
		{Key: "invariant-alpha", Verdict: prune.VerdictMerge, Canonical: true, MergedBody: "solver body plus bravo's detail", Reason: "canonical"},
		{Key: "invariant-bravo", Verdict: prune.VerdictMerge, MergedInto: "invariant-alpha", Reason: "folded into alpha"},
	}
}

func TestLivePush_rewritesCanonicalBodyBeforeForgettingSubordinate(t *testing.T) {
	liveJSON := []byte(`{"invariant-alpha":"solver body","invariant-bravo":"sees invariant-alpha in its own body"}`)
	runBd, calls := fakeBd(liveJSON, nil)

	memPath, roundsDir := setupApplyFixture(t, mergeRows())
	io, _, out, _ := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R5", Force: true, Source: "/fake/angr", Timeout: time.Second, KeepListPath: filepath.Join(roundsDir, "no-keep.txt"), runBd: runBd}
	if err := applyRun(opts); err != nil {
		t.Fatalf("applyRun: %v, output:\n%s", err, out.String())
	}

	want := []string{
		"memories --json",
		"remember --key invariant-alpha -- solver body plus bravo's detail",
		"forget invariant-bravo",
	}
	if strings.Join(*calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("bd calls:\n got %q\nwant %q", *calls, want)
	}
	if !strings.Contains(out.String(), "rewrote=1 forgot=1 failed=0") {
		t.Errorf("expected rewrote=1 forgot=1, got:\n%s", out.String())
	}

	backupRaw, err := os.ReadFile(filepath.Join(roundsDir, "R5", "bd-live-backup.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(backupRaw), `"key":"invariant-alpha","body":"solver body"`) {
		t.Errorf("expected the canonical's pre-merge live body backed up, got:\n%s", backupRaw)
	}
}

func TestLivePush_canonicalAlreadyCurrentIsNotRewritten(t *testing.T) {
	liveJSON := []byte(`{"invariant-alpha":"solver body plus bravo's detail","invariant-bravo":"sees invariant-alpha in its own body"}`)
	runBd, calls := fakeBd(liveJSON, nil)

	memPath, roundsDir := setupApplyFixture(t, mergeRows())
	io, _, _, _ := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R5", Force: true, Source: "/fake/angr", Timeout: time.Second, KeepListPath: filepath.Join(roundsDir, "no-keep.txt"), runBd: runBd}
	if err := applyRun(opts); err != nil {
		t.Fatalf("applyRun: %v", err)
	}
	for _, c := range *calls {
		if strings.HasPrefix(c, "remember") {
			t.Errorf("live body already equals merged_body; expected no remember call, got %q", c)
		}
	}
}

func TestLivePush_canonicalWriteFailureSkipsAllForgets(t *testing.T) {
	liveJSON := []byte(`{"invariant-alpha":"solver body","invariant-bravo":"sees invariant-alpha in its own body"}`)
	runBd, calls := fakeBdWithRemember(liveJSON, nil, map[string]error{
		"invariant-alpha": fmt.Errorf("exit status 1 (stderr: dolt server unreachable)"),
	})

	memPath, roundsDir := setupApplyFixture(t, mergeRows())
	io, _, _, _ := iostreams.Test()
	opts := &Options{IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R5", Force: true, Source: "/fake/angr", Timeout: time.Second, KeepListPath: filepath.Join(roundsDir, "no-keep.txt"), runBd: runBd}
	err := applyRun(opts)
	if err == nil || !strings.Contains(err.Error(), "skipped all 1 forgets") {
		t.Fatalf("expected a canonical-write failure that skips forgets, got: %v", err)
	}
	for _, c := range *calls {
		if strings.HasPrefix(c, "forget") {
			t.Errorf("no forget may run after a canonical write fails, got %q", c)
		}
	}
}

func TestLivePush_rerunAppendsRatherThanTruncatingBackup(t *testing.T) {
	memPath, roundsDir := setupApplyFixture(t, mergeRows())
	io, _, _, _ := iostreams.Test()

	first, _ := fakeBdWithRemember([]byte(`{"invariant-alpha":"solver body","invariant-bravo":"sees invariant-alpha in its own body"}`), nil,
		map[string]error{"invariant-alpha": fmt.Errorf("exit status 1 (stderr: dolt server unreachable)")})
	opts := &Options{IO: io, MemoriesPath: memPath, RoundsDir: roundsDir, Round: "R5", Force: true, Source: "/fake/angr", Timeout: time.Second, KeepListPath: filepath.Join(roundsDir, "no-keep.txt"), runBd: first}
	if err := applyRun(opts); err == nil {
		t.Fatal("expected the first run to fail")
	}

	second, _ := fakeBd([]byte(`{"invariant-alpha":"solver body","invariant-bravo":"sees invariant-alpha in its own body"}`), nil)
	opts.runBd = second
	if err := applyRun(opts); err != nil {
		t.Fatalf("retry: %v", err)
	}

	logRaw, err := os.ReadFile(filepath.Join(roundsDir, "R5", "live-push.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// run 1: failed remember; run 2: remember + forget.
	if n := len(strings.Split(strings.TrimSpace(string(logRaw)), "\n")); n != 3 {
		t.Errorf("expected 3 appended live-push records across both runs, got %d:\n%s", n, logRaw)
	}
}
