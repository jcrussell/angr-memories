package angrcmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jcrussell/angr-memories/pkg/cmdutil"
	"github.com/jcrussell/angr-memories/pkg/iostreams"
)

func TestClassify(t *testing.T) {
	t.Run("nil stays nil", func(t *testing.T) {
		if got := classify(nil); got != nil {
			t.Errorf("classify(nil) = %v, want nil", got)
		}
	})

	t.Run("an existing FlagError passes through unchanged", func(t *testing.T) {
		fe := &cmdutil.FlagError{Err: errors.New("bad flag")}
		got := classify(fe)
		if got != error(fe) {
			t.Errorf("classify(fe) = %v, want the same *FlagError instance", got)
		}
	})

	t.Run("cobra's unknown-command error is wrapped as a FlagError", func(t *testing.T) {
		err := errors.New(`unknown command "bogus" for "angr-memories"`)
		got := classify(err)
		var fe *cmdutil.FlagError
		if !errors.As(got, &fe) {
			t.Fatalf("classify(%v) = %v (%T), want a *cmdutil.FlagError", err, got, got)
		}
	})

	t.Run("cobra's required-flag error is wrapped as a FlagError", func(t *testing.T) {
		err := errors.New(`required flag(s) "source" not set`)
		got := classify(err)
		var fe *cmdutil.FlagError
		if !errors.As(got, &fe) {
			t.Fatalf("classify(%v) = %v (%T), want a *cmdutil.FlagError", err, got, got)
		}
	})

	t.Run("cobra's arg-count error is wrapped as a FlagError", func(t *testing.T) {
		err := errors.New("accepts 1 arg(s), received 0")
		got := classify(err)
		var fe *cmdutil.FlagError
		if !errors.As(got, &fe) {
			t.Fatalf("classify(%v) = %v (%T), want a *cmdutil.FlagError", err, got, got)
		}
	})

	t.Run("an unrelated error is left unchanged", func(t *testing.T) {
		err := errors.New("something else went wrong")
		got := classify(err)
		if got != err {
			t.Errorf("classify(err) = %v, want the same error instance unwrapped", got)
		}
	})
}

func TestRun_exitCodes(t *testing.T) {
	newCmd := func(runE func(*cobra.Command, []string) error) *cobra.Command {
		return &cobra.Command{Use: "x", RunE: runE, SilenceErrors: true, SilenceUsage: true}
	}

	t.Run("success exits 0", func(t *testing.T) {
		io, _, _, _ := iostreams.Test()
		code := Run(context.Background(), newCmd(func(*cobra.Command, []string) error { return nil }), nil, io)
		if code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	})

	t.Run("a generic error exits 1 and prints it", func(t *testing.T) {
		io, _, _, errOut := iostreams.Test()
		code := Run(context.Background(), newCmd(func(*cobra.Command, []string) error { return errors.New("boom") }), nil, io)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(errOut.String(), "boom") {
			t.Errorf("expected the error message on stderr, got: %q", errOut.String())
		}
	})

	t.Run("a FlagError exits 2", func(t *testing.T) {
		io, _, _, _ := iostreams.Test()
		code := Run(context.Background(), newCmd(func(*cobra.Command, []string) error {
			return cmdutil.FlagErrorf("bad flag")
		}), nil, io)
		if code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
	})

	t.Run("ErrCancel exits 2", func(t *testing.T) {
		io, _, _, _ := iostreams.Test()
		code := Run(context.Background(), newCmd(func(*cobra.Command, []string) error { return cmdutil.ErrCancel }), nil, io)
		if code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
	})

	t.Run("ErrSilent exits 1 without printing anything new", func(t *testing.T) {
		io, _, _, errOut := iostreams.Test()
		code := Run(context.Background(), newCmd(func(*cobra.Command, []string) error { return cmdutil.ErrSilent }), nil, io)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if errOut.String() != "" {
			t.Errorf("expected nothing printed for ErrSilent, got: %q", errOut.String())
		}
	})

	t.Run("a missing required positional arg exits 2, not 1", func(t *testing.T) {
		io, _, _, _ := iostreams.Test()
		cmd := newCmd(func(*cobra.Command, []string) error { return nil })
		cmd.Args = cobra.ExactArgs(1)
		code := Run(context.Background(), cmd, nil, io)
		if code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
	})
}

func TestRun_printsHintAfterError(t *testing.T) {
	io, _, _, errOut := iostreams.Test()
	cmd := &cobra.Command{Use: "x", SilenceErrors: true, SilenceUsage: true,
		RunE: func(*cobra.Command, []string) error {
			return cmdutil.WithHint(errors.New("embeddings are stale"), "run `angr-memories embed`")
		}}
	if code := Run(context.Background(), cmd, nil, io); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	got := errOut.String()
	if !strings.Contains(got, "error: embeddings are stale") || !strings.Contains(got, "hint: run `angr-memories embed`") {
		t.Errorf("stderr = %q, want error line followed by hint line", got)
	}
}

func TestRun_contextCanceledExits2(t *testing.T) {
	io, _, _, errOut := iostreams.Test()
	cmd := &cobra.Command{Use: "x", SilenceErrors: true, SilenceUsage: true,
		RunE: func(c *cobra.Command, _ []string) error { return c.Context().Err() }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := Run(ctx, cmd, nil, io); code != 2 {
		t.Errorf("exit code = %d, want 2; stderr=%q", code, errOut.String())
	}
}
