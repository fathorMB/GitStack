//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/GitStack/services/core/internal/databases"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// sqlSpy e' un SQLRunner che registra gli statement (nessun Postgres: l'API non deve toccarlo se non per la rotazione).
type sqlSpy struct {
	mu    sync.Mutex
	stmts []string
}

func (s *sqlSpy) Exec(_ context.Context, _ store.ManagedDB, database, sql string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stmts = append(s.stmts, database+"| "+sql)
	return nil
}

func (s *sqlSpy) Query(context.Context, store.ManagedDB, string, string) ([]string, error) {
	return nil, nil
}

type dbEnv struct {
	*appsEnv
	kube  *fake.Clientset
	ctl   *databases.Controller
	sql   *sqlSpy
	proxy *lateController
}

func newDBEnv(t *testing.T, majors ...int) *dbEnv {
	t.Helper()
	kube := fake.NewSimpleClientset()
	sql := &sqlSpy{}
	proxy := &lateController{}
	holder := &dbEnv{kube: kube, sql: sql, proxy: proxy}
	holder.appsEnv = newAppsEnv(t, httpserver.WithDatabases(httpserver.DatabasesConfig{Controller: proxy, AllowedMajors: majors}))
	holder.ctl = databases.New(store.New(holder.pool), kube, databases.Config{SQL: sql, Images: map[int]databases.Image{}})
	proxy.set(holder.ctl)
	return holder
}

// lateController rimanda al controller vero, creato dopo il router (serve il pool del router).
type lateController struct {
	mu sync.Mutex
	c  *databases.Controller
	// beforeProvision interpone un giro del reconciler fra il commit della riga e Provision (test di DB22).
	beforeProvision func(context.Context, store.ManagedDB)
}

func (l *lateController) set(c *databases.Controller) { l.mu.Lock(); l.c = c; l.mu.Unlock() }
func (l *lateController) get() *databases.Controller  { l.mu.Lock(); defer l.mu.Unlock(); return l.c }
func (l *lateController) Provision(ctx context.Context, d store.ManagedDB, pw string) (string, error) {
	if l.beforeProvision != nil {
		l.beforeProvision(ctx, d)
	}
	return l.get().Provision(ctx, d, pw)
}
func (l *lateController) Delete(ctx context.Context, d store.ManagedDB, a *store.AppActor) error {
	return l.get().Delete(ctx, d, a)
}
func (l *lateController) RotateOwner(ctx context.Context, d store.ManagedDB) (string, error) {
	return l.get().RotateOwner(ctx, d)
}
func (l *lateController) Logs(ctx context.Context, d store.ManagedDB, o *corev1.PodLogOptions) (io.ReadCloser, error) {
	return l.get().Logs(ctx, d, o)
}
func (l *lateController) LinkApp(ctx context.Context, k store.AppSealer, d store.ManagedDB, a store.App, mode string, actor *store.AppActor) (store.ManagedDBLink, databases.LinkCredentials, error) {
	return l.get().LinkApp(ctx, k, d, a, mode, actor)
}
func (l *lateController) RotateLink(ctx context.Context, k store.AppSealer, d store.ManagedDB, link store.ManagedDBLink, actor *store.AppActor) (databases.LinkCredentials, error) {
	return l.get().RotateLink(ctx, k, d, link, actor)
}
func (l *lateController) Unlink(ctx context.Context, d store.ManagedDB, link store.ManagedDBLink, actor *store.AppActor) (bool, error) {
	return l.get().Unlink(ctx, d, link, actor)
}
func (l *lateController) SetLAN(ctx context.Context, d store.ManagedDB, enable bool, a *store.AppActor) (store.ManagedDB, error) {
	return l.get().SetLAN(ctx, d, enable, a)
}
func (l *lateController) LANInfo(ctx context.Context, d store.ManagedDB, host string) (databases.LANInfo, error) {
	return l.get().LANInfo(ctx, d, host)
}
func (l *lateController) Kick() { l.get().Kick() }

func (e *dbEnv) create(user, owner, body string, want int) openapi.DatabaseCreated {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/databases/"+owner, user, body)
	e.want(rec, want, "")
	var out openapi.DatabaseCreated
	if want == http.StatusAccepted {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			e.t.Fatalf("%v: %s", err, rec.Body.String())
		}
	}
	return out
}

var hex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)

