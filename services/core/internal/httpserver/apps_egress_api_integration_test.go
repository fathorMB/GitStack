//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// GIT-385 (H16): l'amministratore apre destinazioni private precise, l'app le restringe, e l'API
// rifiuta cluster, nodo, loopback, link-local e porte di Kubernetes.

type egressSpy struct {
	commitNoopController
	mu    sync.Mutex
	calls [][]uuid.UUID
}

func (s *egressSpy) RefreshEgress(_ context.Context, ids []uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, ids)
	return nil
}

func (s *egressSpy) last() ([]uuid.UUID, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return nil, 0
	}
	return s.calls[len(s.calls)-1], len(s.calls)
}

func newEgressAPIEnv(t *testing.T) (*appsEnv, *egressSpy) {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	id := &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}
	id.sysAdmin[users["carol"]] = true
	e := &issuesEnv{t: t, pool: pool, id: id}
	clock := &appClock{t: time.Now().UTC().Truncate(time.Second)}
	spy := &egressSpy{}
	hosts := map[string][]netip.Addr{
		"db.azienda.lan":  {netip.MustParseAddr("10.20.0.5")},
		"smtp.acme.lan":   {netip.MustParseAddr("192.168.1.25")},
		"pub.example.com": {netip.MustParseAddr("93.184.216.34")},
		"kube.lan":        {netip.MustParseAddr("10.43.0.1")},
		"mixed.lan":       {netip.MustParseAddr("10.20.0.6"), netip.MustParseAddr("127.0.0.1")},
	}
	resolver := func(_ context.Context, host string) ([]netip.Addr, error) {
		if ips, ok := hosts[host]; ok {
			return ips, nil
		}
		return nil, errors.New("no such host")
	}
	e.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(id), httpserver.WithReadableLister(id), httpserver.WithGit(newFakeGit()),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}),
		httpserver.WithAppsDomain("apps.example.com"), httpserver.WithClock(clock.Now),
		httpserver.WithAppController(spy),
		httpserver.WithAppEgress(httpserver.AppEgressConfig{NodeAddresses: []string{"192.168.5.5"}, Deny: []string{"10.99.0.0/16"}, Resolver: resolver}))
	ae := &appsEnv{issuesEnv: e, clock: clock, git: newFakeGit()}
	id.grantWrite(e.repo("handbook", true), "bob")
	return ae, spy
}

func decodeEgress[T any](t *testing.T, rec interface{ Bytes() []byte }) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Bytes(), &v); err != nil {
		t.Fatalf("risposta non valida: %v: %s", err, rec.Bytes())
	}
	return v
}

