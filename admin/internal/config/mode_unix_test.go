//go:build unix

package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadModeRootOnly(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err != nil {
		t.Fatalf("0600 deve passare: %v", err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); !errors.Is(err, ErrInvalid) {
		t.Fatalf("0644 deve essere rifiutato, ottenuto %v", err)
	}
}
