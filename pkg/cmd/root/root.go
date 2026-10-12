// Package root assembles the top-level cobra command and wires every
// subcommand.
package root

import (
	"github.com/spf13/cobra"

	applycmd "github.com/jcrussell/angr-memories/pkg/cmd/apply"
	chunkcmd "github.com/jcrussell/angr-memories/pkg/cmd/chunk"
	embedcmd "github.com/jcrussell/angr-memories/pkg/cmd/embed"
	searchcmd "github.com/jcrussell/angr-memories/pkg/cmd/search"
	sitecmd "github.com/jcrussell/angr-memories/pkg/cmd/site"
	statscmd "github.com/jcrussell/angr-memories/pkg/cmd/stats"
	synccmd "github.com/jcrussell/angr-memories/pkg/cmd/sync"
	topicscmd "github.com/jcrussell/angr-memories/pkg/cmd/topics"
	triagecmd "github.com/jcrussell/angr-memories/pkg/cmd/triage"
	validatecmd "github.com/jcrussell/angr-memories/pkg/cmd/validate"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
)

func NewCmdRoot(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   f.ExecutableName,
		Short: "A continuously-synced, browsable archive of the angr memory corpus",
		Long: "angr-memories tracks the angr project's bd-backed memory corpus over " +
			"time: which memories are remembered, which were forgotten (and when, and " +
			"why), grouped by key-prefix family and rendered as a static site.",

		SilenceUsage:  true,
		SilenceErrors: true,

		RunE: func(c *cobra.Command, args []string) error { return c.Help() },
	}
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return &cmdutil.FlagError{Err: err}
	})
	cmd.AddCommand(sitecmd.NewCmdSite(f, nil))
	cmd.AddCommand(statscmd.NewCmdStats(f, nil))
	cmd.AddCommand(synccmd.NewCmdSync(f, nil))
	cmd.AddCommand(searchcmd.NewCmdSearch(f, nil))
	cmd.AddCommand(triagecmd.NewCmdTriage(f, nil))
	cmd.AddCommand(chunkcmd.NewCmdChunk(f, nil))
	cmd.AddCommand(validatecmd.NewCmdValidate(f, nil))
	cmd.AddCommand(applycmd.NewCmdApply(f, nil))
	cmd.AddCommand(embedcmd.NewCmdEmbed(f, nil))
	cmd.AddCommand(topicscmd.NewCmdTopics(f, nil))
	return cmd
}
