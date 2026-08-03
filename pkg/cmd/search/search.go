// Package search implements `angr-memories search`: a bleve-backed ranked
// search over memories.jsonl. The corpus (~2,400 records) is small enough
// that bleve's speed advantage over a linear scan doesn't matter -- what it
// buys is relevance ranking and fuzzy/typo-tolerant matching, which catch
// near-duplicate or paraphrased memories a literal substring match can't.
// Directly useful during a pruning review: "have I already written
// something like this before deciding to keep/forget/merge it."
package search

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search"
	"github.com/blevesearch/bleve/v2/search/highlight/highlighter/ansi"
	"github.com/spf13/cobra"

	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

type Options struct {
	IO           *iostreams.IOStreams
	CacheDir     func() (string, error)
	MemoriesPath string
	Query        string
	Prefix       string
	All          bool
	Fuzzy        bool
	NoCache      bool
	Limit        int
}

func (o *Options) Validate() error {
	if o.Limit <= 0 {
		return cmdutil.FlagErrorf("--limit must be > 0, got %d", o.Limit)
	}
	return nil
}

func NewCmdSearch(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{IO: f.IOStreams, CacheDir: f.CacheDir}
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search memories, ranked by relevance",
		Long: "Searches memories.jsonl with a bleve index, ranked by relevance. The " +
			"index is cached under the OS cache directory ($XDG_CACHE_HOME/angr-memories " +
			"on Linux), keyed by a content hash of memories.jsonl -- a cache hit reuses " +
			"the index with no reindexing; any change to memories.jsonl (sync, apply) " +
			"produces a new hash and rebuilds automatically on the next search. Stale " +
			"entries from prior content are garbage-collected after each rebuild. " +
			"--no-cache skips the cache entirely (always rebuilds in-memory), useful for " +
			"one-off runs against a scratch memories.jsonl you don't want polluting the " +
			"cache. <query> is bleve's query-string syntax by default (supports " +
			"phrase/boolean operators); --fuzzy switches to edit-distance matching " +
			"instead, for typo-tolerant or near-duplicate lookups. Remembered records " +
			"only by default; --all includes forgotten ones.",
		Example: `  angr-memories search "constraint solver"
  angr-memories search --fuzzy solvr
  angr-memories search --memories m.jsonl -- -exclude-this-term`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			opts.Query = args[0]
			if err := opts.Validate(); err != nil {
				return err
			}
			if runF != nil {
				return runF(opts)
			}
			return searchRun(opts)
		},
	}
	cmd.Flags().StringVar(&opts.MemoriesPath, "memories", "memories.jsonl", "path to memories.jsonl")
	cmd.Flags().StringVar(&opts.Prefix, "prefix", "", "restrict to one key-prefix family")
	cmd.Flags().BoolVar(&opts.All, "all", false, "include forgotten memories (default: remembered only)")
	cmd.Flags().BoolVar(&opts.Fuzzy, "fuzzy", false, "fuzzy (edit-distance) match instead of query-string syntax")
	cmd.Flags().BoolVar(&opts.NoCache, "no-cache", false, "skip the on-disk index cache; always rebuild in-memory")
	cmd.Flags().IntVar(&opts.Limit, "limit", 20, "maximum number of results")
	return cmd
}

// indexedDoc is what gets handed to bleve.Index() per key.
type indexedDoc struct {
	Key    string `json:"key"`
	Body   string `json:"body"`
	Prefix string `json:"prefix"`
}

func searchRun(opts *Options) error {
	// One read of memories.jsonl, parsed via memory.Parse from the exact
	// bytes also used for the cache-key hash -- not memory.Load (a second,
	// independent open+read of the same path). Two separate reads aren't
	// atomic with each other even though Save's temp-file+rename makes
	// each one individually safe: a concurrent sync/apply landing between
	// them could make the parsed records reflect one version of the file
	// while the hash reflects another, silently caching an index built
	// from stale content under the new content's hash.
	raw, err := os.ReadFile(opts.MemoriesPath)
	if err != nil {
		return err
	}
	records, err := memory.Parse(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("%s: %w", opts.MemoriesPath, err)
	}
	byKey := latestByKey(records)

	idx, err := buildOrOpenIndex(opts, raw, byKey)
	if err != nil {
		return err
	}
	defer idx.Close()

	var req *bleve.SearchRequest
	if opts.Fuzzy {
		fq := bleve.NewFuzzyQuery(opts.Query)
		fq.SetField("body")
		req = bleve.NewSearchRequest(fq)
	} else {
		req = bleve.NewSearchRequest(bleve.NewQueryStringQuery(opts.Query))
	}
	// Over-fetch: --prefix/--all filter after scoring, so ask bleve for
	// more than --limit in case some hits get filtered out below.
	req.Size = opts.Limit * 4
	// ansi, not the default html highlighter: this is terminal output, and
	// bleve's html style wraps matches in <mark> tags that would otherwise
	// print literally.
	req.Highlight = bleve.NewHighlightWithStyle(ansi.Name)

	res, err := idx.Search(req)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	// bleve doesn't guarantee a stable tie-break for equal scores, and the
	// on-disk vs. in-memory backends order ties differently -- sort by key
	// as a deterministic secondary key so --no-cache and cached runs (and
	// repeated runs generally) produce identical output.
	sort.SliceStable(res.Hits, func(i, j int) bool {
		if res.Hits[i].Score != res.Hits[j].Score {
			return res.Hits[i].Score > res.Hits[j].Score
		}
		return res.Hits[i].ID < res.Hits[j].ID
	})

	out := opts.IO.Out
	shown := 0
	for _, hit := range res.Hits {
		if shown >= opts.Limit {
			break
		}
		rec := byKey[hit.ID]
		if rec == nil {
			continue
		}
		if !opts.All && !rec.Remembered() {
			continue
		}
		prefix := memory.PrefixOf(rec.Key)
		if opts.Prefix != "" && prefix != opts.Prefix {
			continue
		}

		status := "remembered"
		if !rec.Remembered() {
			status = "forgotten"
		}

		fmt.Fprintf(out, "%-40s %6.2f  %-10s %-10s %s\n", hit.ID, hit.Score, prefix, status, snippetFrom(hit))
		shown++
	}
	if shown == 0 {
		fmt.Fprintln(out, "no matches")
	}
	return nil
}

