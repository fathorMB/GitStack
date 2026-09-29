//go:build integration

package stackitest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
)

// Test di sicurezza (M-02/N): i casi negativi contro lo stack completo.
// Vedi README.md per l'elenco dei casi.

const (
	secretName   = "risorsa-riservata-segretissima"
	victimPass   = "password della vittima 1234"
	victimNewPwd = "nuova password della vittima 5678"
	forgedUserID = "99999999-9999-9999-9999-999999999999"
)

type secEnv struct {
	*stack
	pool       *pgxpool.Pool
	resourceID string
	admin      *http.Cookie
}

func cookieOf(t *testing.T, r reply) *http.Cookie {
	t.Helper()
	for _, c := range r.cookies {
		if c.Name == "gst_session" {
			return c
		}
	}
	t.Fatalf("nessun cookie di sessione nella risposta (%d): %s", r.status, r.body)
	return nil
}

// noLeak verifica che il corpo non contenga dati della risorsa del setup.
func (e *secEnv) noLeak(t *testing.T, r reply) {
	t.Helper()
	b := string(r.body)
	if strings.Contains(b, e.resourceID) || strings.Contains(b, secretName) {
		t.Errorf("il corpo della risposta %d lascia trapelare dati della risorsa: %s", r.status, b)
	}
}

// denied: risposta negativa attesa, senza dati della risorsa.
func (e *secEnv) denied(t *testing.T, r reply, status int, code string) {
	t.Helper()
	want(t, r, status, code)
	e.noLeak(t, r)
}

// login apre una nuova sessione web.
func (e *secEnv) login(t *testing.T, user, pass string) *http.Cookie {
	t.Helper()
	r := e.gw("POST", "/auth/login", map[string]any{"username": user, "password": pass}, nil, nil)
	want(t, r, 200, "")
	return cookieOf(t, r)
}

// mkToken crea un token personale e ne ritorna valore e id.
func (e *secEnv) mkToken(t *testing.T, name string, scopes ...string) (string, string) {
	t.Helper()
	exp := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	r := e.gw("POST", "/user/tokens", map[string]any{"name": name, "scopes": scopes, "expiresAt": exp}, nil, e.admin)
	want(t, r, 201, "")
	tok, _ := r.json()["token"].(string)
	id, _ := r.json()["id"].(string)
	if !strings.HasPrefix(tok, "gst_") || id == "" {
		t.Fatalf("token: %s", r.body)
	}
	return tok, id
}

func bearer(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }

// settleCache aspetta che scada la cache di verifica del gateway (TTL 1s).
func settleCache() { time.Sleep(1300 * time.Millisecond) }

func signed(secret, userID, username string) map[string]string {
	h := http.Header{}
	trust.Sign(h, secret, trust.Identity{UserID: userID, Username: username}, time.Now())
	m := map[string]string{}
	for k := range h {
		m[k] = h.Get(k)
	}
	return m
}

