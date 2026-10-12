package topics

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jcrussell/angr-memories/internal/chart"
	"github.com/jcrussell/angr-memories/internal/ollamaclient"
	"github.com/jcrussell/angr-memories/internal/topics"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

// Suggester is the narrow seam this command needs from a chat model.
type Suggester interface {
	Suggest(ctx context.Context, b ollamaclient.ClusterBrief) (string, error)
}

type SuggestOptions struct {
	IO         *iostreams.IOStreams
	HTTPClient func() *http.Client
	Connect    func(ctx context.Context, opts *SuggestOptions) (Suggester, error)
	Now        func() time.Time

	MemoriesPath   string
	EmbeddingsPath string
	LabelsPath     string
	K              int
	Endpoint       string
	ChatModel      string
	Only           int
	Relabel        bool
	DryRun         bool
}

const (
	DefaultChatModel = "llama3.2"
	excerptChars     = 300
	excerptCount     = 8
	exampleCount     = 6
)

func NewCmdSuggest(f *cmdutil.Factory, runF func(context.Context, *SuggestOptions) error) *cobra.Command {
	opts := &SuggestOptions{IO: f.IOStreams, HTTPClient: f.HTTPClient, Connect: connectOllama, Now: time.Now}
	cmd := &cobra.Command{
		Use:   "suggest",
		Short: "Draft labels for unlabeled clusters with a local chat model",
		Long: `For every cluster without a label, show a chat model served by local Ollama
the cluster's member keys and a few excerpts, and ask for a short label in
the style of the existing ones. Accepted suggestions are written to
topic-labels.jsonl as labeled_by "llm:<model>"; review them with
"angr-memories topics" or on the site, and overrule any with "topics label",
which always wins.

A suggestion is rejected (and retried once asking for something more
specific) when it duplicates an existing label. --relabel also redoes
clusters whose current label came from a model; --only targets one cluster
regardless of its state. The site build never calls a model: run this, look
at the result, commit.`,
		Example: `  ollama pull llama3.2
  angr-memories topics suggest --dry-run     # show drafts, write nothing
  angr-memories topics suggest               # write drafts for unlabeled clusters
  angr-memories topics suggest --only 7      # redo one cluster`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if runF != nil {
				return runF(c.Context(), opts)
			}
			return suggestRun(c.Context(), opts)
		},
	}
	addCorpusFlags(cmd, &opts.MemoriesPath, &opts.EmbeddingsPath, &opts.LabelsPath, &opts.K)
	cmd.Flags().StringVar(&opts.Endpoint, "endpoint", ollamaclient.EndpointFromEnv(), "Ollama server URL")
	cmd.Flags().StringVar(&opts.ChatModel, "chat-model", DefaultChatModel, "chat model name (as `ollama pull` knows it)")
	cmd.Flags().IntVar(&opts.Only, "only", 0, "suggest for this cluster index only")
	cmd.Flags().BoolVar(&opts.Relabel, "relabel", false, "also redo clusters whose label was written by a model (with --only: allow replacing a person's label)")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print suggestions without writing the labels file")
	return cmd
}

func (o *SuggestOptions) Validate() error {
	if err := validateCorpusFlags(o.MemoriesPath, o.EmbeddingsPath, o.LabelsPath, o.K); err != nil {
		return err
	}
	if _, err := ollamaclient.ParseEndpoint(o.Endpoint); err != nil {
		return cmdutil.FlagErrorf("--endpoint: %v", err)
	}
	if strings.TrimSpace(o.ChatModel) == "" {
		return cmdutil.FlagErrorf("--chat-model must not be empty")
	}
	if o.Only < 0 {
		return cmdutil.FlagErrorf("--only must be a cluster index >= 1 (0 = every unlabeled cluster), got %d", o.Only)
	}
	return nil
}

func connectOllama(ctx context.Context, opts *SuggestOptions) (Suggester, error) {
	c, err := ollamaclient.Connect(ctx, opts.Endpoint, opts.HTTPClient())
	if err != nil {
		return nil, err
	}
	return &ollamaclient.Labeler{Client: c, Model: opts.ChatModel, Endpoint: opts.Endpoint}, nil
}

