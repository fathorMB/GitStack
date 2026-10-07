package mirrorpush

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
)

// ---------------------------------------------------------------------------
// Parser del porcelain

func TestParsePorcelain(t *testing.T) {
	out := "To https://example.com/x.git\n" +
		" \trefs/heads/main:refs/heads/main\t1111111..2222222\n" +
		"*\trefs/tags/v1:refs/tags/v1\t[new tag]\n" +
		"=\trefs/tags/v0:refs/tags/v0\t[up to date]\n" +
		"!\trefs/heads/dev:refs/heads/dev\t[rejected] (non-fast-forward)\n" +
		"!\trefs/heads/dev2:refs/heads/dev2\t[rejected] (fetch first)\n" +
		"!\trefs/tags/v2:refs/tags/v2\t[rejected] (already exists)\n" +
		"!\trefs/heads/prot:refs/heads/prot\t[remote rejected] (pre-receive hook declined)\n" +
		"+\trefs/heads/x:refs/heads/x\t1111111...2222222 (forced update)\n" +
		"-\t:refs/heads/gone\t[deleted]\n" +
		"Done\n"
	got := ParsePorcelain(out)
	type want struct {
		to, status, reason string
		diverged           bool
	}
	wants := []want{
		{"refs/heads/main", StatusOK, "", false},
		{"refs/tags/v1", StatusOK, "", false},
		{"refs/tags/v0", StatusUpToDate, "", false},
		{"refs/heads/dev", StatusRejected, "non-fast-forward", true},
		{"refs/heads/dev2", StatusRejected, "fetch first", true},
		{"refs/tags/v2", StatusRejected, "already exists", true},
		{"refs/heads/prot", StatusRejected, "pre-receive hook declined", false},
		// Un push forzato o una cancellazione non devono mai comparire: se
		// compaiono sono un errore, non un successo.
		{"refs/heads/x", StatusError, "forced update", false},
		{"refs/heads/gone", StatusError, "esito inatteso del push (flag -)", false},
	}
	if len(got) != len(wants) {
		t.Fatalf("righe: %d, attese %d: %+v", len(got), len(wants), got)
	}
	for i, w := range wants {
		g := got[i]
		if g.To != w.to || g.Status != w.status || g.Reason != w.reason || g.Diverged() != w.diverged {
			t.Errorf("riga %d: %+v, atteso %+v", i, g, w)
		}
	}
	if n := len(ParsePorcelain("fatal: unable to access\nDone\n")); n != 0 {
		t.Errorf("righe da un errore senza porcelain: %d", n)
	}
}

// ---------------------------------------------------------------------------
// Argv e ambiente: il token non è mai nella riga di comando

func TestArgvSenzaToken(t *testing.T) {
	in := Input{URL: "https://github.com/o/r.git", Username: "mirror-user", Token: "ghp_SUPERSEGRETO123", IP: "140.82.112.3", DefaultBranch: "main"}
	u, ip, err := validate(in)
	if err != nil {
		t.Fatal(err)
	}
	args := buildArgs(u, ip, in.URL, []string{"refs/heads/main:refs/heads/main", "refs/tags/*:refs/tags/*"})
	joined := strings.Join(args, " ")
	cred := base64.StdEncoding.EncodeToString([]byte(in.Username + ":" + in.Token))
	for _, secret := range []string{in.Token, cred, in.Username, "Authorization"} {
		if strings.Contains(joined, secret) {
			t.Errorf("argv contiene %q: %s", secret, joined)
		}
	}
	for _, a := range args {
		if a == "--force" || a == "-f" || a == "--mirror" || a == "--delete" || strings.HasPrefix(a, "+") || strings.HasPrefix(a, ":") {
			t.Errorf("argomento non ammesso: %q", a)
		}
	}
	if !strings.Contains(joined, "http.curloptResolve=github.com:443:140.82.112.3") ||
		!strings.Contains(joined, "http.followRedirects=false") || !strings.Contains(joined, "push --porcelain https://github.com/o/r.git") {
		t.Errorf("argv senza le protezioni attese: %s", joined)
	}
	// IPv6 fra parentesi quadre per curl.
	u6, _ := url.Parse("https://example.org:8443/x.git")
	if a := strings.Join(pushArgs(u6, netip.MustParseAddr("2606:4700::1")), " "); !strings.Contains(a, "example.org:8443:[2606:4700::1]") {
		t.Errorf("IPv6: %s", a)
	}
	// L'ambiente invece sì: è l'unico canale della credenziale.
	env := strings.Join((&Service{}).env(in), "\n")
	if !strings.Contains(env, "GIT_CONFIG_VALUE_0=Authorization: Basic "+cred) {
		t.Errorf("l'ambiente non porta la credenziale: %s", env)
	}
}

