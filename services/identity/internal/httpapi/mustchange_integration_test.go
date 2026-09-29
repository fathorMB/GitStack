//go:build integration

package httpapi_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/fathorMB/GitStack/services/identity/internal/httpapi"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
)

const initialPw = "password-iniziale-generata-1234"

// Con must_change=true (admin appena creato dal bootstrap) ogni chiamata
// autenticata risponde 403 password_change_required, tranne GET della
// sessione corrente, logout e cambio della propria password.
func TestMustChangePasswordEnforcement(t *testing.T) {
	e := newEnv(t, cfg)
	ctx := context.Background()
	if created, err := e.users.BootstrapAdmin(ctx, users.BootstrapAdminInput{Username: "admin", Password: initialPw}); err != nil || !created {
		t.Fatalf("bootstrap: %v %v", created, err)
	}
	e.mk("bob", false)

	ck, r := e.login("admin", initialPw)
	if ck == nil {
		t.Fatalf("login: %d %s", r.StatusCode, r.body)
	}
	if r.json()["mustChangePassword"] != true {
		t.Fatalf("login: mustChangePassword atteso true: %s", r.body)
	}

	blocked := []struct {
		name, method, path string
		body               any
	}{
		{"elenco utenti", "GET", "/users", nil},
		{"crea utente", "POST", "/users", map[string]any{"username": "carol", "password": pw}},
		{"leggi utente", "GET", "/users/bob", nil},
		{"aggiorna utente", "PATCH", "/users/admin", map[string]any{"displayName": "X"}},
		{"elimina utente", "DELETE", "/users/bob", nil},
		{"cambia password di un altro", "PUT", "/users/bob/password", map[string]any{"newPassword": "nuova password molto lunga"}},
	}
	for _, c := range blocked {
		t.Run("bloccata/"+c.name, func(t *testing.T) {
			errCode(t, e.do(c.method, c.path, c.body, ck), http.StatusForbidden, httpapi.CodePasswordChangeRequired)
		})
	}
	// Senza sessione resta 401, non 403.
	errCode(t, e.do("GET", "/users", nil, nil), http.StatusUnauthorized, "unauthenticated")

	// Sessione corrente: consentita e dice che la password va cambiata.
	r = e.do("GET", "/auth/session", nil, ck)
	status(t, r, http.StatusOK)
	if r.json()["mustChangePassword"] != true {
		t.Fatalf("session: mustChangePassword atteso true: %s", r.body)
	}

	// Password attuale sbagliata: il cambio non passa e lo stato resta.
	errCode(t, e.do("PUT", "/users/admin/password", map[string]any{"currentPassword": "sbagliata sbagliata", "newPassword": "nuova password molto lunga"}, ck), http.StatusForbidden, "forbidden")
	errCode(t, e.do("GET", "/users", nil, ck), http.StatusForbidden, httpapi.CodePasswordChangeRequired)

	// Cambio della propria password: consentito, poi tutto si sblocca.
	status(t, e.do("PUT", "/users/admin/password", map[string]any{"currentPassword": initialPw, "newPassword": "nuova password molto lunga"}, ck), http.StatusNoContent)
	r = e.do("GET", "/auth/session", nil, ck)
	status(t, r, http.StatusOK)
	if r.json()["mustChangePassword"] != false {
		t.Fatalf("session dopo il cambio: mustChangePassword atteso false: %s", r.body)
	}
	status(t, e.do("GET", "/users", nil, ck), http.StatusOK)
	status(t, e.do("GET", "/users/bob", nil, ck), http.StatusOK)
}

func TestMustChangePasswordLogoutAllowed(t *testing.T) {
	e := newEnv(t, cfg)
	if _, err := e.users.BootstrapAdmin(context.Background(), users.BootstrapAdminInput{Username: "admin", Password: initialPw}); err != nil {
		t.Fatal(err)
	}
	ck, r := e.login("admin", initialPw)
	if ck == nil {
		t.Fatalf("login: %d %s", r.StatusCode, r.body)
	}
	status(t, e.do("POST", "/auth/logout", nil, ck), http.StatusNoContent)
	errCode(t, e.do("GET", "/auth/session", nil, ck), http.StatusUnauthorized, "unauthenticated")
}

// Un utente normale ha mustChangePassword=false.
func TestNormalUserMustChangeFalse(t *testing.T) {
	e := newEnv(t, cfg)
	e.mk("bob", false)
	r := e.do("GET", "/auth/session", nil, e.mustLogin("bob"))
	status(t, r, http.StatusOK)
	if r.json()["mustChangePassword"] != false {
		t.Fatalf("atteso false: %s", r.body)
	}
}
