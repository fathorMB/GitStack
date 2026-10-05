//go:build integration

package httpapi_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"
)

// verify chiama /internal/verify come fa il gateway e ritorna active e username.
func verify(t *testing.T, e *env, plain string) (bool, string) {
	t.Helper()
	v := e.do("POST", "/internal/verify", map[string]any{"credential": plain}, nil, bearer(secret)...)
	status(t, v, 200)
	j := v.json()
	p, _ := j["principal"].(map[string]any)
	name, _ := p["username"].(string)
	return j["active"] == true, name
}

func TestAgentTokensManagedByAdmin(t *testing.T) {
	var logs bytes.Buffer
	e := newFullEnv(t, &logs, secret)
	e.mk("root", true)
	e.mk("alice", false)
	root, alice := e.mustLogin("root"), e.mustLogin("alice")

	// P5: solo l'admin crea utenti agent.
	agentBody := map[string]any{"username": "bot", "kind": "agent"}
	errCode(t, e.do("POST", "/users", agentBody, alice), 403, "forbidden")
	status(t, e.do("POST", "/users", agentBody, root), 201)

	exp := e.clock.now().Add(48 * time.Hour).Format(time.RFC3339)
	body := map[string]any{"name": "ci", "scopes": []string{"read:user", "write:org"}, "expiresAt": exp}

	// Non admin: 403, anche sul proprio utente e su un agent esistente.
	errCode(t, e.do("POST", "/users/bot/tokens", body, alice), 403, "forbidden")
	errCode(t, e.do("GET", "/users/bot/tokens", nil, alice), 403, "forbidden")
	errCode(t, e.do("POST", "/users/alice/tokens", body, alice), 403, "forbidden")
	errCode(t, e.do("POST", "/users/bot/tokens", body, nil), 401, "unauthenticated")

	// Utente inesistente: 404. Utente umano: 409, anche l'admin su se stesso.
	errCode(t, e.do("POST", "/users/nessuno/tokens", body, root), 404, "not_found")
	errCode(t, e.do("POST", "/users/alice/tokens", body, root), 409, "not_an_agent")
	errCode(t, e.do("GET", "/users/alice/tokens", nil, root), 409, "not_an_agent")
	errCode(t, e.do("POST", "/users/root/tokens", body, root), 409, "not_an_agent")
	errCode(t, e.do("DELETE", "/users/alice/tokens/"+"00000000-0000-0000-0000-000000000000", nil, root), 409, "not_an_agent")
	// Il 409 viene prima della validazione del corpo.
	errCode(t, e.do("POST", "/users/alice/tokens", map[string]any{"name": ""}, root), 409, "not_an_agent")

	// Creazione: valore mostrato una sola volta, Location sotto l'agent.
	r := e.do("POST", "/users/bot/tokens", body, root)
	status(t, r, 201)
	created := r.json()
	plain, _ := created["token"].(string)
	id, _ := created["id"].(string)
	if !strings.HasPrefix(plain, "gst_") || created["hint"] != plain[len(plain)-4:] {
		t.Fatalf("risposta di creazione: %s", r.body)
	}
	if loc := r.Header.Get("Location"); loc != "/v1/users/bot/tokens/"+id {
		t.Fatalf("Location %q", loc)
	}

	// Validazione (422) con le stesse regole dei token personali.
	errCode(t, e.do("POST", "/users/bot/tokens", map[string]any{"name": "x", "scopes": []string{"read:user"}}, root), 422, "validation_failed")
	errCode(t, e.do("POST", "/users/bot/tokens", map[string]any{"name": "x", "scopes": []string{"root"}, "expiresAt": exp}, root), 422, "validation_failed")
	far := e.clock.now().Add(90 * 24 * time.Hour).Format(time.RFC3339)
	errCode(t, e.do("POST", "/users/bot/tokens", map[string]any{"name": "lungo", "scopes": []string{"read:user"}, "expiresAt": far}, root), 422, "validation_failed")
	errCode(t, e.do("POST", "/users/bot/tokens", body, root), 409, "already_exists")

	// Elenco: senza valore; i token dell'agent non finiscono in quelli dell'admin.
	l := e.do("GET", "/users/bot/tokens", nil, root)
	status(t, l, 200)
	if bytes.Contains(l.body, []byte(plain)) || bytes.Contains(l.body, []byte(`"token"`)) {
		t.Fatalf("il token in chiaro è nell'elenco: %s", l.body)
	}
	if items, _ := l.json()["items"].([]any); len(items) != 1 {
		t.Fatalf("elenco: %s", l.body)
	}
	own := e.do("GET", "/user/tokens", nil, root)
	status(t, own, 200)
	if items, _ := own.json()["items"].([]any); len(items) != 0 {
		t.Fatalf("token dell'admin: %s", own.body)
	}

	// Il token funziona come principal dell'agent (verifica del gateway).
	if ok, name := verify(t, e, plain); !ok || name != "bot" {
		t.Fatalf("verifica: active=%v username=%q", ok, name)
	}

	// Revoca: un token di un altro utente è 404 sotto l'agent.
	status(t, e.do("POST", "/users", map[string]any{"username": "bot2x", "kind": "agent"}, root), 201)
	r2 := e.do("POST", "/users/bot2x/tokens", body, root)
	status(t, r2, 201)
	id2 := r2.json()["id"].(string)
	errCode(t, e.do("DELETE", "/users/bot/tokens/"+id2, nil, root), 404, "not_found")
	errCode(t, e.do("DELETE", "/users/bot/tokens/"+id, nil, alice), 403, "forbidden")
	status(t, e.do("DELETE", "/users/bot/tokens/"+id, nil, root), 204)
	errCode(t, e.do("DELETE", "/users/bot/tokens/"+id, nil, root), 404, "not_found")
	if ok, _ := verify(t, e, plain); ok {
		t.Fatal("token revocato ancora attivo")
	}
	if strings.Contains(logs.String(), plain) {
		t.Fatal("il token in chiaro è nei log")
	}
}

