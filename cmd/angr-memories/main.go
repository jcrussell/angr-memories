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

func main() {
	f := cmdutil.New()

	// SIGINT/SIGTERM cancel the context every command sees as
	// cmd.Context() (byob-lifecycle.2), so network-bound commands (embed,
	// topics suggest) stop cleanly. Commands that don't watch ctx get a
	// short grace period, after which pending temp files are removed and
	// the process exits -- Go runs no deferred cleanup on an unhandled
	// signal, so a killed write would otherwise leave its temp file behind.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop() // a second signal now terminates immediately, as usual
		time.Sleep(2 * time.Second)
		atomicfile.CleanupPending()
		os.Exit(130)
	}()

	code := angrcmd.Run(ctx, root.NewCmdRoot(f), os.Args[1:], f.IOStreams)
	atomicfile.CleanupPending()
	os.Exit(code)
}
