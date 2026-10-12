// Package topics implements `angr-memories topics`: list the embedding
// clusters the site's /topics/ page is built from, and record labels for
// them -- by hand (`topics label`) or drafted by a local chat model
// (`topics suggest`). Labels live in topic-labels.jsonl and are committed.
package topics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/jcrussell/angr-memories/internal/topics"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

type Options struct {
	IO             *iostreams.IOStreams
	MemoriesPath   string
	EmbeddingsPath string
	LabelsPath     string
	K              int
	Unlabeled      bool
	JSON           string // comma-separated field list; empty = table
}

func NewCmdTopics(f *cmdutil.Factory, runF func(context.Context, *Options) error) *cobra.Command {
	opts := &Options{IO: f.IOStreams}
	cmd := &cobra.Command{
		Use:   "topics",
		Short: "List the topic clusters and their labels",
		Long: `Cluster the corpus exactly as the site build does (from the committed
embeddings.jsonl) and print one line per cluster: its index (the /topics/<n>/
page number), label, size, kept share, key-prefix mix and the keys nearest
its centre. Clusters without a label show "(unlabeled)".

Labels are data in topic-labels.jsonl, written by a person ("topics label")
or drafted by a local chat model ("topics suggest"). Each label is anchored
to the memories that were in its cluster when it was written, so it follows
them when the tree is recomputed after a sync; a label whose memories no
longer form a cluster is reported as orphaned.

--json takes an explicit field list (for example --json index,label,members)
and prints a JSON array, which is how an agent can label clusters without
reading the site.`,
		Example: `  # what still needs a name
  angr-memories topics --unlabeled

  # machine-readable, with every member key
  angr-memories topics --unlabeled --json index,exemplars,members

  # name cluster 7 by hand; let the local model draft the rest
  angr-memories topics label 7 "Paged memory & permissions"
  angr-memories topics suggest`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if runF != nil {
				return runF(c.Context(), opts)
			}
			return topicsRun(c.Context(), opts)
		},
	}
	addCorpusFlags(cmd, &opts.MemoriesPath, &opts.EmbeddingsPath, &opts.LabelsPath, &opts.K)
	cmd.Flags().BoolVar(&opts.Unlabeled, "unlabeled", false, "only clusters without a label")
	cmd.Flags().StringVar(&opts.JSON, "json", "", "print a JSON array with these comma-separated fields: "+strings.Join(jsonFields, ","))
	cmd.AddCommand(NewCmdLabel(f, nil))
	cmd.AddCommand(NewCmdSuggest(f, nil))
	return cmd
}

func addCorpusFlags(cmd *cobra.Command, mem, emb, labels *string, k *int) {
	cmd.Flags().StringVar(mem, "memories", "memories.jsonl", "path to memories.jsonl")
	cmd.Flags().StringVar(emb, "embeddings", "embeddings.jsonl", "path to the committed embedding vectors")
	cmd.Flags().StringVar(labels, "topic-labels", "topic-labels.jsonl", "path to the topic labels file")
	cmd.Flags().IntVar(k, "topics", topics.DefaultK, "number of clusters (must match the site build)")
}

func validateCorpusFlags(mem, emb, labels string, k int) error {
	if mem == "" || emb == "" || labels == "" {
		return cmdutil.FlagErrorf("--memories, --embeddings and --topic-labels must not be empty")
	}
	if k < 1 {
		return cmdutil.FlagErrorf("--topics must be >= 1, got %d", k)
	}
	return nil
}

// Validate checks flag values before any IO.
func (o *Options) Validate() error {
	if err := validateCorpusFlags(o.MemoriesPath, o.EmbeddingsPath, o.LabelsPath, o.K); err != nil {
		return err
	}
	_, err := parseJSONFields(o.JSON)
	return err
}

func parseJSONFields(spec string) ([]string, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, nil
	}
	valid := map[string]bool{}
	for _, f := range jsonFields {
		valid[f] = true
	}
	var out []string
	for _, f := range strings.Split(spec, ",") {
		f = strings.TrimSpace(f)
		if !valid[f] {
			return nil, cmdutil.FlagErrorf("--json: unknown field %q (valid: %s)", f, strings.Join(jsonFields, ", "))
		}
		out = append(out, f)
	}
	return out, nil
}

func topicsRun(_ context.Context, opts *Options) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	c, err := load(opts.IO, opts.MemoriesPath, opts.EmbeddingsPath, opts.LabelsPath, opts.K)
	if err != nil {
		return err
	}
	c.warnOrphaned(opts.IO)
	rows := c.rows()
	if opts.Unlabeled {
		var keep []row
		for _, r := range rows {
			if r.Unlabeled {
				keep = append(keep, r)
			}
		}
		rows = keep
	}
	fields, _ := parseJSONFields(opts.JSON)
	if fields != nil {
		return writeJSON(opts.IO.Out, rows, fields)
	}
	printTable(opts.IO.Out, rows)
	return nil
}

func writeJSON(w io.Writer, rows []row, fields []string) error {
	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		m := map[string]any{}
		for _, f := range fields {
			switch f {
			case "index":
				m[f] = r.Index
			case "label":
				m[f] = r.Label
			case "unlabeled":
				m[f] = r.Unlabeled
			case "labeled_by":
				m[f] = r.LabeledBy
			case "labeled_at":
				m[f] = r.LabeledAt
			case "size":
				m[f] = r.Size
			case "remembered":
				m[f] = r.Remembered
			case "forgotten":
				m[f] = r.Forgotten
			case "kept":
				m[f] = r.Kept
			case "prefixes":
				m[f] = orEmpty(r.Prefixes)
			case "exemplars":
				m[f] = orEmpty(r.Exemplars)
			case "members":
				m[f] = orEmpty(r.Members)
			}
		}
		out[i] = m
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func printTable(w io.Writer, rows []row) {
	fmt.Fprintf(w, "%4s  %-40s %5s %5s  %-28s %s\n", "#", "label", "n", "kept", "prefixes", "nearest centre")
	for _, r := range rows {
		ex := r.Exemplars
		if len(ex) > 2 {
			ex = ex[:2]
		}
		fmt.Fprintf(w, "%4d  %-40s %5d %4d%%  %-28s %s\n", r.Index, clip(r.Label, 40), r.Size, r.Kept,
			clip(strings.Join(r.Prefixes, ", "), 28), strings.Join(ex, ", "))
	}
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
