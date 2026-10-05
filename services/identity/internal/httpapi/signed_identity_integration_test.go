//go:build integration

package httpapi_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/trust"
)

// signedHdr costruisce gli header che il gateway inoltra a identity per
// una richiesta con token.
func signedHdr(sec string, id trust.Identity, at time.Time) []string {
	h := http.Header{}
	trust.Sign(h, sec, id, at)
	var out []string
	for _, n := range []string{trust.HeaderUserID, trust.HeaderUsername, trust.HeaderScopes, trust.HeaderTimestamp, trust.HeaderSignature, trust.HeaderTokenID, trust.HeaderTokenName} {
		out = append(out, n, h.Get(n))
	}
	return out
}

// Un token personale non ha il cookie: identity accetta l'identità firmata
// dal gateway (stesso flusso: /internal/verify → header firmati → rotta).
func TestSignedIdentityAcceptedWithoutCookie(t *testing.T) {
	var logs bytes.Buffer
	e := newFullEnv(t, &logs, secret)
	e.mk("alice", false)
	ck := e.mustLogin("alice")
	exp := e.clock.now().Add(48 * time.Hour).Format(time.RFC3339)
	r := e.do("POST", "/user/tokens", map[string]any{"name": "ci", "scopes": []string{"read:user"}, "expiresAt": exp}, ck)
	status(t, r, 201)
	plain, _ := r.json()["token"].(string)

	// Il gateway verifica la credenziale e ne inoltra l'identità.
	v := e.do("POST", "/internal/verify", map[string]any{"credential": plain}, nil, bearer(secret)...)
	status(t, v, 200)
	p, _ := v.json()["principal"].(map[string]any)
	if v.json()["active"] != true || p["username"] != "alice" {
		t.Fatalf("verify: %s", v.body)
	}
	id := trust.Identity{UserID: p["userId"].(string), Username: "alice", Scopes: []string{"read:user"}}
	now := time.Now()

	l := e.do("GET", "/users", nil, nil, signedHdr(secret, id, now)...)
	status(t, l, 200)
	if !strings.Contains(string(l.body), "alice") {
		t.Fatalf("elenco utenti: %s", l.body)
	}

	// Il cookie, se c'è, ha la precedenza e resta l'unico modo per logout.
	errCode(t, e.do("POST", "/auth/logout", nil, nil, signedHdr(secret, id, now)...), 401, "unauthenticated")
}

func TestSignedIdentityRejected(t *testing.T) {
	var logs bytes.Buffer
	e := newFullEnv(t, &logs, secret)
	e.mk("alice", false)
	uid := e.userID("alice")
	id := trust.Identity{UserID: uid, Username: "alice", Scopes: []string{"read:user"}}
	now := time.Now()

	// Nessun header: 401.
	errCode(t, e.do("GET", "/users", nil, nil), 401, "unauthenticated")
	// Firma con un altro segreto.
	errCode(t, e.do("GET", "/users", nil, nil, signedHdr("altro-segreto", id, now)...), 401, "unauthenticated")
	// Firma scaduta.
	errCode(t, e.do("GET", "/users", nil, nil, signedHdr(secret, id, now.Add(-2*trust.MaxSkew))...), 401, "unauthenticated")
	// Scope alterati dopo la firma.
	h := signedHdr(secret, id, now)
	for i := 0; i < len(h); i += 2 {
		if h[i] == trust.HeaderScopes {
			h[i+1] = "read:user,admin:org"
		}
	}
	errCode(t, e.do("GET", "/users", nil, nil, h...), 401, "unauthenticated")
	// Utente inesistente ma firma valida.
	ghost := trust.Identity{UserID: "99999999-9999-9999-9999-999999999999", Username: "ghost"}
	errCode(t, e.do("GET", "/users", nil, nil, signedHdr(secret, ghost, now)...), 401, "unauthenticated")
	// Firma valida ma il segreto di identity non è configurato.
	e2 := newFullEnv(t, &logs, "")
	e2.mk("alice", false)
	errCode(t, e2.do("GET", "/users", nil, nil, signedHdr("", trust.Identity{UserID: e2.userID("alice"), Username: "alice"}, now)...), 401, "unauthenticated")
}

func (e *env) userID(name string) string {
	e.t.Helper()
	u, err := e.users.Get(context.Background(), name)
	if err != nil {
		e.t.Fatal(err)
	}
	return u.ID.String()
}
