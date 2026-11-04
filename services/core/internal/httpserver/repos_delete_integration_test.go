//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/attachments"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/repopurge"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// R2 rivista il 4/11 (GIT-530): l'eliminazione è definitiva e immediata. TestRepos_Eliminazione prova
// permessi, sparizione e nome libero subito; TestRepos_EliminazioneTogliTutto prova tutto ciò che un
// repo si porta dietro; TestRepos_EliminazionePulizieInBackground il caso in cui disco o volumi non
// si tolgono subito.

// lazyPurger permette di creare il router prima del Job, che ha bisogno del pool dell'ambiente.
type lazyPurger struct {
	mu  sync.Mutex
	job *repopurge.Job
}

func (l *lazyPurger) set(j *repopurge.Job) { l.mu.Lock(); l.job = j; l.mu.Unlock() }

func (l *lazyPurger) Purge(ctx context.Context, id uuid.UUID) error {
	l.mu.Lock()
	j := l.job
	l.mu.Unlock()
	return j.Purge(ctx, id)
}

type recordingAccess struct {
	mu     sync.Mutex
	purged []uuid.UUID
}

func (a *recordingAccess) PurgeResource(_ context.Context, id uuid.UUID) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.purged = append(a.purged, id)
	return nil
}

type recordingApps struct {
	mu     sync.Mutex
	purged []uuid.UUID
}

func (a *recordingApps) ForgetApps(_ context.Context, ids []uuid.UUID) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.purged = append(a.purged, ids...)
	return nil
}

func countOf(t *testing.T, pool *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRepos_Eliminazione(t *testing.T) {
	e := newReposEnv(t, httpserver.CloneConfig{PublicURL: "https://git.example.com", SSHPort: 2222})
	r := e.create("alice", `{"owner":"alice","name":"gone","visibility":"internal","description":"d","readme":true}`)
	id := uuid.UUID(*r.Id)

	t.Run("senza_admin_403_o_404", func(t *testing.T) {
		// bob legge (interno) ma non è admin: 403. carol non legge un repo privato: 404.
		e.want(e.do(http.MethodDelete, "/repos/alice/gone", "bob", ""), http.StatusForbidden)
		e.create("alice", `{"owner":"alice","name":"secret"}`)
		e.want(e.do(http.MethodDelete, "/repos/alice/secret", "carol", ""), http.StatusNotFound)
		e.want(e.do(http.MethodGet, "/repos/alice/gone", "alice", ""), http.StatusOK)
		e.want(e.do(http.MethodGet, "/repos/alice/secret", "alice", ""), http.StatusOK)
	})

	t.Run("elimina_sparisce_subito_e_il_nome_e_libero", func(t *testing.T) {
		e.want(e.do(http.MethodDelete, "/repos/alice/gone", "alice", ""), http.StatusNoContent)
		e.want(e.do(http.MethodGet, "/repos/alice/gone", "alice", ""), http.StatusNotFound)
		e.want(e.do(http.MethodGet, "/repos/alice/gone", "bob", ""), http.StatusNotFound)
		e.want(e.do(http.MethodDelete, "/repos/alice/gone", "alice", ""), http.StatusNotFound)
		if n := e.count(`SELECT count(*) FROM core.repositories WHERE resource_id = $1`, id); n != 0 {
			t.Fatalf("la riga del repo c'è ancora (%d)", n)
		}
		rec := e.do(http.MethodGet, "/repos", "alice", "")
		e.want(rec, http.StatusOK)
		var l openapi.RepositoryList
		if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		for _, it := range l.Items {
			if it.Name == "gone" {
				t.Fatal("il repo eliminato compare ancora nell'elenco")
			}
		}
		// Nome subito libero: nuovo repo con lo stesso owner/nome, id diverso.
		rec = e.do(http.MethodPost, "/repos", "alice", `{"owner":"alice","name":"gone"}`)
		e.want(rec, http.StatusCreated)
		if uuid.UUID(*decodeRepo(t, rec).Id) == id {
			t.Fatal("il repo nuovo ha l'id del vecchio")
		}
		// Un repo attivo con quel nome resta 409 conflict, senza name_in_trash.
		rec = e.do(http.MethodPost, "/repos", "alice", `{"owner":"alice","name":"gone"}`)
		e.want(rec, http.StatusConflict)
		if strings.Contains(rec.Body.String(), "name_in_trash") || !strings.Contains(rec.Body.String(), `"conflict"`) {
			t.Fatalf("409 = %s", rec.Body.String())
		}
	})

	t.Run("niente_cestino_ne_ripristino", func(t *testing.T) {
		for _, c := range []struct{ method, path string }{
			{http.MethodGet, "/repos/deleted"},
			{http.MethodPost, "/repos/deleted/" + id.String() + "/restore"},
		} {
			rec := e.do(c.method, c.path, "alice", "")
			if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s = %d, voluto 404/405", c.method, c.path, rec.Code)
			}
		}
	})

	t.Run("amministratore_di_sistema", func(t *testing.T) {
		e.id.mu.Lock()
		e.id.sysAdmin[carolID] = true
		e.id.mu.Unlock()
		e.create("bob", `{"owner":"bob","name":"b1"}`)
		e.want(e.do(http.MethodDelete, "/repos/bob/b1", "carol", ""), http.StatusNoContent)
		e.want(e.do(http.MethodPost, "/repos", "bob", `{"owner":"bob","name":"b1"}`), http.StatusCreated)
	})
}

