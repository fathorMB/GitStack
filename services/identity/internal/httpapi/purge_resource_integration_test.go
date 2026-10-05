//go:build integration

package httpapi_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// DELETE /internal/resources/{id} (M-03/F): toglie grant e attributi di una
// risorsa cancellata, solo con il segreto di servizio, ed è idempotente.
func TestPurgeResourceAccess(t *testing.T) {
	e := newOwnerEnv(t)
	e.mk("alice", false)
	e.mk("bob", false)
	res, other := uuid.New(), uuid.New()
	for _, r := range []uuid.UUID{res, other} {
		status(t, e.setAttrs(r, "user", e.userID("alice"), "internal"), 204)
		status(t, e.do("POST", "/internal/resources/"+r.String()+"/grants/creator", map[string]any{"userId": e.userID("bob")}, nil, bearer(secret)...), 201)
	}
	path := "/internal/resources/" + res.String()

	errCode(t, e.do("DELETE", path, nil, nil), 401, "unauthenticated")
	errCode(t, e.do("DELETE", path, nil, nil, bearer("sbagliato")...), 401, "unauthenticated")
	if ok, _ := e.effective(e.userID("bob"), res, "admin"); !ok {
		t.Fatal("senza segreto i grant non dovevano sparire")
	}

	status(t, e.do("DELETE", path, nil, nil, bearer(secret)...), 204)
	status(t, e.do("DELETE", path, nil, nil, bearer(secret)...), 204) // idempotente

	count := func(q string, id uuid.UUID) int {
		var n int
		if err := e.pool.QueryRow(context.Background(), q, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM identity.resource_grants WHERE resource_id = $1`, res); n != 0 {
		t.Fatalf("grant rimasti: %d", n)
	}
	if n := count(`SELECT count(*) FROM identity.resource_attributes WHERE resource_id = $1`, res); n != 0 {
		t.Fatalf("attributi rimasti: %d", n)
	}
	if ok, _ := e.effective(e.userID("bob"), res, "read"); ok {
		t.Fatal("bob ha ancora accesso alla risorsa cancellata")
	}
	// L'altra risorsa non è toccata.
	if ok, _ := e.effective(e.userID("bob"), other, "admin"); !ok {
		t.Fatal("la pulizia ha toccato un'altra risorsa")
	}
	if n := count(`SELECT count(*) FROM identity.resource_attributes WHERE resource_id = $1`, other); n != 1 {
		t.Fatalf("attributi dell'altra risorsa: %d", n)
	}
}
