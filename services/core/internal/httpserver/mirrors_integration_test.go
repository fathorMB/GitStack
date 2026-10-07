//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/mirrors"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
	"github.com/google/uuid"
)

// V8 (GIT-179): mirror in push. API (permessi admin, token cifrato e mai
// restituito, validazione C8), coda e stati del motore (successo, backoff,
// affitto, divergenza con notifica, blocco egress) con un servizio git finto;
// il push vero con un secondo server git è in services/git (mirrorpush).

const mirrorToken = "ghp_TOKEN_SEGRETO_DI_PROVA_0123456789"

type pushCall struct {
	repo uuid.UUID
	in   gitclient.MirrorPushInput
}

// fakePusher è il servizio git finto.
type fakePusher struct {
	mu    sync.Mutex
	calls []pushCall
	// answer decide l'esito; default: tutto riuscito.
	answer func(n int, in gitclient.MirrorPushInput) (gitclient.MirrorPushResult, error)
	// block, se non nil, ferma ogni push finché non si chiude.
	block   chan struct{}
	started chan struct{}
}

func (f *fakePusher) MirrorPush(_ context.Context, caller trust.Identity, repoID uuid.UUID, in gitclient.MirrorPushInput) (gitclient.MirrorPushResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, pushCall{repo: repoID, in: in})
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
	return gitclient.MirrorPushResult{OK: true, Refs: []gitclient.MirrorRef{
		{Ref: "refs/heads/main", Status: "pushed", SHA: strings.Repeat("a", 40)},
		{Ref: "refs/tags/v1", Status: "up-to-date", SHA: strings.Repeat("b", 40)},
	}}, nil
}

func (f *fakePusher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakePusher) setAnswer(fn func(n int, in gitclient.MirrorPushInput) (gitclient.MirrorPushResult, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answer = fn
}

type mirrorEnv struct {
	*issuesEnv
	hid    *hookIdentity
	clock  *hookClock
	pusher *fakePusher
	eng    *mirrors.Engine
	keys   *webhooks.Keyring
}

func newMirrorEnv(t *testing.T) *mirrorEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	base := &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}
	hid := &hookIdentity{issuesIdentity: base}
	keys, err := webhooks.NewKeyring("k1", hookSecretKey, "")
	if err != nil {
		t.Fatal(err)
	}
	eg, err := mirrors.NewEgress(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Il DNS dei test: mirror.example.test è un indirizzo pubblico, evil.example.test
	// risponde anche con un indirizzo interno (rebinding), internal.example.test solo interno.
	eg.Resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
		switch host {
		case "mirror.example.test":
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		case "evil.example.test":
			return []netip.Addr{netip.MustParseAddr("93.184.216.35"), netip.MustParseAddr("10.42.0.7")}, nil
		case "internal.example.test":
			return []netip.Addr{netip.MustParseAddr("100.64.0.9")}, nil
		}
		return nil, errors.New("host sconosciuto")
	}
	e := &issuesEnv{t: t, pool: pool, id: base}
	e.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(hid), httpserver.WithReadableLister(hid), httpserver.WithGit(newFakeGit()),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}),
		httpserver.WithMirrors(httpserver.MirrorConfig{Keys: keys, Egress: eg}))
	clock := &hookClock{t: time.Now().UTC().Add(time.Hour).Truncate(time.Second)}
	pusher := &fakePusher{}
	m := &mirrorEnv{issuesEnv: e, hid: hid, clock: clock, pusher: pusher, keys: keys}
	m.eng = &mirrors.Engine{Pool: pool, Keys: keys, Git: pusher, Egress: eg, Managers: hid, Now: clock.Now, Lease: 5 * time.Minute}
	return m
}

const mirrorURL = "https://mirror.example.test/org/code.git"

func (e *mirrorEnv) create(repo, url string) openapi.RepoMirror {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/repos/alice/"+repo+"/mirrors", "alice",
		fmt.Sprintf(`{"url":%q,"username":"mirror-bot","token":%q}`, url, mirrorToken))
	e.want(rec, http.StatusCreated, "")
	if strings.Contains(rec.Body.String(), mirrorToken) {
		e.t.Fatalf("il token non deve tornare mai: %s", rec.Body.String())
	}
	return e.decodeMirror(rec.Body.Bytes())
}

