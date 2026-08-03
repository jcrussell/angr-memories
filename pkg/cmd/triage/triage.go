// Package triage implements `angr-memories triage`: tag every remembered
// memory with the heuristics ported from the deleted tools/bd_triage.py, so
// a pruning round can prioritize batches instead of reviewing everything
// with equal attention.
package triage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
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

type Options struct {
	IO           *iostreams.IOStreams
	MemoriesPath string
	Source       string // angr checkout for bead status + git ls-files; optional
	DocsDir      string // docs checkout for dup-of-docs fragment matching; optional
	Out          string
	Timeout      time.Duration
}

func (o *Options) Validate() error {
	if o.Out == "" {
		return cmdutil.FlagErrorf("--out must not be empty")
	}
	if o.Timeout <= 0 {
		return cmdutil.FlagErrorf("--timeout must be > 0, got %s", o.Timeout)
	}
	return nil
}

func NewCmdTriage(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{IO: f.IOStreams}
	cmd := &cobra.Command{
		Use:   "triage",
		Short: "Tag remembered memories with pruning heuristics",
		Long: "Tags every remembered memory with the heuristics ported from the " +
			"deleted tools/bd_triage.py: status/closure shape, doc-length-and-language " +
			"(promote candidate), stale/future dates, citing another memory, a verbatim " +
			"fragment match against --docs-dir, and citing a repo path --source's " +
			"`git ls-files` doesn't know about. --source also enables a closed_only tag " +
			"via `bd list --status=... --json` bead-status lookups. Both --source and " +
			"--docs-dir are optional -- their tags simply stay false without them. " +
			"Writes one JSON row per key to --out; use the tags to prioritize `chunk` " +
			"batches instead of reviewing everything with equal attention.",
		RunE: func(c *cobra.Command, args []string) error {
			if err := opts.Validate(); err != nil {
				return err
			}
			if runF != nil {
				return runF(opts)
			}
			return triageRun(opts)
		},
	}
	cmd.Flags().StringVar(&opts.MemoriesPath, "memories", "memories.jsonl", "path to memories.jsonl")
	cmd.Flags().StringVar(&opts.Source, "source", "", "angr project directory (enables closed_only + stale_file_ref)")
	cmd.Flags().StringVar(&opts.DocsDir, "docs-dir", "", "directory of docs/**/*.rst to check for dup_of_docs")
	cmd.Flags().StringVar(&opts.Out, "out", "triage.jsonl", "path to write triage tags to")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", 30*time.Second, "timeout for each bd/git subprocess call")
	return cmd
}

type triageRow struct {
	Key    string `json:"key"`
	Prefix string `json:"prefix"`
	prune.Tags
}

func triageRun(opts *Options) error {
	records, err := memory.Load(opts.MemoriesPath)
	if err != nil {
		return err
	}

	ctx := prune.Context{Citations: prune.BuildGraph(records), Now: time.Now()}

	if opts.Source != "" {
		beadStatus, err := loadBeadStatus(context.Background(), opts.Source, opts.Timeout)
		if err != nil {
			return err
		}
		ctx.BeadStatus = beadStatus

		repoFiles, err := loadRepoFiles(context.Background(), opts.Source, opts.Timeout)
		if err != nil {
			return err
		}
		ctx.RepoFiles = repoFiles
	}

	if opts.DocsDir != "" {
		docsText, err := loadDocsText(opts.DocsDir)
		if err != nil {
			return err
		}
		ctx.DocsText = docsText
	}

	f, err := os.Create(opts.Out)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	totals := map[string]int{}
	n := 0
	for _, m := range records {
		if !m.Remembered() {
			continue
		}
		tags := prune.Tag(m.Key, m.Body, ctx)
		row := triageRow{Key: m.Key, Prefix: memory.PrefixOf(m.Key), Tags: tags}
		if err := enc.Encode(row); err != nil {
			return err
		}
		n++
		addTagTotals(totals, tags)
	}

	out := opts.IO.Out
	fmt.Fprintf(out, "wrote %s (%d rows)\n\ntag totals:\n", opts.Out, n)
	names := make([]string, 0, len(totals))
	for name := range totals {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(out, "  %-16s %5d\n", name, totals[name])
	}
	return nil
}

func addTagTotals(totals map[string]int, t prune.Tags) {
	if t.ClosedOnly {
		totals["closed_only"]++
	}
	if t.StatusShape {
		totals["status_shape"]++
	}
	if t.DocsShape {
		totals["docs_shape"]++
	}
	if t.FutureDate {
		totals["future_date"]++
	}
	if t.StaleDate {
		totals["stale_date"]++
	}
	if t.CitesOther {
		totals["cites_other"]++
	}
	if t.DupOfDocs {
		totals["dup_of_docs"]++
	}
	if t.StaleFileRef {
		totals["stale_file_ref"]++
	}
}

var beadStatuses = []string{"open", "in_progress", "blocked", "deferred", "closed"}

// loadBeadStatus mirrors bd_triage.py's _load_bead_status: one `bd list
// --status=<s> --json` call per status, fixed argv (never a shell), same
// stdout/stderr-separated subprocess pattern pkg/cmd/sync uses for `bd
// memories --json`.
func loadBeadStatus(ctx context.Context, dir string, timeout time.Duration) (map[string]string, error) {
	out := map[string]string{}
	for _, status := range beadStatuses {
		raw, err := runInDir(ctx, dir, timeout, "bd", "list", "--status="+status, "--limit=0", "--json")
		if err != nil {
			return nil, err
		}
		var items []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("bd list --status=%s --json (in %s): parse: %w", status, dir, err)
		}
		for _, it := range items {
			out[it.ID] = status
		}
	}
	return out, nil
}

func loadRepoFiles(ctx context.Context, dir string, timeout time.Duration) (map[string]bool, error) {
	raw, err := runInDir(ctx, dir, timeout, "git", "ls-files")
	if err != nil {
		return nil, err
	}
	files := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		if line := scanner.Text(); line != "" {
			files[line] = true
		}
	}
	return files, scanner.Err()
}

func loadDocsText(docsDir string) (string, error) {
	var sb strings.Builder
	err := filepath.WalkDir(docsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".rst" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sb.Write(b)
		sb.WriteByte('\n')
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walking %s: %w", docsDir, err)
	}
	return sb.String(), nil
}

// runInDir runs a fixed-argv command (never a shell -- input-validation
// convention: no exec.Command("sh", "-c", ...) anywhere) in dir, returning
// stdout. stdout/stderr are captured separately, mirroring pkg/cmd/sync's
// liveSnapshot: real evidence elsewhere in this project that a subprocess's
// stderr can't be trusted mixed into stdout.
func runInDir(parent context.Context, dir string, timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %v (in %s): %w (stderr: %s)", name, args, dir, err, stderr.String())
	}
	return stdout.Bytes(), nil
}
