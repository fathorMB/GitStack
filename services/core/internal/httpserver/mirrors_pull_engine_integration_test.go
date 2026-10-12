//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/mirrors"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// M-09/A3 (GIT-197): motore e API del mirror in pull (MP1-MP8) con un servizio
// git finto. L'aggiornamento prudente vero (fetch in refs/mirror-incoming,
// fast-forward soltanto) è in services/git (mirrorpull, GIT-196).

const pullURL = "https://mirror.example.test/org/code.git"

type pullCall struct {
	repo uuid.UUID
	in   gitclient.MirrorPullInput
}

// fakePuller è il servizio git finto.
type fakePuller struct {
	mu      sync.Mutex
	calls   []pullCall
	answer  func(n int, in gitclient.MirrorPullInput) (gitclient.MirrorPullResult, error)
	block   chan struct{}
	started chan struct{}
}

func (f *fakePuller) MirrorPull(_ context.Context, _ trust.Identity, repoID uuid.UUID, in gitclient.MirrorPullInput) (gitclient.MirrorPullResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, pullCall{repo: repoID, in: in})
	n := len(f.calls)
	answer, block, started := f.answer, f.block, f.started
	f.mu.Unlock()
	if started != nil {
		started <- struct{}{}
	}
	if block != nil {
		<-block
	}
	if answer != nil {
		return answer(n, in)
	}
	return gitclient.MirrorPullResult{OK: true, Changed: 1, Refs: []gitclient.MirrorPullRef{
		{Ref: "refs/heads/main", Status: "updated", SHA: strings.Repeat("a", 40)},
		{Ref: "refs/tags/v1", Status: "up-to-date", SHA: strings.Repeat("b", 40)},
	}}, nil
}

func (f *fakePuller) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakePuller) last() pullCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func (f *fakePuller) setAnswer(fn func(n int, in gitclient.MirrorPullInput) (gitclient.MirrorPullResult, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answer = fn
}

type pullEnv struct {
	*mirrorEnv
	puller *fakePuller
	pull   *mirrors.PullEngine
}

func newPullEnv(t *testing.T) *pullEnv {
	t.Helper()
	m := newMirrorEnv(t)
	p := &fakePuller{}
	pe := &mirrors.PullEngine{Pool: m.pool, Keys: m.keys, Git: p, Egress: m.eng.Egress, Managers: m.hid, Now: m.clock.Now, Lease: 5 * time.Minute}
	return &pullEnv{mirrorEnv: m, puller: p, pull: pe}
}

func (e *pullEnv) enable(repo, url, body string) openapi.PullMirror {
	e.t.Helper()
	if body == "" {
		body = fmt.Sprintf(`{"url":%q,"username":"gh-bot","token":%q}`, url, mirrorToken)
	}
	rec := e.do(http.MethodPost, "/repos/alice/"+repo+"/pull-mirror", "alice", body)
	e.want(rec, http.StatusCreated, "")
	return e.decodePull(rec.Body.Bytes())
}

func (e *pullEnv) decodePull(b []byte) openapi.PullMirror {
	e.t.Helper()
	if strings.Contains(string(b), mirrorToken) {
		e.t.Fatalf("il token non deve tornare mai: %s", b)
	}
	var m openapi.PullMirror
	if err := json.Unmarshal(b, &m); err != nil {
		e.t.Fatalf("mirror in pull non valido: %v: %s", err, b)
	}
	return m
}

func (e *pullEnv) getPull(repo string) openapi.PullMirror {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/repos/alice/"+repo+"/pull-mirror", "alice", "")
	e.want(rec, http.StatusOK, "")
	return e.decodePull(rec.Body.Bytes())
}

func (e *pullEnv) pullRuns(repo string) openapi.PullMirrorRunList {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/repos/alice/"+repo+"/pull-mirror/runs", "alice", "")
	e.want(rec, http.StatusOK, "")
	if strings.Contains(rec.Body.String(), mirrorToken) {
		e.t.Fatalf("il token nel registro: %s", rec.Body.String())
	}
	var l openapi.PullMirrorRunList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		e.t.Fatal(err)
	}
	return l
}

func (e *pullEnv) processPull() int {
	e.t.Helper()
	n, err := e.pull.ProcessDue(context.Background())
	if err != nil {
		e.t.Fatalf("ProcessDue: %v", err)
	}
	return n
}

func (e *pullEnv) syncNow(repo string) {
	e.t.Helper()
	e.want(e.do(http.MethodPost, "/repos/alice/"+repo+"/pull-mirror/sync", "alice", ""), http.StatusAccepted, "")
}

func (e *pullEnv) nextAttempt(id uuid.UUID) *time.Time {
	e.t.Helper()
	var t *time.Time
	if err := e.pool.QueryRow(context.Background(), `SELECT next_attempt_at FROM core.repo_pull_mirrors WHERE id = $1`, id).Scan(&t); err != nil {
		e.t.Fatal(err)
	}
	return t
}

