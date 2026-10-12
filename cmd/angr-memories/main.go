package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jcrussell/angr-memories/internal/angrcmd"
	"github.com/jcrussell/angr-memories/internal/atomicfile"
	"github.com/jcrussell/angr-memories/pkg/cmd/root"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
)

// shutdownGrace bounds how long a command may keep running after SIGINT
// before the process is cut off. Long enough for an interrupted embed to
// fsync a ~10 MB embeddings.jsonl.
const shutdownGrace = 20 * time.Second

func main() {
	f := cmdutil.New()

	// SIGINT/SIGTERM cancel the context every command sees as
	// cmd.Context() (byob-lifecycle.2), so network-bound commands (embed,
	// topics suggest) stop cleanly -- including the atomic write that saves
	// their progress, which is why the grace period below is generous.
	// A command that ignores ctx is cut off when the grace runs out, after
	// pending temp files are removed (Go runs no deferred cleanup on an
	// unhandled signal). A second signal terminates immediately.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		stop()
		select {
		case <-done:
		case <-time.After(shutdownGrace):
			atomicfile.CleanupPending()
			os.Exit(130)
		}
	}()

	code := angrcmd.Run(ctx, root.NewCmdRoot(f), os.Args[1:], f.IOStreams)
	close(done)
	atomicfile.CleanupPending()
	os.Exit(code)
}