func (e *mirrorEnv) decodeMirror(b []byte) openapi.RepoMirror {
	e.t.Helper()
	var m openapi.RepoMirror
	if err := json.Unmarshal(b, &m); err != nil {
		e.t.Fatalf("mirror non valido: %v: %s", err, b)
	}
	return m
}

func (e *mirrorEnv) get(repo string, id openapi.RepoMirror) openapi.RepoMirror {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/repos/alice/"+repo+"/mirrors/"+id.Id.String(), "alice", "")
	e.want(rec, http.StatusOK, "")
	return e.decodeMirror(rec.Body.Bytes())
}

func (e *mirrorEnv) runs(repo string, id openapi.RepoMirror) openapi.RepoMirrorRunList {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/repos/alice/"+repo+"/mirrors/"+id.Id.String()+"/runs", "alice", "")
	e.want(rec, http.StatusOK, "")
	if strings.Contains(rec.Body.String(), mirrorToken) {
		e.t.Fatalf("il token nel log: %s", rec.Body.String())
	}
	var l openapi.RepoMirrorRunList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		e.t.Fatal(err)
	}
	return l
}

func (e *mirrorEnv) process() int {
	e.t.Helper()
	n, err := e.eng.ProcessDue(context.Background())
	if err != nil {
		e.t.Fatalf("ProcessDue: %v", err)
	}
	return n
}

func (e *mirrorEnv) pushEvent(repo uuid.UUID, ref string, isDefault bool) {
	e.t.Helper()
	err := e.eng.HandlePush(context.Background(), pkgEnvelope(), gitpush.Payload{
		Repo:   gitpush.Repo{ID: repo.String(), FullName: "alice/app", DefaultBranch: "main"},
		Pusher: gitpush.Pusher{ID: aliceID, Username: "alice", Type: gitpush.PusherHuman},
		Refs:   []gitpush.RefPush{{Ref: ref, Before: strings.Repeat("1", 40), After: strings.Repeat("2", 40), IsDefaultBranch: isDefault}},
	})
	if err != nil {
		e.t.Fatal(err)
	}
}

