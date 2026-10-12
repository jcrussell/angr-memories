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

// shutdownGrace bounds how long a command may keep running after the first
// SIGINT before the process is cut off: long enough for an interrupted
// embed to fsync a ~10 MB embeddings.jsonl, short enough that a command
// which never looks at ctx does not feel stuck (a second Ctrl-C ends it
// immediately anyway).
const shutdownGrace = 10 * time.Second

func main() {
	f := cmdutil.New()

	// The first SIGINT/SIGTERM cancels the context every command sees as
	// cmd.Context() (byob-lifecycle.2), so network-bound commands (embed,
	// topics suggest) stop cleanly -- including the atomic write that saves
	// their progress, which is why there is a grace period. A command that
	// ignores ctx is cut off when the grace runs out; a second signal cuts
	// it off at once. Both exits remove pending temp files first, since Go
	// runs no deferred cleanup on the way out.
	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-sigs:
		case <-done:
			return
		}
		cancel() // first signal: ask the command to stop
		select {
		case <-done: // it finished (and saved its progress) in time
		case <-sigs: // second signal: the user wants out now
			atomicfile.CleanupPending()
			os.Exit(130)
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
