//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/GitStack/services/core/internal/databases"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
)

// linkSpy e' un SQLRunner che tiene i ruoli di collegamento e registra gli statement nell'ordine. Le sessioni
// del ruolo si chiudono solo DOPO un pg_terminate_backend: un'implementazione che lo saltasse non vedrebbe mai
// zero sessioni (il test dell'ordine di DB12 fallisce).
type linkSpy struct {
	mu         sync.Mutex
	stmts      []string
	roles      map[string]bool
	sessions   int  // sessioni aperte del ruolo che si scollega
	stuck      bool // le sessioni non si chiudono nemmeno dopo pg_terminate_backend
	terminated bool
	failCreate bool
	// rolePw e' la password corrente di ogni ruolo l_<id> (CREATE ROLE / ALTER ROLE ... PASSWORD); failTerminate
	// fa fallire i prossimi N pg_terminate_backend (errore DOPO l'ALTER ROLE della rotazione).
	rolePw        map[string]string
	failTerminate int
}

var pwRe = regexp.MustCompile(`PASSWORD '([0-9a-f]+)'`)

var linkRoleRe = regexp.MustCompile(`l_[0-9a-f]{32}`)

func (s *linkSpy) Exec(_ context.Context, _ store.ManagedDB, database, sql string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	one := strings.Join(strings.Fields(sql), " ")
	s.stmts = append(s.stmts, database+"| "+one)
	role := linkRoleRe.FindString(one)
	switch {
	case strings.HasPrefix(one, "CREATE ROLE \"l_"):
		if s.failCreate {
			return fmt.Errorf("CREATE ROLE: simulato")
		}
		s.roles[role] = true
		if m := pwRe.FindStringSubmatch(one); m != nil {
			s.rolePw[role] = m[1]
		}
	case strings.HasPrefix(one, "ALTER ROLE \"l_") && strings.Contains(one, " PASSWORD '"):
		if m := pwRe.FindStringSubmatch(one); m != nil {
			s.rolePw[role] = m[1]
		}
	case strings.HasPrefix(one, "DROP ROLE \"l_"):
		delete(s.roles, role)
	case strings.Contains(one, "pg_terminate_backend"):
		if s.failTerminate > 0 {
			s.failTerminate--
			return fmt.Errorf("pg_terminate_backend: simulato")
		}
		s.terminated = true
	}
	return nil
}

func (s *linkSpy) Query(_ context.Context, _ store.ManagedDB, database, sql string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	one := strings.Join(strings.Fields(sql), " ")
	s.stmts = append(s.stmts, database+"| "+one)
	switch {
	case strings.Contains(one, "count(*) FROM pg_stat_activity"):
		if s.terminated && !s.stuck {
			return []string{"0"}, nil
		}
		return []string{fmt.Sprint(s.sessions)}, nil
	case strings.Contains(one, "FROM pg_roles WHERE rolname = '"):
		if s.roles[linkRoleRe.FindString(one)] {
			return []string{"1"}, nil
		}
	case strings.Contains(one, "FROM pg_roles WHERE rolname ~"):
		var out []string
		for r := range s.roles {
			out = append(out, r)
		}
		return out, nil
	}
	return nil, nil
}

func (s *linkSpy) statements() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.stmts...)
}

func (s *linkSpy) index(prefix string, from int) int {
	st := s.statements()
	for i := from; i < len(st); i++ {
		if strings.Contains(st[i], prefix) {
			return i
		}
	}
	return -1
}

type linkEnv struct {
	*appsEnv
	spy  *linkSpy
	ctl  *databases.Controller
	st   *store.Store
	kube *fake.Clientset
	ca   string
	keys *webhooks.Keyring
	db   openapi.Database
	dsn  string
}

func newLinkEnv(t *testing.T) *linkEnv { return newLinkEnvWith(t, nil, nil) }

