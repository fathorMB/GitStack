//go:build integration

package httpserver_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// M-06/G (GIT-135): webhook di repo e di organizzazione, consegna firmata,
// tentativi (C7), disattivazione dopo 3 giorni, log, Redeliver e permessi (C6).
// Il ricevitore è un server HTTP finto; il tempo è iniettato (hookClock).

const hookSecretKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

// hookIdentity aggiunge al fake di identity gli owner delle organizzazioni.
type hookIdentity struct {
	*issuesIdentity
}

func (f *hookIdentity) OrgOwners(_ context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids, ok := f.orgOwner[orgID.String()]
	if !ok {
		return nil, identityclient.ErrNotFound
	}
	out := make([]uuid.UUID, 0, len(ids))
	for _, s := range ids {
		out = append(out, uuid.MustParse(s))
	}
	return out, nil
}

var _ identityclient.OrgOwnerLister = (*hookIdentity)(nil)

type hookClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *hookClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *hookClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// received è una richiesta arrivata al ricevitore finto.
type received struct {
	header http.Header
	body   []byte
}

// receiver è il server finto: risponde con handler e tiene le richieste.
type receiver struct {
	srv *httptest.Server
	mu  sync.Mutex
	got []received
	// answer decide la risposta; default 200.
	answer func(n int, w http.ResponseWriter)
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	r := &receiver{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.got = append(r.got, received{header: req.Header.Clone(), body: body})
		n := len(r.got)
		answer := r.answer
		r.mu.Unlock()
		if answer != nil {
			answer(n, w)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *receiver) requests() []received {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.got)
}

func (r *receiver) status(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.answer = func(_ int, w http.ResponseWriter) { w.WriteHeader(code) }
}

// toServer è il Doer dei test: porta ogni richiesta al ricevitore finto, qualunque
// sia l'host dell'indirizzo configurato (il client vero, pkg/egress, blocca
// il loopback dove gira il server di prova: lo si prova a parte).
type toServer struct {
	base *url.URL
	err  error
}

func (d *toServer) Do(req *http.Request) (*http.Response, error) {
	if d.err != nil {
		return nil, d.err
	}
	r2 := req.Clone(req.Context())
	r2.URL.Scheme, r2.URL.Host = d.base.Scheme, d.base.Host
	r2.Host = req.URL.Host
	return http.DefaultClient.Do(r2)
}

type hookEnv struct {
	*issuesEnv
	hid    *hookIdentity
	clock  *hookClock
	recv   *receiver
	doer   *toServer
	engine *webhooks.Engine
	keys   *webhooks.Keyring
}

func newHookEnv(t *testing.T) *hookEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	base := &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}
	hid := &hookIdentity{issuesIdentity: base}
	keys, err := webhooks.NewKeyring("k1", hookSecretKey, "")
	if err != nil {
		t.Fatal(err)
	}
	_, check, err := webhooks.NewEgress(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := &issuesEnv{t: t, pool: pool, id: base}
	e.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(hid), httpserver.WithReadableLister(hid), httpserver.WithGit(newFakeGit()),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}),
		httpserver.WithWebhooks(httpserver.WebhookConfig{Keys: keys, CheckURL: check}))
	recv := newReceiver(t)
	u, _ := url.Parse(recv.srv.URL)
	// L'orologio parte un'ora avanti: il Redeliver dell'API usa l'ora vera e
	// deve risultare già scaduto per il motore.
	clock := &hookClock{t: time.Now().UTC().Add(time.Hour).Truncate(time.Second)}
	he := &hookEnv{issuesEnv: e, hid: hid, clock: clock, recv: recv, doer: &toServer{base: u}, keys: keys}
	he.engine = he.newEngine()
	return he
}

// repo crea un repo e fa elaborare gli eventi di creazione, così non toccano
// i webhook creati dopo.
func (e *hookEnv) repo(name string, internal bool) uuid.UUID {
	e.t.Helper()
	id := e.issuesEnv.repo(name, internal)
	e.run()
	return id
}

// newEngine è un motore nuovo sullo stesso database e sullo stesso orologio:
// «core riavviato».
func (e *hookEnv) newEngine() *webhooks.Engine {
	return &webhooks.Engine{Pool: e.pool, Keys: e.keys, HTTP: e.doer, Users: e.hid, Managers: e.hid, Now: e.clock.Now}
}

// run crea le consegne dagli eventi e invia quelle scadute.
func (e *hookEnv) run() int {
	e.t.Helper()
	return e.runWith(e.engine)
}

func (e *hookEnv) runWith(eng *webhooks.Engine) int {
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

const hookURL = "http://hooks.example.test/in"

// repoHook crea un webhook sul repo alice/<repo> e ne ritorna l'id.
func (e *hookEnv) repoHook(repo, body string) string {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/repos/alice/"+repo+"/hooks", "alice", body)
	e.want(rec, http.StatusCreated, "")
	return e.hookID(rec)
}

func (e *hookEnv) hookID(rec *httptest.ResponseRecorder) string {
	e.t.Helper()
	var w openapi.Webhook
	if err := json.Unmarshal(rec.Body.Bytes(), &w); err != nil {
		e.t.Fatalf("webhook non valido: %v: %s", err, rec.Body.String())
	}
	return w.Id.String()
}

// find cerca una consegna per id nell'elenco.
func find(ds []openapi.WebhookDeliverySummary, id openapi_types.UUID) (openapi.WebhookDeliverySummary, bool) {
	for _, d := range ds {
		if d.Id == id {
			return d, true
		}
	}
	return openapi.WebhookDeliverySummary{}, false
}

func (e *hookEnv) deliveries(repo, hook string) []openapi.WebhookDeliverySummary {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/repos/alice/"+repo+"/hooks/"+hook+"/deliveries", "alice", "")
	e.want(rec, http.StatusOK, "")
	var l openapi.WebhookDeliveryList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		e.t.Fatal(err)
	}
	return l.Items
}

func (e *hookEnv) hook(repo, hook string) openapi.Webhook {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/repos/alice/"+repo+"/hooks/"+hook, "alice", "")
	e.want(rec, http.StatusOK, "")
	var w openapi.Webhook
	if err := json.Unmarshal(rec.Body.Bytes(), &w); err != nil {
		e.t.Fatal(err)
	}
	return w
}

func verifySignature(t *testing.T, secret string, req received) {
	t.Helper()
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(req.body)
	want := "sha256=" + hex.EncodeToString(m.Sum(nil))
	if got := req.header.Get("X-GitStack-Signature"); !hmac.Equal([]byte(got), []byte(want)) {
		t.Fatalf("firma %q, voluta %q", got, want)
	}
}

