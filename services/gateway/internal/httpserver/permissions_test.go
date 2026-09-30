package httpserver

import (
	"net/http"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/gateway/internal/identityclient"
	"github.com/fathorMB/GitStack/services/gateway/internal/security"
)

const (
	resID     = "00000000-0000-0000-0000-000000000001"
	aliceID   = "11111111-1111-1111-1111-111111111111"
	resPath   = "/v1/resources/" + resID
	noScopeID = "gst_u"
)

// Il permesso dichiarato dalla rotta (x-required-permission) viene chiesto a
// identity dopo l'autenticazione e lo scope; la risposta decide fra 204
// (arriva a core), 403 forbidden e 503.
func TestPermessi_TabellaMiddleware(t *testing.T) {
	deny := func(string, string, string) bool { return false }
	allowRead := func(_, _, role string) bool { return role == "read" }

	cases := []struct {
		name     string
		req      func() *http.Request
		allow    func(string, string, string) bool
		permErr  error
		want     int
		wantCode string
		wantRole string // "" = identity non deve essere interpellata
		toCore   bool
	}{
		{name: "GET con read concesso", req: func() *http.Request { return bearerReq("GET", resPath, "gst_x") }, want: 204, wantRole: "read", toCore: true},
		{name: "PATCH chiede write", req: func() *http.Request { return bearerReq("PATCH", resPath, "gst_x") }, want: 204, wantRole: "write", toCore: true},
		{name: "DELETE chiede admin", req: func() *http.Request { return bearerReq("DELETE", resPath, "gst_x") }, want: 204, wantRole: "admin", toCore: true},
		{name: "sessione web: stesso controllo", req: func() *http.Request { return cookieReq("GET", resPath, "sess") }, want: 204, wantRole: "read", toCore: true},
		{name: "GET negato", req: func() *http.Request { return bearerReq("GET", resPath, "gst_x") }, allow: deny, want: 403, wantCode: "forbidden", wantRole: "read"},
		{name: "PATCH negato con solo read", req: func() *http.Request { return bearerReq("PATCH", resPath, "gst_x") }, allow: allowRead, want: 403, wantCode: "forbidden", wantRole: "write"},
		{name: "DELETE negato con solo read", req: func() *http.Request { return bearerReq("DELETE", resPath, "gst_x") }, allow: allowRead, want: 403, wantCode: "forbidden", wantRole: "admin"},
		{name: "identity non risponde: 503", req: func() *http.Request { return bearerReq("GET", resPath, "gst_x") }, permErr: identityclient.ErrUnavailable, want: 503, wantCode: "identity_unavailable", wantRole: "read"},
		{name: "senza credenziali: 401 senza interpellare identity", req: func() *http.Request { return bearerReq("GET", resPath, "") }, want: 401, wantCode: "unauthenticated"},
		{name: "credenziale non attiva: 401", req: func() *http.Request { return bearerReq("GET", resPath, "gst_nope") }, want: 401, wantCode: "unauthenticated"},
		{name: "scope mancante: 403 insufficient_scope prima del permesso", req: func() *http.Request { return bearerReq("GET", resPath, noScopeID) }, want: 403, wantCode: "insufficient_scope"},
		{name: "id non UUID: 400 (lo rifiuta già il router generato)", req: func() *http.Request { return bearerReq("GET", "/v1/resources/non-un-uuid", "gst_x") }, want: 400},
		{name: "rotta senza {resourceId}: nessun controllo di permesso", req: func() *http.Request { return bearerReq("GET", "/v1/resources", "gst_x") }, want: 204, toCore: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := &stubVerifier{results: map[string]identityclient.Result{
				"gst_x":   tokenResult("read:resource", "write:resource"),
				noScopeID: tokenResult("read:user"),
				"sess":    sessionResult("alice", false),
			}}
			e := newEnv(t, v)
			e.perms.allow, e.perms.err = c.allow, c.permErr
			rec := e.do(c.req())
			if rec.Code != c.want {
				t.Fatalf("status %d, voluto %d (%s)", rec.Code, c.want, rec.Body.String())
			}
			if c.wantCode != "" && errorCode(t, rec) != c.wantCode {
				t.Errorf("code = %s, voluto %s", errorCode(t, rec), c.wantCode)
			}
			calls := e.perms.seen()
			if c.wantRole == "" && len(calls) != 0 {
				t.Errorf("identity interpellata senza motivo: %v", calls)
			}
			if c.wantRole != "" {
				if len(calls) != 1 || calls[0].Role != c.wantRole || calls[0].ResourceID != resID {
					t.Fatalf("verifiche = %+v, voluta una per %s su %s", calls, c.wantRole, resID)
				}
				if calls[0].UserID == "" {
					t.Error("userId vuoto nella verifica")
				}
			}
			if got := e.core.count() == 1; got != c.toCore {
				t.Errorf("richieste a core = %d, arrivata=%v voluto %v", e.core.count(), got, c.toCore)
			}
			if c.want == 403 && c.wantCode == "forbidden" && strings.Contains(rec.Body.String(), resID) {
				t.Errorf("il 403 rivela l'id della risorsa: %s", rec.Body.String())
			}
		})
	}
}

