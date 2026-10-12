package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/backup"
	"github.com/fathorMB/GitStack/admin/internal/backupstate"
	"github.com/fathorMB/GitStack/admin/internal/config"
)

const (
	testS3Access = "AKIATESTACCESS01"
	testS3Secret = "SECRETKEY-do-not-log-0123456789"
)

// memStore è uno storage S3 in memoria.
type memStore struct {
	objs    map[string][]byte
	putErr  error
	checkEr error
}

func newMemStore() *memStore { return &memStore{objs: map[string][]byte{}} }

func (m *memStore) Check(context.Context) error { return m.checkEr }
func (m *memStore) Put(_ context.Context, name string, r io.Reader, _ int64) error {
	if m.putErr != nil {
		return m.putErr
	}
	b, err := io.ReadAll(r)
	m.objs[name] = b
	return err
}
func (m *memStore) Get(_ context.Context, name string) (io.ReadCloser, error) {
	b, ok := m.objs[name]
	if !ok {
		return nil, errors.New("non esiste: " + name)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
func (m *memStore) List(context.Context) ([]backup.ObjectInfo, error) {
	var out []backup.ObjectInfo
	for n, b := range m.objs {
		out = append(out, backup.ObjectInfo{Name: n, Size: int64(len(b))})
	}
	return out, nil
}
func (m *memStore) Remove(_ context.Context, name string) error { delete(m.objs, name); return nil }

func (m *memStore) names() []string {
	var n []string
	for k := range m.objs {
		if !strings.HasSuffix(k, ".sha256") {
			n = append(n, k)
		}
	}
	sort.Strings(n)
	return n
}

// s3App prepara un'App con cluster finto, S3 in memoria, chiave e config.
func s3App(t *testing.T, store *memStore, s3extra string) (*App, string, string, *bytes.Buffer, *bytes.Buffer, *fakeRunner) {
	t.Helper()
	r := &fakeRunner{}
	a, out, errb := newApp(r, nil)
	git := t.TempDir()
	_ = os.WriteFile(filepath.Join(git, "f"), []byte("x"), 0o600)
	a.NewCluster = func(*config.Config) backup.Cluster { return &stubCluster{git: git} }
	a.CheckServing = func(context.Context, *config.Config) error { return nil }
	a.NewStore = func(config.S3) (backup.ObjectStore, error) { return store, nil }
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "chiave")
	if err := os.WriteFile(keyFile, []byte("una-chiave-di-prova-abbastanza-lunga\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.yaml")
	body := "version: 1\nhost: h\nimage_tag: sha-aaa\nbackup:\n  s3:\n    endpoint: s3.example.test\n    bucket: b\n    prefix: gitstack\n" +
		"    access_key: " + testS3Access + "\n    secret_key: " + testS3Secret + "\n    key_file: " + filepath.ToSlash(keyFile) + "\n" + s3extra
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return a, cfg, keyFile, out, errb, r
}

func noSecrets(t *testing.T, what, text string) {
	t.Helper()
	for _, s := range []string{testS3Secret, testS3Access} {
		if strings.Contains(text, s) {
			t.Errorf("%s contiene una chiave S3: %q", what, text)
		}
	}
}

func TestS3BackupCaricaCifratoEApplicaConservazione(t *testing.T) {
	store := newMemStore()
	a, cfg, _, out, errb, _ := s3App(t, store, "    retention: 2\n")
	dest := t.TempDir()
	for i := 0; i < 3; i++ {
		at := time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC)
		a.Now = func() time.Time { return at }
		if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", dest}); code != ExitOK {
			t.Fatalf("backup %d: exit %d: %s", i, code, errb.String())
		}
	}
	names := store.names()
	if len(names) != 2 {
		t.Fatalf("su S3 attesi 2 archivi (retention 2): %v", names)
	}
	if !strings.Contains(names[1], "000002Z") || !strings.Contains(names[0], "000001Z") {
		t.Errorf("devono restare i due più recenti: %v", names)
	}
	for _, n := range names {
		if !strings.HasSuffix(n, ".enc") || !bytes.HasPrefix(store.objs[n], []byte("GSBKENC1")) {
			t.Errorf("%s non è cifrato", n)
		}
		if _, ok := store.objs[n+".sha256"]; !ok {
			t.Errorf("manca lo SHA-256 di %s", n)
		}
	}
	if len(store.objs) != 4 {
		t.Errorf("rimasugli di archivi potati su S3: %d oggetti", len(store.objs))
	}
	// Anche l'archivio locale è cifrato (stessa chiave).
	loc, _ := filepath.Glob(filepath.Join(dest, "gitstack-backup-*.tar.gz.enc"))
	if len(loc) == 0 {
		t.Errorf("archivio locale non cifrato: %v", loc)
	}
	noSecrets(t, "stdout", out.String())
	noSecrets(t, "stderr", errb.String())
	st := backupstate.Read(filepath.Dir(cfg))
	if st.Upload == nil || !st.Upload.Success {
		t.Errorf("stato: %+v", st)
	}
}