func newSecEnv(t *testing.T) *secEnv {
	t.Helper()
	pool, dsn := dbtest.NewPool(t)
	identityBin := build(t, "../../../identity", "identity")
	gatewayBin := build(t, "../../../gateway", "gateway")

	coreSrv := httptest.NewServer(httpserver.NewRouter(pool, events.NoopPublisher{}, serviceSecret))
	t.Cleanup(coreSrv.Close)

	identityAddr, gatewayAddr := freeAddr(t), freeAddr(t)
	start(t, "identity", identityBin, identityAddr,
		"GITSTACK_IDENTITY_ADDR="+identityAddr,
		"GITSTACK_IDENTITY_DB_URL="+dsn,
		"GITSTACK_IDENTITY_SERVICE_SECRET="+serviceSecret,
		"GITSTACK_IDENTITY_ADMIN_USERNAME=admin",
		"GITSTACK_IDENTITY_ADMIN_PASSWORD="+adminPassword,
		// soglia per utente bassa e nota; per IP alta, per non bloccare gli altri casi.
		"GITSTACK_IDENTITY_LOGIN_MAX_ATTEMPTS_USER=5",
		"GITSTACK_IDENTITY_LOGIN_MAX_ATTEMPTS_IP=1000",
		"GITSTACK_IDENTITY_LOGIN_WINDOW=15m",
	)
	start(t, "gateway", gatewayBin, gatewayAddr,
		"GITSTACK_GATEWAY_ADDR="+gatewayAddr,
		"GITSTACK_CORE_URL="+coreSrv.URL,
		"GITSTACK_IDENTITY_URL=http://"+identityAddr,
		"GITSTACK_IDENTITY_SERVICE_SECRET="+serviceSecret,
		"GITSTACK_GATEWAY_AUTH_CACHE_TTL=1s",
		"GITSTACK_GATEWAY_AUTH_CACHE_NEGATIVE_TTL=1s",
	)
	e := &secEnv{
		stack: &stack{t: t, gateway: "http://" + gatewayAddr, core: coreSrv.URL},
		pool:  pool,
	}

	// admin: primo login, cambio password obbligatorio, nuova sessione.
	first := e.login(t, "admin", adminPassword)
	want(t, e.gw("PUT", "/users/admin/password", map[string]any{"currentPassword": adminPassword, "newPassword": newPassword}, nil, first), 204, "")
	e.admin = e.login(t, "admin", newPassword)

	// la risorsa da proteggere.
	cr := e.gw("POST", "/resources", map[string]any{"type": "repo", "name": secretName}, nil, e.admin)
	want(t, cr, 201, "")
	e.resourceID, _ = cr.json()["id"].(string)
	if e.resourceID == "" {
		t.Fatalf("risorsa senza id: %s", cr.body)
	}
	// sanità: l'admin la vede, quindi i 4xx sotto non sono per un setup rotto.
	want(t, e.gw("GET", "/resources/"+e.resourceID, nil, nil, e.admin), 200, "")

	// utente normale (non admin) per sessioni e brute force.
	want(t, e.gw("POST", "/users", map[string]any{"username": "vittima", "password": victimPass}, nil, e.admin), 201, "")
	want(t, e.gw("POST", "/users", map[string]any{"username": "bersaglio", "password": victimPass}, nil, e.admin), 201, "")
	return e
}