// newLinkEnvWith e' newLinkEnv con un SQLRunner e una configurazione del controller a scelta (GIT-553: Postgres
// vero, dump e cartella dei dump). realSQL nil = linkSpy, come per tutti gli altri test.
func newLinkEnvWith(t *testing.T, realSQL func(dsn string) databases.SQLRunner, tune func(*databases.Config)) *linkEnv {
	t.Helper()
	pool, dsn := dbtest.NewPool(t)
	id := &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}
	base := &issuesEnv{t: t, pool: pool, id: id}
	keys, err := webhooks.NewKeyring("k1", hookSecretKey, "")
	if err != nil {
		t.Fatal(err)
	}
	clock := &appClock{t: time.Now().UTC().Truncate(time.Second)}
	proxy := &lateController{}
	base.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(id), httpserver.WithReadableLister(id), httpserver.WithGit(newFakeGit()),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}),
		httpserver.WithAppsDomain("apps.example.com"), httpserver.WithClock(clock.Now),
		httpserver.WithWebhooks(httpserver.WebhookConfig{Keys: keys}),
		httpserver.WithDatabases(httpserver.DatabasesConfig{Controller: proxy}))
	ca := t.TempDir()
	if err := os.WriteFile(filepath.Join(ca, "ca.crt"), []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spy := &linkSpy{roles: map[string]bool{}, rolePw: map[string]string{}}
	st := store.New(pool)
	kube := fake.NewSimpleClientset()
	cfg := databases.Config{SQL: spy, CADir: ca, Images: map[int]databases.Image{}, Secrets: keys,
		Unlink: databases.UnlinkOptions{Timeout: 150 * time.Millisecond, Every: 10 * time.Millisecond}, SweepEvery: time.Nanosecond}
	if realSQL != nil {
		cfg.SQL = realSQL(dsn)
	}
	if tune != nil {
		tune(&cfg)
	}
	ctl := databases.New(st, kube, cfg)
	proxy.set(ctl)
	e := &linkEnv{appsEnv: &appsEnv{issuesEnv: base, clock: clock, git: nil}, spy: spy, ctl: ctl, st: st, kube: kube, ca: ca, keys: keys, dsn: dsn}
	e.db = (&dbEnv{appsEnv: e.appsEnv}).create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted).Database
	if err := st.SetManagedDBStatus(context.Background(), uuid.UUID(e.db.Id), store.DBRunning, nil, clock.Now()); err != nil {
		t.Fatal(err)
	}
	return e
}

// mkApp crea repo e app web per un owner (alice o acme) e ritorna l'id dell'app e del repo.
func (e *linkEnv) mkApp(owner, repo string) (appID, repoID uuid.UUID) {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/repos", "alice", fmt.Sprintf(`{"owner":%q,"name":%q,"visibility":"internal"}`, owner, repo))
	e.want(rec, http.StatusCreated, "")
	repoID = uuid.UUID(*decodeRepo(e.t, rec).Id)
	e.want(e.do(http.MethodPost, "/repos/"+owner+"/"+repo+"/apps", "alice", createWeb), http.StatusAccepted, "")
	a := e.app(e.do(http.MethodGet, "/repos/"+owner+"/"+repo+"/apps/web", "alice", ""))
	return uuid.UUID(a.Id), repoID
}

func (e *linkEnv) link(user, owner, repo, mode string, want int) openapi.DatabaseLinkCreated {
	e.t.Helper()
	body := fmt.Sprintf(`{"app":{"owner":%q,"repo":%q,"name":"web"},"mode":%q}`, owner, repo, mode)
	rec := e.do(http.MethodPost, "/databases/alice/shop/links", user, body)
	e.want(rec, want, "")
	var out openapi.DatabaseLinkCreated
	if want == http.StatusCreated {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			e.t.Fatalf("%v: %s", err, rec.Body.String())
		}
	}
	return out
}

func (e *linkEnv) secrets(appID uuid.UUID) map[string]string {
	e.t.Helper()
	keys, _ := webhooks.NewKeyring("k1", hookSecretKey, "")
	v, err := e.st.AppSecretValues(context.Background(), keys, appID)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (e *linkEnv) versionCount(appID uuid.UUID) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.app_versions WHERE app_id = $1`, appID).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *linkEnv) deployCount(appID uuid.UUID) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.app_deploys WHERE app_id = $1`, appID).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// DB9: serve admin sull'owner del database E sull'app; l'owner dell'app deve essere quello del database
