// Package atomicfile writes whole files atomically: a temp file in the
// target's own directory, fully written and fsynced, then renamed over the
// target, then the parent directory fsynced. A crash or kill mid-write can
// never leave the target truncated or half-written: either the rename
// happened and the new content is fully there, or it didn't and the old
// file is untouched. (byob-runtime-directories.3; same-directory temp
// because cross-directory rename is not atomic on POSIX.)
//
// memory.Save and the embeddings / topic-labels writers all go through
// WriteFile so the recipe lives in one place.
package atomicfile

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// WriteFile writes data to path atomically with the given permissions.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	return WriteFunc(path, perm, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// WriteFunc is WriteFile for callers that stream their payload: fn writes
// the full content to w; the file is only renamed into place if fn and
// every subsequent fsync/close succeed.
func WriteFunc(path string, perm os.FileMode, fn func(w io.Writer) error) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	// Register for cleanup on SIGINT/SIGTERM: Go runs no deferred cleanup
	// on an unhandled signal, so a long write killed mid-flight would
	// otherwise leave the temp file behind forever.
	TrackPending(tmpPath)
	defer func() {
		UntrackPending(tmpPath)
		if err != nil {
			os.Remove(tmpPath)
		}
	}()

	if err = fn(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	if err = os.Rename(tmpPath, path); err != nil {
		return err
	}
	// fsync the parent directory so the rename itself is durable across a
	// kernel crash. POSIX-only: a directory-handle Sync is not the same
	// barrier on Windows and some filesystems reject it there.
	if runtime.GOOS != "windows" {
		d, derr := os.Open(dir)
		if derr != nil {
			return derr
		}
		defer d.Close()
		if derr := d.Sync(); derr != nil {
			return derr
		}
	}
	return nil
}
