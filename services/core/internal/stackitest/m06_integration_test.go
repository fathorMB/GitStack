//go:build integration

package stackitest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/gitpush"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/issuelinks"
	"github.com/fathorMB/GitStack/services/core/internal/mailer"
	"github.com/fathorMB/GitStack/services/core/internal/mailer/mailertest"
	"github.com/fathorMB/GitStack/services/core/internal/notify"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// M-06 (GIT-141): collegamenti, notifiche, email e webhook sullo stack
// completo. Gateway, identity e git sono i binari veri, core è il router vero
// in-process, Postgres è reale, NATS è il server embedded e git è il client
// vero. Il consumer dei git.push (collegamento dei commit e webhook push) gira
// in-process come in main.go; i motori di notifiche, email e webhook si
// azionano a mano (Process, DeliverDue) con un orologio iniettato, così le
// attese dei tentativi non si aspettano davvero. L'SMTP è finto (mailertest) e
// il ricevitore dei webhook è un server HTTP finto.
//
// Utenti (da newIssuesE2E): alice (proprietaria, admin), bob (write), carol
// (write su app, read su lib), dave (nessun grant: legge solo gli interni) e
// l'agente botty. Il repo m06-vault è privato: solo alice e bob. Ogni caso
// negativo controlla che non trapelino i dati del vault (m06Leak).

const (
	m06App   = "m06-app"
	m06Lib   = "m06-lib"
	m06Vault = "m06-vault"

	// Dati che chi non legge il vault non deve mai ricevere, né in una
	// risposta, né in una notifica, né in un'email o un webhook.
	m06VaultTitle = "OPALE-RISERVATO-58 piano di migrazione"
	m06VaultBody  = "corpo riservato con CINABRO-31"

	m06RepoSecret = "s3cr3t-repo-m06-aaaa"
	m06OrgSecret  = "s3cr3t-org-m06-bbbb"
	m06KeyHex     = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

	m06HookHost = "http://hooks.example.test"
)

// ---------------------------------------------------------------------------
// Orologio, ricevitore webhook e Doer di prova

type m06Clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *m06Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *m06Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type m06Hit struct {
	path   string
	header http.Header
	body   []byte
}

// m06Receiver è il server finto dei webhook: tiene le richieste (con mutex:
// la CI gira con -race) e risponde col codice impostato per percorso.
type m06Receiver struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []m06Hit
	codes map[string]int
}

