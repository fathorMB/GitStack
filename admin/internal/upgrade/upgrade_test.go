package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/admin/internal/backup"
	"github.com/fathorMB/GitStack/admin/internal/config"
	"github.com/fathorMB/GitStack/admin/internal/status"
)

const (
	oldSHA = "1111111111111111111111111111111111111111"
	newSHA = "2222222222222222222222222222222222222222"
	newBin = "BINARIO-NUOVO"
	oldBin = "BINARIO-VECCHIO"
)

// fakeCluster implementa backup.Cluster: il database è una stringa
// (versioni di schema) e il restore la riporta a quella del dump.
type fakeCluster struct {
	mu       sync.Mutex
	calls    []string
	git      string
	schema   string // risposta della query sulle migrazioni
	dumped   string // schema al momento del pg_dump
	restored bool
	noSchema bool // la query sulle migrazioni fallisce
}

func (f *fakeCluster) note(s string) { f.mu.Lock(); f.calls = append(f.calls, s); f.mu.Unlock() }
func (f *fakeCluster) Replicas(context.Context, string) (int, bool, error) {
	return 1, true, nil
}
func (f *fakeCluster) Scale(_ context.Context, d string, n int) error {
	f.note(fmt.Sprintf("scale %s %d", d, n))
	return nil
}
func (f *fakeCluster) WaitStopped(context.Context, string) error { return nil }
func (f *fakeCluster) WaitReady(context.Context, string) error   { return nil }
func (f *fakeCluster) PGExec(_ context.Context, stdin io.Reader, stdout io.Writer, args ...string) error {
	cmd := strings.Join(args, " ")
	switch {
	case strings.Contains(cmd, "pg_dump"):
		f.note("pg_dump")
		f.dumped = f.schema
		_, err := io.WriteString(stdout, "-- dump\n")
		return err
	case strings.Contains(cmd, "schema_migrations"):
		if f.noSchema {
			return errors.New("relation does not exist")
		}
		_, err := io.WriteString(stdout, f.schema+"\n")
		return err
	default:
		f.note("restore")
		_, _ = io.Copy(io.Discard, stdin)
		f.restored = true
		f.schema = f.dumped
		return nil
	}
}
func (f *fakeCluster) VolumePath(_ context.Context, pvc string) (string, error) {
	if strings.HasSuffix(pvc, "git-data") {
		return f.git, nil
	}
	return "", backup.ErrNotFound
}
func (f *fakeCluster) Secrets(context.Context) ([]backup.Secret, error) { return nil, nil }
func (f *fakeCluster) ApplySecret(context.Context, backup.Secret) error { return nil }

// fakeHelm risponde ai sottocomandi di helm e registra le chiamate.
type fakeHelm struct {
	mu       sync.Mutex
	calls    []string
	upgraded bool
	rolled   bool
	onUp     func() error // esito di helm upgrade
	failRoll bool
	cluster  *fakeCluster
	newSchem string // schema dopo le migrazioni del chart nuovo
	images   string
}

func (h *fakeHelm) Run(_ context.Context, _ []string, _ string, args ...string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, strings.Join(args, " "))
	switch args[0] {
	case "history":
		return []byte(`[{"revision":3,"status":"superseded"},{"revision":4,"status":"deployed"}]`), nil
	case "get":
		return []byte(`{"global":{"image":{"registry":"mirror.example"}}}`), nil
	case "template":
		return []byte(h.images), nil
	case "upgrade":
		h.upgraded = true
		if h.newSchem != "" {
			h.cluster.schema = h.newSchem
		}
		if h.onUp != nil {
			return []byte("Error: boom"), h.onUp()
		}
		return nil, nil
	case "rollback":
		if h.failRoll {
			return []byte("rollback boom"), errors.New("exit 1")
		}
		h.rolled = true
		return nil, nil
	}
	return nil, errors.New("sottocomando inatteso " + args[0])
}