// Criterio: API (crea, elenca, modifica, elimina, stato), credenziale cifrata e mai restituita.
func TestMirrors_API_TokenCifratoEMaiRestituito(t *testing.T) {
	e := newMirrorEnv(t)
	e.repo("app", true) // internal: bob la legge ma non è admin

	m := e.create("app", mirrorURL)
	if !m.HasToken || !m.Enabled || m.State != "pending" || m.Username != "mirror-bot" || m.Url != mirrorURL || m.NextAttemptAt == nil {
		t.Fatalf("mirror creato: %+v", m)
	}
	// Nel database il token è cifrato (AES-256-GCM), non in chiaro.
	var ct []byte
	var keyID string
	if err := e.pool.QueryRow(context.Background(), `SELECT token_ciphertext, token_key_id FROM core.repo_mirrors WHERE id = $1`, m.Id).Scan(&ct, &keyID); err != nil {
		t.Fatal(err)
	}
	if len(ct) == 0 || strings.Contains(string(ct), "ghp_") || keyID != "k1" {
		t.Fatalf("token nel database: %q (%s)", ct, keyID)
	}

	// Elenco e lettura: mai il token, in nessuna forma.
	rec := e.do(http.MethodGet, "/repos/alice/app/mirrors", "alice", "")
	e.want(rec, http.StatusOK, "")
	if strings.Contains(rec.Body.String(), mirrorToken) || strings.Contains(strings.ToLower(rec.Body.String()), `"token"`) {
		t.Fatalf("elenco con il token: %s", rec.Body.String())
	}
	var l openapi.RepoMirrorList
	_ = json.Unmarshal(rec.Body.Bytes(), &l)
	if l.Total != 1 || len(l.Items) != 1 || l.Items[0].Id != m.Id {
		t.Fatalf("elenco: %+v", l)
	}
	if got := e.get("app", m); got.Id != m.Id {
		t.Fatalf("lettura: %+v", got)
	}

	// Stesso indirizzo due volte: 409.
	e.want(e.do(http.MethodPost, "/repos/alice/app/mirrors", "alice",
		fmt.Sprintf(`{"url":%q,"username":"altro","token":"x"}`, mirrorURL)), http.StatusConflict, "conflict")

	// Modifica: pausa e nuovo token (la risposta non lo riporta).
	rec = e.do(http.MethodPatch, "/repos/alice/app/mirrors/"+m.Id.String(), "alice", `{"enabled":false,"token":"nuovo-token-segreto"}`)
	e.want(rec, http.StatusOK, "")
	if strings.Contains(rec.Body.String(), "nuovo-token") {
		t.Fatalf("il nuovo token è tornato: %s", rec.Body.String())
	}
	if got := e.decodeMirror(rec.Body.Bytes()); got.Enabled || got.NextAttemptAt != nil {
		t.Fatalf("dopo la pausa: %+v", got)
	}
	// In pausa: «Sincronizza ora» è 409 e il motore non spinge.
	e.want(e.do(http.MethodPost, "/repos/alice/app/mirrors/"+m.Id.String()+"/sync", "alice", ""), http.StatusConflict, "mirror_disabled")
	if n := e.process(); n != 0 || e.pusher.count() != 0 {
		t.Fatalf("un mirror in pausa non spinge: %d, %d chiamate", n, e.pusher.count())
	}

	// Permessi: bob legge il repo ma non è admin → 403 su ogni operazione.
	base := "/repos/alice/app/mirrors"
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, base, ""}, {http.MethodPost, base, `{"url":"https://x.example.test/a.git","username":"u","token":"t"}`},
		{http.MethodGet, base + "/" + m.Id.String(), ""}, {http.MethodPatch, base + "/" + m.Id.String(), `{"enabled":true}`},
		{http.MethodDelete, base + "/" + m.Id.String(), ""}, {http.MethodPost, base + "/" + m.Id.String() + "/sync", ""},
		{http.MethodGet, base + "/" + m.Id.String() + "/runs", ""},
	} {
		e.want(e.do(c.method, c.path, "bob", c.body), http.StatusForbidden, "forbidden")
	}
	// Un repo privato che bob non legge: 404, non 403 (non si rivela che esiste).
	e.repo("segreto", false)
	e.want(e.do(http.MethodGet, "/repos/alice/segreto/mirrors", "bob", ""), http.StatusNotFound, "not_found")

	// Un mirror di un altro repo risponde 404 anche per alice.
	e.repo("altro", true)
	e.want(e.do(http.MethodGet, "/repos/alice/altro/mirrors/"+m.Id.String(), "alice", ""), http.StatusNotFound, "not_found")

	// Eliminazione.
	e.want(e.do(http.MethodDelete, base+"/"+m.Id.String(), "alice", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodGet, base+"/"+m.Id.String(), "alice", ""), http.StatusNotFound, "not_found")
}

