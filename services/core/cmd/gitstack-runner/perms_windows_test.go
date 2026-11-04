//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestProtectFile: un file leggibile da «Everyone» dopo protectFile non lo è più (GIT-526).
func TestProtectFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("icacls", p, "/grant", "*S-1-1-0:(R)").CombinedOutput(); err != nil { // Everyone
		t.Fatalf("icacls: %v %s", err, out)
	}
	if err := protectFile(p); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("icacls", p).CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "Everyone") || strings.Contains(s, `BUILTIN\Users`) || strings.Contains(s, "Authenticated Users") {
		t.Errorf("il file è ancora leggibile da altri:\n%s", s)
	}
	if _, err := os.ReadFile(p); err != nil {
		t.Errorf("l'utente non legge più il proprio file: %v", err)
	}
}
