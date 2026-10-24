//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// TestCiRunners_TokenDiRegistrazione (GIT-278): il token di registrazione esce solo dalla
// risposta 201 (solo amministratore), nel database c'è l'hash, nessuna GET lo restituisce,
// vale una volta sola; elenco e revoca dei runner sono solo dell'amministratore.
func TestCiRunners_TokenDiRegistrazione(t *testing.T) {
	e := newReposEnv(t, httpserver.CloneConfig{PublicURL: "https://git.example.com", SSHPort: 2222})
	e.id.sysAdmin[carolID] = true
	ctx := context.Background()

	t.Run("non_admin_403", func(t *testing.T) {
		for _, u := range []string{"alice", "bob"} {
			for _, c := range [][2]string{
				{http.MethodPost, "/admin/ci/runners/registration-token"},
				{http.MethodGet, "/admin/ci/runners"},
				{http.MethodDelete, "/admin/ci/runners/" + "00000000-0000-4000-8000-000000000001"},
			} {
				if rec := e.do(c[0], c[1], u, ""); rec.Code != http.StatusForbidden {
					t.Errorf("%s %s %s: %d %s", u, c[0], c[1], rec.Code, rec.Body.String())
				}
			}
		}
	})

	rec := e.do(http.MethodPost, "/admin/ci/runners/registration-token", "carol", "")
	e.want(rec, http.StatusCreated)
	var created openapi.CiRunnerRegistrationToken
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Token, ci.RegistrationTokenPrefix) || len(created.Token) < 40 {
		t.Fatalf("token: %q", created.Token)
	}
	if d := time.Until(created.ExpiresAt); d < 30*time.Minute || d > 2*time.Hour {
		t.Errorf("scadenza fra %v", d)
	}

	t.Run("nel_database_solo_l_hash", func(t *testing.T) {
		var n int
		if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM core.ci_runner_registration_tokens t
			WHERE position($1 in row_to_json(t)::text) > 0`, created.Token).Scan(&n); err != nil || n != 0 {
			t.Fatalf("token in chiaro: %d %v", n, err)
		}
		var rows int
		if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM core.ci_runner_registration_tokens WHERE consumed_at IS NULL AND octet_length(token_hash) = 32`).Scan(&rows); err != nil || rows != 1 {
			t.Fatalf("righe: %d %v", rows, err)
		}
	})

	t.Run("nessuna_GET_lo_restituisce", func(t *testing.T) {
		for _, p := range []string{"/admin/ci/runners", "/admin/ci/queue", "/admin/system", "/admin/ci/runners/registration-token"} {
			rec := e.do(http.MethodGet, p, "carol", "")
			if strings.Contains(rec.Body.String(), created.Token) || strings.Contains(rec.Body.String(), ci.RegistrationTokenPrefix) {
				t.Errorf("GET %s espone il token: %s", p, rec.Body.String())
			}
		}
	})

	runner, runnerToken, err := ci.RegisterRunner(ctx, e.pool, time.Now(), created.Token, "build-box", "t1", []string{"linux"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ci.RegisterRunner(ctx, e.pool, time.Now(), created.Token, "altro", "t1", nil); !errors.Is(err, ci.ErrRegistrationToken) {
		t.Fatalf("secondo uso del token: %v", err)
	}

	t.Run("elenco_senza_token", func(t *testing.T) {
		rec := e.do(http.MethodGet, "/admin/ci/runners", "carol", "")
		e.want(rec, http.StatusOK)
		for _, secret := range []string{created.Token, runnerToken} {
			if strings.Contains(rec.Body.String(), secret) {
				t.Fatalf("l'elenco espone un token: %s", rec.Body.String())
			}
		}
		var out openapi.CiRunnerList
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Items) != 1 || out.Items[0].Name != "build-box" || out.Items[0].Status != "offline" ||
			len(out.Items[0].Labels) != 2 || out.Items[0].Labels[0] != "self-hosted" || out.Items[0].Labels[1] != "linux" {
			t.Fatalf("elenco: %+v", out.Items)
		}
		// Dopo una FetchTask il runner è online.
		if _, err := ci.ResolveRunner(ctx, e.pool, time.Now(), runnerToken); err != nil {
			t.Fatal(err)
		}
		rec = e.do(http.MethodGet, "/admin/ci/runners", "carol", "")
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out.Items[0].Status != "online" {
			t.Errorf("stato: %s", out.Items[0].Status)
		}
	})

	t.Run("revoca", func(t *testing.T) {
		path := "/admin/ci/runners/" + runner.ID.String()
		e.want(e.do(http.MethodDelete, path, "carol", ""), http.StatusNoContent)
		e.want(e.do(http.MethodDelete, path, "carol", ""), http.StatusNotFound)
		if _, err := ci.ResolveRunner(ctx, e.pool, time.Now(), runnerToken); !errors.Is(err, ci.ErrRunnerToken) {
			t.Errorf("token del runner dopo la revoca: %v", err)
		}
		rec := e.do(http.MethodGet, "/admin/ci/runners", "carol", "")
		var out openapi.CiRunnerList
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if len(out.Items) != 0 {
			t.Errorf("elenco dopo la revoca: %+v", out.Items)
		}
	})
}