// Criterio: firma verificabile, intestazioni e payload v1 di un evento issues.
func TestWebhooks_ConsegnaFirmataEPayloadV1(t *testing.T) {
	e := newHookEnv(t)
	e.repo("app", true) // internal: bob la legge e può aprire issue
	rec := e.do(http.MethodPost, "/repos/alice/app/hooks", "alice",
		fmt.Sprintf(`{"url":%q,"events":["issues","issue_comment"],"secret":"s3cr3t-di-prova"}`, hookURL))
	e.want(rec, http.StatusCreated, "")
	if strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Fatalf("il segreto non deve tornare mai: %s", rec.Body.String())
	}
	var created openapi.Webhook
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if !created.HasSecret || !created.Active || created.Scope != "repo" || created.Repository == nil || created.Repository.FullName != "alice/app" {
		t.Fatalf("webhook creato: %+v", created)
	}
	hook := created.Id.String()
	// Il segreto è cifrato nel database, non in chiaro.
	var ct []byte
	var keyID string
	if err := e.pool.QueryRow(context.Background(), `SELECT secret_ciphertext, secret_key_id FROM core.webhooks WHERE id = $1`, hook).Scan(&ct, &keyID); err != nil {
		t.Fatal(err)
	}
	if len(ct) == 0 || strings.Contains(string(ct), "s3cr3t") || keyID != "k1" {
		t.Fatalf("segreto nel database: %q (%s)", ct, keyID)
	}

	n := e.open("app", "bob", "Il parser si blocca")
	if got := e.run(); got != 1 {
		t.Fatalf("consegne inviate = %d, volute 1", got)
	}
	reqs := e.recv.requests()
	if len(reqs) != 1 {
		t.Fatalf("richieste al ricevitore = %d", len(reqs))
	}
	r := reqs[0]
	verifySignature(t, "s3cr3t-di-prova", r)
	if r.header.Get("X-GitStack-Event") != "issues" || r.header.Get("Content-Type") != "application/json" ||
		r.header.Get("User-Agent") != "GitStack-Webhook/1" || r.header.Get("X-GitStack-Payload-Version") != "1" {
		t.Fatalf("intestazioni: %v", r.header)
	}
	delivery := r.header.Get("X-GitStack-Delivery")
	if _, err := uuid.Parse(delivery); err != nil {
		t.Fatalf("X-GitStack-Delivery = %q", delivery)
	}
	var p map[string]any
	if err := json.Unmarshal(r.body, &p); err != nil {
		t.Fatal(err)
	}
	issue, _ := p["issue"].(map[string]any)
	repo, _ := p["repository"].(map[string]any)
	sender, _ := p["sender"].(map[string]any)
	if p["version"] != float64(1) || p["event"] != "issues" || p["action"] != "opened" ||
		issue["number"] != float64(n) || issue["title"] != "Il parser si blocca" || issue["state"] != "open" ||
		repo["fullName"] != "alice/app" || repo["owner"] != "alice" || repo["name"] != "app" || repo["visibility"] != "internal" ||
		sender["username"] != "bob" || sender["type"] != "human" || sender["id"] != bobID {
		t.Fatalf("payload inatteso: %s", r.body)
	}
	if _, ok := p["organization"]; ok {
		t.Fatalf("un repo personale non ha organization: %s", r.body)
	}
	if author, _ := issue["author"].(map[string]any); author["username"] != "bob" {
		t.Fatalf("autore: %s", r.body)
	}

	// Log: la consegna è nell'elenco e nel dettaglio, con richiesta e risposta.
	ds := e.deliveries("app", hook)
	if len(ds) != 1 || ds[0].Id.String() != delivery || ds[0].Status != "success" || ds[0].Attempt != 1 || ds[0].StatusCode == nil || *ds[0].StatusCode != 200 {
		t.Fatalf("log: %+v", ds)
	}
	rec = e.do(http.MethodGet, "/repos/alice/app/hooks/"+hook+"/deliveries/"+delivery, "alice", "")
	e.want(rec, http.StatusOK, "")
	var det openapi.WebhookDeliveryDetail
	_ = json.Unmarshal(rec.Body.Bytes(), &det)
	if det.Request.Headers["X-GitStack-Signature"] != r.header.Get("X-GitStack-Signature") || det.Request.Payload["event"] != "issues" ||
		det.Response == nil || det.DurationMs == nil {
		t.Fatalf("dettaglio: %s", rec.Body.String())
	}
	if w := e.hook("app", hook); w.LastDelivery == nil || w.LastDelivery.Status != "success" {
		t.Fatalf("lastDelivery: %+v", w.LastDelivery)
	}

	// Un commento arriva come issue_comment; un evento non selezionato no.
	e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/issues/%d/comments", n), "alice", `{"body":"Riprodotto"}`), http.StatusCreated, "")
	e.run()
	reqs = e.recv.requests()
	if len(reqs) != 2 || reqs[1].header.Get("X-GitStack-Event") != "issue_comment" {
		t.Fatalf("richieste dopo il commento: %d", len(reqs))
	}
	verifySignature(t, "s3cr3t-di-prova", reqs[1])
	var cp map[string]any
	_ = json.Unmarshal(reqs[1].body, &cp)
	if c, _ := cp["comment"].(map[string]any); cp["action"] != "created" || c["body"] != "Riprodotto" {
		t.Fatalf("payload del commento: %s", reqs[1].body)
	}

	// Chiusura: `issues` / `closed`, con il motivo.
	e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/issues/%d/close", n), "alice", `{"reason":"completed"}`), http.StatusOK, "")
	e.run()
	reqs = e.recv.requests()
	var closed map[string]any
	_ = json.Unmarshal(reqs[len(reqs)-1].body, &closed)
	if is, _ := closed["issue"].(map[string]any); closed["action"] != "closed" || is["state"] != "closed" || is["closeReason"] != "completed" {
		t.Fatalf("payload della chiusura: %s", reqs[len(reqs)-1].body)
	}
}

// Senza segreto la consegna non è firmata; un evento non selezionato non parte;
// un'issue nascosta non ha webhook (I4).
func TestWebhooks_SenzaSegretoEventiSelezionatiENascoste(t *testing.T) {
	e := newHookEnv(t)
	e.repo("app", false)
	hook := e.repoHook("app", fmt.Sprintf(`{"url":%q,"events":["issues"]}`, hookURL))
	commentsOnly := e.repoHook("app", fmt.Sprintf(`{"url":%q,"events":["repository"]}`, hookURL))
	n := e.open("app", "alice", "Una")
	e.run()
	reqs := e.recv.requests()
	if len(reqs) != 1 {
		t.Fatalf("richieste = %d, voluta 1 (solo il webhook issues)", len(reqs))
	}
	if reqs[0].header.Get("X-GitStack-Signature") != "" {
		t.Fatal("senza segreto non c'è firma")
	}
	if got := e.deliveries("app", commentsOnly); len(got) != 0 {
		t.Fatalf("il webhook repository ha ricevuto %d consegne", len(got))
	}
	e.want(e.do(http.MethodPut, fmt.Sprintf("/repos/alice/app/issues/%d/hidden", n), "alice", `{"hidden":true}`), http.StatusOK, "")
	e.want(e.do(http.MethodPost, fmt.Sprintf("/repos/alice/app/issues/%d/comments", n), "alice", `{"body":"x"}`), http.StatusCreated, "")
	e.run()
	if got := len(e.deliveries("app", hook)); got != 1 {
		t.Fatalf("una issue nascosta non ha webhook: consegne = %d", got)
	}
	// Un webhook in pausa (active:false) non riceve niente.
	e.want(e.do(http.MethodPatch, "/repos/alice/app/hooks/"+hook, "alice", `{"active":false}`), http.StatusOK, "")
	e.open("app", "alice", "Due")
	e.run()
	if got := len(e.deliveries("app", hook)); got != 1 {
		t.Fatalf("un webhook in pausa ha ricevuto: %d", got)
	}
}