// DB2: crea, elimina, ridimensiona e collega richiedono `admin` sull'owner; chi non lo e' non vede il
// database (404); l'amministratore dell'istanza vede tutto.
func TestDatabases_Permessi(t *testing.T) {
	e := newDBEnv(t)
	created := e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	if !hex32.MatchString(created.Credentials.Password) || created.Credentials.Username != "shop_owner" || created.Database.Status != "creating" {
		t.Fatalf("risposta di creazione: %+v", created)
	}
	// La password si vede solo alla creazione (DB22).
	rec := e.do(http.MethodGet, "/databases/alice/shop", "alice", "")
	e.want(rec, http.StatusOK, "")
	if strings.Contains(rec.Body.String(), created.Credentials.Password) || strings.Contains(strings.ToLower(rec.Body.String()), "password") {
		t.Fatalf("la lettura non deve rivelare password: %s", rec.Body.String())
	}
	// Chi non e' admin dell'owner: 404 su tutto, anche sul database che esiste.
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/databases/alice", ""}, {http.MethodGet, "/databases/alice/shop", ""},
		{http.MethodPost, "/databases/alice", `{"name":"altro","pgMajor":17}`},
		{http.MethodPatch, "/databases/alice/shop", `{"sizeBytes":2147483648}`},
		{http.MethodDelete, "/databases/alice/shop?confirm=alice/shop", ""},
		{http.MethodPost, "/databases/alice/shop/credentials/rotate", ""},
		{http.MethodGet, "/databases/alice/shop/metrics", ""}, {http.MethodGet, "/databases/alice/shop/logs", ""},
	} {
		e.want(e.do(c.method, c.path, "bob", c.body), http.StatusNotFound, "not_found")
	}
	// L'owner inesistente e' 404 come il database inesistente.
	e.want(e.do(http.MethodGet, "/databases/nessuno", "alice", ""), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodGet, "/databases/alice/inesistente", "alice", ""), http.StatusNotFound, "not_found")

	// Organizzazione: alice e' admin di acme, bob e carol no.
	e.create("alice", "acme", `{"name":"erp","pgMajor":16}`, http.StatusAccepted)
	e.want(e.do(http.MethodGet, "/databases/acme/erp", "alice", ""), http.StatusOK, "")
	e.want(e.do(http.MethodGet, "/databases/acme/erp", "bob", ""), http.StatusNotFound, "not_found")
	e.want(e.do(http.MethodPost, "/databases/acme", "carol", `{"name":"x","pgMajor":17}`), http.StatusNotFound, "not_found")
	// Un utente non puo' agire sull'owner di un altro utente nemmeno creando.
	e.want(e.do(http.MethodPost, "/databases/bob", "alice", `{"name":"x","pgMajor":17}`), http.StatusNotFound, "not_found")

	// L'elenco mostra solo i database dell'owner.
	var list openapi.DatabaseList
	rec = e.do(http.MethodGet, "/databases/alice", "alice", "")
	e.want(rec, http.StatusOK, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].Name != "shop" {
		t.Fatalf("elenco: %+v", list)
	}

	// L'amministratore dell'istanza vede tutto e puo' eliminare.
	e.id.sysAdmin[users["carol"]] = true
	e.want(e.do(http.MethodGet, "/databases/alice/shop", "carol", ""), http.StatusOK, "")
	e.want(e.do(http.MethodDelete, "/databases/alice/shop?confirm=alice/shop", "carol", ""), http.StatusNoContent, "")
}