func (e *pullEnv) countRows(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func failingPull(msg string) func(int, gitclient.MirrorPullInput) (gitclient.MirrorPullResult, error) {
	return func(int, gitclient.MirrorPullInput) (gitclient.MirrorPullResult, error) {
		return gitclient.MirrorPullResult{OK: false, Error: msg}, nil
	}
}

// MP3, MP5: attivazione su un repo esistente, token cifrato e mai restituito,
// validazione dell'origine (C8), permessi admin (403/404 come le P-rules).
func TestPullMirror_API_AttivazioneEPermessi(t *testing.T) {
	e := newPullEnv(t)
	e.repo("app", true) // internal: bob la legge ma non è admin

	m := e.enable("app", pullURL, "")
	if !m.HasToken || m.State != "pending" || m.Mode != "mirror" || m.IntervalSeconds != 600 || m.Username == nil || *m.Username != "gh-bot" ||
		m.Origin != pullURL || m.NextSyncAt == nil || len(m.DivergedRefs) != 0 || m.CreatedBy == nil {
		t.Fatalf("mirror creato: %+v", m)
	}
	var ct []byte
	var keyID string
	if err := e.pool.QueryRow(context.Background(), `SELECT token_ciphertext, token_key_id FROM core.repo_pull_mirrors WHERE id = $1`, m.Id).Scan(&ct, &keyID); err != nil {
		t.Fatal(err)
	}
	if len(ct) == 0 || strings.Contains(string(ct), "ghp_") || keyID != "k1" {
		t.Fatalf("token nel database: %q (%s)", ct, keyID)
	}
	// Il repo mostra il campo mirror (MP2/MP7).
	rec := e.do(http.MethodGet, "/repos/alice/app", "alice", "")
	if !strings.Contains(rec.Body.String(), `"mirror"`) || !strings.Contains(rec.Body.String(), pullURL) {
		t.Fatalf("campo mirror del repo: %s", rec.Body.String())
	}
	// Un secondo mirror in pull sullo stesso repo: 409.
	e.want(e.do(http.MethodPost, "/repos/alice/app/pull-mirror", "alice", fmt.Sprintf(`{"url":%q}`, "https://other.example.test/x.git")), http.StatusConflict, "conflict")

	// Modifica: intervallo e token (la risposta non lo riporta); toglie l'utente
	// con una stringa vuota solo insieme al token.
	rec = e.do(http.MethodPatch, "/repos/alice/app/pull-mirror", "alice", `{"intervalSeconds":900,"token":"nuovo-token-segreto"}`)
	e.want(rec, http.StatusOK, "")
	if strings.Contains(rec.Body.String(), "nuovo-token") {
		t.Fatalf("il nuovo token è tornato: %s", rec.Body.String())
	}
	if got := e.decodePull(rec.Body.Bytes()); got.IntervalSeconds != 900 || !got.HasToken {
		t.Fatalf("dopo la modifica: %+v", got)
	}
	e.want(e.do(http.MethodPatch, "/repos/alice/app/pull-mirror", "alice", `{"username":""}`), http.StatusUnprocessableEntity, "validation_failed")
	rec = e.do(http.MethodPatch, "/repos/alice/app/pull-mirror", "alice", `{"username":"","token":""}`)
	e.want(rec, http.StatusOK, "")
	if got := e.decodePull(rec.Body.Bytes()); got.HasToken || got.Username != nil {
		t.Fatalf("credenziali tolte: %+v", got)
	}
	e.want(e.do(http.MethodPatch, "/repos/alice/app/pull-mirror", "alice", `{}`), http.StatusBadRequest, "")

	// Permessi: bob legge il repo ma non è admin → 403 su ogni operazione.
	base := "/repos/alice/app/pull-mirror"
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, base, ""}, {http.MethodPost, base, `{"url":"https://x.example.test/a.git"}`},
		{http.MethodPatch, base, `{"intervalSeconds":600}`}, {http.MethodPost, base + "/sync", ""},
		{http.MethodPost, base + "/pause", ""}, {http.MethodPost, base + "/resume", ""},
		{http.MethodPost, base + "/stop", `{"confirm":"app"}`}, {http.MethodGet, base + "/runs", ""},
	} {
		e.want(e.do(c.method, c.path, "bob", c.body), http.StatusForbidden, "forbidden")
	}
	// Un repo privato che bob non legge: 404, non 403.
	e.repo("segreto", false)
	e.want(e.do(http.MethodGet, "/repos/alice/segreto/pull-mirror", "bob", ""), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodPost, "/repos/alice/segreto/pull-mirror", "bob", `{"url":"https://x.example.test/a.git"}`), http.StatusNotFound, "not_found")
	// Un repo che non ha mai avuto un mirror in pull: 404 anche per l'admin.
	e.repo("normale", true)
	e.want(e.do(http.MethodGet, "/repos/alice/normale/pull-mirror", "alice", ""), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodGet, "/repos/alice/normale/pull-mirror/runs", "alice", ""), http.StatusNotFound, "not_found")
	// Un repo archiviato rifiuta l'attivazione (R10).
	e.repo("vecchio", true)
	e.want(e.do(http.MethodPatch, "/repos/alice/vecchio", "alice", `{"archived":true}`), http.StatusOK, "")
	e.want(e.do(http.MethodPost, "/repos/alice/vecchio/pull-mirror", "alice", `{"url":"https://mirror.example.test/v.git"}`), http.StatusConflict, "archived")
}