// Criterio: tentativi con attesa crescente (tempo iniettato), fino a 8 in ~24 ore.
func TestWebhooks_TentativiConAttesaCrescente(t *testing.T) {
	e := newHookEnv(t)
	e.repo("app", false)
	hook := e.repoHook("app", fmt.Sprintf(`{"url":%q,"events":["issues"],"secret":"k"}`, hookURL))
	e.recv.status(http.StatusInternalServerError)
	start := e.clock.Now()
	e.open("app", "alice", "Una")

	want := []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 9 * time.Hour}
	var at []time.Time // quando è partito ogni tentativo
	for i := 1; i <= webhooks.MaxAttempts; i++ {
		if got := e.run(); got != 1 {
			t.Fatalf("tentativo %d: consegne inviate = %d", i, got)
		}
		at = append(at, e.clock.Now())
		ds := e.deliveries("app", hook)
		if len(ds) != 1 || ds[0].Attempt != i || ds[0].StatusCode == nil || *ds[0].StatusCode != 500 {
			t.Fatalf("dopo il tentativo %d: %+v", i, ds)
		}
		if i == webhooks.MaxAttempts {
			if ds[0].Status != "failed" || ds[0].NextAttemptAt != nil {
				t.Fatalf("dopo l'ottavo tentativo: %+v", ds[0])
			}
			break
		}
		if ds[0].Status != "pending" || ds[0].NextAttemptAt == nil {
			t.Fatalf("dopo il tentativo %d la consegna deve restare pending: %+v", i, ds[0])
		}
		if wait := ds[0].NextAttemptAt.Sub(e.clock.Now()); wait != want[i-1] {
			t.Fatalf("attesa dopo il tentativo %d = %v, voluta %v", i, wait, want[i-1])
		}
		// Prima della scadenza non si ritenta.
		e.clock.Advance(want[i-1] - time.Second)
		if got := e.run(); got != 0 {
			t.Fatalf("ritentata %v prima della scadenza", time.Second)
		}
		e.clock.Advance(time.Second)
	}
	if total := at[len(at)-1].Sub(start); total != 23*time.Hour+36*time.Minute {
		t.Fatalf("durata dei tentativi = %v, voluta 23h36m", total)
	}
	if len(e.recv.requests()) != webhooks.MaxAttempts {
		t.Fatalf("richieste = %d, volute %d", len(e.recv.requests()), webhooks.MaxAttempts)
	}
	// Finiti i tentativi non si ritenta più.
	e.clock.Advance(48 * time.Hour)
	if got := e.run(); got != 0 {
		t.Fatalf("consegna fallita ritentata: %d", got)
	}
	// Un tentativo che poi riesce chiude la consegna.
}

func TestWebhooks_EsitiChiusiNonSiRitentano(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		wantStatus string
	}{
		{"404", http.StatusNotFound, "failed"},
		{"400", http.StatusBadRequest, "failed"},
		{"410", http.StatusGone, "gone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newHookEnv(t)
			e.repo("app", false)
			hook := e.repoHook("app", fmt.Sprintf(`{"url":%q,"events":["issues"]}`, hookURL))
			e.recv.status(tc.status)
			e.open("app", "alice", "Una")
			e.run()
			e.clock.Advance(48 * time.Hour)
			e.run()
			ds := e.deliveries("app", hook)
			if len(ds) != 1 || string(ds[0].Status) != tc.wantStatus || ds[0].Attempt != 1 || ds[0].NextAttemptAt != nil {
				t.Fatalf("consegna: %+v", ds)
			}
			if len(e.recv.requests()) != 1 {
				t.Fatalf("richieste = %d: nessun altro tentativo dopo un %d", len(e.recv.requests()), tc.status)
			}
			// Il 410 ferma la consegna ma si può rimandare a mano.
			e.recv.status(200)
			rec := e.do(http.MethodPost, "/repos/alice/app/hooks/"+hook+"/deliveries/"+ds[0].Id.String()+"/redeliver", "alice", "")
			e.want(rec, http.StatusAccepted, "")
			e.run()
			got := e.deliveries("app", hook)
			ok := len(got) == 2
			for _, d := range got {
				if d.RedeliveryOf != nil && d.Status != "success" {
					ok = false
				}
			}
			if !ok {
				t.Fatalf("dopo Redeliver: %+v", got)
			}
		})
	}
}

// Errore di rete e timeout si ritentano come i 5xx.
func TestWebhooks_ErroreDiReteESuccessivoSuccesso(t *testing.T) {
	e := newHookEnv(t)
	e.repo("app", false)
	hook := e.repoHook("app", fmt.Sprintf(`{"url":%q,"events":["issues"]}`, hookURL))
	e.doer.err = context.DeadlineExceeded
	e.open("app", "alice", "Una")
	e.run()
	ds := e.deliveries("app", hook)
	if len(ds) != 1 || ds[0].Status != "pending" || ds[0].StatusCode != nil || ds[0].Error == nil || !strings.Contains(*ds[0].Error, "deadline") {
		t.Fatalf("dopo un timeout: %+v", ds)
	}
	if w := e.hook("app", hook); w.FailingSince == nil {
		t.Fatal("un fallimento avvia il conto (failingSince)")
	}
	e.doer.err = nil
	e.clock.Advance(time.Minute)
	e.run()
	ds = e.deliveries("app", hook)
	if ds[0].Status != "success" || ds[0].Attempt != 2 || ds[0].Error != nil {
		t.Fatalf("dopo il secondo tentativo: %+v", ds[0])
	}
	if w := e.hook("app", hook); w.FailingSince != nil || !w.Active {
		t.Fatalf("un successo azzera il conto: %+v", w)
	}
}

