package sync

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

func openRecords(n int) []*memory.Memory {
	out := make([]*memory.Memory, n)
	for i := range out {
		out[i] = &memory.Memory{Key: string(rune('a'+i%26)) + string(rune(i))}
	}
	return out
}

func liveFrom(open []*memory.Memory, skip int) map[string]string {
	live := make(map[string]string, len(open))
	for i, m := range open {
		if i < skip {
			continue
		}
		live[m.Key] = "body"
	}
	return live
}

// TestSanityCheckLive_EmptyLiveIsAlwaysRejected covers the confirmed
// mass-close bug: `bd memories --json` exiting 0 with an empty or
// truncated-but-valid map must never be silently treated as "everything
// was forgotten," regardless of archive size.
func TestSanityCheckLive_EmptyLiveIsAlwaysRejected(t *testing.T) {
	for _, n := range []int{0, 1, minOpenForGuard - 1, minOpenForGuard, 1289} {
		open := openRecords(n)
		if err := sanityCheckLive(map[string]string{}, open); err == nil {
			t.Errorf("open=%d: expected empty live snapshot to be rejected, got nil error", n)
		}
	}
}

func TestSanityCheckLive_SmallArchiveSkipsFractionGuard(t *testing.T) {
	open := openRecords(minOpenForGuard - 1) // below the guard threshold
	// Every one of open's keys closes (100%), but live is non-empty (one
	// unrelated new key) so this exercises the fraction guard specifically,
	// not the separate always-on empty-live check.
	live := map[string]string{"unrelated-new-key": "body"}
	if err := sanityCheckLive(live, open); err != nil {
		t.Errorf("small archive should skip the fraction guard even at 100%% closure, got: %v", err)
	}
}

func TestSanityCheckLive_LargeArchiveRejectsOverThreshold(t *testing.T) {
	open := openRecords(1289)
	// Non-empty live snapshot (one unrelated key), but none of open's keys
	// are in it -- a 100% mass-close, distinct from the empty-live case.
	live := map[string]string{"unrelated-new-key": "body"}
	err := sanityCheckLive(live, open)
	if err == nil {
		t.Fatal("expected a 100% mass-close on a 1289-record archive to be rejected")
	}
}

func TestSanityCheckLive_LargeArchiveAllowsSmallFraction(t *testing.T) {
	open := openRecords(1289)
	skip := 10 // 10 keys close (~0.8%), well under the 20% threshold
	live := liveFrom(open, skip)
	if err := sanityCheckLive(live, open); err != nil {
		t.Errorf("closing 10/1289 (~0.8%%) should be well under the threshold, got: %v", err)
	}
}

func TestSanityCheckLive_ThresholdBoundary(t *testing.T) {
	open := openRecords(1000)

	// Exactly at 20% should still pass (guard is "> maxCloseFraction", not ">=").
	atThreshold := liveFrom(open, 200) // 200/1000 = 20% close
	if err := sanityCheckLive(atThreshold, open); err != nil {
		t.Errorf("exactly at the 20%% threshold should pass, got: %v", err)
	}

	// One more than 20% should fail.
	overThreshold := liveFrom(open, 201) // 201/1000 = 20.1% close
	if err := sanityCheckLive(overThreshold, open); err == nil {
		t.Error("expected just over the 20% threshold to be rejected")
	}
}

func TestSanityCheckLive_NoClosuresAlwaysPasses(t *testing.T) {
	open := openRecords(1289)
	live := liveFrom(open, 0) // every key still live, nothing closes
	// Live snapshot is bigger than open (no new keys added here, but the
	// point is: 0 closures should never trip the fraction guard).
	if err := sanityCheckLive(live, open); err != nil {
		t.Errorf("no closures should never be rejected, got: %v", err)
	}
}

// liveSnapshot's own "--source is required" check must exit 2 like every
// other flag-validation error, not 1 -- it's currently only reachable by a
// caller bypassing cobra's MarkFlagRequired("source"), but that's exactly
// what a test calling syncRun/liveSnapshot directly does.
func TestLiveSnapshot_EmptySourceIsAFlagError(t *testing.T) {
	_, err := liveSnapshot("", time.Second)
	if err == nil {
		t.Fatal("expected an error for an empty --source")
	}
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("expected a *cmdutil.FlagError so it maps to exit code 2, got %T: %v", err, err)
	}
}

// A truly nonexistent --memories path (not even an empty file) must
// bootstrap a fresh archive, since creating the archive is sync's job.
func TestSyncRun_bootstrapsFromNonexistentMemoriesPath(t *testing.T) {
	sourceDir := t.TempDir()
	installFakeBd(t, `{"fresh-key-one":"body one","fresh-key-two":"body two"}`)

	memPath := filepath.Join(t.TempDir(), "does-not-exist.jsonl")
	io, _, _, _ := iostreams.Test()
	opts := &Options{
		IO:           io,
		Source:       sourceDir,
		MemoriesPath: memPath,
		Timeout:      5 * time.Second,
	}

	if err := syncRun(opts); err != nil {
		t.Fatalf("syncRun: %v", err)
	}

	records, err := memory.Load(memPath)
	if err != nil {
		t.Fatalf("expected sync to create %s, got: %v", memPath, err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 fresh records, got %d: %v", len(records), records)
	}
}

// installFakeBd puts a fake `bd` executable on PATH that answers `bd
// memories --json` with jsonOut, so syncRun can be exercised without a
// real bd binary or .beads/ database.
func installFakeBd(t *testing.T, jsonOut string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncat <<'FAKE_BD_EOF'\n" + jsonOut + "\nFAKE_BD_EOF\n"
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
