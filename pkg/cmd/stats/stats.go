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

type bucket struct{ remembered, forgotten int }

func statsRun(opts *Options) error {
	records, err := memory.Load(opts.MemoriesPath)
	if err != nil {
		return err
	}

	byPrefix := map[string]*bucket{}
	var totalRemembered, totalForgotten int
	var survivalDurations []time.Duration
	incarnationsPerKey := map[string]int{}

	for _, m := range records {
		incarnationsPerKey[m.Key]++

		p := memory.PrefixOf(m.Key)
		b := byPrefix[p]
		if b == nil {
			b = &bucket{}
			byPrefix[p] = b
		}

		if m.Remembered() {
			b.remembered++
			totalRemembered++
			continue
		}
		b.forgotten++
		totalForgotten++
		if fs, err := time.Parse(time.RFC3339, m.FirstSeen); err == nil {
			if da, err := time.Parse(time.RFC3339, m.DecidedAt); err == nil {
				survivalDurations = append(survivalDurations, da.Sub(fs))
			}
		}
	}

	recreated := 0
	for _, n := range incarnationsPerKey {
		if n > 1 {
			recreated++
		}
	}

	out := opts.IO.Out
	fmt.Fprintf(out, "total: %d remembered, %d forgotten (%d records, %d recreated key%s)\n",
		totalRemembered, totalForgotten, len(records), recreated, plural(recreated))
	fmt.Fprintln(out)

	prefixes := make([]string, 0, len(byPrefix))
	for p := range byPrefix {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)
	fmt.Fprintf(out, "%-12s %10s %10s\n", "prefix", "remembered", "forgotten")
	for _, p := range prefixes {
		b := byPrefix[p]
		fmt.Fprintf(out, "%-12s %10d %10d\n", p, b.remembered, b.forgotten)
	}

	if len(survivalDurations) > 0 {
		sort.Slice(survivalDurations, func(i, j int) bool { return survivalDurations[i] < survivalDurations[j] })
		var total time.Duration
		for _, d := range survivalDurations {
			total += d
		}
		mean := total / time.Duration(len(survivalDurations))
		median := survivalDurations[len(survivalDurations)/2]
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