// Le API generiche /resources non devono vedere un repo eliminato, né permettere di eliminare o
// rinominare un repo aggirando le regole.
func TestRepos_EliminatoNelleApiGeneriche(t *testing.T) {
	e := newReposEnv(t, httpserver.CloneConfig{PublicURL: "https://git.example.com", SSHPort: 2222})
	r := e.create("alice", `{"owner":"alice","name":"hidden"}`)
	id := uuid.UUID(*r.Id).String()

	listed := func() bool {
		rec := e.do(http.MethodGet, "/resources?type=repo", "alice", "")
		e.want(rec, http.StatusOK)
		var l struct {
			Items []struct {
				Id string `json:"id"`
			} `json:"items"`
			Total int `json:"total"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, it := range l.Items {
			found = found || it.Id == id
		}
		if found != (l.Total == 1) || (!found && l.Total != 0) {
			t.Fatalf("total = %d incoerente con l'elenco (trovato: %v)", l.Total, found)
		}
		return found
	}

	t.Run("repo_vivo_non_modificabile_da_resources", func(t *testing.T) {
		if !listed() {
			t.Fatal("il repo vivo deve comparire in GET /resources?type=repo")
		}
		e.want(e.do(http.MethodGet, "/resources/"+id, "alice", ""), http.StatusOK)
		rec := e.do(http.MethodDelete, "/resources/"+id, "alice", "")
		e.want(rec, http.StatusConflict)
		if !strings.Contains(rec.Body.String(), "use_repos_api") {
			t.Fatalf("codice atteso use_repos_api: %s", rec.Body.String())
		}
		e.want(e.do(http.MethodPatch, "/resources/"+id, "alice", `{"name":"alice/other"}`), http.StatusConflict)
		if e.count(`SELECT count(*) FROM core.resources WHERE id = $1 AND name = 'alice/hidden'`, uuid.UUID(*r.Id)) != 1 {
			t.Fatal("la risorsa è stata modificata o cancellata")
		}
	})

	t.Run("eliminato_sparisce_dalle_api_generiche", func(t *testing.T) {
		e.want(e.do(http.MethodDelete, "/repos/alice/hidden", "alice", ""), http.StatusNoContent)
		if listed() {
			t.Fatal("il repo eliminato compare in GET /resources?type=repo")
		}
		e.want(e.do(http.MethodGet, "/resources/"+id, "alice", ""), http.StatusNotFound)
		e.want(e.do(http.MethodDelete, "/resources/"+id, "alice", ""), http.StatusNotFound)
		e.want(e.do(http.MethodPatch, "/resources/"+id, "alice", `{"name":"x"}`), http.StatusNotFound)
	})
}

// flakyGit fa fallire la cancellazione su disco finché fail è vero (disco lento, o servizio git giù
// dopo che il repo è già stato spostato).
type flakyGit struct {
	*fakeGit
	fmu  sync.Mutex
	fail bool
}

func (g *flakyGit) Delete(ctx context.Context, c trust.Identity, id uuid.UUID) error {
	g.fmu.Lock()
	f := g.fail
	g.fmu.Unlock()
	if f {
		return gitclient.ErrUnavailable
	}
	return g.fakeGit.Delete(ctx, c, id)
}

func (g *flakyGit) setFail(v bool) { g.fmu.Lock(); g.fail = v; g.fmu.Unlock() }

// Criterio 1 di GIT-530: DELETE di un repo con issue, release (e il loro allegato su disco), allegato di
// issue, immagine nel registry (la toglie il servizio git insieme ai dati del repo), app con volume,
// webhook e mirror in pull con credenziali; subito dopo un POST con lo stesso owner/nome risponde 201
// e di tutto questo non resta niente.
func TestRepos_EliminazioneTogliTutto(t *testing.T) {
	attDir, relDir := t.TempDir(), t.TempDir()
	lp := &lazyPurger{}
	e := newAppsEnv(t, httpserver.WithRepoPurger(lp),
		httpserver.WithAttachments(httpserver.AttachmentsConfig{Disk: &attachments.Disk{Dir: attDir}, ReleaseAssets: &attachments.Disk{Dir: relDir}}))
	acc, apps := &recordingAccess{}, &recordingApps{}
	lp.set(&repopurge.Job{Store: store.New(e.pool), Git: e.git, Identity: acc, Apps: apps,
		Attachments: &attachments.Disk{Dir: attDir}, ReleaseAssets: &attachments.Disk{Dir: relDir}})

	repoID := e.repo("handbook", true)
	e.open("handbook", "alice", "problema")
	e.attachment(repoID, "alice")
	relID, assetID := uuid.New(), uuid.New()
	alice := uuid.MustParse(aliceID)
	e.sql(`INSERT INTO core.releases (id, repo_id, tag_name, name, created_by, created_at) VALUES ($1, $2, 'v1', 'v1', $3, now())`, relID, repoID, alice)
	e.sql(`INSERT INTO core.release_assets (id, release_id, name, sha256, size_bytes, created_at) VALUES ($1, $2, 'a.zip', 'abc', 3, now())`, assetID, relID)
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")
	e.sql(`INSERT INTO core.app_volumes (app_id, name, mount_path, size_bytes, version, created_at) SELECT id, 'data', '/data', 1073741824, 1, now() FROM core.apps`)
	e.sql(`INSERT INTO core.webhooks (id, scope, repo_id, url, events, created_by) VALUES (gen_random_uuid(), 'repo', $1, 'https://example.com/h', ARRAY['push'], $2)`, repoID, alice)
	e.sql(`INSERT INTO core.repo_pull_mirrors (id, repo_id, origin_url, username, token_ciphertext, token_nonce, token_key_id, created_by)
		VALUES (gen_random_uuid(), $1, 'https://example.org/x.git', 'u', '\x0102', '\x010203040506070809101112', 'k1', $2)`, repoID, alice)
	for _, dir := range []string{attDir, relDir} {
		if err := os.MkdirAll(filepath.Join(dir, repoID.String()), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, repoID.String(), "file"), []byte("abc"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var appID uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM core.apps WHERE repo_id = $1`, repoID).Scan(&appID); err != nil {
		t.Fatal(err)
	}
	// Prima dell'eliminazione c'è tutto.
	for _, q := range []string{
		`SELECT count(*) FROM core.issues WHERE repo_id = $1`, `SELECT count(*) FROM core.issue_attachments WHERE repo_id = $1`,
		`SELECT count(*) FROM core.releases WHERE repo_id = $1`, `SELECT count(*) FROM core.apps WHERE repo_id = $1`,
		`SELECT count(*) FROM core.webhooks WHERE repo_id = $1`, `SELECT count(*) FROM core.repo_pull_mirrors WHERE repo_id = $1`,
	} {
		if countOf(t, e.pool, q, repoID) != 1 {
			t.Fatalf("prima dell'eliminazione manca qualcosa: %s", q)
		}
	}

	e.want(e.do(http.MethodDelete, "/repos/alice/handbook", "alice", ""), http.StatusNoContent, "")
	// Subito dopo, senza attese: lo stesso owner/nome si crea.
	e.want(e.do(http.MethodPost, "/repos", "alice", `{"owner":"alice","name":"handbook"}`), http.StatusCreated, "")

	for _, q := range []struct{ name, sql string }{
		{"repo", `SELECT count(*) FROM core.resources WHERE id = $1`},
		{"issue", `SELECT count(*) FROM core.issues WHERE repo_id = $1`},
		{"allegati delle issue", `SELECT count(*) FROM core.issue_attachments WHERE repo_id = $1`},
		{"release", `SELECT count(*) FROM core.releases WHERE repo_id = $1`},
		{"app", `SELECT count(*) FROM core.apps WHERE repo_id = $1`},
		{"webhook", `SELECT count(*) FROM core.webhooks WHERE repo_id = $1`},
		{"mirror in pull e credenziali", `SELECT count(*) FROM core.repo_pull_mirrors WHERE repo_id = $1`},
	} {
		if got := countOf(t, e.pool, q.sql, repoID); got != 0 {
			t.Errorf("%s: %d righe dopo l'eliminazione", q.name, got)
		}
	}
	if got := countOf(t, e.pool, `SELECT count(*) FROM core.release_assets WHERE id = $1`, assetID); got != 0 {
		t.Errorf("allegati delle release: %d righe", got)
	}
	if got := countOf(t, e.pool, `SELECT count(*) FROM core.app_volumes`); got != 0 {
		t.Errorf("volumi: %d righe", got)
	}
	for _, dir := range []string{attDir, relDir} {
		if _, err := os.Stat(filepath.Join(dir, repoID.String())); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("la cartella %s del repo c'è ancora (%v)", dir, err)
		}
	}
	// Disco e immagini del registry (stesso id, le toglie il servizio git), app e volumi nel cluster,
	// grant in identity: ognuno toccato una volta.
	if len(e.git.deleted) != 1 || e.git.deleted[0] != repoID {
		t.Errorf("git: cancellato %v, voluto solo %s", e.git.deleted, repoID)
	}
	if len(apps.purged) != 1 || apps.purged[0] != appID {
		t.Errorf("app e volumi nel cluster: %v", apps.purged)
	}
	if len(acc.purged) != 1 || acc.purged[0] != repoID {
		t.Errorf("grant in identity: %v", acc.purged)
	}
	if got := countOf(t, e.pool, `SELECT count(*) FROM core.repo_cleanup`); got != 0 {
		t.Errorf("coda di pulizia non vuota: %d", got)
	}
}