func TestPullMirror_API_ValidazioneEEgress(t *testing.T) {
	e := newPullEnv(t)
	e.repo("app", false)
	cases := []struct {
		name, body string
		status     int
		code       string
	}{
		{"http", `{"url":"http://mirror.example.test/a.git"}`, 422, "url_not_allowed"},
		{"ssh", `{"url":"ssh://git@mirror.example.test/a.git"}`, 422, "url_not_allowed"},
		{"userinfo", `{"url":"https://u:p@mirror.example.test/a.git"}`, 422, "url_not_allowed"},
		{"loopback", `{"url":"https://127.0.0.1/a.git"}`, 422, "url_not_allowed"},
		{"localhost", `{"url":"https://localhost/a.git"}`, 422, "url_not_allowed"},
		{"cluster", `{"url":"https://10.42.0.5/a.git"}`, 422, "url_not_allowed"},
		{"solo utente", `{"url":"https://mirror.example.test/a.git","username":"u"}`, 422, "validation_failed"},
		{"solo token", `{"url":"https://mirror.example.test/a.git","token":"t"}`, 422, "validation_failed"},
		{"intervallo corto", `{"url":"https://mirror.example.test/a.git","intervalSeconds":299}`, 422, "validation_failed"},
		{"intervallo lungo", `{"url":"https://mirror.example.test/a.git","intervalSeconds":86401}`, 422, "validation_failed"},
		{"campo ignoto", `{"url":"https://mirror.example.test/a.git","force":true}`, 400, ""},
	}
	for _, c := range cases {
		rec := e.do(http.MethodPost, "/repos/alice/app/pull-mirror", "alice", c.body)
		if rec.Code != c.status || (c.code != "" && !strings.Contains(rec.Body.String(), `"`+c.code+`"`)) {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body.String())
		}
	}
	// Origine pubblica senza credenziali, intervallo ai limiti (5 minuti, 24 ore).
	m := e.enable("app", pullURL, fmt.Sprintf(`{"url":%q,"intervalSeconds":300}`, pullURL))
	if m.HasToken || m.Username != nil || m.IntervalSeconds != 300 {
		t.Fatalf("senza credenziali: %+v", m)
	}
	rec := e.do(http.MethodPatch, "/repos/alice/app/pull-mirror", "alice", `{"intervalSeconds":86400}`)
	e.want(rec, http.StatusOK, "")
}

// MP4: pianificazione a intervallo, «Sincronizza ora», trigger delle esecuzioni.
func TestPullMirror_Pianificazione_IntervalloESyncNow(t *testing.T) {
	e := newPullEnv(t)
	e.repo("app", true)
	m := e.enable("app", pullURL, "")

	if n := e.processPull(); n != 1 {
		t.Fatalf("primo aggiornamento: %d", n)
	}
	call := e.puller.last()
	if call.in.URL != pullURL || call.in.Token != mirrorToken || call.in.Username != "gh-bot" || call.in.IP != "93.184.216.34" ||
		call.in.DefaultBranch != "main" || call.in.Owner != "alice" || call.in.Name != "app" || call.in.Pusher.ID != aliceID {
		t.Fatalf("chiamata a git: %v / %+v", call.in, call.in.Pusher)
	}
	got := e.getPull("app")
	if got.State != "in_sync" || got.LastSyncedAt == nil || got.LastError != nil || got.Attempts != 0 || got.NextSyncAt == nil {
		t.Fatalf("dopo il primo giro: %+v", got)
	}
	if want := e.clock.Now().Add(10 * time.Minute); !got.NextSyncAt.Equal(want) {
		t.Fatalf("prossimo aggiornamento %s, voluto %s", got.NextSyncAt, want)
	}

	// Non è dovuto prima dell'intervallo, lo è alla scadenza.
	e.clock.Advance(9 * time.Minute)
	if n := e.processPull(); n != 0 {
		t.Fatalf("prima dell'intervallo: %d", n)
	}
	e.clock.Advance(time.Minute)
	if n := e.processPull(); n != 1 || e.puller.count() != 2 {
		t.Fatalf("all'intervallo: %d eseguiti, %d chiamate", n, e.puller.count())
	}
	// Un intervallo più corto vale da subito dall'ultimo successo.
	e.want(e.do(http.MethodPatch, "/repos/alice/app/pull-mirror", "alice", `{"intervalSeconds":300}`), http.StatusOK, "")
	if want := e.clock.Now().Add(5 * time.Minute); !e.nextAttempt(m.Id).Equal(want) {
		t.Fatalf("dopo la modifica: %s, voluto %s", e.nextAttempt(m.Id), want)
	}

	// «Sincronizza ora»: 202, in coda subito, esegue prima dell'intervallo.
	rec := e.do(http.MethodPost, "/repos/alice/app/pull-mirror/sync", "alice", "")
	e.want(rec, http.StatusAccepted, "")
	if s := e.decodePull(rec.Body.Bytes()); s.State != "pending" {
		t.Fatalf("dopo sync: %+v", s)
	}
	if n := e.processPull(); n != 1 {
		t.Fatalf("sync now: %d", n)
	}

	runs := e.pullRuns("app")
	if runs.Total != 3 || len(runs.Items) != 3 {
		t.Fatalf("registro: %+v", runs)
	}
	// Dalla più recente: manuale, a intervallo, iniziale.
	if runs.Items[0].Trigger != "manual" || runs.Items[1].Trigger != "schedule" || runs.Items[2].Trigger != "initial" {
		t.Fatalf("trigger: %s %s %s", runs.Items[0].Trigger, runs.Items[1].Trigger, runs.Items[2].Trigger)
	}
	// Del giro riuscito resta solo ciò che è cambiato (i ref allineati si omettono).
	if r := runs.Items[2]; r.Outcome != "success" || r.Attempt != 1 || len(r.Refs) != 1 || r.Refs[0].Ref != "refs/heads/main" || r.Refs[0].Status != "updated" {
		t.Fatalf("esecuzione: %+v", r)
	}
}

