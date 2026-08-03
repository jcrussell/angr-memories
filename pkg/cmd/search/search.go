// Package search implements `angr-memories search`: a bleve-backed ranked
// search over memories.jsonl. The corpus (~2,400 records) is small enough
// that bleve's speed advantage over a linear scan doesn't matter -- what it
// buys is relevance ranking and fuzzy/typo-tolerant matching, which catch
// near-duplicate or paraphrased memories a literal substring match can't.
// Directly useful during a pruning review: "have I already written
// something like this before deciding to keep/forget/merge it."
package search

import (
	"fmt"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search"
	"github.com/blevesearch/bleve/v2/search/highlight/highlighter/ansi"
	"github.com/spf13/cobra"

	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

type Options struct {
	IO           *iostreams.IOStreams
	MemoriesPath string
	Query        string
	Prefix       string
	All          bool
	Fuzzy        bool
	Limit        int
}

func (o *Options) Validate() error {
	if o.Limit <= 0 {
		return cmdutil.FlagErrorf("--limit must be > 0, got %d", o.Limit)
	}
	return nil
}

func NewCmdSearch(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{IO: f.IOStreams}
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search memories, ranked by relevance",
		Long: "Builds a fresh in-memory bleve index from memories.jsonl on every " +
			"invocation and searches it -- no persisted index directory, so nothing can " +
			"go stale after sync/apply mutate the archive (a couple of seconds on the " +
			"full corpus, which is the price of that simplicity). <query> is bleve's " +
			"query-string syntax by default (supports phrase/boolean operators); --fuzzy " +
			"switches to edit-distance matching instead, for typo-tolerant or " +
			"near-duplicate lookups. Remembered records only by default; --all includes " +
			"forgotten ones.",
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
	records, err := memory.Load(opts.MemoriesPath)
	if err != nil {
		return err
	}

	idx, byKey, err := buildIndex(records)
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

// buildIndex indexes every record's latest incarnation into a fresh
// in-memory bleve index, returning the index plus a key->Memory lookup
// (used to apply the remembered/prefix filters and print status after
// scoring, since those aren't part of the query itself).
func buildIndex(records []*memory.Memory) (bleve.Index, map[string]*memory.Memory, error) {
	// bleve.NewIndexMapping()'s own default analyzer ("standard": tokenize +
	// lowercase, no stemming) is used deliberately rather than overriding to
	// the stemming "en" analyzer -- fuzzy queries compare the raw query term
	// against already-analyzed indexed terms, so a stemming analyzer (e.g.
	// "concretization" -> "concret") blows up the edit distance for exactly
	// the typo-tolerant lookups --fuzzy exists for.
	idx, err := bleve.NewMemOnly(bleve.NewIndexMapping())
	if err != nil {
		return nil, nil, fmt.Errorf("build search index: %w", err)
	}

	// Last-write-wins per key, same invariant internal/prune.Apply relies
	// on: memory.Load reads a file memory.Save last wrote, which sorts by
	// (key, first_seen) ascending, so the last incarnation in file order is
	// the current one.
	byKey := make(map[string]*memory.Memory, len(records))
	for _, m := range records {
		byKey[m.Key] = m
	}

	// One idx.Batch() call, not one idx.Index() call per document: bleve's
	// scorch backend does a segment flush/merge per individual Index() call,
	// which measured ~3.5s indexing ~1,300 real remembered records one at a
	// time. Batching cuts that to ~1.8s -- still not sub-second, but this is
	// the whole cost of "always rebuild, never a stale index file."
	batch := idx.NewBatch()
	for key, rec := range byKey {
		doc := indexedDoc{Key: rec.Key, Body: rec.Body, Prefix: memory.PrefixOf(rec.Key)}
		if err := batch.Index(key, doc); err != nil {
			return nil, nil, fmt.Errorf("indexing %s: %w", key, err)
		}
	}
	if err := idx.Batch(batch); err != nil {
		return nil, nil, fmt.Errorf("build search index: %w", err)
	}
	return idx, byKey, nil
}
