// Package apply implements `angr-memories apply`: apply a round's batch
// manifests to memories.jsonl, mutating the archive directly
// (DecidedAt/Reason/MergedInto/Body) as the round's reviewed source of
// truth. Ported from the deleted tools/bd_prune.py. Optionally, with
// --source, also pushes the same forgets live via `bd forget` against an
// angr checkout's bd-backed corpus -- backing up each key's live body first,
// since bd has no atomic multi-key forget or rollback, so this is
// best-effort, not transactional.
package apply

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

// bdRunner runs a fixed-argv bd subprocess in dir with a timeout, returning
// stdout. Overridable on Options for tests -- never fake bd via PATH/env,
// this keeps the real exec.CommandContext call at the edge and out of test
// scope entirely.
type bdRunner func(ctx context.Context, dir string, timeout time.Duration, name string, args ...string) ([]byte, error)

type Options struct {
	IO           *iostreams.IOStreams
	MemoriesPath string
	RoundsDir    string
	Round        string
	KeepListPath string
	Force        bool
	SkipGuard    bool
	Source       string
	Timeout      time.Duration

	runBd bdRunner
}

func (o *Options) Validate() error {
	if !prune.ValidRoundName(o.Round) {
		return cmdutil.FlagErrorf("--round must match %s, got %q", prune.RoundNamePattern, o.Round)
	}
	return nil
}

func NewCmdApply(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{IO: f.IOStreams, runBd: runBdCommand}
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
			"--skip-guard to skip the guard.\n\n" +
			"Dry-run by default -- pass -f/--force to actually mutate memories.jsonl, " +
			"back it up to rounds/<round>/memories.pre-apply.jsonl first, and append to " +
			"rounds/<round>/decisions.jsonl.\n\n" +
			"Live push: pass --source <angr-checkout> alongside --force to also " +
			"`bd forget` every newly-forgotten key against that checkout's live bd " +
			"corpus. Each key's live body is backed up to " +
			"rounds/<round>/bd-live-backup.jsonl before it's forgotten, and per-key " +
			"push outcomes are logged to rounds/<round>/live-push.jsonl. bd has no " +
			"atomic multi-key forget or rollback, so a failed push warns and continues " +
			"rather than aborting the batch -- restoring a key is a manual " +
			"`bd remember \"<body>\" --key <key>` from the backup file. Exits non-zero " +
			"if any push fails, even though the archive-side apply already succeeded.",
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
	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "actually mutate memories.jsonl (default: dry-run)")
	cmd.Flags().BoolVar(&opts.SkipGuard, "skip-guard", false, "skip the citation orphan guard")
	cmd.Flags().StringVar(&opts.Source, "source", "", "angr checkout to also push forgets to live bd (where bd's .beads/ lives); omit for archive-only")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", 30*time.Second, "timeout per bd subprocess call")
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
	outcomes, err := prune.Apply(records, plainRows, citations, opts.SkipGuard, now)
	if err != nil {
		return err
	}

	batchOf := make(map[string]string, len(allRows))
	for _, r := range allRows {
		batchOf[r.Key] = r.Batch
	}

	if err := report(opts.IO, outcomes, opts.Force); err != nil {
		return err
	}

	if !opts.Force {
		fmt.Fprintln(opts.IO.Out, "\ndry-run: pass -f/--force to write these changes")
		if opts.Source != "" {
			if n := len(forgottenKeys(outcomes)); n > 0 {
				fmt.Fprintf(opts.IO.Out, "live-push preview: would push forget for up to %d keys to bd at %s (fewer if some are already gone live)\n", n, opts.Source)
			}
		}
		return nil
	}

	if err := backupMemories(opts.MemoriesPath, roundDir); err != nil {
		return err
	}

	if err := memory.Save(opts.MemoriesPath, records); err != nil {
		return err
	}
	if err := appendDecisions(roundDir, opts.Round, outcomes, batchOf, now); err != nil {
		return err
	}

	if opts.Source != "" {
		if err := livePush(opts, roundDir, outcomes); err != nil {
			return err
		}
	}
	return nil
}

// backupMemories copies memoriesPath's current bytes verbatim to
// rounds/<round>/memories.pre-apply.jsonl before it's mutated -- round-scoped
// so it sits with that round's other audit artifacts and captures exactly
// the state this round's apply is about to change.
func backupMemories(memoriesPath, roundDir string) error {
	data, err := os.ReadFile(memoriesPath)
	if err != nil {
		return fmt.Errorf("backing up %s: %w", memoriesPath, err)
	}
	backupPath := filepath.Join(roundDir, "memories.pre-apply.jsonl")
	if err := os.WriteFile(backupPath, data, 0o644); err != nil {
		return fmt.Errorf("writing backup %s: %w", backupPath, err)
	}
	return nil
}

