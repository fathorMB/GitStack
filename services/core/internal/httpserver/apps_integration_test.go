//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/repopurge"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
)

// GIT-342 (v3-a, H5/H8): modello dati e API delle app in core.

type appClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *appClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *appClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type appsEnv struct {
	*issuesEnv
	clock *appClock
	git   *fakeGit
}

func newAppsEnv(t *testing.T) *appsEnv {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	id := &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}
	e := &issuesEnv{t: t, pool: pool, id: id}
	git := newFakeGit()
	clock := &appClock{t: time.Now().UTC().Truncate(time.Second)}
	e.router = httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret,
		httpserver.WithRepoIdentity(id), httpserver.WithReadableLister(id), httpserver.WithGit(git),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}),
		httpserver.WithAppsDomain("apps.example.com"), httpserver.WithClock(clock.Now))
	return &appsEnv{issuesEnv: e, clock: clock, git: git}
}

// do firma con l'orologio finto: la richiesta resta valida anche dopo giorni di tempo simulato.
func (e *appsEnv) do(method, path, user, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	trust.Sign(req.Header, trustSecret, trust.Identity{UserID: users[user], Username: user}, e.clock.Now())
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *appsEnv) app(rec *httptest.ResponseRecorder) openapi.App {
	e.t.Helper()
	var a openapi.App
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		e.t.Fatalf("app non valida: %v: %s", err, rec.Body.String())
	}
	return a
}

func (e *appsEnv) versions(path, user string) openapi.AppVersionList {
	e.t.Helper()
	rec := e.do(http.MethodGet, path, user, "")
	e.want(rec, http.StatusOK, "")
	var l openapi.AppVersionList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
		e.t.Fatal(err)
	}
	return l
}

const createWeb = `{"name":"web","image":"sha-7f3a91c","port":8080}`