// Criterio: dopo 3 giorni di fallimenti consecutivi (tempo iniettato) il
// webhook si disattiva e chi lo gestisce riceve una notifica `webhook`.
func TestWebhooks_DisattivazioneDopoTreGiorniConNotifica(t *testing.T) {
	e := newHookEnv(t)
	e.repo("app", false)
	hook := e.repoHook("app", fmt.Sprintf(`{"url":%q,"events":["issues"]}`, hookURL))
	e.recv.status(http.StatusBadGateway)
	e.open("app", "alice", "Una")
	e.run()
	w := e.hook("app", hook)
	if !w.Active || w.FailingSince == nil {
		t.Fatalf("dopo il primo fallimento: %+v", w)
	}
	// Poco meno di 3 giorni dopo l'inizio dei fallimenti: ancora attivo.
	e.clock.Advance(3*24*time.Hour - time.Hour)
	e.run()
	if w := e.hook("app", hook); !w.Active {
		t.Fatalf("disattivato in anticipo: %+v", w)
	}
	var n int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.notifications WHERE reason = 'webhook'`).Scan(&n)
	if n != 0 {
		t.Fatalf("notifiche prima dei 3 giorni: %d", n)
	}
	// Al superamento dei 3 giorni il prossimo fallimento lo disattiva.
	e.clock.Advance(2 * time.Hour)
	e.run()
	w = e.hook("app", hook)
	if w.Active || w.DisabledAt == nil || w.DisabledReason == nil || *w.DisabledReason != "consecutive_failures" {
		t.Fatalf("webhook dopo 3 giorni: %+v", w)
	}
	rec := e.do(http.MethodGet, "/notifications?reason=webhook", "alice", "")
	e.want(rec, http.StatusOK, "")
	var list openapi.NotificationList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].Reason != "webhook" || list.Items[0].Webhook == nil ||
		list.Items[0].Webhook.Id.String() != hook || list.Items[0].Webhook.Scope != "repo" {
		t.Fatalf("notifica di alice: %s", rec.Body.String())
	}
	// Chi non gestisce il webhook non riceve niente.
	e.want(e.do(http.MethodGet, "/notifications?reason=webhook", "bob", ""), http.StatusOK, "")
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.notifications WHERE reason = 'webhook' AND user_id <> $1`, aliceID).Scan(&n)
	if n != 0 {
		t.Fatalf("notifiche a chi non gestisce il webhook: %d", n)
	}
	// Disattivato: niente nuove consegne; active:true non basta (409), reactivate sì.
	before := len(e.deliveries("app", hook))
	e.open("app", "alice", "Due")
	e.run()
	if got := len(e.deliveries("app", hook)); got != before {
		t.Fatalf("un webhook disattivato ha ricevuto consegne nuove: %d -> %d", before, got)
	}
	e.want(e.do(http.MethodPatch, "/repos/alice/app/hooks/"+hook, "alice", `{"active":true}`), http.StatusConflict, "webhook_disabled")
	rec = e.do(http.MethodPost, "/repos/alice/app/hooks/"+hook+"/reactivate", "alice", "")
	e.want(rec, http.StatusOK, "")
	w = e.hook("app", hook)
	if !w.Active || w.FailingSince != nil || w.DisabledAt != nil || w.DisabledReason != nil {
		t.Fatalf("dopo reactivate: %+v", w)
	}
	e.want(e.do(http.MethodPost, "/repos/alice/app/hooks/"+hook+"/reactivate", "alice", ""), http.StatusOK, "")
}

// Un webhook di organizzazione disattivato avvisa tutti gli owner.
func TestWebhooks_OrganizzazioneDisattivazioneAvvisaGliOwner(t *testing.T) {
	e := newHookEnv(t)
	e.hid.orgOwner[acmeID] = []string{aliceID, bobID}
	e.want(e.do(http.MethodPost, "/repos", "alice", `{"owner":"acme","name":"tools","visibility":"private"}`), http.StatusCreated, "")
	rec := e.do(http.MethodPost, "/orgs/acme/hooks", "alice", fmt.Sprintf(`{"url":%q,"events":["issues"]}`, hookURL))
	e.want(rec, http.StatusCreated, "")
	hook := e.hookID(rec)
	e.recv.status(http.StatusServiceUnavailable)
	e.want(e.do(http.MethodPost, "/repos/acme/tools/issues", "alice", `{"title":"X","body":"y"}`), http.StatusCreated, "")
	e.run()
	var org openapi.Webhook
	rec = e.do(http.MethodGet, "/orgs/acme/hooks/"+hook, "alice", "")
	e.want(rec, http.StatusOK, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &org)
	if org.Scope != "org" || org.Organization == nil || *org.Organization != "acme" || org.Repository != nil {
		t.Fatalf("webhook di organizzazione: %+v", org)
	}
	// Il payload porta l'organizzazione.
	var p map[string]any
	_ = json.Unmarshal(e.recv.requests()[0].body, &p)
	if o, _ := p["organization"].(map[string]any); o["name"] != "acme" || o["id"] != acmeID {
		t.Fatalf("organization nel payload: %v", p["organization"])
	}
	e.clock.Advance(73 * time.Hour)
	e.run()
	var owners []string
	rows, err := e.pool.Query(context.Background(), `SELECT user_id::text FROM core.notifications WHERE reason = 'webhook' AND webhook_id = $1 AND repo_id IS NULL ORDER BY 1`, hook)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var u string
		_ = rows.Scan(&u)
		owners = append(owners, u)
	}
	rows.Close()
	if !slices.Equal(owners, []string{aliceID, bobID}) {
		t.Fatalf("notificati = %v, voluti gli owner", owners)
	}
}

