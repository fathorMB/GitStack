package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/config"
)

type fakeCluster struct {
	mu             sync.Mutex
	replicas       map[string]int // per nome di Deployment
	calls          []string
	paths          map[string]string // pvc -> cartella
	secrets        []Secret
	applied        []string
	dump           string
	psqlIn         string
	failDump       bool
	failWait       bool
	failRestart    bool
	statefulSets   map[string]bool // StatefulSet presenti
	failSTSRestart bool
	ctxAtDump      context.Context
	registryRepos  string // output della query dei repo con includeInBackup
	dumpArgs       string // comando di pg_dump
}

func (f *fakeCluster) note(s string) { f.mu.Lock(); f.calls = append(f.calls, s); f.mu.Unlock() }

func (f *fakeCluster) Replicas(_ context.Context, d string) (int, bool, error) {
	n, ok := f.replicas[d]
	return n, ok, nil
}
func (f *fakeCluster) Scale(_ context.Context, d string, n int) error {
	f.note("scale " + d + " " + string(rune('0'+n)))
	f.replicas[d] = n
	return nil
}
func (f *fakeCluster) WaitStopped(_ context.Context, c string) error {
	f.note("wait " + c)
	if f.failWait {
		return errors.New("timeout")
	}
	return nil
}
func (f *fakeCluster) WaitReady(_ context.Context, d string) error { f.note("ready " + d); return nil }
func (f *fakeCluster) StatefulSetExists(_ context.Context, n string) (bool, error) {
	return f.statefulSets[n], nil
}
func (f *fakeCluster) RestartStatefulSet(_ context.Context, n string) error {
	f.note("restart-sts " + n)
	if f.failSTSRestart {
		return errors.New("restart fallito")
	}
	return nil
}
func (f *fakeCluster) WaitStatefulSetReady(_ context.Context, n string) error {
	f.note("ready-sts " + n)
	return nil
}
func (f *fakeCluster) PGExec(ctx context.Context, stdin io.Reader, stdout io.Writer, args ...string) error {
	if strings.Contains(strings.Join(args, " "), "repo_package_settings") {
		// Elenco dei repo con le immagini nel backup (GIT-325): una lettura, non entra nelle chiamate.
		_, err := io.WriteString(stdout, f.registryRepos)
		return err
	}
	if strings.Contains(strings.Join(args, " "), "pg_dump") {
		f.note("pg_dump")
		f.dumpArgs = strings.Join(args, " ")
		f.ctxAtDump = ctx
		if f.failDump {
			return errors.New("pg_dump fallito")
		}
		_, err := io.WriteString(stdout, f.dump)
		return err
	}
	f.note("psql")
	b, _ := io.ReadAll(stdin)
	f.psqlIn = string(b)
	return nil
}
func (f *fakeCluster) VolumePath(_ context.Context, pvc string) (string, error) {
	p, ok := f.paths[pvc]
	if !ok {
		return "", ErrNotFound
	}
	return p, nil
}
func (f *fakeCluster) Secrets(context.Context) ([]Secret, error) { return f.secrets, nil }
func (f *fakeCluster) ApplySecret(_ context.Context, s Secret) error {
	f.applied = append(f.applied, s.Name)
	return nil
}