// MP4: ritentativi 30 s, 2 min, 10 min, 1 h, 3 h, 6 h (tetto); un successo azzera.
func TestPullMirror_Ritentativi_AttesaCrescente(t *testing.T) {
	e := newPullEnv(t)
	e.repo("app", false)
	m := e.enable("app", pullURL, "")
	e.puller.setAnswer(failingPull("connessione rifiutata dall'origine"))

	waits := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour, 6 * time.Hour}
	for i, w := range waits {
		if n := e.processPull(); n != 1 {
			t.Fatalf("tentativo %d: eseguiti %d", i+1, n)
		}
		got := e.getPull("app")
		if got.State != "error" || got.Attempts != i+1 || got.LastError == nil || !strings.Contains(*got.LastError, "connessione rifiutata") {
			t.Fatalf("dopo il tentativo %d: %+v", i+1, got)
		}
		next := e.nextAttempt(m.Id)
		if next == nil || !next.Equal(e.clock.Now().Add(w)) {
			t.Fatalf("attesa dopo il tentativo %d: %v, voluta %s", i+1, next, w)
		}
		// Un secondo prima della scadenza non parte.
		e.clock.Advance(w - time.Second)
		if n := e.processPull(); n != 0 {
			t.Fatalf("prima della scadenza (%d): %d", i+1, n)
		}
		e.clock.Advance(time.Second)
	}
	// L'origine torna raggiungibile: successo, tentativi azzerati, errore tolto.
	e.puller.setAnswer(nil)
	if n := e.processPull(); n != 1 {
		t.Fatalf("dopo il ripristino: %d", n)
	}
	if got := e.getPull("app"); got.State != "in_sync" || got.Attempts != 0 || got.LastError != nil {
		t.Fatalf("dopo il successo: %+v", got)
	}
	runs := e.pullRuns("app")
	if runs.Items[0].Outcome != "success" || runs.Items[0].Attempt != 8 || runs.Items[1].Outcome != "error" {
		t.Fatalf("registro: %+v", runs.Items[:2])
	}
}

// C8/MP4: un blocco della policy di uscita non si ritenta, nemmeno col tempo;
// riparte solo con «Sincronizza ora».
func TestPullMirror_BloccoEgress_NonSiRitenta(t *testing.T) {
	e := newPullEnv(t)
	e.repo("app", false)
	// Il DNS dell'origine risponde anche con un indirizzo interno (rebinding).
	e.enable("app", "https://evil.example.test/a.git", "")
	e.processPull()
	if e.puller.count() != 0 {
		t.Fatal("verso un indirizzo interno non si deve chiamare git")
	}
	got := e.getPull("app")
	if got.State != "error" || got.LastError == nil || !strings.Contains(*got.LastError, "cluster") || got.NextSyncAt != nil {
		t.Fatalf("origine bloccata: %+v", got)
	}
	if runs := e.pullRuns("app"); len(runs.Items) != 1 || runs.Items[0].Outcome != "blocked" {
		t.Fatalf("registro: %+v", runs.Items)
	}
	e.clock.Advance(48 * time.Hour)
	if n := e.processPull(); n != 0 {
		t.Fatalf("un blocco egress non si ritenta: %d", n)
	}
	// Richiesta a mano: il motore riprova (e il blocco è lo stesso).
	e.syncNow("app")
	if n := e.processPull(); n != 1 {
		t.Fatalf("dopo sync now: %d", n)
	}
	if runs := e.pullRuns("app"); runs.Total != 2 || e.puller.count() != 0 {
		t.Fatalf("registro dopo sync now: %+v", runs)
	}
}

// MP4: un aggiornamento alla volta (affitto, anche con più repliche); un
// «Sincronizza ora» durante un giro è idempotente; un crash si riprende.
func TestPullMirror_UnSoloAggiornamentoPerVolta(t *testing.T) {
	e := newPullEnv(t)
	e.repo("app", false)
	m := e.enable("app", pullURL, "")
	e.puller.block = make(chan struct{})
	e.puller.started = make(chan struct{}, 4)

	done := make(chan int, 1)
	go func() { done <- e.processPull() }()
	select {
	case <-e.puller.started:
	case <-time.After(20 * time.Second):
		t.Fatal("il primo aggiornamento non è partito")
	}
	other := &mirrors.PullEngine{Pool: e.pool, Keys: e.keys, Git: e.puller, Egress: e.pull.Egress, Now: e.clock.Now, Lease: 5 * time.Minute}
	if n, err := other.ProcessDue(context.Background()); err != nil || n != 0 {
		t.Fatalf("seconda replica: %d (%v)", n, err)
	}
	if got := e.getPull("app"); got.State != "syncing" || got.NextSyncAt != nil {
		t.Fatalf("durante l'aggiornamento: %+v", got)
	}
	// «Sincronizza ora» durante il giro: 202 e niente di nuovo da eseguire.
	e.syncNow("app")
	if got := e.getPull("app"); got.State != "syncing" {
		t.Fatalf("sync durante il giro: %+v", got)
	}
	close(e.puller.block)
	if n := <-done; n != 1 {
		t.Fatalf("eseguiti %d", n)
	}
	e.puller.block = nil
	if n := e.processPull(); n != 0 || e.puller.count() != 1 {
		t.Fatalf("dopo il giro: %d eseguiti, %d chiamate", n, e.puller.count())
	}
	// Un affitto scaduto (crash del worker) si riprende.
	e.sql(`UPDATE core.repo_pull_mirrors SET state = 'syncing', lease_until = $2 WHERE id = $1`, m.Id, e.clock.Now().Add(time.Minute))
	if n := e.processPull(); n != 0 {
		t.Fatalf("affitto ancora valido: %d", n)
	}
	e.clock.Advance(2 * time.Minute)
	if n := e.processPull(); n != 1 {
		t.Fatalf("affitto scaduto: eseguiti %d", n)
	}
}