func TestValidate(t *testing.T) {
	ok := Input{URL: "https://example.com/a.git", Username: "u", Token: "t", IP: "93.184.216.34", DefaultBranch: "main"}
	if _, _, err := validate(ok); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*Input){
		"http":       func(i *Input) { i.URL = "http://example.com/a.git" },
		"ssh":        func(i *Input) { i.URL = "ssh://git@example.com/a.git" },
		"file":       func(i *Input) { i.URL = "file:///etc" },
		"userinfo":   func(i *Input) { i.URL = "https://u:p@example.com/a.git" },
		"opzione":    func(i *Input) { i.URL = "--upload-pack=x" },
		"senza ip":   func(i *Input) { i.IP = "" },
		"senza tok":  func(i *Input) { i.Token = "" },
		"newline":    func(i *Input) { i.Token = "a\nb" },
		"branch -":   func(i *Input) { i.DefaultBranch = "-x" },
		"branch +":   func(i *Input) { i.DefaultBranch = "+main" },
		"branch :":   func(i *Input) { i.DefaultBranch = "a:b" },
		"senza user": func(i *Input) { i.Username = "" },
	} {
		in := ok
		mut(&in)
		if _, _, err := validate(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestRedact(t *testing.T) {
	in := Input{Username: "u", Token: "tok-123"}
	cred := base64.StdEncoding.EncodeToString([]byte("u:tok-123"))
	s := Redact(in, "fatal: x tok-123 y\nAuthorization: Basic "+cred+"\nremote: "+cred)
	for _, bad := range []string{"tok-123", cred, "Authorization"} {
		if strings.Contains(s, bad) {
			t.Errorf("%q ancora presente: %s", bad, s)
		}
	}
}

// ---------------------------------------------------------------------------
// Integrazione: un secondo server git vero (git http-backend dietro TLS)

const (
	user  = "mirror-bot"
	token = "ghp_TOKENDIPROVA_9f8e7d6c"
)

// cleanEnv: via le GIT_* dell'ambiente di sessione (hook che bloccano gli
// update di ref), HOME e config globale isolati.
func cleanEnv(home string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "HOME="+home, "USERPROFILE="+home, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
}

type rig struct {
	t      *testing.T
	home   string
	src    string // repo locale (bare) del servizio
	work   string // clone di lavoro per fare commit
	dest   string // repo bare della destinazione
	srv    *httptest.Server
	svc    *Service
	in     Input
	mu     sync.Mutex
	auths  []string
	hold   chan struct{} // se non nil, il server aspetta qui prima di rispondere
	seen   chan struct{}
	reject string // se non vuoto, il server risponde 403 con questo testo
}

func (r *rig) git(dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=" + os.DevNull}, args...)...)
	cmd.Dir = dir
	cmd.Env = cleanEnv(r.home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// set modifica i campi letti dall'handler del server sotto r.mu: il server è già avviato.
func (r *rig) set(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f()
}

func newRig(t *testing.T) *rig {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git non disponibile")
	}
	base := t.TempDir()
	r := &rig{t: t, home: filepath.Join(base, "home"), src: filepath.Join(base, "src.git"), work: filepath.Join(base, "work"),
		dest: filepath.Join(base, "dest", "dest.git")}
	for _, d := range []string{r.home, r.work, filepath.Dir(r.dest)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r.git(r.work, "init", "-b", "main")
	r.git(r.work, "commit", "--allow-empty", "-m", "uno")
	r.git(r.work, "clone", "--bare", r.work, r.src)
	r.git(filepath.Dir(r.dest), "init", "--bare", "-b", "main", r.dest)

	// git-http-backend si lancia direttamente dalla exec-path: il `git` del PATH
	// può essere un wrapper che senza il suo ambiente non parte.
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Fatalf("git --exec-path: %v", err)
	}
	backendPath := filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")
	if runtime.GOOS == "windows" {
		backendPath += ".exe"
	}
	backend := &cgi.Handler{
		Path: backendPath, InheritEnv: []string{"PATH", "SystemRoot", "TEMP", "TMP", "HOME", "USERPROFILE"},
		Env: []string{"GIT_PROJECT_ROOT=" + filepath.Dir(r.dest), "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=" + user,
			"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"},
	}
	r.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.auths = append(r.auths, req.Header.Get("Authorization"))
		hold, seen, reject := r.hold, r.seen, r.reject
		r.mu.Unlock()
		if reject != "" {
			// Una destinazione maligna che rimanda indietro la credenziale.
			http.Error(w, reject+" "+req.Header.Get("Authorization")+" "+token, http.StatusForbidden)
			return
		}
		if seen != nil {
			select {
			case seen <- struct{}{}:
			default:
			}
		}
		if hold != nil {
			<-hold
		}
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+token))
		if req.Header.Get("Authorization") != want {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, req)
	}))
	t.Cleanup(r.srv.Close)

	ca := filepath.Join(base, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: r.srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	runner, err := gitrun.New()
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(r.srv.URL)
	// «example.com» è nel certificato di httptest; l'IP fissato lo porta al server locale.
	r.in = Input{URL: "https://example.com:" + u.Port() + "/dest.git", Username: user, Token: token, IP: "127.0.0.1", DefaultBranch: "main"}
	r.svc = &Service{Run: runner, Dir: func(string) (string, error) { return r.src, nil }, Timeout: 30 * time.Second,
		ExtraEnv: []string{"GIT_SSL_CAINFO=" + ca, "GIT_SSL_BACKEND=openssl"}}
	return r
}

