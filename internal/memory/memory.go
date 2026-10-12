// Package memory is the canonical model for angr-memories: one Memory per
// line of memories.jsonl, plus the prefix-family grouping and live-diff
// logic shared by the site and sync commands.
package memory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/jcrussell/angr-memories/internal/atomicfile"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// Memory is one record: a memory key's lifetime from first appearance to
// (if applicable) the point it was forgotten. A key can have more than one
// Memory if it was forgotten and later recreated -- see the "incarnation"
// discussion in the migration plan. There is no stored incarnation number;
// callers group by Key and sort by FirstSeen to recover the order.
type Memory struct {
	Key        string `json:"key"`
	Body       string `json:"body"`
	FirstSeen  string `json:"first_seen"`
	UpdatedAt  string `json:"updated_at"`
	DecidedAt  string `json:"decided_at"`
	Reason     string `json:"reason"`
	MergedInto string `json:"merged_into"`
}

// Remembered reports whether this record is still open (never forgotten).
func (m *Memory) Remembered() bool { return m.DecidedAt == "" }

// Load reads a memories.jsonl file, one Memory per line.
func Load(path string) ([]*Memory, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := validateAtMostOneRememberedPerKey(out); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}

// validateAtMostOneRememberedPerKey rejects a data-integrity violation: two
// records for the same key that are both currently remembered (DecidedAt
// == "") at once. The invariant this protects (documented at
// internal/prune.BuildGraph and relied on there) is that a key has at most
// one open incarnation at a time -- an older incarnation must be forgotten
// (DecidedAt set) before a newer one can exist. Two simultaneously-open
// records for one key would otherwise be silently double-counted by
// Aggregate and mislabeled "recreated" even though no forget ever happened.
// Only checked here, not in Parse -- a caller reading raw bytes for another
// purpose (e.g. hashing them as a search cache key) shouldn't gain a new
// failure mode from a check it doesn't need.
func validateAtMostOneRememberedPerKey(records []*Memory) error {
	seen := map[string]bool{}
	for _, m := range records {
		if !m.Remembered() {
			continue
		}
		if seen[m.Key] {
			return fmt.Errorf("key %q has more than one currently-remembered record", m.Key)
		}
		seen[m.Key] = true
	}
	return nil
}

// Parse reads Memory records from r, one JSON object per line -- the same
// format Load reads from a path. Factored out so a caller that needs the
// raw bytes for another purpose (e.g. hashing them as a cache key) can
// parse from that exact byte slice instead of re-opening the path for a
// second, independent read: two separate reads of the same path aren't
// atomic with each other even though each one individually is safe (Save
// writes via temp-file + rename), so a concurrent writer landing between
// them could make the two reads see different content.
func Parse(r io.Reader) ([]*Memory, error) {
	var out []*Memory
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var m Memory
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Save writes records to path, sorted by key then first_seen -- this is
// what makes incarnation order reproducible without a stored counter.
//
// Writes to a temp file in the same directory and renames over path, so a
// crash or kill mid-write (OOM, cron timeout, SIGTERM) can never leave
// memories.jsonl truncated or half-written: either the rename happens and
// the new content is fully there, or it doesn't and the old file is
// untouched. os.Create-then-truncate-in-place would destroy the previous
// content the instant it's called, before a single new byte is written.
func Save(path string, records []*Memory) (err error) {
	sorted := append([]*Memory(nil), records...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Key != sorted[j].Key {
			return sorted[i].Key < sorted[j].Key
		}
		return sorted[i].FirstSeen < sorted[j].FirstSeen
	})

	return atomicfile.WriteFunc(path, 0o644, func(w io.Writer) error {
		bw := bufio.NewWriter(w)
		enc := json.NewEncoder(bw)
		enc.SetEscapeHTML(false)
		for _, m := range sorted {
			if err := enc.Encode(m); err != nil {
				return err
			}
		}
		return bw.Flush()
	})
}

