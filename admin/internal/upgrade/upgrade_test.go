package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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
	noSchema bool                          // la query sulle migrazioni fallisce
	ready    func(deployment string) error // esito di WaitReady (nil: sempre pronto)
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
func (f *fakeCluster) WaitReady(_ context.Context, d string) error {
	if f.ready != nil {
		return f.ready(d)
	}
	return nil
}
func (f *fakeCluster) PGExec(_ context.Context, stdin io.Reader, stdout io.Writer, args ...string) error {
	cmd := strings.Join(args, " ")
	switch {
	case strings.Contains(cmd, "pg_dump"):
		f.note("pg_dump")
		f.dumped = f.schema
		_, err := io.WriteString(stdout, "-- dump\n")
		return err
	case strings.Contains(cmd, "repo_package_settings"):
		return nil // elenco dei repo con le immagini nel backup (GIT-325): nessuno
	case strings.Contains(cmd, "core.app_versions"), strings.Contains(cmd, "core.app_volumes"):
		return nil // immagini in uso dalle app (H4): nessuna app
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

func (f *fakeCluster) StatefulSetExists(context.Context, string) (bool, error) { return false, nil }
func (f *fakeCluster) RestartStatefulSet(context.Context, string) error        { return nil }
func (f *fakeCluster) WaitStatefulSetReady(context.Context, string) error      { return nil }

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

	// F9: valori della release (helm get values), contenuto del file dei
	// valori al momento di helm upgrade, e stato dei Secret di servizio.
	values        string
	upgradeValues string
	newSecret     bool // <release>-service-secrets esiste dopo l'upgrade
	legacyDeleted bool
	deleteErr     error

	// GIT-479: dopo helm rollback il Deployment di core resta legato al
	// ServiceAccount che l'upgrade aveva messo (campo deprecato
	// serviceAccount); lo toglie solo la patch merge con i campi a null.
	staleSA bool
	breakSA bool // l'upgrade ha legato core a un ServiceAccount: il rollback lo lascia
}

func (h *fakeHelm) Run(_ context.Context, _ []string, name string, args ...string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if name == "k3s" && len(args) > 0 && args[0] == "kubectl" {
		h.calls = append(h.calls, "kubectl "+strings.Join(args[1:], " "))
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "get secret gitstack-service-secrets"):
			if h.upgraded && h.newSecret {
				return []byte("secret/gitstack-service-secrets\n"), nil
			}
			return []byte("Error from server (NotFound)"), errors.New("exit 1")
		case strings.Contains(joined, "patch deployment gitstack-core") && strings.Contains(joined, `"serviceAccount":null`):
			h.staleSA = false
			return []byte("deployment.apps/gitstack-core patched\n"), nil
		case strings.Contains(joined, "patch deployment"):
			return []byte("deployment.apps/x patched (no change)\n"), nil
		case strings.Contains(joined, "delete secret gitstack-identity-service"):
			if h.deleteErr != nil {
				return []byte("forbidden"), h.deleteErr
			}
			h.legacyDeleted = true
			return []byte("secret \"gitstack-identity-service\" deleted\n"), nil
		}
		return nil, errors.New("kubectl inatteso: " + joined)
	}
	h.calls = append(h.calls, strings.Join(args, " "))
	switch args[0] {
	case "history":
		return []byte(`[{"revision":3,"status":"superseded"},{"revision":4,"status":"deployed"}]`), nil
	case "get":
		if len(args) > 1 && args[1] == "manifest" {
			return []byte("---\nkind: Service\nmetadata:\n  name: gitstack-core\n---\nkind: Deployment\nmetadata:\n  name: gitstack-core\nspec:\n  template:\n    spec:\n      containers: []\n---\nkind: Deployment\nmetadata:\n  name: gitstack-gateway\nspec:\n  template:\n    spec:\n      containers: []\n---\nkind: Deployment\nmetadata:\n  name: gitstack-runner\nspec:\n  template:\n    spec:\n      serviceAccountName: ci-dispatcher\n"), nil
		}
		if h.values != "" {
			return []byte(h.values), nil
		}
		return []byte(`{"global":{"image":{"registry":"mirror.example"}}}`), nil
	case "template":
		return []byte(h.images), nil
	case "upgrade":
		for i, a := range args {
			if a == "-f" && i+1 < len(args) {
				if b, err := os.ReadFile(args[i+1]); err == nil {
					h.upgradeValues = string(b)
				}
				break
			}
		}
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
		h.staleSA = h.upgraded && h.breakSA
		if h.staleSA && strings.Contains(strings.Join(args, " "), "--wait") {
			return []byte("Error: timed out waiting for the condition"), errors.New("exit 1")
		}
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

func chartTarball(t *testing.T, sha string, k3sFile ...string) []byte {
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
	for _, v := range k3sFile {
		if v != "" {
			add("GitStack-"+sha+"/deploy/k3s-version", v+"\n")
		}
	}
	add("GitStack-"+sha+"/deploy/gitstack/Chart.yaml", "name: gitstack\nversion: 0.1.0\n")
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
	k3sFile string // contenuto di deploy/k3s-version nel commit nuovo

	// Firme (V5): la release pubblica i bundle cosign accanto agli artefatti.
	noBundle   bool   // nessun bundle pubblicato (artefatto non firmato)
	signer     string // identità del certificato (vuoto: quella valida)
	binSigned  string // contenuto realmente firmato per il binario (vuoto: newBin)
	chartTgz   []byte // chart firmato pubblicato nella release
	chartVer   string // versione del chart nella release (default 0.1.0)
	chartSign  []byte // contenuto firmato per il chart (nil: chartTgz)
	imgSigners string // file delle immagini firmate (per il cosign finto)

	hitMu sync.Mutex
	hits  []string // percorsi richiesti al server
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, compare: "ahead", chartVer: "0.1.0"}
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
	mustWriteMode(t, w.cfgFile, 0o600, "version: 1\nhost: h\nchart_dir: "+filepath.ToSlash(w.chart)+"\nimage_tag: sha-"+oldSHA+"\n")

	sum := sha256.Sum256([]byte(newBin))
	w.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		w.hitMu.Lock()
		w.hits = append(w.hits, p)
		w.hitMu.Unlock()
		switch {
		case strings.HasSuffix(p, "/commits/"+newSHA), strings.HasSuffix(p, "/commits/main"), strings.HasSuffix(p, "/commits/2222222"):
			_, _ = io.WriteString(rw, `{"sha":"`+newSHA+`"}`)
		case strings.HasSuffix(p, "/commits/v0.2.0"):
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
		case strings.HasSuffix(p, "/gitstack-linux-amd64.sigstore.json"):
			if w.noBundle {
				http.NotFound(rw, r)
				return
			}
			signed := []byte(newBin)
			if w.binSigned != "" {
				signed = []byte(w.binSigned)
			}
			_, _ = rw.Write(fakeBundle(signed, w.identity()))
		case strings.HasSuffix(p, "/gitstack-"+w.chartVer+".tgz"):
			_, _ = rw.Write(w.chartTgz)
		case strings.HasSuffix(p, "/gitstack-"+w.chartVer+".tgz.sigstore.json"):
			if w.noBundle {
				http.NotFound(rw, r)
				return
			}
			signed := w.chartTgz
			if w.chartSign != nil {
				signed = w.chartSign
			}
			_, _ = rw.Write(fakeBundle(signed, w.identity()))
		case strings.HasSuffix(p, "/tar.gz/"+newSHA), strings.HasSuffix(p, "/gitstack/source/"+newSHA+".tar.gz"):
			_, _ = rw.Write(chartTarball(t, newSHA, w.k3sFile))
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
	w.chartTgz = signedChartTgz(t)
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
		V5:          useFakeCosign(t, w),
	}
	// l'API è montata sotto /repos-api: il server risponde per suffisso, ma
	// il percorso reale è <base>/repos/<repo>/...
	return w
}

