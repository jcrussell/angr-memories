package search

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blevesearch/bleve/v2"

	"github.com/jcrussell/angr-memories/internal/memory"
)

// tmpDirPrefix marks an in-progress build's temp directory under the cache
// root -- gcStaleEntries must never remove one of these, even mid-build.
const tmpDirPrefix = ".tmp-"

// cacheSchemaVersion namespaces the on-disk cache. Bump it whenever
// indexedDoc's shape (or the bleve version) changes in a way that could
// make an old cached index unreadable or silently wrong. This is a pure
// cache -- rebuildable from memories.jsonl at any time -- so per
// byob-runtime-directories.4 there's no read-time migration path, just a
// fresh namespace; gcStaleEntries removes what's left under the old one.
const cacheSchemaVersion = "v1"

// openCachedOrBuildIndex returns a bleve index over byKey, backed by an
// on-disk cache under cacheDir keyed by a content hash of memories.jsonl's
// raw bytes. A hash match opens the existing index with no reindexing; any
// miss (new content, missing/corrupt cache, first run) builds fresh and
// installs it for next time. cacheDir == "" skips caching entirely and
// always builds in-memory, matching the original always-rebuild behavior.
func openCachedOrBuildIndex(cacheDir string, fileBytes []byte, byKey map[string]*memory.Memory) (bleve.Index, error) {
	if cacheDir == "" {
		return buildMemIndex(byKey)
	}

	root := filepath.Join(cacheDir, "search-index-"+cacheSchemaVersion)
	target := filepath.Join(root, contentHashHex(fileBytes))

	if idx, err := bleve.Open(target); err == nil {
		return idx, nil
	}
	// Any Open failure (not found, corrupt, incompatible bleve version)
	// falls through to a full rebuild. A bad cache entry is never fatal --
	// worst case this run pays the build cost the cache exists to avoid.

	idx, err := buildAndInstallIndex(root, target, byKey)
	if err != nil {
		return nil, err
	}
	gcStaleEntries(root, filepath.Base(target))
	return idx, nil
}

// contentHashHex is the cache key for a given memories.jsonl content.
func contentHashHex(fileBytes []byte) string {
	sum := sha256.Sum256(fileBytes)
	return hex.EncodeToString(sum[:])
}

// buildAndInstallIndex builds a fresh on-disk index at a temp path under
// root, then atomically renames it into place at target -- same
// temp-in-same-dir + rename discipline as memory.Save, extended to a whole
// directory (bleve's on-disk index is many files, not one). The index is
// closed and reopened from target rather than reused post-rename: renaming
// out from under open file descriptors is safe on POSIX in practice, but
// reopening is cheap and doesn't require trusting that across platforms
// and filesystems.
func buildAndInstallIndex(root, target string, byKey map[string]*memory.Memory) (bleve.Index, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create search cache dir: %w", err)
	}
	tmp, err := os.MkdirTemp(root, tmpDirPrefix+"*")
	if err != nil {
		return nil, fmt.Errorf("create search cache temp dir: %w", err)
	}
	// os.MkdirTemp creates the dir; bleve.New refuses to create an index at
	// a path that already exists, so remove it and let bleve create it
	// fresh at the same name.
	tmpIndexPath := filepath.Join(tmp, "index")

	if err := buildDiskIndex(tmpIndexPath, byKey); err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}

	// Install with one retry. A rename conflict (target already exists)
	// has two different causes that need opposite responses, and either
	// one can happen on *either* attempt -- a concurrent build can finish
	// and win the race at any point, not just before our first try:
	//   - a concurrent `search` invocation finished building the same
	//     content hash first -- their index is just as valid as ours, so
	//     open and reuse it instead of discarding a perfectly good result;
	//   - a stale/corrupt entry is sitting there (this is exactly the case
	//     openCachedOrBuildIndex's own bleve.Open miss couldn't rule out:
	//     Open failing on target only means it's unreadable *now*, not
	//     that the directory was ever removed) -- clear it and retry,
	//     since nothing valid is there to lose.
	// Both checks run identically on both attempts; only the "clear and
	// retry" action is limited to once, so a permanently broken target
	// (e.g. unremovable) fails after two tries instead of looping forever.
	for attempt := 0; ; attempt++ {
		renameErr := os.Rename(tmpIndexPath, target)
		if renameErr == nil {
			break
		}
		if idx, openErr := bleve.Open(target); openErr == nil {
			os.RemoveAll(tmp)
			return idx, nil
		}
		if attempt > 0 {
			os.RemoveAll(tmp)
			return nil, fmt.Errorf("install search cache: %w", renameErr)
		}
		if rmErr := os.RemoveAll(target); rmErr != nil {
			os.RemoveAll(tmp)
			return nil, fmt.Errorf("install search cache: replacing broken cache entry: %w", rmErr)
		}
	}
	os.RemoveAll(tmp) // the "index" subdir moved out; drop the now-empty temp parent

	idx, err := bleve.Open(target)
	if err != nil {
		return nil, fmt.Errorf("open freshly built search cache: %w", err)
	}
	return idx, nil
}

// gcStaleEntries removes every entry under root except keep (the content
// hash just installed) -- otherwise every distinct memories.jsonl content
// (every sync/apply run) leaves its old index behind forever. This is a
// single-slot cache, not an LRU: alternating between two different
// memories.jsonl contents against the same cache root evicts on every
// switch. Entries named with tmpDirPrefix are always skipped, never just
// best-effort-ignored-on-error -- one is a concurrent invocation's
// in-progress build (buildAndInstallIndex builds into one before renaming
// it away), and deleting it out from under that build would turn its own
// install rename into a hard failure. Removal of any other stale entry is
// best-effort: an error there just means it lingers until the next GC.
func gcStaleEntries(root, keep string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if name == keep || strings.HasPrefix(name, tmpDirPrefix) {
			continue
		}
		os.RemoveAll(filepath.Join(root, name))
	}
}