func TestS3ConservazioneBucketNonCancella(t *testing.T) {
	store := newMemStore()
	a, cfg, _, _, errb, _ := s3App(t, store, "    retention: bucket\n")
	dest := t.TempDir()
	for i := 0; i < 3; i++ {
		at := time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC)
		a.Now = func() time.Time { return at }
		if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", dest, "--retention", "1"}); code != ExitOK {
			t.Fatalf("backup %d: exit %d: %s", i, code, errb.String())
		}
	}
	if n := len(store.names()); n != 3 {
		t.Errorf("con «bucket» nessuna potatura: attesi 3 archivi, %d", n)
	}
}

func TestS3RestoreFromS3SuAmbientePulito(t *testing.T) {
	store := newMemStore()
	a, cfg, _, _, errb, _ := s3App(t, store, "")
	dest := t.TempDir()
	if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", dest}); code != ExitOK {
		t.Fatalf("backup: %d %s", code, errb.String())
	}
	// «Macchina nuova»: altra cartella dei backup, nessun archivio locale.
	b, cfg2, _, out2, errb2, _ := s3App(t, store, "")
	fresh := t.TempDir()
	if code := b.Run(context.Background(), []string{"restore", "--from", "s3", "--config", cfg2, "--dest", fresh}); code != ExitOK {
		t.Fatalf("restore --from s3: %d %s", code, errb2.String())
	}
	if !strings.Contains(out2.String(), "Restore completato") {
		t.Errorf("stdout: %s", out2.String())
	}
	noSecrets(t, "stdout", out2.String())
	if left, _ := filepath.Glob(filepath.Join(fresh, ".s3-fetch-*")); len(left) != 0 {
		t.Errorf("cartella di download non rimossa: %v", left)
	}
	// Per nome, e nome inesistente / con percorso rifiutati.
	name := store.names()[0]
	if code := b.Run(context.Background(), []string{"restore", "--from", "s3", name, "--config", cfg2, "--dest", fresh}); code != ExitOK {
		t.Errorf("restore per nome: %d", code)
	}
	for _, bad := range []string{"../../etc/passwd", "gitstack-backup-nonesiste.tar.gz.enc"} {
		if code := b.Run(context.Background(), []string{"restore", "--from", "s3", bad, "--config", cfg2, "--dest", fresh}); code == ExitOK {
			t.Errorf("restore di %q doveva fallire", bad)
		}
	}
}

func TestS3RestoreSenzaConfigurazione(t *testing.T) {
	a, cfg := backupApp(t, "sha-aaa")
	if code := a.Run(context.Background(), []string{"restore", "--from", "s3", "--config", cfg}); code != ExitConfig {
		t.Errorf("exit %d, atteso %d", code, ExitConfig)
	}
}