func TestAgentDeactivatedOrDeletedLosesTokens(t *testing.T) {
	e := newFullEnv(t, new(bytes.Buffer), secret)
	e.mk("root", true)
	e.mk("alice", false)
	root, alice := e.mustLogin("root"), e.mustLogin("alice")
	exp := e.clock.now().Add(48 * time.Hour).Format(time.RFC3339)
	mkToken := func(agent string) string {
		t.Helper()
		status(t, e.do("POST", "/users", map[string]any{"username": agent, "kind": "agent"}, root), 201)
		r := e.do("POST", "/users/"+agent+"/tokens", map[string]any{"name": "t", "scopes": []string{"read:user"}, "expiresAt": exp}, root)
		status(t, r, 201)
		return r.json()["token"].(string)
	}

	// Disattivazione: verifica OK, poi non più attivo.
	tok := mkToken("bot-off")
	if ok, _ := verify(t, e, tok); !ok {
		t.Fatal("token dell'agent non attivo")
	}
	errCode(t, e.do("PATCH", "/users/bot-off", map[string]any{"isActive": false}, alice), 403, "forbidden")
	status(t, e.do("PATCH", "/users/bot-off", map[string]any{"isActive": false}, root), http.StatusOK)
	if ok, _ := verify(t, e, tok); ok {
		t.Fatal("agent disattivato: il token passa ancora la verifica")
	}

	// Eliminazione: solo l'admin; poi il token non passa più.
	tok = mkToken("bot-del")
	if ok, _ := verify(t, e, tok); !ok {
		t.Fatal("token dell'agent non attivo")
	}
	errCode(t, e.do("DELETE", "/users/bot-del", nil, alice), 403, "forbidden")
	if ok, _ := verify(t, e, tok); !ok {
		t.Fatal("un non admin non deve poter eliminare l'agent")
	}
	status(t, e.do("DELETE", "/users/bot-del", nil, root), 204)
	if ok, _ := verify(t, e, tok); ok {
		t.Fatal("agent eliminato: il token passa ancora la verifica")
	}
}