// forgottenKeys returns the keys that become forgotten by this manifest --
// plain forgets and merge-subordinates, excluding anything the citation
// orphan guard deferred. Mirrors internal/prune/apply.go's own
// removed/goneOrRewritten computation; merge-canonicals are excluded since
// their live body is already correct in bd.
func forgottenKeys(outcomes []prune.Outcome) []string {
	var keys []string
	for _, o := range outcomes {
		if o.Deferred {
			continue
		}
		switch {
		case o.Row.Verdict == prune.VerdictForget:
			keys = append(keys, o.Row.Key)
		case o.Row.Verdict == prune.VerdictMerge && !o.Row.Canonical:
			keys = append(keys, o.Row.Key)
		}
	}
	return keys
}

type bdBackupEntry struct {
	Key  string `json:"key"`
	Body string `json:"body"`
}

type livePushResult struct {
	Key      string    `json:"key"`
	Success  bool      `json:"success"`
	Error    string    `json:"error,omitempty"`
	PushedAt time.Time `json:"pushed_at"`
}

// livePush backs up each newly-forgotten key's current live body (from `bd
// memories --json`) to rounds/<round>/bd-live-backup.jsonl, then calls
// `bd forget <key>` for each one against opts.Source, logging per-key
// outcomes to rounds/<round>/live-push.jsonl. bd has no atomic multi-key
// forget or rollback, so a failed forget warns and continues rather than
// aborting the rest of the batch -- there's nothing to roll back to anyway,
// and the archive-side apply this runs after has already succeeded.
// Restoring a key is a manual `bd remember "<body>" --key <key>` from the
// backup file.
func livePush(opts *Options, roundDir string, outcomes []prune.Outcome) error {
	keys := forgottenKeys(outcomes)
	if len(keys) == 0 {
		return nil
	}

	ctx := context.Background()
	raw, err := opts.runBd(ctx, opts.Source, opts.Timeout, "bd", "memories", "--json")
	if err != nil {
		return fmt.Errorf("live-push: bd memories --json (in %s): %w", opts.Source, err)
	}
	var live map[string]string
	if err := json.Unmarshal(raw, &live); err != nil {
		return fmt.Errorf("live-push: bd memories --json (in %s): parse: %w", opts.Source, err)
	}

	backupPath := filepath.Join(roundDir, "bd-live-backup.jsonl")
	bf, err := os.Create(backupPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", backupPath, err)
	}
	enc := json.NewEncoder(bf)
	var toForget []string
	for _, k := range keys {
		body, ok := live[k]
		if !ok {
			continue // already gone live -- nothing to back up or forget
		}
		if err := enc.Encode(bdBackupEntry{Key: k, Body: body}); err != nil {
			bf.Close()
			return fmt.Errorf("writing %s: %w", backupPath, err)
		}
		toForget = append(toForget, k)
	}
	if err := bf.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", backupPath, err)
	}

	logPath := filepath.Join(roundDir, "live-push.jsonl")
	lf, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", logPath, err)
	}
	lenc := json.NewEncoder(lf)

	pushed, failed := 0, 0
	for _, k := range toForget {
		_, pushErr := opts.runBd(ctx, opts.Source, opts.Timeout, "bd", "forget", k)
		result := livePushResult{Key: k, PushedAt: time.Now().UTC()}
		if pushErr != nil && !isAlreadyGone(pushErr) {
			result.Error = pushErr.Error()
			failed++
			fmt.Fprintf(opts.IO.ErrOut, "WARN: bd forget %s: %v\n", k, pushErr)
		} else {
			result.Success = true
			pushed++
		}
		if err := lenc.Encode(result); err != nil {
			lf.Close()
			return fmt.Errorf("writing %s: %w", logPath, err)
		}
	}
	if err := lf.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", logPath, err)
	}

	fmt.Fprintf(opts.IO.Out, "\nlive-push (source=%s): forgot=%d failed=%d (backup: %s, log: %s)\n",
		opts.Source, pushed, failed, backupPath, logPath)

	if failed > 0 {
		return fmt.Errorf("live-push: %d of %d keys failed to push to bd -- see %s", failed, len(toForget), logPath)
	}
	return nil
}

// isAlreadyGone reports whether err looks like bd rejected a forget because
// the key is already gone -- treated as a successful no-op so re-running
// apply after a partial live-push failure only retries the keys that
// actually still need it.
func isAlreadyGone(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not found") || strings.Contains(msg, "no such") || strings.Contains(msg, "unknown key")
}

// runBdCommand is the real bdRunner: a fixed-argv subprocess (never a shell)
// run with dir as its cwd so bd's own .beads/*.db auto-discovery finds the
// live corpus, mirroring pkg/cmd/sync's liveSnapshot and pkg/cmd/triage's
// runInDir.
func runBdCommand(ctx context.Context, dir string, timeout time.Duration, name string, args ...string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %v (in %s): %w (stderr: %s)", name, args, dir, err, stderr.String())
	}
	return stdout.Bytes(), nil
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