// DB1: nome, major e valori; il nome e' unico per owner (name_conflict).
func TestDatabases_Validazione(t *testing.T) {
	e := newDBEnv(t, 17)
	e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	e.want(e.do(http.MethodPost, "/databases/alice", "alice", `{"name":"shop","pgMajor":17}`), http.StatusConflict, "name_conflict")
	// Lo stesso nome per un altro owner e' libero.
	e.create("alice", "acme", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	// Major non ammessa dall'amministratore.
	e.want(e.do(http.MethodPost, "/databases/alice", "alice", `{"name":"vecchio","pgMajor":16}`), http.StatusUnprocessableEntity, "pg_major_not_allowed")
	// Nome non valido: maiuscole, trattino ai bordi, troppo lungo, vuoto.
	for _, name := range []string{"Shop", "-shop", "shop-", "con_underscore", strings.Repeat("a", 31), ""} {
		rec := e.do(http.MethodPost, "/databases/alice", "alice", `{"name":"`+name+`","pgMajor":17}`)
		if rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
			t.Errorf("nome %q: risposta %d, voluto 400 o 422", name, rec.Code)
		}
	}
	// Valori non positivi, campi sconosciuti, corpo vuoto.
	for _, body := range []string{`{"name":"a","pgMajor":17,"sizeBytes":0}`, `{"name":"a","pgMajor":17,"cpuMillis":-1}`,
		`{"name":"a","pgMajor":17,"memoryBytes":0}`, `{"name":"a","pgMajor":17,"extra":1}`, `{}`, ``} {
		if rec := e.do(http.MethodPost, "/databases/alice", "alice", body); rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
			t.Errorf("corpo %q: risposta %d, voluto 400 o 422", body, rec.Code)
		}
	}
	// Ridimensionamento: almeno un campo, positivi.
	for _, body := range []string{`{}`, `{"sizeBytes":0}`, `{"cpuMillis":-5}`} {
		if rec := e.do(http.MethodPatch, "/databases/alice/shop", "alice", body); rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusBadRequest {
			t.Errorf("patch %q: risposta %d", body, rec.Code)
		}
	}
	// I default dell'ADR: 1 GiB, 100m, 256 MiB.
	d := e.create("alice", "alice", `{"name":"default","pgMajor":17}`, http.StatusAccepted).Database
	if d.SizeBytes != 1<<30 || d.CpuMillis != 100 || d.MemoryBytes != 256<<20 || d.LanEnabled || d.LinkCount != 0 {
		t.Fatalf("default: %+v", d)
	}
}

