//go:build integration

package httpserver_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-376 (H6 del 2026-10-26, pilota retail.it): la lista di accesso dell'app per i tester esterni.
// alice è admin; bob ha write e carol read sul repo internal «handbook». Sul repo privato «riservato»
// carol e dave non hanno nessun ruolo: sono i tester (carol diretta, dave tramite il team retail/business).

type listEnv struct {
	*appsEnv
	nid   *notifyIdentity
	caDir string
	fp    string
}

func newListEnv(t *testing.T) *listEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	users["dave"] = daveID
	base := &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}
	base.owners["dave"] = ownerUser(daveID, "dave")
	nid := &notifyIdentity{issuesIdentity: base, revoked: map[uuid.UUID]map[string]bool{}, teams: map[string][]string{"retail/business": {"dave"}}}
	git := newFakeGit()
	clock := &appClock{t: time.Now().UTC().Truncate(time.Second)}

	// CA di prova: l'impronta attesa è lo SHA-256 del DER, come lo calcolerebbe openssl sul file servito su /downloads/ca.crt.
	caDir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(9), Subject: pkix.Name{CommonName: "GitStack test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caDir, "ca.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}

	e := &issuesEnv{t: t, pool: pool, id: base}
	e.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(nid), httpserver.WithReadableLister(nid), httpserver.WithGit(git),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}),
		httpserver.WithAppsDomain("apps.example.com"), httpserver.WithClock(clock.Now),
		httpserver.WithInstanceCA(httpserver.CAConfig{Dir: caDir}))
	return &listEnv{appsEnv: &appsEnv{issuesEnv: e, clock: clock, git: git}, nid: nid, caDir: caDir, fp: strings.Join(parts, ":")}
}

// appID crea l'app «web» sul repo e ne ritorna l'id.
func (e *listEnv) appID(owner, repo string) string {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/repos/"+owner+"/"+repo+"/apps", "alice", createWeb)
	e.want(rec, http.StatusAccepted, "")
	var c openapi.AppCreated
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		e.t.Fatal(err)
	}
	return uuid.UUID(c.App.Id).String()
}

func (e *listEnv) members(path, user string) []openapi.AppAccessMember {
	e.t.Helper()
	rec := e.do(http.MethodGet, path, user, "")
	e.want(rec, http.StatusOK, "")
	var l openapi.AppAccessMemberList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		e.t.Fatal(err)
	}
	return l.Items
}

func (e *listEnv) isMember(appID, user string) bool {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/apps/"+appID+"/access-membership", user, "")
	e.want(rec, http.StatusOK, "")
	var m openapi.AppAccessMembership
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		e.t.Fatal(err)
	}
	return m.Member
}

// Solo admin sull'app modifica la lista e chiede l'invito; write e read ricevono 403; chi non vede il repo 404.
func TestAppsAccessList_SoloAdmin(t *testing.T) {
	e := newListEnv(t)
	repoID := e.repo("handbook", true)
	e.id.grantWrite(repoID, "bob")
	e.repo("riservato", false)
	e.appID("alice", "handbook")
	base := "/repos/alice/handbook/apps/web/access"
	add := `{"kind":"user","name":"carol"}`

	for _, user := range []string{"bob", "carol"} { // write e read
		e.want(e.do(http.MethodGet, base+"/members", user, ""), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodPost, base+"/members", user, add), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodDelete, base+"/members/"+uuid.NewString(), user, ""), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodGet, base+"/invite", user, ""), http.StatusForbidden, "forbidden")
	}
	// Un repo che non si legge è 404 anche per la lista.
	e.appID("alice", "riservato")
	e.want(e.do(http.MethodGet, "/repos/alice/riservato/apps/web/access/members", "carol", ""), http.StatusNotFound, "")
	e.want(e.do(http.MethodGet, "/repos/alice/riservato/apps/web/access/invite", "carol", ""), http.StatusNotFound, "")

	// admin: aggiunge, vede, toglie.
	rec := e.do(http.MethodPost, base+"/members", "alice", add)
	e.want(rec, http.StatusCreated, "")
	var m openapi.AppAccessMember
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || m.Kind != openapi.AppAccessMemberKindUser || m.Name != "carol" || m.AddedBy == nil || m.AddedBy.Username != "alice" {
		t.Fatalf("membro: %v %s", err, rec.Body.String())
	}
	if l := e.members(base+"/members", "alice"); len(l) != 1 || l[0].Name != "carol" {
		t.Fatalf("lista: %+v", l)
	}
	e.want(e.do(http.MethodDelete, base+"/members/"+uuid.UUID(m.Id).String(), "alice", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodDelete, base+"/members/"+uuid.UUID(m.Id).String(), "alice", ""), http.StatusNotFound, "")
	if l := e.members(base+"/members", "alice"); len(l) != 0 {
		t.Fatalf("lista dopo la rimozione: %+v", l)
	}
}

