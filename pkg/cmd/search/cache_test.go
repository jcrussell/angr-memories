package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blevesearch/bleve/v2"

	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

func sampleByKey() map[string]*memory.Memory {
	return latestByKey([]*memory.Memory{
		{Key: "invariant-solver-eval-timing", Body: "solver details", FirstSeen: "2026-06-01T00:00:00Z"},
	})
}

func TestOpenCachedOrBuildIndex_emptyCacheDirSkipsCaching(t *testing.T) {
	idx, err := openCachedOrBuildIndex("", []byte("anything"), sampleByKey())
	if err != nil {
		t.Fatalf("openCachedOrBuildIndex: %v", err)
	}
	defer idx.Close()
	// No filesystem assertion possible for "" -- this just confirms it
	// doesn't try to use "" as a real path and still returns a working index.
	assertIndexHasDoc(t, idx, "invariant-solver-eval-timing")
}

func TestOpenCachedOrBuildIndex_missThenHit(t *testing.T) {
	cacheDir := t.TempDir()
	content := []byte("memories-v1")
	byKey := sampleByKey()

	idx1, err := openCachedOrBuildIndex(cacheDir, content, byKey)
	if err != nil {
		t.Fatalf("first call (miss, builds): %v", err)
	}
	assertIndexHasDoc(t, idx1, "invariant-solver-eval-timing")
	idx1.Close()

	root := filepath.Join(cacheDir, "search-index-"+cacheSchemaVersion)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("reading cache root: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one cache entry after a fresh build, got %d: %v", len(entries), entries)
	}
	firstHashDir := entries[0].Name()

	idx2, err := openCachedOrBuildIndex(cacheDir, content, byKey)
	if err != nil {
		t.Fatalf("second call (same content, should hit): %v", err)
	}
	defer idx2.Close()
	assertIndexHasDoc(t, idx2, "invariant-solver-eval-timing")

	entries, err = os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != firstHashDir {
		t.Errorf("expected the same single cache entry after a hit, got %v", entries)
	}
}

func TestOpenCachedOrBuildIndex_contentChangeRebuildsAndGCsOldEntry(t *testing.T) {
	cacheDir := t.TempDir()
	byKey := sampleByKey()

	idx1, err := openCachedOrBuildIndex(cacheDir, []byte("version-1"), byKey)
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	idx1.Close()

	root := filepath.Join(cacheDir, "search-index-"+cacheSchemaVersion)
	// The old entry only gets GC'd once it's aged past gcGracePeriod --
	// backdate it here to simulate that, rather than sleeping in a test.
	backdateAll(t, root, gcGracePeriod*2)

	idx2, err := openCachedOrBuildIndex(cacheDir, []byte("version-2"), byKey)
	if err != nil {
		t.Fatalf("second build (different content): %v", err)
	}
	defer idx2.Close()

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected the old content hash's entry to be garbage-collected, got %d entries: %v", len(entries), entries)
	}
}

func TestOpenCachedOrBuildIndex_corruptCacheEntryFallsBackToRebuild(t *testing.T) {
	cacheDir := t.TempDir()
	content := []byte("memories-v1")
	byKey := sampleByKey()

	// Pre-create a bogus (non-index) directory at exactly the path a real
	// build would use, simulating a corrupted or incompatible cache entry.
	root := filepath.Join(cacheDir, "search-index-"+cacheSchemaVersion)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := contentHashHex(content)
	if err := os.MkdirAll(filepath.Join(root, sum), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, sum, "not-a-real-index"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := openCachedOrBuildIndex(cacheDir, content, byKey)
	if err != nil {
		t.Fatalf("expected a corrupt cache entry to be silently rebuilt, got error: %v", err)
	}
	defer idx.Close()
	assertIndexHasDoc(t, idx, "invariant-solver-eval-timing")
}

// bleve.Open succeeding doesn't mean every segment file is intact -- a
// missing/corrupt segment inside an otherwise-openable index needs its own
// liveness check (DocCount) to be caught, per the code's own claim that
// "any Open failure...falls through to a full rebuild" (finding from an
// adversarial concurrency pass: a deleted segment file used to slip past
// this and only surface as a runtime error on the actual query).
func TestOpenCachedOrBuildIndex_missingSegmentFileTriggersRebuild(t *testing.T) {
	cacheDir := t.TempDir()
	content := []byte("memories-v1")
	byKey := sampleByKey()

	idx, err := openCachedOrBuildIndex(cacheDir, content, byKey)
	if err != nil {
		t.Fatalf("initial build: %v", err)
	}
	idx.Close()

	root := filepath.Join(cacheDir, "search-index-"+cacheSchemaVersion)
	target := filepath.Join(root, contentHashHex(content))
	segments, err := filepath.Glob(filepath.Join(target, "store", "*.zap"))
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) == 0 {
		t.Fatalf("expected at least one segment file under %s/store", target)
	}
	if err := os.Remove(segments[0]); err != nil {
		t.Fatal(err)
	}

	idx2, err := openCachedOrBuildIndex(cacheDir, content, byKey)
	if err != nil {
		t.Fatalf("expected a missing segment to be silently rebuilt, got error: %v", err)
	}
	defer idx2.Close()
	assertIndexHasDoc(t, idx2, "invariant-solver-eval-timing")
}

