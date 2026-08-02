// Package root assembles the top-level cobra command and wires every
// subcommand.
package root

import (
	"github.com/spf13/cobra"

	sitecmd "github.com/jcrussell/angr-memories/pkg/cmd/site"
	statscmd "github.com/jcrussell/angr-memories/pkg/cmd/stats"
	synccmd "github.com/jcrussell/angr-memories/pkg/cmd/sync"
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
	return cmd
}