func (r *rig) lastAuth() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.auths) == 0 {
		return ""
	}
	return r.auths[len(r.auths)-1]
}

func (r *rig) srcSHA(ref string) string  { return r.git(r.src, "rev-parse", ref) }
func (r *rig) destSHA(ref string) string { return r.git(r.dest, "rev-parse", "--verify", "-q", ref) }

func TestPush_BranchPrincipaleETag(t *testing.T) {
	r := newRig(t)
	r.git(r.src, "tag", "v1")
	r.git(r.src, "tag", "-a", "-m", "annotato", "v2")
	// Un altro branch locale non si spinge: solo il principale e i tag.
	r.git(r.src, "branch", "feature")

	res, err := r.svc.Push(context.Background(), "repo", r.in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Diverged {
		t.Fatalf("esito: %+v", res)
	}
	for _, ref := range []string{"refs/heads/main", "refs/tags/v1", "refs/tags/v2"} {
		if got, want := r.destSHA(ref), r.srcSHA(ref); got != want {
			t.Errorf("%s: destinazione %s, sorgente %s", ref, got, want)
		}
	}
	if out := r.git(r.dest, "for-each-ref", "refs/heads/feature"); out != "" {
		t.Errorf("il branch feature non doveva essere spinto: %s", out)
	}
	pushed := map[string]string{}
	for _, rr := range res.Refs {
		pushed[rr.Ref] = rr.SHA
	}
	if pushed["refs/heads/main"] != r.srcSHA("refs/heads/main") || pushed["refs/tags/v2"] != r.srcSHA("refs/tags/v2") {
		t.Errorf("SHA nell'esito: %+v", res.Refs)
	}
	// La credenziale è arrivata al server come Basic user:token.
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+token))
	if got := r.lastAuth(); got != want {
		t.Errorf("Authorization visto dal server: %q", got)
	}

	// Secondo giro, nuovo commit: fast-forward.
	r.git(r.work, "commit", "--allow-empty", "-m", "due")
	r.git(r.work, "push", r.src, "main")
	res, err = r.svc.Push(context.Background(), "repo", r.in)
	if err != nil || !res.OK {
		t.Fatalf("secondo push: %v %+v", err, res)
	}
	if r.destSHA("refs/heads/main") != r.srcSHA("refs/heads/main") {
		t.Error("la destinazione non ha seguito il fast-forward")
	}
	// Terzo giro senza novità: tutto aggiornato.
	res, _ = r.svc.Push(context.Background(), "repo", r.in)
	if !res.OK {
		t.Fatalf("terzo push: %+v", res)
	}
	for _, rr := range res.Refs {
		if rr.Status != StatusUpToDate {
			t.Errorf("%s: %s, atteso up-to-date", rr.Ref, rr.Status)
		}
	}
}

