// Package site implements `angr-memories site`: render memories.jsonl as a
// browsable static site under -out.
package site

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jcrussell/angr-memories/internal/embeddings"
	"github.com/jcrussell/angr-memories/internal/memory"
	sitepkg "github.com/jcrussell/angr-memories/internal/site"
	"github.com/jcrussell/angr-memories/internal/topics"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

type Options struct {
	IO             *iostreams.IOStreams
	MemoriesPath   string
	EmbeddingsPath string
	LabelsPath     string
	OutDir         string
	BaseURL        string
	RepoURL        string
	TopicK         int
	TopicMinSize   int
	AllowStale     bool
}

func NewCmdSite(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{IO: f.IOStreams}
	cmd := &cobra.Command{
		Use:   "site",
		Short: "Render memories.jsonl as a static site",
		Long: `Walk memories.jsonl, group records by key-prefix family (splitting large
families into several pages), render markdown bodies with goldmark + Chroma,
and write one index.html per route.

The /topics/ section clusters every memory by meaning using the committed
embedding vectors in embeddings.jsonl (see "angr-memories embed") and the
labels in topic-labels.jsonl (see "angr-memories topics"). The build is
offline: it never contacts a model server. If embeddings.jsonl is missing
the topics page renders an empty state; if it exists but lags behind
memories.jsonl the build fails, so a stale tree is never published. Pass
--allow-stale-embeddings to build anyway for local experiments, leaving the
out-of-date keys out of the clustering.`,
		Example: `  # local preview (open _site/index.html in a browser)
  angr-memories site

  # what the Pages workflow runs
  angr-memories site --base-url /angr-memories --repo-url https://github.com/jcrussell/angr-memories`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if runF != nil {
				return runF(opts)
			}
			return siteRun(opts)
		},
	}
	cmd.Flags().StringVar(&opts.MemoriesPath, "memories", "memories.jsonl", "path to memories.jsonl")
	cmd.Flags().StringVar(&opts.EmbeddingsPath, "embeddings", "embeddings.jsonl", "path to the committed embedding vectors")
	cmd.Flags().StringVar(&opts.LabelsPath, "topic-labels", "topic-labels.jsonl", "path to the topic labels file")
	cmd.Flags().StringVar(&opts.OutDir, "out", "_site", "output directory")
	cmd.Flags().StringVar(&opts.BaseURL, "base-url", "", "URL prefix for absolute links")
	cmd.Flags().StringVar(&opts.RepoURL, "repo-url", "https://github.com/jcrussell/angr-memories", "repo URL for the header link")
	cmd.Flags().IntVar(&opts.TopicK, "topics", 0, "fixed number of topic clusters (0 = as many as --min-topic-size allows)")
	cmd.Flags().IntVar(&opts.TopicMinSize, "min-topic-size", 0, fmt.Sprintf("smallest topic the automatic cut may produce (0 = default %d; not with --topics)", topics.DefaultMinSize))
	cmd.Flags().BoolVar(&opts.AllowStale, "allow-stale-embeddings", false, "build even if embeddings.jsonl lags memories.jsonl (stale keys are left out of topics)")
	return cmd
}

// Validate checks flag values before any filesystem work.
func (o *Options) Validate() error {
	if o.MemoriesPath == "" || o.OutDir == "" {
		return cmdutil.FlagErrorf("--memories and --out must not be empty")
	}
	if o.TopicK < 0 {
		return cmdutil.FlagErrorf("--topics must be >= 0 (0 = automatic), got %d", o.TopicK)
	}
	if o.TopicMinSize < 0 {
		return cmdutil.FlagErrorf("--min-topic-size must be >= 0 (0 = default %d), got %d", topics.DefaultMinSize, o.TopicMinSize)
	}
	if o.TopicK > 0 && o.TopicMinSize > 0 {
		return cmdutil.FlagErrorf("--topics and --min-topic-size are different ways to decide how many topics there are; give one")
	}
	return nil
}

func siteRun(opts *Options) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	records, err := memory.Load(opts.MemoriesPath)
	if err != nil {
		return err
	}
	var wo sitepkg.Options
	if opts.EmbeddingsPath != "" {
		o, err := topicOptions(opts, records)
		if err != nil {
			return err
		}
		if o != nil {
			wo = *o
		}
	}
	s, err := sitepkg.WalkRecords(records, wo)
	if err != nil {
		return err
	}
	s.BaseURL = opts.BaseURL
	s.RepoURL = opts.RepoURL
	return sitepkg.Render(s, opts.OutDir)
}

// topicOptions loads the embeddings and labels and applies the staleness
// gate. A nil result (no error) means "no topics section".
func topicOptions(opts *Options, records []*memory.Memory) (*sitepkg.Options, error) {
	f, err := embeddings.Load(opts.EmbeddingsPath)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(opts.IO.ErrOut, "note: %s not found; the topics page will be empty (run `angr-memories embed` to create it)\n", opts.EmbeddingsPath)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st := embeddings.Check(records, f)
	if !st.Clean() {
		msg := fmt.Sprintf("%s is out of date: %d missing, %d stale of %d keys (%s)",
			opts.EmbeddingsPath, len(st.Missing), len(st.Stale), st.Current+len(st.Missing)+len(st.Stale), preview(append(st.Missing, st.Stale...)))
		if !opts.AllowStale {
			return nil, cmdutil.WithHint(errors.New(msg), "run `angr-memories embed` and commit embeddings.jsonl, or pass --allow-stale-embeddings to build without those keys")
		}
		fmt.Fprintln(opts.IO.ErrOut, "warning:", msg, "-- building without them")
	}
	labels, err := topics.LoadLabels(opts.LabelsPath)
	if err != nil {
		return nil, err
	}
	return &sitepkg.Options{
		TopicK:       opts.TopicK,
		TopicMinSize: opts.TopicMinSize,
		Labels:       labels.Labels,
		Vectors:      embeddings.Vectors(records, f, true),
		Model:        f.Header.Model,
	}, nil
}

func preview(keys []string) string {
	const n = 20
	if len(keys) > n {
		return strings.Join(keys[:n], ", ") + fmt.Sprintf(", … %d more", len(keys)-n)
	}
	return strings.Join(keys, ", ")
}