// (422 owner_mismatch) ma i repo possono essere diversi.
func TestDatabaseLinks_PermessiEOwner(t *testing.T) {
	e := newLinkEnv(t)
	_, webRepo := e.mkApp("alice", "web")
	apiApp, _ := e.mkApp("alice", "api")
	acmeApp, _ := e.mkApp("acme", "site")

	// Da un owner diverso: rifiutato, e l'app dell'altro owner non cambia.
	versionsBefore := e.versionCount(acmeApp)
	rec := e.do(http.MethodPost, "/databases/alice/shop/links", "alice", `{"app":{"owner":"acme","repo":"site","name":"web"},"mode":"rw"}`)
	e.want(rec, http.StatusUnprocessableEntity, "owner_mismatch")
	if e.versionCount(acmeApp) != versionsBefore || len(e.secrets(acmeApp)) != 0 {
		t.Fatal("un collegamento rifiutato non deve toccare l'app")
	}

	// Con admin sull'app ma non sull'owner del database: 404 (il database non si vede).
	e.id.grants[webRepo] = append(e.id.grants[webRepo], users["bob"])
	e.link("bob", "alice", "web", "rw", http.StatusNotFound)
	if len(e.spy.roles) != 0 {
		t.Fatal("nessun ruolo per un collegamento rifiutato")
	}

	// Da un repo diverso dello stesso owner: funziona.
	l1 := e.link("alice", "alice", "web", "rw", http.StatusCreated)
	l2 := e.link("alice", "alice", "api", "ro", http.StatusCreated)
	if l1.Link.State != "active" || l1.Link.Mode != "rw" || l2.Link.Mode != "ro" || l1.Link.App.Repo != "web" || l2.Link.App.Repo != "api" {
		t.Fatalf("collegamenti: %+v %+v", l1.Link, l2.Link)
	}
	if l1.Credentials.Username == l2.Credentials.Username || l1.Credentials.Password == l2.Credentials.Password {
		t.Fatal("ogni collegamento ha il suo ruolo e la sua password (DB9)")
	}
	if !hex32.MatchString(l1.Credentials.Password) || l1.Credentials.Host != databases.ServiceHost(uuid.UUID(e.db.Id)) || l1.Credentials.SslMode != "verify-full" || l1.Credentials.SslRootcert != "/etc/gitstack/db/ca.crt" {
		t.Fatalf("credenziali: %+v", l1.Credentials)
	}
	_ = apiApp

	// L'elenco mostra i collegamenti senza password; vale solo per chi e' admin dell'owner.
	rec = e.do(http.MethodGet, "/databases/alice/shop/links", "alice", "")
	e.want(rec, http.StatusOK, "")
	for _, secret := range []string{l1.Credentials.Password, l2.Credentials.Password} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("l'elenco non deve rivelare password: %s", rec.Body.String())
		}
	}
	var list openapi.DatabaseLinkList
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 2 {
		t.Fatalf("elenco: %+v", list)
	}
	e.want(e.do(http.MethodGet, "/databases/alice/shop/links", "bob", ""), http.StatusNotFound, "not_found")
	if d := e.getDB(); d.LinkCount != 2 {
		t.Fatalf("linkCount = %d", d.LinkCount)
	}

	// Il stesso collegamento due volte: l'app ha gia' le variabili.
	e.link("alice", "alice", "web", "rw", http.StatusConflict)
}

func (e *linkEnv) getDB() openapi.Database {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/databases/alice/shop", "alice", "")
	e.want(rec, http.StatusOK, "")
	var d openapi.Database
	_ = json.Unmarshal(rec.Body.Bytes(), &d)
	return d
}

// Admin sull'owner del database ma non sull'app (che bob vede solo in lettura): 403, nessun ruolo, app intatta.
func TestDatabaseLinks_ServeAdminSullApp(t *testing.T) {
	e := newLinkEnv(t)
	appID, _ := e.mkApp("alice", "web")
	scope := uuid.NewSHA1(uuid.MustParse("6b0c9f7e-3a52-4d0c-9a53-7a0c6f5d1d11"), []byte("databases:"+aliceID))
	e.id.grants[scope] = []string{users["bob"]}
	e.want(e.do(http.MethodGet, "/databases/alice/shop/links", "bob", ""), http.StatusOK, "")
	versions := e.versionCount(appID)
	e.link("bob", "alice", "web", "rw", http.StatusForbidden)
	if len(e.spy.roles) != 0 || e.versionCount(appID) != versions || len(e.secrets(appID)) != 0 {
		t.Fatal("il 403 ha lasciato tracce")
	}
}

func (e *linkEnv) repoID(name string) uuid.UUID {
	e.t.Helper()
	r, err := e.st.GetRepoByName(context.Background(), "alice", name)
	if err != nil {
		e.t.Fatal(err)
	}
	return r.ID
}

