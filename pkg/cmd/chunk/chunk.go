// Package chunk implements `angr-memories chunk`: bucket remembered
// memories into per-round review batches for agent review, porting the
// bucketing the deleted tools/bd_chunk_for_review.py used (memory.PrefixOf
// and memory.AlphaSplit are already that tool's family table and
// alpha-split algorithm, shared here with the site's own pagination).
package chunk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/internal/prune"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

// defaultChunkCap matches bd_chunk_for_review.py's default -- calibrated
// for agent-review cognitive load per batch, not webpage size (contrast
// internal/site's much larger defaultChunkCap, tuned for page size instead).
const defaultChunkCap = 70

type Options struct {
	IO           *iostreams.IOStreams
	MemoriesPath string
	RoundsDir    string
	Round        string
	ChunkCap     int
	DeltaSince   string
}

// Validate runs before any file gets written, per the project's
// boundary-validation convention: reject bad flag shapes before side
// effects, not partway through writing batch files.
func (o *Options) Validate() error {
	if !prune.ValidRoundName(o.Round) {
		return cmdutil.FlagErrorf("--round must match %s (it becomes a directory name), got %q",
			prune.RoundNamePattern, o.Round)
	}
	if o.ChunkCap <= 0 {
		return cmdutil.FlagErrorf("--chunk-cap must be > 0, got %d", o.ChunkCap)
	}
	if o.DeltaSince != "" {
		if _, err := time.Parse(time.RFC3339, o.DeltaSince); err != nil {
			return cmdutil.FlagErrorf("--delta-since must be RFC3339, got %q: %v", o.DeltaSince, err)
		}
	}
	return nil
}

func NewCmdChunk(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{IO: f.IOStreams}
	cmd := &cobra.Command{
		Use:   "chunk",
		Short: "Bucket remembered memories into review batches for a pruning round",
		Long: "Buckets remembered memories by key-prefix family (the same families " +
			"the site groups pages by), alpha-splitting any family over --chunk-cap " +
			"into numbered batches, and writes one bodies+cites bundle per batch " +
			"under rounds/<round>/batches/ for agent review.\n\n" +
			"--delta-since restricts the key set to memories first seen or last " +
			"updated after that time -- use it on a round after the first to review " +
			"only what's new or changed since the last full pass, instead of " +
			"re-reviewing an already-scrubbed survivor set. This operationalizes " +
			"round 4's delta-scope pivot (measured ~0.4% forget yield re-reviewing " +
			"untouched survivors vs 9-26% on genuinely new/changed keys) as a flag " +
			"instead of a manual mid-round decision.",
		RunE: func(c *cobra.Command, args []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}
			if runF != nil {
				return runF(opts)
			}
			return chunkRun(opts)
		},
	}
	cmd.Flags().StringVar(&opts.MemoriesPath, "memories", "memories.jsonl", "path to memories.jsonl")
	cmd.Flags().StringVar(&opts.RoundsDir, "rounds-dir", "rounds", "directory to write round artifacts under")
	cmd.Flags().StringVar(&opts.Round, "round", "", "round name, e.g. R5 (becomes a directory + batch-id prefix)")
	cmd.Flags().IntVar(&opts.ChunkCap, "chunk-cap", defaultChunkCap, "max keys per review batch")
	cmd.Flags().StringVar(&opts.DeltaSince, "delta-since", "",
		"RFC3339 timestamp; restrict to keys first seen or updated after this time")
	if err := cmd.MarkFlagRequired("round"); err != nil {
		// Only fails if the flag name above is misspelled -- a programmer
		// error that should fail loud at dev/test time, not silently.
		panic(err)
	}
	return cmd
}

// citesSubset is the per-batch citation bundle: any citation edge touching
// a key in the batch, in or out, so reviewers see load-bearing citations
// even from keys outside their batch. Field names ("out"/"in") match the
// deleted bd_chunk_for_review.py's cites-<batch>.json shape.
type citesSubset struct {
	Out map[string][]string `json:"out,omitempty"`
	In  map[string][]string `json:"in,omitempty"`
}

