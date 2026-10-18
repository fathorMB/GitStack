package mirrorpull

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

	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
	"github.com/fathorMB/GitStack/services/git/internal/mirrorpush"
	"github.com/fathorMB/GitStack/services/git/internal/pushevent"
)

const (
	user  = "mirror-bot"
	token = "ghp_TOKENDIPROVA_pull_5a4b3c"
	// Un UUID qualsiasi: il «mirror» come autore degli eventi.
	pusherID = "11111111-2222-4333-8444-555555555555"
)

// cleanEnv: via le GIT_* della sessione, HOME e config globale isolati.
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

type fakePub struct {
	mu     sync.Mutex
	events []gitpush.Payload
}

func (p *fakePub) Publish(_ context.Context, name string, version int, payload any) error {
	if name != gitpush.Name || version != gitpush.Version {
		return errors.New("evento inatteso: " + name)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, payload.(gitpush.Payload))
	return nil
}

func (p *fakePub) all() []gitpush.Payload {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]gitpush.Payload(nil), p.events...)
}

type rig struct {
	t      *testing.T
	home   string
	work   string // clone di lavoro dell'origine
	origin string // repo bare servito in HTTPS
	local  string // repo nudo del servizio (il mirror)
	srv    *httptest.Server
	svc    *Service
	notif  *pushevent.Notifier
	pub    *fakePub
	in     Input

	mu     sync.Mutex
	hits   int
	auths  []string
	hold   chan struct{}
	seen   chan struct{}
	reject string
	public bool // se true il server non chiede credenziali
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

func (r *rig) set(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f()
}

func (r *rig) serverHits() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hits
}

func newRig(t *testing.T) *rig {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git non disponibile")
	}
	base := t.TempDir()
	r := &rig{t: t, home: filepath.Join(base, "home"), work: filepath.Join(base, "work"),
		origin: filepath.Join(base, "srv", "origin.git"), local: filepath.Join(base, "local.git")}
	for _, d := range []string{r.home, r.work, filepath.Dir(r.origin)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Origine: main (c1), topic (c1), feature (c1+c2), tag v1 leggero e v2 annotato.
	r.git(r.work, "init", "-b", "main")
	r.git(r.work, "commit", "--allow-empty", "-m", "uno")
	r.git(r.work, "branch", "topic")
	r.git(r.work, "checkout", "-b", "feature")
	r.git(r.work, "commit", "--allow-empty", "-m", "due")
	r.git(r.work, "checkout", "main")
	r.git(r.work, "tag", "v1")
	r.git(r.work, "tag", "-a", "-m", "annotato", "v2")
	r.git(r.work, "clone", "--bare", r.work, r.origin)
	r.git(base, "init", "--bare", "-b", "master", r.local)

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
		Env: []string{"GIT_PROJECT_ROOT=" + filepath.Dir(r.origin), "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=" + user,
			"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"},
	}
	r.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.hits++
		r.auths = append(r.auths, req.Header.Get("Authorization"))
		hold, seen, reject, public := r.hold, r.seen, r.reject, r.public
		r.mu.Unlock()
		if reject != "" {
			// Un'origine maligna che rimanda indietro la credenziale.
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
		if !public && req.Header.Get("Authorization") != want {
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
	r.pub = &fakePub{}
	r.notif = &pushevent.Notifier{Pub: r.pub, Git: runner, Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))}
	u, _ := url.Parse(r.srv.URL)
	// «example.com» è nel certificato di httptest; l'IP fissato porta al server locale.
	r.in = Input{
		URL: "https://example.com:" + u.Port() + "/origin.git", Username: user, Token: token, IP: "127.0.0.1",
		DefaultBranch: "master", Owner: "alice", Name: "backup", PusherID: pusherID, PusherUsername: "mirror",
	}
	r.svc = &Service{
		Run: runner, Dir: func(string) (string, error) { return r.local, nil }, Events: r.notif, Timeout: 30 * time.Second,
		// Il test parla con 127.0.0.1: la difesa di default lo vieterebbe (ha il suo test).
		CheckIP:  func(netip.Addr) error { return nil },
		ExtraEnv: []string{"GIT_SSL_CAINFO=" + ca, "GIT_SSL_BACKEND=openssl"},
	}
	return r
}