// MP4: registro delle ultime 50 esecuzioni e per 30 giorni.
func TestPullMirror_Registro_50E30Giorni(t *testing.T) {
	e := newPullEnv(t)
	e.repo("app", false)
	m := e.enable("app", pullURL, "")
	for i := 0; i < mirrors.MaxRuns+5; i++ {
		e.syncNow("app")
		if n := e.processPull(); n != 1 {
			t.Fatalf("giro %d: %d", i, n)
		}
		e.clock.Advance(time.Second)
	}
	if n := e.countRows(`SELECT count(*) FROM core.repo_pull_mirror_runs WHERE mirror_id = $1`, m.Id); n != mirrors.MaxRuns {
		t.Fatalf("registro dopo la potatura: %d", n)
	}
	if runs := e.pullRuns("app"); runs.Total != mirrors.MaxRuns {
		t.Fatalf("registro dall'API: %d", runs.Total)
	}
	e.sql(`UPDATE core.repo_pull_mirror_runs SET started_at = $2 WHERE mirror_id = $1 AND id IN (SELECT id FROM core.repo_pull_mirror_runs WHERE mirror_id = $1 LIMIT 5)`,
		m.Id, e.clock.Now().Add(-31*24*time.Hour))
	if got, err := e.pull.PurgeLog(context.Background(), e.clock.Now()); err != nil || got != 5 {
		t.Fatalf("PurgeLog = %d (%v)", got, err)
	}
}

// MP1, C3: ref divergenti segnati e notificati una volta agli admin; un ref
// che torna allineato esce dall'elenco; un'esecuzione con ref divergenti resta in_sync.
func TestPullMirror_RefDivergenti_Notifica(t *testing.T) {
	e := newPullEnv(t)
	repoID := e.repo("app", true)
	e.enable("app", pullURL, "")
	sha := func(c string) string { return strings.Repeat(c, 40) }
	diverged := func(refs ...gitclient.MirrorPullRef) func(int, gitclient.MirrorPullInput) (gitclient.MirrorPullResult, error) {
		return func(int, gitclient.MirrorPullInput) (gitclient.MirrorPullResult, error) {
			return gitclient.MirrorPullResult{OK: false, Diverged: true, Refs: refs}, nil
		}
	}
	main := gitclient.MirrorPullRef{Ref: "refs/heads/main", Status: "diverged", SHA: sha("a"), Origin: sha("b"), Reason: "non-fast-forward"}
	old := gitclient.MirrorPullRef{Ref: "refs/heads/old", Status: "diverged", SHA: sha("c"), Reason: "deleted-at-origin"}
	e.puller.setAnswer(diverged(main, old, gitclient.MirrorPullRef{Ref: "refs/heads/dev", Status: "updated", SHA: sha("d")}))
	e.processPull()

	got := e.getPull("app")
	if got.State != "in_sync" || len(got.DivergedRefs) != 2 {
		t.Fatalf("stato: %+v", got)
	}
	byRef := map[string]openapi.PullMirrorDivergedRef{}
	for _, d := range got.DivergedRefs {
		byRef[d.Ref] = d
	}
	if d := byRef["refs/heads/main"]; d.Kind != "rewritten" || d.LocalSha != sha("a") || d.RemoteSha == nil || *d.RemoteSha != sha("b") {
		t.Fatalf("main: %+v", d)
	}
	if d := byRef["refs/heads/old"]; d.Kind != "deleted" || d.LocalSha != sha("c") || d.RemoteSha != nil {
		t.Fatalf("old: %+v", d)
	}
	if runs := e.pullRuns("app"); runs.Items[0].Outcome != "diverged" || len(runs.Items[0].Refs) != 3 {
		t.Fatalf("registro: %+v", runs.Items[0])
	}
	// Notifica all'admin (alice), una sola per esecuzione; non a bob che legge soltanto.
	count := func(user string) int {
		return e.countRows(`SELECT count(*) FROM core.notifications WHERE reason = 'mirror' AND user_id = $1 AND repo_id = $2 AND event_name = 'pull_mirror.diverged'`, user, repoID)
	}
	if count(aliceID) != 1 || count(bobID) != 0 {
		t.Fatalf("notifiche: alice %d, bob %d", count(aliceID), count(bobID))
	}
	rec := e.do(http.MethodGet, "/notifications?reason=mirror", "alice", "")
	e.want(rec, http.StatusOK, "")
	if !strings.Contains(rec.Body.String(), "alice/app") || !strings.Contains(rec.Body.String(), "divergenti") {
		t.Fatalf("notifica nella casella: %s", rec.Body.String())
	}

	// Gli stessi ref ancora divergenti: nessuna notifica nuova. Un ref nuovo sì.
	e.syncNow("app")
	e.processPull()
	if count(aliceID) != 1 {
		t.Fatalf("stessi ref: notifiche %d", count(aliceID))
	}
	e.puller.setAnswer(diverged(main, gitclient.MirrorPullRef{Ref: "refs/tags/v1", Status: "diverged", SHA: sha("e"), Origin: sha("f"), Reason: "tag-moved"},
		gitclient.MirrorPullRef{Ref: "refs/heads/old", Status: "up-to-date", SHA: sha("c")}))
	e.syncNow("app")
	e.processPull()
	if count(aliceID) != 2 {
		t.Fatalf("ref nuovo: notifiche %d", count(aliceID))
	}
	got = e.getPull("app")
	refs := map[string]bool{}
	for _, d := range got.DivergedRefs {
		refs[d.Ref] = true
	}
	if len(got.DivergedRefs) != 2 || !refs["refs/heads/main"] || !refs["refs/tags/v1"] || refs["refs/heads/old"] {
		t.Fatalf("ref divergenti dopo il giro: %+v", got.DivergedRefs)
	}
}