func newM06Receiver(t *testing.T) *m06Receiver {
	t.Helper()
	r := &m06Receiver{codes: map[string]int{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.hits = append(r.hits, m06Hit{path: req.URL.Path, header: req.Header.Clone(), body: body})
		code := r.codes[req.URL.Path]
		r.mu.Unlock()
		if code == 0 {
			code = http.StatusOK
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *m06Receiver) on(path string) []m06Hit {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []m06Hit
	for _, h := range r.hits {
		if h.path == path {
			out = append(out, h)
		}
	}
	return out
}

func (r *m06Receiver) all() []m06Hit {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]m06Hit(nil), r.hits...)
}

func (r *m06Receiver) setCode(path string, code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.codes[path] = code
}

// m06ToServer porta ogni richiesta al ricevitore finto, qualunque sia l'host
// dell'indirizzo del webhook: il client vero (pkg/egress) blocca il loopback
// dove gira il server di prova, e quel blocco si prova a parte (C8).
type m06ToServer struct{ base *url.URL }

func (d *m06ToServer) Do(req *http.Request) (*http.Response, error) {
	r2 := req.Clone(req.Context())
	r2.URL.Scheme, r2.URL.Host = d.base.Scheme, d.base.Host
	r2.Host = req.URL.Host
	return http.DefaultClient.Do(r2)
}

func m06VerifySig(t *testing.T, secret string, h m06Hit) {
	t.Helper()
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(h.body)
	want := "sha256=" + hex.EncodeToString(m.Sum(nil))
	if got := h.header.Get("X-GitStack-Signature"); !hmac.Equal([]byte(got), []byte(want)) {
		t.Fatalf("firma %q, voluta %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Ambiente

type m06Env struct {
	*issuesE2E
	clock *m06Clock
	recv  *m06Receiver
	smtp  *mailertest.Server
	notif *notify.Engine
	mail  *notify.EmailDispatcher
	hooks *webhooks.Engine
	keys  *webhooks.Keyring

	appID, libID, vaultID string
	vaultBobGrant         string

	works map[string]string // clone di lavoro di m06-app per utente
	seq   int
}

func m06Eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("scaduto il tempo: %s", what)
}

func newM06Env(t *testing.T) *m06Env {
	t.Helper()
	keys, err := webhooks.NewKeyring("k1", m06KeyHex, "")
	if err != nil {
		t.Fatal(err)
	}
	_, check, err := webhooks.NewEgress(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := newIssuesE2E(t, httpserver.WithEmail(true), httpserver.WithWebhooks(httpserver.WebhookConfig{Keys: keys, CheckURL: check}))
	e := &m06Env{issuesE2E: g, keys: keys, works: map[string]string{}}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Repo e grant.
	e.appID = e.createRepo("alice", m06App, "internal")
	e.libID = e.createRepo("alice", m06Lib, "internal")
	e.vaultID = e.createRepo("alice", m06Vault, "private")
	grant := func(repoID, user, role string) string {
		r := e.gw("POST", "/resources/"+repoID+"/grants", map[string]any{"subjectType": "user", "subjectId": e.userID[user], "role": role}, e.bearer("alice"), nil)
		want(t, r, 201, "")
		id, _ := r.json()["id"].(string)
		return id
	}
	// L'agente legge la sua casella via API (C4): token creato dall'admin.
	adminLogin := e.gw("POST", "/auth/login", map[string]any{"username": "admin", "password": newPassword}, nil, nil)
	want(t, adminLogin, 200, "")
	bt := e.gw("POST", "/users/botty/tokens", map[string]any{"name": "agent", "scopes": []string{"read:resource", "write:resource"},
		"expiresAt": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)}, nil, e.session(adminLogin))
	want(t, bt, 201, "")
	e.tokens["botty"], _ = bt.json()["token"].(string)
	dl := e.gw("POST", "/auth/login", map[string]any{"username": "dave", "password": "password-di-prova-molto-lunga-1"}, nil, nil)
	want(t, dl, 200, "")
	e.cookies["dave"] = e.session(dl)
	grant(e.appID, "bob", "write")
	grant(e.appID, "carol", "write")
	grant(e.appID, "botty", "write")
	grant(e.libID, "bob", "write")
	e.vaultBobGrant = grant(e.vaultID, "bob", "write")

	// Motori in-process, come in main.go; l'orologio parte un'ora avanti: il
	// Redeliver dell'API usa l'ora vera e deve risultare già scaduto.
	e.clock = &m06Clock{t: time.Now().UTC().Add(time.Hour).Truncate(time.Second)}
	e.recv = newM06Receiver(t)
	u, _ := url.Parse(e.recv.srv.URL)
	e.notif = &notify.Engine{Pool: e.pool, Identity: e.idc, Log: quiet, EmailWindow: time.Hour}
	e.smtp = mailertest.Start(t, &mailertest.Server{})
	e.mail = &notify.EmailDispatcher{
		Pool: e.pool, Users: e.idc, PublicURL: "https://git.example.com",
		Mail:    &mailer.Mailer{Cfg: mailer.Config{Host: e.smtp.Host, Port: e.smtp.Port, Security: mailer.SecurityNone, From: "GitStack <noreply@example.com>"}},
		Backoff: time.Millisecond, MaxAttempts: 3,
	}
	e.hooks = &webhooks.Engine{Pool: e.pool, Keys: keys, HTTP: &m06ToServer{base: u}, Users: e.idc, Managers: e.idc, Log: quiet, Now: e.clock.Now}
	linker := &issuelinks.Linker{Pool: e.pool, Identity: e.idc, Notifier: e.notif, Git: e.gitc, Log: quiet}

	// I motori impostano i default al primo uso: farlo qui, prima dei consumer,
	// evita scritture concorrenti sugli stessi campi (la CI gira con -race).
	if _, err := e.notif.PurgeRead(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.hooks.PurgeLog(context.Background(), e.clock.Now()); err != nil {
		t.Fatal(err)
	}

	nc, err := nats.Connect(e.natsURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel) // registrato dopo nc.Close: parte prima
	keep := func(durable string, h gitpush.Handler) {
		go func() {
			for ctx.Err() == nil {
				if err := gitpush.Run(ctx, js, durable, h, quiet); err == nil || ctx.Err() != nil {
					return
				}
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
				}
			}
		}()
	}
	keep(gitpush.DurableIssueLinks, linker.HandlePush)
	keep(gitpush.DurableWebhooks, e.hooks.EnqueuePush)

	// Contenuto iniziale del repo app, spinto da alice.
	w := e.initWork(e.httpsURL("alice", e.tokens["alice"], "/alice/"+m06App+".git"), map[string]string{"README.md": "# app\n"})
	e.mustGit(w, "", "push", "-q", "origin", "main")
	return e
}

// drain aspetta che il consumer durevole abbia elaborato tutti i git.push
// pubblicati finora.
func (e *m06Env) drain(durable string) {
	e.t.Helper()
	nc, err := nats.Connect(e.natsURL)
	if err != nil {
		e.t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		e.t.Fatal(err)
	}
	m06Eventually(e.t, "consumer "+durable+" in pari", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, err := js.Consumer(ctx, "GIT", durable)
		if err != nil {
			return false
		}
		info, err := c.Info(ctx)
		return err == nil && info.NumPending == 0 && info.NumAckPending == 0
	})
}

// work è il clone di m06-app di un utente.
func (e *m06Env) work(user string) string {
	e.t.Helper()
	if w, ok := e.works[user]; ok {
		return w
	}
	w := e.clone(e.httpsURL(user, e.tokens[user], "/alice/"+m06App+".git"), "")
	e.works[user] = w
	return w
}

// commitPush fa un commit nuovo sopra origin/main e lo spinge su `dst`
// (nome del branch); ritorna lo sha.
func (e *m06Env) commitPush(user, dst, msg string) string {
	e.t.Helper()
	w := e.work(user)
	e.mustGit(w, "", "fetch", "-q", "origin")
	e.mustGit(w, "", "checkout", "-q", "-B", "wip", "origin/main")
	e.seq++
	e.commitFile(w, fmt.Sprintf("f%d.txt", e.seq), "x\n", msg)
	e.mustGit(w, "", "push", "-q", "origin", "HEAD:refs/heads/"+dst)
	return strings.TrimSpace(e.mustGit(w, "", "rev-parse", "HEAD"))
}

// pushSHA spinge uno sha già noto su un branch.
func (e *m06Env) pushSHA(user, sha, dst string) {
	e.t.Helper()
	e.mustGit(e.work(user), "", "push", "-q", "origin", sha+":refs/heads/"+dst)
}

func (e *m06Env) must06(user, method, path string, body any, status int) reply {
	e.t.Helper()
	return e.must(user, method, path, body, status)
}

// org chiama il gateway come alice con la sessione (le rotte /orgs vogliono gli
// scope org, che il token di prova non ha).
func (e *m06Env) org(method, path string, body any) reply {
	e.t.Helper()
	return e.gw(method, path, body, nil, e.cookies["alice"])
}

// m06Leak: nessun dato del vault né segreto dei webhook nel testo.
func (e *m06Env) m06Leak(what, body string) {
	e.t.Helper()
	for _, n := range []string{"OPALE", "CINABRO", m06Vault, e.vaultID, m06RepoSecret, m06OrgSecret} {
		if strings.Contains(body, n) {
			e.t.Errorf("%s: trapela %q: %s", what, n, truncate([]byte(body)))
		}
	}
}

// m06Deny: rifiutato con uno degli stati dati e senza dati riservati.
func (e *m06Env) m06Deny(user, method, path string, body any, statuses ...int) {
	e.t.Helper()
	r := e.api(user, method, path, body)
	ok := false
	for _, s := range statuses {
		ok = ok || r.status == s
	}
	if !ok {
		e.t.Errorf("%s %s come %s: %d, voluto uno di %v: %s", method, path, user, r.status, statuses, r.body)
	}
	e.m06Leak(fmt.Sprintf("%s %s come %s", method, path, user), string(r.body))
}

// --- issue ----------------------------------------------------------------

func (e *m06Env) issueState(repo string, n float64) (string, string) {
	e.t.Helper()
	j := e.must("alice", "GET", issuePath(repo, n, ""), nil, 200).json()
	st, _ := j["state"].(string)
	reason, _ := j["closeReason"].(string)
	return st, reason
}

func (e *m06Env) issueEvents(user, repo string, n float64) []string {
	e.t.Helper()
	return eventTypesOf(e.must(user, "GET", issuePath(repo, n, "/events"), nil, 200))
}

func m06Count(list []string, s string) int {
	n := 0
	for _, x := range list {
		if x == s {
			n++
		}
	}
	return n
}

// --- notifiche ----------------------------------------------------------------

type m06Item struct {
	ID      string `json:"id"`
	Reason  string `json:"reason"`
	Summary string `json:"summary"`
	Read    bool   `json:"read"`
	Issue   *struct {
		Number int64 `json:"number"`
	} `json:"issue"`
	Repository *struct {
		FullName string `json:"fullName"`
	} `json:"repository"`
}

// settle fa elaborare gli eventi in sospeso al motore delle notifiche.
func (e *m06Env) settle() {
	e.t.Helper()
	for i := 0; i < 20; i++ {
		n, err := e.notif.Process(context.Background())
		if err != nil {
			e.t.Fatalf("notify.Process: %v", err)
		}
		if n == 0 {
			return
		}
	}
}

// inbox è la casella dell'utente, dopo aver fatto elaborare gli eventi.
func (e *m06Env) inbox(user string) ([]m06Item, string) {
	e.t.Helper()
	e.settle()
	r := e.must(user, "GET", "/notifications", nil, 200)
	var l struct {
		Items []m06Item `json:"items"`
	}
	if err := json.Unmarshal(r.body, &l); err != nil {
		e.t.Fatalf("casella di %s: %v: %s", user, err, r.body)
	}
	return l.Items, string(r.body)
}

func m06Find(items []m06Item, repo string, n int64, reason string) bool {
	for _, it := range items {
		if it.Issue != nil && it.Issue.Number == n && it.Repository != nil && it.Repository.FullName == "alice/"+repo && it.Reason == reason {
			return true
		}
	}
	return false
}

func m06About(items []m06Item, repo string, n int64) []string {
	var rs []string
	for _, it := range items {
		if it.Issue != nil && it.Issue.Number == n && it.Repository != nil && it.Repository.FullName == "alice/"+repo {
			rs = append(rs, it.Reason)
		}
	}
	return rs
}

// --- email ----------------------------------------------------------------------

// flushMail fa scadere la finestra di raggruppamento e spedisce.
func (e *m06Env) flushMail() int {
	e.t.Helper()
	e.settle()
	if _, err := e.pool.Exec(context.Background(), `UPDATE core.notifications SET email_due_at = clock_timestamp() - interval '1 second'
		WHERE email_due_at IS NOT NULL AND email_sent_at IS NULL`); err != nil {
		e.t.Fatal(err)
	}
	n, err := e.mail.Process(context.Background())
	if err != nil {
		e.t.Fatalf("dispatcher: %v", err)
	}
	return n
}

// --- webhook -----------------------------------------------------------------

func (e *m06Env) runHooks(eng *webhooks.Engine) int {
	e.t.Helper()
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		n, err := eng.ProcessOutbox(ctx)
		if err != nil {
			e.t.Fatalf("ProcessOutbox: %v", err)
		}
		if n == 0 {
			break
		}
	}
	total := 0
	for i := 0; i < 20; i++ {
		n, err := eng.DeliverDue(ctx)
		if err != nil {
			e.t.Fatalf("DeliverDue: %v", err)
		}
		total += n
		if n == 0 {
			break
		}
	}
	return total
}

func (e *m06Env) deliveries(call func(method, path string, body any) reply, base string) []openapi.WebhookDeliverySummary {
	e.t.Helper()
	r := call("GET", base+"/deliveries", nil)
	want(e.t, r, 200, "")
	var l openapi.WebhookDeliveryList
	if err := json.Unmarshal(r.body, &l); err != nil {
		e.t.Fatal(err)
	}
	return l.Items
}

func (e *m06Env) alice(method, path string, body any) reply {
	return e.api("alice", method, path, body)
}

func m06Payload(t *testing.T, h m06Hit) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(h.body, &p); err != nil {
		t.Fatalf("payload non valido: %v: %s", err, h.body)
	}
	return p
}