// Criterio: pkg/egress applicato alla destinazione (C8) e validazione dell'input.
func TestMirrors_API_ValidazioneEEgress(t *testing.T) {
	e := newMirrorEnv(t)
	e.repo("app", false)
	cases := []struct {
		name, body string
		status     int
		code       string
	}{
		{"http", `{"url":"http://mirror.example.test/a.git","username":"u","token":"t"}`, 422, "url_not_allowed"},
		{"ssh", `{"url":"ssh://git@mirror.example.test/a.git","username":"u","token":"t"}`, 422, "url_not_allowed"},
		{"userinfo", `{"url":"https://u:p@mirror.example.test/a.git","username":"u","token":"t"}`, 422, "url_not_allowed"},
		{"loopback", `{"url":"https://127.0.0.1/a.git","username":"u","token":"t"}`, 422, "url_not_allowed"},
		{"localhost", `{"url":"https://localhost/a.git","username":"u","token":"t"}`, 422, "url_not_allowed"},
		{"cluster", `{"url":"https://10.42.0.5/a.git","username":"u","token":"t"}`, 422, "url_not_allowed"},
		{"metadata", `{"url":"https://169.254.169.254/a.git","username":"u","token":"t"}`, 422, "url_not_allowed"},
		{"senza token", `{"url":"https://mirror.example.test/a.git","username":"u"}`, 422, "validation_failed"},
		{"token vuoto", `{"url":"https://mirror.example.test/a.git","username":"u","token":""}`, 422, "validation_failed"},
		{"senza utente", `{"url":"https://mirror.example.test/a.git","username":"","token":"t"}`, 422, "validation_failed"},
		{"campo ignoto", `{"url":"https://mirror.example.test/a.git","username":"u","token":"t","force":true}`, 400, ""},
	}
	for _, c := range cases {
		rec := e.do(http.MethodPost, "/repos/alice/app/mirrors", "alice", c.body)
		if rec.Code != c.status || (c.code != "" && !strings.Contains(rec.Body.String(), `"`+c.code+`"`)) {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body.String())
		}
	}
	// Una destinazione che il DNS risolve a un indirizzo interno si accetta alla
	// creazione (i nomi non si risolvono lì) ma il push è bloccato e non si ritenta.
	m := e.create("app", "https://evil.example.test/a.git")
	e.process()
	if e.pusher.count() != 0 {
		t.Fatal("verso un indirizzo interno non si deve chiamare git")
	}
	got := e.get("app", m)
	if got.State != "error" || got.LastError == nil || !strings.Contains(*got.LastError, "cluster") || got.NextAttemptAt != nil {
		t.Fatalf("destinazione bloccata: %+v", got)
	}
	runs := e.runs("app", m)
	if len(runs.Items) != 1 || runs.Items[0].Outcome != "blocked" {
		t.Fatalf("log: %+v", runs.Items)
	}
	e.clock.Advance(24 * time.Hour)
	if n := e.process(); n != 0 {
		t.Fatalf("un blocco egress non si ritenta: %d", n)
	}
	// Solo un intervallo speciale (CGNAT): bloccato salvo allowlist dell’amministratore.
	m2 := e.create("app", "https://internal.example.test/a.git")
	e.process()
	if got := e.get("app", m2); got.State != "error" || e.pusher.count() != 0 {
		t.Fatalf("indirizzo privato: %+v", got)
	}
}