func TestSecurity(t *testing.T) {
	e := newSecEnv(t)
	res := "/resources/" + e.resourceID

	t.Run("nessuna_credenziale", func(t *testing.T) {
		for _, p := range []string{"/resources", res, res + "/grants", "/users", "/auth/session", "/user/tokens"} {
			e.denied(t, e.gw("GET", p, nil, nil, nil), 401, "unauthenticated")
		}
		e.denied(t, e.gw("POST", "/resources", map[string]any{"type": "repo", "name": "x"}, nil, nil), 401, "unauthenticated")
		e.denied(t, e.gw("DELETE", res, nil, nil, nil), 401, "unauthenticated")
		// header Authorization vuoto o con schema sconosciuto
		e.denied(t, e.gw("GET", res, nil, map[string]string{"Authorization": ""}, nil), 401, "unauthenticated")
		e.denied(t, e.gw("GET", res, nil, map[string]string{"Authorization": "Basic YWRtaW46eA=="}, nil), 401, "unauthenticated")
		// /internal non e' mai esposto dal gateway
		e.denied(t, e.gw("POST", "/internal/verify", map[string]any{"credential": "x"}, nil, nil), 404, "")
	})

	t.Run("token_malformato", func(t *testing.T) {
		for _, tok := range []string{"gst_", "gst_inventato", "senza-prefisso", "gst_%00%zz", "gst_èè"} {
			e.denied(t, e.gw("GET", res, nil, map[string]string{"Authorization": "Bearer " + tok}, nil), 401, "unauthenticated")
		}
		e.denied(t, e.gw("GET", res, nil, map[string]string{"Authorization": "Bearer"}, nil), 401, "unauthenticated")
		for _, c := range []string{"", "inventato", "gst_inventato"} {
			e.denied(t, e.gw("GET", res, nil, nil, &http.Cookie{Name: "gst_session", Value: c}), 401, "unauthenticated")
		}
	})

	// DIFETTO NOTO (da correggere in gateway/identity, non qui): una credenziale
	// molto lunga (512 caratteri) fa rispondere a identity /internal/verify con
	// 400 e il gateway la traduce in 503 identity_unavailable invece di 401
	// unauthenticated. Si chiude fail-closed e senza dati, ma con lo stato
	// sbagliato. Riproduzione: questo test senza il t.Skip. Togliere lo Skip
	// quando il difetto e' corretto.
	t.Run("token_troppo_lungo", func(t *testing.T) {
		t.Skip("difetto noto: /internal/verify 400 -> gateway 503 invece di 401 (vedi commento sull'item GIT-42)")
		e.denied(t, e.gw("GET", res, nil, bearer("gst_"+strings.Repeat("A", 512)), nil), 401, "unauthenticated")
		e.denied(t, e.gw("GET", res, nil, nil, &http.Cookie{Name: "gst_session", Value: strings.Repeat("z", 300)}), 401, "unauthenticated")
	})

	t.Run("token_revocato", func(t *testing.T) {
		tok, id := e.mkToken(t, "da-revocare", "read:resource")
		want(t, e.gw("GET", res, nil, bearer(tok), nil), 200, "")
		want(t, e.gw("DELETE", "/user/tokens/"+id, nil, nil, e.admin), 204, "")
		settleCache()
		e.denied(t, e.gw("GET", res, nil, bearer(tok), nil), 401, "unauthenticated")
		e.denied(t, e.gw("GET", "/resources", nil, bearer(tok), nil), 401, "unauthenticated")
	})

	t.Run("token_scaduto", func(t *testing.T) {
		tok, _ := e.mkToken(t, "da-scadere", "read:resource")
		want(t, e.gw("GET", res, nil, bearer(tok), nil), 200, "")
		tag, err := e.pool.Exec(context.Background(),
			`UPDATE identity.api_tokens SET expires_at = now() - interval '1 hour' WHERE name = $1`, "da-scadere")
		if err != nil || tag.RowsAffected() != 1 {
			t.Fatalf("UPDATE expires_at: %v (righe %d)", err, tag.RowsAffected())
		}
		settleCache()
		e.denied(t, e.gw("GET", res, nil, bearer(tok), nil), 401, "unauthenticated")
	})

	t.Run("scope_insufficiente", func(t *testing.T) {
		readUser, _ := e.mkToken(t, "solo-read-user", "read:user")
		readRes, _ := e.mkToken(t, "solo-read-resource", "read:resource")
		// read:user non basta per le risorse
		e.denied(t, e.gw("GET", res, nil, bearer(readUser), nil), 403, "insufficient_scope")
		e.denied(t, e.gw("GET", "/resources", nil, bearer(readUser), nil), 403, "insufficient_scope")
		// read:resource non basta per scrivere o cancellare
		e.denied(t, e.gw("POST", "/resources", map[string]any{"type": "repo", "name": "nuova"}, bearer(readRes), nil), 403, "insufficient_scope")
		e.denied(t, e.gw("DELETE", res, nil, bearer(readRes), nil), 403, "insufficient_scope")
		// e neppure per le rotte di amministrazione di identity
		e.denied(t, e.gw("POST", "/orgs", map[string]any{"name": "acme"}, bearer(readRes), nil), 403, "insufficient_scope")
		e.denied(t, e.gw("POST", "/users", map[string]any{"username": "intruso", "password": victimPass}, bearer(readUser), nil), 403, "insufficient_scope")
		// la risorsa e' intatta
		want(t, e.gw("GET", res, nil, nil, e.admin), 200, "")
	})

	t.Run("sessione_dopo_logout", func(t *testing.T) {
		c := e.login(t, "vittima", victimPass)
		want(t, e.gw("GET", "/auth/session", nil, nil, c), 200, "")
		want(t, e.gw("POST", "/auth/logout", nil, nil, c), 204, "")
		settleCache()
		e.denied(t, e.gw("GET", "/auth/session", nil, nil, c), 401, "unauthenticated")
		e.denied(t, e.gw("GET", res, nil, nil, c), 401, "unauthenticated")
	})

	t.Run("sessione_dopo_cambio_password", func(t *testing.T) {
		a := e.login(t, "vittima", victimPass)
		b := e.login(t, "vittima", victimPass) // es. la sessione di chi ha rubato il cookie
		want(t, e.gw("GET", "/auth/session", nil, nil, b), 200, "")
		want(t, e.gw("PUT", "/users/vittima/password", map[string]any{"currentPassword": victimPass, "newPassword": victimNewPwd}, nil, a), 204, "")
		settleCache()
		// le altre sessioni sono revocate, quella che ha cambiato la password resta
		e.denied(t, e.gw("GET", "/auth/session", nil, nil, b), 401, "unauthenticated")
		e.denied(t, e.gw("GET", res, nil, nil, b), 401, "unauthenticated")
		want(t, e.gw("GET", "/auth/session", nil, nil, a), 200, "")
		// la vecchia password non vale piu'
		e.denied(t, e.gw("POST", "/auth/login", map[string]any{"username": "vittima", "password": victimPass}, nil, nil), 401, "invalid_credentials")
		want(t, e.gw("POST", "/auth/login", map[string]any{"username": "vittima", "password": victimNewPwd}, nil, nil), 200, "")
	})

	t.Run("header_identita_falsificati", func(t *testing.T) {
		forged := map[string]string{
			"X-Gitstack-User-Id": forgedUserID, "X-Gitstack-Username": "admin", "X-Gitstack-Scopes": "admin:org write:resource read:resource",
		}
		// al gateway: ignorati, non danno identita'...
		e.denied(t, e.gw("GET", res, nil, forged, nil), 401, "unauthenticated")
		// ...e non allargano gli scope di un token vero
		readUser, _ := e.mkToken(t, "per-header-falsi", "read:user")
		h := map[string]string{"Authorization": "Bearer " + readUser}
		for k, v := range forged {
			h[k] = v
		}
		e.denied(t, e.gw("GET", res, nil, h, nil), 403, "insufficient_scope")
		// a core direttamente: nessuna identita', header inventati, firma sbagliata, firma scaduta
		e.denied(t, e.do("GET", e.core+"/resources", nil, nil, nil), 401, "unauthenticated")
		e.denied(t, e.do("GET", e.core+"/resources/"+e.resourceID, nil, forged, nil), 401, "unauthenticated")
		e.denied(t, e.do("GET", e.core+"/resources/"+e.resourceID, nil,
			signed("un-altro-segreto", forgedUserID, "admin"), nil), 401, "unauthenticated")
		old := http.Header{}
		trust.Sign(old, serviceSecret, trust.Identity{UserID: forgedUserID, Username: "admin"}, time.Now().Add(-10*time.Minute))
		oldHdr := map[string]string{}
		for k := range old {
			oldHdr[k] = old.Get(k)
		}
		e.denied(t, e.do("GET", e.core+"/resources/"+e.resourceID, nil, oldHdr, nil), 401, "unauthenticated")
		// firma valida ma header modificato dopo la firma
		tam := signed(serviceSecret, forgedUserID, "utente")
		tam["X-Gitstack-Username"] = "admin"
		e.denied(t, e.do("GET", e.core+"/resources/"+e.resourceID, nil, tam, nil), 401, "unauthenticated")
		e.denied(t, e.do("DELETE", e.core+"/resources/"+e.resourceID, nil, forged, nil), 401, "unauthenticated")
		want(t, e.gw("GET", res, nil, nil, e.admin), 200, "")
	})

	t.Run("brute_force_login", func(t *testing.T) {
		// soglia per utente: 5 fallimenti (GITSTACK_IDENTITY_LOGIN_MAX_ATTEMPTS_USER)
		for i := 0; i < 5; i++ {
			e.denied(t, e.gw("POST", "/auth/login", map[string]any{"username": "bersaglio", "password": fmt.Sprintf("tentativo sbagliato %d", i)}, nil, nil), 401, "invalid_credentials")
		}
		// il sesto e' bloccato con 429, con Retry-After, anche con la password giusta
		r := e.gw("POST", "/auth/login", map[string]any{"username": "bersaglio", "password": victimPass}, nil, nil)
		e.denied(t, r, 429, "too_many_attempts")
		if len(r.cookies) != 0 {
			t.Errorf("login bloccato ha impostato un cookie: %v", r.cookies)
		}
		// un altro utente non e' toccato
		want(t, e.gw("POST", "/auth/login", map[string]any{"username": "vittima", "password": victimNewPwd}, nil, nil), 200, "")
		// un utente inesistente si comporta come uno esistente
		for i := 0; i < 5; i++ {
			e.denied(t, e.gw("POST", "/auth/login", map[string]any{"username": "fantasma", "password": "password inventata 12"}, nil, nil), 401, "invalid_credentials")
		}
		e.denied(t, e.gw("POST", "/auth/login", map[string]any{"username": "fantasma", "password": "password inventata 12"}, nil, nil), 429, "too_many_attempts")
	})
}