// MP4, C3: nessun aggiornamento riuscito da più di 24 ore → una notifica agli
// admin, una volta sola finché non torna a posto.
func TestPullMirror_NessunAggiornamentoDa24Ore_NotificaUnaVolta(t *testing.T) {
	e := newPullEnv(t)
	repoID := e.repo("app", true)
	m := e.enable("app", pullURL, "")
	e.puller.setAnswer(failingPull("origine non raggiungibile"))
	e.processPull()
	count := func() int {
		return e.countRows(`SELECT count(*) FROM core.notifications WHERE reason = 'mirror' AND user_id = $1 AND repo_id = $2 AND event_name = 'pull_mirror.stale'`, aliceID, repoID)
	}
	ctx := context.Background()
	// Meno di 24 ore dalla creazione: niente.
	e.sql(`UPDATE core.repo_pull_mirrors SET created_at = $2 WHERE id = $1`, m.Id, e.clock.Now().Add(-23*time.Hour))
	if n, err := e.pull.CheckStale(ctx, e.clock.Now()); err != nil || n != 0 || count() != 0 {
		t.Fatalf("a 23 ore: %d (%v), notifiche %d", n, err, count())
	}
	// Oltre le 24 ore senza un solo successo: avviso agli admin, non a bob.
	e.sql(`UPDATE core.repo_pull_mirrors SET created_at = $2 WHERE id = $1`, m.Id, e.clock.Now().Add(-25*time.Hour))
	if n, err := e.pull.CheckStale(ctx, e.clock.Now()); err != nil || n != 1 || count() != 1 {
		t.Fatalf("a 25 ore: %d (%v), notifiche %d", n, err, count())
	}
	if e.countRows(`SELECT count(*) FROM core.notifications WHERE reason = 'mirror' AND user_id = $1`, bobID) != 0 {
		t.Fatal("bob non è admin")
	}
	// Una volta sola, anche se continua a fallire.
	e.clock.Advance(12 * time.Hour)
	if n, err := e.pull.CheckStale(ctx, e.clock.Now()); err != nil || n != 0 || count() != 1 {
		t.Fatalf("seconda volta: %d (%v), notifiche %d", n, err, count())
	}
	rec := e.do(http.MethodGet, "/notifications?reason=mirror", "alice", "")
	if !strings.Contains(rec.Body.String(), "24 ore") {
		t.Fatalf("notifica nella casella: %s", rec.Body.String())
	}
	// Un aggiornamento riuscito rimette a posto: se poi si ferma di nuovo, riavvisa.
	e.puller.setAnswer(nil)
	e.syncNow("app")
	e.processPull()
	if e.countRows(`SELECT count(*) FROM core.repo_pull_mirrors WHERE id = $1 AND stale_notified_at IS NULL`, m.Id) != 1 {
		t.Fatal("il successo deve azzerare l'avviso")
	}
	e.sql(`UPDATE core.repo_pull_mirrors SET last_success_at = $2 WHERE id = $1`, m.Id, e.clock.Now().Add(-25*time.Hour))
	if n, err := e.pull.CheckStale(ctx, e.clock.Now()); err != nil || n != 1 || count() != 2 {
		t.Fatalf("dopo il ripristino: %d (%v), notifiche %d", n, err, count())
	}
	// Un mirror in pausa non avvisa: è fermo di proposito.
	e.want(e.do(http.MethodPost, "/repos/alice/app/pull-mirror/pause", "alice", ""), http.StatusOK, "")
	e.sql(`UPDATE core.repo_pull_mirrors SET stale_notified_at = NULL, last_success_at = $2 WHERE id = $1`, m.Id, e.clock.Now().Add(-48*time.Hour))
	if n, _ := e.pull.CheckStale(ctx, e.clock.Now()); n != 0 {
		t.Fatalf("in pausa: %d", n)
	}
}