// Il servizio git riceve il token in chiaro (decifrato) e l'IP scelto da core: è
// l'unico posto, e lo stato dopo un successo è in_sync con gli SHA spinti.
func TestMirrors_Esecuzione_Successo(t *testing.T) {
	e := newMirrorEnv(t)
	repoID := e.repo("app", false)
	m := e.create("app", mirrorURL)

	if n := e.process(); n != 1 {
		t.Fatalf("mirror eseguiti = %d", n)
	}
	if e.pusher.count() != 1 {
		t.Fatalf("chiamate a git = %d", e.pusher.count())
	}
	call := e.pusher.calls[0]
	if call.repo != repoID || call.in.Token != mirrorToken || call.in.Username != "mirror-bot" || call.in.IP != "93.184.216.34" ||
		call.in.DefaultBranch != "main" || call.in.URL != mirrorURL {
		t.Fatalf("chiamata a git: %v (repo %s)", call.in, call.repo)
	}
	// Il Stringer non stampa il token.
	if strings.Contains(fmt.Sprintf("%v %+v", call.in, call.in), mirrorToken) {
		t.Fatal("MirrorPushInput stampa il token")
	}
	got := e.get("app", m)
	if got.State != "in_sync" || got.LastSuccessAt == nil || got.LastAttemptAt == nil || got.NextAttemptAt != nil || got.LastError != nil ||
		got.LastPushed["refs/heads/main"] != strings.Repeat("a", 40) || got.LastPushed["refs/tags/v1"] != strings.Repeat("b", 40) {
		t.Fatalf("stato dopo il successo: %+v", got)
	}
	runs := e.runs("app", m)
	if len(runs.Items) != 1 || runs.Items[0].Outcome != "success" || len(runs.Items[0].Refs) != 2 || runs.Items[0].Attempt != 1 {
		t.Fatalf("log: %+v", runs.Items)
	}

	// Un push su un altro branch non tocca il mirror; sul principale e sui tag sì.
	e.pushEvent(repoID, "refs/heads/feature", false)
	if got := e.get("app", m); got.State != "in_sync" {
		t.Fatalf("un push su un altro branch non deve accodare: %+v", got)
	}
	e.pushEvent(repoID, "refs/heads/main", true)
	if got := e.get("app", m); got.State != "pending" || got.NextAttemptAt == nil {
		t.Fatalf("push sul principale: %+v", got)
	}
	e.process()
	e.pushEvent(repoID, "refs/tags/v2", false)
	if got := e.get("app", m); got.State != "pending" {
		t.Fatalf("push di un tag: %+v", got)
	}
	// Una cancellazione di tag non si specchia (mai cancellazioni sulla destinazione).
	e.process()
	err := e.eng.HandlePush(context.Background(), pkgEnvelope(), gitpush.Payload{
		Repo: gitpush.Repo{ID: repoID.String(), DefaultBranch: "main"}, Pusher: gitpush.Pusher{ID: aliceID, Username: "alice"},
		Refs: []gitpush.RefPush{{Ref: "refs/tags/v2", Before: strings.Repeat("1", 40), After: gitpush.ZeroSHA}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.get("app", m); got.State != "in_sync" {
		t.Fatalf("cancellazione di un tag: %+v", got)
	}
}

// Criterio: tentativi con backoff e log delle esecuzioni.
func TestMirrors_Backoff(t *testing.T) {
	e := newMirrorEnv(t)
	e.repo("app", false)
	m := e.create("app", mirrorURL)
	e.pusher.setAnswer(func(int, gitclient.MirrorPushInput) (gitclient.MirrorPushResult, error) {
		return gitclient.MirrorPushResult{}, errors.New("servizio git non disponibile con " + mirrorToken)
	})
	start := e.clock.Now()
	waits := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour, 6 * time.Hour}
	for i, wait := range waits {
		if n := e.process(); n != 1 {
			t.Fatalf("giro %d: eseguiti %d", i, n)
		}
		got := e.get("app", m)
		if got.State != "error" || got.Attempts != i+1 || got.NextAttemptAt == nil {
			t.Fatalf("giro %d: %+v", i, got)
		}
		if got.LastError == nil || strings.Contains(*got.LastError, mirrorToken) {
			t.Fatalf("giro %d: errore con il token: %v", i, got.LastError)
		}
		if d := got.NextAttemptAt.Sub(e.clock.Now()); d != wait {
			t.Fatalf("giro %d: prossimo tentativo fra %s, voluto %s", i, d, wait)
		}
		// Prima della scadenza non si ritenta; alla scadenza sì.
		e.clock.Advance(wait - time.Second)
		if n := e.process(); n != 0 {
			t.Fatalf("giro %d: ritentato in anticipo", i)
		}
		e.clock.Advance(time.Second)
	}
	if e.clock.Now().Sub(start) < 16*time.Hour {
		t.Fatal("orologio")
	}
	// Il log tiene le esecuzioni (potatura a 50) e nessuna contiene il token.
	runs := e.runs("app", m)
	if len(runs.Items) != len(waits) || runs.Items[0].Attempt != len(waits) || runs.Items[0].Outcome != "error" {
		t.Fatalf("log: %d righe, prima %+v", len(runs.Items), runs.Items[0])
	}
	// Un successo azzera i tentativi.
	e.pusher.setAnswer(nil)
	e.process()
	if got := e.get("app", m); got.State != "in_sync" || got.Attempts != 0 {
		t.Fatalf("dopo il successo: %+v", got)
	}
	// Potatura: oltre 50 righe restano le ultime 50.
	for i := 0; i < 60; i++ {
		e.sql(`INSERT INTO core.repo_mirror_runs (id, mirror_id, started_at, finished_at, outcome) VALUES (gen_random_uuid(), $1, now() - ($2 || ' minutes')::interval, now(), 'error')`, m.Id, fmt.Sprint(i+1))
	}
	e.sql(`UPDATE core.repo_mirrors SET pending = true, next_attempt_at = now() - interval '1 hour' WHERE id = $1`, m.Id)
	e.clock.Advance(24 * time.Hour)
	e.process()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.repo_mirror_runs WHERE mirror_id = $1`, m.Id).Scan(&n); err != nil || n != mirrors.MaxRuns {
		t.Fatalf("log dopo la potatura: %d (%v)", n, err)
	}
	// E il log vecchio di 30 giorni si elimina.
	e.sql(`UPDATE core.repo_mirror_runs SET started_at = $2 WHERE mirror_id = $1 AND id IN (SELECT id FROM core.repo_mirror_runs WHERE mirror_id = $1 LIMIT 5)`, m.Id, e.clock.Now().Add(-31*24*time.Hour))
	if got, err := e.eng.PurgeLog(context.Background(), e.clock.Now()); err != nil || got != 5 {
		t.Fatalf("PurgeLog = %d (%v)", got, err)
	}
}

// Criterio: un solo push concorrente per mirror (affitto, anche con più repliche).
func TestMirrors_UnSoloPushPerMirror(t *testing.T) {
	e := newMirrorEnv(t)
	repoID := e.repo("app", false)
	m := e.create("app", mirrorURL)
	e.pusher.block = make(chan struct{})
	e.pusher.started = make(chan struct{}, 4)

	done := make(chan int, 1)
	go func() { done <- e.process() }()
	select {
	case <-e.pusher.started:
	case <-time.After(20 * time.Second):
		t.Fatal("il primo push non è partito")
	}
	// Un'altra «replica» (motore nuovo) mentre il primo è in corso: niente da fare.
	other := &mirrors.Engine{Pool: e.pool, Keys: e.keys, Git: e.pusher, Egress: e.eng.Egress, Now: e.clock.Now, Lease: 5 * time.Minute}
	if n, err := other.ProcessDue(context.Background()); err != nil || n != 0 {
		t.Fatalf("seconda replica: %d (%v)", n, err)
	}
	if got := e.get("app", m); got.State != "syncing" {
		t.Fatalf("durante il push: %+v", got)
	}
	// Un push arrivato mentre gira non si perde: finito il primo, il mirror resta in coda.
	e.pushEvent(repoID, "refs/heads/main", true)
	close(e.pusher.block)
	if n := <-done; n != 1 {
		t.Fatalf("eseguiti %d", n)
	}
	if got := e.get("app", m); got.State != "pending" || got.NextAttemptAt == nil {
		t.Fatalf("push arrivato durante l'esecuzione: %+v", got)
	}
	e.pusher.block = nil
	e.process()
	if got := e.get("app", m); got.State != "in_sync" {
		t.Fatalf("dopo il secondo giro: %+v", got)
	}
	if e.pusher.count() != 2 {
		t.Fatalf("chiamate a git = %d, volute 2", e.pusher.count())
	}
	// Un affitto scaduto (crash del worker) si riprende.
	e.sql(`UPDATE core.repo_mirrors SET state = 'syncing', pending = true, next_attempt_at = now() - interval '1 hour', lease_until = $2 WHERE id = $1`, m.Id, e.clock.Now().Add(time.Minute))
	if n := e.process(); n != 0 {
		t.Fatalf("affitto ancora valido: %d", n)
	}
	e.clock.Advance(2 * time.Minute)
	if n := e.process(); n != 1 {
		t.Fatalf("affitto scaduto: eseguiti %d", n)
	}
}

// Criterio: destinazione divergente: nessun force, stato «diverged» con motivo
// e notifica agli admin; riparte solo con «Sincronizza ora».
func TestMirrors_Divergenza_NotificaERipartenza(t *testing.T) {
	e := newMirrorEnv(t)
	repoID := e.repo("app", true)
	m := e.create("app", mirrorURL)
	e.pusher.setAnswer(func(int, gitclient.MirrorPushInput) (gitclient.MirrorPushResult, error) {
		return gitclient.MirrorPushResult{OK: false, Diverged: true, Refs: []gitclient.MirrorRef{
			{Ref: "refs/heads/main", Status: "rejected", Reason: "non-fast-forward"},
		}, Error: "refs/heads/main: rejected (non-fast-forward)"}, nil
	})
	e.process()
	got := e.get("app", m)
	if got.State != "diverged" || got.LastError == nil || !strings.Contains(*got.LastError, "non-fast-forward") || got.NextAttemptAt != nil {
		t.Fatalf("stato: %+v", got)
	}
	runs := e.runs("app", m)
	if len(runs.Items) != 1 || runs.Items[0].Outcome != "diverged" || runs.Items[0].Refs[0].Status != "rejected" {
		t.Fatalf("log: %+v", runs.Items)
	}
	// Notifica all'admin (alice, che ha creato il mirror ed è proprietaria); non a bob.
	var forAlice, forBob int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.notifications WHERE reason = 'mirror' AND user_id = $1 AND mirror_id = $2 AND repo_id = $3`, aliceID, m.Id, repoID).Scan(&forAlice)
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.notifications WHERE reason = 'mirror' AND user_id = $1`, bobID).Scan(&forBob)
	if forAlice != 1 || forBob != 0 {
		t.Fatalf("notifiche: alice %d, bob %d", forAlice, forBob)
	}
	rec := e.do(http.MethodGet, "/notifications?reason=mirror", "alice", "")
	e.want(rec, http.StatusOK, "")
	if !strings.Contains(rec.Body.String(), "alice/app") || !strings.Contains(rec.Body.String(), "storia diversa") {
		t.Fatalf("notifica nella casella: %s", rec.Body.String())
	}

	// Fermo: né i push né il tempo lo riaccodano, nessun tentativo automatico.
	e.pushEvent(repoID, "refs/heads/main", true)
	e.clock.Advance(48 * time.Hour)
	if n := e.process(); n != 0 || e.pusher.count() != 1 {
		t.Fatalf("un mirror diverged non si ritenta: %d eseguiti, %d chiamate", n, e.pusher.count())
	}
	if got := e.get("app", m); got.State != "diverged" {
		t.Fatalf("dopo un push: %+v", got)
	}

	// «Sincronizza ora»: 202, torna in coda; se la destinazione è tornata allineata, in_sync.
	e.pusher.setAnswer(nil)
	rec = e.do(http.MethodPost, "/repos/alice/app/mirrors/"+m.Id.String()+"/sync", "alice", "")
	e.want(rec, http.StatusAccepted, "")
	if got := e.decodeMirror(rec.Body.Bytes()); got.State != "pending" || got.NextAttemptAt == nil {
		t.Fatalf("dopo sync: %+v", got)
	}
	e.process()
	if got := e.get("app", m); got.State != "in_sync" || got.LastError != nil {
		t.Fatalf("dopo la ripartenza: %+v", got)
	}
}

// Il token illeggibile (chiave persa) è un errore chiaro, non un panic né un push senza credenziale.
func TestMirrors_TokenIlleggibile(t *testing.T) {
	e := newMirrorEnv(t)
	e.repo("app", false)
	m := e.create("app", mirrorURL)
	e.sql(`UPDATE core.repo_mirrors SET token_key_id = 'sconosciuta' WHERE id = $1`, m.Id)
	e.process()
	got := e.get("app", m)
	if e.pusher.count() != 0 || got.State != "error" || got.LastError == nil || got.NextAttemptAt != nil {
		t.Fatalf("token illeggibile: %+v (chiamate %d)", got, e.pusher.count())
	}
}

// Senza chiave dei segreti non si crea un mirror (503), mai un token in chiaro.
func TestMirrors_SenzaChiave_503(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	base := &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}
	e := &issuesEnv{t: t, pool: pool, id: base}
	e.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(base), httpserver.WithReadableLister(base), httpserver.WithGit(newFakeGit()),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}),
		httpserver.WithMirrors(httpserver.MirrorConfig{}))
	e.repo("app", false)
	e.want(e.do(http.MethodPost, "/repos/alice/app/mirrors", "alice",
		`{"url":"https://mirror.example.test/a.git","username":"u","token":"t"}`), http.StatusServiceUnavailable, "mirror_secrets_unavailable")
}

func pkgEnvelope() pkgevents.Envelope { return pkgevents.Envelope{} }