func TestApps_Permessi(t *testing.T) {
	e := newAppsEnv(t)
	repoID := e.repo("handbook", true) // internal: bob e carol leggono
	e.id.grantWrite(repoID, "bob")
	e.repo("riservato", false)
	base := "/repos/alice/handbook/apps"

	// write non crea; read nemmeno; il repo che non si legge è 404.
	e.want(e.do(http.MethodPost, base, "bob", createWeb), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodPost, base, "carol", createWeb), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodPost, "/repos/alice/riservato/apps", "carol", createWeb), http.StatusNotFound, "")
	e.want(e.do(http.MethodGet, "/repos/alice/riservato/apps", "carol", ""), http.StatusNotFound, "")

	// admin crea: 202 con app e versione 1; indirizzo automatico sotto il dominio delle app.
	rec := e.do(http.MethodPost, base, "alice", createWeb)
	e.want(rec, http.StatusAccepted, "")
	var created openapi.AppCreated
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.App.Name != "web" || created.App.Owner != "alice" || created.App.Repo != "handbook" || created.Version.Number != 1 ||
		created.Version.Trigger != openapi.AppVersionTriggerCreate || created.App.ActiveVersion != 1 {
		t.Fatalf("app creata: %s", rec.Body.String())
	}
	if created.App.Address == nil || *created.App.Address != "https://web-handbook-alice.apps.example.com" {
		t.Fatalf("indirizzo: %v", created.App.Address)
	}
	if created.App.Image.Reference != "git.example.com/alice/handbook:sha-7f3a91c" || created.App.Image.Source != openapi.AppImageSourceRegistry || created.App.Image.Verified {
		t.Fatalf("immagine: %+v", created.App.Image)
	}
	if created.App.CreatedBy == nil || created.App.CreatedBy.Username != "alice" {
		t.Fatalf("createdBy: %+v", created.App.CreatedBy)
	}

	// read e write vedono: elenco, app, versioni.
	for _, user := range []string{"carol", "bob"} {
		rec = e.do(http.MethodGet, base, user, "")
		e.want(rec, http.StatusOK, "")
		var l openapi.AppList
		if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil || len(l.Items) != 1 || l.Quota == nil || l.Quota.Used.CpuMillis != 500 {
			t.Fatalf("elenco di %s: %v %s", user, err, rec.Body.String())
		}
		e.want(e.do(http.MethodGet, base+"/web", user, ""), http.StatusOK, "")
		e.want(e.do(http.MethodGet, base+"/web/versions", user, ""), http.StatusOK, "")
		e.want(e.do(http.MethodGet, base+"/web/versions/1", user, ""), http.StatusOK, "")
	}
	// Nessun accesso a chi non vede il repo; un'app che non c'è è 404.
	e.want(e.do(http.MethodGet, "/repos/alice/riservato/apps/web", "carol", ""), http.StatusNotFound, "")
	e.want(e.do(http.MethodGet, base+"/non-c-e", "carol", ""), http.StatusNotFound, "not_found")

	// write e read non modificano né eliminano; admin sì.
	for _, user := range []string{"bob", "carol"} {
		e.want(e.do(http.MethodPatch, base+"/web", user, `{"port":9090}`), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodDelete, base+"/web", user, ""), http.StatusForbidden, "forbidden")
	}
	e.want(e.do(http.MethodDelete, "/repos/alice/riservato/apps/web", "carol", ""), http.StatusNotFound, "")
	e.want(e.do(http.MethodGet, base+"/web", "bob", ""), http.StatusOK, "")
	e.want(e.do(http.MethodDelete, base+"/web", "alice", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodGet, base+"/web", "alice", ""), http.StatusNotFound, "")
	e.want(e.do(http.MethodDelete, base+"/web", "alice", ""), http.StatusNotFound, "")
	// Eliminata l'app, spariscono le versioni (cascata).
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.app_versions`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("versioni dopo l'eliminazione: %d %v", n, err)
	}
}

func TestApps_ValidazioneConflittiEImmagineEsterna(t *testing.T) {
	e := newAppsEnv(t)
	e.repo("c", true)
	e.repo("b-c", true)
	base := "/repos/alice/c/apps"
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"Web","image":"x"}`), http.StatusUnprocessableEntity, "validation_failed")
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"web","image":"x","altro":1}`), http.StatusBadRequest, "")
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"web","image":"x","copies":0}`), http.StatusUnprocessableEntity, "validation_failed")
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"web","image":"x","resources":{"guaranteed":{"cpuMillis":500,"memoryBytes":100},"limit":{"cpuMillis":100,"memoryBytes":100}}}`), http.StatusUnprocessableEntity, "validation_failed")

	// Un'immagine esterna richiede comunque un repo ed è sempre «non verificata»; non si segue un branch.
	rec := e.do(http.MethodPost, base, "alice", `{"name":"ext","image":"docker.io/library/nginx:1.27"}`)
	e.want(rec, http.StatusAccepted, "")
	a := e.app(e.do(http.MethodGet, base+"/ext", "alice", ""))
	if a.Image.Source != openapi.AppImageSourceExternal || a.Image.Verified || a.Address != nil {
		t.Fatalf("immagine esterna: %+v address=%v", a.Image, a.Address)
	}
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"ext2","image":"docker.io/library/nginx:1.27","followBranch":"main"}`), http.StatusUnprocessableEntity, "validation_failed")
	e.want(e.do(http.MethodPost, "/repos/alice/non-esiste/apps", "alice", `{"name":"web","image":"docker.io/library/nginx:1.27"}`), http.StatusNotFound, "")

	// Nome già usato; indirizzo coincidente (app a-b in c e app a in b-c).
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"ext","image":"x","port":80}`), http.StatusConflict, "name_taken")
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"a-b","image":"x","port":80}`), http.StatusAccepted, "")
	e.want(e.do(http.MethodPost, "/repos/alice/b-c/apps", "alice", `{"name":"a","image":"x","port":80}`), http.StatusConflict, "address_conflict")
	// Etichetta DNS oltre 63 caratteri.
	e.repo(strings.Repeat("r", 40), true)
	e.want(e.do(http.MethodPost, "/repos/alice/"+strings.Repeat("r", 40)+"/apps", "alice",
		`{"name":"`+strings.Repeat("n", 25)+`","image":"x","port":80}`), http.StatusUnprocessableEntity, "address_too_long")
}