func snippetFrom(hit *search.DocumentMatch) string {
	if frags := hit.Fragments["body"]; len(frags) > 0 {
		return strings.TrimSpace(frags[0])
	}
	return ""
}

// latestByKey collapses records to one entry per key. Last-write-wins, same
// invariant internal/prune.Apply relies on: memory.Load reads a file
// memory.Save last wrote, which sorts by (key, first_seen) ascending, so
// the last incarnation in file order is the current one.
func latestByKey(records []*memory.Memory) map[string]*memory.Memory {
	byKey := make(map[string]*memory.Memory, len(records))
	for _, m := range records {
		byKey[m.Key] = m
	}
	return byKey
}

// buildOrOpenIndex routes to the on-disk cache (cache.go) unless
// --no-cache was passed, the Factory couldn't resolve a cache directory
// (e.g. os.UserCacheDir() failing on an unusual platform/environment), or
// the cache itself fails for any reason (disk full, permissions, a lost
// install race) -- in every case, falling back to an in-memory,
// always-rebuild index is always correct, just slower. A cache is only
// worth having if it can never turn a working search into a failing one.
func buildOrOpenIndex(opts *Options, fileBytes []byte, byKey map[string]*memory.Memory) (bleve.Index, error) {
	if opts.NoCache || opts.CacheDir == nil {
		return buildMemIndex(byKey)
	}
	dir, err := opts.CacheDir()
	if err != nil || dir == "" {
		return buildMemIndex(byKey)
	}

	idx, err := openCachedOrBuildIndex(dir, fileBytes, byKey)
	if err != nil {
		return buildMemIndex(byKey)
	}
	return idx, nil
}

// indexMapping is shared by the in-memory and on-disk index paths.
// bleve.NewIndexMapping()'s own default analyzer ("standard": tokenize +
// lowercase, no stemming) is used deliberately rather than overriding to
// the stemming "en" analyzer -- fuzzy queries compare the raw query term
// against already-analyzed indexed terms, so a stemming analyzer (e.g.
// "concretization" -> "concret") blows up the edit distance for exactly
// the typo-tolerant lookups --fuzzy exists for.
func indexMapping() mapping.IndexMapping {
	return bleve.NewIndexMapping()
}

// buildMemIndex builds a bleve index entirely in memory -- used when
// caching is off or unavailable.
func buildMemIndex(byKey map[string]*memory.Memory) (bleve.Index, error) {
	idx, err := bleve.NewMemOnly(indexMapping())
	if err != nil {
		return nil, fmt.Errorf("build search index: %w", err)
	}
	if err := indexBatch(idx, byKey); err != nil {
		return nil, err
	}
	return idx, nil
}

// buildDiskIndex builds a bleve index on disk at path, closing it before
// returning -- callers that want an open handle reopen with bleve.Open
// (cache.go does this after the atomic rename into the cache's final
// location, so the returned handle is never one that just got renamed out
// from under it).
func buildDiskIndex(path string, byKey map[string]*memory.Memory) error {
	idx, err := bleve.New(path, indexMapping())
	if err != nil {
		return fmt.Errorf("build search index: %w", err)
	}
	defer idx.Close()
	return indexBatch(idx, byKey)
}

// indexBatch indexes every key via one idx.Batch() call, not one
// idx.Index() call per document: bleve's scorch backend does a segment
// flush/merge per individual Index() call, which measured ~3.5s indexing
// ~1,300 real remembered records one at a time. Batching cuts that to
// ~1.8s -- still not sub-second, which is the whole reason this package
// also caches the built index on disk (cache.go) rather than relying on
// batching alone.
func indexBatch(idx bleve.Index, byKey map[string]*memory.Memory) error {
	batch := idx.NewBatch()
	for key, rec := range byKey {
		doc := indexedDoc{Key: rec.Key, Body: rec.Body, Prefix: memory.PrefixOf(rec.Key)}
		if err := batch.Index(key, doc); err != nil {
			return fmt.Errorf("indexing %s: %w", key, err)
		}
	}
	if err := idx.Batch(batch); err != nil {
		return fmt.Errorf("build search index: %w", err)
	}
	return nil
}