type env struct {
	f             *fakeCluster
	o             *Options
	dest          string
	git           string
	att           string
	releaseAssets string
	ci            string
	confDir       string
	log           *bytes.Buffer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	git := filepath.Join(root, "git-data")
	att := filepath.Join(root, "att")
	releaseAssets := filepath.Join(root, "release-assets")
	ci := filepath.Join(root, "ci-data")
	conf := filepath.Join(root, "etc")
	for _, d := range []string{git, att, releaseAssets, ci, conf} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, c string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(git, "alice", "repo.git", "HEAD"), "ref: refs/heads/main\n")
	write(filepath.Join(git, "alice", "repo.git", "objects", "ab", "cdef"), "OGGETTO")
	write(filepath.Join(att, "r1", "a1"), "ALLEGATO")
	write(filepath.Join(releaseAssets, "r1", "release.bin"), "ASSET-RELEASE-ESCLUSO")
	write(filepath.Join(ci, "logs", "job-1.log"), "RIGA-DI-LOG\n")
	write(filepath.Join(ci, "data", "artifacts", "r1", "a1"), "ARTEFATTO-ESCLUSO")
	write(filepath.Join(ci, "data", "caches", "r1", "c1"), "CACHE-ESCLUSA")
	write(filepath.Join(conf, "config.yaml"), "version: 1\n")
	write(filepath.Join(conf, "ca", "ca.key"), "CHIAVE-CA")
	f := &fakeCluster{
		replicas: map[string]int{"gs-gateway": 2, "gs-git": 1, "gs-core": 1, "gs-identity": 1, "gs-web": 1},
		paths:    map[string]string{"gs-git-data": git, "gs-attachments-data": att, "gs-release-assets-data": releaseAssets, "gs-ci-data": ci},
		secrets: []Secret{
			{Name: "gs-postgres", Raw: []byte(`{"name":"pg"}`)},
			{Name: "gs-git-ssh-host-key", Raw: []byte(`{"name":"ssh"}`)},
		},
		dump: "CREATE SCHEMA identity;\nINSERT INTO core.releases (tag_name, body) VALUES ('v1', 'note release');\n",
	}
	cfg := &config.Config{Release: "gs", Namespace: "ns", ImageTag: "sha-abc123", Backup: config.Backup{Retention: 2}}
	log := &bytes.Buffer{}
	e := &env{f: f, dest: filepath.Join(root, "backups"), git: git, att: att, releaseAssets: releaseAssets, ci: ci, confDir: conf, log: log}
	e.o = &Options{Cfg: cfg, Cluster: f, BinaryVersion: "v-test", ConfigDir: conf, ConfigFile: filepath.Join(conf, "config.yaml"),
		DestDir: e.dest, Log: log, Now: func() time.Time { return time.Date(2026, 10, 6, 1, 2, 3, 0, time.UTC) }}
	return e
}

