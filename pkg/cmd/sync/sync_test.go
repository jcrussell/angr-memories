package sync

import (
	"testing"

	"github.com/jcrussell/angr-memories/internal/memory"
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
