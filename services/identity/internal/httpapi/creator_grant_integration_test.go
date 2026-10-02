//go:build integration

package httpapi_test

import (
	"testing"

	"github.com/google/uuid"
)

func TestGrantResourceCreator(t *testing.T) {
	e := newPermEnv(t)
	e.mk("alice", false)
	e.mk("ghost", false)
	alice := e.userID("alice")
	res := uuid.NewString()
	path := "/internal/resources/" + res + "/grants/creator"
	call := func(user string, hdr ...string) resp {
		return e.do("POST", path, map[string]any{"userId": user}, nil, hdr...)
	}

	// Serve il segreto di servizio.
	errCode(t, call(alice), 401, "unauthenticated")
	errCode(t, call(alice, bearer("sbagliato")...), 401, "unauthenticated")
	// Corpo non valido.
	errCode(t, e.do("POST", path, map[string]any{"userId": "x"}, nil, bearer(secret)...), 400, "bad_request")

	// 201: grant admin, granted_by = l'utente stesso.
	r := call(alice, bearer(secret)...)
	status(t, r, 201)
	if j := r.json(); j["role"] != "admin" || j["subjectType"] != "user" || j["subjectId"] != alice || j["resourceId"] != res {
		t.Fatalf("grant inatteso: %s", r.body)
	}
	var by string
	if err := e.pool.QueryRow(t.Context(), `SELECT granted_by::text FROM identity.resource_grants WHERE resource_id = $1`, res).Scan(&by); err != nil || by != alice {
		t.Fatalf("granted_by = %q, %v; voluto %s", by, err, alice)
	}
	if got := e.check(alice, res, "admin"); got != true {
		t.Fatalf("il creatore deve avere admin effettivo")
	}

	// Ripetuto: 200, ancora un solo grant.
	status(t, call(alice, bearer(secret)...), 200)
	var n int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM identity.resource_grants WHERE resource_id = $1`, res).Scan(&n); err != nil || n != 1 {
		t.Fatalf("grant = %d, %v; voluto 1", n, err)
	}

	// Un grant preesistente più basso viene portato ad admin (200).
	res2 := uuid.New()
	e.sql(`INSERT INTO identity.resource_grants (id, resource_id, user_id, role) VALUES ($1, $2, $3, 'read')`, uuid.New(), res2, alice)
	r = e.do("POST", "/internal/resources/"+res2.String()+"/grants/creator", map[string]any{"userId": alice}, nil, bearer(secret)...)
	status(t, r, 200)
	if r.json()["role"] != "admin" {
		t.Fatalf("ruolo = %v, voluto admin", r.json()["role"])
	}

	// Utente inesistente: 404. Utente disattivato: 404.
	errCode(t, call(uuid.NewString(), bearer(secret)...), 404, "not_found")
	e.sql(`UPDATE identity.users SET is_active = false WHERE username = 'ghost'`)
	errCode(t, call(e.userID("ghost"), bearer(secret)...), 404, "not_found")
}

func (e *permEnv) check(user, res, role string) bool {
	e.t.Helper()
	r := e.do("POST", "/internal/permissions/check", map[string]any{"userId": user, "resourceId": res, "role": role}, nil, bearer(secret)...)
	status(e.t, r, 200)
	allowed, _ := r.json()["allowed"].(bool)
	return allowed
}