func TestApps_QuotaContataSuOwner(t *testing.T) {
	e := newAppsEnv(t)
	e.repo("uno", true)
	e.repo("due", true)
	// Quota dell'owner: 1000 mCPU garantiti.
	st := store.New(e.pool)
	if err := st.SetOwnerAppQuota(context.Background(), "alice", store.AppQuotaAmounts{CPUMillis: 1000, MemoryBytes: 4 << 30, DiskBytes: 1 << 30}); err != nil {
		t.Fatal(err)
	}
	big := `{"name":"big","image":"x","copies":2,"resources":{"guaranteed":{"cpuMillis":400,"memoryBytes":134217728},"limit":{"cpuMillis":800,"memoryBytes":268435456}}}`
	e.want(e.do(http.MethodPost, "/repos/alice/uno/apps", "alice", big), http.StatusAccepted, "") // 800
	// Il secondo repo dello stesso owner conta nella stessa quota: 800 + 400 > 1000.
	rec := e.do(http.MethodPost, "/repos/alice/due/apps", "alice", strings.Replace(big, `"copies":2`, `"copies":1`, 1))
	e.want(rec, http.StatusConflict, "insufficient_capacity")
	if !strings.Contains(rec.Body.String(), `"cpuMillis":200`) {
		t.Fatalf("details senza quanto manca: %s", rec.Body.String())
	}
	// Una piccola ci sta (800 + 100 = 900) e l'elenco mostra l'uso dell'owner.
	e.want(e.do(http.MethodPost, "/repos/alice/due/apps", "alice", `{"name":"small","image":"x","resources":{"guaranteed":{"cpuMillis":100,"memoryBytes":134217728},"limit":{"cpuMillis":100,"memoryBytes":134217728}}}`), http.StatusAccepted, "")
	rec = e.do(http.MethodGet, "/repos/alice/uno/apps", "alice", "")
	var l openapi.AppList
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil || l.Quota == nil || l.Quota.Used.CpuMillis != 900 || l.Quota.Limits.CpuMillis != 1000 || !l.Quota.Warning {
		t.Fatalf("quota: %v %s", err, rec.Body.String())
	}
	// Eliminare un'app libera la quota.
	e.want(e.do(http.MethodDelete, "/repos/alice/uno/apps/big", "alice", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodPost, "/repos/alice/due/apps", "alice", strings.Replace(big, `"copies":2`, `"copies":1`, 1)), http.StatusAccepted, "")
}

// H11: la riserva dell'istanza e la quota dell'owner sono entrambe controllate
// al punto di ingresso del deploy (creazione), mentre un tetto troppo alto si
// normalizza al massimo dell'istanza.
func TestApps_RiservaQuotaETetto(t *testing.T) {
	e := newAppsEnv(t)
	e.id.sysAdmin[users["carol"]] = true
	e.repo("uno", true)
	e.repo("due", true)
	// L'admin porta la riserva a 600 mCPU / 1 GiB; e' la capacita' globale,
	// quindi due owner diversi non possono aggirarla.
	e.want(e.do(http.MethodPut, "/admin/apps/reserve", "carol", `{"cpuMillis":600,"memoryBytes":1073741824}`), http.StatusOK, "")
	e.want(e.do(http.MethodPut, "/admin/apps/quotas/alice", "carol", `{"cpuMillis":500,"memoryBytes":2147483648,"diskBytes":0}`), http.StatusOK, "")
	first := `{"name":"first","image":"x","resources":{"guaranteed":{"cpuMillis":400,"memoryBytes":268435456},"limit":{"cpuMillis":9000,"memoryBytes":17179869184}}}`
	e.want(e.do(http.MethodPost, "/repos/alice/uno/apps", "alice", first), http.StatusAccepted, "")
	a := e.app(e.do(http.MethodGet, "/repos/alice/uno/apps/first", "alice", ""))
	if a.Resources.Limit.CpuMillis != store.DefaultAppMax.CPUMillis || a.Resources.Limit.MemoryBytes != store.DefaultAppMax.MemoryBytes {
		t.Fatalf("tetto non applicato: %+v", a.Resources.Limit)
	}
	// 400 + 300 supera la riserva (anche se la quota dell'owner basterebbe).
	rec := e.do(http.MethodPost, "/repos/alice/due/apps", "alice", `{"name":"second","image":"x","resources":{"guaranteed":{"cpuMillis":300,"memoryBytes":268435456},"limit":{"cpuMillis":300,"memoryBytes":268435456}}}`)
	e.want(rec, http.StatusConflict, "insufficient_capacity")
	if !strings.Contains(rec.Body.String(), "reserveCpuMillis") {
		t.Fatalf("rifiuto della riserva senza dettaglio: %s", rec.Body.String())
	}
	// A 80% esatto compare l'avviso nell'API di quota dell'admin, anche se il
	// disco non ha un limite (0 non e' una soglia raggiunta).
	var quotas openapi.AppOwnerQuotaList
	rec = e.do(http.MethodGet, "/admin/apps/quotas", "carol", "")
	e.want(rec, http.StatusOK, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &quotas); err != nil || len(quotas.Items) != 1 || !quotas.Items[0].Warning {
		t.Fatalf("avviso quota: %v %s", err, rec.Body.String())
	}
	if quotaWarning(store.AppQuotaAmounts{CPUMillis: 395}, store.AppQuotaAmounts{CPUMillis: 500, DiskBytes: 0}) {
		t.Fatal("79% con disco senza limite non deve avvisare")
	}
	if !quotaWarning(store.AppQuotaAmounts{CPUMillis: 400}, store.AppQuotaAmounts{CPUMillis: 500, DiskBytes: 0}) {
		t.Fatal("80% con disco senza limite deve avvisare")
	}
	e.want(e.do(http.MethodPut, "/admin/apps/quotas/sconosciuto", "carol", `{"cpuMillis":1,"memoryBytes":1,"diskBytes":0}`), http.StatusNotFound, "not_found")
}

