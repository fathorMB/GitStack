//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	aliceID = "aaaaaaaa-0000-0000-0000-000000000001"
	bobID   = "bbbbbbbb-0000-0000-0000-000000000002"
	carolID = "cccccccc-0000-0000-0000-000000000003"
	acmeID  = "dddddddd-0000-0000-0000-000000000004"
)

// fakeIdentity riproduce le regole di identity che servono ai repo: owner
// utente = admin (P6), owner dell'organizzazione = admin (P1), visibilità
// interna = read a tutti (P3), amministratore di sistema = tutto.
type fakeIdentity struct {
	mu       sync.Mutex
	owners   map[string]identityclient.Owner
	orgOwner map[string][]string // id organizzazione -> utenti owner
	attrs    map[uuid.UUID]attr
	grants   map[uuid.UUID][]string // utenti con grant admin
	sysAdmin map[string]bool

	failGrant bool
	failAttrs bool
	attrCalls int
}

type attr struct {
	ownerType string
	ownerID   uuid.UUID
	vis       string
}

func newFakeIdentity() *fakeIdentity {
	owner := func(t, id, name string) identityclient.Owner {
		return identityclient.Owner{Type: t, ID: uuid.MustParse(id), Name: name}
	}
	return &fakeIdentity{
		owners: map[string]identityclient.Owner{
			"alice": owner("user", aliceID, "alice"),
			"bob":   owner("user", bobID, "bob"),
			"carol": owner("user", carolID, "carol"),
			"acme":  owner("organization", acmeID, "acme"),
		},
		orgOwner: map[string][]string{acmeID: {aliceID}},
		attrs:    map[uuid.UUID]attr{},
		grants:   map[uuid.UUID][]string{},
		sysAdmin: map[string]bool{},
	}
}

func (f *fakeIdentity) role(user string, res uuid.UUID) string {
	if f.sysAdmin[user] {
		return "admin"
	}
	if slices.Contains(f.grants[res], user) {
		return "admin"
	}
	a, ok := f.attrs[res]
	if !ok {
		return ""
	}
	if a.ownerType == "user" && a.ownerID.String() == user {
		return "admin"
	}
	if a.ownerType == "organization" && slices.Contains(f.orgOwner[a.ownerID.String()], user) {
		return "admin"
	}
	if a.vis == "internal" {
		return "read"
	}
	return ""
}

func rank(r string) int { return map[string]int{"": 0, "read": 1, "write": 2, "admin": 3}[r] }

func (f *fakeIdentity) HasRole(_ context.Context, userID, resourceID uuid.UUID, role string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return rank(f.role(userID.String(), resourceID)) >= rank(role), nil
}

func (f *fakeIdentity) ReadableResources(_ context.Context, userID uuid.UUID) (bool, []uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sysAdmin[userID.String()] {
		return true, nil, nil
	}
	var ids []uuid.UUID
	for id := range f.attrs {
		if rank(f.role(userID.String(), id)) >= 1 {
			ids = append(ids, id)
		}
	}
	return false, ids, nil
}

func (f *fakeIdentity) GrantResourceCreator(_ context.Context, resourceID, userID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failGrant {
		return errors.New("identity giù")
	}
	f.grants[resourceID] = append(f.grants[resourceID], userID.String())
	return nil
}

func (f *fakeIdentity) ResolveOwner(_ context.Context, name string) (identityclient.Owner, error) {
	o, ok := f.owners[name]
	if !ok {
		return identityclient.Owner{}, identityclient.ErrNotFound
	}
	return o, nil
}

func (f *fakeIdentity) SetResourceAttributes(_ context.Context, id uuid.UUID, ownerType string, ownerID uuid.UUID, vis string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attrCalls++
	if f.failAttrs {
		return identityclient.ErrUnavailable
	}
	f.attrs[id] = attr{ownerType, ownerID, vis}
	return nil
}

// fakeGit tiene i repo in memoria: stesse risposte dell'API interna di git.
type fakeGit struct {
	mu       sync.Mutex
	repos    map[uuid.UUID]*gitclient.State
	created  []gitclient.CreateInput
	failNext error
	trashed  []uuid.UUID
	deleted  []uuid.UUID
}

