//go:build !windows

package config

import (
	"os"
	"testing"
)

func assertPrivate(t *testing.T, path string, dir bool) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	want := os.FileMode(0o600)
	if dir {
		want = 0o700
	}
	if got := st.Mode().Perm(); got != want {
		t.Errorf("%s: permessi %o, attesi %o", path, got, want)
	}
}
