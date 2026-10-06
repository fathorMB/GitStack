//go:build integration

package httpapi_test

import (
	"bytes"
	"testing"
)

func TestResolveMentionsOverHTTP(t *testing.T) {
	var logs bytes.Buffer
	e := newFullEnv(t, &logs, secret)
	e.mk("alice", false)
	body := map[string]any{"names": []string{"Alice", "nessuno", "acme/devs"}}
	// Solo il servizio: senza segreto 401.
	errCode(t, e.do("POST", "/internal/mentions/resolve", body, nil), 401, "unauthenticated")

	r := e.do("POST", "/internal/mentions/resolve", body, nil, bearer(secret)...)
	status(t, r, 200)
	users, _ := r.json()["users"].([]any)
	teams, _ := r.json()["teams"].([]any)
	if len(users) != 1 || len(teams) != 0 {
		t.Fatalf("attesto solo alice: %s", r.body)
	}
	u, _ := users[0].(map[string]any)
	if u["name"] != "Alice" || u["username"] != "alice" || u["kind"] != "human" || u["id"] != e.userID("alice") {
		t.Fatalf("utente: %s", r.body)
	}

	errCode(t, e.do("POST", "/internal/mentions/resolve", map[string]any{"names": []string{}}, nil, bearer(secret)...), 400, "bad_request")
}