func newFakeGit() *fakeGit { return &fakeGit{repos: map[uuid.UUID]*gitclient.State{}} }

func (g *fakeGit) Create(_ context.Context, _ trust.Identity, in gitclient.CreateInput) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.failNext != nil {
		err := g.failNext
		g.failNext = nil
		return false, err
	}
	g.created = append(g.created, in)
	withFiles := in.Readme || in.GitignoreTemplate != "" || in.LicenseTemplate != ""
	st := &gitclient.State{Empty: !withFiles, Branches: []string{}}
	if withFiles {
		st.Branches = []string{in.DefaultBranch}
	}
	g.repos[in.RepoID] = st
	return st.Empty, nil
}

func (g *fakeGit) Get(_ context.Context, _ trust.Identity, id uuid.UUID) (gitclient.State, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	st, ok := g.repos[id]
	if !ok {
		return gitclient.State{}, gitclient.ErrNotFound
	}
	return *st, nil
}

func (g *fakeGit) Trash(_ context.Context, _ trust.Identity, id uuid.UUID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.trashed = append(g.trashed, id)
	if st, ok := g.repos[id]; ok {
		st.Trashed = true
	}
	return nil
}

func (g *fakeGit) Delete(_ context.Context, _ trust.Identity, id uuid.UUID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.deleted = append(g.deleted, id)
	delete(g.repos, id)
	return nil
}

func (g *fakeGit) Restore(_ context.Context, _ trust.Identity, id uuid.UUID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if st, ok := g.repos[id]; ok {
		st.Trashed = false
	}
	return nil
}

var _ gitclient.Git = (*fakeGit)(nil)

type reposEnv struct {
	t      *testing.T
	pool   *pgxpool.Pool
	router http.Handler
	id     *fakeIdentity
	git    *fakeGit
}

func newReposEnv(t *testing.T, clone httpserver.CloneConfig) *reposEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	e := &reposEnv{t: t, pool: pool, id: newFakeIdentity(), git: newFakeGit()}
	e.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(e.id), httpserver.WithReadableLister(e.id), httpserver.WithGit(e.git), httpserver.WithCloneConfig(clone))
	return e
}

var users = map[string]string{"alice": aliceID, "bob": bobID, "carol": carolID}

func (e *reposEnv) do(method, path, user, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	trust.Sign(req.Header, trustSecret, trust.Identity{UserID: users[user], Username: user}, time.Now())
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *reposEnv) want(rec *httptest.ResponseRecorder, status int) {
	e.t.Helper()
	if rec.Code != status {
		e.t.Fatalf("risposta %d %s, voluto %d", rec.Code, rec.Body.String(), status)
	}
}

func decodeRepo(t *testing.T, rec *httptest.ResponseRecorder) openapi.Repository {
	t.Helper()
	var r openapi.Repository
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("risposta non valida: %v: %s", err, rec.Body.String())
	}
	return r
}

func (e *reposEnv) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *reposEnv) create(user, body string) openapi.Repository {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/repos", user, body)
	e.want(rec, http.StatusCreated)
	return decodeRepo(e.t, rec)
}