func TestAppsAccessList_Validazione(t *testing.T) {
	e := newListEnv(t)
	e.repo("handbook", true)
	e.appID("alice", "handbook")
	p := "/repos/alice/handbook/apps/web/access/members"

	e.want(e.do(http.MethodPost, p, "alice", `{"kind":"user","name":"carol"}`), http.StatusCreated, "")
	e.want(e.do(http.MethodPost, p, "alice", `{"kind":"user","name":"carol"}`), http.StatusConflict, "already_member")
	e.want(e.do(http.MethodPost, p, "alice", `{"kind":"user","name":"nessuno"}`), http.StatusUnprocessableEntity, "name")
	e.want(e.do(http.MethodPost, p, "alice", `{"kind":"team","name":"retail/inesistente"}`), http.StatusUnprocessableEntity, "name")
	e.want(e.do(http.MethodPost, p, "alice", `{"kind":"team","name":"senza-barra"}`), http.StatusUnprocessableEntity, "name")
	e.want(e.do(http.MethodPost, p, "alice", `{"kind":"anon","name":"x"}`), http.StatusBadRequest, "")
	e.want(e.do(http.MethodPost, p, "alice", `{"kind":"user","name":"carol","extra":1}`), http.StatusBadRequest, "")
	// Il team si accetta in qualunque maiuscola e si conserva minuscolo; il doppione è 409.
	rec := e.do(http.MethodPost, p, "alice", `{"kind":"team","name":"Retail/Business"}`)
	e.want(rec, http.StatusCreated, "")
	var m openapi.AppAccessMember
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || m.Name != "retail/business" || m.MemberCount == nil || *m.MemberCount != 1 {
		t.Fatalf("team: %v %s", err, rec.Body.String())
	}
	e.want(e.do(http.MethodPost, p, "alice", `{"kind":"team","name":"retail/business"}`), http.StatusConflict, "already_member")
	// Un'app che non c'è è 404.
	e.want(e.do(http.MethodGet, "/repos/alice/handbook/apps/nessuna/access/members", "alice", ""), http.StatusNotFound, "")
}