// Se disco o volumi non si tolgono subito, la DELETE risponde comunque 204, il nome è libero e la
// pulizia finisce al giro successivo.
func TestRepos_EliminazionePulizieInBackground(t *testing.T) {
	lp := &lazyPurger{}
	e := newAppsEnv(t, httpserver.WithRepoPurger(lp))
	fg := &flakyGit{fakeGit: e.git, fail: true}
	acc := &recordingAccess{}
	job := &repopurge.Job{Store: store.New(e.pool), Git: fg, Identity: acc}
	lp.set(job)

	repoID := e.repo("slow", false)
	e.want(e.do(http.MethodDelete, "/repos/alice/slow", "alice", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodPost, "/repos", "alice", `{"owner":"alice","name":"slow"}`), http.StatusCreated, "")
	e.want(e.do(http.MethodGet, "/repos/alice/slow", "alice", ""), http.StatusOK, "")

	if got := countOf(t, e.pool, `SELECT count(*) FROM core.repo_cleanup WHERE repo_id = $1 AND attempts = 1 AND last_error <> ''`, repoID); got != 1 {
		t.Fatalf("il repo doveva restare in coda con un tentativo e l'errore: %d", got)
	}
	if len(acc.purged) != 0 {
		t.Fatalf("grant tolti prima del disco: %v", acc.purged)
	}
	fg.setFail(false)
	if n, err := job.RunOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("RunOnce = %d, %v", n, err)
	}
	if got := countOf(t, e.pool, `SELECT count(*) FROM core.repo_cleanup`); got != 0 {
		t.Fatalf("coda non vuota dopo il giro: %d", got)
	}
	if len(acc.purged) != 1 || acc.purged[0] != repoID {
		t.Fatalf("grant: %v", acc.purged)
	}
}