func TestRepos_Creazione(t *testing.T) {
	e := newReposEnv(t, httpserver.CloneConfig{PublicURL: "https://git.example.com", SSHPort: 2222})

	t.Run("visibilita_private_di_default_e_contatore", func(t *testing.T) {
		rec := e.do(http.MethodPost, "/repos", "alice", `{"owner":"alice","name":"my-app"}`)
		e.want(rec, http.StatusCreated)
		r := decodeRepo(t, rec)
		if r.Visibility != "private" {
			t.Fatalf("visibility = %q, voluto private anche con il campo assente (P7)", r.Visibility)
		}
		if r.DefaultBranch != "main" || !r.ProtectDefaultBranch || r.Archived || !r.Empty || r.FullName != "alice/my-app" {
			t.Fatalf("valori di default inattesi: %+v", r)
		}
		if r.Owner.Name != "alice" || r.Owner.Type != "user" {
			t.Fatalf("owner = %+v", r.Owner)
		}
		if loc := rec.Header().Get("Location"); loc != "/repos/alice/my-app" {
			t.Fatalf("Location = %q", loc)
		}
		// I1: la riga del contatore #n è nata nella stessa transazione, a 1.
		if n := e.count(`SELECT next_number FROM core.repo_counters WHERE repo_id = $1`, uuid.UUID(*r.Id)); n != 1 {
			t.Fatalf("next_number = %d, voluto 1", n)
		}
		// Identity: attributi (privato) impostati e grant admin al creatore.
		e.id.mu.Lock()
		defer e.id.mu.Unlock()
		if a := e.id.attrs[uuid.UUID(*r.Id)]; a.vis != "private" || a.ownerID.String() != aliceID {
			t.Fatalf("attributi in identity: %+v", a)
		}
		if !slices.Contains(e.id.grants[uuid.UUID(*r.Id)], aliceID) {
			t.Fatal("manca il grant admin al creatore")
		}
	})

	t.Run("visibilita_interna_e_contenuto_iniziale", func(t *testing.T) {
		r := e.create("alice", `{"owner":"alice","name":"with-files","visibility":"internal","description":"d","readme":true,"gitignoreTemplate":"go","licenseTemplate":"mit"}`)
		if r.Visibility != "internal" || r.Description != "d" || r.Empty {
			t.Fatalf("repo = %+v", r)
		}
		last := e.git.created[len(e.git.created)-1]
		if !last.Readme || last.GitignoreTemplate != "go" || last.LicenseTemplate != "mit" || last.DefaultBranch != "main" || last.Author.Name != "alice" || last.Author.Email == "" {
			t.Fatalf("richiesta a git: %+v", last)
		}
	})

	t.Run("etichette_predefinite_di_default_e_opzione_spenta", func(t *testing.T) {
		on := e.create("alice", `{"owner":"alice","name":"labels-on"}`)
		off := e.create("alice", `{"owner":"alice","name":"labels-off","defaultLabels":false}`)
		const q = `SELECT count(*) FROM core.labels WHERE repo_id = $1`
		if n := e.count(q, uuid.UUID(*on.Id)); n != 8 {
			t.Fatalf("etichette predefinite = %d, volute 8 (I5)", n)
		}
		if n := e.count(`SELECT count(*) FROM core.labels WHERE repo_id = $1 AND name IN ('bug','agent-ready','good first issue')`, uuid.UUID(*on.Id)); n != 3 {
			t.Fatalf("mancano etichette attese: %d", n)
		}
		if n := e.count(q, uuid.UUID(*off.Id)); n != 0 {
			t.Fatalf("con defaultLabels=false: %d etichette, volute 0", n)
		}
	})

	t.Run("owner_organizzazione", func(t *testing.T) {
		r := e.create("alice", `{"owner":"acme","name":"site"}`)
		if r.Owner.Type != "organization" || r.FullName != "acme/site" {
			t.Fatalf("repo = %+v", r)
		}
	})

	t.Run("senza_permesso_403", func(t *testing.T) {
		before := e.count(`SELECT count(*) FROM core.repositories`)
		// Bob non è owner dell'organizzazione né l'utente alice.
		e.want(e.do(http.MethodPost, "/repos", "bob", `{"owner":"acme","name":"nope"}`), http.StatusForbidden)
		e.want(e.do(http.MethodPost, "/repos", "bob", `{"owner":"alice","name":"nope"}`), http.StatusForbidden)
		if n := e.count(`SELECT count(*) FROM core.repositories`); n != before {
			t.Fatalf("righe: %d, volute %d", n, before)
		}
	})

	t.Run("nome_non_valido_400", func(t *testing.T) {
		for _, name := range []string{"Maiuscole", "x.git", ".nascosto", "con spazio", "", strings.Repeat("a", 101), "a/b"} {
			body, _ := json.Marshal(map[string]string{"owner": "alice", "name": name})
			e.want(e.do(http.MethodPost, "/repos", "alice", string(body)), http.StatusBadRequest)
		}
		e.want(e.do(http.MethodPost, "/repos", "alice", `{"owner":"alice","name":"ok","visibility":"public"}`), http.StatusBadRequest)
		e.want(e.do(http.MethodPost, "/repos", "alice", `{"owner":"alice","name":"ok","extra":1}`), http.StatusBadRequest)
	})

	t.Run("nome_duplicato_409", func(t *testing.T) {
		e.want(e.do(http.MethodPost, "/repos", "alice", `{"owner":"alice","name":"my-app"}`), http.StatusConflict)
		// Stesso nome con un altro owner: va.
		e.create("bob", `{"owner":"bob","name":"my-app"}`)
	})

	t.Run("owner_inesistente_404", func(t *testing.T) {
		e.want(e.do(http.MethodPost, "/repos", "alice", `{"owner":"nessuno","name":"x"}`), http.StatusNotFound)
	})

	t.Run("modello_sconosciuto_400_nessun_repo", func(t *testing.T) {
		e.git.failNext = gitclient.ErrInvalid
		e.want(e.do(http.MethodPost, "/repos", "alice", `{"owner":"alice","name":"bad-tpl","readme":true}`), http.StatusBadRequest)
		if n := e.count(`SELECT count(*) FROM core.repositories WHERE name = 'bad-tpl'`); n != 0 {
			t.Fatalf("repo a metà: %d righe", n)
		}
	})

	t.Run("git_fallisce_nessun_repo_a_meta", func(t *testing.T) {
		e.git.failNext = gitclient.ErrUnavailable
		e.want(e.do(http.MethodPost, "/repos", "alice", `{"owner":"alice","name":"git-down"}`), http.StatusServiceUnavailable)
		if n := e.count(`SELECT count(*) FROM core.repositories WHERE name = 'git-down'`); n != 0 {
			t.Fatalf("repositories: %d righe, volute 0", n)
		}
		if n := e.count(`SELECT count(*) FROM core.resources WHERE name = 'alice/git-down'`); n != 0 {
			t.Fatalf("resources: %d righe, volute 0", n)
		}
		if n := e.count(`SELECT count(*) FROM core.repo_counters c WHERE NOT EXISTS (SELECT 1 FROM core.repositories r WHERE r.resource_id = c.repo_id)`); n != 0 {
			t.Fatalf("contatori orfani: %d", n)
		}
		// Il nome non resta occupato: riprovando si crea.
		e.create("alice", `{"owner":"alice","name":"git-down"}`)
	})

	t.Run("grant_fallito_annulla_anche_il_disco", func(t *testing.T) {
		e.id.failGrant = true
		defer func() { e.id.failGrant = false }()
		before := len(e.git.deleted)
		e.want(e.do(http.MethodPost, "/repos", "alice", `{"owner":"alice","name":"no-grant"}`), http.StatusServiceUnavailable)
		if n := e.count(`SELECT count(*) FROM core.repositories WHERE name = 'no-grant'`); n != 0 {
			t.Fatalf("repo a metà: %d righe", n)
		}
		if len(e.git.deleted) != before+1 {
			t.Fatal("il repo su disco doveva essere rimosso")
		}
	})

	t.Run("senza_identita_401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/repos", strings.NewReader(`{"owner":"alice","name":"x"}`))
		rec := httptest.NewRecorder()
		e.router.ServeHTTP(rec, req)
		e.want(rec, http.StatusUnauthorized)
	})
}