func (r *rig) originSHA(ref string) string { return r.git(r.origin, "rev-parse", ref) }
func (r *rig) localSHA(ref string) string  { return r.git(r.local, "rev-parse", "--verify", "-q", ref) }

func (r *rig) pull() Result {
	r.t.Helper()
	res, err := r.svc.Pull(context.Background(), "repo", r.in)
	if err != nil {
		r.t.Fatalf("Pull: %v", err)
	}
	return res
}

func (r *rig) events() []gitpush.Payload {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !r.notif.Wait(ctx) {
		r.t.Fatal("pubblicazione non finita")
	}
	return r.pub.all()
}

func statusOf(res Result, ref string) RefResult {
	for _, rr := range res.Refs {
		if rr.Ref == ref {
			return rr
		}
	}
	return RefResult{}
}

// ---------------------------------------------------------------------------
// MP1: copia di tutti i branch e i tag

func TestPull_CopiaTuttiBranchETag(t *testing.T) {
	r := newRig(t)
	// HEAD dell'origine è «topic»: il repo locale, vuoto, ha HEAD su master.
	r.git(r.origin, "symbolic-ref", "HEAD", "refs/heads/topic")

	res := r.pull()
	if !res.OK || res.Diverged || res.Error != "" {
		t.Fatalf("esito: %+v", res)
	}
	for _, ref := range []string{"refs/heads/main", "refs/heads/topic", "refs/heads/feature", "refs/tags/v1", "refs/tags/v2"} {
		if got, want := r.localSHA(ref), r.originSHA(ref); got == "" || got != want {
			t.Errorf("%s: locale %q, origine %q", ref, got, want)
		}
		if st := statusOf(res, ref); st.Status != StatusCreated {
			t.Errorf("%s: %+v", ref, st)
		}
	}
	if res.Changed != 5 {
		t.Errorf("changed = %d", res.Changed)
	}
	// Lo spazio temporaneo non lascia ref.
	if out := r.git(r.local, "for-each-ref", IncomingNS); out != "" {
		t.Errorf("ref temporanei rimasti: %s", out)
	}
	// MP8: il branch principale è quello di HEAD dell'origine.
	if res.HeadBranch != "topic" {
		t.Errorf("headBranch = %q", res.HeadBranch)
	}
	if got := r.git(r.local, "symbolic-ref", "HEAD"); got != "refs/heads/topic" {
		t.Errorf("HEAD locale = %s", got)
	}
	// La credenziale è arrivata come Basic user:token.
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+token))
	r.mu.Lock()
	last := r.auths[len(r.auths)-1]
	r.mu.Unlock()
	if last != want {
		t.Errorf("Authorization: %q", last)
	}

	// Evento git.push per tutti i ref creati, con l'autore «mirror».
	evs := r.events()
	if len(evs) != 1 {
		t.Fatalf("eventi: %d", len(evs))
	}
	ev := evs[0]
	if ev.Repo.FullName != "alice/backup" || ev.Repo.ID != "repo" || ev.Repo.DefaultBranch != "topic" ||
		ev.Pusher.ID != pusherID || ev.Pusher.Username != "mirror" {
		t.Errorf("evento: %+v", ev)
	}
	got := map[string]gitpush.RefPush{}
	for _, rp := range ev.Refs {
		got[rp.Ref] = rp
	}
	if len(got) != 5 || got["refs/heads/topic"].Before != gitpush.ZeroSHA || !got["refs/heads/topic"].IsDefaultBranch ||
		got["refs/heads/topic"].After != r.originSHA("refs/heads/topic") {
		t.Errorf("ref dell'evento: %+v", got)
	}

	// Secondo giro senza novità: tutto aggiornato, nessun nuovo evento.
	res = r.pull()
	if !res.OK || res.Changed != 0 {
		t.Fatalf("secondo giro: %+v", res)
	}
	for _, rr := range res.Refs {
		if rr.Status != StatusUpToDate {
			t.Errorf("%s: %s", rr.Ref, rr.Status)
		}
	}
	if len(r.events()) != 1 {
		t.Error("evento pubblicato senza ref cambiati")
	}

	// Terzo giro: un fast-forward e un branch nuovo.
	r.git(r.work, "checkout", "topic")
	r.git(r.work, "commit", "--allow-empty", "-m", "tre")
	r.git(r.work, "branch", "nuovo", "main")
	r.git(r.work, "push", r.origin, "topic", "nuovo")
	res = r.pull()
	if !res.OK || res.Changed != 2 {
		t.Fatalf("terzo giro: %+v", res)
	}
	if st := statusOf(res, "refs/heads/topic"); st.Status != StatusUpdated || st.SHA != r.originSHA("refs/heads/topic") {
		t.Errorf("topic: %+v", st)
	}
	if res.HeadBranch != "" {
		t.Errorf("HEAD ricalcolata su un repo non vuoto: %q", res.HeadBranch)
	}
	evs = r.events()
	if len(evs) != 2 || len(evs[1].Refs) != 2 {
		t.Fatalf("eventi dopo il terzo giro: %+v", evs)
	}
	for _, rp := range evs[1].Refs {
		if rp.Ref == "refs/heads/topic" && (rp.Forced || len(rp.Commits) != 1) {
			t.Errorf("topic nell'evento: %+v", rp)
		}
	}
}