// Criterio: Redeliver rimanda lo stesso payload con una consegna nuova, firma
// ricalcolata col segreto corrente, anche su un webhook non attivo.
func TestWebhooks_Redeliver(t *testing.T) {
	e := newHookEnv(t)
	e.repo("app", false)
	hook := e.repoHook("app", fmt.Sprintf(`{"url":%q,"events":["issues"],"secret":"uno"}`, hookURL))
	e.open("app", "alice", "Una")
	e.run()
	first := e.deliveries("app", hook)[0]

	// Il segreto cambia, il webhook va in pausa: Redeliver firma col segreto nuovo.
	e.want(e.do(http.MethodPatch, "/repos/alice/app/hooks/"+hook, "alice", `{"secret":"due","active":false}`), http.StatusOK, "")
	rec := e.do(http.MethodPost, "/repos/alice/app/hooks/"+hook+"/deliveries/"+first.Id.String()+"/redeliver", "alice", "")
	e.want(rec, http.StatusAccepted, "")
	var queued openapi.WebhookDelivery
	_ = json.Unmarshal(rec.Body.Bytes(), &queued)
	if queued.Id == first.Id || queued.Status != "pending" || queued.RedeliveryOf == nil || *queued.RedeliveryOf != first.Id {
		t.Fatalf("consegna in coda: %+v", queued)
	}
	if e.run() != 1 {
		t.Fatal("la consegna in coda non è partita")
	}
	reqs := e.recv.requests()
	if len(reqs) != 2 {
		t.Fatalf("richieste = %d", len(reqs))
	}
	if string(reqs[0].body) != string(reqs[1].body) {
		t.Fatalf("il payload deve restare lo stesso:\n%s\n%s", reqs[0].body, reqs[1].body)
	}
	verifySignature(t, "uno", reqs[0])
	verifySignature(t, "due", reqs[1])
	if reqs[1].header.Get("X-GitStack-Delivery") != queued.Id.String() || reqs[1].header.Get("X-GitStack-Delivery") == reqs[0].header.Get("X-GitStack-Delivery") {
		t.Fatalf("X-GitStack-Delivery della Redeliver: %q", reqs[1].header.Get("X-GitStack-Delivery"))
	}
	ds := e.deliveries("app", hook)
	if got, ok := find(ds, queued.Id); len(ds) != 2 || !ok || got.Status != "success" {
		t.Fatalf("log: %+v", ds)
	}
	// Una consegna di un altro webhook o inesistente: 404.
	other := e.repoHook("app", fmt.Sprintf(`{"url":%q,"events":["issues"]}`, hookURL))
	e.want(e.do(http.MethodPost, "/repos/alice/app/hooks/"+other+"/deliveries/"+first.Id.String()+"/redeliver", "alice", ""), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodPost, "/repos/alice/app/hooks/"+hook+"/deliveries/"+uuid.NewString()+"/redeliver", "alice", ""), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodGet, "/repos/alice/app/hooks/"+other+"/deliveries/"+first.Id.String(), "alice", ""), http.StatusNotFound, "not_found")
}

