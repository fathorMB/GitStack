package backupstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteRead(t *testing.T) {
	dir := t.TempDir()
	state := State{
		Success: true,
		Path:    "/var/backups/gitstack/gitstack-backup-20250101T120000Z-sha-abc.tar.gz",
		At:      time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
	}
	if err := Write(dir, state); err != nil {
		t.Fatal(err)
	}
	got := Read(dir)
	if !got.Success {
		t.Errorf("Success: wanted true, got %v", got.Success)
	}
	if got.Path != state.Path {
		t.Errorf("Path: wanted %q, got %q", state.Path, got.Path)
	}
	if got.At.IsZero() {
		t.Error("At: zero time")
	}
}

func TestWriteReadError(t *testing.T) {
	dir := t.TempDir()
	state := State{
		Success: false,
		Error:   "cluster non interrogabile",
		At:      time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
	}
	if err := Write(dir, state); err != nil {
		t.Fatal(err)
	}
	got := Read(dir)
	if got.Success {
		t.Error("Success: wanted false")
	}
	if got.Error != "cluster non interrogabile" {
		t.Errorf("Error: wanted %q, got %q", "cluster non interrogabile", got.Error)
	}
}

func TestReadMissing(t *testing.T) {
	dir := t.TempDir()
	got := Read(dir)
	if got.Success {
		t.Error("wanted zero state")
	}
}

func TestStatePath(t *testing.T) {
	got := StatePath("/etc/gitstack")
	// Su Windows filepath.Join usa \, quindi controlliamo che finisca
	// correttamente e che il file di stato esista nel nome.
	if !strings.HasSuffix(got, filepath.FromSlash("backup-state.json")) {
		t.Errorf("StatePath: got %q, volevo un path che finisce con %q", got, "backup-state.json")
	}
}

func TestWriteAtomicity(t *testing.T) {
	dir := t.TempDir()
	// Write una volta
	state := State{Success: true, Path: "/tmp/x.tar.gz"}
	if err := Write(dir, state); err != nil {
		t.Fatal(err)
	}
	// Verifica che non ci sia più il .tmp
	_, err := os.Stat(filepath.Join(dir, "backup-state.json.tmp"))
	if !os.IsNotExist(err) {
		t.Error("il file .tmp non è stato rimosso")
	}
	// Verifica che esista il file principale
	if _, err := os.Stat(filepath.Join(dir, "backup-state.json")); os.IsNotExist(err) {
		t.Error("il file principale non esiste")
	}
}
