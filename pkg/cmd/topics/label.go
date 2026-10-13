package topics

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/jcrussell/angr-memories/internal/topics"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

const maxLabelLen = 80

type LabelOptions struct {
	IO             *iostreams.IOStreams
	Now            func() time.Time
	MemoriesPath   string
	EmbeddingsPath string
	LabelsPath     string
	K              int
	MinSize        int
	Index          int
	Text           string
	By             string
}

func NewCmdLabel(f *cmdutil.Factory, runF func(context.Context, *LabelOptions) error) *cobra.Command {
	opts := &LabelOptions{IO: f.IOStreams, Now: time.Now, By: "human"}
	cmd := &cobra.Command{
		Use:   "label <index> <text>",
		Short: "Record a label for one topic cluster",
		Long: `Attach <text> to cluster <index> (the number shown by "angr-memories topics"
and in the /topics/<n>/ URL). The label is stored in topic-labels.jsonl
together with anchor keys derived from the cluster -- its nearest-centre
memories plus a few remembered members -- so it keeps pointing at the same
material when the tree is recomputed. A previous label for the same
material (or with the same text) is replaced. The embedding model is
recorded too: anchors only mean something under the geometry they were
chosen in, so the label is set aside if the vectors are ever regenerated
with a different model. Commit the file afterwards.`,
		Example: `  angr-memories topics label 12 "Register proxy & state sync"`,
		Args:    cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			n, err := strconv.Atoi(args[0])
			if err != nil {
				return cmdutil.FlagErrorf("<index> must be a cluster number, got %q", args[0])
			}
			opts.Index, opts.Text = n, args[1]
			if runF != nil {
				return runF(c.Context(), opts)
			}
			return labelRun(c.Context(), opts)
		},
	}
	addCorpusFlags(cmd, &opts.MemoriesPath, &opts.EmbeddingsPath, &opts.LabelsPath, &opts.K, &opts.MinSize)
	cmd.Flags().StringVar(&opts.By, "by", "human", "who wrote the label (recorded as labeled_by)")
	return cmd
}

func (o *LabelOptions) Validate() error {
	if err := validateCorpusFlags(o.MemoriesPath, o.EmbeddingsPath, o.LabelsPath, o.K, o.MinSize); err != nil {
		return err
	}
	if o.Index < 1 {
		return cmdutil.FlagErrorf("<index> must be >= 1, got %d", o.Index)
	}
	if err := validateLabelText(o.Text); err != nil {
		return cmdutil.FlagErrorf("<text>: %v", err)
	}
	if strings.TrimSpace(o.By) == "" {
		return cmdutil.FlagErrorf("--by must not be empty")
	}
	return nil
}

func validateLabelText(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("label must not be empty")
	}
	if n := utf8.RuneCountInString(s); n > maxLabelLen {
		return fmt.Errorf("label is %d characters, max %d", n, maxLabelLen)
	}
	return nil
}

func labelRun(_ context.Context, opts *LabelOptions) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	c, err := load(opts.IO, opts.MemoriesPath, opts.EmbeddingsPath, opts.LabelsPath, opts.K, opts.MinSize)
	if err != nil {
		return err
	}
	cl, err := c.clusterAt(opts.Index)
	if err != nil {
		return err
	}
	anchors := topics.AnchorsFor(cl, c.res.Docs)
	l := topics.Label{
		Text:       strings.TrimSpace(opts.Text),
		Anchors:    anchors,
		LabeledBy:  opts.By,
		At:         opts.Now().UTC().Format(time.RFC3339),
		EmbedModel: c.model,
	}
	c.labels.Upsert(l, c.currentLabel(opts.Index-1))
	if err := topics.WriteLabels(opts.LabelsPath, c.labels); err != nil {
		return err
	}
	fmt.Fprintf(opts.IO.Out, "labeled cluster %d as %q (%d anchors) -> %s\n", opts.Index, l.Text, len(l.Anchors), opts.LabelsPath)
	return nil
}
