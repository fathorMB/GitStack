//go:build integration

package httpserver_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// jobFixture crea un'esecuzione e un job in corso del repo, con i permessi
// indicati e un token valido; ritorna il token in chiaro.
func (e *releaseEnv) jobFixture(repo, token, permissions string, expires time.Time) {
	e.t.Helper()
	ctx := context.Background()
	var repoID uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT resource_id FROM core.repositories WHERE name=$1`, repo).Scan(&repoID); err != nil {
		e.t.Fatal(err)
	}
	run, job := uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO core.ci_runs (id, repo_id, owner_type, owner_id, workflow_path, event, ref, sha, group_key, status)
			VALUES ($1, $2, 'user', $3, '.github/workflows/release.yml', 'push', 'refs/tags/v1.0.0', repeat('a', 40), $4, 'in_progress')`, []any{run, repoID, uuid.New(), "g-" + run.String()}},
		{`INSERT INTO core.ci_jobs (id, run_id, seq, job_key, name, status, permissions) VALUES ($1, $2, 0, 'release', 'release', 'in_progress', $3::jsonb)`, []any{job, run, permissions}},
		{`INSERT INTO core.ci_job_tokens (job_id, token_hash, issued_at, expires_at) VALUES ($1, $2, now(), $3)`, []any{job, hash(token), expires}},
	} {
		if _, err := e.pool.Exec(ctx, q.sql, q.args...); err != nil {
			e.t.Fatalf("%s: %v", q.sql, err)
		}
	}
}

func hash(token string) []byte { h := sha256.Sum256([]byte(token)); return h[:] }