// Un utente senza ruolo sul repo ma nella lista (diretto o tramite team) è riconosciuto membro; tolto dalla
// lista no. Lo stesso utente riceve 404 su codice, issues, scheda Apps e API del repo: la lista non è un ruolo.
func TestAppsAccessList_MembroDirettoOTeamNonVedeIlRepo(t *testing.T) {
	e := newListEnv(t)
	e.repo("riservato", false)
	app := e.appID("alice", "riservato")
	p := "/repos/alice/riservato/apps/web/access/members"

	for _, u := range []string{"carol", "dave", "bob"} {
		if e.isMember(app, u) {
			t.Fatalf("%s non è in lista", u)
		}
	}
	rec := e.do(http.MethodPost, p, "alice", `{"kind":"user","name":"carol"}`)
	e.want(rec, http.StatusCreated, "")
	var carol openapi.AppAccessMember
	_ = json.Unmarshal(rec.Body.Bytes(), &carol)
	rec = e.do(http.MethodPost, p, "alice", `{"kind":"team","name":"retail/business"}`)
	e.want(rec, http.StatusCreated, "")
	var team openapi.AppAccessMember
	_ = json.Unmarshal(rec.Body.Bytes(), &team)

	if !e.isMember(app, "carol") || !e.isMember(app, "dave") || e.isMember(app, "bob") {
		t.Fatal("carol (diretta) e dave (team) sono membri, bob no")
	}
	// Nessun ruolo sul repo, nessuna visibilità: 404 su tutto il resto.
	for _, user := range []string{"carol", "dave"} {
		for _, path := range []string{
			"/repos/alice/riservato", "/repos/alice/riservato/tree", "/repos/alice/riservato/branches", "/repos/alice/riservato/commits",
			"/repos/alice/riservato/contents?path=README.md", "/repos/alice/riservato/issues", "/repos/alice/riservato/apps",
			"/repos/alice/riservato/apps/web", "/repos/alice/riservato/apps/web/access", "/repos/alice/riservato/apps/web/versions",
			"/repos/alice/riservato/apps/web/access/members", "/repos/alice/riservato/apps/web/access/invite",
		} {
			if rec := e.do(http.MethodGet, path, user, ""); rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
				t.Fatalf("%s su %s: %d %s", user, path, rec.Code, rec.Body.String())
			}
		}
		e.want(e.do(http.MethodPost, "/repos/alice/riservato/issues", user, `{"title":"x"}`), http.StatusNotFound, "")
		e.want(e.do(http.MethodPost, "/repos/alice/riservato/apps/web/access/members", user, `{"kind":"user","name":"bob"}`), http.StatusNotFound, "")
	}
	// Non risulta nessun ruolo: la lista non crea grant (la riservatezza del repo non cambia, H5).
	var got int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.app_access_members`).Scan(&got); err != nil || got != 2 {
		t.Fatalf("righe: %d", got)
	}

	// Tolti dalla lista, non sono più membri (il gateway li rifiuta).
	e.want(e.do(http.MethodDelete, p+"/"+uuid.UUID(carol.Id).String(), "alice", ""), http.StatusNoContent, "")
	if e.isMember(app, "carol") || !e.isMember(app, "dave") {
		t.Fatal("carol tolta, dave resta via team")
	}
	e.want(e.do(http.MethodDelete, p+"/"+uuid.UUID(team.Id).String(), "alice", ""), http.StatusNoContent, "")
	if e.isMember(app, "dave") {
		t.Fatal("team tolto: dave non è più membro")
	}
	// Un'app che non c'è: member falso, non un errore.
	if e.isMember(uuid.NewString(), "carol") {
		t.Fatal("app inesistente")
	}
}

// Un team che si svuota non apre più l'app.
func TestAppsAccessList_TeamSvuotato(t *testing.T) {
	e := newListEnv(t)
	e.repo("riservato", false)
	app := e.appID("alice", "riservato")
	e.want(e.do(http.MethodPost, "/repos/alice/riservato/apps/web/access/members", "alice", `{"kind":"team","name":"retail/business"}`), http.StatusCreated, "")
	e.nid.teams = map[string][]string{"retail/business": {}}
	if e.isMember(app, "dave") {
		t.Fatal("team svuotato")
	}
}

// L'invito: link alla guida con l'app come parametro, indirizzo, impronta uguale a GET /instance/ca e al
// certificato servito su /downloads/ca.crt (SHA-256 del DER), testo come nel mockup H13.
func TestAppsAccessList_Invito(t *testing.T) {
	e := newListEnv(t)
	repoID := e.repo("handbook", true)
	e.id.grantWrite(repoID, "bob")
	e.appID("alice", "handbook")
	path := "/repos/alice/handbook/apps/web/access/invite"

	rec := e.do(http.MethodGet, path, "alice", "")
	e.want(rec, http.StatusOK, "")
	var inv openapi.AppAccessInvite
	if err := json.Unmarshal(rec.Body.Bytes(), &inv); err != nil {
		t.Fatal(err)
	}
	if inv.GuideUrl != "https://git.example.com/help/ca?app=web-handbook-alice.apps.example.com" || inv.Address != "https://web-handbook-alice.apps.example.com" || inv.CaMode != openapi.AppAccessInviteCaModeInternal {
		t.Fatalf("invito: %s", rec.Body.String())
	}
	if inv.FingerprintSha256 == nil || *inv.FingerprintSha256 != e.fp {
		t.Fatalf("impronta dell'invito %v, attesa quella del certificato %s", inv.FingerprintSha256, e.fp)
	}
	// Uguale a GET /instance/ca (fonte unica di GIT-383).
	rec = e.do(http.MethodGet, "/instance/ca", "alice", "")
	e.want(rec, http.StatusOK, "")
	var ca openapi.InstanceCa
	if err := json.Unmarshal(rec.Body.Bytes(), &ca); err != nil || ca.FingerprintSha256 == nil || *ca.FingerprintSha256 != *inv.FingerprintSha256 {
		t.Fatalf("GET /instance/ca: %v %s", err, rec.Body.String())
	}
	want := "Hi, you can now test web (alice/handbook).\n\n" +
		"1. Open " + inv.GuideUrl + " and follow the steps for your device.\n" +
		"   Your browser may warn you on that page: continue only if the page shows this fingerprint:\n" +
		"   SHA-256 " + e.fp + "\n" +
		"2. Open https://web-handbook-alice.apps.example.com and sign in with your GitStack account.\n" +
		"3. Found a problem? Use \"Report a problem\" inside the app."
	if inv.Text != want {
		t.Fatalf("testo:\n%s\n--- atteso:\n%s", inv.Text, want)
	}
}