// Criterio: le consegne in sospeso sopravvivono al riavvio di core. La coda è
// la tabella: un motore nuovo (processo riavviato) le riprende.
func TestWebhooks_LeConsegneInSospesoSopravvivonoAlRiavvio(t *testing.T) {
	e := newHookEnv(t)
	e.repo("app", false)
	hook := e.repoHook("app", fmt.Sprintf(`{"url":%q,"events":["issues"]}`, hookURL))

	// 1) Evento confermato nell'outbox ma core si ferma prima di elaborarlo.
	e.open("app", "alice", "Una")
	restarted := e.newEngine()
	e.runWith(restarted)
	if got := len(e.recv.requests()); got != 1 {
		t.Fatalf("dopo il riavvio la consegna dell'evento doveva partire: richieste = %d", got)
	}

	// 2) Consegna creata ma non ancora inviata: core si ferma tra le due fasi.
	e.open("app", "alice", "Due")
	ctx := context.Background()
	if n, err := e.engine.ProcessOutbox(ctx); err != nil || n == 0 {
		t.Fatalf("ProcessOutbox: %d %v", n, err)
	}
	var pending int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM core.webhook_deliveries WHERE status = 'pending'`).Scan(&pending)
	if pending != 1 {
		t.Fatalf("consegne pending prima del riavvio = %d", pending)
	}
	e.runWith(e.newEngine())
	if got := len(e.recv.requests()); got != 2 {
		t.Fatalf("richieste dopo il secondo riavvio = %d", got)
	}

	// 3) Consegna in attesa del prossimo tentativo (5xx): il motore nuovo la
	// riprende alla scadenza, e il contatore dei tentativi non riparte.
	e.recv.status(http.StatusInternalServerError)
	e.open("app", "alice", "Tre")
	e.run()
	e.recv.status(http.StatusOK)
	e.runWith(e.newEngine()) // non ancora scaduta
	if got := len(e.recv.requests()); got != 3 {
		t.Fatalf("richieste prima della scadenza = %d", got)
	}
	e.clock.Advance(time.Minute)
	e.runWith(e.newEngine())
	ds := e.deliveries("app", hook)
	retried := 0
	for _, d := range ds {
		if d.Status == "success" && d.Attempt == 2 {
			retried++
		}
	}
	if len(ds) != 3 || retried != 1 {
		t.Fatalf("log dopo il riavvio: %+v", ds)
	}

	// 4) Un motore morto a metà tentativo: la consegna era «presa in carico»
	// (affitto), alla scadenza la riprende un altro.
	e.open("app", "alice", "Quattro")
	if n, err := e.engine.ProcessOutbox(ctx); err != nil || n == 0 {
		t.Fatalf("ProcessOutbox: %d %v", n, err)
	}
	e.clock.Advance(time.Hour)
	if _, err := e.pool.Exec(ctx, `UPDATE core.webhook_deliveries SET next_attempt_at = $1 WHERE status = 'pending'`, e.clock.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := e.run(); got != 0 {
		t.Fatalf("consegna affittata ripresa troppo presto: %d", got)
	}
	e.clock.Advance(3 * time.Minute)
	if got := e.run(); got != 1 {
		t.Fatalf("consegna affittata non ripresa alla scadenza: %d", got)
	}
}

// Un evento di dominio non crea due consegne per lo stesso webhook, nemmeno se
// il motore lo rielabora.
func TestWebhooks_EventoIdempotente(t *testing.T) {
	e := newHookEnv(t)
	e.repo("app", false)
	hook := e.repoHook("app", fmt.Sprintf(`{"url":%q,"events":["issues"]}`, hookURL))
	e.open("app", "alice", "Una")
	e.run()
	if _, err := e.pool.Exec(context.Background(), `UPDATE core.event_outbox SET webhooked_at = NULL`); err != nil {
		t.Fatal(err)
	}
	e.run()
	if got := len(e.deliveries("app", hook)); got != 1 {
		t.Fatalf("consegne = %d, voluta 1", got)
	}
}

// git.push: un messaggio per ref, idempotente alla riconsegna di NATS.
func TestWebhooks_PushUnMessaggioPerRef(t *testing.T) {
	e := newHookEnv(t)
	repoID := e.repo("app", false)
	hook := e.repoHook("app", fmt.Sprintf(`{"url":%q,"events":["push"],"secret":"p"}`, hookURL))
	other := e.repoHook("app", fmt.Sprintf(`{"url":%q,"events":["issues"]}`, hookURL))
	sha1, sha2 := strings.Repeat("1", 40), strings.Repeat("2", 40)
	env := pkgevents.Envelope{Name: gitpush.Name, Version: 1, ID: uuid.NewString(), Time: e.clock.Now()}
	p := gitpush.Payload{
		Repo:   gitpush.Repo{ID: repoID.String(), FullName: "alice/app", DefaultBranch: "main"},
		Pusher: gitpush.Pusher{ID: aliceID, Username: "alice", Type: "human"},
		Refs: []gitpush.RefPush{
			{Ref: "refs/heads/main", Before: sha1, After: sha2, IsDefaultBranch: true, Commits: []gitpush.Commit{{SHA: sha2, Message: "Corregge\n\nfixes #1\n",
				Author: gitpush.Person{Name: "Ada", Email: "ada@example.com", Date: "2026-10-05T12:20:00Z"}}}},
			{Ref: "refs/tags/v1", Before: gitpush.ZeroSHA, After: sha2, Commits: []gitpush.Commit{}},
			{Ref: "refs/heads/old", Before: sha1, After: gitpush.ZeroSHA, Commits: []gitpush.Commit{}},
		},
	}
	for i := 0; i < 2; i++ { // la seconda è la riconsegna del messaggio
		if err := e.engine.EnqueuePush(context.Background(), env, p); err != nil {
			t.Fatal(err)
		}
	}
	e.run()
	reqs := e.recv.requests()
	if len(reqs) != 3 {
		t.Fatalf("richieste = %d, volute 3 (una per ref)", len(reqs))
	}
	if got := len(e.deliveries("app", other)); got != 0 {
		t.Fatalf("il webhook issues ha ricevuto %d push", got)
	}
	byRef := map[string]map[string]any{}
	for _, r := range reqs {
		verifySignature(t, "p", r)
		if r.header.Get("X-GitStack-Event") != "push" {
			t.Fatalf("evento %q", r.header.Get("X-GitStack-Event"))
		}
		var m map[string]any
		_ = json.Unmarshal(r.body, &m)
		byRef[m["ref"].(string)] = m
	}
	main := byRef["refs/heads/main"]
	if main["event"] != "push" || main["action"] != nil || main["before"] != sha1 || main["after"] != sha2 || main["created"] != false ||
		main["deleted"] != false || main["isDefaultBranch"] != true || main["commitsTruncated"] != false {
		t.Fatalf("push main: %v", main)
	}
	if cs, _ := main["commits"].([]any); len(cs) != 1 || cs[0].(map[string]any)["message"] != "Corregge\n\nfixes #1\n" {
		t.Fatalf("commit: %v", main["commits"])
	}
	if s, _ := main["sender"].(map[string]any); s["username"] != "alice" || main["repository"].(map[string]any)["defaultBranch"] != "main" {
		t.Fatalf("sender/repository: %v", main)
	}
	if byRef["refs/tags/v1"]["created"] != true || byRef["refs/heads/old"]["deleted"] != true {
		t.Fatalf("created/deleted: %v %v", byRef["refs/tags/v1"], byRef["refs/heads/old"])
	}
	if got := len(e.deliveries("app", hook)); got != 3 {
		t.Fatalf("consegne = %d", got)
	}
}

// Un webhook di organizzazione riceve gli eventi di tutti i repo
// dell'organizzazione e non quelli dei repo di altri.
func TestWebhooks_OrganizzazioneVedeITuttiIRepo(t *testing.T) {
	e := newHookEnv(t)
	e.want(e.do(http.MethodPost, "/repos", "alice", `{"owner":"acme","name":"uno","visibility":"private"}`), http.StatusCreated, "")
	e.want(e.do(http.MethodPost, "/repos", "alice", `{"owner":"acme","name":"due","visibility":"private"}`), http.StatusCreated, "")
	e.repo("personale", false)
	rec := e.do(http.MethodPost, "/orgs/acme/hooks", "alice", fmt.Sprintf(`{"url":%q,"events":["issues","repository"]}`, hookURL))
	e.want(rec, http.StatusCreated, "")
	e.want(e.do(http.MethodPost, "/repos/acme/uno/issues", "alice", `{"title":"A","body":""}`), http.StatusCreated, "")
	e.want(e.do(http.MethodPost, "/repos/acme/due/issues", "alice", `{"title":"B","body":""}`), http.StatusCreated, "")
	e.open("personale", "alice", "C")
	e.run()
	var titles []string
	for _, r := range e.recv.requests() {
		var m map[string]any
		_ = json.Unmarshal(r.body, &m)
		if m["event"] == "issues" {
			titles = append(titles, m["issue"].(map[string]any)["title"].(string))
		}
	}
	slices.Sort(titles)
	if !slices.Equal(titles, []string{"A", "B"}) {
		t.Fatalf("issue consegnate all'organizzazione: %v", titles)
	}
	// Evento repository: archiviazione di un repo dell'organizzazione.
	e.want(e.do(http.MethodPatch, "/repos/acme/uno", "alice", `{"archived":true}`), http.StatusOK, "")
	e.run()
	var last map[string]any
	reqs := e.recv.requests()
	_ = json.Unmarshal(reqs[len(reqs)-1].body, &last)
	if last["event"] != "repository" || last["action"] != "archived" || last["repository"].(map[string]any)["archived"] != true {
		t.Fatalf("evento repository: %s", reqs[len(reqs)-1].body)
	}
}

// C8: l'uscita passa solo dal client protetto: una destinazione bloccata dà un
// fallimento nel log, senza ritentare.
func TestWebhooks_DestinazioneBloccataNonSiRitenta(t *testing.T) {
	e := newHookEnv(t)
	e.repo("app", false)
	hook := e.repoHook("app", fmt.Sprintf(`{"url":%q,"events":["issues"]}`, hookURL))
	// L'indirizzo passa dalla creazione (un nome), ma risolve su loopback:
	// lo blocca il dialer del client vero.
	real, _, err := webhooks.NewEgress(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE core.webhooks SET url = $1`, e.recv.srv.URL+"/in"); err != nil {
		t.Fatal(err)
	}
	e.engine.HTTP = real
	e.open("app", "alice", "Una")
	e.run()
	e.clock.Advance(48 * time.Hour)
	e.run()
	ds := e.deliveries("app", hook)
	if len(ds) != 1 || ds[0].Status != "failed" || ds[0].Attempt != 1 || ds[0].Error == nil || ds[0].StatusCode != nil {
		t.Fatalf("consegna verso loopback: %+v", ds)
	}
	if len(e.recv.requests()) != 0 {
		t.Fatal("la richiesta non doveva arrivare al server")
	}
}

// C8: la risposta si tronca a 4 KB, senza rompere il database con byte non UTF-8.
func TestWebhooks_RispostaTroncataA4KB(t *testing.T) {
	e := newHookEnv(t)
	e.repo("app", false)
	hook := e.repoHook("app", fmt.Sprintf(`{"url":%q,"events":["issues"]}`, hookURL))
	e.recv.mu.Lock()
	e.recv.answer = func(_ int, w http.ResponseWriter) {
		w.Header().Set("X-Prova", "sì")
		_, _ = w.Write([]byte(strings.Repeat("è", 3000) + "\xff\x00" + strings.Repeat("x", 9000)))
	}
	e.recv.mu.Unlock()
	e.open("app", "alice", "Una")
	e.run()
	d := e.deliveries("app", hook)[0]
	if d.Status != "success" {
		t.Fatalf("consegna: %+v", d)
	}
	rec := e.do(http.MethodGet, "/repos/alice/app/hooks/"+hook+"/deliveries/"+d.Id.String(), "alice", "")
	e.want(rec, http.StatusOK, "")
	var det openapi.WebhookDeliveryDetail
	_ = json.Unmarshal(rec.Body.Bytes(), &det)
	if det.Response == nil || !det.Response.Truncated || len(det.Response.Body) > 4096 || len(det.Response.Body) < 4000 || det.Response.Headers["X-Prova"] == "" {
		t.Fatalf("risposta nel log: truncated=%v len=%d", det.Response != nil && det.Response.Truncated, len(det.Response.Body))
	}
}

