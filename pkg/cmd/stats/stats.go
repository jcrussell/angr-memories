// Package stats implements `angr-memories stats`: a live query over
// memories.jsonl replacing the old static batch_stats.txt/forget_reasons.txt
// digest files.
package stats

import (
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

type Options struct {
	IO           *iostreams.IOStreams
	MemoriesPath string
}

func NewCmdStats(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{IO: f.IOStreams}
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Print remembered/forgotten counts by prefix",
		RunE: func(c *cobra.Command, args []string) error {
			if runF != nil {
				return runF(opts)
			}
			return statsRun(opts)
		},
	}
	cmd.Flags().StringVar(&opts.MemoriesPath, "memories", "memories.jsonl", "path to memories.jsonl")
	return cmd
}

func statsRun(opts *Options) error {
	records, err := memory.Load(opts.MemoriesPath)
	if err != nil {
		return err
	}
	s := memory.Aggregate(records)

	out := opts.IO.Out
	fmt.Fprintf(out, "total: %d remembered, %d forgotten (%d records, %d recreated key%s)\n",
		s.TotalRemembered, s.TotalForgotten, len(records), s.Recreated, plural(s.Recreated))
	fmt.Fprintln(out)

	prefixes := make([]string, 0, len(s.ByPrefix))
	for p := range s.ByPrefix {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)
	fmt.Fprintf(out, "%-12s %10s %10s\n", "prefix", "remembered", "forgotten")
	for _, p := range prefixes {
		b := s.ByPrefix[p]
		fmt.Fprintf(out, "%-12s %10d %10d\n", p, b.Remembered, b.Forgotten)
	}

	if len(s.SurvivalDurations) > 0 {
		var total time.Duration
		for _, d := range s.SurvivalDurations {
			total += d
		}
		mean := total / time.Duration(len(s.SurvivalDurations))
		median := s.SurvivalDurations[len(s.SurvivalDurations)/2]
		fmt.Fprintln(out)
		fmt.Fprintf(out, "forgotten-memory survival time: mean %s, median %s\n",
			roundToDay(mean), roundToDay(median))
	}

	return nil
}

func roundToDay(d time.Duration) time.Duration { return d.Round(24 * time.Hour) }

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
