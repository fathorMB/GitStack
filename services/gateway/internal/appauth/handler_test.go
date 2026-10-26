package appauth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/gateway/internal/identityclient"
)

const (
	testSecret = "segreto-del-gateway"
	appID      = "11111111-1111-1111-1111-111111111111"
	repoID     = "22222222-2222-2222-2222-222222222222"
	appHost    = "web-handbook-acme.apps.example.com"
	aliceID    = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	bobID      = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	agentID    = "cccccccc-cccc-cccc-cccc-cccccccccccc"
)

// sigFor è la firma che core mette nell'indirizzo del forward-auth (services/core/internal/apps.AuthSignature).
func sigFor(secret, app, repo string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("gitstack-appauth-v1\n" + app + "\n" + repo))
	return hex.EncodeToString(m.Sum(nil))
}

// Il vettore è lo stesso del test di core (TestAuthSignature_VettoreFisso): se una delle due
// implementazioni cambia, uno dei due test diventa rosso.
func TestSigFor_VettoreFisso(t *testing.T) {
	if got := sigFor("s3", appID, repoID); got != vettoreFisso {
		t.Fatalf("firma dell'indirizzo cambiata: %s", got)
	}
}

const vettoreFisso = "cbc52710e95d93846b04998b95ed76d37d83fdd09c309a6695c9e44cf17bd0db"

type fakeIdentity struct {
	sessions map[string]identityclient.Caller // credenziale → chi è
	profiles map[string]identityclient.Profile
	read     map[string]bool // userID+"/"+repoID
	down     bool
	calls    int
}

func (f *fakeIdentity) Verify(_ context.Context, cred string, _ identityclient.Kind) (identityclient.Result, error) {
	if f.down {
		return identityclient.Result{}, identityclient.ErrUnavailable
	}
	c, ok := f.sessions[cred]
	if !ok {
		return identityclient.Result{}, nil
	}
	return identityclient.Result{Active: true, Principal: c}, nil
}

func (f *fakeIdentity) LookupProfile(_ context.Context, id string) (identityclient.Profile, error) {
	if f.down {
		return identityclient.Profile{}, identityclient.ErrUnavailable
	}
	p, ok := f.profiles[id]
	if !ok {
		return identityclient.Profile{}, identityclient.ErrUnavailable
	}
	return p, nil
}

func (f *fakeIdentity) CheckPermission(_ context.Context, user, resource, role string) (bool, error) {
	f.calls++
	if f.down {
		return false, identityclient.ErrUnavailable
	}
	if role != "read" {
		return false, errors.New("la regola di default chiede solo read")
	}
	return f.read[user+"/"+resource], nil
}

func newFake() *fakeIdentity {
	return &fakeIdentity{
		sessions: map[string]identityclient.Caller{
			"sess-alice":                  {UserID: aliceID, Username: "alice"},
			"sess-bob":                    {UserID: bobID, Username: "bob"},
			"gst_agent-read":              {UserID: agentID, Username: "botty", IsToken: true, Scopes: []string{"read:resource"}},
			"gst_agent-solo-registry":     {UserID: agentID, Username: "botty", IsToken: true, Scopes: []string{"read:registry"}},
			"gst_agent-no-repo-read":      {UserID: bobID, Username: "bob", IsToken: true, Scopes: []string{"read:resource"}},
			"gst_agent-write-and-read-ok": {UserID: agentID, Username: "botty", IsToken: true, Scopes: []string{"read:resource", "write:resource"}},
		},
		profiles: map[string]identityclient.Profile{
			aliceID: {ID: aliceID, Username: "alice", Email: "alice@example.com", Teams: []string{"acme/api", "acme/web"}, Active: true},
			bobID:   {ID: bobID, Username: "bob", Active: true},
			agentID: {ID: agentID, Username: "botty", Active: true},
		},
		read: map[string]bool{aliceID + "/" + repoID: true, agentID + "/" + repoID: true},
	}
}

type env struct {
	h    *Handler
	id   *fakeIdentity
	now  time.Time
	keys []PublicKey
}