// ---------------------------------------------------------------------------
// MP1: niente sovrascritture, niente cancellazioni

func TestPull_DivergenzaNienteSovrascrittura(t *testing.T) {
	r := newRig(t)
	if res := r.pull(); !res.OK {
		t.Fatalf("primo giro: %+v", res)
	}
	r.events()
	mainBefore := r.localSHA("refs/heads/main")
	featureBefore := r.localSHA("refs/heads/feature")
	v1Before := r.localSHA("refs/tags/v1")

	// Nell'origine: main riscritto (force-push), feature cancellato, v1 spostato;
	// topic avanza in fast-forward e nasce un branch nuovo.
	r.git(r.work, "checkout", "topic")
	r.git(r.work, "commit", "--allow-empty", "-m", "tre")
	r.git(r.work, "branch", "nuovo", "topic")
	r.git(r.work, "push", r.origin, "topic", "nuovo")
	r.git(r.work, "checkout", "main")
	r.git(r.work, "commit", "--amend", "--allow-empty", "-m", "uno riscritto")
	r.git(r.work, "push", "--force", r.origin, "main")
	r.git(r.origin, "update-ref", "-d", "refs/heads/feature")
	r.git(r.origin, "update-ref", "refs/tags/v1", r.originSHA("refs/heads/topic"))

	res := r.pull()
	if res.OK || !res.Diverged {
		t.Fatalf("atteso diverged: %+v", res)
	}
	check := func(ref, reason string) {
		t.Helper()
		st := statusOf(res, ref)
		if st.Status != StatusDiverged || st.Reason != reason {
			t.Errorf("%s: %+v, atteso diverged/%s", ref, st, reason)
		}
	}
	check("refs/heads/main", ReasonNonFastForward)
	check("refs/heads/feature", ReasonDeleted)
	check("refs/tags/v1", ReasonTagMoved)
	// I ref divergenti sono rimasti come erano.
	if got := r.localSHA("refs/heads/main"); got != mainBefore {
		t.Errorf("main sovrascritto: %s, atteso %s", got, mainBefore)
	}
	if got := r.localSHA("refs/heads/feature"); got != featureBefore {
		t.Errorf("feature cancellato o cambiato: %q", got)
	}
	if got := r.localSHA("refs/tags/v1"); got != v1Before {
		t.Errorf("tag sovrascritto: %s", got)
	}
	// Gli altri continuano ad aggiornarsi.
	if got := r.localSHA("refs/heads/topic"); got != r.originSHA("refs/heads/topic") {
		t.Errorf("topic non aggiornato: %s", got)
	}
	if got := r.localSHA("refs/heads/nuovo"); got != r.originSHA("refs/heads/nuovo") {
		t.Errorf("nuovo non creato: %q", got)
	}
	if st := statusOf(res, "refs/tags/v2"); st.Status != StatusUpToDate {
		t.Errorf("v2: %+v", st)
	}
	if res.Changed != 2 || !strings.Contains(res.Error, "refs/heads/main") {
		t.Errorf("changed=%d error=%q", res.Changed, res.Error)
	}
	// L'evento descrive solo ciò che è cambiato.
	evs := r.events()
	if len(evs) != 2 {
		t.Fatalf("eventi: %d", len(evs))
	}
	var names []string
	for _, rp := range evs[1].Refs {
		names = append(names, rp.Ref)
	}
	if strings.Join(names, ",") != "refs/heads/nuovo,refs/heads/topic" {
		t.Errorf("ref nell'evento: %v", names)
	}
	// Un secondo giro resta divergente e non cambia niente.
	res = r.pull()
	if res.OK || !res.Diverged || res.Changed != 0 {
		t.Errorf("secondo giro: %+v", res)
	}
	if r.localSHA("refs/heads/main") != mainBefore {
		t.Error("main cambiato al secondo giro")
	}
}