func TestBuildAndInstallIndex_targetAlreadyBuiltByAnotherRunIsReused(t *testing.T) {
	root := t.TempDir()
	byKey := sampleByKey()
	target := filepath.Join(root, "some-hash")

	// Simulate a concurrent invocation that already finished installing a
	// valid index at target before we get there.
	if err := buildDiskIndex(target, byKey); err != nil {
		t.Fatalf("buildDiskIndex (setup): %v", err)
	}

	idx, err := buildAndInstallIndex(root, target, byKey)
	if err != nil {
		t.Fatalf("buildAndInstallIndex should fall back to the existing target, got error: %v", err)
	}
	defer idx.Close()
	assertIndexHasDoc(t, idx, "invariant-solver-eval-timing")
}

func TestGCStaleEntries_removesEverythingExceptKeep(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"keep-me", "stale-1", "stale-2"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	backdateAll(t, root, gcGracePeriod*2)
	gcStaleEntries(root, "keep-me")

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "keep-me" {
		t.Errorf("expected only 'keep-me' to survive, got %v", entries)
	}
}

// A stale entry that's younger than the grace period must survive GC --
// another process may still have it open (e.g. a concurrent `search`
// reading a just-evicted cache entry, or an in-progress .tmp-* build).
// Deleting it out from under that reader is exactly the race this grace
// period exists to bound (finding from an adversarial concurrency pass).
func TestGCStaleEntries_recentEntriesSurviveTheGracePeriod(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"stale-hash", tmpDirPrefix + "abc123"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	gcStaleEntries(root, "current-hash")

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("expected both recently-created entries to survive GC, got %v", entries)
	}
}

// Once an entry (tmp or not) ages past the grace period, GC reclaims it --
// this is what prevents an abandoned .tmp-* build directory from a
// crashed/killed process from leaking forever.
func TestGCStaleEntries_removesEntriesOlderThanTheGracePeriod(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"stale-hash", tmpDirPrefix + "abandoned"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	backdateAll(t, root, gcGracePeriod*2)

	gcStaleEntries(root, "current-hash")

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected both aged-out entries to be removed, got %v", entries)
	}
}

func backdateAll(t *testing.T, root string, age time.Duration) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-age)
	for _, e := range entries {
		if err := os.Chtimes(filepath.Join(root, e.Name()), old, old); err != nil {
			t.Fatal(err)
		}
	}
}

