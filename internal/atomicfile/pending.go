package atomicfile

import (
	"os"
	"sync"
)

var (
	pendingMu    sync.Mutex
	pendingPaths = map[string]bool{}
)

// TrackPending registers a temp file or directory that must be removed if
// the process is killed before it can clean up after itself normally.
// Go doesn't run deferred cleanup on an unhandled SIGINT/SIGTERM, so a long
// write killed mid-flight would otherwise leave the temp file behind
// forever. Call UntrackPending once path is either renamed into place or
// removed through the caller's own normal cleanup path.
func TrackPending(path string) {
	pendingMu.Lock()
	pendingPaths[path] = true
	pendingMu.Unlock()
}

// UntrackPending reverses TrackPending.
func UntrackPending(path string) {
	pendingMu.Lock()
	delete(pendingPaths, path)
	pendingMu.Unlock()
}

// CleanupPending removes every currently-tracked pending path and clears
// the registry. Meant to be called from main right before the process
// exits on a signal.
func CleanupPending() {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	for p := range pendingPaths {
		os.RemoveAll(p)
		delete(pendingPaths, p)
	}
}

// PendingCount reports how many paths are currently tracked. Exposed for
// tests that assert a successful write leaves nothing registered.
func PendingCount() int {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	return len(pendingPaths)
}