func chunkRun(opts *Options) error {
	records, err := memory.Load(opts.MemoriesPath)
	if err != nil {
		return err
	}

	var since time.Time
	hasSince := opts.DeltaSince != ""
	if hasSince {
		since, _ = time.Parse(time.RFC3339, opts.DeltaSince) // already validated
	}

	var keys []string
	byKey := map[string]*memory.Memory{}
	for _, m := range records {
		if !m.Remembered() {
			continue
		}
		if hasSince && !isAfterOrEqual(m.FirstSeen, since) && !isAfterOrEqual(m.UpdatedAt, since) {
			continue
		}
		keys = append(keys, m.Key)
		byKey[m.Key] = m
	}

	citations := prune.BuildGraph(records)

	families := map[string][]string{}
	for _, k := range keys {
		fam := memory.PrefixOf(k)
		families[fam] = append(families[fam], k)
	}

	famNames := make([]string, 0, len(families))
	for fam := range families {
		famNames = append(famNames, fam)
	}
	sort.Strings(famNames)

	roundDir := filepath.Join(opts.RoundsDir, opts.Round)
	batchesDir := filepath.Join(roundDir, "batches")
	if err := os.MkdirAll(batchesDir, 0o755); err != nil {
		return err
	}

	batchIndex := map[string][]string{}
	for _, fam := range famNames {
		chunks := memory.AlphaSplit(families[fam], opts.ChunkCap)
		for i, chunkKeys := range chunks {
			batchID := batchIDFor(opts.Round, fam, i, len(chunks))
			batchIndex[batchID] = chunkKeys

			batchDir := filepath.Join(batchesDir, batchID)
			if err := os.MkdirAll(batchDir, 0o755); err != nil {
				return err
			}

			bodies := make(map[string]string, len(chunkKeys))
			for _, k := range chunkKeys {
				bodies[k] = byKey[k].Body
			}
			if err := writeJSON(filepath.Join(batchDir, "bodies.json"), bodies); err != nil {
				return err
			}

			ck := make(map[string]bool, len(chunkKeys))
			for _, k := range chunkKeys {
				ck[k] = true
			}
			subset := citesSubset{Out: map[string][]string{}, In: map[string][]string{}}
			for _, k := range chunkKeys {
				if c := citations.Cites[k]; len(c) > 0 {
					subset.Out[k] = c
				}
				if c := citations.CitedBy[k]; len(c) > 0 {
					subset.In[k] = c
				}
			}
			if err := writeJSON(filepath.Join(batchDir, "cites.json"), subset); err != nil {
				return err
			}
		}
	}

	if err := writeJSON(filepath.Join(roundDir, "batch-index.json"), batchIndex); err != nil {
		return err
	}

	out := opts.IO.Out
	fmt.Fprintf(out, "wrote %d batches under %s:\n", len(batchIndex), batchesDir)
	batchIDs := make([]string, 0, len(batchIndex))
	for id := range batchIndex {
		batchIDs = append(batchIDs, id)
	}
	sort.Strings(batchIDs)
	for _, id := range batchIDs {
		fmt.Fprintf(out, "  %-24s %4d keys\n", id, len(batchIndex[id]))
	}
	return nil
}

// batchIDFor mirrors bd_chunk_for_review.py's naming: "<round>-<FAM>" when
// a family fits in one chunk, "<round>-<FAM>-<n>" (1-indexed) when split.
func batchIDFor(round, family string, chunkIdx, nChunks int) string {
	fam := strings.ToUpper(family)
	if nChunks == 1 {
		return round + "-" + fam
	}
	return fmt.Sprintf("%s-%s-%d", round, fam, chunkIdx+1)
}

func isAfterOrEqual(ts string, since time.Time) bool {
	if ts == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return false
	}
	return !t.Before(since)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
