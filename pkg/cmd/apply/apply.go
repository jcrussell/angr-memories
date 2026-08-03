// Package apply implements `angr-memories apply`: apply a round's batch
// manifests to memories.jsonl. Ported from the deleted tools/bd_prune.py,
// but mutating the archive directly (DecidedAt/Reason/MergedInto/Body)
// instead of shelling out to `bd forget`/`bd remember` -- this repo is the
// archive, not a live bd-backed corpus.
package apply

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/internal/prune"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

type Options struct {
	IO           *iostreams.IOStreams
	MemoriesPath string
	RoundsDir    string
	Round        string
	KeepListPath string
	Apply        bool
	Force        bool
}

func (o *Options) Validate() error {
	if !prune.ValidRoundName(o.Round) {
		return cmdutil.FlagErrorf("--round must match %s, got %q", prune.RoundNamePattern, o.Round)
	}
	return nil
}

func NewCmdApply(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{IO: f.IOStreams}
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply a round's batch manifests to memories.jsonl",
		Long: "Loads every batch manifest in rounds/<round>/batches/, combines them " +
			"into one manifest (giving the citation orphan guard full visibility across " +
			"batch boundaries, the same reason round 3 applied combined rather than " +
			"per-batch), re-validates the combination, and applies forget/merge " +
			"decisions directly to memories.jsonl. Promote rows are reported but never " +
			"auto-applied -- see the package doc for why.\n\n" +
			"Citation orphan guard: a forget/merge-away is deferred, not applied, if " +
			"some other remembered memory outside this manifest still cites it. Pass " +
			"--force to skip the guard.\n\n" +
			"Dry-run by default -- pass --apply to actually mutate memories.jsonl and " +
			"append to rounds/<round>/decisions.jsonl.",
		RunE: func(c *cobra.Command, args []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}
			if runF != nil {
				return runF(opts)
			}
			return applyRun(opts)
		},
	}
	cmd.Flags().StringVar(&opts.MemoriesPath, "memories", "memories.jsonl", "path to memories.jsonl")
	cmd.Flags().StringVar(&opts.RoundsDir, "rounds-dir", "rounds", "directory round artifacts are written under")
	cmd.Flags().StringVar(&opts.Round, "round", "", "round name to apply, e.g. R5")
	cmd.Flags().StringVar(&opts.KeepListPath, "keep-list", "rounds/mandatory-keep.txt", "path to the mandatory-keep list")
	cmd.Flags().BoolVar(&opts.Apply, "apply", false, "actually mutate memories.jsonl (default: dry-run)")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "skip the citation orphan guard")
	if err := cmd.MarkFlagRequired("round"); err != nil {
		panic(err)
	}
	return cmd
}

// batchedRow tracks which batch a manifest row came from, purely for the
// decisions.jsonl audit trail -- kept out of prune.Row so the manifest
// schema a reviewer writes doesn't have to carry provenance.
type batchedRow struct {
	prune.Row
	Batch string
}