// Negato è 403 anche se la risorsa non esiste: core non viene mai
// interpellato, quindi la risposta non dipende dall'esistenza.
func TestPermessi_NegatoUgualeSeLaRisorsaNonEsiste(t *testing.T) {
	e := newEnv(t, allowAll())
	e.perms.allow = func(string, string, string) bool { return false }
	a := e.do(bearerReq("GET", resPath, "gst_x"))
	b := e.do(bearerReq("GET", "/v1/resources/ffffffff-ffff-ffff-ffff-ffffffffffff", "gst_x"))
	if a.Code != 403 || b.Code != 403 || a.Body.String() != b.Body.String() {
		t.Errorf("risposte diverse: %d %s / %d %s", a.Code, a.Body.String(), b.Code, b.Body.String())
	}
	if e.core.count() != 0 {
		t.Error("core interpellato su una richiesta negata")
	}
}

// Il gateway non parte con un checker di permessi assente per finta: senza
// verifica disponibile le rotte con permesso rispondono 503.
func TestPermessi_SenzaCheckerEPermessoRichiesto503(t *testing.T) {
	e := newEnv(t, allowAll())
	e.router = NewRouter(newTestConfig(t, e.core.srv.URL), discardLogger(), WithVerifier(allowAll()))
	rec := e.do(bearerReq("GET", resPath, "gst_x"))
	if rec.Code != 503 || errorCode(t, rec) != "identity_unavailable" {
		t.Errorf("status %d corpo %s", rec.Code, rec.Body.String())
	}
}

// Ogni rotta non pubblica di core su {resourceId} dichiara il permesso
// (niente rotte aperte per dimenticanza) e ogni permesso dichiarato è valido.
func TestPermessi_OgniRottaDiCoreSuRisorsaDichiaraIlPermesso(t *testing.T) {
	want := map[string]security.Permission{
		"GET /resources/{resourceId}":    security.PermissionRead,
		"PATCH /resources/{resourceId}":  security.PermissionWrite,
		"DELETE /resources/{resourceId}": security.PermissionAdmin,
	}
	found := 0
	for _, r := range security.Routes {
		if r.Service == security.ServiceCore && !r.Public && strings.Contains(r.Path, "{resourceId}") {
			found++
			if w, ok := want[r.Method+" "+r.Path]; !ok || r.Permission != w {
				t.Errorf("%s %s: permesso %q, voluto %q", r.Method, r.Path, r.Permission, w)
			}
		}
		if r.Permission != security.PermissionNone && (r.Public || r.Service != security.ServiceCore) {
			t.Errorf("%s %s: permesso dichiarato su una rotta che il gateway non controlla", r.Method, r.Path)
		}
	}
	if found != len(want) {
		t.Errorf("rotte di core su {resourceId} trovate: %d, attese %d", found, len(want))
	}
}