// buildAndInstallIndex can fail for reasons that have nothing to do with a
// stale cache entry -- e.g. the cache root can't be created at all. That
// must degrade to the in-memory index, not fail the whole search (finding
// from peer review: the original version propagated this as a hard error,
// contradicting its own "a bad cache entry is never fatal" comment).
func TestSearchRun_cacheBuildFailureFallsBackGracefully(t *testing.T) {
	memPath := writeMemories(t, []*memory.Memory{
		{Key: "invariant-solver-eval-timing", Body: "solver details", FirstSeen: "2026-06-01T00:00:00Z"},
	})

	// A regular file where the cache dir should be: os.MkdirAll(root, ...)
	// fails because a path component already exists as a non-directory.
	cacheDirParent := t.TempDir()
	blockedCacheDir := filepath.Join(cacheDirParent, "not-a-directory")
	if err := os.WriteFile(blockedCacheDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	io, _, out, _ := iostreams.Test()
	opts := &Options{
		IO:           io,
		CacheDir:     func() (string, error) { return blockedCacheDir, nil },
		MemoriesPath: memPath, Query: "solver", Limit: 20,
	}
	if err := searchRun(opts); err != nil {
		t.Fatalf("expected a cache build failure to fall back to in-memory, got error: %v", err)
	}
	if !strings.Contains(out.String(), "invariant-solver-eval-timing") {
		t.Fatalf("expected a match despite the cache being unusable, got:\n%s", out.String())
	}
}

func TestSearchRun_withCacheDirProducesSameResultsAsWithout(t *testing.T) {
	memPath := writeMemories(t, []*memory.Memory{
		{Key: "invariant-solver-eval-timing", Body: "The solver evaluates constraints lazily.", FirstSeen: "2026-06-01T00:00:00Z"},
	})
	cacheDir := t.TempDir()
	cacheDirFn := func() (string, error) { return cacheDir, nil }

	io, _, out, _ := iostreams.Test()
	opts := &Options{IO: io, CacheDir: cacheDirFn, MemoriesPath: memPath, Query: "solver constraints", Limit: 20}
	if err := searchRun(opts); err != nil {
		t.Fatalf("searchRun (cache miss): %v", err)
	}
	if !strings.Contains(out.String(), "invariant-solver-eval-timing") {
		t.Fatalf("expected a match on first (cache-miss) run, got:\n%s", out.String())
	}

	io2, _, out2, _ := iostreams.Test()
	opts2 := &Options{IO: io2, CacheDir: cacheDirFn, MemoriesPath: memPath, Query: "solver constraints", Limit: 20}
	if err := searchRun(opts2); err != nil {
		t.Fatalf("searchRun (cache hit): %v", err)
	}
	if !strings.Contains(out2.String(), "invariant-solver-eval-timing") {
		t.Fatalf("expected the same match on second (cache-hit) run, got:\n%s", out2.String())
	}
}

func TestSearchRun_noCacheNeverTouchesCacheDir(t *testing.T) {
	memPath := writeMemories(t, []*memory.Memory{
		{Key: "invariant-solver-eval-timing", Body: "solver details", FirstSeen: "2026-06-01T00:00:00Z"},
	})
	cacheDir := t.TempDir()

	io, _, out, _ := iostreams.Test()
	opts := &Options{
		IO: io, CacheDir: func() (string, error) { return cacheDir, nil },
		MemoriesPath: memPath, Query: "solver", Limit: 20, NoCache: true,
	}
	if err := searchRun(opts); err != nil {
		t.Fatalf("searchRun: %v", err)
	}
	if !strings.Contains(out.String(), "invariant-solver-eval-timing") {
		t.Fatalf("expected a match even with --no-cache, got:\n%s", out.String())
	}

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected --no-cache to leave the cache dir untouched, got %v", entries)
	}
}

func TestSearchRun_cacheDirErrorFallsBackGracefully(t *testing.T) {
	memPath := writeMemories(t, []*memory.Memory{
		{Key: "invariant-solver-eval-timing", Body: "solver details", FirstSeen: "2026-06-01T00:00:00Z"},
	})

	io, _, out, _ := iostreams.Test()
	opts := &Options{
		IO:           io,
		CacheDir:     func() (string, error) { return "", errCacheUnavailableForTest },
		MemoriesPath: memPath, Query: "solver", Limit: 20,
	}
	if err := searchRun(opts); err != nil {
		t.Fatalf("searchRun should fall back to in-memory when CacheDir errors, got: %v", err)
	}
	if !strings.Contains(out.String(), "invariant-solver-eval-timing") {
		t.Fatalf("expected a match despite the cache dir being unavailable, got:\n%s", out.String())
	}
}

var errCacheUnavailableForTest = &fakeErr{"cache dir unavailable"}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }

func assertIndexHasDoc(t *testing.T, idx bleve.Index, key string) {
	t.Helper()
	doc, err := idx.Document(key)
	if err != nil {
		t.Fatalf("Document(%q): %v", key, err)
	}
	if doc == nil {
		t.Fatalf("expected %q to be present in the index", key)
	}
}
