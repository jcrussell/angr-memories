// Package validate implements `angr-memories validate`: check every batch
// manifest in a round against its own bodies bundle and the mandatory-keep
// list, porting round4/validate_manifest.py's checks (which used to run
// per-batch by hand) into one command that covers a whole round.
package validate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/jcrussell/angr-memories/internal/prune"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

type Options struct {
	IO           *iostreams.IOStreams
	RoundsDir    string
	Round        string
	KeepListPath string
}

func (o *Options) Validate() error {
	if !prune.ValidRoundName(o.Round) {
		return cmdutil.FlagErrorf("--round must match %s, got %q", prune.RoundNamePattern, o.Round)
	}
	return nil
}

func NewCmdValidate(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{IO: f.IOStreams}
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate every batch manifest in a round",
		Long: "Checks every rounds/<round>/batches/<batch>/manifest.jsonl against its " +
			"own bodies.json (every key present exactly once, no dupes/missing/extra), " +
			"verdict values, the mandatory-keep list, and merge row consistency " +
			"(canonicals carry merged_body, subordinates' merged_into targets are " +
			"in-batch canonical rows). Fails loud -- a round isn't ready for apply " +
			"until every batch validates clean.",
		RunE: func(c *cobra.Command, args []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}
			if runF != nil {
				return runF(opts)
			}
			return validateRun(opts)
		},
	}
	cmd.Flags().StringVar(&opts.RoundsDir, "rounds-dir", "rounds", "directory round artifacts are written under")
	cmd.Flags().StringVar(&opts.Round, "round", "", "round name to validate, e.g. R5")
	cmd.Flags().StringVar(&opts.KeepListPath, "keep-list", "rounds/mandatory-keep.txt", "path to the mandatory-keep list")
	if err := cmd.MarkFlagRequired("round"); err != nil {
		panic(err)
	}
	return cmd
}

func validateRun(opts *Options) error {
	roundDir := filepath.Join(opts.RoundsDir, opts.Round)

	var index map[string][]string
	if err := readJSON(filepath.Join(roundDir, "batch-index.json"), &index); err != nil {
		return fmt.Errorf("reading batch-index.json (did you run `chunk --round %s` first?): %w", opts.Round, err)
	}

	mandatoryKeep, err := prune.LoadKeepList(opts.KeepListPath)
	if err != nil {
		return err
	}

	batchIDs := make([]string, 0, len(index))
	for id := range index {
		batchIDs = append(batchIDs, id)
	}
	sort.Strings(batchIDs)

	out := opts.IO.Out
	anyInvalid := false
	for _, batchID := range batchIDs {
		batchKeys := make(map[string]bool, len(index[batchID]))
		for _, k := range index[batchID] {
			batchKeys[k] = true
		}

		manifestPath := filepath.Join(roundDir, "batches", batchID, "manifest.jsonl")
		rows, err := prune.LoadManifest(manifestPath)
		if err != nil {
			fmt.Fprintf(out, "%-24s INVALID: no manifest yet (%v)\n", batchID, err)
			anyInvalid = true
			continue
		}

		errs := prune.Validate(rows, batchKeys, mandatoryKeep)
		if len(errs) > 0 {
			fmt.Fprintf(out, "%-24s INVALID:\n", batchID)
			for _, e := range errs {
				fmt.Fprintf(out, "  - %s\n", e)
			}
			anyInvalid = true
			continue
		}

		fmt.Fprintf(out, "%-24s OK (%d keys, %s)\n", batchID, len(rows), verdictCounts(rows))
	}

	if anyInvalid {
		return fmt.Errorf("round %s: one or more batches failed validation", opts.Round)
	}
	fmt.Fprintf(out, "\nround %s: all %d batches valid\n", opts.Round, len(batchIDs))
	return nil
}

func verdictCounts(rows []prune.Row) string {
	counts := map[prune.Verdict]int{}
	for _, r := range rows {
		counts[r.Verdict]++
	}
	return fmt.Sprintf("forget=%d merge=%d promote=%d keep=%d",
		counts[prune.VerdictForget], counts[prune.VerdictMerge], counts[prune.VerdictPromote], counts[prune.VerdictKeep])
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