func TestPull_OrigineIndietroNonRiavvolge(t *testing.T) {
	r := newRig(t)
	if res := r.pull(); !res.OK {
		t.Fatalf("primo giro: %+v", res)
	}
	feature := r.localSHA("refs/heads/feature")
	// L'origine riporta feature indietro: per il mirror è una riscrittura.
	r.git(r.origin, "update-ref", "refs/heads/feature", r.originSHA("refs/heads/main"))
	res := r.pull()
	if st := statusOf(res, "refs/heads/feature"); st.Status != StatusDiverged {
		t.Fatalf("feature: %+v", st)
	}
	if r.localSHA("refs/heads/feature") != feature {
		t.Error("feature riavvolto")
	}
}

// ---------------------------------------------------------------------------
// MP5 / MP3: argv, ambiente, redactor, destinazioni

func TestPull_ArgvSenzaCredenzialiNeForce(t *testing.T) {
	in := Input{URL: "https://github.com/o/r.git", Username: "mirror-user", Token: "ghp_SUPERSEGRETO123", IP: "140.82.112.3",
		DefaultBranch: "main", Owner: "o", Name: "r", PusherID: pusherID, PusherUsername: "mirror"}
	u, ip, err := validate(in)
	if err != nil {
		t.Fatal(err)
	}
	args := fetchArgs(u, ip, in.URL)
	joined := strings.Join(args, " ")
	cred := base64.StdEncoding.EncodeToString([]byte(in.Username + ":" + in.Token))
	for _, secret := range []string{in.Token, cred, in.Username, "Authorization"} {
		if strings.Contains(joined, secret) {
			t.Errorf("argv contiene %q: %s", secret, joined)
		}
	}
	for _, a := range args {
		if a == "--force" || a == "-f" || a == "--prune" || a == "-p" || a == "--prune-tags" || a == "--mirror" || a == "--update-head-ok" ||
			strings.HasPrefix(a, "+") || strings.Contains(a, ":+") || strings.HasPrefix(a, ":") {
			t.Errorf("argomento non ammesso: %q", a)
		}
	}
	// Ogni refspec scrive solo nello spazio temporaneo, mai nei ref veri.
	for _, a := range args {
		if strings.Contains(a, ":") && strings.HasPrefix(a, "refs/") && !strings.Contains(a, ":"+IncomingNS) {
			t.Errorf("refspec verso i ref veri: %q", a)
		}
	}
	for _, want := range []string{"http.curloptResolve=github.com:443:140.82.112.3", "http.followRedirects=false",
		"credential.helper=", "protocol.https.allow=always", "fetch --no-tags", "https://github.com/o/r.git"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv senza %q: %s", want, joined)
		}
	}
	env, err := mirrorpush.NetEnv(in.Username, in.Token, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := strings.Join(env, "\n")
	for _, want := range []string{"GIT_CONFIG_VALUE_0=Authorization: Basic " + cred, "GIT_ASKPASS=\n", "GIT_TERMINAL_PROMPT=0"} {
		if !strings.Contains(e+"\n", want) {
			t.Errorf("ambiente senza %q", want)
		}
	}
	// Origine pubblica: nessuna intestazione.
	pub, err := mirrorpush.NetEnv("", "", nil)
	if err != nil || strings.Contains(strings.Join(pub, "\n"), "Authorization") || strings.Contains(strings.Join(pub, "\n"), "GIT_CONFIG_COUNT") {
		t.Errorf("ambiente pubblico: %v %v", pub, err)
	}
	if _, err := mirrorpush.NetEnv("solo-utente", "", nil); err == nil {
		t.Error("utente senza token ammesso")
	}
}