func (h *fakeHelm) has(sub string) bool {
	for _, c := range h.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func chartTarball(t *testing.T, sha string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name, body string) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	add("GitStack-"+sha+"/README.md", "no")
	add("GitStack-"+sha+"/deploy/gitstack/Chart.yaml", "name: gitstack\n")
	add("GitStack-"+sha+"/deploy/gitstack/values.yaml", "NUOVO: 1\n")
	add("GitStack-"+sha+"/deploy/gitstack/templates/a.yaml", "x")
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

type world struct {
	t       *testing.T
	srv     *httptest.Server
	cl      *fakeCluster
	helm    *fakeHelm
	o       *Options
	exe     string
	cfgFile string
	chart   string
	dest    string
	healthy func() bool
	compare string // risposta del confronto
	badSum  bool
	noBin   bool
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, compare: "ahead"}
	root := t.TempDir()
	w.exe = filepath.Join(root, "bin", "gitstack")
	w.chart = filepath.Join(root, "share", "chart")
	w.cfgFile = filepath.Join(root, "etc", "config.yaml")
	w.dest = filepath.Join(root, "backups")
	git := filepath.Join(root, "git-data")
	for _, d := range []string{filepath.Dir(w.exe), w.chart, filepath.Dir(w.cfgFile), git} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, w.exe, oldBin)
	mustWrite(t, filepath.Join(w.chart, "Chart.yaml"), "name: gitstack\n")
	mustWrite(t, filepath.Join(w.chart, "values.yaml"), "VECCHIO: 1\n")
	mustWrite(t, filepath.Join(git, "r.git"), "OGGETTO")
	mustWrite(t, w.cfgFile, "version: 1\nhost: h\nchart_dir: "+filepath.ToSlash(w.chart)+"\nimage_tag: sha-"+oldSHA+"\n")

	sum := sha256.Sum256([]byte(newBin))
	w.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/commits/"+newSHA), strings.HasSuffix(p, "/commits/main"), strings.HasSuffix(p, "/commits/2222222"):
			_, _ = io.WriteString(rw, `{"sha":"`+newSHA+`"}`)
		case strings.HasSuffix(p, "/commits/v9.9.9"):
			http.NotFound(rw, r)
		case strings.Contains(p, "/compare/"):
			_, _ = io.WriteString(rw, `{"status":"`+w.compare+`"}`)
		case strings.HasSuffix(p, "/gitstack-linux-amd64.sha256"):
			s := hex.EncodeToString(sum[:])
			if w.badSum {
				s = strings.Repeat("0", 64)
			}
			_, _ = io.WriteString(rw, s+"  gitstack-linux-amd64\n")
		case strings.HasSuffix(p, "/gitstack-linux-amd64"):
			if w.noBin {
				http.NotFound(rw, r)
				return
			}
			_, _ = io.WriteString(rw, newBin)
		case strings.HasSuffix(p, "/tar.gz/"+newSHA):
			_, _ = rw.Write(chartTarball(t, newSHA))
		default:
			http.NotFound(rw, r)
		}
	}))
	t.Cleanup(w.srv.Close)

	cfg, err := config.Load(w.cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Backup.Destination = w.dest
	w.cl = &fakeCluster{git: git, schema: "identity|5|f\ncore|11|f"}
	w.helm = &fakeHelm{cluster: w.cl, images: "image: \"mirror.example/fathormb/gitstack-core:sha-" + newSHA + "\"\n      - image: busybox:1\n"}
	w.healthy = func() bool { return true }
	w.o = &Options{
		Cfg: cfg, ConfigFile: w.cfgFile, Cluster: w.cl, Runner: w.helm,
		Health: func(context.Context) *status.Report {
			ok := w.healthy()
			r := &status.Report{APIHealthy: ok, Services: []status.Service{{Name: "gs-core", Desired: 1, Ready: 1}}}
			if !ok {
				r.Services[0].Ready = 0
			}
			return r
		},
		Backup:      backup.Options{Cfg: cfg, ConfigDir: filepath.Dir(w.cfgFile), ConfigFile: w.cfgFile, BinaryVersion: "t", DestDir: w.dest},
		Timeout:     200 * time.Millisecond,
		Poll:        10 * time.Millisecond,
		ExePath:     w.exe,
		Repo:        "o/r",
		APIBase:     w.srv.URL + "/repos-api",
		CodeloadURL: w.srv.URL + "/codeload",
		ReleaseURL:  w.srv.URL + "/gh",
		ImageCheck:  func(context.Context, string) error { return nil },
	}
	// l'API è montata sotto /repos-api: il server risponde per suffisso, ma
	// il percorso reale è <base>/repos/<repo>/...
	return w
}