func newEnv(t *testing.T, mod func(*Config)) *env {
	t.Helper()
	signer, err := ParseSigningKeys(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{id: newFake(), now: time.Unix(1_800_000_000, 0)}
	cfg := Config{
		Verifier: e.id, Profiles: e.id, Permissions: e.id, Secret: testSecret, Signer: signer,
		PublicURL: "https://git.example.com", AppsDomain: "apps.example.com", Now: func() time.Time { return e.now },
	}
	if mod != nil {
		mod(&cfg)
	}
	e.h = New(cfg)
	e.keys = signer.PublicKeys()
	return e
}

// verify chiama il forward-auth come farebbe Traefik: indirizzo firmato da core e intestazioni
// X-Forwarded-* della richiesta originale.
func (e *env) verify(method, uri string, hdr map[string]string) *httptest.ResponseRecorder {
	return e.verifyWith(sigFor(testSecret, appID, repoID), appHost, method, uri, hdr)
}

func (e *env) verifyWith(sig, host, method, uri string, hdr map[string]string) *httptest.ResponseRecorder {
	q := url.Values{"app": {appID}, "repo": {repoID}, "sig": {sig}}
	r := httptest.NewRequest(method, PathVerify+"?"+q.Encode(), nil)
	r.Header.Set("X-Forwarded-Method", method)
	r.Header.Set("X-Forwarded-Host", host)
	r.Header.Set("X-Forwarded-Uri", uri)
	r.Header.Set("X-Forwarded-Proto", "https")
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.h.ServeVerify(w, r)
	return w
}

func signedFrom(w *httptest.ResponseRecorder, method, host, path string) (kid, sig string, in Signed) {
	h := w.Header()
	return h.Get(HeaderKeyID), h.Get(HeaderSignature), Signed{
		Method: method, Host: host, Path: path, AuthTime: h.Get(HeaderAuthTime),
		User: h.Get(HeaderUser), Email: h.Get(HeaderEmail), Teams: h.Get(HeaderTeams),
	}
}

// ---- criterio 1: utente senza read rifiutato, con read passa; agenti con token ----

func TestVerify_UtenteConReadPassaSenzaReadNo(t *testing.T) {
	e := newEnv(t, nil)
	w := e.verify("GET", "/", map[string]string{"Cookie": "gst_session=sess-alice"})
	if w.Code != http.StatusOK || w.Header().Get(HeaderUser) != "alice" {
		t.Fatalf("alice con read: %d %v", w.Code, w.Header())
	}
	w = e.verify("GET", "/", map[string]string{"Cookie": "gst_session=sess-bob"})
	if w.Code != http.StatusForbidden || w.Header().Get(HeaderUser) != "" || w.Header().Get(HeaderSignature) != "" {
		t.Fatalf("bob senza read: %d %v", w.Code, w.Header())
	}
	// Mai fail open: identity che non risponde non fa passare nessuno.
	e.id.down = true
	if w = e.verify("GET", "/", map[string]string{"Cookie": "gst_session=sess-alice"}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("identity giù: %d", w.Code)
	}
}

func TestVerify_TokenDAgente(t *testing.T) {
	e := newEnv(t, nil)
	for _, c := range []struct {
		name, token string
		want        int
	}{
		{"token con read e read sul repo", "gst_agent-read", http.StatusOK},
		{"token con read e altri scope", "gst_agent-write-and-read-ok", http.StatusOK},
		{"token senza scope read:resource", "gst_agent-solo-registry", http.StatusForbidden},
		{"token con read ma l'utente non legge il repo", "gst_agent-no-repo-read", http.StatusForbidden},
		{"token sconosciuto", "gst_inventato", http.StatusUnauthorized},
	} {
		w := e.verify("GET", "/api", map[string]string{"Authorization": "Bearer " + c.token})
		if w.Code != c.want {
			t.Errorf("%s: %d, atteso %d", c.name, w.Code, c.want)
		}
		if c.want == http.StatusOK && w.Header().Get(HeaderUser) != "botty" {
			t.Errorf("%s: utente %q", c.name, w.Header().Get(HeaderUser))
		}
	}
}

func TestVerify_SenzaCredenzialiNonPassa(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.PublicURL = "" })
	if w := e.verify("GET", "/", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("senza credenziali: %d", w.Code)
	}
	if w := e.verify("GET", "/", map[string]string{"Cookie": "gst_session=sconosciuta"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("sessione sconosciuta: %d", w.Code)
	}
}

