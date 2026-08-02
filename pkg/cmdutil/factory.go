package cmdutil

import "github.com/jcrussell/angr-memories/pkg/iostreams"

// Factory bundles cross-cutting dependencies. Built once in main(),
// threaded into every NewCmdXxx constructor.
type Factory struct {
	IOStreams      *iostreams.IOStreams
	ExecutableName string
}

func New() *Factory {
	return &Factory{
		IOStreams:      iostreams.System(),
		ExecutableName: "angr-memories",
	}
}
