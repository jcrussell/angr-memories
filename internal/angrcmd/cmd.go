// Package angrcmd is the middle tier between cmd/angr-memories/main.go and
// the per-verb command packages: it owns the error→exit-code mapping.
package angrcmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

// Run executes root with args and maps the resulting error to an exit
// code. Commands return errors, never call os.Exit.
func Run(root *cobra.Command, args []string, ios *iostreams.IOStreams) int {
	root.SetArgs(args)
	root.SetIn(ios.In)
	root.SetOut(ios.Out)
	root.SetErr(ios.ErrOut)

	err := classify(root.Execute())
	switch {
	case err == nil:
		return 0
	case errors.Is(err, cmdutil.ErrCancel):
		return 2
	case errors.Is(err, cmdutil.ErrSilent):
		return 1
	case errors.As(err, new(*cmdutil.FlagError)):
		fmt.Fprintln(ios.ErrOut, "error:", err)
		return 2
	default:
		fmt.Fprintln(ios.ErrOut, "error:", err)
		return 1
	}
}

// classify wraps cobra-emitted "unknown command" and "required flag(s) ..."
// errors as FlagError so they exit 2 alongside flag-parse errors.
// MarkFlagRequired's violation (e.g. `sync` without --source) has no
// public sentinel either, same as unknown-command -- match on the message
// prefix cobra emits in (*Command).ValidateRequiredFlags.
func classify(err error) error {
	if err == nil {
		return nil
	}
	var fe *cmdutil.FlagError
	if errors.As(err, &fe) {
		return err
	}
	if strings.HasPrefix(err.Error(), "unknown command ") ||
		strings.HasPrefix(err.Error(), "required flag(s) ") {
		return &cmdutil.FlagError{Err: err}
	}
	return err
}