// MP6: pausa (ferma, resta in sola lettura), ripresa, interruzione con
// conferma del nome (credenziali via; registro e ref divergenti restano),
// riattivazione.
func TestPullMirror_PausaRipresaInterruzione(t *testing.T) {
	e := newPullEnv(t)
	e.repo("app", true)
	m := e.enable("app", pullURL, "")
	e.puller.setAnswer(func(int, gitclient.MirrorPullInput) (gitclient.MirrorPullResult, error) {
		return gitclient.MirrorPullResult{Diverged: true, Refs: []gitclient.MirrorPullRef{
			{Ref: "refs/heads/main", Status: "diverged", SHA: strings.Repeat("a", 40), Origin: strings.Repeat("b", 40), Reason: "non-fast-forward"}}}, nil
	})
	e.processPull()

	rec := e.do(http.MethodPost, "/repos/alice/app/pull-mirror/pause", "alice", "")
	e.want(rec, http.StatusOK, "")
	if got := e.decodePull(rec.Body.Bytes()); got.State != "paused" || got.NextSyncAt != nil {
		t.Fatalf("in pausa: %+v", got)
	}
	// Idempotente; ferma anche «Sincronizza ora»; il repo resta in sola lettura (campo mirror).
	e.want(e.do(http.MethodPost, "/repos/alice/app/pull-mirror/pause", "alice", ""), http.StatusOK, "")
	e.want(e.do(http.MethodPost, "/repos/alice/app/pull-mirror/sync", "alice", ""), http.StatusConflict, "mirror_paused")
	e.clock.Advance(48 * time.Hour)
	if n := e.processPull(); n != 0 {
		t.Fatalf("un mirror in pausa non si aggiorna: %d", n)
	}
	if body := e.do(http.MethodGet, "/repos/alice/app", "alice", "").Body.String(); !strings.Contains(body, `"mirror"`) {
		t.Fatalf("in pausa il repo resta mirror: %s", body)
	}

	// Riprendi: torna in coda e si aggiorna subito.
	rec = e.do(http.MethodPost, "/repos/alice/app/pull-mirror/resume", "alice", "")
	e.want(rec, http.StatusOK, "")
	if got := e.decodePull(rec.Body.Bytes()); got.State != "pending" || got.NextSyncAt == nil {
		t.Fatalf("ripreso: %+v", got)
	}
	e.want(e.do(http.MethodPost, "/repos/alice/app/pull-mirror/resume", "alice", ""), http.StatusOK, "") // idempotente
	if n := e.processPull(); n != 1 {
		t.Fatalf("dopo la ripresa: %d", n)
	}

	// Interrompi: la conferma è il nome del repo.
	stop := "/repos/alice/app/pull-mirror/stop"
	e.want(e.do(http.MethodPost, stop, "alice", `{"confirm":"altro"}`), http.StatusUnprocessableEntity, "confirmation_mismatch")
	e.want(e.do(http.MethodPost, stop, "alice", `{}`), http.StatusUnprocessableEntity, "confirmation_mismatch")
	e.want(e.do(http.MethodPost, stop, "bob", `{"confirm":"app"}`), http.StatusForbidden, "forbidden")
	if got := e.getPull("app"); got.State == "stopped" {
		t.Fatal("una conferma sbagliata non interrompe")
	}
	rec = e.do(http.MethodPost, stop, "alice", `{"confirm":"app"}`)
	e.want(rec, http.StatusOK, "")
	got := e.decodePull(rec.Body.Bytes())
	if got.State != "stopped" || got.StoppedAt == nil || got.HasToken || got.Username != nil || got.NextSyncAt != nil || len(got.DivergedRefs) != 1 {
		t.Fatalf("interrotto: %+v", got)
	}
	if e.countRows(`SELECT count(*) FROM core.repo_pull_mirrors WHERE id = $1 AND token_ciphertext IS NULL AND token_nonce IS NULL AND token_key_id IS NULL AND username IS NULL`, m.Id) != 1 {
		t.Fatal("le credenziali vanno cancellate")
	}
	if runs := e.pullRuns("app"); runs.Total != 2 {
		t.Fatalf("il registro resta: %d", runs.Total)
	}
	if body := e.do(http.MethodGet, "/repos/alice/app", "alice", "").Body.String(); strings.Contains(body, `"mirror"`) {
		t.Fatalf("il repo interrotto è normale: %s", body)
	}
	// Idempotente; sync, pausa, ripresa e modifica rispondono 409.
	e.want(e.do(http.MethodPost, stop, "alice", `{"confirm":"app"}`), http.StatusOK, "")
	for _, p := range []string{"/sync", "/pause", "/resume"} {
		e.want(e.do(http.MethodPost, "/repos/alice/app/pull-mirror"+p, "alice", ""), http.StatusConflict, "mirror_stopped")
	}
	e.want(e.do(http.MethodPatch, "/repos/alice/app/pull-mirror", "alice", `{"intervalSeconds":600}`), http.StatusConflict, "mirror_stopped")
	if e.countRows(`SELECT count(*) FROM core.notifications WHERE pull_mirror_id = $1`, m.Id) == 0 {
		t.Fatal("le notifiche del mirror restano")
	}
	if n := e.processPull(); n != 0 {
		t.Fatalf("interrotto non si aggiorna: %d", n)
	}

	// Riattivazione: stessa riga, registro e ref divergenti conservati.
	e.puller.setAnswer(nil)
	again := e.enable("app", pullURL, fmt.Sprintf(`{"url":%q}`, pullURL))
	if again.Id != m.Id || again.State != "pending" || again.StoppedAt != nil || again.HasToken {
		t.Fatalf("riattivato: %+v", again)
	}
	if n := e.processPull(); n != 1 {
		t.Fatalf("dopo la riattivazione: %d", n)
	}
	if runs := e.pullRuns("app"); runs.Total != 3 {
		t.Fatalf("registro dopo la riattivazione: %d", runs.Total)
	}
}

// MP8: «Copia una volta sola». Crea il repo e il mirror, copia, interrompe e
// cancella le credenziali in un passo; se la copia fallisce il repo resta con
// l'errore visibile e si può riprovare.
func TestPullMirror_Import_CopiaUnaVoltaSola(t *testing.T) {
	e := newPullEnv(t)
	e.puller.setAnswer(failingPull("repository not found"))

	body := fmt.Sprintf(`{"owner":"alice","name":"copia","visibility":"internal","url":%q,"mode":"once","username":"gh-bot","token":%q}`, pullURL, mirrorToken)
	rec := e.do(http.MethodPost, "/repos/import", "alice", body)
	e.want(rec, http.StatusCreated, "")
	if strings.Contains(rec.Body.String(), mirrorToken) || !strings.Contains(rec.Body.String(), `"mirror"`) {
		t.Fatalf("repo importato: %s", rec.Body.String())
	}
	got := e.getPull("copia")
	if got.Mode != "once" || got.State != "pending" || !got.HasToken {
		t.Fatalf("mirror dell'import: %+v", got)
	}

	// La copia fallisce: il repo resta con l'errore visibile, le credenziali
	// restano per riprovare.
	e.processPull()
	e.clock.Advance(time.Second)
	got = e.getPull("copia")
	if got.State != "error" || got.LastError == nil || !strings.Contains(*got.LastError, "repository not found") || !got.HasToken || got.NextSyncAt == nil {
		t.Fatalf("copia fallita: %+v", got)
	}
	if e.do(http.MethodGet, "/repos/alice/copia", "alice", "").Code != http.StatusOK {
		t.Fatal("il repo deve restare")
	}
	// Si riprova a mano; riesce: interrotto da solo, credenziali cancellate, repo normale.
	e.puller.setAnswer(nil)
	e.syncNow("copia")
	if n := e.processPull(); n != 1 {
		t.Fatalf("secondo tentativo: %d", n)
	}
	got = e.getPull("copia")
	if got.State != "stopped" || got.StoppedAt == nil || got.HasToken || got.Username != nil || got.LastSyncedAt == nil || got.LastError != nil {
		t.Fatalf("copia fatta: %+v", got)
	}
	if call := e.puller.last(); call.in.Token != mirrorToken || call.in.Name != "copia" {
		t.Fatalf("chiamata a git: %+v", call.in)
	}
	if body := e.do(http.MethodGet, "/repos/alice/copia", "alice", "").Body.String(); strings.Contains(body, `"mirror"`) {
		t.Fatalf("dopo la copia il repo è normale: %s", body)
	}
	runs := e.pullRuns("copia")
	if runs.Total != 2 || runs.Items[0].Outcome != "success" || runs.Items[1].Outcome != "error" {
		t.Fatalf("registro: %+v", runs)
	}
	// Niente altro da fare: non si aggiorna più.
	e.clock.Advance(48 * time.Hour)
	if n := e.processPull(); n != 0 {
		t.Fatalf("dopo la copia: %d", n)
	}

	// «Mantieni sincronizzato»: il mirror resta attivo a intervallo.
	rec = e.do(http.MethodPost, "/repos/import", "alice",
		fmt.Sprintf(`{"owner":"alice","name":"specchio","url":%q,"mode":"mirror","intervalSeconds":1800}`, pullURL))
	e.want(rec, http.StatusCreated, "")
	e.processPull()
	if got := e.getPull("specchio"); got.State != "in_sync" || got.Mode != "mirror" || got.IntervalSeconds != 1800 {
		t.Fatalf("import mirror: %+v", got)
	}
}

