package main

import (
	"os"

	"github.com/jcrussell/angr-memories/internal/angrcmd"
	"github.com/jcrussell/angr-memories/pkg/cmd/root"
	"github.com/jcrussell/angr-memories/pkg/cmdutil"
)

func main() {
	f := cmdutil.New()
	os.Exit(angrcmd.Run(root.NewCmdRoot(f), os.Args[1:], f.IOStreams))
}