// Ogni modifica di configurazione produce una versione numerata e consultabile (H8).
func TestApps_OgniModificaCreaUnaVersione(t *testing.T) {
	e := newAppsEnv(t)
	e.repo("handbook", true)
	base := "/repos/alice/handbook/apps/web"
	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", createWeb), http.StatusAccepted, "")

	e.clock.advance(time.Minute)
	a := e.app(e.do(http.MethodPatch, base, "alice", `{"port":9090}`))
	if a.Port == nil || *a.Port != 9090 {
		t.Fatalf("porta: %+v", a.Port)
	}
	e.clock.advance(time.Minute)
	e.want(e.do(http.MethodPatch, base, "alice", `{"healthCheck":{"path":"/healthz","intervalSeconds":5}}`), http.StatusOK, "")
	e.clock.advance(time.Minute)
	e.want(e.do(http.MethodPatch, base, "alice", `{"restricted":true}`), http.StatusOK, "")

	// Nessun cambiamento che entra in una versione: nessuna versione nuova (anche con valori uguali).
	e.want(e.do(http.MethodPatch, base, "alice", `{"port":9090}`), http.StatusOK, "")
	a = e.app(e.do(http.MethodPatch, base, "alice", `{"followBranch":"main","autoDeployPaused":true}`))
	if a.Follow == nil || a.Follow.Branch != "main" || !a.Follow.Paused {
		t.Fatalf("follow: %+v", a.Follow)
	}
	a = e.app(e.do(http.MethodPatch, base, "alice", `{"followBranch":null}`))
	if a.Follow != nil {
		t.Fatalf("followBranch null non toglie il branch: %+v", a.Follow)
	}
	// Validazione del corpo.
	e.want(e.do(http.MethodPatch, base, "alice", `{}`), http.StatusBadRequest, "")
	e.want(e.do(http.MethodPatch, base, "alice", `{"name":"altro"}`), http.StatusBadRequest, "")
	e.want(e.do(http.MethodPatch, base, "alice", `{"port":70000}`), http.StatusUnprocessableEntity, "validation_failed")
	e.want(e.do(http.MethodPatch, base, "alice", `{"healthCheck":{"path":"healthz","intervalSeconds":5}}`), http.StatusUnprocessableEntity, "validation_failed")

	l := e.versions(base+"/versions", "carol")
	if l.Total != 4 || len(l.Items) != 4 {
		t.Fatalf("versioni: %d", l.Total)
	}
	// Dalla più recente; numerate di seguito; ogni versione ha la sua configurazione e chi l'ha fatta.
	for i, want := range []int{4, 3, 2, 1} {
		if l.Items[i].Number != want {
			t.Fatalf("ordine: %d al posto %d", l.Items[i].Number, i)
		}
	}
	v1 := e.versionOf(base+"/versions/1", "carol")
	v2 := e.versionOf(base+"/versions/2", "carol")
	v3 := e.versionOf(base+"/versions/3", "carol")
	v4 := e.versionOf(base+"/versions/4", "carol")
	if *v1.Config.Port != 8080 || v1.Config.HealthCheck != nil || *v1.Config.Restricted {
		t.Fatalf("v1 cambiata: %+v", v1.Config)
	}
	if *v2.Config.Port != 9090 || v2.Trigger != openapi.AppVersionTriggerConfig || v2.Summary == nil || *v2.Summary != "porta" {
		t.Fatalf("v2: %+v", v2)
	}
	if v3.Config.HealthCheck == nil || v3.Config.HealthCheck.Path != "/healthz" || *v3.Config.Port != 9090 {
		t.Fatalf("v3: %+v", v3.Config)
	}
	if !*v4.Config.Restricted || v4.CreatedBy == nil || v4.CreatedBy.Username != "alice" || !v4.CreatedAt.After(v1.CreatedAt) {
		t.Fatalf("v4: %+v", v4)
	}
	if v4.Image.Reference != v1.Image.Reference || v2.Config.SecretNames == nil || len(v2.Config.Variables) != 0 {
		t.Fatalf("immagine o segreti: %+v", v4)
	}
	// Il numero successivo non si riusa nemmeno se l'app resta com'è; versione che non c'è: 404.
	e.want(e.do(http.MethodGet, base+"/versions/9", "carol", ""), http.StatusNotFound, "not_found")
	// Paginazione.
	if p := e.versions(base+"/versions?perPage=2&page=2", "carol"); p.Total != 4 || len(p.Items) != 2 || p.Items[0].Number != 2 {
		t.Fatalf("pagina 2: %+v", p)
	}
	// Ogni versione ha un deploy nello storico: l'ultimo è quello della versione 4.
	got := e.app(e.do(http.MethodGet, base, "carol", ""))
	if got.LastDeploy == nil || got.LastDeploy.Version != 4 || got.LastDeploy.State != openapi.AppDeployStateInProgress {
		t.Fatalf("ultimo deploy: %+v", got.LastDeploy)
	}
}