// ---- criterio 2: intestazioni firmate, falsificate scartate ----

func TestVerify_IntestazioniFirmateVerificabiliConLaChiavePubblica(t *testing.T) {
	e := newEnv(t, nil)
	w := e.verify("GET", "/dashboard?x=1", map[string]string{"Cookie": "gst_session=sess-alice"})
	if w.Code != http.StatusOK {
		t.Fatalf("%d", w.Code)
	}
	h := w.Header()
	if h.Get(HeaderUser) != "alice" || h.Get(HeaderEmail) != "alice@example.com" || h.Get(HeaderTeams) != "acme/api,acme/web" {
		t.Fatalf("intestazioni: %v", h)
	}
	if h.Get(HeaderAuthTime) != "1800000000" {
		t.Fatalf("auth time: %v", h.Get(HeaderAuthTime))
	}

	// Le chiavi pubbliche si leggono dall'endpoint, come farebbe un'app.
	kw := httptest.NewRecorder()
	e.h.ServeKeys(kw, httptest.NewRequest("GET", PathKeys, nil))
	var doc struct {
		Keys []PublicKey `json:"keys"`
	}
	if err := json.Unmarshal(kw.Body.Bytes(), &doc); err != nil || len(doc.Keys) != 1 || doc.Keys[0].Algorithm != "Ed25519" {
		t.Fatalf("chiavi pubblicate: %s", kw.Body)
	}

	kid, sig, in := signedFrom(w, "GET", appHost, "/dashboard")
	if err := VerifySignature(doc.Keys, kid, sig, in); err != nil {
		t.Fatalf("la firma emessa deve verificare: %v", err)
	}
	// Ogni campo coperto: cambiarne uno fa fallire la verifica.
	for name, mutate := range map[string]func(Signed) Signed{
		"utente":   func(s Signed) Signed { s.User = "root"; return s },
		"email":    func(s Signed) Signed { s.Email = "ceo@example.com"; return s },
		"team":     func(s Signed) Signed { s.Teams += ",acme/admins"; return s },
		"ora":      func(s Signed) Signed { s.AuthTime = "1900000000"; return s },
		"percorso": func(s Signed) Signed { s.Path = "/admin"; return s },
		"host":     func(s Signed) Signed { s.Host = "altra.apps.example.com"; return s },
		"metodo":   func(s Signed) Signed { s.Method = "DELETE"; return s },
	} {
		if err := VerifySignature(doc.Keys, kid, sig, mutate(in)); err == nil {
			t.Errorf("modificando %s la firma deve smettere di tornare", name)
		}
	}
	if VerifySignature(doc.Keys, "chiave-sconosciuta", sig, in) == nil {
		t.Error("key id sconosciuto: rifiuto")
	}
}

func TestVerify_IntestazioneFalsificataDalClientScartata(t *testing.T) {
	e := newEnv(t, nil)
	// Il client prova a presentarsi come admin con intestazioni sue (e una firma finta).
	w := e.verify("GET", "/", map[string]string{
		"Cookie":            "gst_session=sess-alice",
		HeaderUser:          "root",
		HeaderEmail:         "root@example.com",
		HeaderTeams:         "acme/admins",
		HeaderAuthTime:      "1",
		HeaderKeyID:         "fasullo",
		HeaderSignature:     "ZmFsc2E",
		"X-Gitstack-Extra":  "x",
		"X-GitStack-Forged": "y",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("%d", w.Code)
	}
	h := w.Header()
	if h.Get(HeaderUser) != "alice" || h.Get(HeaderEmail) != "alice@example.com" || h.Get(HeaderTeams) != "acme/api,acme/web" {
		t.Fatalf("il gateway deve restituire solo ciò che ha stabilito lui: %v", h)
	}
	if h.Get("X-Gitstack-Extra") != "" || h.Get("X-GitStack-Forged") != "" {
		t.Fatalf("le intestazioni del client non vanno rispecchiate: %v", h)
	}
	kid, sig, in := signedFrom(w, "GET", appHost, "/")
	if kid == "fasullo" || sig == "ZmFsc2E" {
		t.Fatal("chiave e firma del client non vanno mai restituite")
	}
	if err := VerifySignature(e.keys, kid, sig, in); err != nil {
		t.Fatal(err)
	}
	// Una firma finta presentata all'app non verifica.
	forged := in
	forged.User = "root"
	if VerifySignature(e.keys, "fasullo", "ZmFsc2E", forged) == nil || VerifySignature(e.keys, kid, "ZmFsc2E", forged) == nil {
		t.Fatal("una firma falsificata deve essere scartata")
	}
}

func TestVerify_RotazioneDelleChiavi(t *testing.T) {
	old := base64.StdEncoding.EncodeToString([]byte("vecchia-chiave-vecchia-chiave-32"))
	cur := base64.StdEncoding.EncodeToString([]byte("nuova-chiave-nuova-chiave-nuova-"))
	s, err := ParseSigningKeys(cur + "," + old)
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, func(c *Config) { c.Signer = s })
	keys := s.PublicKeys()
	if len(keys) != 2 || keys[0].KeyID == keys[1].KeyID {
		t.Fatalf("chiavi: %+v", keys)
	}
	w := e.verify("GET", "/", map[string]string{"Cookie": "gst_session=sess-alice"})
	if w.Header().Get(HeaderKeyID) != keys[0].KeyID {
		t.Fatal("firma con la prima chiave")
	}
	if _, err := ParseSigningKeys("non-base64"); err == nil {
		t.Fatal("seme non valido")
	}
}