func TestBackupRoundTrip(t *testing.T) {
	e := newEnv(t)
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(res.Path); st.Mode().Perm() != 0o600 {
			t.Errorf("permessi archivio %v", st.Mode().Perm())
		}
		if st, _ := os.Stat(e.dest); st.Mode().Perm() != 0o700 {
			t.Errorf("permessi cartella %v", st.Mode().Perm())
		}
	}
	if e.f.replicas["gs-gateway"] != 2 || e.f.replicas["gs-git"] != 1 {
		t.Fatalf("repliche non ripristinate: %v", e.f.replicas)
	}
	// l'ordine: stop, attesa, dump, riavvio, attesa che siano pronti
	want := []string{"scale gs-gateway 0", "scale gs-git 0", "wait gateway", "wait git", "pg_dump", "scale gs-gateway 2", "scale gs-git 1", "ready gs-gateway", "ready gs-git"}
	if got := strings.Join(e.f.calls, "|"); got != strings.Join(want, "|") {
		t.Fatalf("chiamate:\n%v\nattese:\n%v", e.f.calls, want)
	}
	m := res.Manifest
	if m.Version != "sha-abc123" || m.Commit != "abc123" || m.BinaryVersion != "v-test" || m.CreatedAt.IsZero() {
		t.Errorf("manifest %+v", m)
	}
	names := map[string]bool{}
	for _, f := range m.Files {
		names[f.Name] = true
		if len(f.SHA256) != 64 {
			t.Errorf("checksum mancante per %s", f.Name)
		}
	}
	for _, n := range []string{DatabaseName, GitDataName, AttachmentsName, CILogsName, SecretsName, "config/config.yaml", "config/ca/ca.key"} {
		if !names[n] {
			t.Errorf("manca %s nel manifest", n)
		}
	}
	if names["release-assets.tar"] {
		t.Error("gli asset release non devono entrare nel backup predefinito")
	}
	for _, ex := range []string{"core.ci_artifacts", "core.ci_caches"} {
		if !strings.Contains(e.f.dumpArgs, "--exclude-table-data="+ex) {
			t.Errorf("il dump deve escludere i dati di %s: %s", ex, e.f.dumpArgs)
		}
	}
	if _, err := os.Stat(res.Path + ".sha256"); err != nil {
		t.Errorf("sidecar: %v", err)
	}
	if strings.Contains(e.log.String(), "OGGETTO") || strings.Contains(e.log.String(), "alice") {
		t.Errorf("dati utente nei log: %s", e.log.String())
	}

	// Restore su un'installazione "pulita": volumi diversi e vuoti.
	r := newEnv(t)
	r.o.DestDir = filepath.Join(t.TempDir(), "b")
	if err := os.RemoveAll(r.releaseAssets); err != nil {
		t.Fatal(err)
	}
	// Sul volume della CI restano un vecchio log e un vecchio artefatto: il restore li toglie.
	if err := os.WriteFile(filepath.Join(r.ci, "logs", "vecchio.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(r.ci, "logs", "job-1.log")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.git, "vecchio"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.confDir, "config.yaml"), []byte("NUOVO"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(filepath.Join(r.confDir, "ca"))
	if err := Restore(context.Background(), r.o, res.Path); err != nil {
		t.Fatal(err)
	}
	for p, c := range map[string]string{
		filepath.Join(r.git, "alice", "repo.git", "objects", "ab", "cdef"): "OGGETTO",
		filepath.Join(r.att, "r1", "a1"):                                   "ALLEGATO",
		filepath.Join(r.ci, "logs", "job-1.log"):                            "RIGA-DI-LOG\n",
		filepath.Join(r.confDir, "ca", "ca.key"):                           "CHIAVE-CA",
		filepath.Join(r.confDir, "config.yaml"):                            "NUOVO",
	} {
		b, err := os.ReadFile(p)
		if err != nil || string(b) != c {
			t.Errorf("%s = %q, %v; atteso %q", p, b, err, c)
		}
	}
	for _, gone := range []string{
		filepath.Join(r.ci, "logs", "vecchio.log"),
		filepath.Join(r.ci, "data", "artifacts", "r1", "a1"),
		filepath.Join(r.ci, "data", "caches", "r1", "c1"),
	} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("%s non doveva esserci dopo il restore (artefatti e cache restano fuori dal backup)", gone)
		}
	}
	if _, err := os.Stat(filepath.Join(r.git, "vecchio")); err == nil {
		t.Error("il contenuto precedente del volume non è stato rimosso")
	}
	if !strings.Contains(r.f.psqlIn, "DROP SCHEMA IF EXISTS core") || !strings.Contains(r.f.psqlIn, "CREATE SCHEMA identity;") {
		t.Errorf("stdin di psql: %q", r.f.psqlIn)
	}
	if !strings.Contains(r.f.psqlIn, "note release") {
		t.Error("le note release non sono state ripristinate dal dump")
	}
	if _, err := os.Stat(filepath.Join(r.releaseAssets, "r1", "release.bin")); err == nil {
		t.Error("gli asset release non devono essere ripristinati dal backup predefinito")
	}
	if len(r.f.applied) != 1 || r.f.applied[0] != "gs-git-ssh-host-key" {
		t.Errorf("Secret applicati: %v (postgres non va ripristinato)", r.f.applied)
	}
	for _, d := range []string{"gs-gateway", "gs-git", "gs-core", "gs-identity"} {
		if r.f.replicas[d] == 0 {
			t.Errorf("%s è rimasto a 0", d)
		}
	}
}

func TestBackupRestoresReplicasOnError(t *testing.T) {
	for name, mut := range map[string]func(*fakeCluster){
		"dump":   func(f *fakeCluster) { f.failDump = true },
		"attesa": func(f *fakeCluster) { f.failWait = true },
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			mut(e.f)
			if _, err := Backup(context.Background(), e.o); err == nil {
				t.Fatal("atteso errore")
			}
			if e.f.replicas["gs-gateway"] != 2 || e.f.replicas["gs-git"] != 1 {
				t.Fatalf("repliche non ripristinate: %v", e.f.replicas)
			}
			ents, _ := os.ReadDir(e.dest)
			if len(ents) != 0 {
				t.Errorf("restano file dopo l'errore: %v", ents)
			}
		})
	}
}