func applyRun(opts *Options) error {
	roundDir := filepath.Join(opts.RoundsDir, opts.Round)

	var index map[string][]string
	if err := readJSON(filepath.Join(roundDir, "batch-index.json"), &index); err != nil {
		return fmt.Errorf("reading batch-index.json (did you run `chunk --round %s` first?): %w", opts.Round, err)
	}

	batchIDs := make([]string, 0, len(index))
	for id := range index {
		batchIDs = append(batchIDs, id)
	}
	sort.Strings(batchIDs)

	var allRows []batchedRow
	allKeys := map[string]bool{}
	for _, batchID := range batchIDs {
		for _, k := range index[batchID] {
			allKeys[k] = true
		}
		manifestPath := filepath.Join(roundDir, "batches", batchID, "manifest.jsonl")
		rows, err := prune.LoadManifest(manifestPath)
		if err != nil {
			return fmt.Errorf("batch %s has no manifest yet -- run validate first: %w", batchID, err)
		}
		for _, r := range rows {
			allRows = append(allRows, batchedRow{Row: r, Batch: batchID})
		}
	}

	mandatoryKeep, err := prune.LoadKeepList(opts.KeepListPath)
	if err != nil {
		return err
	}

	plainRows := make([]prune.Row, len(allRows))
	for i, r := range allRows {
		plainRows[i] = r.Row
	}
	if errs := prune.Validate(plainRows, allKeys, mandatoryKeep); len(errs) > 0 {
		msg := fmt.Sprintf("round %s: manifest failed validation (run `validate` for details):\n", opts.Round)
		for _, e := range errs {
			msg += "  - " + e + "\n"
		}
		return fmt.Errorf("%s", msg)
	}

	records, err := memory.Load(opts.MemoriesPath)
	if err != nil {
		return err
	}
	citations := prune.BuildGraph(records)

	now := time.Now()
	outcomes, err := prune.Apply(records, plainRows, citations, opts.Force, now)
	if err != nil {
		return err
	}

	batchOf := make(map[string]string, len(allRows))
	for _, r := range allRows {
		batchOf[r.Key] = r.Batch
	}

	if err := report(opts.IO, outcomes, opts.Apply); err != nil {
		return err
	}

	if !opts.Apply {
		fmt.Fprintln(opts.IO.Out, "\ndry-run: pass --apply to write these changes")
		return nil
	}

	if err := memory.Save(opts.MemoriesPath, records); err != nil {
		return err
	}
	if err := appendDecisions(roundDir, opts.Round, outcomes, batchOf, now); err != nil {
		return err
	}
	return nil
}

func report(ios *iostreams.IOStreams, outcomes []prune.Outcome, applying bool) error {
	counts := map[string]int{}
	out := ios.Out
	mode := "dry-run"
	if applying {
		mode = "applied"
	}

	var deferred, promoted []prune.Outcome
	for _, o := range outcomes {
		if o.Deferred {
			deferred = append(deferred, o)
			counts["deferred"]++
			continue
		}
		counts[string(o.Row.Verdict)]++
		if o.Row.Verdict == prune.VerdictPromote {
			promoted = append(promoted, o)
		}
	}

	fmt.Fprintf(out, "%s: forget=%d merge=%d promote=%d keep=%d deferred=%d\n",
		mode, counts[string(prune.VerdictForget)], counts[string(prune.VerdictMerge)],
		counts[string(prune.VerdictPromote)], counts[string(prune.VerdictKeep)], counts["deferred"])

	if len(deferred) > 0 {
		fmt.Fprintln(out, "\ndeferred (cited by a survivor -- needs re-review in a later round):")
		for _, o := range deferred {
			fmt.Fprintf(out, "  %-40s %s\n", o.Row.Key, o.Why)
		}
	}
	if len(promoted) > 0 {
		fmt.Fprintln(out, "\npromote candidates (not auto-applied -- add to docs by hand, then forget):")
		for _, o := range promoted {
			fmt.Fprintf(out, "  %-40s -> %s\n", o.Row.Key, o.Row.PromotedTo)
		}
	}
	return nil
}

type decisionRecord struct {
	Key       string    `json:"key"`
	Verdict   string    `json:"verdict"`
	Reason    string    `json:"reason,omitempty"`
	Batch     string    `json:"batch"`
	Round     string    `json:"round"`
	Deferred  bool      `json:"deferred,omitempty"`
	Why       string    `json:"why,omitempty"`
	AppliedAt time.Time `json:"applied_at"`
}

func appendDecisions(roundDir, round string, outcomes []prune.Outcome, batchOf map[string]string, at time.Time) error {
	f, err := os.OpenFile(filepath.Join(roundDir, "decisions.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, o := range outcomes {
		rec := decisionRecord{
			Key:       o.Row.Key,
			Verdict:   string(o.Row.Verdict),
			Reason:    o.Row.Reason,
			Batch:     batchOf[o.Row.Key],
			Round:     round,
			Deferred:  o.Deferred,
			Why:       o.Why,
			AppliedAt: at.UTC(),
		}
		if err := enc.Encode(rec); err != nil {
			return err
		}
	}
	return nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
