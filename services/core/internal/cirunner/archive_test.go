package cirunner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestExtractTarGzModes verifica che i modi siano quelli di git checkout (0644/0755), così
// un Dockerfile con USER non root può leggere i file copiati (GIT-511).
func TestExtractTarGzModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("i bit di permesso Unix non sono verificabili su Windows")
	}
	defer setUmask(0o022)()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	entries := []struct {
		name string
		mode int64
		dir  bool
		body string
	}{
		{"repo-abc/", 0o755, true, ""},
		{"repo-abc/server.cjs", 0o644, false, "x"},
		{"repo-abc/run.sh", 0o755, false, "#!/bin/sh\n"},
		{"repo-abc/sub/", 0o755, true, ""},
		{"repo-abc/sub/a.txt", 0o644, false, "a"},
	}
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: e.mode, Typeflag: tar.TypeReg, Size: int64(len(e.body))}
		if e.dir {
			h.Typeflag = tar.TypeDir
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "ws")
	if err := extractTarGz(&buf, dest); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{
		"server.cjs": 0o644,
		"run.sh":     0o755,
		"sub":        0o755,
		"sub/a.txt":  0o644,
	} {
		fi, err := os.Stat(filepath.Join(dest, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s: modo %o, atteso %o", p, got, want)
		}
	}
}