// ---- criterio 3: il cookie di GitStack non arriva all'app ----

func TestVerify_CookieEAuthorizationDiGitStackNonArrivanoAllApp(t *testing.T) {
	e := newEnv(t, nil)
	w := e.verify("GET", "/", map[string]string{
		"Cookie": "theme=dark; gst_session=sess-alice; __Host-gst_session=sess-alice; gst_app_session=y; lang=it",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("%d", w.Code)
	}
	// Traefik sostituisce il Cookie della richiesta con questo: restano solo quelli dell'app.
	if got := w.Header().Get("Cookie"); got != "theme=dark; lang=it" {
		t.Fatalf("Cookie verso l'app: %q", got)
	}
	// Con soli cookie di GitStack non si restituisce nulla: Traefik toglie l'intestazione.
	w = e.verify("GET", "/", map[string]string{"Cookie": "gst_session=sess-alice"})
	if _, ok := w.Header()["Cookie"]; ok {
		t.Fatalf("nessun cookie deve arrivare all'app: %v", w.Header()["Cookie"])
	}
	// Il token d'agente non viaggia verso l'app; un'Authorization dell'app sì.
	w = e.verify("GET", "/", map[string]string{"Authorization": "Bearer gst_agent-read"})
	if _, ok := w.Header()["Authorization"]; ok {
		t.Fatal("il token GitStack non deve arrivare all'app")
	}
	w = e.verify("GET", "/", map[string]string{"Cookie": "gst_session=sess-alice", "Authorization": "Basic YTpi"})
	if w.Header().Get("Authorization") != "Basic YTpi" {
		t.Fatalf("l'Authorization dell'app resta: %q", w.Header().Get("Authorization"))
	}
}

// ---- indirizzo del forward-auth firmato da core ----

func TestVerify_IndirizzoNonFirmatoORepoCambiato(t *testing.T) {
	e := newEnv(t, nil)
	hdr := map[string]string{"Cookie": "gst_session=sess-alice"}
	if w := e.verifyWith("", appHost, "GET", "/", hdr); w.Code != http.StatusForbidden {
		t.Fatalf("senza firma: %d", w.Code)
	}
	if w := e.verifyWith(sigFor("altro-segreto", appID, repoID), appHost, "GET", "/", hdr); w.Code != http.StatusForbidden {
		t.Fatalf("firma con un altro segreto: %d", w.Code)
	}
	// Chi chiama il gateway da fuori non può scegliere un altro repo con la firma di un'altra app.
	q := url.Values{"app": {appID}, "repo": {bobID}, "sig": {sigFor(testSecret, appID, repoID)}}
	r := httptest.NewRequest("GET", PathVerify+"?"+q.Encode(), nil)
	r.Header.Set("X-Forwarded-Host", appHost)
	r.Header.Set("Cookie", "gst_session=sess-alice")
	w := httptest.NewRecorder()
	e.h.ServeVerify(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("repo diverso dalla firma: %d", w.Code)
	}
	// Un host fuori dal dominio delle app non passa.
	if w := e.verifyWith(sigFor(testSecret, appID, repoID), "evil.example.org", "GET", "/", hdr); w.Code != http.StatusForbidden {
		t.Fatalf("host estraneo: %d", w.Code)
	}
	// Rotazione del segreto: vale anche la firma con la chiave vecchia dell'elenco.
	e2 := newEnv(t, func(c *Config) { c.Secret = "nuova," + testSecret })
	if w := e2.verify("GET", "/", hdr); w.Code != http.StatusOK {
		t.Fatalf("segreto in rotazione: %d", w.Code)
	}
}

func TestVerify_SenzaChiaviDiFirmaNonPassaNessuno(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.Signer = nil })
	if w := e.verify("GET", "/", map[string]string{"Cookie": "gst_session=sess-alice"}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("senza chiavi: %d", w.Code)
	}
	kw := httptest.NewRecorder()
	e.h.ServeKeys(kw, httptest.NewRequest("GET", PathKeys, nil))
	if kw.Code != http.StatusNotFound {
		t.Fatalf("chiavi: %d", kw.Code)
	}
}