func mustWrite(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (w *world) assertUntouched() {
	w.t.Helper()
	if got := readFile(w.t, w.exe); got != oldBin {
		w.t.Errorf("binario cambiato: %q", got)
	}
	if got := readFile(w.t, filepath.Join(w.chart, "values.yaml")); got != "VECCHIO: 1\n" {
		w.t.Errorf("chart locale cambiato: %q", got)
	}
	if got := readFile(w.t, w.cfgFile); !strings.Contains(got, "image_tag: sha-"+oldSHA) {
		w.t.Errorf("config cambiato: %s", got)
	}
	for _, leftover := range []string{w.exe + ".prev", w.exe + ".new", w.chart + ".new", w.chart + ".prev"} {
		if _, err := os.Stat(leftover); err == nil {
			w.t.Errorf("rimasto %s", leftover)
		}
	}
}

func TestUpgradeSuccess(t *testing.T) {
	w := newWorld(t)
	w.helm.newSchem = "identity|6|f\ncore|12|f"
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := readFile(t, w.exe); got != newBin {
		t.Errorf("binario: %q", got)
	}
	if got := readFile(t, filepath.Join(w.chart, "values.yaml")); got != "NUOVO: 1\n" {
		t.Errorf("chart locale: %q", got)
	}
	if got := readFile(t, w.cfgFile); !strings.Contains(got, "image_tag: sha-"+newSHA) || !strings.Contains(got, "host: h") {
		t.Errorf("config: %s", got)
	}
	if res.BackupPath == "" {
		t.Error("manca il backup")
	}
	if _, err := os.Stat(res.BackupPath); err != nil {
		t.Error(err)
	}
	if w.helm.rolled || w.cl.restored {
		t.Error("rollback inatteso")
	}
	// ordine: backup (pg_dump) prima di helm upgrade
	if !w.helm.has("upgrade gitstack") || !w.helm.has("global.image.tag=sha-"+newSHA) || !w.helm.has("--wait") {
		t.Errorf("chiamate helm: %v", w.helm.calls)
	}
	if !w.helm.has("-f ") {
		t.Error("i valori della release non sono riapplicati")
	}
	for _, c := range w.helm.calls {
		if strings.Contains(c, "reuse-values") {
			t.Error("--reuse-values porta con sé i default vecchi")
		}
	}
	if w.cl.dumped == "" {
		t.Error("pg_dump non eseguito")
	}
	if _, err := os.Stat(w.exe + ".prev"); err == nil {
		t.Error(".prev rimasto")
	}
}

func TestDowngradeRefused(t *testing.T) {
	for _, st := range []string{"behind", "diverged"} {
		w := newWorld(t)
		w.compare = st
		res, err := Run(context.Background(), w.o)
		var ref *RefusedError
		if !errors.As(err, &ref) || res.Outcome != Refused {
			t.Fatalf("%s: res=%+v err=%v", st, res, err)
		}
		if st == "behind" && !strings.Contains(err.Error(), "downgrade non supportato") {
			t.Errorf("messaggio: %v", err)
		}
		if w.helm.has("upgrade gitstack ") || w.cl.dumped != "" {
			t.Errorf("%s: toccato qualcosa: %v", st, w.helm.calls)
		}
		w.assertUntouched()
	}
}

func TestIdenticalVersion(t *testing.T) {
	w := newWorld(t)
	w.o.To = "sha-" + newSHA
	w.o.Cfg.ImageTag = "sha-" + newSHA
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != NoChange {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if w.helm.has("upgrade gitstack ") {
		t.Error("helm upgrade su versione identica")
	}
}

func TestChecksRefuse(t *testing.T) {
	cases := map[string]func(w *world){
		"destinazione inesistente": func(w *world) { w.o.To = "v9.9.9" },
		"checksum sbagliato":       func(w *world) { w.badSum = true },
		"binario assente":          func(w *world) { w.noBin = true },
		"immagine mancante": func(w *world) {
			w.o.ImageCheck = func(_ context.Context, img string) error {
				if strings.Contains(img, "gitstack-core") {
					return errors.New("immagine " + img + " non trovata nel registry")
				}
				return nil
			}
		},
		"sistema non sano": func(w *world) { w.healthy = func() bool { return false } },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			mod(w)
			res, err := Run(context.Background(), w.o)
			var ref *RefusedError
			if !errors.As(err, &ref) || res.Outcome != Refused {
				t.Fatalf("res=%+v err=%v", res, err)
			}
			if w.helm.has("upgrade gitstack ") || w.cl.dumped != "" {
				t.Errorf("modificato qualcosa: %v", w.helm.calls)
			}
			w.assertUntouched()
		})
	}
}