func TestS3CaricamentoFallitoStatoENotifica(t *testing.T) {
	store := newMemStore()
	store.putErr = errors.New("403 Forbidden: credenziali " + testS3Access + "/" + testS3Secret + " non valide")
	a, cfg, _, out, errb, r := s3App(t, store, "    notify_command: echo avviso\n")
	dest := t.TempDir()
	code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", dest})
	if code != ExitUploadFailed {
		t.Fatalf("exit %d, atteso %d: %s", code, ExitUploadFailed, errb.String())
	}
	// Il backup locale è valido.
	loc, _ := filepath.Glob(filepath.Join(dest, "gitstack-backup-*.tar.gz.enc"))
	if len(loc) != 1 {
		t.Fatalf("backup locale assente: %v", loc)
	}
	st := backupstate.Read(filepath.Dir(cfg))
	if !st.Success || st.Upload == nil || st.Upload.Success || st.Upload.Error == "" {
		t.Errorf("stato: %+v", st)
	}
	// Notifica via notify_command, con l'errore ripulito dalle chiavi.
	if len(r.args) < 3 || r.args[0] != "sh" || r.args[2] != "echo avviso" {
		t.Errorf("notify_command non eseguito: %v", r.args)
	}
	if !strings.Contains(strings.Join(r.env, "\n"), "GITSTACK_BACKUP_EVENT=s3-upload-failed") {
		t.Errorf("env: %v", r.env)
	}
	noSecrets(t, "env della notifica", strings.Join(r.env, "\n"))
	noSecrets(t, "stdout", out.String())
	noSecrets(t, "stderr", errb.String())
	raw, _ := os.ReadFile(backupstate.StatePath(filepath.Dir(cfg)))
	noSecrets(t, "backup-state.json", string(raw))

	// gitstack status lo mostra.
	var so, se bytes.Buffer
	sa, _, _ := newApp(&fakeRunner{out: kubectlJSON(1)}, healthServer(200))
	sa.Stdout, sa.Stderr = &so, &se
	sa.Run(context.Background(), []string{"status", "--config", cfg})
	if !strings.Contains(so.String(), "Backup su S3: ULTIMO CARICAMENTO FALLITO") {
		t.Errorf("status non segnala l'errore:\n%s", so.String())
	}
	noSecrets(t, "status", so.String())
}

func TestS3StatusDopoSuccesso(t *testing.T) {
	store := newMemStore()
	a, cfg, _, _, errb, _ := s3App(t, store, "")
	if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", t.TempDir()}); code != ExitOK {
		t.Fatalf("%d %s", code, errb.String())
	}
	var so bytes.Buffer
	sa, _, _ := newApp(&fakeRunner{out: kubectlJSON(1)}, healthServer(200))
	sa.Stdout = &so
	sa.Run(context.Background(), []string{"status", "--config", cfg})
	if !strings.Contains(so.String(), "Backup su S3: ultimo successo") {
		t.Errorf("status:\n%s", so.String())
	}
}

func TestConfigSetBackupS3RifiutaSenzaChiave(t *testing.T) {
	a, cfg := backupApp(t, "sha-aaa")
	store := newMemStore()
	a.NewStore = func(config.S3) (backup.ObjectStore, error) { return store, nil }
	before, _ := os.ReadFile(cfg)
	args := []string{"config", "set", "backup.s3", "--config", cfg, "--endpoint", "s3.example.test", "--bucket", "b",
		"--access-key", testS3Access, "--secret-key", testS3Secret}
	var errb bytes.Buffer
	a.Stderr = &errb
	if code := a.Run(context.Background(), args); code != ExitUsage {
		t.Fatalf("senza --key-file: exit %d", code)
	}
	if !strings.Contains(errb.String(), "key_file") {
		t.Errorf("messaggio: %s", errb.String())
	}
	after, _ := os.ReadFile(cfg)
	if !bytes.Equal(before, after) {
		t.Errorf("la configurazione è cambiata nonostante il rifiuto")
	}
	// Chiave troppo corta: rifiutata.
	short := filepath.Join(t.TempDir(), "k")
	_ = os.WriteFile(short, []byte("corta"), 0o600)
	if code := a.Run(context.Background(), append(args, "--key-file", short)); code != ExitUsage {
		t.Errorf("chiave corta: exit %d", code)
	}
	noSecrets(t, "stderr", errb.String())
}