func TestBackupRestoresReplicasOnInterrupt(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	e.f.dump = ""
	// L'interruzione arriva durante il dump.
	e.o.Cluster = &cancelAtDump{fakeCluster: e.f, cancel: cancel}
	if _, err := Backup(ctx, e.o); err == nil {
		t.Fatal("atteso errore")
	}
	if e.f.replicas["gs-gateway"] != 2 || e.f.replicas["gs-git"] != 1 {
		t.Fatalf("repliche non ripristinate dopo l'interruzione: %v", e.f.replicas)
	}
}

type cancelAtDump struct {
	*fakeCluster
	cancel func()
}

func (c *cancelAtDump) PGExec(ctx context.Context, in io.Reader, out io.Writer, args ...string) error {
	c.cancel()
	return ctx.Err()
}

func (c *cancelAtDump) Scale(ctx context.Context, d string, n int) error {
	if n > 0 && ctx.Err() != nil {
		return ctx.Err() // il ripristino non deve usare il contesto annullato
	}
	return c.fakeCluster.Scale(ctx, d, n)
}

func TestBackupKeepsZeroReplicas(t *testing.T) {
	e := newEnv(t)
	e.f.replicas["gs-git"] = 0
	delete(e.f.replicas, "gs-gateway")
	if _, err := Backup(context.Background(), e.o); err != nil {
		t.Fatal(err)
	}
	if e.f.replicas["gs-git"] != 0 {
		t.Errorf("git era a 0 e va lasciato a 0: %v", e.f.replicas)
	}
	for _, c := range e.f.calls {
		if strings.HasPrefix(c, "scale") {
			t.Errorf("nessuna scala attesa, c'è %s", c)
		}
	}
}

func TestRestoreRefusesOtherVersion(t *testing.T) {
	e := newEnv(t)
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	r := newEnv(t)
	r.o.Cfg.ImageTag = "sha-def456"
	err = Restore(context.Background(), r.o, res.Path)
	var ref *RefusedError
	if !errors.As(err, &ref) || !strings.Contains(err.Error(), "sha-abc123") || !strings.Contains(err.Error(), "sha-def456") {
		t.Fatalf("atteso rifiuto con le due versioni, ottenuto %v", err)
	}
	if len(r.f.calls) != 0 || r.f.psqlIn != "" {
		t.Errorf("il rifiuto non deve toccare il cluster: %v", r.f.calls)
	}
}

func TestRestoreRefusesCorrupt(t *testing.T) {
	e := newEnv(t)
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	// Non è un archivio.
	junk := filepath.Join(t.TempDir(), "x.tar.gz")
	_ = os.WriteFile(junk, []byte("non sono un archivio"), 0o600)
	var ref *RefusedError
	if err := Restore(context.Background(), e.o, junk); !errors.As(err, &ref) {
		t.Errorf("atteso rifiuto, ottenuto %v", err)
	}
	// Byte alterato dentro il gzip: il gzip o il checksum lo rilevano.
	b, _ := os.ReadFile(res.Path)
	b[len(b)/2] ^= 0xff
	bad := filepath.Join(t.TempDir(), "bad.tar.gz")
	_ = os.WriteFile(bad, b, 0o600)
	r := newEnv(t)
	if err := Restore(context.Background(), r.o, bad); !errors.As(err, &ref) {
		t.Errorf("atteso rifiuto per archivio alterato, ottenuto %v", err)
	}
	if len(r.f.calls) != 0 {
		t.Errorf("il cluster non va toccato: %v", r.f.calls)
	}
}

func TestEncryptedBackup(t *testing.T) {
	e := newEnv(t)
	e.o.Key = []byte("una-chiave-di-prova-lunga-abbastanza")
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(res.Path, ".enc") {
		t.Errorf("nome %s", res.Path)
	}
	raw, _ := os.ReadFile(res.Path)
	if bytes.Contains(raw, []byte("CHIAVE-CA")) || bytes.Contains(raw, []byte("gitstack")) && bytes.Contains(raw, []byte("manifest")) {
		t.Error("il contenuto è in chiaro")
	}
	var ref *RefusedError
	r := newEnv(t)
	r.o.Key = nil
	if err := Restore(context.Background(), r.o, res.Path); !errors.As(err, &ref) {
		t.Errorf("senza chiave: %v", err)
	}
	r.o.Key = []byte("un'altra-chiave-sbagliata-ma-lunga")
	if err := Restore(context.Background(), r.o, res.Path); !errors.As(err, &ref) {
		t.Errorf("chiave errata: %v", err)
	}
	r.o.Key = e.o.Key
	if err := Restore(context.Background(), r.o, res.Path); err != nil {
		t.Errorf("chiave giusta: %v", err)
	}
	if _, err := NewEncryptWriter(io.Discard, []byte("corta")); err == nil {
		t.Error("chiave corta accettata")
	}
}

