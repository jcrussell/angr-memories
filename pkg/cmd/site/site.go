// Package site implements `angr-memories site`: render memories.jsonl as a
// browsable static site under -out.
package site

import (
	"github.com/spf13/cobra"

	sitepkg "github.com/jcrussell/angr-memories/internal/site"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

type Options struct {
	IO           *iostreams.IOStreams
	MemoriesPath string
	OutDir       string
	BaseURL      string
	RepoURL      string
}

func NewCmdSite(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	opts := &Options{IO: f.IOStreams}
	cmd := &cobra.Command{
		Use:   "site",
		Short: "Render memories.jsonl as a static site",
		Long: "Walks memories.jsonl, groups records by key-prefix family (splitting " +
			"large families into several pages), renders markdown bodies with " +
			"goldmark + Chroma, and writes one index.html per route.",
		RunE: func(c *cobra.Command, args []string) error {
			if runF != nil {
				return runF(opts)
			}
			return siteRun(opts)
		},
	}
	cmd.Flags().StringVar(&opts.MemoriesPath, "memories", "memories.jsonl", "path to memories.jsonl")
	cmd.Flags().StringVar(&opts.OutDir, "out", "_site", "output directory")
	cmd.Flags().StringVar(&opts.BaseURL, "base-url", "", "URL prefix for absolute links")
	cmd.Flags().StringVar(&opts.RepoURL, "repo-url", "https://github.com/jcrussell/angr-memories", "repo URL for the header link")
	return cmd
}

func siteRun(opts *Options) error {
	s, err := sitepkg.Walk(opts.MemoriesPath)
	if err != nil {
		return err
	}
	s.BaseURL = opts.BaseURL
	s.RepoURL = opts.RepoURL
	return sitepkg.Render(s, opts.OutDir)
}
