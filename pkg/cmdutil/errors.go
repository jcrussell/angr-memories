// Package cmdutil holds the cross-cutting types every command package
// imports: the Factory (dependency bundle) and the error vocabulary
// (FlagError / ErrSilent / ErrCancel) that the top-level runner maps to
// exit codes.
package cmdutil

import "errors"

// FlagError wraps an error reported by flag parsing or argument validation.
// The runner maps this to exit code 2 and prints the wrapped message.
type FlagError struct{ Err error }

func (e *FlagError) Error() string { return e.Err.Error() }
func (e *FlagError) Unwrap() error { return e.Err }

// ErrSilent signals "an error occurred but it has already been printed";
// the runner exits non-zero without printing anything more.
var ErrSilent = errors.New("silent")

// ErrCancel signals user-initiated cancellation (Ctrl-C, prompt abort).
// The runner exits with a distinct code so scripts can tell the difference
// between failure and cancellation.
var ErrCancel = errors.New("cancel")