func TestRepos_LetturaEElenco(t *testing.T) {
	e := newReposEnv(t, httpserver.CloneConfig{PublicURL: "https://git.example.com", SSHPort: 2222})
	e.create("alice", `{"owner":"alice","name":"privato"}`)
	e.create("alice", `{"owner":"alice","name":"interno","visibility":"internal","readme":true}`)
	e.create("bob", `{"owner":"bob","name":"di-bob"}`)
	e.create("alice", `{"owner":"acme","name":"org-repo"}`)

	t.Run("get_del_proprietario", func(t *testing.T) {
		rec := e.do(http.MethodGet, "/repos/alice/privato", "alice", "")
		e.want(rec, http.StatusOK)
		r := decodeRepo(t, rec)
		if r.Name != "privato" || r.Owner.Name != "alice" || !r.Empty {
			t.Fatalf("repo = %+v", r)
		}
		if got := decodeRepo(t, e.do(http.MethodGet, "/repos/alice/interno", "alice", "")); got.Empty {
			t.Fatal("un repo con README non è vuoto")
		}
	})

	t.Run("repo_privato_di_altri_404", func(t *testing.T) {
		rec := e.do(http.MethodGet, "/repos/alice/privato", "bob", "")
		e.want(rec, http.StatusNotFound)
		// Stessa risposta di un repo che non esiste: nessuna differenza osservabile.
		missing := e.do(http.MethodGet, "/repos/alice/non-esiste", "bob", "")
		e.want(missing, http.StatusNotFound)
		if rec.Body.String() != missing.Body.String() {
			t.Fatalf("risposte diverse: %q vs %q", rec.Body.String(), missing.Body.String())
		}
		e.want(e.do(http.MethodGet, "/repos/org-sconosciuta/x", "bob", ""), http.StatusNotFound)
	})

	t.Run("repo_interno_leggibile_da_tutti", func(t *testing.T) {
		e.want(e.do(http.MethodGet, "/repos/alice/interno", "bob", ""), http.StatusOK)
		e.want(e.do(http.MethodGet, "/repos/alice/interno", "carol", ""), http.StatusOK)
	})

	t.Run("elenco_filtrato_per_permesso", func(t *testing.T) {
		names := func(user, query string) []string {
			rec := e.do(http.MethodGet, "/repos"+query, user, "")
			e.want(rec, http.StatusOK)
			var l openapi.RepositoryList
			if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, it := range l.Items {
				out = append(out, it.FullName)
			}
			if l.Total != len(out) && query == "" {
				t.Fatalf("total %d, elementi %d", l.Total, len(out))
			}
			return out
		}
		if got := names("alice", ""); len(got) != 3 {
			t.Fatalf("alice vede %v, volute 3 (suoi e org; di bob niente)", got)
		}
		got := names("bob", "")
		slices.Sort(got)
		if strings.Join(got, ",") != "alice/interno,bob/di-bob" {
			t.Fatalf("bob vede %v", got)
		}
		if got := names("carol", ""); len(got) != 1 || got[0] != "alice/interno" {
			t.Fatalf("carol vede %v", got)
		}
		if got := names("alice", "?owner=acme"); len(got) != 1 || got[0] != "acme/org-repo" {
			t.Fatalf("filtro owner: %v", got)
		}
		// Amministratore di sistema: tutto.
		e.id.sysAdmin[carolID] = true
		if got := names("carol", ""); len(got) != 4 {
			t.Fatalf("admin vede %v", got)
		}
		e.want(e.do(http.MethodGet, "/repos?page=0", "alice", ""), http.StatusBadRequest)
		rec := e.do(http.MethodGet, "/repos?perPage=2&page=2", "alice", "")
		e.want(rec, http.StatusOK)
		var l openapi.RepositoryList
		_ = json.Unmarshal(rec.Body.Bytes(), &l)
		if l.Total != 3 || len(l.Items) != 1 || l.Page != 2 {
			t.Fatalf("paginazione: %+v", l)
		}
	})
}