func TestPullMirror_Import_PermessiEValidazione(t *testing.T) {
	e := newPullEnv(t)
	e.repo("esiste", true)
	imp := func(user, body string) *httptest.ResponseRecorder {
		return e.do(http.MethodPost, "/repos/import", user, body)
	}
	ok := fmt.Sprintf(`{"owner":"alice","name":"nuovo","url":%q,"mode":"once"}`, pullURL)

	e.want(imp("bob", ok), http.StatusForbidden, "forbidden") // non è admin dell'owner
	if n := e.countRows(`SELECT count(*) FROM core.repositories WHERE name = 'nuovo'`); n != 0 {
		t.Fatalf("un import rifiutato non lascia repo: %d", n)
	}
	e.want(imp("alice", fmt.Sprintf(`{"owner":"alice","name":"esiste","url":%q,"mode":"once"}`, pullURL)), http.StatusConflict, "conflict")
	for name, body := range map[string]string{
		"http":       `{"owner":"alice","name":"x1","url":"http://mirror.example.test/a.git","mode":"once"}`,
		"loopback":   `{"owner":"alice","name":"x2","url":"https://127.0.0.1/a.git","mode":"once"}`,
		"modo":       `{"owner":"alice","name":"x3","url":"https://mirror.example.test/a.git","mode":"boh"}`,
		"intervallo": `{"owner":"alice","name":"x4","url":"https://mirror.example.test/a.git","mode":"mirror","intervalSeconds":10}`,
		"solo token": `{"owner":"alice","name":"x5","url":"https://mirror.example.test/a.git","mode":"once","token":"t"}`,
	} {
		if rec := imp("alice", body); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if n := e.countRows(`SELECT count(*) FROM core.repositories WHERE name LIKE 'x%'`); n != 0 {
		t.Fatalf("validazione fallita ma repo creati: %d", n)
	}
	e.want(imp("alice", `{"owner":"alice","name":"nuovo","url":"https://mirror.example.test/a.git"}`), http.StatusUnprocessableEntity, "validation_failed")
	e.want(imp("alice", ok), http.StatusCreated, "")
}

// MP6: con il mirror in pull attivo è rifiutato un mirror in push verso la
// stessa origine (URL normalizzato), e viceversa; interrotto il mirror in pull
// il divieto cade.
func TestPullMirror_DivietoMirrorInPushVersoLOrigine(t *testing.T) {
	e := newPullEnv(t)
	e.repo("app", false)
	e.enable("app", pullURL, fmt.Sprintf(`{"url":%q}`, pullURL))

	push := func(url string) *httptest.ResponseRecorder {
		return e.do(http.MethodPost, "/repos/alice/app/mirrors", "alice", fmt.Sprintf(`{"url":%q,"username":"u","token":"t"}`, url))
	}
	for _, v := range []string{
		pullURL,
		"https://MIRROR.example.test/org/code",
		"https://mirror.example.test:443/org/code.git/",
		"https://mirror.example.test/Org/Code.git",
	} {
		e.want(push(v), http.StatusConflict, "conflict")
	}
	// Un altro indirizzo va bene.
	other := push("https://other.example.test/org/code.git")
	e.want(other, http.StatusCreated, "")
	// E non si può portare un mirror in push sull'origine con una modifica.
	var pm openapi.RepoMirror
	_ = json.Unmarshal(other.Body.Bytes(), &pm)
	e.want(e.do(http.MethodPatch, "/repos/alice/app/mirrors/"+pm.Id.String(), "alice", fmt.Sprintf(`{"url":%q}`, pullURL)), http.StatusConflict, "conflict")

	// Il contrario: un mirror in push esistente vieta l'origine uguale.
	e.repo("altro", false)
	e.want(e.do(http.MethodPost, "/repos/alice/altro/mirrors", "alice",
		fmt.Sprintf(`{"url":%q,"username":"u","token":"t"}`, "https://push.example.test/o/r.git")), http.StatusCreated, "")
	e.want(e.do(http.MethodPost, "/repos/alice/altro/pull-mirror", "alice", `{"url":"https://push.example.test/o/r"}`), http.StatusConflict, "conflict")

	// Interrotto il mirror in pull, la destinazione è di nuovo ammessa.
	e.want(e.do(http.MethodPost, "/repos/alice/app/pull-mirror/stop", "alice", `{"confirm":"app"}`), http.StatusOK, "")
	e.want(push(pullURL), http.StatusCreated, "")
}