func TestConfigSetBackupS3ScriveSoloRoot0600(t *testing.T) {
	a, cfg := backupApp(t, "sha-aaa")
	store := newMemStore()
	a.NewStore = func(config.S3) (backup.ObjectStore, error) { return store, nil }
	keyFile := filepath.Join(t.TempDir(), "chiave")
	_ = os.WriteFile(keyFile, []byte("una-chiave-di-prova-abbastanza-lunga"), 0o600)
	var out, errb bytes.Buffer
	a.Stdout, a.Stderr = &out, &errb
	a.Getenv = func(k string) string {
		switch k {
		case envS3AccessKey:
			return testS3Access
		case envS3SecretKey:
			return testS3Secret
		}
		return ""
	}
	args := []string{"config", "set", "backup.s3", "--config", cfg, "--endpoint", "https://s3.example.test", "--bucket", "b",
		"--prefix", "/gs/", "--key-file", keyFile, "--retention", "10"}
	if code := a.Run(context.Background(), args); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	noSecrets(t, "stdout", out.String())
	noSecrets(t, "stderr", errb.String())
	c, err := config.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.Backup.S3 == nil || c.Backup.S3.Bucket != "b" || c.Backup.S3.Prefix != "gs" || c.Backup.S3.KeepCount() != 10 || c.Backup.S3.SecretKey != testS3Secret {
		t.Errorf("config: %+v", c.Backup.S3)
	}
	if c.Host != "h" || c.ImageTag != "sha-aaa" {
		t.Errorf("il resto del file è cambiato: %+v", c)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(cfg)
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("modo %v, atteso 0600", fi.Mode().Perm())
		}
	}
	// Connessione fallita: niente scritto.
	store.checkEr = errors.New("bucket irraggiungibile con " + testS3Secret)
	before, _ := os.ReadFile(cfg)
	errb.Reset()
	if code := a.Run(context.Background(), append(args, "--bucket", "altro")); code == ExitOK {
		t.Fatalf("doveva fallire")
	}
	after, _ := os.ReadFile(cfg)
	if !bytes.Equal(before, after) {
		t.Errorf("config cambiata con bucket irraggiungibile")
	}
	noSecrets(t, "stderr", errb.String())
	// --remove.
	store.checkEr = nil
	if code := a.Run(context.Background(), []string{"config", "set", "backup.s3", "--config", cfg, "--remove"}); code != ExitOK {
		t.Fatalf("remove: %d", code)
	}
	if c, _ := config.Load(cfg); c == nil || c.Backup.S3 != nil {
		t.Errorf("sezione S3 non rimossa")
	}
}

func TestS3ConfigSenzaChiaveRifiutataAlCaricamento(t *testing.T) {
	body := "version: 1\nhost: h\nbackup:\n  s3:\n    endpoint: e\n    bucket: b\n    access_key: " + testS3Access + "\n    secret_key: " + testS3Secret + "\n"
	_, err := config.Parse([]byte(body), "x")
	if err == nil || !strings.Contains(err.Error(), "key_file") {
		t.Fatalf("atteso rifiuto per key_file mancante: %v", err)
	}
	noSecrets(t, "errore", err.Error())
	s := config.S3{Endpoint: "e", Bucket: "b", AccessKey: testS3Access, SecretKey: testS3Secret, KeyFile: "/k"}
	for _, txt := range []string{s.String(), s.GoString()} {
		noSecrets(t, "String()", txt)
	}
}

func TestS3KeyFileDiversoDaConfigAvvisaEdIlRestoreDefaultFallisce(t *testing.T) {
	store := newMemStore()
	a, cfg, _, _, errb, _ := s3App(t, store, "")
	other := filepath.Join(t.TempDir(), "altra-chiave")
	if err := os.WriteFile(other, []byte("un'altra-chiave-di-prova-lunga-abbastanza\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if code := a.Run(context.Background(), []string{"backup", "--config", cfg, "--dest", dest, "--key-file", other}); code != ExitOK {
		t.Fatalf("backup: %d %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "diverso da backup.s3.key_file") {
		t.Errorf("manca l'avviso: %q", errb.String())
	}
	// Senza --key-file il restore usa la chiave del config e non apre l'archivio...
	fresh := t.TempDir()
	if code := a.Run(context.Background(), []string{"restore", "--from", "s3", "--config", cfg, "--dest", fresh}); code != ExitRefused {
		t.Errorf("restore con la chiave del config: exit %d, atteso %d", code, ExitRefused)
	}
	// ...con la chiave usata per il backup sì.
	if code := a.Run(context.Background(), []string{"restore", "--from", "s3", "--config", cfg, "--dest", fresh, "--key-file", other}); code != ExitOK {
		t.Errorf("restore con --key-file: exit %d", code)
	}
	// Stessa chiave del config: nessun avviso.
	b, cfg2, keyFile, _, errb2, _ := s3App(t, newMemStore(), "")
	if code := b.Run(context.Background(), []string{"backup", "--config", cfg2, "--dest", t.TempDir(), "--key-file", keyFile}); code != ExitOK {
		t.Fatalf("backup 2: %d", code)
	}
	if strings.Contains(errb2.String(), "diverso") {
		t.Errorf("avviso inutile: %q", errb2.String())
	}
}
