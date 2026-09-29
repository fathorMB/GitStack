//go:build integration

package httpapi_test

import (
	"bytes"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/apitokens"
	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/dbtest"
	"github.com/fathorMB/GitStack/services/identity/internal/httpapi"
	"github.com/fathorMB/GitStack/services/identity/internal/loginlimit"
	"github.com/fathorMB/GitStack/services/identity/internal/sessions"
	"github.com/fathorMB/GitStack/services/identity/internal/userkeys"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
)

const secret = "segreto-di-servizio-di-prova"

func newFullEnv(t *testing.T, logs *bytes.Buffer, svcSecret string) *env {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	c := &clock{t: time.Now().UTC().Truncate(time.Microsecond)}
	us := users.New(pool, c.now)
	svc := &auth.Service{Users: us, Sessions: sessions.New(pool, c.now, time.Hour), Limiter: loginlimit.New(cfg, c.now)}
	lg := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := httpapi.New(svc, lg,
		httpapi.WithTokens(apitokens.New(pool, c.now, 30*24*time.Hour)),
		httpapi.WithSSHKeys(userkeys.New(pool, c.now)),
		httpapi.WithServiceSecret(svcSecret))
	return &env{t: t, h: h, clock: c, users: us}
}

func pubKey(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../sshkeys/testdata/" + name + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func bearer(s string) []string { return []string{"Authorization", "Bearer " + s} }

func TestTokensLifecycleOverHTTP(t *testing.T) {
	var logs bytes.Buffer
	e := newFullEnv(t, &logs, secret)
	e.mk("alice", false)
	ck := e.mustLogin("alice")

	// Senza sessione: 401.
	errCode(t, e.do("GET", "/user/tokens", nil, nil), 401, "unauthenticated")

	exp := e.clock.now().Add(48 * time.Hour).Format(time.RFC3339)
	r := e.do("POST", "/user/tokens", map[string]any{"name": "ci", "scopes": []string{"read:user", "write:org"}, "expiresAt": exp}, ck)
	status(t, r, 201)
	created := r.json()
	plain, _ := created["token"].(string)
	if !strings.HasPrefix(plain, "gst_") || created["hint"] != plain[len(plain)-4:] {
		t.Fatalf("risposta di creazione: %s", r.body)
	}
	id, _ := created["id"].(string)
	if loc := r.Header.Get("Location"); loc != "/v1/user/tokens/"+id {
		t.Fatalf("Location %q", loc)
	}

	// Il valore in chiaro non compare altrove: né nell'elenco né nei log.
	l := e.do("GET", "/user/tokens?perPage=10", nil, ck)
	status(t, l, 200)
	if bytes.Contains(l.body, []byte(plain)) || bytes.Contains(l.body, []byte(`"token"`)) {
		t.Fatalf("il token in chiaro è nell'elenco: %s", l.body)
	}
	if items, _ := l.json()["items"].([]any); len(items) != 1 {
		t.Fatalf("elenco: %s", l.body)
	}

	// Errori: scadenza mancante, scope sconosciuto, nome duplicato.
	r = e.do("POST", "/user/tokens", map[string]any{"name": "x", "scopes": []string{"read:user"}}, ck)
	errCode(t, r, 422, "validation_failed")
	if bytes.Contains(r.body, []byte(plain)) {
		t.Fatal("token nell'errore")
	}
	errCode(t, e.do("POST", "/user/tokens", map[string]any{"name": "x", "scopes": []string{"root"}, "expiresAt": exp}, ck), 422, "validation_failed")
	errCode(t, e.do("POST", "/user/tokens", map[string]any{"name": "ci", "scopes": []string{"read:user"}, "expiresAt": exp}, ck), 409, "already_exists")
	far := e.clock.now().Add(90 * 24 * time.Hour).Format(time.RFC3339)
	errCode(t, e.do("POST", "/user/tokens", map[string]any{"name": "lungo", "scopes": []string{"read:user"}, "expiresAt": far}, ck), 422, "validation_failed")

	// Verifica interna: attivo, con scope e principal.
	v := e.do("POST", "/internal/verify", map[string]any{"credential": plain}, nil, bearer(secret)...)
	status(t, v, 200)
	vj := v.json()
	p, _ := vj["principal"].(map[string]any)
	if vj["active"] != true || p["username"] != "alice" || p["authMethod"] != "token" || p["credentialId"] != id {
		t.Fatalf("verify: %s", v.body)
	}
	if sc, _ := p["scopes"].([]any); len(sc) != 2 {
		t.Fatalf("scope: %s", v.body)
	}

	// Revoca (204) -> non più attivo, senza distinzione.
	status(t, e.do("DELETE", "/user/tokens/"+id, nil, ck), 204)
	errCode(t, e.do("DELETE", "/user/tokens/"+id, nil, ck), 404, "not_found")
	v = e.do("POST", "/internal/verify", map[string]any{"credential": plain}, nil, bearer(secret)...)
	status(t, v, 200)
	if vj := v.json(); vj["active"] != false || vj["principal"] != nil {
		t.Fatalf("revocato: %s", v.body)
	}

	// Scaduto: nuovo token, si avanza l'orologio oltre la scadenza.
	r = e.do("POST", "/user/tokens", map[string]any{"name": "breve", "scopes": []string{"read:user"}, "expiresAt": e.clock.now().Add(time.Hour).Format(time.RFC3339)}, ck)
	status(t, r, 201)
	short := r.json()["token"].(string)
	e.clock.advance(30 * time.Minute)
	status(t, e.do("POST", "/internal/verify", map[string]any{"credential": short}, nil, bearer(secret)...), 200)
	e.clock.advance(31 * time.Minute)
	v = e.do("POST", "/internal/verify", map[string]any{"credential": short}, nil, bearer(secret)...)
	if v.json()["active"] != false {
		t.Fatalf("scaduto: %s", v.body)
	}

	for _, tok := range []string{plain, short} {
		if strings.Contains(logs.String(), tok) {
			t.Fatalf("il token in chiaro è nei log:\n%s", logs.String())
		}
	}
}

func TestVerifySessionAndServiceAuth(t *testing.T) {
	var logs bytes.Buffer
	e := newFullEnv(t, &logs, secret)
	e.mk("alice", false)
	ck := e.mustLogin("alice")
	body := map[string]any{"credential": ck.Value}

	status(t, e.do("POST", "/internal/verify", body, nil), 401)
	status(t, e.do("POST", "/internal/verify", body, nil, bearer("sbagliato")...), 401)
	status(t, e.do("POST", "/internal/verify", body, nil, "Authorization", "Basic "+secret), 401)
	status(t, e.do("POST", "/internal/verify", body, ck, bearer("sbagliato")...), 401)

	v := e.do("POST", "/internal/verify", body, nil, bearer(secret)...)
	status(t, v, 200)
	p, _ := v.json()["principal"].(map[string]any)
	if v.json()["active"] != true || p["authMethod"] != "password" || p["scopes"] != nil {
		t.Fatalf("sessione: %s", v.body)
	}
	v = e.do("POST", "/internal/verify", map[string]any{"credential": "sessione-inesistente"}, nil, bearer(secret)...)
	status(t, v, 200)
	if v.json()["active"] != false {
		t.Fatalf("sconosciuta: %s", v.body)
	}
	errCode(t, e.do("POST", "/internal/verify", map[string]any{"credential": ""}, nil, bearer(secret)...), 400, "bad_request")

	// L'endpoint interno delle chiavi è protetto allo stesso modo.
	status(t, e.do("GET", "/internal/ssh-keys/SHA256:"+strings.Repeat("A", 43), nil, nil), 401)
}

func TestEmptyServiceSecretRejectsAll(t *testing.T) {
	var logs bytes.Buffer
	e := newFullEnv(t, &logs, "")
	e.mk("alice", false)
	body := map[string]any{"credential": "x"}
	status(t, e.do("POST", "/internal/verify", body, nil), 401)
	status(t, e.do("POST", "/internal/verify", body, nil, "Authorization", "Bearer "), 401)
	status(t, e.do("POST", "/internal/verify", body, nil, bearer("")...), 401)
}

func TestSSHKeysOverHTTP(t *testing.T) {
	var logs bytes.Buffer
	e := newFullEnv(t, &logs, secret)
	e.mk("alice", false)
	e.mk("bob", false)
	a, b := e.mustLogin("alice"), e.mustLogin("bob")

	errCode(t, e.do("GET", "/user/ssh-keys", nil, nil), 401, "unauthenticated")

	r := e.do("POST", "/user/ssh-keys", map[string]any{"title": "laptop", "publicKey": pubKey(t, "ed25519")}, a)
	status(t, r, 201)
	k := r.json()
	id, _ := k["id"].(string)
	fp, _ := k["fingerprint"].(string)
	if k["keyType"] != "ssh-ed25519" || !strings.HasPrefix(fp, "SHA256:") || r.Header.Get("Location") != "/v1/user/ssh-keys/"+id {
		t.Fatalf("chiave: %s", r.body)
	}
	if _, has := k["publicKey"]; has {
		t.Fatal("la risposta non deve avere publicKey (non è nel contratto)")
	}

	// Chiave già registrata da un altro utente: 409 ssh_key_in_use.
	errCode(t, e.do("POST", "/user/ssh-keys", map[string]any{"title": "mia", "publicKey": pubKey(t, "ed25519")}, b), 409, "ssh_key_in_use")
	// Titolo duplicato dello stesso utente: 409 already_exists.
	errCode(t, e.do("POST", "/user/ssh-keys", map[string]any{"title": "laptop", "publicKey": pubKey(t, "ecdsa256")}, a), 409, "already_exists")
	// Chiave non valida o debole: 422.
	errCode(t, e.do("POST", "/user/ssh-keys", map[string]any{"title": "dsa", "publicKey": pubKey(t, "dsa")}, a), 422, "validation_failed")
	errCode(t, e.do("POST", "/user/ssh-keys", map[string]any{"title": "x", "publicKey": "boh"}, a), 422, "validation_failed")

	status(t, e.do("GET", "/user/ssh-keys/"+id, nil, a), 200)
	errCode(t, e.do("GET", "/user/ssh-keys/"+id, nil, b), 404, "not_found")
	errCode(t, e.do("DELETE", "/user/ssh-keys/"+id, nil, b), 404, "not_found")
	if items, _ := e.do("GET", "/user/ssh-keys", nil, a).json()["items"].([]any); len(items) != 1 {
		t.Fatal("elenco di alice")
	}
	if items, _ := e.do("GET", "/user/ssh-keys", nil, b).json()["items"].([]any); len(items) != 0 {
		t.Fatal("bob vede la chiave di alice")
	}

	// Lookup interno dal fingerprint.
	lk := e.do("GET", "/internal/ssh-keys/"+url.PathEscape(fp), nil, nil, bearer(secret)...)
	status(t, lk, 200)
	if u, _ := lk.json()["user"].(map[string]any); u["username"] != "alice" {
		t.Fatalf("lookup: %s", lk.body)
	}
	errCode(t, e.do("GET", "/internal/ssh-keys/SHA256:"+strings.Repeat("B", 43), nil, nil, bearer(secret)...), 404, "not_found")

	status(t, e.do("DELETE", "/user/ssh-keys/"+id, nil, a), 204)
	errCode(t, e.do("GET", "/internal/ssh-keys/"+url.PathEscape(fp), nil, nil, bearer(secret)...), 404, "not_found")
}