// Conservazione: il log si tiene 30 giorni.
func TestWebhooks_LogConservatoTrentaGiorni(t *testing.T) {
	e := newHookEnv(t)
	e.repo("app", false)
	hook := e.repoHook("app", fmt.Sprintf(`{"url":%q,"events":["issues"]}`, hookURL))
	e.open("app", "alice", "Una")
	e.run()
	e.clock.Advance(29 * 24 * time.Hour)
	e.open("app", "alice", "Due")
	e.run()
	if n, err := e.engine.PurgeLog(context.Background(), e.clock.Now()); err != nil || n != 0 {
		t.Fatalf("dopo 29 giorni: eliminate %d (%v)", n, err)
	}
	e.clock.Advance(2 * 24 * time.Hour)
	n, err := e.engine.PurgeLog(context.Background(), e.clock.Now())
	if err != nil || n != 1 {
		t.Fatalf("dopo 31 giorni: eliminate %d (%v), volta 1", n, err)
	}
	if got := len(e.deliveries("app", hook)); got != 1 {
		t.Fatalf("consegne rimaste = %d", got)
	}
	if n, _ := e.engine.PurgeLog(context.Background(), e.clock.Now()); n != 0 {
		t.Fatalf("la pulizia non è idempotente: %d", n)
	}
}

// Criterio: solo admin del repo o owner dell'organizzazione gestiscono e
// vedono il log.
func TestWebhooks_Permessi(t *testing.T) {
	e := newHookEnv(t)
	e.repo("app", true) // internal: carol e bob la leggono
	hook := e.repoHook("app", fmt.Sprintf(`{"url":%q,"events":["issues"]}`, hookURL))
	e.open("app", "alice", "Una")
	e.run()
	d := e.deliveries("app", hook)[0].Id.String()
	e.hid.grantWrite(uuid.MustParse(e.repoIDOf("app")), "bob")

	base := "/repos/alice/app/hooks"
	calls := []struct{ method, path, body string }{
		{http.MethodGet, base, ""},
		{http.MethodPost, base, fmt.Sprintf(`{"url":%q,"events":["push"]}`, hookURL)},
		{http.MethodGet, base + "/" + hook, ""},
		{http.MethodPatch, base + "/" + hook, `{"active":false}`},
		{http.MethodDelete, base + "/" + hook, ""},
		{http.MethodPost, base + "/" + hook + "/reactivate", ""},
		{http.MethodGet, base + "/" + hook + "/deliveries", ""},
		{http.MethodGet, base + "/" + hook + "/deliveries/" + d, ""},
		{http.MethodPost, base + "/" + hook + "/deliveries/" + d + "/redeliver", ""},
	}
	for _, user := range []string{"bob", "carol"} { // bob ha write, carol read: né l'uno né l'altra sono admin
		for _, c := range calls {
			rec := e.do(c.method, c.path, user, c.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s %s: %d %s, voluto 403", user, c.method, c.path, rec.Code, rec.Body.String())
			}
		}
	}
	// Un repo privato che non si legge è 404, non 403.
	e.repo("segreto", false)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/repos/alice/segreto/hooks"},
		{http.MethodPost, "/repos/alice/segreto/hooks"},
		{http.MethodGet, "/repos/alice/segreto/hooks/" + uuid.NewString() + "/deliveries"},
	} {
		if rec := e.do(c.method, c.path, "carol", `{"url":"https://x.test/a","events":["push"]}`); rec.Code != http.StatusNotFound {
			t.Fatalf("carol %s %s: %d, voluto 404", c.method, c.path, rec.Code)
		}
	}
	// Admin: tutto permesso; dopo i rifiuti il webhook è intatto.
	if w := e.hook("app", hook); !w.Active {
		t.Fatal("un rifiuto non deve aver modificato il webhook")
	}
	e.want(e.do(http.MethodGet, base, "alice", ""), http.StatusOK, "")
	// Un webhook di un altro repo non si raggiunge dall'indirizzo di questo.
	e.repo("altro", false)
	e.want(e.do(http.MethodGet, "/repos/alice/altro/hooks/"+hook, "alice", ""), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodDelete, "/repos/alice/altro/hooks/"+hook, "alice", ""), http.StatusNotFound, "not_found")

	// Organizzazione: owner sì; un membro non owner 403; sconosciuta 404; admin di sistema sì.
	org := "/orgs/acme/hooks"
	e.want(e.do(http.MethodGet, org, "alice", ""), http.StatusOK, "")
	rec := e.do(http.MethodPost, org, "alice", fmt.Sprintf(`{"url":%q,"events":["push"]}`, hookURL))
	e.want(rec, http.StatusCreated, "")
	oh := e.hookID(rec)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, org}, {http.MethodPost, org}, {http.MethodGet, org + "/" + oh}, {http.MethodPatch, org + "/" + oh},
		{http.MethodDelete, org + "/" + oh}, {http.MethodPost, org + "/" + oh + "/reactivate"},
		{http.MethodGet, org + "/" + oh + "/deliveries"}, {http.MethodGet, org + "/" + oh + "/deliveries/" + uuid.NewString()},
		{http.MethodPost, org + "/" + oh + "/deliveries/" + uuid.NewString() + "/redeliver"},
	} {
		if rec := e.do(c.method, c.path, "bob", `{"url":"https://x.test/a","events":["push"]}`); rec.Code != http.StatusForbidden {
			t.Fatalf("bob %s %s: %d, voluto 403", c.method, c.path, rec.Code)
		}
	}
	e.want(e.do(http.MethodGet, "/orgs/nessuna/hooks", "alice", ""), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodGet, "/orgs/alice/hooks", "alice", ""), http.StatusNotFound, "not_found") // alice è un utente, non un'organizzazione
	e.hid.sysAdmin[carolID] = true
	e.want(e.do(http.MethodGet, org+"/"+oh, "carol", ""), http.StatusOK, "")
	// Senza identità: 401.
	req := httptest.NewRequest(http.MethodGet, org, nil)
	rec2 := httptest.NewRecorder()
	e.router.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("senza identità: %d", rec2.Code)
	}
}