// asJob manda la richiesta come il gateway la inoltra per un token del job:
// identità «system:ci-job» firmata e token in X-Gitstack-Job-Token.
func (e *releaseEnv) asJob(method, path, token, body string, data []byte) *httptest.ResponseRecorder {
	e.t.Helper()
	var req *http.Request
	if data != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(data))
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	trust.Sign(req.Header, trustSecret, trust.Identity{UserID: uuid.Nil.String(), Username: trust.JobUsername, Scopes: []string{trust.JobScope}}, time.Now())
	if token != "" {
		req.Header.Set(trust.HeaderJobToken, token)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// GIT-372 (REL1): il token del job con contents: write crea la release del suo
// repo e ne carica un asset; senza permesso, su un altro repo o su una rotta che non
// è di release è rifiutato.
func TestReleases_TokenDelJob(t *testing.T) {
	e := newReleaseEnv(t)
	e.create("alice", `{"owner":"alice","name":"altro","visibility":"internal"}`)
	later := time.Now().Add(time.Hour)
	e.jobFixture("app", "gsj_scrive", `{"contents":"write","packages":"read","actions":"read"}`, later)
	e.jobFixture("app", "gsj_legge", `{"contents":"read","packages":"write","actions":"read"}`, later)
	e.jobFixture("app", "gsj_scaduto", `{"contents":"write","packages":"read","actions":"read"}`, time.Now().Add(-time.Minute))
	e.jobFixture("altro", "gsj_altro", `{"contents":"write","packages":"read","actions":"read"}`, later)

	// Positivo: 201 sulla release e 201 sull'asset, che poi un utente scarica.
	rec := e.asJob("POST", "/repos/alice/app/releases", "gsj_scrive", `{"tagName":"v1.0.0","name":"Versione 1"}`, nil)
	e.want(rec, http.StatusCreated)
	var rel openapi.Release
	if err := json.Unmarshal(rec.Body.Bytes(), &rel); err != nil || rel.TagName != "v1.0.0" || rel.Draft {
		t.Fatalf("release: %v %s", err, rec.Body.String())
	}
	data := []byte("artefatto del job\n")
	up := e.asJob("POST", "/repos/alice/app/releases/"+rel.Id.String()+"/assets?name="+url.QueryEscape("build.zip"), "gsj_scrive", "", data)
	e.want(up, http.StatusCreated)
	var asset openapi.ReleaseAsset
	if err := json.Unmarshal(up.Body.Bytes(), &asset); err != nil || asset.Name != "build.zip" || asset.SizeBytes != int64(len(data)) {
		t.Fatalf("asset: %v %s", err, up.Body.String())
	}
	dl := e.release("GET", "/repos/alice/app/releases/"+rel.Id.String()+"/assets/"+asset.Id.String()+"/download", "bob", "")
	e.want(dl, http.StatusOK)
	if !bytes.Equal(dl.Body.Bytes(), data) {
		t.Errorf("asset scaricato = %q", dl.Body.Bytes())
	}

	// Senza contents: write (read, anche con packages: write): 403, e nessuna release creata.
	e.want(e.asJob("POST", "/repos/alice/app/releases", "gsj_legge", `{"tagName":"v1.1.0","name":"No"}`, nil), http.StatusForbidden)
	e.want(e.asJob("POST", "/repos/alice/app/releases/"+rel.Id.String()+"/assets?name=x.zip", "gsj_legge", "", data), http.StatusForbidden)
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.releases`).Scan(&n); err != nil || n != 1 {
		t.Errorf("release in tabella = %d (%v), voluta 1", n, err)
	}

	// Un altro repo: 404 (come il registry, non rivela se esiste), con il token valido di un altro repo.
	e.want(e.asJob("POST", "/repos/alice/altro/releases", "gsj_scrive", `{"tagName":"v1.0.0","name":"X"}`, nil), http.StatusNotFound)
	e.want(e.asJob("POST", "/repos/alice/app/releases", "gsj_altro", `{"tagName":"v1.1.0","name":"X"}`, nil), http.StatusNotFound)
	e.want(e.asJob("POST", "/repos/alice/app/releases/"+rel.Id.String()+"/assets?name=y.zip", "gsj_altro", "", data), http.StatusNotFound)
	e.want(e.asJob("POST", "/repos/alice/inesistente/releases", "gsj_scrive", `{"tagName":"v1.0.0","name":"X"}`, nil), http.StatusNotFound)

	// Token assente, sconosciuto o scaduto: 401.
	e.want(e.asJob("POST", "/repos/alice/app/releases", "", `{"tagName":"v1.1.0","name":"X"}`, nil), http.StatusUnauthorized)
	e.want(e.asJob("POST", "/repos/alice/app/releases", "gsj_inventato", `{"tagName":"v1.1.0","name":"X"}`, nil), http.StatusUnauthorized)
	e.want(e.asJob("POST", "/repos/alice/app/releases", "gsj_scaduto", `{"tagName":"v1.1.0","name":"X"}`, nil), http.StatusUnauthorized)

	// Un job non più in corso perde il token.
	if _, err := e.pool.Exec(context.Background(), `UPDATE core.ci_jobs SET status='completed' WHERE id=(SELECT job_id FROM core.ci_job_tokens WHERE token_hash=$1)`, hash("gsj_scrive")); err != nil {
		t.Fatal(err)
	}
	e.want(e.asJob("POST", "/repos/alice/app/releases", "gsj_scrive", `{"tagName":"v1.1.0","name":"X"}`, nil), http.StatusUnauthorized)
	if _, err := e.pool.Exec(context.Background(), `UPDATE core.ci_jobs SET status='in_progress' WHERE id=(SELECT job_id FROM core.ci_job_tokens WHERE token_hash=$1)`, hash("gsj_scrive")); err != nil {
		t.Fatal(err)
	}

	// Archiviato: 409 come per un utente.
	if _, err := e.pool.Exec(context.Background(), `UPDATE core.repositories SET archived_at = now() WHERE name = 'app'`); err != nil {
		t.Fatal(err)
	}
	e.want(e.asJob("POST", "/repos/alice/app/releases", "gsj_scrive", `{"tagName":"v1.1.0","name":"X"}`, nil), http.StatusConflict)
	if _, err := e.pool.Exec(context.Background(), `UPDATE core.repositories SET archived_at = NULL WHERE name = 'app'`); err != nil {
		t.Fatal(err)
	}

	// «Nient'altro»: il token del job con contents: write non fa altro che create/upload.
	for _, tc := range []struct{ method, path, body string }{
		{"PATCH", "/repos/alice/app", `{"description":"preso"}`},
		{"DELETE", "/repos/alice/app", ""},
		{"POST", "/repos/alice/app/issues", `{"title":"x"}`},
		{"GET", "/repos/alice/app/issues", ""},
		{"GET", "/repos/alice/app/releases", ""},
		{"PATCH", "/repos/alice/app/releases/" + rel.Id.String(), `{"draft":true}`},
		{"DELETE", "/repos/alice/app/releases/" + rel.Id.String(), ""},
		{"GET", "/repos/alice/app/releases/" + rel.Id.String() + "/assets/" + asset.Id.String() + "/download", ""},
		{"POST", "/repos", `{"owner":"alice","name":"nuovo"}`},
	} {
		if rec := e.asJob(tc.method, tc.path, "gsj_scrive", tc.body, nil); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s con il token del job = %d %s, voluto 403", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
	// Il repo non è cambiato, la release c'è ancora.
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.releases WHERE tag_name='v1.0.0' AND NOT draft`).Scan(&n); err != nil || n != 1 {
		t.Errorf("la release è stata toccata: %d (%v)", n, err)
	}
	var desc string
	if err := e.pool.QueryRow(context.Background(), `SELECT description FROM core.repositories WHERE name='app'`).Scan(&desc); err != nil || desc == "preso" {
		t.Errorf("il repo è stato modificato dal token del job: %q (%v)", desc, err)
	}
}
