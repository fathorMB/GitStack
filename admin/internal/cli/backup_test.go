package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/backup"
	"github.com/fathorMB/GitStack/admin/internal/backupstate"
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
	if code := a.Run(context.Background(), []string{"restore", ents[0], "--config", cfg, "--dest", dest}); code != ExitOK {
		t.Errorf("restore exit %d", code)
	}
	// versione diversa: rifiutato
	b, errb := func() (*App, string) { return backupApp(t, "sha-bbb") }()
	_ = errb
	var stderr strings.Builder
	b.Stderr = &stderr
	if code := b.Run(context.Background(), []string{"restore", "--config", errb, "--dest", dest, ents[0]}); code != ExitRefused {
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

// failCluster è un cluster che fallisce sempre (errore su Replicas).
type failCluster struct{}

func (f *failCluster) Replicas(_ context.Context, _ string) (int, bool, error) {
	return 0, false, backup.ErrNotFound
}
func (f *failCluster) Scale(_ context.Context, _ string, _ int) error { return nil }
func (f *failCluster) WaitStopped(_ context.Context, _ string) error  { return nil }
func (f *failCluster) WaitReady(_ context.Context, _ string) error    { return nil }
func (f *failCluster) PGExec(_ context.Context, _ io.Reader, w io.Writer, _ ...string) error {
	return nil
}
func (f *failCluster) VolumePath(_ context.Context, _ string) (string, error) {
	return "", backup.ErrNotFound
}
func (f *failCluster) Secrets(_ context.Context) ([]backup.Secret, error)   { return nil, nil }
func (f *failCluster) ApplySecret(_ context.Context, _ backup.Secret) error { return nil }

func TestBackupRetention(t *testing.T) {
	a, cfg := backupApp(t, "sha-aaa")
	dest := t.TempDir()
	// Due backup con retention=1 → resta uno.
	for i := 0; i < 2; i++ {
		git := t.TempDir()
		_ = os.WriteFile(filepath.Join(git, "f"), []byte("x"), 0o600)
		a.NewCluster = func(*config.Config) backup.Cluster { return &stubCluster{git: git} }
		if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", dest, "--retention", "1"}); code != ExitOK {
			t.Fatalf("backup %d: exit %d", i+1, code)
		}
	}
	ents, _ := filepath.Glob(filepath.Join(dest, "gitstack-backup-*.tar.gz"))
	if len(ents) != 1 {
		t.Errorf("dopo due backup con retention=1: %d archivi, volevo 1", len(ents))
	}
}

func TestBackupRetentionNoPruneOnFail(t *testing.T) {
	a, cfg := backupApp(t, "sha-aaa")
	dest := t.TempDir()
	// Backup riuscito.
	git := t.TempDir()
	_ = os.WriteFile(filepath.Join(git, "f"), []byte("x"), 0o600)
	a.NewCluster = func(*config.Config) backup.Cluster { return &stubCluster{git: git} }
	if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", dest, "--retention", "2"}); code != ExitOK {
		t.Fatalf("backup 1: exit %d", code)
	}
	// Simulo un backup fallito (cluster non disponibile): non deve cancellare.
	a.NewCluster = func(*config.Config) backup.Cluster { return &failCluster{} }
	// Backup fallito: l'exit code è ExitUnexpected (70, errore generico).
	if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", dest}); code != ExitUnexpected {
		t.Fatalf("backup 2: exit %d", code)
	}
	// Verifico che sia rimasto un solo backup (nessuna cancellazione).
	ents, _ := filepath.Glob(filepath.Join(dest, "gitstack-backup-*.tar.gz"))
	if len(ents) != 1 {
		t.Errorf("dopo backup fallito: %d archivi, volevo 1", len(ents))
	}
}

func TestBackupStateFile(t *testing.T) {
	a, cfg := backupApp(t, "sha-aaa")
	dest := t.TempDir()
	configDir := filepath.Dir(cfg)
	git := t.TempDir()
	_ = os.WriteFile(filepath.Join(git, "f"), []byte("x"), 0o600)
	a.NewCluster = func(*config.Config) backup.Cluster { return &stubCluster{git: git} }
	if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", dest}); code != ExitOK {
		t.Fatalf("backup exit %d", code)
	}
	// Lo stato deve esistere e riportare successo.
	statePath := filepath.Join(configDir, "backup-state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("leggere stato: %v", err)
	}
	var s backupstate.State
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("parse stato: %v", err)
	}
	if !s.Success {
		t.Errorf("stato: wanted success=true, got false")
	}
	if s.At.IsZero() {
		t.Error("stato: At zero")
	}
	// Backup fallito: lo stato deve riportare errore.
	a.NewCluster = func(*config.Config) backup.Cluster { return &failCluster{} }
	if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", dest}); code != ExitUnexpected {
		t.Fatalf("backup fallito: exit %d", code)
	}
	data, err = os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("leggere stato dopo fallimento: %v", err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("parse stato dopo fallimento: %v", err)
	}
	if s.Success {
		t.Errorf("stato dopo fallimento: non volevo success=true")
	}
	if s.Error == "" {
		t.Error("stato dopo fallimento: Error vuoto")
	}
}