func TestAppEgress_DestinazioniPrivateDellAmministratoreERestrizionePerApp(t *testing.T) {
	e, spy := newEgressAPIEnv(t)
	put := func(user, body string) int { return e.do(http.MethodPut, "/admin/apps/egress", user, body).Code }

	// Solo l'amministratore dell'installazione.
	e.want(e.do(http.MethodGet, "/admin/apps/egress", "alice", ""), http.StatusForbidden, "")
	e.want(e.do(http.MethodPut, "/admin/apps/egress", "alice", `{"privateDestinations":[{"host":"db.azienda.lan","port":5432}]}`), http.StatusForbidden, "")
	rec := e.do(http.MethodGet, "/admin/apps/egress", "carol", "")
	e.want(rec, http.StatusOK, "")
	if l := decodeEgress[openapi.AppsEgressSettings](t, rec.Body); len(l.PrivateDestinations) != 0 {
		t.Fatalf("all'inizio non c'è nulla di aperto: %s", rec.Body.String())
	}

	// Cluster, nodo, loopback, link-local, porte di Kubernetes, nomi che portano lì o non si risolvono: mai.
	for body, code := range map[string]string{
		`{"privateDestinations":[{"host":"10.43.0.1","port":443}]}`:           "destination_blocked", // servizio del cluster
		`{"privateDestinations":[{"host":"10.42.3.4","port":8080}]}`:          "destination_blocked", // pod
		`{"privateDestinations":[{"host":"kube.lan","port":443}]}`:            "destination_blocked", // nome che porta nel cluster
		`{"privateDestinations":[{"host":"192.168.5.5","port":22}]}`:          "destination_blocked", // nodo
		`{"privateDestinations":[{"host":"127.0.0.1","port":5432}]}`:          "destination_blocked",
		`{"privateDestinations":[{"host":"169.254.169.254","port":80}]}`:      "destination_blocked",
		`{"privateDestinations":[{"host":"db.azienda.lan","port":6443}]}`:     "destination_blocked", // API di Kubernetes
		`{"privateDestinations":[{"host":"db.azienda.lan","port":10250}]}`:    "destination_blocked", // kubelet
		`{"privateDestinations":[{"host":"db.azienda.lan","port":2379}]}`:     "destination_blocked", // etcd
		`{"privateDestinations":[{"host":"mixed.lan","port":5432}]}`:          "destination_blocked", // un indirizzo su due è loopback
		`{"privateDestinations":[{"host":"::1","port":5432}]}`:                "destination_blocked",
		`{"privateDestinations":[{"host":"10.99.0.4","port":5432}]}`:          "destination_blocked", // lista vietata
		`{"privateDestinations":[{"host":"non-esiste.lan","port":5432}]}`:     "destination_unresolved",
		`{"privateDestinations":[{"host":"http://db.azienda.lan","port":1}]}`: "validation_failed",
		`{"privateDestinations":[{"host":"10.0.0.0/8","port":5432}]}`:         "validation_failed",
		`{"privateDestinations":[{"host":"*.azienda.lan","port":5432}]}`:      "validation_failed",
		`{"privateDestinations":[{"host":"db.azienda.lan","port":70000}]}`:    "validation_failed",
	} {
		rec := e.do(http.MethodPut, "/admin/apps/egress", "carol", body)
		e.want(rec, http.StatusUnprocessableEntity, code)
	}
	// Una sola voce vietata fa rifiutare tutto l'elenco: nulla è stato scritto.
	if c := put("carol", `{"privateDestinations":[{"host":"db.azienda.lan","port":5432},{"host":"10.43.0.1","port":80}]}`); c != http.StatusUnprocessableEntity {
		t.Fatalf("elenco con una voce vietata: %d", c)
	}
	rec = e.do(http.MethodGet, "/admin/apps/egress", "carol", "")
	if l := decodeEgress[openapi.AppsEgressSettings](t, rec.Body); len(l.PrivateDestinations) != 0 {
		t.Fatalf("nulla deve essere stato aperto: %s", rec.Body.String())
	}
	if _, n := spy.last(); n != 0 {
		t.Fatalf("un rifiuto non riapplica la policy: %d", n)
	}

	// Apre due destinazioni precise (un doppione conta una volta).
	rec = e.do(http.MethodPut, "/admin/apps/egress", "carol", `{"privateDestinations":[{"host":"DB.azienda.lan","port":5432,"note":"database del backoffice"},{"host":"db.azienda.lan","port":5432},{"host":"192.168.1.25","port":587}]}`)
	e.want(rec, http.StatusOK, "")
	l := decodeEgress[openapi.AppsEgressSettings](t, rec.Body)
	// Ordinate per data e poi per host: 192.168.1.25 prima di db.azienda.lan.
	if len(l.PrivateDestinations) != 2 || l.PrivateDestinations[0].Host != "192.168.1.25" || l.PrivateDestinations[1].Port != 5432 ||
		l.PrivateDestinations[1].Host != "db.azienda.lan" || l.PrivateDestinations[1].Note != "database del backoffice" || l.PrivateDestinations[1].CreatedBy == nil || l.PrivateDestinations[1].CreatedBy.Username != "carol" {
		t.Fatalf("destinazioni aperte: %s", rec.Body.String())
	}
	if ids, n := spy.last(); n != 1 || ids != nil {
		t.Fatalf("la modifica riapplica la policy di tutte le app: %v %d", ids, n)
	}

	// L'app: crea, legge e restringe. read non basta per leggere, write non basta per scrivere.
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", `{"name":"web","image":"ghcr.io/alice/web:v1","port":8080}`), http.StatusAccepted, "")
	base := "/repos/alice/handbook/apps/web/egress"
	rec = e.do(http.MethodGet, base, "alice", "")
	e.want(rec, http.StatusOK, "")
	got := decodeEgress[openapi.AppEgress](t, rec.Body)
	if got.Restricted || len(got.Destinations) != 0 || len(got.AllowedPrivate) != 2 {
		t.Fatalf("senza restrizione l'app esce come l'istanza: %s", rec.Body.String())
	}
	e.want(e.do(http.MethodGet, base, "bob", ""), http.StatusOK, "") // write legge
	e.want(e.do(http.MethodPut, base, "bob", `{"destinations":[]}`), http.StatusForbidden, "")
	e.want(e.do(http.MethodDelete, base, "bob", ""), http.StatusForbidden, "")

	// Fuori dalla lista dell'amministratore: rifiutato dall'API. Altra porta, altro indirizzo privato,
	// cluster: sì al pubblico.
	for _, body := range []string{
		`{"destinations":[{"host":"10.20.0.99","port":5432}]}`,     // privato non aperto
		`{"destinations":[{"host":"db.azienda.lan","port":5433}]}`, // porta non aperta
		`{"destinations":[{"host":"10.43.0.1","port":443}]}`,       // cluster
		`{"destinations":[{"host":"127.0.0.1","port":80}]}`,
		`{"destinations":[{"host":"db.azienda.lan","port":6443}]}`,
		`{"destinations":[{"host":"pub.example.com","port":443},{"host":"smtp.acme.lan","port":25}]}`, // una voce su due fuori
	} {
		e.want(e.do(http.MethodPut, base, "alice", body), http.StatusUnprocessableEntity, "destination_blocked")
	}
	e.want(e.do(http.MethodPut, base, "alice", `{"destinations":[{"host":"nope.lan","port":80}]}`), http.StatusUnprocessableEntity, "destination_unresolved")
	if got := decodeEgress[openapi.AppEgress](t, e.do(http.MethodGet, base, "alice", "").Body); got.Restricted {
		t.Fatalf("un rifiuto non scrive la restrizione: %+v", got)
	}

	rec = e.do(http.MethodPut, base, "alice", `{"destinations":[{"host":"db.azienda.lan","port":5432},{"host":"pub.example.com","port":443},{"host":"192.168.1.25","port":587},{"host":"pub.example.com","port":443}]}`)
	e.want(rec, http.StatusOK, "")
	got = decodeEgress[openapi.AppEgress](t, rec.Body)
	if !got.Restricted || len(got.Destinations) != 3 || got.UpdatedBy == nil || got.UpdatedBy.Username != "alice" {
		t.Fatalf("restrizione: %s", rec.Body.String())
	}
	var appID uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM core.apps WHERE name = 'web'`).Scan(&appID); err != nil {
		t.Fatal(err)
	}
	if ids, _ := spy.last(); len(ids) != 1 || ids[0] != appID {
		t.Fatalf("la restrizione riapplica la policy dell'app: %v", ids)
	}
	if r, err := store.New(e.pool).GetAppEgressRestriction(context.Background(), appID); err != nil || r == nil || len(r.Destinations) != 3 {
		t.Fatalf("restrizione salvata: %+v %v", r, err)
	}

	// Un elenco vuoto è valido: l'app non esce da nessuna parte (oltre a peer e DNS).
	e.want(e.do(http.MethodPut, base, "alice", `{"destinations":[]}`), http.StatusOK, "")
	if got := decodeEgress[openapi.AppEgress](t, e.do(http.MethodGet, base, "alice", "").Body); !got.Restricted || len(got.Destinations) != 0 {
		t.Fatalf("elenco vuoto: %+v", got)
	}

	// Toglie la restrizione; idempotente.
	e.want(e.do(http.MethodDelete, base, "alice", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodDelete, base, "alice", ""), http.StatusNoContent, "")
	if got := decodeEgress[openapi.AppEgress](t, e.do(http.MethodGet, base, "alice", "").Body); got.Restricted {
		t.Fatalf("restrizione tolta: %+v", got)
	}

	// L'amministratore chiude tutto: l'elenco si svuota.
	e.want(e.do(http.MethodPut, "/admin/apps/egress", "carol", `{"privateDestinations":[]}`), http.StatusOK, "")
	if l := decodeEgress[openapi.AppsEgressSettings](t, e.do(http.MethodGet, "/admin/apps/egress", "carol", "").Body); len(l.PrivateDestinations) != 0 {
		t.Fatalf("elenco svuotato: %+v", l)
	}
}