func TestPruneKeepsRetention(t *testing.T) {
	e := newEnv(t)
	for i := range 4 {
		e.o.Now = func() time.Time { return time.Date(2026, 10, 6, 1, 2, i, 0, time.UTC) }
		if _, err := Backup(context.Background(), e.o); err != nil {
			t.Fatal(err)
		}
	}
	ents, _ := os.ReadDir(e.dest)
	n := 0
	for _, x := range ents {
		if strings.HasSuffix(x.Name(), ".tar.gz") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("archivi conservati %d, attesi 2 (%v)", n, ents)
	}
}

func TestSafeJoin(t *testing.T) {
	for _, bad := range []string{"../x", "/etc/passwd", "a/../../x", "c:/x"} {
		if _, err := safeJoin("/r", bad); err == nil {
			t.Errorf("%q accettato", bad)
		}
	}
	if p, err := safeJoin("/r", "a/b/"); err != nil || filepath.ToSlash(p) != "/r/a/b" {
		t.Errorf("%q, %v", p, err)
	}
}

func (f *fakeCluster) Restart(_ context.Context, d string) error {
	f.note("restart " + d)
	if f.failRestart {
		return errors.New("restart fallito")
	}
	return nil
}

// failReady fa fallire WaitReady: l'attesa dei Deployment riscalati non deve
// far fallire il backup (lo stato «non pronto» è quello reale).
type failReady struct{ *fakeCluster }

func (failReady) WaitReady(context.Context, string) error { return errors.New("rollout non finito") }

func TestBackupAttesaDiProntezzaNonFaFallireIlBackup(t *testing.T) {
	e := newEnv(t)
	e.o.Cluster = failReady{e.f}
	if _, err := Backup(context.Background(), e.o); err != nil {
		t.Fatalf("il backup non deve fallire se l'attesa fallisce: %v", err)
	}
	if !strings.Contains(e.log.String(), "attesa della ripartenza di gateway non riuscita") {
		t.Errorf("log: %s", e.log.String())
	}
}

func TestBackupFallitoAspettaComunqueLeRepliche(t *testing.T) {
	e := newEnv(t)
	e.f.failDump = true
	if _, err := Backup(context.Background(), e.o); err == nil {
		t.Fatal("atteso errore")
	}
	got := strings.Join(e.f.calls, "|")
	if !strings.HasSuffix(got, "scale gs-gateway 2|scale gs-git 1|ready gs-gateway|ready gs-git") {
		t.Errorf("chiamate: %s", got)
	}
}

// TestBackupLogCIEscludeArtefattiECache: nell'archivio entrano i log, mai artefatti e cache (CI5);
// un log che cresce durante la copia (core lo scrive ancora) non fa fallire il backup.
func TestBackupLogCIEscludeArtefattiECache(t *testing.T) {
	e := newEnv(t)
	res, err := Backup(context.Background(), e.o)
	if err != nil {
		t.Fatal(err)
	}
	tr, closeFn, err := openArchive(res.Path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	stage := t.TempDir()
	hdr, err := tr.Next()
	if err != nil || hdr.Name != ManifestName {
		t.Fatalf("manifest: %v", err)
	}
	var m Manifest
	if err := json.NewDecoder(tr).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if err := extractVerified(tr, &m, stage); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(stage, CILogsName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	got := map[string]string{}
	trr := tar.NewReader(f)
	for {
		h, err := trr.Next()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(trr)
		got[h.Name] = string(b)
	}
	if got["logs/job-1.log"] != "RIGA-DI-LOG\n" {
		t.Errorf("log nel tar: %v", got)
	}
	for name := range got {
		if name != "logs/" && !strings.HasPrefix(name, "logs/") {
			t.Errorf("voce fuori da logs/ nel backup: %s", name)
		}
	}
}