func (e *hookEnv) repoIDOf(name string) string {
	e.t.Helper()
	var id string
	if err := e.pool.QueryRow(context.Background(), `SELECT resource_id::text FROM core.repositories WHERE name = $1`, name).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// Configurazione: validazione, segreto mai restituito, rimozione, 503 senza chiave,
// archiviato (R10), eliminazione.
func TestWebhooks_ConfigurazioneEValidazione(t *testing.T) {
	e := newHookEnv(t)
	e.repo("app", false)
	post := func(body string) *httptest.ResponseRecorder {
		return e.do(http.MethodPost, "/repos/alice/app/hooks", "alice", body)
	}
	// Indirizzi non ammessi (C8): loopback, link-local, cluster, schema, credenziali.
	for _, bad := range []string{"http://127.0.0.1/x", "http://[::1]:9/x", "http://169.254.169.254/latest/meta-data", "http://10.43.0.10:8080/", "ftp://x.test/a", "http://localhost/a", "http://u:p@x.test/"} {
		rec := post(fmt.Sprintf(`{"url":%q,"events":["push"]}`, bad))
		e.want(rec, http.StatusUnprocessableEntity, "url_not_allowed")
		if !strings.Contains(rec.Body.String(), `"url"`) {
			t.Fatalf("details.fields.url mancante: %s", rec.Body.String())
		}
	}
	e.want(post(`{"url":"http://192.168.1.5:9000/hook","events":["push"]}`), http.StatusCreated, "") // rete aziendale ammessa
	for _, bad := range []string{
		`{"url":"https://x.test/a","events":[]}`,
		`{"url":"https://x.test/a","events":["push","push"]}`,
		`{"url":"https://x.test/a","events":["ping"]}`,
		`{"url":"","events":["push"]}`,
		fmt.Sprintf(`{"url":"https://x.test/%s","events":["push"]}`, strings.Repeat("a", 2100)),
		fmt.Sprintf(`{"url":"https://x.test/a","events":["push"],"secret":%q}`, strings.Repeat("s", 257)),
	} {
		e.want(post(bad), http.StatusUnprocessableEntity, "validation_failed")
	}
	e.want(post(`{"url":"https://x.test/a","events":["push"],"altro":1}`), http.StatusBadRequest, "bad_request")

	// Segreto: impostato, sostituito, tolto; mai nelle risposte.
	rec := post(`{"url":"https://x.test/a","events":["push"],"secret":"abc"}`)
	e.want(rec, http.StatusCreated, "")
	hook := e.hookID(rec)
	if strings.Contains(rec.Body.String(), "abc\"") && strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("segreto nella risposta: %s", rec.Body.String())
	}
	patch := func(body string) openapi.Webhook {
		r := e.do(http.MethodPatch, "/repos/alice/app/hooks/"+hook, "alice", body)
		e.want(r, http.StatusOK, "")
		var w openapi.Webhook
		_ = json.Unmarshal(r.Body.Bytes(), &w)
		return w
	}
	if w := patch(`{"url":"https://y.test/b","events":["issues","push"]}`); !w.HasSecret || w.Url != "https://y.test/b" || len(w.Events) != 2 {
		t.Fatalf("modifica: %+v", w)
	}
	if w := patch(`{"secret":"nuovo"}`); !w.HasSecret {
		t.Fatalf("sostituzione: %+v", w)
	}
	if w := patch(`{"secret":""}`); w.HasSecret {
		t.Fatalf("rimozione: %+v", w)
	}
	var cols int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.webhooks WHERE id = $1 AND secret_ciphertext IS NULL AND secret_nonce IS NULL AND secret_key_id IS NULL`, hook).Scan(&cols)
	if cols != 1 {
		t.Fatal("il segreto tolto deve azzerare le tre colonne")
	}
	e.want(e.do(http.MethodPatch, "/repos/alice/app/hooks/"+hook, "alice", `{"url":"http://127.0.0.1/x"}`), http.StatusUnprocessableEntity, "url_not_allowed")
	e.want(e.do(http.MethodGet, "/repos/alice/app/hooks/"+uuid.NewString(), "alice", ""), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodGet, "/repos/alice/app/hooks?page=0", "alice", ""), http.StatusBadRequest, "invalid_page")
	e.want(e.do(http.MethodGet, "/repos/alice/app/hooks/"+hook+"/deliveries?status=boh", "alice", ""), http.StatusBadRequest, "")

	// Elenco paginato.
	rec = e.do(http.MethodGet, "/repos/alice/app/hooks?perPage=1&page=2", "alice", "")
	e.want(rec, http.StatusOK, "")
	var list openapi.WebhookList
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if list.Total != 2 || list.Page != 2 || len(list.Items) != 1 {
		t.Fatalf("elenco: %+v", list)
	}

	// Repo archiviato: sola lettura (R10).
	e.want(e.do(http.MethodPatch, "/repos/alice/app", "alice", `{"archived":true}`), http.StatusOK, "")
	e.want(post(`{"url":"https://x.test/a","events":["push"]}`), http.StatusConflict, "archived")
	e.want(e.do(http.MethodPatch, "/repos/alice/app/hooks/"+hook, "alice", `{"active":false}`), http.StatusConflict, "archived")
	e.want(e.do(http.MethodDelete, "/repos/alice/app/hooks/"+hook, "alice", ""), http.StatusConflict, "archived")
	e.want(e.do(http.MethodGet, "/repos/alice/app/hooks/"+hook, "alice", ""), http.StatusOK, "")
	e.want(e.do(http.MethodPatch, "/repos/alice/app", "alice", `{"archived":false}`), http.StatusOK, "")

	// Eliminazione: sparisce con le sue consegne.
	e.want(e.do(http.MethodDelete, "/repos/alice/app/hooks/"+hook, "alice", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodGet, "/repos/alice/app/hooks/"+hook, "alice", ""), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodDelete, "/repos/alice/app/hooks/"+hook, "alice", ""), http.StatusNotFound, "not_found")
}

// Senza GITSTACK_WEBHOOK_SECRET_KEY un segreto risponde 503; senza segreto i
// webhook funzionano.
func TestWebhooks_SenzaChiaveDeiSegreti(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	base := &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}
	hid := &hookIdentity{issuesIdentity: base}
	e := &issuesEnv{t: t, pool: pool, id: base}
	e.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(hid), httpserver.WithReadableLister(hid), httpserver.WithGit(newFakeGit()),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}))
	e.repo("app", false)
	e.want(e.do(http.MethodPost, "/repos/alice/app/hooks", "alice", `{"url":"https://x.test/a","events":["push"],"secret":"abc"}`), http.StatusServiceUnavailable, "webhook_secrets_unavailable")
	e.want(e.do(http.MethodPost, "/repos/alice/app/hooks", "alice", `{"url":"https://x.test/a","events":["push"]}`), http.StatusCreated, "")
	var n int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM core.webhooks`).Scan(&n)
	if n != 1 {
		t.Fatalf("webhook scritti = %d: un segreto rifiutato non deve lasciare righe", n)
	}
}

var _ = errors.New
