//go:build integration

package httpapi_test

import (
	"bytes"
	"testing"
)

func TestLookupUsersByIdsOverHTTP(t *testing.T) {
	var logs bytes.Buffer
	e := newFullEnv(t, &logs, secret)
	e.mk("alice", false)
	e.mk("bob", false)
	aliceID := e.userID("alice")

	body := map[string]any{"ids": []string{aliceID, "11111111-1111-1111-1111-111111111111"}}
	// Solo il servizio: senza segreto 401.
	errCode(t, e.do("POST", "/internal/users/lookup-ids", body, nil), 401, "unauthenticated")

	r := e.do("POST", "/internal/users/lookup-ids", body, nil, bearer(secret)...)
	status(t, r, 200)
	users, _ := r.json()["users"].([]any)
	if len(users) != 1 {
		t.Fatalf("attesto solo l'utente noto: %s", r.body)
	}
	u, _ := users[0].(map[string]any)
	if u["id"] != aliceID || u["username"] != "alice" || u["kind"] != "human" {
		t.Fatalf("utente: %s", r.body)
	}
	// M-06/F (C5): core manda le notifiche email e la chiede qui; l'operazione è interna (segreto di servizio).
	if u["email"] != "alice@example.com" {
		t.Fatalf("manca l'email dell'utente: %s", r.body)
	}

	errCode(t, e.do("POST", "/internal/users/lookup-ids", map[string]any{"ids": []string{}}, nil, bearer(secret)...), 400, "bad_request")
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = "11111111-1111-1111-1111-111111111111"
	}
	errCode(t, e.do("POST", "/internal/users/lookup-ids", map[string]any{"ids": tooMany}, nil, bearer(secret)...), 400, "bad_request")
}
