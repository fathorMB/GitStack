package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/backup"
	"github.com/fathorMB/GitStack/admin/internal/config"
)

type stubCluster struct{ git string }

func (s *stubCluster) Replicas(context.Context, string) (int, bool, error) { return 1, true, nil }
func (s *stubCluster) Scale(context.Context, string, int) error            { return nil }
func (s *stubCluster) WaitStopped(context.Context, string) error           { return nil }
func (s *stubCluster) WaitReady(context.Context, string) error             { return nil }
func (s *stubCluster) PGExec(_ context.Context, _ io.Reader, w io.Writer, _ ...string) error {
	_, err := io.WriteString(w, "-- dump\n")
	return err
}
func (s *stubCluster) VolumePath(_ context.Context, pvc string) (string, error) {
	if strings.HasSuffix(pvc, "git-data") {
		return s.git, nil
	}
	return "", backup.ErrNotFound
}
func (s *stubCluster) Secrets(context.Context) ([]backup.Secret, error) { return nil, nil }
func (s *stubCluster) ApplySecret(context.Context, backup.Secret) error { return nil }

func backupApp(t *testing.T, tag string) (*App, string) {
	t.Helper()
	a, _, _ := newApp(&fakeRunner{}, nil)
	git := t.TempDir()
	_ = os.WriteFile(filepath.Join(git, "f"), []byte("x"), 0o600)
	a.NewCluster = func(*config.Config) backup.Cluster { return &stubCluster{git: git} }
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfg, []byte("version: 1\nhost: h\nimage_tag: "+tag+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return a, cfg
}

func TestBackupRestoreCommands(t *testing.T) {
	a, cfg := backupApp(t, "sha-aaa")
	dest := t.TempDir()
	if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", dest}); code != ExitOK {
		t.Fatalf("backup exit %d", code)
	}
	ents, _ := filepath.Glob(filepath.Join(dest, "gitstack-backup-*.tar.gz"))
	if len(ents) != 1 {
		t.Fatalf("archivi: %v", ents)
	}
	// stessa versione: ok, con l'archivio prima o dopo le opzioni
	if code := a.Run(context.Background(), []string{"restore", ents[0], "--config", cfg}); code != ExitOK {
		t.Errorf("restore exit %d", code)
	}
	// versione diversa: rifiutato
	b, errb := func() (*App, string) { return backupApp(t, "sha-bbb") }()
	_ = errb
	var stderr strings.Builder
	b.Stderr = &stderr
	if code := b.Run(context.Background(), []string{"restore", "--config", errb, ents[0]}); code != ExitRefused {
		t.Errorf("restore su versione diversa: exit %d, atteso %d", code, ExitRefused)
	}
	if !strings.Contains(stderr.String(), "versione diversa") {
		t.Errorf("messaggio: %s", stderr.String())
	}
}

func TestRestoreUsageAndRoot(t *testing.T) {
	a, cfg := backupApp(t, "sha-aaa")
	if code := a.Run(context.Background(), []string{"restore", "--config", cfg}); code != ExitUsage {
		t.Errorf("senza archivio: %d", code)
	}
	a.Geteuid = func() int { return 1000 }
	if code := a.Run(context.Background(), []string{"backup", "--config", cfg}); code != ExitNeedsRoot {
		t.Errorf("senza root: %d", code)
	}
}
