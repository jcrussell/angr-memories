// Package sync implements `angr-memories sync`: pull the live angr project's
// bd memory corpus and fold new/removed/edited keys into memories.jsonl, so
// the archive stays current instead of frozen at round 4.
package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

// maxCloseFraction bounds how much of the currently-remembered set a single
// sync run is allowed to close without --force. `bd memories --json`
// returning an empty or truncated-but-valid map (wrong --source, a bd bug,
// a mid-migration DB race) would otherwise read as "everything was
// forgotten" and silently mass-close the whole archive -- confirmed as a
// real, unguarded failure mode during review. This check only guards
// against a live snapshot that looks implausibly *smaller* than what's on
// record; it does nothing for a live snapshot that's merely wrong in some
// other way (bd returning stale or corrupted-but-plausible content), which
// no local sanity check can catch.
const maxCloseFraction = 0.2

// minOpenForGuard: below this many currently-open records, the fraction
// guard is skipped -- on a small or freshly-seeded archive, closing more
// than 20% in one run is plausible and not worth blocking on.
const minOpenForGuard = 10

type Options struct {
	IO           *iostreams.IOStreams
	Source       string
	MemoriesPath string
	Timeout      time.Duration
	Force        bool
}

func NewCmdSync(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{IO: f.IOStreams}
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Pull the live angr bd corpus and fold changes into memories.jsonl",
		Long: "Runs `bd memories --json` in --source (so bd's .beads/*.db auto-discovery " +
			"finds the live DB), diffs it against the currently-remembered records in " +
			"memories.jsonl, and appends new records / closes vanished ones / updates " +
			"edited bodies in place. Safe to run repeatedly -- a no-op against an " +
			"unchanged live corpus. Refuses to close an implausibly large fraction of " +
			"the archive in one run (an empty or truncated live snapshot would otherwise " +
			"read as a mass-forget) unless --force is passed.",
		RunE: func(c *cobra.Command, args []string) error {
			if runF != nil {
				return runF(opts)
			}
			return syncRun(opts)
		},
	}
	cmd.Flags().StringVar(&opts.Source, "source", "", "angr project directory (where bd's .beads/ lives)")
	cmd.Flags().StringVar(&opts.MemoriesPath, "memories", "memories.jsonl", "path to memories.jsonl")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", 30*time.Second, "timeout for the bd subprocess")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "skip the mass-close sanity check")
	if err := cmd.MarkFlagRequired("source"); err != nil {
		// Only fails if the flag name above is misspelled -- a programmer
		// error that should fail loud at dev/test time, not silently.
		panic(err)
	}
	return cmd
}

func syncRun(opts *Options) error {
	live, err := liveSnapshot(opts.Source, opts.Timeout)
	if err != nil {
		return err
	}

	records, err := memory.Load(opts.MemoriesPath)
	if err != nil {
		return err
	}

	var open []*memory.Memory
	for _, m := range records {
		if m.Remembered() {
			open = append(open, m)
		}
	}

	if !opts.Force {
		if err := sanityCheckLive(live, open); err != nil {
			return err
		}
	}

	opened, closed, edited := memory.Diff(open, live, time.Now())
	records = append(records, opened...)

	if err := memory.Save(opts.MemoriesPath, records); err != nil {
		return err
	}

	fmt.Fprintf(opts.IO.Out, "sync: %d new, %d closed, %d edited (%d live, %d total records)\n",
		len(opened), len(closed), len(edited), len(live), len(records))
	return nil
}

// sanityCheckLive refuses an implausible-looking live snapshot before
// anything gets closed: empty outright, or closing more than
// maxCloseFraction of the currently-open set on an archive large enough
// for that fraction to be meaningful.
func sanityCheckLive(live map[string]string, open []*memory.Memory) error {
	if len(live) == 0 {
		return fmt.Errorf("live snapshot is empty (0 memories) -- refusing to treat this as " +
			"\"everything was forgotten\"; pass --force if this is genuinely correct")
	}

	if len(open) < minOpenForGuard {
		return nil
	}

	wouldClose := 0
	for _, m := range open {
		if _, stillLive := live[m.Key]; !stillLive {
			wouldClose++
		}
	}
	if frac := float64(wouldClose) / float64(len(open)); frac > maxCloseFraction {
		return fmt.Errorf("live snapshot would close %d/%d (%.0f%%) of currently-remembered records, "+
			"above the %.0f%% sanity threshold -- refusing; pass --force if this is genuinely correct",
			wouldClose, len(open), frac*100, maxCloseFraction*100)
	}
	return nil
}

// liveSnapshot runs `bd memories --json` in dir and parses its stdout as a
// {key: body} map. stdout and stderr are captured separately -- byob-go-cli's
// own Makefile redirects bd's stderr to /dev/null before piping stdout
// onward (`bd export --include-memories 2>/dev/null | ...`), real evidence
// that bd's stderr can't be trusted mixed into stdout.
func liveSnapshot(dir string, timeout time.Duration) (map[string]string, error) {
	if dir == "" {
		return nil, fmt.Errorf("--source is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bd", "memories", "--json")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("bd memories --json (in %s): %w (stderr: %s)", dir, err, stderr.String())
	}

	var live map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &live); err != nil {
		return nil, fmt.Errorf("bd memories --json (in %s): parse stdout: %w", dir, err)
	}
	return live, nil
}