func (e *appsEnv) versionOf(path, user string) openapi.AppVersion {
	e.t.Helper()
	rec := e.do(http.MethodGet, path, user, "")
	e.want(rec, http.StatusOK, "")
	var v openapi.AppVersion
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || v.Config == nil {
		e.t.Fatalf("versione: %v %s", err, rec.Body.String())
	}
	return v
}

// R10: archiviato, le app si fermano e non accettano modifiche; riattivato, tornano avviabili.
func TestApps_RepoArchiviato(t *testing.T) {
	e := newAppsEnv(t)
	repoID := e.repo("handbook", true)
	base := "/repos/alice/handbook/apps"
	e.want(e.do(http.MethodPost, base, "alice", createWeb), http.StatusAccepted, "")
	e.sql(`UPDATE core.apps SET status = 'running', ready_copies = 1`)
	if a := e.app(e.do(http.MethodGet, base+"/web", "carol", "")); a.Status != openapi.AppStatusRunning || a.StoppedReason != nil || a.ReadyCopies != 1 {
		t.Fatalf("prima: %+v", a)
	}

	e.sql(`UPDATE core.repositories SET archived_at = $2 WHERE resource_id = $1`, repoID, e.clock.Now())
	a := e.app(e.do(http.MethodGet, base+"/web", "carol", ""))
	if a.Status != openapi.AppStatusStopped || a.StoppedReason == nil || *a.StoppedReason != openapi.AppStoppedReasonRepoArchived || a.ReadyCopies != 0 {
		t.Fatalf("archiviato: %+v", a)
	}
	rec := e.do(http.MethodGet, base, "carol", "")
	if !strings.Contains(rec.Body.String(), `"stoppedReason":"repo_archived"`) {
		t.Fatalf("elenco: %s", rec.Body.String())
	}
	// Letture sì, modifiche no (409 archived), anche per admin.
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"altra","image":"x"}`), http.StatusConflict, "archived")
	e.want(e.do(http.MethodPatch, base+"/web", "alice", `{"port":9090}`), http.StatusConflict, "archived")
	e.want(e.do(http.MethodDelete, base+"/web", "alice", ""), http.StatusConflict, "archived")
	e.want(e.do(http.MethodGet, base+"/web/versions", "carol", ""), http.StatusOK, "")

	// Riattivazione: l'app torna com'era, avviabile.
	e.sql(`UPDATE core.repositories SET archived_at = NULL WHERE resource_id = $1`, repoID)
	a = e.app(e.do(http.MethodGet, base+"/web", "carol", ""))
	if a.Status != openapi.AppStatusRunning || a.StoppedReason != nil || a.ReadyCopies != 1 {
		t.Fatalf("riattivato: %+v", a)
	}
	e.want(e.do(http.MethodPatch, base+"/web", "alice", `{"port":9090}`), http.StatusOK, "")
}

func (e *appsEnv) sql(q string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), q, args...); err != nil {
		e.t.Fatal(err)
	}
}

type noIdentityPurge struct{}

func (noIdentityPurge) PurgeResource(context.Context, uuid.UUID) error { return nil }

// R2: repo eliminato, le app si fermano subito; tornano se il repo è ripristinato entro 7 giorni;
// scaduti i 7 giorni il purge le elimina. Orologio finto.
func TestApps_RepoEliminatoRipristinoEPurge(t *testing.T) {
	e := newAppsEnv(t)
	repoID := e.repo("handbook", true)
	base := "/repos/alice/handbook/apps"
	e.want(e.do(http.MethodPost, base, "alice", createWeb), http.StatusAccepted, "")
	e.sql(`UPDATE core.apps SET status = 'running', ready_copies = 1`)
	st := store.New(e.pool)
	ctx := context.Background()
	stateOf := func() (string, string) {
		apps, err := st.ListApps(ctx, repoID)
		if err != nil || len(apps) != 1 {
			t.Fatalf("app del repo: %v %d", err, len(apps))
		}
		s, r := apps[0].Effective()
		if r == nil {
			return s, ""
		}
		return s, *r
	}

	// Eliminato: ferme subito (la API non le mostra più, il repo è sparito), con motivo repo_deleted.
	e.want(e.do(http.MethodDelete, "/repos/alice/handbook", "alice", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodGet, base, "alice", ""), http.StatusNotFound, "")
	if s, r := stateOf(); s != "stopped" || r != "repo_deleted" {
		t.Fatalf("subito dopo l'eliminazione: %s %s", s, r)
	}

	// Il giorno 6 il repo si ripristina: l'app torna avviabile e visibile con lo stato di prima.
	e.clock.advance(6 * 24 * time.Hour)
	e.want(e.do(http.MethodPost, "/repos/deleted/"+repoID.String()+"/restore", "alice", ""), http.StatusOK, "")
	if s, r := stateOf(); s != "running" || r != "" {
		t.Fatalf("dopo il ripristino: %s %s", s, r)
	}
	a := e.app(e.do(http.MethodGet, base+"/web", "carol", ""))
	if a.Status != openapi.AppStatusRunning || a.StoppedReason != nil || a.ActiveVersion != 1 {
		t.Fatalf("app dopo il ripristino: %+v", a)
	}
	if l := e.versions(base+"/web/versions", "carol"); l.Total != 1 {
		t.Fatalf("versioni dopo il ripristino: %d", l.Total)
	}

	// Eliminato di nuovo: oltre i 7 giorni il ripristino non è più possibile e il purge elimina app e versioni.
	e.want(e.do(http.MethodDelete, "/repos/alice/handbook", "alice", ""), http.StatusNoContent, "")
	deletedAt := e.clock.Now()
	e.clock.advance(7*24*time.Hour - time.Hour)
	job := &repopurge.Job{Store: st, Git: e.git, Identity: noIdentityPurge{}, Now: e.clock.Now}
	if n, err := job.RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("purge prima dei 7 giorni: %d %v", n, err)
	}
	if s, r := stateOf(); s != "stopped" || r != "repo_deleted" {
		t.Fatalf("a 6 giorni e 23 ore: %s %s", s, r)
	}
	e.clock.advance(2 * time.Hour) // 7 giorni e un'ora dall'eliminazione
	if got := e.clock.Now().Sub(deletedAt); got < store.Retention {
		t.Fatalf("orologio: %v", got)
	}
	e.want(e.do(http.MethodPost, "/repos/deleted/"+repoID.String()+"/restore", "alice", ""), http.StatusNotFound, "")
	// Il purge confronta con deleted_at scritto dal database (l'API usa l'orologio finto): lo si porta
	// all'istante finto, così il confronto è tutto sul tempo simulato.
	e.sql(`UPDATE core.repositories SET deleted_at = $2 WHERE resource_id = $1`, repoID, deletedAt)
	if n, err := job.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("purge dopo i 7 giorni: %d %v", n, err)
	}
	for _, table := range []string{"apps", "app_versions", "app_deploys"} {
		var n int
		if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM core.`+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s dopo il purge: %d %v", table, n, err)
		}
	}
}