// DB3: CPU, RAM e disco dei database contano nella stessa quota dell'owner delle app; oltre il tetto 409
// insufficient_capacity con details.missing, e viceversa le app vedono l'uso dei database.
func TestDatabases_QuotaCondivisaConLeApp(t *testing.T) {
	e := newDBEnv(t)
	e.repo("uno", true)
	st := store.New(e.pool)
	if err := st.SetOwnerAppQuota(context.Background(), "alice", store.AppQuotaAmounts{CPUMillis: 1000, MemoryBytes: 4 << 30, DiskBytes: 2 << 30}); err != nil {
		t.Fatal(err)
	}
	app := `{"name":"web","image":"x","copies":2,"resources":{"guaranteed":{"cpuMillis":400,"memoryBytes":134217728},"limit":{"cpuMillis":800,"memoryBytes":268435456}}}`
	e.want(e.do(http.MethodPost, "/repos/alice/uno/apps", "alice", app), http.StatusAccepted, "") // 800 mCPU

	// 800 + 300 > 1000: mancano 100 mCPU.
	rec := e.do(http.MethodPost, "/databases/alice", "alice", `{"name":"big","pgMajor":17,"cpuMillis":300}`)
	e.want(rec, http.StatusConflict, "insufficient_capacity")
	var body struct {
		Error struct {
			Details struct {
				Missing map[string]int64 `json:"missing"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.Details.Missing["cpuMillis"] != 100 {
		t.Fatalf("details.missing: %s", rec.Body.String())
	}
	// Con 200 mCPU ci sta (1000 in tutto) e occupa 1 GiB di disco.
	e.create("alice", "alice", `{"name":"fits","pgMajor":17,"cpuMillis":200}`, http.StatusAccepted)
	// Disco: 1 GiB usato + 2 GiB > 2 GiB: manca 1 GiB (e la CPU non basta piu', ma il disco e' nel missing).
	rec = e.do(http.MethodPost, "/databases/alice", "alice", `{"name":"disk","pgMajor":17,"cpuMillis":1,"sizeBytes":2147483648}`)
	e.want(rec, http.StatusConflict, "insufficient_capacity")
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.Details.Missing["diskBytes"] != 1<<30 {
		t.Fatalf("details.missing del disco: %s", rec.Body.String())
	}
	// L'app dello stesso owner vede l'uso del database: altri 100 mCPU non ci stanno (1000 usati).
	e.want(e.do(http.MethodPost, "/repos/alice/uno/apps", "alice", strings.Replace(app, `"name":"web"`, `"name":"altra"`, 1)), http.StatusConflict, "insufficient_capacity")
	// Il ridimensionamento ricontrolla la quota sull'incremento: +100 mCPU non ci sta, +0 si'.
	e.want(e.do(http.MethodPatch, "/databases/alice/fits", "alice", `{"cpuMillis":300}`), http.StatusConflict, "insufficient_capacity")
	rec = e.do(http.MethodPatch, "/databases/alice/fits", "alice", `{"sizeBytes":2147483648}`)
	e.want(rec, http.StatusOK, "")
	var updated openapi.Database
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated.SizeBytes != 2<<30 || updated.Status != "creating" {
		t.Fatalf("dopo il ridimensionamento: %+v", updated)
	}
	// Un altro owner non ne risente.
	e.create("alice", "acme", `{"name":"erp","pgMajor":17,"cpuMillis":300}`, http.StatusAccepted)
}

// DB19/DB20: eliminazione con conferma `owner/nome`, nome libero alla risposta, namespace tolto, audit ed evento.
func TestDatabases_EliminazioneApi(t *testing.T) {
	e := newDBEnv(t)
	created := e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	ns := databases.Namespace(uuid.UUID(created.Database.Id))
	if _, err := e.kube.CoreV1().Namespaces().Get(context.Background(), ns, metav1.GetOptions{}); err != nil {
		t.Fatalf("namespace non creato: %v", err)
	}
	e.want(e.do(http.MethodDelete, "/databases/alice/shop", "alice", ""), http.StatusUnprocessableEntity, "confirmation_required")
	e.want(e.do(http.MethodDelete, "/databases/alice/shop?confirm=shop", "alice", ""), http.StatusUnprocessableEntity, "confirmation_mismatch")
	e.want(e.do(http.MethodDelete, "/databases/alice/shop?confirm=bob/shop", "alice", ""), http.StatusUnprocessableEntity, "confirmation_mismatch")
	// Nulla e' stato toccato.
	e.want(e.do(http.MethodGet, "/databases/alice/shop", "alice", ""), http.StatusOK, "")
	if _, err := e.kube.CoreV1().Namespaces().Get(context.Background(), ns, metav1.GetOptions{}); err != nil {
		t.Fatal("namespace toccato da una conferma errata")
	}
	e.want(e.do(http.MethodDelete, "/databases/alice/shop?confirm=alice/shop", "alice", ""), http.StatusNoContent, "")
	if _, err := e.kube.CoreV1().Namespaces().Get(context.Background(), ns, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("namespace ancora presente: %v", err)
	}
	e.want(e.do(http.MethodGet, "/databases/alice/shop", "alice", ""), http.StatusNotFound, "not_found")
	// Il nome si libera alla risposta.
	e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	var events int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.event_outbox WHERE name IN ('database.created','database.deleted')`).Scan(&events); err != nil || events != 3 {
		t.Fatalf("eventi: %d %v", events, err)
	}
	// Nessun segreto negli eventi (DB20).
	var leaked int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.event_outbox WHERE name LIKE 'database.%' AND payload::text ~* 'password|secret'`).Scan(&leaked)
	if leaked != 0 {
		t.Fatal("un payload di database.* contiene un segreto")
	}
}

// DB22: la rotazione mostra la password nuova una volta e la applica via SQL; non serve se il database non e' pronto.
func TestDatabases_RotazioneCredenziali(t *testing.T) {
	e := newDBEnv(t)
	created := e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	e.want(e.do(http.MethodPost, "/databases/alice/shop/credentials/rotate", "alice", ""), http.StatusUnprocessableEntity, "database_not_ready")
	st := store.New(e.pool)
	if err := st.SetManagedDBStatus(context.Background(), uuid.UUID(created.Database.Id), store.DBRunning, nil, e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	rec := e.do(http.MethodPost, "/databases/alice/shop/credentials/rotate", "alice", "")
	e.want(rec, http.StatusOK, "")
	var rot openapi.DatabaseRotated
	_ = json.Unmarshal(rec.Body.Bytes(), &rot)
	if !hex32.MatchString(rot.Credentials.Password) || rot.Credentials.Password == created.Credentials.Password {
		t.Fatalf("password ruotata: %q", rot.Credentials.Password)
	}
	e.sql.mu.Lock()
	joined := strings.Join(e.sql.stmts, "\n")
	e.sql.mu.Unlock()
	if !strings.Contains(joined, `ALTER ROLE "shop_owner" PASSWORD '`+rot.Credentials.Password+`'`) {
		t.Fatalf("SQL della rotazione: %s", joined)
	}
	sec, _ := e.kube.CoreV1().Secrets(databases.Namespace(uuid.UUID(created.Database.Id))).Get(context.Background(), databases.SecretAuth, metav1.GetOptions{})
	if string(sec.Data["owner-password"]) != rot.Credentials.Password {
		t.Fatal("il Secret deve avere la password nuova")
	}
	audit, _ := st.ManagedDBAudit(context.Background(), uuid.UUID(created.Database.Id))
	last := audit[len(audit)-1]
	if last.Action != "credentials_rotated" || strings.Contains(string(last.Detail), rot.Credentials.Password) {
		t.Fatalf("audit: %+v", last)
	}
}