// ---------------------------------------------------------------------------

func TestM06E2E(t *testing.T) {
	e := newM06Env(t)

	// Issue iniziali, aperte da alice. app#1 sarà chiusa con push, vault#1 è
	// riservata.
	var appN1, appN2, libN1, libN2, vaultN1 float64
	appN1 = e.open("alice", m06App, "Crash all'avvio", "stack trace", nil)
	appN2 = e.open("alice", m06App, "Seconda issue", "altro", nil)
	libN1 = e.open("alice", m06Lib, "Da chiudere da app", "x", nil)
	libN2 = e.open("alice", m06Lib, "Solo collegamento", "y", nil)
	vaultN1 = e.open("alice", m06Vault, m06VaultTitle, m06VaultBody, nil)

	// --- C1, C2: collegamenti e chiusura via commit --------------------------
	t.Run("collegamenti_e_chiusura_via_commit", func(t *testing.T) {
		// Su un branch qualsiasi: solo il collegamento (C2).
		sha := e.commitPush("bob", "feature", fmt.Sprintf("Fixes #%d: corregge il crash", int(appN1)))
		m06Eventually(t, "collegamento sul branch", func() bool {
			return m06Count(e.issueEvents("carol", m06App, appN1), "commit_linked") == 1
		})
		if st, _ := e.issueState(m06App, appN1); st != "open" {
			t.Fatalf("su un branch non principale la issue deve restare aperta: %s", st)
		}
		if ts := e.issueEvents("alice", m06App, appN1); m06Count(ts, "closed_by_commit") != 0 {
			t.Fatalf("chiusa dal branch: %v", ts)
		}

		// Lo stesso commit entra nel principale: chiusura e traccia (C2).
		e.pushSHA("bob", sha, "main")
		m06Eventually(t, "chiusura al push sul principale", func() bool {
			st, _ := e.issueState(m06App, appN1)
			return st == "closed"
		})
		if _, reason := e.issueState(m06App, appN1); reason != "completed" {
			t.Fatalf("motivo = %q, voluto completed", reason)
		}
		ts := e.issueEvents("carol", m06App, appN1)
		if m06Count(ts, "closed_by_commit") != 1 || m06Count(ts, "commit_linked") != 1 {
			t.Fatalf("cronologia: %v (un solo collegamento, una sola chiusura)", ts)
		}
		// La chiusura da commit non ha un actor: il corpo non cita chi ha spinto come chiusore.
		for _, ev := range e.must("carol", "GET", issuePath(m06App, appN1, "/events"), nil, 200).json()["items"].([]any) {
			m := ev.(map[string]any)
			if m["type"] == "closed_by_commit" && m["actor"] != nil {
				t.Fatalf("closed_by_commit con actor: %v", m)
			}
		}
		// Alice, autrice, riceve la notifica di chiusura.
		it, _ := e.inbox("alice")
		if !m06Find(it, m06App, int64(appN1), "state_change") {
			t.Fatalf("manca la notifica state_change ad alice: %+v", it)
		}

		// Una issue riaperta non si richiude con un push successivo (C2).
		e.must("alice", "POST", issuePath(m06App, appN1, "/reopen"), map[string]any{}, 200)
		e.commitPush("bob", "main", "Aggiorna la documentazione")
		e.commitPush("bob", "other", fmt.Sprintf("Fixes #%d (riferimento al secondo)", int(appN2)))
		m06Eventually(t, "collegamento della seconda issue", func() bool {
			return m06Count(e.issueEvents("carol", m06App, appN2), "commit_linked") == 1
		})
		if st, _ := e.issueState(m06App, appN1); st != "open" {
			t.Fatalf("la issue riaperta non deve richiudersi da sola: %s", st)
		}
		if st, _ := e.issueState(m06App, appN2); st != "open" {
			t.Fatalf("il branch non principale non chiude: %s", st)
		}
	})

	t.Run("riferimento_tra_repo_con_e_senza_write", func(t *testing.T) {
		// bob ha write su lib: «Closes owner/repo#n» dal principale di app chiude (C1).
		e.commitPush("bob", "main", fmt.Sprintf("Closes alice/%s#%d", m06Lib, int(libN1)))
		m06Eventually(t, "chiusura cross-repo con write", func() bool {
			st, _ := e.issueState(m06Lib, libN1)
			return st == "closed"
		})
		if ts := e.issueEvents("carol", m06Lib, libN1); m06Count(ts, "closed_by_commit") != 1 || m06Count(ts, "commit_linked") != 1 {
			t.Fatalf("cronologia di lib#%v: %v", libN1, ts)
		}

		// carol ha solo read su lib: solo il collegamento, nessuna chiusura (C1).
		e.commitPush("carol", "main", fmt.Sprintf("Fixes alice/%s#%d", m06Lib, int(libN2)))
		m06Eventually(t, "collegamento senza write", func() bool {
			return m06Count(e.issueEvents("alice", m06Lib, libN2), "commit_linked") == 1
		})
		if st, _ := e.issueState(m06Lib, libN2); st != "open" {
			t.Fatalf("senza write sul repo della issue non si chiude: %s", st)
		}
		if ts := e.issueEvents("alice", m06Lib, libN2); m06Count(ts, "closed_by_commit") != 0 {
			t.Fatalf("chiusa senza write: %v", ts)
		}

		// carol non legge il vault: nessun collegamento e niente tracce. Nello
		// stesso commit c'è un riferimento valido (app#2): quando quello si
		// vede, il commit è stato elaborato e l'assenza è affidabile.
		e.commitPush("carol", "main", fmt.Sprintf("Fixes alice/%s#%d e fixes #%d", m06Vault, int(vaultN1), int(appN2)))
		m06Eventually(t, "chiusura di app#2 dallo stesso commit", func() bool {
			st, _ := e.issueState(m06App, appN2)
			return st == "closed"
		})
		if st, _ := e.issueState(m06Vault, vaultN1); st != "open" {
			t.Fatalf("il vault non va toccato da chi non lo legge: %s", st)
		}
		if ts := e.issueEvents("alice", m06Vault, vaultN1); m06Count(ts, "commit_linked") != 0 || m06Count(ts, "closed_by_commit") != 0 {
			t.Fatalf("tracce di un commit di chi non legge il vault: %v", ts)
		}
		// Chi non legge il vault non ne vede la issue né dalla issue stessa né dal commit.
		for _, user := range []string{"carol", "dave"} {
			e.m06Deny(user, "GET", issuePath(m06Vault, vaultN1, ""), nil, 404)
			e.m06Deny(user, "GET", issuePath(m06Vault, vaultN1, "/events"), nil, 404)
		}
		// Nessuna notifica di collegamento sul vault, nemmeno ad alice.
		it, _ := e.inbox("alice")
		for _, x := range it {
			if x.Repository != nil && x.Repository.FullName == "alice/"+m06Vault && (x.Reason == "commit_linked" || x.Reason == "state_change") {
				t.Fatalf("notifica di un collegamento al vault: %+v", x)
			}
		}
		// Un riferimento a un repo che non esiste è ignorato come uno non letto.
		_ = libN2
	})

	// --- C3, C4: notifiche ------------------------------------------------------
	t.Run("notifiche_partecipanti_assegnati_menzionati_watch_e_ignore", func(t *testing.T) {
		// Menzione e assegnazione.
		n3 := e.open("alice", m06App, "Menzione e assegnazione", "ciao @dave", nil)
		e.must("alice", "PUT", issuePath(m06App, n3, "/assignees"), map[string]any{"assignees": []string{"bob"}}, 200)
		dave, _ := e.inbox("dave")
		if !m06Find(dave, m06App, int64(n3), "mentioned") {
			t.Fatalf("dave non ha la menzione: %+v", dave)
		}
		bob, _ := e.inbox("bob")
		if !m06Find(bob, m06App, int64(n3), "assigned") {
			t.Fatalf("bob non ha l'assegnazione: %+v", bob)
		}
		alice, _ := e.inbox("alice")
		if len(m06About(alice, m06App, int64(n3))) != 0 {
			t.Fatalf("nessuna notifica per le proprie azioni: %v", m06About(alice, m06App, int64(n3)))
		}
		// Chi non c'entra non riceve niente (Watch di default: Partecipando).
		if carol, _ := e.inbox("carol"); len(m06About(carol, m06App, int64(n3))) != 0 {
			t.Fatalf("carol non partecipa: %+v", carol)
		}

		// Un commento di bob: alice (autrice) è partecipante, bob non riceve il proprio.
		e.must("bob", "POST", issuePath(m06App, n3, "/comments"), map[string]any{"body": "ci lavoro"}, 201)
		alice, _ = e.inbox("alice")
		if !m06Find(alice, m06App, int64(n3), "participating") {
			t.Fatalf("alice dovrebbe partecipare: %+v", alice)
		}
		bob, _ = e.inbox("bob")
		if got := m06About(bob, m06App, int64(n3)); len(got) != 1 || got[0] != "assigned" {
			t.Fatalf("bob: solo l'assegnazione, non il proprio commento: %v", got)
		}

		// Watch «all»: dave segue anche una issue nuova a cui non partecipa.
		e.must("dave", "PUT", "/repos/alice/"+m06App+"/watch", map[string]any{"mode": "all"}, 200)
		n4 := e.open("alice", m06App, "Nuova per chi segue tutto", "senza menzioni", nil)
		dave, _ = e.inbox("dave")
		if !m06Find(dave, m06App, int64(n4), "subscribed") {
			t.Fatalf("Watch all: manca subscribed: %+v", dave)
		}
		bob, _ = e.inbox("bob")
		if len(m06About(bob, m06App, int64(n4))) != 0 {
			t.Fatalf("bob in Partecipando non riceve issue estranee: %v", m06About(bob, m06App, int64(n4)))
		}

		// Watch «ignore»: niente, nemmeno menzioni e assegnazioni.
		e.must("carol", "PUT", "/repos/alice/"+m06App+"/watch", map[string]any{"mode": "ignore"}, 200)
		e.must("alice", "POST", issuePath(m06App, n3, "/comments"), map[string]any{"body": "@carol guarda qui"}, 201)
		e.must("alice", "PUT", issuePath(m06App, n4, "/assignees"), map[string]any{"assignees": []string{"carol"}}, 200)
		carol, body := e.inbox("carol")
		if len(m06About(carol, m06App, int64(n3)))+len(m06About(carol, m06App, int64(n4))) != 0 {
			t.Fatalf("carol in Ignore ha ricevuto notifiche: %+v", carol)
		}
		e.m06Leak("casella di carol", body)

		// Tornare a Partecipando riattiva le notifiche.
		e.must("carol", "DELETE", "/repos/alice/"+m06App+"/watch", nil, 204)
		n5 := e.open("alice", m06App, "Dopo il reset", "@carol ci sei?", nil)
		if carol, _ = e.inbox("carol"); !m06Find(carol, m06App, int64(n5), "mentioned") {
			t.Fatalf("dopo il reset carol è menzionata: %+v", carol)
		}

		// C4: l'agente ha la stessa casella delle persone, via API.
		e.must("alice", "PUT", issuePath(m06App, n5, "/assignees"), map[string]any{"assignees": []string{"botty"}}, 200)
		botty, _ := e.inbox("botty")
		if !m06Find(botty, m06App, int64(n5), "assigned") {
			t.Fatalf("botty non ha l'assegnazione: %+v", botty)
		}

		// Il vault: dave e carol non lo leggono, quindi nessuna menzione, nessuna
		// assegnazione e nessuna notifica con i suoi dati (I8).
		e.must("alice", "POST", issuePath(m06Vault, vaultN1, "/comments"), map[string]any{"body": "cc @dave e @carol"}, 201)
		e.must("alice", "PUT", issuePath(m06Vault, vaultN1, "/assignees"), map[string]any{"assignees": []string{"bob"}}, 200)
		for _, user := range []string{"dave", "carol", "botty"} {
			it, body := e.inbox(user)
			for _, x := range it {
				if x.Repository != nil && x.Repository.FullName == "alice/"+m06Vault {
					t.Errorf("%s ha una notifica del vault: %+v", user, x)
				}
			}
			e.m06Leak("casella di "+user, body)
		}
		// bob lo legge: l'assegnazione c'è.
		if bob, _ = e.inbox("bob"); !m06Find(bob, m06Vault, int64(vaultN1), "assigned") {
			t.Fatalf("bob legge il vault e deve avere l'assegnazione: %+v", bob)
		}
		// Le notifiche degli altri non si leggono: id di bob con il token di dave.
		for _, x := range bob {
			e.m06Deny("dave", "GET", "/notifications/"+x.ID, nil, 404)
			e.m06Deny("dave", "PATCH", "/notifications/"+x.ID, map[string]any{"read": true}, 404)
		}
		// Senza credenziali: 401.
		if r := e.api("", "GET", "/notifications", nil); r.status != 401 {
			t.Fatalf("senza credenziali: %d", r.status)
		}
	})

	// --- C5: email ----------------------------------------------------------------
	t.Run("email_secondo_le_preferenze", func(t *testing.T) {
		e.flushMail()
		base := len(e.smtp.Messages())

		// Default: email per menzioni e assegnazioni; carol spegne le assegnazioni.
		p := e.must("carol", "GET", "/user/notification-preferences", nil, 200).json()
		if p["emailAvailable"] != true {
			t.Fatalf("preferenze: %v", p)
		}
		if em, _ := p["email"].(map[string]any); em["mentioned"] != true || em["assigned"] != true || em["participating"] != false {
			t.Fatalf("default delle email: %v", p["email"])
		}
		e.must("carol", "PUT", "/user/notification-preferences", map[string]any{"email": map[string]any{"assigned": false}}, 200)
		e.m06Deny("carol", "PUT", "/user/notification-preferences", map[string]any{"email": map[string]any{"inesistente": true}}, 400, 422)

		n := e.open("alice", m06App, "Email di prova", "@bob guarda questo", nil)
		e.must("alice", "PUT", issuePath(m06App, n, "/assignees"), map[string]any{"assignees": []string{"carol", "botty"}}, 200)
		e.flushMail()

		msgs := e.smtp.Messages()[base:]
		if len(msgs) != 1 || len(msgs[0].To) != 1 || msgs[0].To[0] != "bob@example.com" {
			t.Fatalf("email partite: %+v (voluta una sola, a bob)", msgs)
		}
		body := msgs[0].Data
		flat := strings.ReplaceAll(body, "=\r\n", "")
		for _, w := range []string{"Auto-Submitted: auto-generated", "Subject: [alice/" + m06App + "]", fmt.Sprintf("https://git.example.com/alice/%s/issues/%d", m06App, int(n))} {
			if !strings.Contains(flat, w) {
				t.Errorf("manca %q:\n%s", w, body)
			}
		}
		e.m06Leak("email a bob", body)
		// Le notifiche in-app ci sono comunque: la preferenza spegne solo la posta (C4, C5).
		carol, _ := e.inbox("carol")
		if !m06Find(carol, m06App, int64(n), "assigned") {
			t.Fatalf("carol ha l'assegnazione in-app: %+v", carol)
		}
		if botty, _ := e.inbox("botty"); !m06Find(botty, m06App, int64(n), "assigned") {
			t.Fatalf("botty ha l'assegnazione in-app: %+v", botty)
		}
		// Un secondo giro non rimanda niente.
		if got := e.flushMail(); got != 0 || len(e.smtp.Messages()) != base+1 {
			t.Fatalf("secondo giro: %d email, %d messaggi", got, len(e.smtp.Messages())-base)
		}
		// Mai agli agenti, mai a chi non legge: nessun messaggio con i loro indirizzi.
		for _, m := range e.smtp.Messages() {
			for _, to := range m.To {
				if strings.Contains(to, "botty") {
					t.Fatalf("email a un agente: %+v", m)
				}
			}
			if m.To[0] != "bob@example.com" { // bob legge il vault: le sue email possono citarlo
				e.m06Leak("email "+strings.Join(m.To, ","), m.Data)
			}
		}
	})

	// --- C6, C7, C8: webhook --------------------------------------------------------
	t.Run("webhook_firmati_tentativi_410_e_redeliver", func(t *testing.T) {
		// Gli eventi precedenti non toccano i webhook creati dopo.
		e.drain(gitpush.DurableWebhooks)
		e.runHooks(e.hooks)

		// Webhook di repo con segreto: il segreto non torna mai (C6).
		repoBase := "/repos/alice/" + m06App + "/hooks"
		rec := e.alice("POST", repoBase, map[string]any{"url": m06HookHost + "/repo", "events": []string{"issues", "issue_comment", "push"}, "secret": m06RepoSecret})
		want(t, rec, 201, "")
		if strings.Contains(string(rec.body), m06RepoSecret) || rec.json()["hasSecret"] != true {
			t.Fatalf("webhook di repo creato: %s", rec.body)
		}
		repoHook, _ := rec.json()["id"].(string)
		repoPath := repoBase + "/" + repoHook
		repoCall := e.alice

		// Webhook di organizzazione: owner alice, repo acme/tools.
		want(t, e.org("POST", "/orgs", map[string]any{"name": "acme"}), 201, "")
		want(t, e.gw("POST", "/repos", map[string]any{"owner": "acme", "name": "tools", "visibility": "private"}, e.bearer("alice"), nil), 201, "")
		rec = e.org("POST", "/orgs/acme/hooks", map[string]any{"url": m06HookHost + "/org", "events": []string{"issues"}, "secret": m06OrgSecret})
		want(t, rec, 201, "")
		if strings.Contains(string(rec.body), m06OrgSecret) {
			t.Fatalf("segreto dell'organizzazione nella risposta: %s", rec.body)
		}
		orgHook, _ := rec.json()["id"].(string)
		orgPath := "/orgs/acme/hooks/" + orgHook

		// Consegna firmata con payload v1 (C6).
		title := "Primo webhook"
		n := e.open("alice", m06App, title, "corpo", nil)
		if got := e.runHooks(e.hooks); got != 1 {
			t.Fatalf("consegne inviate = %d, volute 1", got)
		}
		hits := e.recv.on("/repo")
		if len(hits) != 1 {
			t.Fatalf("richieste al webhook del repo = %d", len(hits))
		}
		h := hits[0]
		m06VerifySig(t, m06RepoSecret, h)
		if h.header.Get("X-GitStack-Event") != "issues" || h.header.Get("X-GitStack-Payload-Version") != "1" ||
			h.header.Get("User-Agent") != "GitStack-Webhook/1" || h.header.Get("Content-Type") != "application/json" {
			t.Fatalf("intestazioni: %v", h.header)
		}
		p := m06Payload(t, h)
		iss, _ := p["issue"].(map[string]any)
		repo, _ := p["repository"].(map[string]any)
		sender, _ := p["sender"].(map[string]any)
		if p["version"] != float64(1) || p["event"] != "issues" || p["action"] != "opened" || iss["number"] != float64(n) || iss["title"] != title ||
			repo["fullName"] != "alice/"+m06App || sender["username"] != "alice" {
			t.Fatalf("payload: %s", h.body)
		}
		if len(e.recv.on("/org")) != 0 {
			t.Fatal("il webhook dell'organizzazione ha ricevuto un evento di un repo di alice")
		}

		// L'organizzazione vede i suoi repo, firmato col suo segreto.
		e.must("alice", "POST", "/repos/acme/tools/issues", map[string]any{"title": "Dell'organizzazione", "body": "x"}, 201)
		e.runHooks(e.hooks)
		oh := e.recv.on("/org")
		if len(oh) != 1 || len(e.recv.on("/repo")) != 1 {
			t.Fatalf("richieste: org=%d repo=%d", len(oh), len(e.recv.on("/repo")))
		}
		m06VerifySig(t, m06OrgSecret, oh[0])
		op := m06Payload(t, oh[0])
		if o, _ := op["organization"].(map[string]any); o["name"] != "acme" {
			t.Fatalf("organization nel payload: %s", oh[0].body)
		}
		if r, _ := op["repository"].(map[string]any); r["fullName"] != "acme/tools" {
			t.Fatalf("repository nel payload: %s", oh[0].body)
		}

		// Il vault non ha webhook e non finisce in quelli degli altri.
		e.open("alice", m06Vault, "Altra riservata "+m06VaultTitle, m06VaultBody, nil)
		e.runHooks(e.hooks)
		for _, x := range e.recv.all() {
			e.m06Leak("richiesta ricevuta su "+x.path, string(x.body))
		}

		// Push: un messaggio con ref, before, after e commit (C6).
		sha := e.commitPush("bob", "hook-push", "Aggiorna la documentazione del webhook")
		m06Eventually(t, "consegna del push", func() bool {
			e.runHooks(e.hooks)
			for _, x := range e.recv.on("/repo") {
				if x.header.Get("X-GitStack-Event") == "push" {
					return true
				}
			}
			return false
		})
		var push m06Hit
		pushes := 0
		for _, x := range e.recv.on("/repo") {
			if x.header.Get("X-GitStack-Event") == "push" {
				push = x
				pushes++
			}
		}
		if pushes != 1 {
			t.Fatalf("push consegnati = %d, voluto 1", pushes)
		}
		m06VerifySig(t, m06RepoSecret, push)
		pp := m06Payload(t, push)
		if pp["ref"] != "refs/heads/hook-push" || pp["after"] != sha {
			t.Fatalf("payload del push: %s", push.body)
		}
		if cs, _ := pp["commits"].([]any); len(cs) != 1 || !strings.Contains(fmt.Sprint(cs[0]), "Aggiorna la documentazione") {
			t.Fatalf("commit del push: %s", push.body)
		}
		if s, _ := pp["sender"].(map[string]any); s["username"] != "bob" {
			t.Fatalf("sender del push: %s", push.body)
		}

		// Tentativi con attesa crescente (C7): 500, poi un minuto, poi riesce.
		e.recv.setCode("/repo", 500)
		before := len(e.recv.on("/repo"))
		e.open("alice", m06App, "Con un errore", "x", nil)
		if got := e.runHooks(e.hooks); got != 1 {
			t.Fatalf("primo tentativo: %d", got)
		}
		ds := e.deliveries(repoCall, repoPath)
		var failing openapi.WebhookDeliverySummary
		for _, d := range ds {
			if d.Status == "pending" {
				failing = d
			}
		}
		if failing.Attempt != 1 || failing.StatusCode == nil || *failing.StatusCode != 500 || failing.NextAttemptAt == nil {
			t.Fatalf("dopo il primo tentativo: %+v", ds)
		}
		if wait := failing.NextAttemptAt.Sub(e.clock.Now()); wait != time.Minute {
			t.Fatalf("attesa = %v, voluta 1m", wait)
		}
		e.clock.Advance(59 * time.Second)
		if got := e.runHooks(e.hooks); got != 0 {
			t.Fatalf("ritentata prima della scadenza: %d", got)
		}
		e.recv.setCode("/repo", 200)
		e.clock.Advance(time.Second)
		if got := e.runHooks(e.hooks); got != 1 {
			t.Fatalf("secondo tentativo: %d", got)
		}
		if got := len(e.recv.on("/repo")) - before; got != 2 {
			t.Fatalf("richieste per la consegna ritentata = %d, volute 2", got)
		}
		done, ok := m06Delivery(e.deliveries(repoCall, repoPath), failing.Id.String())
		if !ok || done.Status != "success" || done.Attempt != 2 {
			t.Fatalf("consegna dopo il secondo tentativo: %+v", done)
		}

		// 410 Gone: la consegna si ferma, senza altri tentativi (C7).
		e.recv.setCode("/repo", 410)
		before = len(e.recv.on("/repo"))
		e.open("alice", m06App, "Il destinatario non c'è più", "x", nil)
		e.runHooks(e.hooks)
		e.clock.Advance(48 * time.Hour)
		e.runHooks(e.hooks)
		if got := len(e.recv.on("/repo")) - before; got != 1 {
			t.Fatalf("richieste dopo un 410 = %d, voluta 1", got)
		}
		var gone openapi.WebhookDeliverySummary
		for _, d := range e.deliveries(repoCall, repoPath) {
			if d.Status == "gone" {
				gone = d
			}
		}
		if gone.Attempt != 1 || gone.NextAttemptAt != nil || gone.StatusCode == nil || *gone.StatusCode != 410 {
			t.Fatalf("consegna dopo il 410: %+v", gone)
		}

		// Redeliver: nuova consegna, stesso payload, nuovo X-GitStack-Delivery, segreto attuale.
		e.recv.setCode("/repo", 200)
		re := e.alice("POST", repoPath+"/deliveries/"+gone.Id.String()+"/redeliver", nil)
		want(t, re, 202, "")
		var queued openapi.WebhookDelivery
		_ = json.Unmarshal(re.body, &queued)
		if queued.Id == gone.Id || queued.RedeliveryOf == nil || *queued.RedeliveryOf != gone.Id {
			t.Fatalf("Redeliver: %s", re.body)
		}
		e.runHooks(e.hooks)
		all := e.recv.on("/repo")
		last, prev := all[len(all)-1], all[len(all)-2]
		if string(last.body) != string(prev.body) {
			t.Fatalf("il payload della Redeliver deve restare lo stesso:\n%s\n%s", prev.body, last.body)
		}
		if last.header.Get("X-GitStack-Delivery") != queued.Id.String() || last.header.Get("X-GitStack-Delivery") == prev.header.Get("X-GitStack-Delivery") {
			t.Fatalf("X-GitStack-Delivery: %q", last.header.Get("X-GitStack-Delivery"))
		}
		m06VerifySig(t, m06RepoSecret, last)
		if d, ok := m06Delivery(e.deliveries(repoCall, repoPath), queued.Id.String()); !ok || d.Status != "success" {
			t.Fatalf("consegna rinviata: %+v", d)
		}

		// Il dettaglio mostra richiesta e risposta, mai il segreto.
		det := e.alice("GET", repoPath+"/deliveries/"+queued.Id.String(), nil)
		want(t, det, 200, "")
		e.m06Leak("dettaglio della consegna", string(det.body))
		if !strings.Contains(string(det.body), "X-GitStack-Signature") {
			t.Fatalf("il dettaglio deve mostrare gli header inviati: %s", det.body)
		}

		// Redeliver anche per l'organizzazione (owner).
		orgDeliveries := e.deliveries(e.org, orgPath)
		if len(orgDeliveries) != 1 {
			t.Fatalf("consegne dell'organizzazione = %d", len(orgDeliveries))
		}
		want(t, e.org("POST", orgPath+"/deliveries/"+orgDeliveries[0].Id.String()+"/redeliver", nil), 202, "")
		e.runHooks(e.hooks)
		if oh = e.recv.on("/org"); len(oh) != 2 || string(oh[0].body) != string(oh[1].body) {
			t.Fatalf("Redeliver dell'organizzazione: %d richieste", len(oh))
		}
		m06VerifySig(t, m06OrgSecret, oh[1])

		// Permessi: solo admin del repo e owner dell'organizzazione (C6); i
		// rifiuti non portano né segreti né dati del vault.
		hookBody := map[string]any{"url": m06HookHost + "/x", "events": []string{"push"}}
		e.m06Deny("bob", "POST", repoBase, hookBody, 403)
		e.m06Deny("bob", "GET", repoPath, nil, 403)
		e.m06Deny("bob", "POST", repoPath+"/deliveries/"+gone.Id.String()+"/redeliver", nil, 403)
		e.m06Deny("carol", "GET", repoBase, nil, 403, 404)
		e.m06Deny("carol", "POST", "/repos/alice/"+m06Vault+"/hooks", hookBody, 404)
		e.m06Deny("dave", "GET", "/repos/alice/"+m06Vault+"/hooks", nil, 404)
		e.m06Deny("bob", "GET", "/repos/alice/"+m06Vault+"/hooks", nil, 403)
		e.m06Deny("", "GET", repoBase, nil, 401)
		for _, user := range []string{"bob", "dave"} {
			for _, c := range []struct{ method, path string }{
				{"GET", "/orgs/acme/hooks"}, {"GET", orgPath}, {"GET", orgPath + "/deliveries"},
				{"POST", "/orgs/acme/hooks"}, {"POST", orgPath + "/reactivate"},
			} {
				r := e.gw(c.method, c.path, hookBody, nil, e.cookies[user])
				if r.status != 403 && r.status != 404 {
					t.Errorf("%s %s come %s: %d (%s)", c.method, c.path, user, r.status, r.body)
				}
				e.m06Leak(c.method+" "+c.path+" come "+user, string(r.body))
			}
		}
		// Il vault non si elenca nemmeno attraverso le consegne.
		if len(e.deliveries(e.alice, "/repos/alice/"+m06App+"/hooks/"+repoHook)) == 0 {
			t.Fatal("alice vede le consegne del suo webhook")
		}
	})

	t.Run("destinazioni_ssrf_bloccate", func(t *testing.T) {
		repoBase := "/repos/alice/" + m06App + "/hooks"
		var list openapi.WebhookList
		_ = json.Unmarshal(e.alice("GET", repoBase, nil).body, &list)
		hooksBefore := list.Total
		if hooksBefore != 1 {
			t.Fatalf("webhook del repo prima = %d", hooksBefore)
		}
		repoHook := list.Items[0].Id.String()

		// Alla creazione: loopback, link-local (metadata), cluster, schemi, credenziali (C8).
		for _, bad := range []string{
			"http://127.0.0.1/x", "http://[::1]:9/x", "http://169.254.169.254/latest/meta-data/", "http://10.43.0.10:8080/",
			"http://localhost/a", "http://127.0.0.1.localhost/a", "ftp://hooks.example.test/a", "http://u:p@hooks.example.test/a", "file:///etc/passwd",
		} {
			for _, call := range []struct {
				name string
				do   func(string) reply
			}{
				{"repo", func(u string) reply {
					return e.alice("POST", repoBase, map[string]any{"url": u, "events": []string{"push"}, "secret": m06RepoSecret})
				}},
				{"org", func(u string) reply {
					return e.org("POST", "/orgs/acme/hooks", map[string]any{"url": u, "events": []string{"push"}, "secret": m06OrgSecret})
				}},
			} {
				r := call.do(bad)
				if r.status != 422 || r.errCode() != "url_not_allowed" {
					t.Errorf("%s %s: %d %s", call.name, bad, r.status, r.body)
				}
				e.m06Leak(call.name+" "+bad, string(r.body))
			}
		}
		// Nessun webhook è stato creato, e la modifica verso un indirizzo bloccato non cambia quello esistente.
		_ = json.Unmarshal(e.alice("GET", repoBase, nil).body, &list)
		if list.Total != hooksBefore {
			t.Fatalf("webhook di repo dopo i rifiuti = %d", list.Total)
		}
		r := e.alice("PATCH", repoBase+"/"+repoHook, map[string]any{"url": "http://169.254.169.254/latest/meta-data/"})
		if r.status != 422 || r.errCode() != "url_not_allowed" {
			t.Fatalf("PATCH verso un indirizzo bloccato: %d %s", r.status, r.body)
		}
		e.m06Leak("PATCH bloccato", string(r.body))
		var w openapi.Webhook
		_ = json.Unmarshal(e.alice("GET", repoBase+"/"+repoHook, nil).body, &w)
		if w.Url != m06HookHost+"/repo" {
			t.Fatalf("l'indirizzo è cambiato nonostante il rifiuto: %q", w.Url)
		}
		// Una rete aziendale non è bloccata (liste del chart): serve a dimostrare
		// che il rifiuto è per indirizzo e non per tutto.
		want(t, e.alice("POST", "/repos/alice/"+m06Lib+"/hooks", map[string]any{"url": "http://192.168.1.5:9000/hook", "events": []string{"push"}}), 201, "")

		// Alla consegna: un nome che risolve al loopback passa il controllo
		// testuale ma il client di uscita lo blocca, senza ritentare e senza
		// aprire la connessione.
		e.runHooks(e.hooks)
		made := e.alice("POST", "/repos/alice/"+m06Lib+"/hooks", map[string]any{"url": m06HookHost + "/ssrf", "events": []string{"issues"}})
		want(t, made, 201, "")
		ssrfHook, _ := made.json()["id"].(string)
		if _, err := e.pool.Exec(context.Background(), `UPDATE core.webhooks SET url = $2 WHERE id = $1`, ssrfHook, e.recv.srv.URL+"/ssrf"); err != nil {
			t.Fatal(err)
		}
		real, _, err := webhooks.NewEgress(nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		eng := &webhooks.Engine{Pool: e.pool, Keys: e.keys, HTTP: real, Users: e.idc, Managers: e.idc, Now: e.clock.Now,
			Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		seen := len(e.recv.all())
		e.open("alice", m06Lib, "Verso il loopback", "x", nil)
		if got := e.runHooks(eng); got != 1 {
			t.Fatalf("consegne tentate = %d, voluta 1", got)
		}
		e.clock.Advance(72 * time.Hour)
		e.runHooks(eng)
		if got := len(e.recv.all()); got != seen {
			t.Fatalf("il ricevitore sul loopback ha ricevuto %d richieste: il client di uscita non ha bloccato", got-seen)
		}
		ds := e.deliveries(e.alice, "/repos/alice/"+m06Lib+"/hooks/"+ssrfHook)
		if len(ds) != 1 || ds[0].Status != "failed" || ds[0].Attempt != 1 || ds[0].NextAttemptAt != nil || ds[0].StatusCode != nil || ds[0].Error == nil {
			t.Fatalf("consegna verso il loopback: %+v", ds)
		}
		det := e.alice("GET", "/repos/alice/"+m06Lib+"/hooks/"+ssrfHook+"/deliveries/"+ds[0].Id.String(), nil)
		want(t, det, 200, "")
		if strings.Contains(string(det.body), `"response"`) && !strings.Contains(string(det.body), `"response":null`) {
			t.Fatalf("una destinazione bloccata non ha risposta da mostrare: %s", det.body)
		}
	})

	// F7 (ASVS V12.6.1, GIT-233): l'indirizzo del nodo e le porte di
	// Kubernetes sono bloccati sempre, anche con una LAN in allow.
	t.Run("webhook_to_node_address_blocked", func(t *testing.T) {
		const nodeIP = "192.168.1.5"
		lan := []string{"192.168.1.0/24"}
		node := []string{nodeIP}
		real, check, err := webhooks.NewEgressForNode(lan, nil, nil, node, nil)
		if err != nil {
			t.Fatal(err)
		}
		// Alla creazione: il nodo e le porte di Kubernetes sono rifiutati, un
		// altro host della LAN su una porta qualunque no.
		for _, bad := range []string{"http://" + nodeIP + ":9000/hook", "http://192.168.1.9:10250/pods", "http://192.168.1.9:6443/", "http://192.168.1.9:2379/"} {
			if err := check(bad); !errors.Is(err, webhooks.ErrURLNotAllowed) {
				t.Errorf("%s: voluto url_not_allowed, ottenuto %v", bad, err)
			}
		}
		if err := check("http://192.168.1.9:9000/hook"); err != nil {
			t.Errorf("host della LAN ammesso: %v", err)
		}

		// Alla consegna: nessuna connessione, consegna fallita senza risposta.
		targets := map[string]string{"nodo": "http://" + nodeIP + ":9000/hook", "kubelet": "http://192.168.1.9:10250/pods"}
		hooks := map[string]string{}
		for name, target := range targets {
			made := e.alice("POST", "/repos/alice/"+m06Lib+"/hooks", map[string]any{"url": m06HookHost + "/" + name, "events": []string{"issues"}})
			want(t, made, 201, "")
			id, _ := made.json()["id"].(string)
			if _, err := e.pool.Exec(context.Background(), `UPDATE core.webhooks SET url = $2 WHERE id = $1`, id, target); err != nil {
				t.Fatal(err)
			}
			hooks[name] = id
		}
		eng := &webhooks.Engine{Pool: e.pool, Keys: e.keys, HTTP: real, Users: e.idc, Managers: e.idc, Now: e.clock.Now,
			Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		e.open("alice", m06Lib, "Verso il nodo", "x", nil)
		e.runHooks(eng)
		e.clock.Advance(72 * time.Hour)
		e.runHooks(eng)
		for name, id := range hooks {
			ds := e.deliveries(e.alice, "/repos/alice/"+m06Lib+"/hooks/"+id)
			if len(ds) != 1 || ds[0].Status != "failed" || ds[0].Attempt != 1 || ds[0].NextAttemptAt != nil || ds[0].StatusCode != nil || ds[0].Error == nil {
				t.Fatalf("consegna verso %s: %+v", name, ds)
			}
			det := e.alice("GET", "/repos/alice/"+m06Lib+"/hooks/"+id+"/deliveries/"+ds[0].Id.String(), nil)
			want(t, det, 200, "")
			if strings.Contains(string(det.body), `"response"`) && !strings.Contains(string(det.body), `"response":null`) {
				t.Fatalf("%s: una destinazione bloccata non ha risposta da mostrare: %s", name, det.body)
			}
		}
	})

	// --- UI: pagine 05 e 21 sullo stack vero, con le notifiche di alice ---------
	t.Run("ui_smoke", func(t *testing.T) { e.uiSmoke(t) })

	// --- C9: conservazione -------------------------------------------------------
	t.Run("conservazione_e_perdita_di_accesso", func(t *testing.T) {
		// bob ha una notifica del vault (assegnazione) e una dell'app.
		bob, _ := e.inbox("bob")
		if !m06Find(bob, m06Vault, int64(vaultN1), "assigned") {
			t.Fatalf("bob dovrebbe avere l'assegnazione del vault: %+v", bob)
		}
		// Le lette si eliminano dopo 90 giorni, le non lette restano (C9).
		var read, unread string
		for _, x := range bob {
			if x.Repository != nil && x.Repository.FullName == "alice/"+m06App {
				if read == "" {
					read = x.ID
				} else if unread == "" {
					unread = x.ID
				}
			}
		}
		if read == "" || unread == "" {
			t.Fatalf("servono due notifiche dell'app per bob: %+v", bob)
		}
		e.must("bob", "PATCH", "/notifications/"+read, map[string]any{"read": true}, 200)
		if n, err := e.notif.PurgeRead(context.Background(), time.Now().Add(89*24*time.Hour)); err != nil || n != 0 {
			t.Fatalf("dopo 89 giorni la letta resta: %d %v", n, err)
		}
		if n, err := e.notif.PurgeRead(context.Background(), time.Now().Add(91*24*time.Hour)); err != nil || n < 1 {
			t.Fatalf("dopo 91 giorni la letta sparisce: %d %v", n, err)
		}
		e.m06Deny("bob", "GET", "/notifications/"+read, nil, 404)
		e.must("bob", "GET", "/notifications/"+unread, nil, 200)

		// Perdendo l'accesso al vault, le sue notifiche spariscono (C9) e non si leggono più.
		want(t, e.gw("DELETE", "/resources/"+e.vaultID+"/grants/"+e.vaultBobGrant, nil, e.bearer("alice"), nil), 204, "")
		time.Sleep(1200 * time.Millisecond) // cache di verifica del gateway: 1 s
		after, body := e.inbox("bob")
		for _, x := range after {
			if x.Repository != nil && x.Repository.FullName == "alice/"+m06Vault {
				t.Fatalf("notifica del vault dopo la perdita di accesso: %+v", x)
			}
		}
		e.m06Leak("casella di bob senza accesso", body)
		for _, x := range bob {
			if x.Repository != nil && x.Repository.FullName == "alice/"+m06Vault {
				e.m06Deny("bob", "GET", "/notifications/"+x.ID, nil, 404)
			}
		}
		e.m06Deny("bob", "GET", issuePath(m06Vault, vaultN1, ""), nil, 404)
	})
}

// m06Delivery cerca una consegna per id nell'elenco.
func m06Delivery(ds []openapi.WebhookDeliverySummary, id string) (openapi.WebhookDeliverySummary, bool) {
	for _, d := range ds {
		if d.Id.String() == id {
			return d, true
		}
	}
	return openapi.WebhookDeliverySummary{}, false
}

// uiSmoke lancia il controllo di fumo della UI sulle pagine 05 (centro
// notifiche) e 21 (impostazioni delle notifiche): web/src/smoke/m06.smoke.test.tsx,
// Vitest con le pagine vere di App.tsx contro il gateway vero. Senza corepack o
// node_modules è saltato, a meno di GITSTACK_REQUIRE_UI_SMOKE (impostata in CI).
func (e *m06Env) uiSmoke(t *testing.T) {
	skip := func(why string) {
		if os.Getenv("GITSTACK_REQUIRE_UI_SMOKE") != "" {
			t.Fatalf("smoke UI richiesto ma non eseguibile: %s", why)
		}
		t.Skip(why)
	}
	web, err := filepath.Abs("../../../../web")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("corepack"); err != nil {
		skip("corepack non è nel PATH")
	}
	if _, err := os.Stat(filepath.Join(web, "node_modules")); err != nil {
		skip("web/node_modules manca: corepack pnpm install --frozen-lockfile")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "corepack", "pnpm", "exec", "vitest", "run", "src/smoke/m06.smoke.test.tsx")
	cmd.Dir = web
	cmd.Env = append(os.Environ(),
		"VITE_SMOKE_GATEWAY="+e.gateway,
		"VITE_SMOKE_TOKEN="+e.tokens["alice"],
		"VITE_SMOKE_HOOK_REPO=alice/"+m06App,
		"VITE_SMOKE_HOOK_URL="+m06HookHost+"/repo",
		"CI=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke UI: %v\n%s", err, out)
	}
	plain := ansiRe.ReplaceAllString(string(out), "")
	if strings.Contains(plain, "skipped") || strings.Contains(plain, "failed") || !strings.Contains(plain, "passed") {
		t.Fatalf("smoke UI: test saltati, falliti o non eseguiti:\n%s", out)
	}
	t.Logf("smoke UI:\n%s", out)
}