// H2: l'indirizzo automatico arriva anche se la porta viene data dopo la creazione; togliere la
// porta lascia l'etichetta riservata; gli stessi controlli della creazione valgono per il PATCH.
func TestApps_IndirizzoDopoLaCreazione(t *testing.T) {
	e := newAppsEnv(t)
	e.repo("handbook", true)
	e.repo("c", true)
	e.repo("b-c", true)
	base := "/repos/alice/handbook/apps/worker"

	e.want(e.do(http.MethodPost, "/repos/alice/handbook/apps", "alice", `{"name":"worker","image":"x"}`), http.StatusAccepted, "")
	if a := e.app(e.do(http.MethodGet, base, "alice", "")); a.Address != nil {
		t.Fatalf("app senza porta con indirizzo: %v", *a.Address)
	}
	e.clock.advance(time.Minute)
	a := e.app(e.do(http.MethodPatch, base, "alice", `{"port":8080}`))
	if a.Address == nil || *a.Address != "https://worker-handbook-alice.apps.example.com" {
		t.Fatalf("indirizzo dopo il PATCH: %v", a.Address)
	}
	if l := e.versions(base+"/versions", "alice"); l.Total != 2 || l.Items[0].Trigger != openapi.AppVersionTriggerConfig {
		t.Fatalf("versioni dopo il PATCH: %+v", l)
	}
	// Togliere la porta lascia l'etichetta riservata (l'indirizzo resta).
	a = e.app(e.do(http.MethodPatch, base, "alice", `{"port":null}`))
	if a.Port != nil || a.Address == nil || *a.Address != "https://worker-handbook-alice.apps.example.com" {
		t.Fatalf("dopo aver tolto la porta: port=%v address=%v", a.Port, a.Address)
	}
	if l := e.versions(base+"/versions", "alice"); l.Total != 3 {
		t.Fatalf("versioni dopo aver tolto la porta: %d", l.Total)
	}

	// Conflitto: a-b in c e a in b-c danno la stessa etichetta.
	e.want(e.do(http.MethodPost, "/repos/alice/c/apps", "alice", `{"name":"a-b","image":"x","port":80}`), http.StatusAccepted, "")
	e.want(e.do(http.MethodPost, "/repos/alice/b-c/apps", "alice", `{"name":"a","image":"x"}`), http.StatusAccepted, "")
	e.want(e.do(http.MethodPatch, "/repos/alice/b-c/apps/a", "alice", `{"port":80}`), http.StatusConflict, "address_conflict")
	if a := e.app(e.do(http.MethodGet, "/repos/alice/b-c/apps/a", "alice", "")); a.Port != nil || a.Address != nil {
		t.Fatalf("PATCH respinto ma app cambiata: %+v", a)
	}
	if l := e.versions("/repos/alice/b-c/apps/a/versions", "alice"); l.Total != 1 {
		t.Fatalf("versione creata da un PATCH respinto: %d", l.Total)
	}

	// Etichetta oltre 63 caratteri.
	long := strings.Repeat("r", 40)
	e.repo(long, true)
	e.want(e.do(http.MethodPost, "/repos/alice/"+long+"/apps", "alice", `{"name":"`+strings.Repeat("n", 25)+`","image":"x"}`), http.StatusAccepted, "")
	e.want(e.do(http.MethodPatch, "/repos/alice/"+long+"/apps/"+strings.Repeat("n", 25), "alice", `{"port":80}`), http.StatusUnprocessableEntity, "address_too_long")
}
