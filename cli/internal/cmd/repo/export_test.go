package repo

import (
	"context"
	"io"
)

// SetOpenURL sostituisce l'apertura del browser nei test.
func SetOpenURL(fn func(string) error) func() {
	old := openURL
	openURL = fn
	return func() { openURL = old }
}

// SetRunGit sostituisce l'esecuzione di git nei test.
func SetRunGit(fn func(ctx context.Context, in io.Reader, out, errOut io.Writer, args ...string) error) func() {
	old := runGit
	runGit = fn
	return func() { runGit = old }
}
