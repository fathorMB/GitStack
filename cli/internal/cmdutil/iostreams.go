package cmdutil

import (
	"bytes"
	"io"
	"os"

	"golang.org/x/term"
)

// IOStreams sono i flussi di un comando e quello che si sa del terminale.
type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer

	InTTY  bool // stdin è un terminale
	OutTTY bool // stdout è un terminale
	// Width è la larghezza del terminale di stdout; 0 se ignota o non TTY.
	Width int
}

// System costruisce gli IOStreams del processo.
func System() *IOStreams {
	s := &IOStreams{
		In:     os.Stdin,
		Out:    os.Stdout,
		ErrOut: os.Stderr,
		InTTY:  term.IsTerminal(int(os.Stdin.Fd())),
		OutTTY: term.IsTerminal(int(os.Stdout.Fd())),
	}
	if s.OutTTY {
		if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
			s.Width = w
		}
	}
	return s
}

// Test costruisce IOStreams su buffer, non-TTY, per i test.
func Test() (*IOStreams, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer) {
	in, out, errOut := &bytes.Buffer{}, &bytes.Buffer{}, &bytes.Buffer{}
	return &IOStreams{In: in, Out: out, ErrOut: errOut}, in, out, errOut
}
