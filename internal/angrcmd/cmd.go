// Package angrcmd is the middle tier between cmd/angr-memories/main.go and
// the per-verb command packages: it owns the error→exit-code mapping.
package angrcmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

// Run executes root with args under ctx and maps the resulting error to an
// exit code. Commands return errors, never call os.Exit. ctx is the
// process-level context main wires to SIGINT/SIGTERM (byob-lifecycle.1/.2);
// cobra hands it to every RunE as c.Context().
func Run(ctx context.Context, root *cobra.Command, args []string, ios *iostreams.IOStreams) int {
	root.SetArgs(args)
	root.SetIn(ios.In)
	root.SetOut(ios.Out)
	root.SetErr(ios.ErrOut)

	err := classify(root.ExecuteContext(ctx))
	var code int
	switch {
	case err == nil:
		return 0
	case errors.Is(err, cmdutil.ErrCancel), errors.Is(err, context.Canceled):
		return 2
	case errors.Is(err, cmdutil.ErrSilent):
		return 1
	case errors.As(err, new(*cmdutil.FlagError)):
		fmt.Fprintln(ios.ErrOut, "error:", err)
		code = 2
	default:
		fmt.Fprintln(ios.ErrOut, "error:", err)
		code = 1
	}
	var h *cmdutil.ErrHint
	if errors.As(err, &h) && h.Hint != "" {
		fmt.Fprintln(ios.ErrOut, "hint:", h.Hint)
	}
	return code
}

// classify wraps cobra-emitted "unknown command", "required flag(s) ...",
// and "accepts N arg(s)..." errors as FlagError so they exit 2 alongside
// flag-parse errors. None of these have a public sentinel to match on
// (MarkFlagRequired's violation, e.g. `sync` without --source; an unknown
// subcommand; or an Args validator like ExactArgs rejecting the wrong
// positional arg count, e.g. `search` with no query) -- match on the
// message prefixes cobra emits in (*Command).ValidateRequiredFlags,
// (*Command).Find, and (*Command).ValidateArgs respectively.
func classify(err error) error {
	if err == nil {
		return nil
	}
	var fe *cmdutil.FlagError
	if errors.As(err, &fe) {
		return err
	}
	if strings.HasPrefix(err.Error(), "unknown command ") ||
		strings.HasPrefix(err.Error(), "required flag(s) ") ||
		strings.HasPrefix(err.Error(), "accepts ") {
		return &cmdutil.FlagError{Err: err}
	}
	return err
}