// DB10: le variabili stanno nei segreti dell'app (cifrati), il collegamento crea una versione e un deploy, e
// nessuna API mostra mai i valori. Le variabili gestite non si cambiano a mano.
func TestDatabaseLinks_VariabiliComeSegretiGestiti(t *testing.T) {
	e := newLinkEnv(t)
	appID, _ := e.mkApp("alice", "web")
	versions, deploys := e.versionCount(appID), e.deployCount(appID)
	c := e.link("alice", "alice", "web", "rw", http.StatusCreated)

	got := e.secrets(appID)
	for _, n := range store.ManagedDBEnvNames {
		if got[n] == "" {
			t.Errorf("manca %s tra i segreti dell'app", n)
		}
	}
	u, err := url.Parse(got["DATABASE_URL"])
	if err != nil {
		t.Fatal(err)
	}
	if pw, _ := u.User.Password(); pw != c.Credentials.Password || u.User.Username() != c.Credentials.Username || got["PGPASSWORD"] != c.Credentials.Password {
		t.Fatalf("DATABASE_URL non coerente con le credenziali: %s", got["DATABASE_URL"])
	}
	if got["PGSSLMODE"] != "verify-full" || got["PGSSLROOTCERT"] != "/etc/gitstack/db/ca.crt" || got["PGHOST"] != c.Credentials.Host || got["PGPORT"] != "5432" || got["PGDATABASE"] != "shop" {
		t.Fatalf("variabili PG*: %+v", got)
	}
	if e.versionCount(appID) != versions+1 || e.deployCount(appID) != deploys+1 {
		t.Fatalf("il collegamento crea una versione e un deploy: versioni %d->%d deploy %d->%d", versions, e.versionCount(appID), deploys, e.deployCount(appID))
	}
	// Mai in chiaro nel database di core (colonna cifrata) ne' nelle risposte.
	var plain int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.app_secrets WHERE app_id = $1 AND position(convert_to($2, 'UTF8') in ciphertext) > 0`, appID, c.Credentials.Password).Scan(&plain); err != nil {
		t.Fatal(err)
	}
	if plain != 0 {
		t.Fatal("la password sta in chiaro nella tabella dei segreti")
	}
	base := "/repos/alice/web/apps/web"
	for _, p := range []string{"/secrets", "/variables", "/versions", "", "/deploys"} {
		rec := e.do(http.MethodGet, base+p, "alice", "")
		if strings.Contains(rec.Body.String(), c.Credentials.Password) || strings.Contains(rec.Body.String(), "postgresql://") {
			t.Fatalf("GET %s rivela le credenziali: %s", p, rec.Body.String())
		}
	}
	rec := e.do(http.MethodGet, base+"/secrets", "alice", "")
	if !strings.Contains(rec.Body.String(), "DATABASE_URL") {
		t.Fatalf("i nomi dei segreti gestiti si vedono (i valori no): %s", rec.Body.String())
	}
	// Variabili gestite: non si riscrivono ne' si tolgono a mano.
	e.want(e.do(http.MethodPut, base+"/secrets/DATABASE_URL", "alice", `{"value":"x"}`), http.StatusConflict, "managed_by_database_link")
	e.want(e.do(http.MethodDelete, base+"/secrets/PGPASSWORD", "alice", ""), http.StatusConflict, "managed_by_database_link")
	e.want(e.do(http.MethodPut, base+"/variables/PGHOST", "alice", `{"value":"x"}`), http.StatusConflict, "name_in_use")
	if e.secrets(appID)["PGPASSWORD"] != c.Credentials.Password {
		t.Fatal("un tentativo rifiutato ha cambiato la variabile")
	}
}

// 409 variable_conflict: un nome gia' presente blocca il collegamento e l'app non si tocca (nessuna versione, nessun ruolo).
func TestDatabaseLinks_ConflittoDiNomeNonTocca(t *testing.T) {
	e := newLinkEnv(t)
	appID, _ := e.mkApp("alice", "web")
	base := "/repos/alice/web/apps/web"
	e.want(e.do(http.MethodPut, base+"/variables/PGHOST", "alice", `{"value":"mio"}`), http.StatusOK, "")
	e.want(e.do(http.MethodPut, base+"/secrets/PGUSER", "alice", `{"value":"mio"}`), http.StatusOK, "")
	versions, deploys := e.versionCount(appID), e.deployCount(appID)
	rec := e.do(http.MethodPost, "/databases/alice/shop/links", "alice", `{"app":{"owner":"alice","repo":"web","name":"web"},"mode":"rw"}`)
	e.want(rec, http.StatusConflict, "variable_conflict")
	if !strings.Contains(rec.Body.String(), "PGHOST") || !strings.Contains(rec.Body.String(), "PGUSER") {
		t.Fatalf("il 409 elenca i nomi in conflitto: %s", rec.Body.String())
	}
	if e.versionCount(appID) != versions || e.deployCount(appID) != deploys || len(e.spy.roles) != 0 || e.getDB().LinkCount != 0 {
		t.Fatal("il conflitto ha lasciato tracce: versione, deploy, ruolo o collegamento")
	}
	if got := e.secrets(appID); got["PGUSER"] != "mio" || len(got) != 1 {
		t.Fatalf("segreti toccati: %v", got)
	}
}

// DB9: il ruolo del link e' PROPRIO, senza CREATEROLE/CREATEDB; se non si crea non resta niente.
func TestDatabaseLinks_RuoloProprioESenzaTracceSeFallisce(t *testing.T) {
	e := newLinkEnv(t)
	appID, _ := e.mkApp("alice", "web")
	e.spy.failCreate = true
	versions := e.versionCount(appID)
	e.want(e.do(http.MethodPost, "/databases/alice/shop/links", "alice", `{"app":{"owner":"alice","repo":"web","name":"web"},"mode":"rw"}`), http.StatusInternalServerError, "")
	if e.versionCount(appID) != versions || len(e.secrets(appID)) != 0 || e.getDB().LinkCount != 0 {
		t.Fatal("ruolo non creato: nessuna versione, segreto o collegamento deve restare")
	}
	e.spy.failCreate = false
	c := e.link("alice", "alice", "web", "rw", http.StatusCreated)
	st := e.spy.statements()
	var create string
	for _, s := range st {
		if strings.Contains(s, "CREATE ROLE \""+c.Credentials.Username+"\"") {
			create = s
		}
	}
	if !strings.Contains(create, "LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE") || !strings.Contains(create, "GRANT \"shop_owner\" TO \""+c.Credentials.Username+"\"") ||
		!strings.Contains(create, "SET role = 'shop_owner'") {
		t.Fatalf("ruolo rw: %s", create)
	}
}

// DB12: la rotazione cambia la password del ruolo, termina le sessioni, riscrive i segreti e crea una versione
// con il suo deploy: l'app riparte con la nuova.
func TestDatabaseLinks_RotazioneRiscriveISegreti(t *testing.T) {
	e := newLinkEnv(t)
	appID, repoID := e.mkApp("alice", "web")
	c := e.link("alice", "alice", "web", "rw", http.StatusCreated)
	versions, deploys := e.versionCount(appID), e.deployCount(appID)
	path := "/databases/alice/shop/links/" + uuid.UUID(c.Link.Id).String() + "/rotate"
	stmtsBefore := len(e.spy.statements())

	// Admin dell'app ma non dell'owner: puo' ruotare. Un estraneo: 404.
	e.id.grants[repoID] = append(e.id.grants[repoID], users["bob"])
	e.want(e.do(http.MethodPost, path, "carol", ""), http.StatusNotFound, "not_found")
	rec := e.do(http.MethodPost, path, "bob", "")
	e.want(rec, http.StatusOK, "")
	var rot openapi.DatabaseLinkRotated
	_ = json.Unmarshal(rec.Body.Bytes(), &rot)
	if rot.Credentials.Password == c.Credentials.Password || !hex32.MatchString(rot.Credentials.Password) || rot.Credentials.Username != c.Credentials.Username {
		t.Fatalf("rotazione: %+v", rot.Credentials)
	}
	got := e.secrets(appID)
	if got["PGPASSWORD"] != rot.Credentials.Password || !strings.Contains(got["DATABASE_URL"], rot.Credentials.Password) || strings.Contains(got["DATABASE_URL"], c.Credentials.Password) {
		t.Fatalf("i segreti dell'app non hanno la password nuova: %v", got)
	}
	if e.versionCount(appID) != versions+1 || e.deployCount(appID) != deploys+1 {
		t.Fatal("la rotazione crea una versione e un deploy: l'app riparte")
	}
	alter := e.spy.index("ALTER ROLE \""+c.Credentials.Username+"\" PASSWORD '"+rot.Credentials.Password+"'", 0)
	term := e.spy.index("pg_terminate_backend", alter)
	if alter < 0 || term < 0 {
		t.Fatalf("serve ALTER ROLE PASSWORD poi pg_terminate_backend: %v", e.spy.statements())
	}
	for _, s := range e.spy.statements()[stmtsBefore:] {
		if strings.Contains(s, c.Credentials.Password) {
			t.Fatalf("la vecchia password non deve comparire in nessuno statement: %s", s)
		}
	}
	for _, p := range []string{"/versions", "/secrets", ""} {
		if body := e.do(http.MethodGet, "/repos/alice/web/apps/web"+p, "alice", "").Body.String(); strings.Contains(body, rot.Credentials.Password) {
			t.Fatalf("GET %s rivela la password", p)
		}
	}
	// Database non pronto: 422.
	if err := e.st.SetManagedDBStatus(context.Background(), uuid.UUID(e.db.Id), store.DBRestarting, nil, e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	e.want(e.do(http.MethodPost, path, "alice", ""), http.StatusUnprocessableEntity, "database_not_ready")
}

// DB12: ordine di scollegamento NOLOGIN -> pg_terminate_backend -> DROP OWNED/DROP ROLE -> solo dopo le
// variabili. Con sessioni che non si chiudono il collegamento resta `unlinking` e le variabili restano.
func TestDatabaseLinks_ScollegamentoNellOrdineDellADR(t *testing.T) {
	e := newLinkEnv(t)
	appID, repoID := e.mkApp("alice", "web")
	c := e.link("alice", "alice", "web", "rw", http.StatusCreated)
	role := c.Credentials.Username
	path := "/databases/alice/shop/links/" + uuid.UUID(c.Link.Id).String()
	versions := e.versionCount(appID)

	e.spy.stuck, e.spy.sessions = true, 2
	rec := e.do(http.MethodDelete, path, "alice", "")
	e.want(rec, http.StatusAccepted, "")
	var l openapi.DatabaseLink
	_ = json.Unmarshal(rec.Body.Bytes(), &l)
	if l.State != "unlinking" {
		t.Fatalf("stato = %s", l.State)
	}
	if e.spy.index("DROP ROLE", 0) >= 0 || e.spy.index("DROP OWNED", 0) >= 0 {
		t.Fatal("con sessioni aperte il ruolo non si elimina")
	}
	if e.secrets(appID)["PGPASSWORD"] != c.Credentials.Password || e.versionCount(appID) != versions {
		t.Fatal("finche' il ruolo esiste le variabili restano nell'app (DB12 passo 4)")
	}
	list := e.do(http.MethodGet, "/databases/alice/shop/links", "alice", "").Body.String()
	if !strings.Contains(list, `"state":"unlinking"`) {
		t.Fatalf("il collegamento resta unlinking: %s", list)
	}
	e.want(e.do(http.MethodPost, path+"/rotate", "alice", ""), http.StatusConflict, "link_unlinking")

	// Le sessioni si chiudono: il giro del controller (e anche un secondo DELETE) completa lo scollegamento.
	e.spy.stuck = false
	e.id.grants[repoID] = append(e.id.grants[repoID], users["bob"])
	d, err := e.st.GetManagedDBByID(context.Background(), uuid.UUID(e.db.Id))
	if err != nil {
		t.Fatal(err)
	}
	e.ctl.ResumeUnlinks(context.Background(), d)

	nologin := e.spy.index("ALTER ROLE \""+role+"\" NOLOGIN", 0)
	term := e.spy.index("pg_terminate_backend", nologin)
	dropOwned := e.spy.index("DROP OWNED BY \""+role+"\"", term)
	dropRole := e.spy.index("DROP ROLE \""+role+"\"", dropOwned)
	if nologin < 0 || term < 0 || dropOwned < 0 || dropRole < 0 {
		t.Fatalf("ordine NOLOGIN(%d) -> terminate(%d) -> DROP OWNED(%d) -> DROP ROLE(%d) non rispettato: %v", nologin, term, dropOwned, dropRole, e.spy.statements())
	}
	if got := e.secrets(appID); len(got) != 0 {
		t.Fatalf("lo scollegamento toglie le variabili: %v", got)
	}
	if e.versionCount(appID) != versions+1 {
		t.Fatalf("lo scollegamento crea una versione e un deploy")
	}
	if e.getDB().LinkCount != 0 {
		t.Fatal("collegamento rimasto")
	}
	// Idempotente: un secondo DELETE sullo stesso collegamento e' 404, non un errore.
	e.want(e.do(http.MethodDelete, path, "alice", ""), http.StatusNotFound, "not_found")
}

// Scollega chi ha admin sull'app o sull'owner; un estraneo no.
func TestDatabaseLinks_ScollegaAdminDellAppOOwner(t *testing.T) {
	e := newLinkEnv(t)
	_, repoID := e.mkApp("alice", "web")
	c := e.link("alice", "alice", "web", "ro", http.StatusCreated)
	path := "/databases/alice/shop/links/" + uuid.UUID(c.Link.Id).String()
	e.want(e.do(http.MethodDelete, path, "carol", ""), http.StatusNotFound, "not_found")
	e.id.grants[repoID] = append(e.id.grants[repoID], users["bob"])
	e.want(e.do(http.MethodDelete, path, "bob", ""), http.StatusAccepted, "")
	if e.getDB().LinkCount != 0 {
		t.Fatal("scollegamento dell'admin dell'app non riuscito")
	}
}

// DB19: eliminare il database scollega le app: perdono le variabili (nuova versione e deploy).
func TestDatabaseLinks_EliminazioneDelDatabaseTogliLeVariabili(t *testing.T) {
	e := newLinkEnv(t)
	appID, _ := e.mkApp("alice", "web")
	apiID, _ := e.mkApp("alice", "api")
	e.link("alice", "alice", "web", "rw", http.StatusCreated)
	e.link("alice", "alice", "api", "ro", http.StatusCreated)
	versions := e.versionCount(appID)
	e.want(e.do(http.MethodDelete, "/databases/alice/shop?confirm=alice/shop", "alice", ""), http.StatusNoContent, "")
	for _, id := range []uuid.UUID{appID, apiID} {
		if got := e.secrets(id); len(got) != 0 {
			t.Fatalf("l'app %s ha ancora variabili del database eliminato: %v", id, got)
		}
	}
	if e.versionCount(appID) != versions+1 {
		t.Fatal("l'eliminazione del database crea una versione per l'app scollegata")
	}
}

// DB9/DB12: eliminare l'app porta via il collegamento a cascata; il ruolo non resta orfano (e con lui le
// sue sessioni): il controller lo toglie nell'ordine dell'ADR.
func TestDatabaseLinks_RuoloDiUnAppEliminataSiTogliCol(t *testing.T) {
	e := newLinkEnv(t)
	e.mkApp("alice", "web")
	c := e.link("alice", "alice", "web", "rw", http.StatusCreated)
	e.want(e.do(http.MethodDelete, "/repos/alice/web/apps/web", "alice", ""), http.StatusNoContent, "")
	if e.getDB().LinkCount != 0 {
		t.Fatal("il collegamento doveva sparire con l'app")
	}
	if !e.spy.roles[c.Credentials.Username] {
		t.Fatal("prima della pulizia il ruolo c'e' ancora")
	}
	d, err := e.st.GetManagedDBByID(context.Background(), uuid.UUID(e.db.Id))
	if err != nil {
		t.Fatal(err)
	}
	e.ctl.SweepOrphanRoles(context.Background(), d)
	if e.spy.roles[c.Credentials.Username] {
		t.Fatalf("ruolo orfano non rimosso: %v", e.spy.statements())
	}
	nologin := e.spy.index("ALTER ROLE \""+c.Credentials.Username+"\" NOLOGIN", 0)
	term := e.spy.index("pg_terminate_backend", nologin)
	if nologin < 0 || term < 0 || e.spy.index("DROP ROLE \""+c.Credentials.Username+"\"", term) < 0 {
		t.Fatalf("la pulizia segue l'ordine di DB12: %v", e.spy.statements())
	}
	// Un ruolo con collegamento attivo non si tocca.
	e.mkApp("alice", "api")
	live := e.link("alice", "alice", "api", "ro", http.StatusCreated)
	e.spy.mu.Lock()
	e.spy.stmts = nil
	e.spy.mu.Unlock()
	e.ctl.SweepOrphanRoles(context.Background(), d)
	if !e.spy.roles[live.Credentials.Username] || e.spy.index("DROP ROLE", 0) >= 0 {
		t.Fatal("un ruolo con collegamento non si elimina")
	}
}
