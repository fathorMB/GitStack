package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// build compila gs per goos/goarch con CGO_ENABLED=0 e restituisce il percorso.
func build(t *testing.T, goos, goarch string, ldflags string) string {
	t.Helper()
	name := "gs-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	out := filepath.Join(t.TempDir(), name)
	args := []string{"build", "-trimpath"}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	args = append(args, "-o", out, ".")
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s/%s: %v\n%s", goos, goarch, err, b)
	}
	return out
}

// La versione si inietta con -ldflags "-X main.version=..." (come admin/).
func TestVersioneDaLdflags(t *testing.T) {
	if testing.Short() {
		t.Skip("compila il binario")
	}
	bin := build(t, runtime.GOOS, runtime.GOARCH, "-X main.version=1.2.3-test")
	for _, arg := range []string{"version", "--version"} {
		out, err := exec.Command(bin, arg).Output()
		if err != nil {
			t.Fatalf("gs %s: %v", arg, err)
		}
		if got := strings.TrimSpace(string(out)); got != "gs version 1.2.3-test" {
			t.Errorf("gs %s = %q", arg, got)
		}
	}
	// Senza ldflags vale "dev".
	bin = build(t, runtime.GOOS, runtime.GOARCH, "")
	out, err := exec.Command(bin, "version").Output()
	if err != nil || strings.TrimSpace(string(out)) != "gs version dev" {
		t.Errorf("senza ldflags: %q %v", out, err)
	}
}

// Il binario si compila senza cgo per tutti i target di release.
func TestBuildTuttiITarget(t *testing.T) {
	if testing.Short() {
		t.Skip("compila 6 binari")
	}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			t.Run(goos+"-"+goarch, func(t *testing.T) {
				t.Parallel()
				build(t, goos, goarch, "-s -w -X main.version=sha-test")
			})
		}
	}
}
