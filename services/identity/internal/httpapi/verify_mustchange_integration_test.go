//go:build integration

package httpapi_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/fathorMB/GitStack/services/identity/internal/users"
)

// /internal/verify per una sessione con password da cambiare risponde
// active=true con principal.mustChangePassword=true (è il gateway a
// rispondere 403 password_change_required); dopo il cambio è false.
func TestVerifyMustChangePassword(t *testing.T) {
	var logs bytes.Buffer
	e := newFullEnv(t, &logs, secret)
	if created, err := e.users.BootstrapAdmin(context.Background(), users.BootstrapAdminInput{Username: "admin", Password: initialPw}); err != nil || !created {
		t.Fatalf("bootstrap: %v %v", created, err)
	}
	ck, r := e.login("admin", initialPw)
	if ck == nil {
		t.Fatalf("login: %d %s", r.StatusCode, r.body)
	}

	v := e.do("POST", "/internal/verify", map[string]any{"credential": ck.Value}, nil, bearer(secret)...)
	status(t, v, 200)
	p, _ := v.json()["principal"].(map[string]any)
	if v.json()["active"] != true || p["mustChangePassword"] != true || p["username"] != "admin" {
		t.Fatalf("verify prima del cambio: %s", v.body)
	}

	status(t, e.do("PUT", "/users/admin/password", map[string]any{"currentPassword": initialPw, "newPassword": "nuova password molto lunga"}, ck), 204)
	v = e.do("POST", "/internal/verify", map[string]any{"credential": ck.Value}, nil, bearer(secret)...)
	status(t, v, 200)
	p, _ = v.json()["principal"].(map[string]any)
	if v.json()["active"] != true || p["mustChangePassword"] != false {
		t.Fatalf("verify dopo il cambio: %s", v.body)
	}
}