// ---- un solo punto per la regola di accesso (GIT-376) ----

func TestVerify_LaRegolaDiAccessoSiEstendeInUnPuntoSolo(t *testing.T) {
	list := map[string]bool{bobID: true} // es. lista di accesso dei tester
	e := newEnv(t, func(c *Config) {
		def := DefaultAccess(c.Permissions)
		c.Access = func(ctx context.Context, user string, app App) (bool, error) {
			if ok, err := def(ctx, user, app); err != nil || ok {
				return ok, err
			}
			return list[user], nil
		}
	})
	if w := e.verify("GET", "/", map[string]string{"Cookie": "gst_session=sess-bob"}); w.Code != http.StatusOK {
		t.Fatalf("bob in lista: %d", w.Code)
	}
	delete(list, bobID)
	if w := e.verify("GET", "/", map[string]string{"Cookie": "gst_session=sess-bob"}); w.Code != http.StatusForbidden {
		t.Fatalf("bob fuori lista: %d", w.Code)
	}
}

// ---- passaggio dal browser ----

func (e *env) login(host, rd string, cookie string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", PathLogin+"?"+url.Values{"host": {host}, "rd": {rd}}.Encode(), nil)
	if cookie != "" {
		r.Header.Set("Cookie", cookie)
	}
	w := httptest.NewRecorder()
	e.h.ServeLogin(w, r)
	return w
}

