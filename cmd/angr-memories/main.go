package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/jcrussell/angr-memories/internal/angrcmd"
	"github.com/jcrussell/angr-memories/internal/memory"
	"github.com/jcrussell/angr-memories/pkg/cmd/root"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
)

func main() {
	f := cmdutil.New()

	// Go doesn't run deferred cleanup (Save's temp file, the search cache's
	// temp build dir) on an unhandled SIGINT/SIGTERM -- without this, a
	// killed write leaves its temp file/dir behind forever.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		memory.CleanupPending()
		os.Exit(130)
	}()

	os.Exit(angrcmd.Run(root.NewCmdRoot(f), os.Args[1:], f.IOStreams))
}