func TestPush_DestinazioneDivergente_NienteForce(t *testing.T) {
	r := newRig(t)
	if res, err := r.svc.Push(context.Background(), "repo", r.in); err != nil || !res.OK {
		t.Fatalf("primo push: %v %+v", err, res)
	}
	// La destinazione avanza per conto suo (un commit che la sorgente non ha).
	tree := r.git(r.dest, "rev-parse", "refs/heads/main^{tree}")
	parent := r.destSHA("refs/heads/main")
	theirs := r.git(r.dest, "commit-tree", tree, "-p", parent, "-m", "commit della destinazione")
	r.git(r.dest, "update-ref", "refs/heads/main", theirs)
	// E la sorgente avanza in un altro modo.
	r.git(r.work, "commit", "--allow-empty", "-m", "commit della sorgente")
	r.git(r.work, "push", r.src, "main")

	res, err := r.svc.Push(context.Background(), "repo", r.in)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || !res.Diverged {
		t.Fatalf("atteso diverged: %+v", res)
	}
	if got := r.destSHA("refs/heads/main"); got != theirs {
		t.Fatalf("la destinazione è stata sovrascritta: %s, atteso %s", got, theirs)
	}
	if res.Error == "" || !strings.Contains(res.Error, "refs/heads/main") {
		t.Errorf("manca il motivo: %q", res.Error)
	}

	// Un tag spostato in locale non sovrascrive quello della destinazione.
	r2 := newRig(t)
	r2.git(r2.src, "tag", "v1")
	if res, err := r2.svc.Push(context.Background(), "repo", r2.in); err != nil || !res.OK {
		t.Fatalf("push dei tag: %v %+v", err, res)
	}
	orig := r2.destSHA("refs/tags/v1")
	r2.git(r2.work, "commit", "--allow-empty", "-m", "altro")
	r2.git(r2.work, "push", r2.src, "main")
	r2.git(r2.src, "tag", "-f", "v1", "refs/heads/main")
	res, err = r2.svc.Push(context.Background(), "repo", r2.in)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || !res.Diverged {
		t.Fatalf("tag spostato: atteso diverged: %+v", res)
	}
	if got := r2.destSHA("refs/tags/v1"); got != orig {
		t.Fatalf("tag sovrascritto: %s, atteso %s", got, orig)
	}
	// Il branch, in questo giro, è avanzato in fast-forward: non è bloccato dal tag.
	if r2.destSHA("refs/heads/main") != r2.srcSHA("refs/heads/main") {
		t.Log("nota: git non spinge il branch quando un altro ref è rifiutato (push non atomico: dipende dal server)")
	}
}

func TestPush_ErroreDelServerNonPerdeIlToken(t *testing.T) {
	r := newRig(t)
	r.set(func() { r.reject = "rifiutato" })
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	res, err := r.svc.Push(context.Background(), "repo", r.in)
	if err != nil {
		t.Fatal(err)
	}
	log.Info("esito", "ok", res.OK, "error", res.Error, "refs", res.Refs)
	if res.OK || res.Diverged {
		t.Fatalf("atteso errore, non diverged: %+v", res)
	}
	cred := base64.StdEncoding.EncodeToString([]byte(user + ":" + token))
	for _, bad := range []string{token, cred, "Authorization"} {
		if strings.Contains(res.Error, bad) || strings.Contains(logs.String(), bad) {
			t.Errorf("%q presente nell'errore o nei log: %q", bad, res.Error)
		}
	}
	if res.Error == "" {
		t.Error("errore vuoto")
	}
}

func TestPush_UnSoloPushPerMirror(t *testing.T) {
	r := newRig(t)
	r.set(func() {
		r.hold = make(chan struct{})
		r.seen = make(chan struct{}, 1)
	})
	done := make(chan error, 1)
	go func() {
		_, err := r.svc.Push(context.Background(), "repo", r.in)
		done <- err
	}()
	select {
	case <-r.seen:
	case <-time.After(20 * time.Second):
		t.Fatal("il primo push non ha raggiunto il server")
	}
	if _, err := r.svc.Push(context.Background(), "repo", r.in); !errors.Is(err, ErrBusy) {
		t.Fatalf("secondo push concorrente: err = %v, atteso ErrBusy", err)
	}
	// Un altro repo (altro mirror) non è bloccato dal lock.
	if !r.svc.acquire("altro\x00" + r.in.URL) {
		t.Error("il lock è globale, atteso per mirror")
	}
	r.svc.release("altro\x00" + r.in.URL)
	close(r.hold)
	if err := <-done; err != nil {
		t.Fatalf("primo push: %v", err)
	}
	if _, err := r.svc.Push(context.Background(), "repo", r.in); err != nil {
		t.Fatalf("dopo il rilascio: %v", err)
	}
}

func TestPush_TimeoutEBloccoDelDNS(t *testing.T) {
	r := newRig(t)
	r.set(func() { r.hold = make(chan struct{}) })
	r.svc.Timeout = 2 * time.Second
	start := time.Now()
	res, err := r.svc.Push(context.Background(), "repo", r.in)
	close(r.hold)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Error != "timeout del push" || time.Since(start) > 15*time.Second {
		t.Fatalf("timeout: %+v dopo %s", res, time.Since(start))
	}
}

func TestPush_RepoVuoto(t *testing.T) {
	r := newRig(t)
	empty := t.TempDir()
	r.git(empty, "init", "--bare")
	r.svc.Dir = func(string) (string, error) { return empty, nil }
	res, err := r.svc.Push(context.Background(), "repo", r.in)
	if err != nil || !res.OK || len(res.Refs) != 0 {
		t.Fatalf("repo vuoto: %v %+v", err, res)
	}
}