func suggestRun(ctx context.Context, opts *SuggestOptions) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	c, err := load(opts.IO, opts.MemoriesPath, opts.EmbeddingsPath, opts.LabelsPath, opts.K)
	if err != nil {
		return err
	}
	c.warnOrphaned(opts.IO)

	// Which clusters to draft for.
	var targets []int // positions in c.res.Clusters
	if opts.Only > 0 {
		if _, err := c.clusterAt(opts.Only); err != nil {
			return err
		}
		if a := c.assigned[opts.Only-1]; a.Label != nil && !strings.HasPrefix(a.Label.LabeledBy, "llm:") && !opts.Relabel && !opts.DryRun {
			return cmdutil.WithHint(
				fmt.Errorf("cluster %d already has a label written by %s: %q", opts.Only, a.Label.LabeledBy, a.Label.Text),
				"a person's label is not replaced by a draft; pass --relabel as well to overwrite it, or --dry-run to only see the suggestion")
		}
		targets = []int{opts.Only - 1}
	} else {
		for i := range c.res.Clusters {
			a := c.assigned[i]
			if a.Label == nil || (opts.Relabel && strings.HasPrefix(a.Label.LabeledBy, "llm:")) {
				targets = append(targets, i)
			}
		}
	}
	if len(targets) == 0 {
		fmt.Fprintln(opts.IO.ErrOut, "every cluster already has a label; use --relabel or --only to redo one")
		return nil
	}

	s, err := opts.Connect(ctx, opts)
	if err != nil {
		return err
	}
	taken := c.takenLabels()
	examples := c.exampleLabels()
	current := make([]string, len(c.res.Clusters))
	for i := range c.res.Clusters {
		current[i] = c.currentLabel(i)
	}
	by := "llm:" + opts.ChatModel
	drafted := 0
	var results []row
	for n, i := range targets {
		cl := c.res.Clusters[i]
		anchors := topics.AnchorsFor(cl, c.res.Docs)
		brief := c.brief(cl, examples)
		r := c.row(i)
		delete(taken, normLabel(current[i])) // re-proposing the cluster's own label is not a clash
		text, err := suggestOne(ctx, s, brief, taken)
		if err != nil {
			if current[i] != "" {
				taken[normLabel(current[i])] = true
			}
			if ctx.Err() != nil {
				break
			}
			fmt.Fprintf(opts.IO.ErrOut, "cluster %d: skipped (%v)\n", cl.Index, err)
			continue
		}
		fmt.Fprintf(opts.IO.ErrOut, "cluster %d (%d/%d): %q\n", cl.Index, n+1, len(targets), text)
		taken[normLabel(text)] = true
		l := topics.Label{Text: text, Anchors: anchors, LabeledBy: by, At: opts.Now().UTC().Format(time.RFC3339)}
		c.labels.Upsert(l, current[i])
		drafted++
		r.Label, r.Unlabeled, r.LabeledBy, r.LabeledAt = text, false, by, l.At
		results = append(results, r)
	}
	if drafted > 0 && !opts.DryRun {
		if err := topics.WriteLabels(opts.LabelsPath, c.labels); err != nil {
			return err
		}
	}
	printTable(opts.IO.Out, results)
	switch {
	case ctx.Err() != nil:
		fmt.Fprintf(opts.IO.ErrOut, "interrupted after %d of %d; %s\n", drafted, len(targets), savedNote(opts, drafted))
		return cmdutil.ErrCancel
	case opts.DryRun:
		fmt.Fprintf(opts.IO.ErrOut, "dry run: %d suggestion(s) shown, nothing written\n", drafted)
	default:
		fmt.Fprintf(opts.IO.ErrOut, "wrote %d label(s) to %s; review with `angr-memories topics` and commit\n", drafted, opts.LabelsPath)
	}
	return nil
}

func savedNote(opts *SuggestOptions, drafted int) string {
	if opts.DryRun || drafted == 0 {
		return "nothing written"
	}
	return "drafts so far saved to " + opts.LabelsPath
}

// suggestOne asks once, and once more with the clash named if the answer
// duplicates an existing label.
func suggestOne(ctx context.Context, s Suggester, b ollamaclient.ClusterBrief, taken map[string]bool) (string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := s.Suggest(ctx, b)
		if err != nil {
			return "", err
		}
		text := cleanLabel(raw)
		if err := validateLabelText(text); err != nil {
			return "", fmt.Errorf("model answered %q: %v", raw, err)
		}
		if !taken[normLabel(text)] {
			return text, nil
		}
		b.Avoid = append(b.Avoid, text)
	}
	return "", fmt.Errorf("model kept proposing a label already in use (%s)", strings.Join(b.Avoid, "; "))
}

// cleanLabel strips the decorations small models like to add.
func cleanLabel(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"'“”‘’`")
	s = strings.TrimRight(s, ".")
	return strings.Join(strings.Fields(s), " ")
}

// brief packs what the model sees for one cluster.
func (c *corpus) brief(cl topics.Cluster, examples []string) ollamaclient.ClusterBrief {
	b := ollamaclient.ClusterBrief{Size: len(cl.Members), Remembered: cl.Remembered, Examples: examples}
	for _, p := range cl.Prefixes {
		b.Prefixes = append(b.Prefixes, fmt.Sprintf("%s %d", p.Prefix, p.Count))
	}
	for _, di := range cl.Members {
		b.Keys = append(b.Keys, c.res.Docs[di].Key)
	}
	seen := map[int]bool{}
	for _, di := range append(append([]int(nil), cl.Exemplars...), cl.Members...) {
		if len(b.Excerpts) >= excerptCount {
			break
		}
		if seen[di] {
			continue
		}
		seen[di] = true
		key := c.res.Docs[di].Key
		body := ""
		if m := c.latest[key]; m != nil {
			body = m.Body
		}
		b.Excerpts = append(b.Excerpts, key+": "+excerpt(body))
	}
	return b
}

func excerpt(body string) string {
	return chart.Truncate(strings.Join(strings.Fields(body), " "), excerptChars+1)
}

// exampleLabels picks a few existing labels to show the style, human ones first.
func (c *corpus) exampleLabels() []string {
	var human, llm []string
	for _, l := range c.labels.Labels {
		if strings.HasPrefix(l.LabeledBy, "llm:") {
			llm = append(llm, l.Text)
		} else {
			human = append(human, l.Text)
		}
	}
	sort.Strings(human)
	sort.Strings(llm)
	all := append(human, llm...)
	if len(all) > exampleCount {
		all = all[:exampleCount]
	}
	return all
}