func TestPull_Validate(t *testing.T) {
	ok := Input{URL: "https://example.com/a.git", Username: "u", Token: "t", IP: "93.184.216.34", DefaultBranch: "main",
		Owner: "o", Name: "n", PusherID: pusherID, PusherUsername: "mirror"}
	if _, _, err := validate(ok); err != nil {
		t.Fatal(err)
	}
	pubIn := ok
	pubIn.Username, pubIn.Token = "", ""
	if _, _, err := validate(pubIn); err != nil {
		t.Errorf("origine pubblica: %v", err)
	}
	for name, mut := range map[string]func(*Input){
		"http":         func(i *Input) { i.URL = "http://example.com/a.git" },
		"ssh":          func(i *Input) { i.URL = "ssh://git@example.com/a.git" },
		"file":         func(i *Input) { i.URL = "file:///etc" },
		"userinfo":     func(i *Input) { i.URL = "https://u:p@example.com/a.git" },
		"opzione":      func(i *Input) { i.URL = "--upload-pack=x" },
		"senza ip":     func(i *Input) { i.IP = "" },
		"solo utente":  func(i *Input) { i.Token = "" },
		"solo token":   func(i *Input) { i.Username = "" },
		"newline":      func(i *Input) { i.Token = "a\nb" },
		"branch -":     func(i *Input) { i.DefaultBranch = "-x" },
		"branch +":     func(i *Input) { i.DefaultBranch = "+main" },
		"senza owner":  func(i *Input) { i.Owner = "" },
		"senza pusher": func(i *Input) { i.PusherID = "" },
	} {
		in := ok
		mut(&in)
		if _, _, err := validate(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestPull_DestinazioniVietateSenzaConnessione(t *testing.T) {
	r := newRig(t)
	r.svc.CheckIP = nil // la difesa di default
	for _, ip := range []string{"127.0.0.1", "::1", "169.254.169.254", "0.0.0.0", "224.0.0.1", "::ffff:127.0.0.1"} {
		in := r.in
		in.IP = ip
		_, err := r.svc.Pull(context.Background(), "repo", in)
		if !errors.Is(err, ErrForbidden) {
			t.Errorf("%s: err = %v, atteso ErrForbidden", ip, err)
		}
	}
	if n := r.serverHits(); n != 0 {
		t.Errorf("il server ha ricevuto %d richieste", n)
	}
	// Una destinazione ammessa non è toccata dalla difesa.
	if err := DefaultCheckIP(netip.MustParseAddr("93.184.216.34")); err != nil {
		t.Errorf("IP pubblico rifiutato: %v", err)
	}
	// Con la difesa di test, il giro parte.
	r.svc.CheckIP = func(netip.Addr) error { return nil }
	if res := r.pull(); !res.OK {
		t.Fatalf("giro: %+v", res)
	}
	// Schema non https: rifiutato prima di ogni connessione.
	before := r.serverHits()
	in := r.in
	in.URL = strings.Replace(in.URL, "https://", "http://", 1)
	if _, err := r.svc.Pull(context.Background(), "repo", in); !errors.Is(err, ErrInvalid) {
		t.Errorf("http: %v", err)
	}
	if r.serverHits() != before {
		t.Error("connessione per un URL http")
	}
}

func TestPull_ErroreDelServerNonPerdeLeCredenziali(t *testing.T) {
	r := newRig(t)
	r.set(func() { r.reject = "rifiutato" })
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	res, err := r.svc.Pull(context.Background(), "repo", r.in)
	if err != nil {
		t.Fatal(err)
	}
	log.Info("esito", "ok", res.OK, "error", res.Error, "refs", res.Refs)
	if res.OK || res.Diverged || res.Error == "" {
		t.Fatalf("atteso un errore: %+v", res)
	}
	cred := base64.StdEncoding.EncodeToString([]byte(user + ":" + token))
	for _, bad := range []string{token, cred, "Authorization"} {
		if strings.Contains(res.Error, bad) || strings.Contains(logs.String(), bad) {
			t.Errorf("%q presente nell'errore o nei log: %q", bad, res.Error)
		}
	}
	// Un fetch fallito non cambia niente e non pubblica eventi.
	if out := r.git(r.local, "for-each-ref"); out != "" {
		t.Errorf("ref dopo un fetch fallito: %s", out)
	}
	if n := len(r.events()); n != 0 {
		t.Errorf("eventi: %d", n)
	}
}

func TestPull_OrigineSenzaCredenziali(t *testing.T) {
	r := newRig(t)
	r.set(func() { r.public = true })
	r.in.Username, r.in.Token = "", ""
	res := r.pull()
	if !res.OK || res.Changed != 5 {
		t.Fatalf("origine pubblica: %+v", res)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.auths {
		if a != "" {
			t.Errorf("intestazione inviata a un'origine pubblica: %q", a)
		}
	}
}

// ---------------------------------------------------------------------------
// Lock per repo e timeout

func TestPull_UnSoloFetchPerRepo(t *testing.T) {
	r := newRig(t)
	r.set(func() {
		r.hold = make(chan struct{})
		r.seen = make(chan struct{}, 1)
	})
	done := make(chan error, 1)
	go func() {
		_, err := r.svc.Pull(context.Background(), "repo", r.in)
		done <- err
	}()
	select {
	case <-r.seen:
	case <-time.After(20 * time.Second):
		t.Fatal("il primo fetch non ha raggiunto il server")
	}
	if _, err := r.svc.Pull(context.Background(), "repo", r.in); !errors.Is(err, ErrBusy) {
		t.Fatalf("secondo fetch concorrente: err = %v, atteso ErrBusy", err)
	}
	// Un altro repo non è bloccato dal lock.
	if !r.svc.acquire("altro") {
		t.Error("il lock è globale, atteso per repo")
	}
	r.svc.release("altro")
	close(r.hold)
	if err := <-done; err != nil {
		t.Fatalf("primo fetch: %v", err)
	}
	if _, err := r.svc.Pull(context.Background(), "repo", r.in); err != nil {
		t.Fatalf("dopo il rilascio: %v", err)
	}
}

func TestPull_Timeout(t *testing.T) {
	r := newRig(t)
	r.set(func() { r.hold = make(chan struct{}) })
	r.svc.Timeout = 2 * time.Second
	start := time.Now()
	res, err := r.svc.Pull(context.Background(), "repo", r.in)
	close(r.hold)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Error != "timeout del fetch" || time.Since(start) > 20*time.Second {
		t.Fatalf("timeout: %+v dopo %s", res, time.Since(start))
	}
	// Il lock è stato rilasciato e nessun ref è stato toccato.
	if !r.svc.acquire("repo") {
		t.Error("lock non rilasciato dopo il timeout")
	}
	r.svc.release("repo")
	if out := r.git(r.local, "for-each-ref"); out != "" {
		t.Errorf("ref dopo il timeout: %s", out)
	}
}

func TestPull_OrigineVuota(t *testing.T) {
	r := newRig(t)
	empty := filepath.Join(filepath.Dir(r.origin), "vuoto.git")
	r.git(r.home, "init", "--bare", empty)
	r.in.URL = strings.Replace(r.in.URL, "origin.git", "vuoto.git", 1)
	res := r.pull()
	if !res.OK || len(res.Refs) != 0 || res.HeadBranch != "" {
		t.Fatalf("origine vuota: %+v", res)
	}
}

// N9: con un proxy aziendale il fetch non ha `-c http.proxy=` né l'IP fissato,
// e il proxy (con le sue credenziali) passa solo dall'ambiente.
func TestPull_ProxyAziendaleSoloDaAmbiente(t *testing.T) {
	in := Input{URL: "https://github.com/o/r.git", Username: "u", Token: "tok", IP: "140.82.112.3",
		DefaultBranch: "main", Owner: "o", Name: "r", PusherID: pusherID, PusherUsername: "mirror"}
	u, ip, err := validate(in)
	if err != nil {
		t.Fatal(err)
	}
	proxy, _ := url.Parse("http://pu:ppw@proxy.corp:3128")
	svcProxy := func(*http.Request) (*url.URL, error) { return proxy, nil }
	p := mirrorpush.ProxyFor(svcProxy, u)
	if p == nil {
		t.Fatal("ProxyFor: nil")
	}
	args := strings.Join(fetchArgsProxy(u, ip, in.URL, true), " ")
	for _, bad := range []string{"ppw", "proxy.corp", "http.proxy=", "curloptResolve"} {
		if strings.Contains(args, bad) {
			t.Errorf("argv contiene %q: %s", bad, args)
		}
	}
	for _, a := range [][]string{fetchArgsProxy(u, ip, in.URL, true), lsRemoteHeadArgs(u, ip, in.URL, true)} {
		if strings.Contains(strings.Join(a, " "), "ppw") {
			t.Errorf("credenziale del proxy in argv: %v", a)
		}
	}
	for _, creds := range [][2]string{{"u", "tok"}, {"", ""}} {
		env, err := mirrorpush.NetEnvProxy(creds[0], creds[1], nil, p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(env, "\n"), "http.proxy\nGIT_CONFIG_VALUE_") && !strings.Contains(strings.Join(env, "\n"), "GIT_CONFIG_VALUE_1=http://pu:ppw@proxy.corp:3128") && !strings.Contains(strings.Join(env, "\n"), "GIT_CONFIG_VALUE_0=http://pu:ppw@proxy.corp:3128") {
			t.Errorf("l'ambiente non porta http.proxy: %v", env)
		}
	}
	// NO_PROXY: nessun proxy e argv come prima.
	if mirrorpush.ProxyFor(func(*http.Request) (*url.URL, error) { return nil, nil }, u) != nil {
		t.Fatal("atteso nessun proxy")
	}
	if a := strings.Join(fetchArgs(u, ip, in.URL), " "); !strings.Contains(a, "curloptResolve") || !strings.Contains(a, "http.proxy=") {
		t.Errorf("senza proxy: %s", a)
	}
}

// TestPull_SizeCap: un'origine più grande del tetto (F18, V12.1.1) non può
// riempire il volume: il fetch fallisce, nessun ref cambia e gli oggetti
// scaricati vengono eliminati. Sotto il tetto l'aggiornamento riesce.
func TestPull_SizeCap(t *testing.T) {
	r := newRig(t)
	// ~3 MiB di dati incomprimibili nell'origine.
	big := make([]byte, 3<<20)
	x := uint32(2463534242)
	for i := range big {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		big[i] = byte(x)
	}
	if err := os.WriteFile(filepath.Join(r.work, "grande.bin"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	r.git(r.work, "add", ".")
	r.git(r.work, "commit", "-m", "grande")
	r.git(r.work, "push", r.origin, "main")

	r.svc.MaxFetchBytes = 256 << 10
	res := r.pull()
	if res.OK || !strings.Contains(res.Error, "supera il tetto di 256 KB") {
		t.Fatalf("il fetch oltre il tetto doveva fallire: %+v", res)
	}
	if out := r.git(r.local, "for-each-ref"); out != "" {
		t.Errorf("ref dopo il tetto superato: %s", out)
	}
	if n := objectsSize(r.local); n > 256<<10 {
		t.Errorf("gli oggetti scaricati dovevano essere eliminati: %d byte rimasti", n)
	}
	if !r.svc.acquire("repo") {
		t.Error("lock non rilasciato")
	}
	r.svc.release("repo")

	// Con un tetto più largo (e con 0 = nessun tetto) l'aggiornamento riesce.
	r.svc.MaxFetchBytes = 64 << 20
	if res := r.pull(); !res.OK {
		t.Fatalf("sotto il tetto doveva riuscire: %+v", res)
	}
}