func TestRepos_Impostazioni(t *testing.T) {
	e := newReposEnv(t, httpserver.CloneConfig{PublicURL: "https://git.example.com", SSHPort: 2222})
	r := e.create("alice", `{"owner":"alice","name":"cfg","readme":true}`)
	id := uuid.UUID(*r.Id)
	// Il repo ha un secondo branch su disco.
	e.git.repos[id].Branches = []string{"develop", "main"}
	patch := func(user, body string) *httptest.ResponseRecorder {
		return e.do(http.MethodPatch, "/repos/alice/cfg", user, body)
	}

	t.Run("senza_permesso", func(t *testing.T) {
		// Bob non legge un repo privato: 404, non 403.
		e.want(patch("bob", `{"description":"x"}`), http.StatusNotFound)
		// Se lo legge (interno) ma non è admin: 403.
		e.want(patch("alice", `{"visibility":"internal"}`), http.StatusOK)
		e.want(patch("bob", `{"description":"x"}`), http.StatusForbidden)
		e.want(patch("alice", `{"visibility":"private"}`), http.StatusOK)
	})

	t.Run("corpo_non_valido", func(t *testing.T) {
		e.want(patch("alice", `{}`), http.StatusBadRequest)
		e.want(patch("alice", `{"visibility":"public"}`), http.StatusBadRequest)
		e.want(patch("alice", `{"nome":"nuovo"}`), http.StatusBadRequest)
		e.want(patch("alice", `{"defaultBranch":""}`), http.StatusBadRequest)
	})

	t.Run("branch_principale_fra_quelli_esistenti", func(t *testing.T) {
		e.want(patch("alice", `{"defaultBranch":"fantasma"}`), http.StatusBadRequest)
		rec := patch("alice", `{"defaultBranch":"develop"}`)
		e.want(rec, http.StatusOK)
		if decodeRepo(t, rec).DefaultBranch != "develop" {
			t.Fatalf("repo = %s", rec.Body.String())
		}
		e.want(patch("alice", `{"defaultBranch":"main"}`), http.StatusOK)
	})

	t.Run("protezione_attiva_di_default_e_disattivabile", func(t *testing.T) {
		if !decodeRepo(t, e.do(http.MethodGet, "/repos/alice/cfg", "alice", "")).ProtectDefaultBranch {
			t.Fatal("R9: la protezione è attiva di default")
		}
		if decodeRepo(t, patch("alice", `{"protectDefaultBranch":false}`)).ProtectDefaultBranch {
			t.Fatal("protectDefaultBranch doveva diventare false")
		}
	})

	t.Run("visibilita_aggiorna_identity", func(t *testing.T) {
		e.want(patch("alice", `{"visibility":"internal","description":"nuova"}`), http.StatusOK)
		e.id.mu.Lock()
		vis := e.id.attrs[id].vis
		e.id.mu.Unlock()
		if vis != "internal" {
			t.Fatalf("visibilità in identity = %q", vis)
		}
		e.want(e.do(http.MethodGet, "/repos/alice/cfg", "bob", ""), http.StatusOK)
		// Identity giù: la modifica si annulla del tutto.
		e.id.failAttrs = true
		e.want(patch("alice", `{"visibility":"private","description":"non applicata"}`), http.StatusServiceUnavailable)
		e.id.failAttrs = false
		got := decodeRepo(t, e.do(http.MethodGet, "/repos/alice/cfg", "alice", ""))
		if got.Visibility != "internal" || got.Description != "nuova" {
			t.Fatalf("modifica applicata nonostante l'errore: %+v", got)
		}
	})

	t.Run("archiviazione_e_riattivazione", func(t *testing.T) {
		rec := patch("alice", `{"archived":true}`)
		e.want(rec, http.StatusOK)
		a := decodeRepo(t, rec)
		if !a.Archived || a.ArchivedAt == nil {
			t.Fatalf("repo = %+v", a)
		}
		// Archiviato: le impostazioni sono rifiutate con 409...
		e.want(patch("alice", `{"description":"x"}`), http.StatusConflict)
		e.want(patch("alice", `{"visibility":"private"}`), http.StatusConflict)
		e.want(patch("alice", `{"protectDefaultBranch":true}`), http.StatusConflict)
		e.want(patch("alice", `{"defaultBranch":"develop"}`), http.StatusConflict)
		// ...anche la riattivazione accompagnata da altro...
		e.want(patch("alice", `{"archived":false,"description":"x"}`), http.StatusConflict)
		// ...ma resta leggibile.
		e.want(e.do(http.MethodGet, "/repos/alice/cfg", "alice", ""), http.StatusOK)
		// La riattivazione da sola passa.
		rec = patch("alice", `{"archived":false}`)
		e.want(rec, http.StatusOK)
		if b := decodeRepo(t, rec); b.Archived || b.ArchivedAt != nil {
			t.Fatalf("repo = %+v", b)
		}
		e.want(patch("alice", `{"description":"di nuovo modificabile"}`), http.StatusOK)
	})
}