func mustWrite(t *testing.T, p, s string) {
	t.Helper()
	mustWriteMode(t, p, 0o644, s)
}

// mustWriteMode scrive il file con i permessi dati: config.Load su Unix
// rifiuta un config.yaml diverso da 0600.
func mustWriteMode(t *testing.T, p string, perm os.FileMode, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), perm); err != nil {
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
	if !w.helm.has("rollback gitstack 4") {
		t.Errorf("chiamate: %v", w.helm.calls)
	}
	for _, c := range w.helm.calls {
		if strings.HasPrefix(c, "rollback") && strings.Contains(c, "--wait") {
			t.Errorf("rollback con --wait: %s", c)
		}
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

// GIT-479: l'upgrade rompe solo il gateway (valore non valido), ma ha anche
// legato core a un ServiceAccount nuovo. Il database è toccato dalle
// migrazioni: helm rollback + restore. Il rollback lascia core legato al
// ServiceAccount cancellato: senza la pulizia core non ha pod, WaitReady del
// restore scade e l'esito è Broken (exit 8). Con la pulizia: RolledBack (exit 7).
func TestGatewayOnlyBreakRollsBackAndRestores(t *testing.T) {
	w := newWorld(t)
	w.helm.newSchem = "identity|5|f\ncore|12|f"
	w.helm.breakSA = true
	w.helm.onUp = func() error { return errors.New("exit 1") } // helm upgrade --wait: il gateway non diventa pronto
	w.cl.ready = func(d string) error {
		w.helm.mu.Lock()
		defer w.helm.mu.Unlock()
		if w.helm.staleSA && strings.HasSuffix(d, "-core") {
			return errors.New("timed out waiting for the condition")
		}
		return nil
	}
	w.healthy = func() bool { return !w.helm.upgraded || w.helm.rolled && w.cl.restored && !w.helm.staleSA }
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != RolledBack {
		t.Fatalf("res=%+v err=%v (atteso RolledBack, exit 7)", res, err)
	}
	if !res.RolledBackChart || !res.Restored || !w.cl.restored {
		t.Errorf("rollback: chart=%v restore=%v", res.RolledBackChart, res.Restored)
	}
	if !w.helm.has("get manifest gitstack --revision 4") {
		t.Errorf("manifest della revisione di destinazione non letto: %v", w.helm.calls)
	}
	if w.helm.staleSA {
		t.Error("il ServiceAccount di core è rimasto")
	}
	w.assertUntouched()
}

// GIT-479, ramo senza restore: le migrazioni non hanno cambiato il database,
// ma l'upgrade ha legato core a un ServiceAccount che il rollback cancella.
// Con helm rollback --wait il rollback scadeva (exit 8); senza --wait e con la
// pulizia dopo il rollback l'esito è RolledBack (exit 7), senza restore.
func TestGatewayOnlyBreakRollsBackWithoutRestore(t *testing.T) {
	w := newWorld(t)
	w.helm.breakSA = true
	w.helm.onUp = func() error { return errors.New("exit 1") }
	w.healthy = func() bool { return !w.helm.upgraded || w.helm.rolled && !w.helm.staleSA }
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != RolledBack {
		t.Fatalf("res=%+v err=%v (atteso RolledBack, exit 7)", res, err)
	}
	if !res.RolledBackChart || res.Restored || w.cl.restored {
		t.Errorf("rollback: chart=%v restore=%v", res.RolledBackChart, res.Restored)
	}
	if w.helm.staleSA {
		t.Error("il ServiceAccount di core è rimasto")
	}
	for _, c := range w.helm.calls {
		if strings.HasPrefix(c, "rollback") && strings.Contains(c, "--wait") {
			t.Errorf("rollback con --wait: %s", c)
		}
	}
	w.assertUntouched()
}

// Senza il ServiceAccount stantio il passo non cambia niente di osservabile.
func TestDeploymentsWithoutServiceAccount(t *testing.T) {
	m := "kind: Deployment\nmetadata:\n  name: a\nspec:\n  template:\n    spec:\n      serviceAccountName: x\n---\nkind: Deployment\nmetadata:\n  name: b\nspec:\n  template:\n    spec: {}\n---\nkind: StatefulSet\nmetadata:\n  name: c\n"
	got, err := deploymentsWithoutServiceAccount([]byte(m))
	if err != nil || len(got) != 1 || got[0] != "b" {
		t.Fatalf("got=%v err=%v", got, err)
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

func (f *fakeCluster) Restart(context.Context, string) error { return nil }

// Servizi sani ma /downloads/ca.crt ancora 502 (Ingress non aggiornato):
// l'upgrade non è riuscito e va in rollback del chart.
func TestCAUnavailableRollsBack(t *testing.T) {
	w := newWorld(t)
	w.o.CheckCA = func(context.Context) error {
		if w.helm.rolled {
			return nil
		}
		return errors.New("http://h/downloads/ca.crt: HTTP 502")
	}
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != RolledBack {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !strings.Contains(res.Reason, "ca.crt") {
		t.Errorf("motivo: %q", res.Reason)
	}
}

// Dopo qualche 502 la CA torna e l'upgrade riesce.
func TestCARecoversUpgradeSucceeds(t *testing.T) {
	w := newWorld(t)
	w.o.Timeout = 2 * time.Second
	n := 0
	w.o.CheckCA = func(context.Context) error {
		if n++; n <= 2 {
			return errors.New("HTTP 502")
		}
		return nil
	}
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if n < 3 {
		t.Errorf("CheckCA chiamato %d volte", n)
	}
}

// --- GIT-222: rilascio con versione (--version vX.Y.Z) -----------------------

func TestIsVersionTag(t *testing.T) {
	for in, want := range map[string]bool{
		"v1.0.0": true, "v0.0.0-test.1": true, "v1.2.3-rc.1": true, "v10.20.30": true,
		"1.0.0": false, "v1": false, "v1.0": false, "v01.0.0": false, "v1.0.0+build": false,
		"v1.0.0-": false, "sha-abc": false, "main": false, "v1.0.0/../x": false,
	} {
		if got := IsVersionTag(in); got != want {
			t.Errorf("IsVersionTag(%q)=%v, atteso %v", in, got, want)
		}
	}
}

func TestResolveTargetVersionTag(t *testing.T) {
	w := newWorld(t)
	got, err := w.o.ResolveTarget(context.Background(), "v0.2.0")
	if err != nil || got.Tag != "v0.2.0" || got.Version != "0.2.0" || got.SHA != newSHA {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = w.o.ResolveTarget(context.Background(), "sha-"+newSHA)
	if err != nil || got.Version != "" {
		t.Errorf("una build sha-<commit> non ha versione: %+v %v", got, err)
	}
}

// versionWorld prepara una release vX.Y.Z: chart gitstack-0.2.0.tgz con
// version e appVersion 0.2.0, firmato dalla CI su un tag (identità @refs/tags).
func versionWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.chartVer = "0.2.0"
	w.chartTgz = signedChartTgzVersion(t, "name: gitstack\nversion: 0.2.0\nappVersion: \"0.2.0\"\n")
	w.signer = "https://github.com/o/r/.github/workflows/ci.yml@refs/tags/v0.2.0"
	w.o.To = "v0.2.0"
	return w
}

func TestRunVersionTagVerificaFirmeEInstalla(t *testing.T) {
	w := versionWorld(t)
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	// Gli asset arrivano dalla release v0.2.0, non da sha-<commit>.
	for _, p := range []string{
		"/gh/o/r/releases/download/v0.2.0/gitstack-linux-amd64",
		"/gh/o/r/releases/download/v0.2.0/gitstack-linux-amd64.sigstore.json",
		"/gh/o/r/releases/download/v0.2.0/gitstack-0.2.0.tgz",
		"/gh/o/r/releases/download/v0.2.0/gitstack-0.2.0.tgz.sigstore.json",
	} {
		if w.hitsWith(p) == 0 {
			t.Errorf("non richiesto: %s (richieste: %v)", p, w.hits)
		}
	}
	var blob int
	for _, c := range cosignCalls(t) {
		if strings.HasPrefix(c, "verify-blob ") {
			blob++
		}
	}
	if blob != 2 {
		t.Errorf("verify-blob=%d, attesi 2 (binario e chart): la verifica V5 deve restare attiva", blob)
	}
	if !w.helm.has("global.image.tag=v0.2.0") {
		t.Errorf("tag immagine v0.2.0 non passato a helm: %v", w.helm.calls)
	}
	if got := readFile(t, w.cfgFile); !strings.Contains(got, "image_tag: v0.2.0") {
		t.Errorf("config: %q", got)
	}
}

func TestRunVersionTagSenzaBundleRifiutato(t *testing.T) {
	w := versionWorld(t)
	w.noBundle = true
	runRefused(t, w, "bundle")
}

func TestRunVersionTagBinarioAlteratoRifiutato(t *testing.T) {
	w := versionWorld(t)
	w.binSigned = "altro contenuto"
	runRefused(t, w)
}

func TestRunVersionTagFirmatoDaAltraIdentitaRifiutato(t *testing.T) {
	w := versionWorld(t)
	w.signer = "https://github.com/altro/repo/.github/workflows/ci.yml@refs/tags/v0.2.0"
	runRefused(t, w)
}

func TestRunVersionTagChartDiUnaltraVersioneRifiutato(t *testing.T) {
	w := versionWorld(t)
	// Firmato, ma con version/appVersion diverse dalla release richiesta.
	w.chartTgz = signedChartTgzVersion(t, "name: gitstack\nversion: 0.1.0\nappVersion: \"0.1.0\"\n")
	runRefused(t, w, "non è il chart di questa versione")
}

func TestChartHasVersion(t *testing.T) {
	if !chartHasVersion("version: 1.0.0\r\nappVersion: \"1.0.0\"\r\n", "1.0.0") {
		t.Error("atteso vero")
	}
	for _, y := range []string{"version: 1.0.0\n", "version: 1.0.0\nappVersion: 0.9.0\n", "appVersion: 1.0.0\n"} {
		if chartHasVersion(y, "1.0.0") {
			t.Errorf("accettato %q", y)
		}
	}
}

// F9 (GIT-235): l'upgrade toglie dai valori identity.serviceSecret, che il
// chart nuovo non legge più, e a servizi sani cancella il Secret condiviso.
func TestUpgrade_MigraISegretiDiServizio(t *testing.T) {
	w := newWorld(t)
	w.helm.values = `{"global":{"image":{"registry":"mirror.example"}},"identity":{"serviceSecret":{"existingSecret":"mio-segreto"},"admin":{"existingSecret":"mio-admin"}}}`
	w.helm.newSecret = true
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if strings.Contains(w.helm.upgradeValues, "serviceSecret") || strings.Contains(w.helm.upgradeValues, "mio-segreto") {
		t.Errorf("helm upgrade con identity.serviceSecret: %s", w.helm.upgradeValues)
	}
	if !strings.Contains(w.helm.upgradeValues, "mio-admin") {
		t.Errorf("gli altri valori di identity vanno tenuti: %s", w.helm.upgradeValues)
	}
	if !w.helm.legacyDeleted {
		t.Errorf("Secret condiviso non cancellato: %v", w.helm.calls)
	}
	if !strings.Contains(strings.Join(res.Summary, "\n"), "Secret condiviso gitstack-identity-service") {
		t.Errorf("manca la riga nel resoconto: %v", res.Summary)
	}
}

// Il Secret condiviso si cancella solo dopo che il Secret per chiamante
// esiste, e solo a upgrade riuscito.
func TestUpgrade_SecretCondivisoNonCancellatoSenzaQuelloNuovo(t *testing.T) {
	w := newWorld(t)
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if w.helm.legacyDeleted {
		t.Error("Secret condiviso cancellato senza il Secret nuovo")
	}

	w = newWorld(t)
	w.helm.newSecret = true
	w.helm.onUp = func() error { return errors.New("exit 1") }
	res, _ = Run(context.Background(), w.o)
	if res.Outcome == Upgraded {
		t.Fatal("upgrade riuscito, doveva fallire")
	}
	if w.helm.legacyDeleted {
		t.Error("Secret condiviso cancellato da un upgrade fallito")
	}
}

func TestUpgrade_CancellazioneDelSecretNonFatale(t *testing.T) {
	w := newWorld(t)
	w.helm.newSecret = true
	w.helm.deleteErr = errors.New("exit 1")
	res, err := Run(context.Background(), w.o)
	if err != nil || res.Outcome != Upgraded {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !strings.Contains(strings.Join(res.Summary, "\n"), "delete secret gitstack-identity-service") {
		t.Errorf("manca l'istruzione manuale: %v", res.Summary)
	}
}

func TestDropLegacyServiceSecretValues(t *testing.T) {
	for _, tc := range []struct {
		name, in, want, existing string
		changed                  bool
	}{
		{"niente", `{"a":1}`, `{"a":1}`, "", false},
		{"identity senza serviceSecret", `{"identity":{"admin":{}}}`, `{"identity":{"admin":{}}}`, "", false},
		{"solo serviceSecret", `{"identity":{"serviceSecret":{"existingSecret":"x"}}}`, `{}`, "x", true},
		{"con altri valori", `{"identity":{"serviceSecret":{"existingSecret":"x"},"enabled":true},"b":2}`, `{"b":2,"identity":{"enabled":true}}`, "x", true},
		{"serviceSecret vuoto", `{"identity":{"serviceSecret":{"existingSecret":""}}}`, `{}`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, existing, changed, err := dropLegacyServiceSecretValues([]byte(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if changed != tc.changed || existing != tc.existing {
				t.Fatalf("changed=%v existing=%q", changed, existing)
			}
			var a, b map[string]any
			if err := json.Unmarshal(out, &a); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.want), &b); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(a, b) {
				t.Errorf("out = %s, voluto %s", out, tc.want)
			}
		})
	}
}

func TestImagesFromManifestsRunnerCI(t *testing.T) {
	out := "env:\n  - name: GITSTACK_CORE_CI_IMAGE\n    value: \"ghcr.io/o/gitstack-ci-runner:sha-abc\"\n  - name: X\n    value: y\nimage: a/b:1\n"
	got := imagesFromManifests(out)
	if len(got) != 2 || got[0] != "a/b:1" || got[1] != "ghcr.io/o/gitstack-ci-runner:sha-abc" {
		t.Fatalf("%v", got)
	}
	if !IsGitstackImage(got[1]) {
		t.Fatal("l'immagine del runner va verificata con cosign come le altre")
	}
}
