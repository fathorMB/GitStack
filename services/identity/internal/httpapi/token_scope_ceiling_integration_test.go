//go:build integration

package httpapi_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/trust"
)

// F1 (ASVS V4.1.3, GIT-219): un token personale non crea token con scope che
// il token chiamante non concede. La sessione del browser mantiene gli scope
// pieni.
func TestCreateToken_ScopesNotBeyondCaller(t *testing.T) {
	var logs bytes.Buffer
	e := newFullEnv(t, &logs, secret)
	e.mk("alice", false)
	ck := e.mustLogin("alice")
	exp := e.clock.now().Add(48 * time.Hour).Format(time.RFC3339)
	now := time.Now()

	// Sessione: scope pieni, anche admin:org.
	r := e.do("POST", "/user/tokens", map[string]any{"name": "pieno", "scopes": []string{"admin:org", "write:resource"}, "expiresAt": exp}, ck)
	status(t, r, 201)

	// Token con il solo write:user (che implica read:user).
	id := trust.Identity{UserID: e.userID("alice"), Username: "alice", Scopes: []string{"write:user"}, TokenID: "00000000-0000-0000-0000-0000000000a1", TokenName: "ristretto"}
	hdr := signedHdr(secret, id, now)

	r = e.do("POST", "/user/tokens", map[string]any{"name": "scalata", "scopes": []string{"admin:org", "write:resource"}, "expiresAt": exp}, nil, hdr...)
	errCode(t, r, 403, "insufficient_scope")
	status(t, e.do("POST", "/user/tokens", map[string]any{"name": "scalata2", "scopes": []string{"write:user", "admin:org"}, "expiresAt": exp}, nil, hdr...), 403)

	// Scope concessi (anche per implicazione): ok.
	status(t, e.do("POST", "/user/tokens", map[string]any{"name": "uguale", "scopes": []string{"write:user"}, "expiresAt": exp}, nil, hdr...), 201)
	status(t, e.do("POST", "/user/tokens", map[string]any{"name": "meno", "scopes": []string{"read:user"}, "expiresAt": exp}, nil, hdr...), 201)

	// Fuori catalogo: resta il 422 della validazione.
	errCode(t, e.do("POST", "/user/tokens", map[string]any{"name": "x", "scopes": []string{"root"}, "expiresAt": exp}, nil, hdr...), 422, "validation_failed")

	// Nessun token è stato creato dalle richieste rifiutate.
	l := e.do("GET", "/user/tokens?perPage=50", nil, ck)
	status(t, l, 200)
	if total, _ := l.json()["total"].(float64); total != 3 {
		t.Fatalf("token dell'utente = %v, voluti 3: %s", total, l.body)
	}
}

// Stessa regola per createUserToken: l'admin con un token ristretto non crea
// token di un agent con scope che il suo token non ha; con la sessione sì.
func TestCreateAgentToken_ScopesNotBeyondCaller(t *testing.T) {
	var logs bytes.Buffer
	e := newFullEnv(t, &logs, secret)
	e.mk("root", true)
	root := e.mustLogin("root")
	status(t, e.do("POST", "/users", map[string]any{"username": "bot", "kind": "agent"}, root), 201)
	exp := e.clock.now().Add(48 * time.Hour).Format(time.RFC3339)
	now := time.Now()

	// Sessione admin: scope pieni.
	status(t, e.do("POST", "/users/bot/tokens", map[string]any{"name": "pieno", "scopes": []string{"admin:org", "write:resource"}, "expiresAt": exp}, root), 201)

	id := trust.Identity{UserID: e.userID("root"), Username: "root", Scopes: []string{"write:user"}, TokenID: "00000000-0000-0000-0000-0000000000a2", TokenName: "ristretto"}
	hdr := signedHdr(secret, id, now)

	errCode(t, e.do("POST", "/users/bot/tokens", map[string]any{"name": "scalata", "scopes": []string{"admin:org", "write:resource"}, "expiresAt": exp}, nil, hdr...), 403, "insufficient_scope")
	status(t, e.do("POST", "/users/bot/tokens", map[string]any{"name": "ok", "scopes": []string{"write:user", "read:user"}, "expiresAt": exp}, nil, hdr...), 201)

	l := e.do("GET", "/users/bot/tokens?perPage=50", nil, root)
	status(t, l, 200)
	if total, _ := l.json()["total"].(float64); total != 2 {
		t.Fatalf("token dell'agent = %v, voluti 2: %s", total, l.body)
	}
}
