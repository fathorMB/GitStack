package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	a.CheckServing = func(context.Context, *config.Config) error { return nil }
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
	// Tre backup con retention=2 → restano 2 (i più recenti).
	a.Now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", dest, "--retention", "2"}); code != ExitOK {
		t.Fatalf("backup 1: exit %d", code)
	}
	a.Now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC) }
	if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", dest, "--retention", "2"}); code != ExitOK {
		t.Fatalf("backup 2: exit %d", code)
	}
	a.Now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 2, 0, time.UTC) }
	if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", dest, "--retention", "2"}); code != ExitOK {
		t.Fatalf("backup 3: exit %d", code)
	}
	ents, _ := filepath.Glob(filepath.Join(dest, "gitstack-backup-*.tar.gz"))
	if len(ents) != 2 {
		t.Fatalf("dopo tre backup con retention=2: %d archivi, volevo 2", len(ents))
	}
	// I due più recenti: i nomi sono ordinati per data UTC nel prefisso.
	names := make([]string, len(ents))
	for i, e := range ents {
		names[i] = filepath.Base(e)
	}
	// Il più vecchio (primo) deve essere stato eliminato.
	if strings.Contains(names[0], "00002") {
		t.Errorf("il backup più vecchio (00002) è ancora presente: %v", names)
	}
	if !strings.Contains(names[1], "00002") {
		t.Errorf("manca il backup più recente: %v", names)
	}
}

func TestBackupRetentionNoPruneOnFail(t *testing.T) {
	a, cfg := backupApp(t, "sha-aaa")
	dest := t.TempDir()
	// Creo due archivi come se fossero già presenti.
	archive1 := filepath.Join(dest, "gitstack-backup-20260101T000000Z-sha-aaa.tar.gz")
	_ = os.WriteFile(archive1, []byte("archivio 1"), 0o600)
	_ = os.WriteFile(archive1+".sha256", []byte("aaaa  gitstack-backup-20260101T000000Z-sha-aaa.tar.gz\n"), 0o600)
	archive2 := filepath.Join(dest, "gitstack-backup-20260101T000001Z-sha-aaa.tar.gz")
	_ = os.WriteFile(archive2, []byte("archivio 2"), 0o600)
	_ = os.WriteFile(archive2+".sha256", []byte("bbbb  gitstack-backup-20260101T000001Z-sha-aaa.tar.gz\n"), 0o600)

	// Backup fallito con retention=1: non deve cancellare nessuno dei due.
	a.NewCluster = func(*config.Config) backup.Cluster { return &failCluster{} }
	if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", dest, "--retention", "1"}); code != ExitUnexpected {
		t.Fatalf("backup fallito: exit %d", code)
	}
	ents, _ := filepath.Glob(filepath.Join(dest, "gitstack-backup-*.tar.gz"))
	if len(ents) != 2 {
		t.Errorf("dopo backup fallito con retention=1: %d archivi, volevo 2", len(ents))
	}
}

func TestBackupStateFile(t *testing.T) {
	a, cfg := backupApp(t, "sha-aaa")
	dest := t.TempDir()
	configDir := filepath.Dir(cfg)
	git := t.TempDir()
	_ = os.WriteFile(filepath.Join(git, "f"), []byte("x"), 0o600)
	a.NewCluster = func(*config.Config) backup.Cluster { return &stubCluster{git: git} }
	a.CheckServing = func(context.Context, *config.Config) error { return nil }
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

func (s *stubCluster) Restart(context.Context, string) error { return nil }

func (f *failCluster) Restart(_ context.Context, _ string) error { return nil }

func TestRestorePublishesTLSOnlyForInternalAndCustom(t *testing.T) {
	for _, tc := range []struct {
		tls  string
		want bool
	}{{"internal", true}, {"custom", true}, {"insecure", false}, {"letsencrypt", false}} {
		t.Run(tc.tls, func(t *testing.T) {
			a, cfg := backupApp(t, "sha-aaa")
			if err := os.WriteFile(cfg, []byte("version: 1\nhost: h\ntls: "+tc.tls+"\nimage_tag: sha-aaa\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			dest := t.TempDir()
			if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", dest}); code != ExitOK {
				t.Fatalf("backup exit %d", code)
			}
			ents, _ := filepath.Glob(filepath.Join(dest, "gitstack-backup-*.tar.gz"))
			if len(ents) != 1 {
				t.Fatalf("archivi: %v", ents)
			}
			rr := &recRunner{}
			a.Runner = rr
			a.Getenv = func(string) string { return "" }
			if code := a.Run(context.Background(), []string{"restore", "--config", cfg, "--dest", dest, ents[0]}); code != ExitOK {
				t.Fatalf("restore exit %d", code)
			}
			c := rr.find("gitstack-tls")
			if !tc.want {
				if c != nil {
					t.Fatalf("tls %s: nessun ensure atteso, c'è %+v", tc.tls, c)
				}
				return
			}
			if c == nil || len(c.args) != 1 || c.args[0] != "ensure" {
				t.Fatalf("atteso gitstack-tls ensure, chiamate: %+v", rr.calls)
			}
			wantEnv := "GITSTACK_TLS_DIR=" + filepath.Join(filepath.Dir(cfg), "tls")
			if len(c.env) != 1 || c.env[0] != wantEnv {
				t.Errorf("env = %v, atteso %s", c.env, wantEnv)
			}
		})
	}
}

func TestRestoreTLSEnsureFailureExitsNonZero(t *testing.T) {
	a, cfg := backupApp(t, "sha-aaa")
	_ = os.WriteFile(cfg, []byte("version: 1\nhost: h\ntls: internal\nimage_tag: sha-aaa\n"), 0o600)
	dest := t.TempDir()
	if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", dest}); code != ExitOK {
		t.Fatalf("backup exit %d", code)
	}
	ents, _ := filepath.Glob(filepath.Join(dest, "gitstack-backup-*.tar.gz"))
	a.Runner = failTLSRunner{}
	a.Getenv = func(string) string { return "" }
	var stderr strings.Builder
	a.Stderr = &stderr
	if code := a.Run(context.Background(), []string{"restore", "--config", cfg, "--dest", dest, ents[0]}); code != ExitUnexpected {
		t.Errorf("exit %d, atteso %d", code, ExitUnexpected)
	}
	if !strings.Contains(stderr.String(), "sudo gitstack-tls ensure") {
		t.Errorf("manca il comando da rilanciare: %s", stderr.String())
	}
}

type failTLSRunner struct{}

func (failTLSRunner) Run(context.Context, []string, string, ...string) ([]byte, error) {
	return []byte("openssl: errore"), errors.New("exit status 1")
}