func TestDryRun(t *testing.T) {
	w := newWorld(t)
	w.o.DryRun = true
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != NoChange {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if w.helm.has("upgrade gitstack ") || w.cl.dumped != "" {
		t.Error("dry-run ha modificato")
	}
	w.assertUntouched()
}

// Un servizio che non diventa sano, con le migrazioni che non hanno
// toccato il database: rollback del chart, senza restore.
func TestBrokenServiceRollsBackChartOnly(t *testing.T) {
	w := newWorld(t)
	w.healthy = func() bool { return !w.helm.upgraded || w.helm.rolled }
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != RolledBack {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !res.RolledBackChart || res.Restored || w.cl.restored {
		t.Errorf("rollback: chart=%v restore=%v", res.RolledBackChart, res.Restored)
	}
	if !w.helm.has("rollback gitstack 4") || !w.helm.has("--wait") {
		t.Errorf("chiamate: %v", w.helm.calls)
	}
	w.assertUntouched()
	if !strings.Contains(strings.Join(res.Summary, "\n"), "dati intatti") {
		t.Errorf("riepilogo: %v", res.Summary)
	}
}

// Una migrazione rotta: lo schema è cambiato (o sporco) e il chart non
// diventa sano: rollback del chart e restore dal backup.
func TestBrokenMigrationRestoresBackup(t *testing.T) {
	w := newWorld(t)
	w.helm.newSchem = "identity|5|f\ncore|12|t"
	w.helm.onUp = func() error { return errors.New("exit 1") }
	w.healthy = func() bool { return !w.helm.upgraded || w.helm.rolled && w.cl.restored }
	before := w.cl.schema
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != RolledBack {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !res.Restored || !w.cl.restored {
		t.Fatal("restore non eseguito")
	}
	if w.cl.schema != before {
		t.Errorf("schema dopo il rollback: %q, atteso %q", w.cl.schema, before)
	}
	// con il database toccato il rollback non aspetta i pod: li riavvia il restore
	for _, c := range w.helm.calls {
		if strings.HasPrefix(c, "rollback") && strings.Contains(c, "--wait") {
			t.Errorf("rollback con --wait su database toccato: %s", c)
		}
	}
	w.assertUntouched()
	if !strings.Contains(strings.Join(res.Summary, "\n"), "ripristinati dal backup") {
		t.Errorf("riepilogo: %v", res.Summary)
	}
}

func TestUnreadableSchemaCountsAsTouched(t *testing.T) {
	w := newWorld(t)
	w.cl.noSchema = true
	w.helm.onUp = func() error { return errors.New("exit 1") }
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != RolledBack || !res.Restored {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRollbackFailureIsReported(t *testing.T) {
	w := newWorld(t)
	w.helm.onUp = func() error { return errors.New("exit 1") }
	w.helm.failRoll = true
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Broken {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	s := strings.Join(res.Summary, "\n")
	if !strings.Contains(s, "NON riuscito") || !strings.Contains(s, res.BackupPath) {
		t.Errorf("riepilogo: %s", s)
	}
}

func TestInterruptedStillRollsBack(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	w.helm.onUp = func() error { cancel(); return errors.New("signal: interrupt") }
	res, err := Run(ctx, w.o)
	if err != nil || res.Outcome != RolledBack {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	w.assertUntouched()
}

func TestBackupFailureLeavesEverything(t *testing.T) {
	w := newWorld(t)
	w.cl.git = filepath.Join(t.TempDir(), "assente")
	res, err := Run(context.Background(), w.o)
	if err == nil || res.Outcome != Failed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if w.helm.has("upgrade gitstack ") {
		t.Error("helm upgrade dopo un backup fallito")
	}
	w.assertUntouched()
}

func TestResolveTarget(t *testing.T) {
	w := newWorld(t)
	for in, tag := range map[string]string{"": "sha-" + newSHA, "2222222": "sha-" + newSHA, "sha-" + newSHA: "sha-" + newSHA} {
		got, err := w.o.ResolveTarget(context.Background(), in)
		if err != nil || got.Tag != tag {
			t.Errorf("%q: %+v %v", in, got, err)
		}
	}
	for _, bad := range []string{"v9.9.9", "../x", "a b", "sha-zz"} {
		if _, err := w.o.ResolveTarget(context.Background(), bad); err == nil {
			t.Errorf("%q accettato", bad)
		}
	}
}

func TestSplitImage(t *testing.T) {
	cases := []struct{ in, host, repo, ref string }{
		{"ghcr.io/fathormb/gitstack-core:sha-abc", "ghcr.io", "fathormb/gitstack-core", "sha-abc"},
		{"busybox:1.36", "registry-1.docker.io", "library/busybox", "1.36"},
		{"docker.io/nats:2", "registry-1.docker.io", "library/nats", "2"},
		{"localhost:5000/a/b:t", "localhost:5000", "a/b", "t"},
		{"reg.example/a@sha256:ff", "reg.example", "a", "sha256:ff"},
	}
	for _, c := range cases {
		h, r, ref, err := splitImage(c.in)
		if err != nil || h != c.host || r != c.repo || ref != c.ref {
			t.Errorf("%s: %s %s %s %v", c.in, h, r, ref, err)
		}
	}
}

func TestCheckImageTokenFlow(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			_, _ = io.WriteString(rw, `{"token":"T"}`)
		case strings.HasSuffix(r.URL.Path, "/manifests/ok"), strings.HasSuffix(r.URL.Path, "/manifests/missing"):
			if r.Header.Get("Authorization") != "Bearer T" {
				rw.Header().Set("Www-Authenticate", `Bearer realm="`+srv.URL+`/token",service="s",scope="repository:a/b:pull"`)
				rw.WriteHeader(http.StatusUnauthorized)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/missing") {
				http.NotFound(rw, r)
				return
			}
			rw.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()
	o := &Options{}
	host := strings.TrimPrefix(srv.URL, "http://")
	// 127.0.0.1 è trattato come registry http
	if err := o.CheckImage(context.Background(), host+"/a/b:ok"); err != nil {
		t.Errorf("immagine presente: %v", err)
	}
	if err := o.CheckImage(context.Background(), host+"/a/b:missing"); err == nil || !strings.Contains(err.Error(), "non trovata") {
		t.Errorf("immagine assente: %v", err)
	}
}

func TestImagesFromManifests(t *testing.T) {
	out := "spec:\n  containers:\n    - image: \"a/b:1\"\n      name: x\n    - image: c/d:2\n  initContainers:\n  - image: a/b:1\n"
	got := imagesFromManifests(out)
	if len(got) != 2 || got[0] != "a/b:1" || got[1] != "c/d:2" {
		t.Errorf("%v", got)
	}
}

func TestFetchChartRejectsTraversal(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "r/deploy/gitstack/../../evil", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	_ = gz.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) { _, _ = rw.Write(buf.Bytes()) }))
	defer srv.Close()
	o := &Options{CodeloadURL: srv.URL, Repo: "o/r"}
	dest := filepath.Join(t.TempDir(), "c")
	if err := o.FetchChart(context.Background(), &Target{SHA: newSHA}, dest); err == nil {
		t.Error("percorso con .. accettato")
	}
}