// TrackPending, UntrackPending and CleanupPending live in
// internal/atomicfile (which owns the temp-file recipe); these aliases keep
// existing call sites compiling.
var (
	TrackPending   = atomicfile.TrackPending
	UntrackPending = atomicfile.UntrackPending
	CleanupPending = atomicfile.CleanupPending
)

// families is the key-prefix grouping validated by the historical
// tools/bd_chunk_for_review.py -- ported verbatim.
var families = map[string]string{
	"invariant": "invariant",
	"avoid":     "avoid",
	"z3":        "z3",
	"rust":      "rust",
	"benchmark": "benchmark",
	"bench":     "benchmark",
	"bd":        "ops",
	"ralph":     "ops",
	"dolt":      "ops",
	"git":       "ops",
	"gh":        "ops",
	"ci":        "ops",
	"github":    "ops",
	"claude":    "ops",
	"v5a5":      "v5a5",
	"inspect":   "inspect",
	"syscall":   "syscall",
}

// Latest picks the most recent incarnation of every key: the record whose
// body the site shows and whose vector the clustering uses.
func Latest(records []*Memory) map[string]*Memory {
	latest := map[string]*Memory{}
	for _, m := range records {
		if cur, ok := latest[m.Key]; !ok || m.FirstSeen > cur.FirstSeen {
			latest[m.Key] = m
		}
	}
	return latest
}

// PrefixOf derives a memory's family bucket from its key. Never stored --
// always computed on read, so it can't drift from key.
func PrefixOf(key string) string {
	prefix, _, _ := strings.Cut(key, "-")
	if fam, ok := families[prefix]; ok {
		return fam
	}
	return "catch"
}

// AlphaSplit splits sorted keys into alpha-ordered chunks of at most
// chunkCap entries, ported from bd_chunk_for_review.py's _alpha_split. Used
// to paginate large prefix families (catch, invariant) instead of
// rendering one unwieldy page.
func AlphaSplit(keys []string, chunkCap int) [][]string {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	if len(sorted) <= chunkCap {
		return [][]string{sorted}
	}
	nChunks := (len(sorted) + chunkCap - 1) / chunkCap
	base := len(sorted) / nChunks
	rem := len(sorted) % nChunks
	chunks := make([][]string, 0, nChunks)
	idx := 0
	for i := 0; i < nChunks; i++ {
		size := base
		if i < rem {
			size++
		}
		chunks = append(chunks, sorted[idx:idx+size])
		idx += size
	}
	return chunks
}

// Diff compares a live {key: body} snapshot (from `bd memories --json`)
// against the currently-open records, at the given sync timestamp.
//   - opened: keys live but with no open record -- new records to append.
//   - closed: open records whose key vanished from the live snapshot --
//     the same records, mutated in place with DecidedAt set.
//   - edited: open records whose key is still live but whose body changed --
//     the same records, mutated in place with Body updated.
//
// Recreation needs no special handling here: a previously closed key has
// no open record, so if it reappears live it simply falls into `opened`,
// identical to a never-before-seen key.
func Diff(open []*Memory, live map[string]string, at time.Time) (opened, closed, edited []*Memory) {
	ts := at.UTC().Format(time.RFC3339)

	byKey := make(map[string]*Memory, len(open))
	for _, m := range open {
		byKey[m.Key] = m
	}

	for key, body := range live {
		if _, ok := byKey[key]; !ok {
			opened = append(opened, &Memory{
				Key:       key,
				Body:      body,
				FirstSeen: ts,
			})
		}
	}

	for _, m := range open {
		body, stillLive := live[m.Key]
		switch {
		case !stillLive:
			m.DecidedAt = ts
			// Not a guess at *why* the key vanished (unknowable from a
			// live diff) -- just a breadcrumb for *how* this closure was
			// detected, so it's distinguishable from a curated decision
			// or, worse, a bogus mass-close from a bad live snapshot.
			m.Reason = "sync: absent from live snapshot"
			closed = append(closed, m)
		case body != m.Body:
			m.Body = body
			m.UpdatedAt = ts
			edited = append(edited, m)
		}
	}

	return opened, closed, edited
}
