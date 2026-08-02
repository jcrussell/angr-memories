// Package iostreams holds the In/Out/ErrOut bundle every command takes via
// the Factory. Using a struct (rather than reaching for os.Stdin / os.Stdout
// directly) lets tests redirect each stream independently.
package iostreams

import (
	"bytes"
	"io"
	"os"
)

type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer
}

func System() *IOStreams {
	return &IOStreams{
		In:     os.Stdin,
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}
}

// Test returns an IOStreams whose three streams are in-memory buffers, plus
// pointers to those buffers so a test can read what was written.
func Test() (*IOStreams, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer) {
	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &IOStreams{In: in, Out: out, ErrOut: errOut}, in, out, errOut
}
