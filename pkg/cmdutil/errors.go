// Package cmdutil holds the cross-cutting types every command package
// imports: the Factory (dependency bundle) and the error vocabulary
// (FlagError / ErrSilent / ErrCancel) that the top-level runner maps to
// exit codes.
package cmdutil

import (
	"errors"
	"fmt"
)

// FlagError wraps an error reported by flag parsing or argument validation.
// The runner maps this to exit code 2 and prints the wrapped message.
type FlagError struct{ Err error }

func (e *FlagError) Error() string { return e.Err.Error() }
func (e *FlagError) Unwrap() error { return e.Err }

// FlagErrorf is the formatted-string convenience form of FlagError, for use
// in an Options.Validate() method: return it before any side effect
// (file/network IO) runs, so a bad flag value fails as a usage error (exit
// code 2) instead of a generic one (exit code 1) partway through work.
func FlagErrorf(format string, args ...any) error {
	return &FlagError{Err: fmt.Errorf(format, args...)}
}

// ErrSilent signals "an error occurred but it has already been printed";
// the runner exits non-zero without printing anything more.
var ErrSilent = errors.New("silent")

// ErrCancel signals user-initiated cancellation (Ctrl-C, prompt abort).
// The runner exits with a distinct code so scripts can tell the difference
// between failure and cancellation.
var ErrCancel = errors.New("cancel")

// ErrHint wraps an error with a short, specific remediation ("run
// `angr-memories embed`", "start Ollama"). The runner prints the error and
// then the hint on its own line (byob-errors.2). Attach one only when the
// fix is concrete; avoid hint spam.
type ErrHint struct {
	Err  error
	Hint string
}

func (e *ErrHint) Error() string { return e.Err.Error() }
func (e *ErrHint) Unwrap() error { return e.Err }

// WithHint attaches hint to err. A nil err stays nil.
func WithHint(err error, hint string) error {
	if err == nil {
		return nil
	}
	return &ErrHint{Err: err, Hint: hint}
}