func TestBrowser_DalLoginAlCookieDApp(t *testing.T) {
	e := newEnv(t, nil)
	// 1. Senza credenziali il browser è mandato al login GitStack sul suo host.
	w := e.verify("GET", "/docs?a=1", map[string]string{"Accept": "text/html,application/xhtml+xml"})
	loc, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusFound || loc.Host != "git.example.com" || loc.Path != "/api"+PathLogin ||
		loc.Query().Get("host") != appHost || loc.Query().Get("rd") != "/docs?a=1" {
		t.Fatalf("redirect al login: %d %s", w.Code, w.Header().Get("Location"))
	}
	// Una chiamata non-HTML (fetch, curl) riceve 401.
	if w = e.verify("GET", "/api", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("senza Accept html: %d", w.Code)
	}

	// 2. Sull'host di GitStack, con la sessione, si riceve un codice e si torna all'app.
	if w = e.login(appHost, "/docs?a=1", ""); w.Code != http.StatusFound || !strings.HasSuffix(w.Header().Get("Location"), "/login") {
		t.Fatalf("senza sessione: login web, %d %s", w.Code, w.Header().Get("Location"))
	}
	w = e.login(appHost, "/docs?a=1", "gst_session=sess-alice")
	cb, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusFound || cb.Scheme != "https" || cb.Host != appHost || cb.Path != CallbackPath || cb.Query().Get("rd") != "/docs?a=1" {
		t.Fatalf("ritorno all'app: %d %s", w.Code, w.Header().Get("Location"))
	}

	// 3. Il ritorno passa dal forward-auth: rilascia il cookie d'app e rimanda alla pagina.
	w = e.verify("GET", cb.RequestURI(), nil)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/docs?a=1" {
		t.Fatalf("callback: %d %v", w.Code, w.Header())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != AppCookie || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].Path != "/" {
		t.Fatalf("cookie d'app: %+v", cookies)
	}
	ck := cookies[0].Name + "=" + cookies[0].Value

	// 4. Con il cookie d'app l'utente passa, e quel cookie non arriva all'app.
	w = e.verify("GET", "/docs", map[string]string{"Cookie": "theme=dark; " + ck})
	if w.Code != http.StatusOK || w.Header().Get(HeaderUser) != "alice" || w.Header().Get("Cookie") != "theme=dark" {
		t.Fatalf("con il cookie d'app: %d %v", w.Code, w.Header())
	}

	// Il cookie è legato all'host, scade, e vale solo finché l'utente ha read.
	if w = e.verifyWith(sigFor(testSecret, appID, repoID), "altra.apps.example.com", "GET", "/", map[string]string{"Cookie": ck}); w.Code != http.StatusUnauthorized {
		t.Fatalf("cookie su un altro host: %d", w.Code)
	}
	e.id.read[aliceID+"/"+repoID] = false
	if w = e.verify("GET", "/docs", map[string]string{"Cookie": ck}); w.Code != http.StatusForbidden {
		t.Fatalf("read revocato: il cookie non basta, %d", w.Code)
	}
	e.id.read[aliceID+"/"+repoID] = true
	e.now = e.now.Add(9 * time.Hour)
	if w = e.verify("GET", "/docs", map[string]string{"Cookie": ck}); w.Code != http.StatusUnauthorized {
		t.Fatalf("cookie scaduto: %d", w.Code)
	}

	// Il codice è breve e legato all'host: scaduto non vale più.
	if w = e.verify("GET", cb.RequestURI(), nil); w.Code != http.StatusForbidden {
		t.Fatalf("codice scaduto: %d", w.Code)
	}
}

func TestBrowser_CallbackSenzaReadNonDaIlCookie(t *testing.T) {
	e := newEnv(t, nil)
	w := e.login(appHost, "/", "gst_session=sess-bob") // bob è un utente valido, ma senza read sul repo
	cb, _ := url.Parse(w.Header().Get("Location"))
	w = e.verify("GET", cb.RequestURI(), nil)
	if w.Code != http.StatusForbidden || len(w.Result().Cookies()) != 0 {
		t.Fatalf("senza read: %d cookie %v", w.Code, w.Result().Cookies())
	}
}

func TestBrowser_RichiesteMalformateRifiutate(t *testing.T) {
	e := newEnv(t, nil)
	for _, rd := range []string{"//evil.example.org/", "https://evil.example.org/", "/\\evil", "docs", "/a\nb", ""} {
		if w := e.login(appHost, rd, "gst_session=sess-alice"); w.Code != http.StatusBadRequest {
			t.Errorf("rd %q: %d", rd, w.Code)
		}
	}
	for _, host := range []string{"evil.example.org", "apps.example.com", "x.y.apps.example.com", appHost + ".evil.org"} {
		if w := e.login(host, "/", "gst_session=sess-alice"); w.Code != http.StatusBadRequest {
			t.Errorf("host %q: %d", host, w.Code)
		}
	}
	// Un codice rubato non vale su un altro host né come cookie; un cookie non vale come codice.
	code := e.h.sealToken(tokenCode, aliceID, appHost, time.Minute)
	if _, ok := e.h.openToken(code, tokenSession, appHost); ok {
		t.Error("un codice non è un cookie")
	}
	if _, ok := e.h.openToken(code, tokenCode, "altra.apps.example.com"); ok {
		t.Error("codice su un altro host")
	}
	if _, ok := e.h.openToken(code+"x", tokenCode, appHost); ok {
		t.Error("firma alterata")
	}
}

func TestStripCookies(t *testing.T) {
	for in, want := range map[string]string{
		"":                      "",
		"a=1":                   "a=1",
		"gst_session=x":         "",
		"a=1;gst_session=x;b=2": "a=1; b=2",
		"__Host-gst_session=x; gst_app_session=y": "",
		"mygst_session=1":                         "mygst_session=1",
	} {
		if got := stripCookies(in); got != want {
			t.Errorf("%q → %q, atteso %q", in, got, want)
		}
	}
}
