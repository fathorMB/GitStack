//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/GitStack/services/core/internal/databases"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
)

// DB20 (GIT-568): la famiglia database.* arriva come webhook `database` ai webhook
// d'organizzazione dell'owner iscritti (C6), firmata come gli altri, con il payload di
// docs/events.md: nessun segreto (mai password, Secret, DATABASE_URL) in nessun campo.

type dbHookEnv struct {
	*dbEnv
	hid    *hookIdentity
	recv   *receiver
	doer   *toServer
	engine *webhooks.Engine
}

func newDBHookEnv(t *testing.T) *dbHookEnv {
	t.Helper()
	kube := fake.NewSimpleClientset()
	sql := &sqlSpy{}
	proxy := &lateController{}
	holder := &dbEnv{kube: kube, sql: sql, proxy: proxy}
	// L'identità con OrgOwners serve a creare il webhook sull'organizzazione (ruolo owner, C6).
	base := &issuesIdentity{fakeIdentity: newFakeIdentity(), writers: map[uuid.UUID][]string{}}
	hid := &hookIdentity{issuesIdentity: base}
	holder.appsEnv = newAppsEnv(t,
		httpserver.WithDatabases(httpserver.DatabasesConfig{Controller: proxy}),
		httpserver.WithRepoIdentity(hid), httpserver.WithReadableLister(hid),
		httpserver.WithWebhooks(httpserver.WebhookConfig{Keys: hookKeys(t), CheckURL: hookCheckURL(t)}),
	)
	holder.ctl = databases.New(store.New(holder.pool), kube, databases.Config{SQL: sql, Images: map[int]databases.Image{}})
	proxy.set(holder.ctl)
	recv := newReceiver(t)
	u, _ := url.Parse(recv.srv.URL)
	doer := &toServer{base: u}
	return &dbHookEnv{dbEnv: holder, hid: hid, recv: recv, doer: doer,
		engine: &webhooks.Engine{Pool: holder.pool, Keys: hookKeys(t), HTTP: doer, Users: hid, Now: time.Now}}
}

func hookKeys(t *testing.T) *webhooks.Keyring {
	t.Helper()
	keys, err := webhooks.NewKeyring("k1", hookSecretKey, "")
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func hookCheckURL(t *testing.T) func(string) error {
	t.Helper()
	_, check, err := webhooks.NewEgress(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return check
}

// run elabora l'outbox e consegna come fa il motore in produzione.
func (e *dbHookEnv) run() {
	e.t.Helper()
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		n, err := e.engine.ProcessOutbox(ctx)
		if err != nil {
			e.t.Fatalf("ProcessOutbox: %v", err)
		}
		if n == 0 {
			break
		}
	}
	for i := 0; i < 20; i++ {
		n, err := e.engine.DeliverDue(ctx)
		if err != nil {
			e.t.Fatalf("DeliverDue: %v", err)
		}
		if n == 0 {
			break
		}
	}
}

// TestDatabases_WebhookConsegnaSenzaSegreti crea, ridimensiona, accende e spegne la LAN ed elimina
// un database di un'organizzazione SENZA repository e verifica le consegne: azioni, schema del
// payload e divieto di segreti. Il matching va per org_id nell'evento, non per i repo dell'org.
func TestDatabases_WebhookConsegnaSenzaSegreti(t *testing.T) {
	e := newDBHookEnv(t)
	// alice è owner dell'organizzazione acme. L'org NON ha nessun repository: i destinatari si
	// risolvono da ownerId/ownerType dell'evento (anche per database.deleted, quando la riga
	// non c'è più).
	e.hid.orgOwner[acmeID] = []string{aliceID, bobID}
	rec := e.do(http.MethodPost, "/orgs/acme/hooks", "alice", fmt.Sprintf(`{"url":%q,"events":["database"],"secret":"s3cr3t"}`, hookURL))
	e.want(rec, http.StatusCreated, "")
	var repos int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.repositories`).Scan(&repos); err != nil || repos != 0 {
		t.Fatalf("il test vuole un'org senza repo: %d %v", repos, err)
	}

	// Creazione: 202 con la password una tantum (DB22): non deve finire MAI nel webhook.
	created := e.create("alice", "acme", `{"name":"erp","pgMajor":16}`, http.StatusAccepted)
	dbID := uuid.UUID(created.Database.Id)
	e.run()

	// Ridimensionamento.
	e.want(e.do(http.MethodPatch, "/databases/acme/erp", "alice", `{"cpuMillis":200}`), http.StatusOK, "")
	// LAN: accesa (con la porta assegnata nell'evento, docs/events.md) e poi spenta (porta assente).
	// Si pubblica dallo store come fa il PUT /lan (DB16): il controller non tocca il cluster qui.
	aliceActor := &store.AppActor{ID: uuid.MustParse(aliceID), Username: "alice", Type: "human"}
	st := store.New(e.pool)
	lan, err := st.SetManagedDBLAN(context.Background(), dbID, true, store.LANPortPolicy{From: 15432, To: 15440}, aliceActor, e.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if lan.LANPort == nil {
		t.Fatal("la LAN accesa deve avere una porta")
	}
	if _, err := st.SetManagedDBLAN(context.Background(), dbID, false, store.LANPortPolicy{From: 15432, To: 15440}, aliceActor, e.clock.Now()); err != nil {
		t.Fatal(err)
	}
	// Eliminazione (conferma owner/nome).
	e.want(e.do(http.MethodDelete, "/databases/acme/erp?confirm=acme/erp", "alice", ""), http.StatusNoContent, "")
	e.run()

	got := e.recv.requests()
	if len(got) != 5 {
		t.Fatalf("consegne: %d", len(got))
	}

	type payload struct {
		Version int    `json:"version"`
		Event   string `json:"event"`
		Action  string `json:"action"`
		Time    string `json:"time"`
		Sender  struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"sender"`
		Database struct {
			ID      string `json:"id"`
			Owner   string `json:"owner"`
			Name    string `json:"name"`
			PgMajor int    `json:"pgMajor"`
		} `json:"database"`
		SizeBytes   *int64          `json:"sizeBytes"`
		CPUMillis   *int            `json:"cpuMillis"`
		MemoryBytes *int64          `json:"memoryBytes"`
		Changes     json.RawMessage `json:"changes"`
		DeletedAt   string          `json:"deletedAt"`
		Port        *int            `json:"port"`
	}
	// Le consegne partono con worker paralleli: l'ordine di arrivo non e' garantito; si abbinano per azione.
	byAction := map[string]payload{}
	for i, req := range got {
		if h := req.header.Get("X-GitStack-Event"); h != "database" {
			t.Fatalf("consegna %d: X-GitStack-Event %q", i, h)
		}
		if req.header.Get("X-GitStack-Signature") == "" {
			t.Fatalf("consegna %d: firma assente", i)
		}
		var p payload
		if err := json.Unmarshal(req.body, &p); err != nil {
			t.Fatalf("consegna %d: %v: %s", i, err, req.body)
		}
		if byAction[p.Action].Event != "" {
			t.Fatalf("azione %s duplicata", p.Action)
		}
		byAction[p.Action] = p

		// DB20: nessun segreto in nessun campo. La password una tantum della creazione, la
		// parola «password» e ogni riferimento a Secret o DATABASE_URL non compaiono mai.
		body := strings.ToLower(string(req.body))
		for _, banned := range []string{
			strings.ToLower(created.Credentials.Password),
			"password", "secret", "database_url", "owner-password",
		} {
			if strings.Contains(body, banned) {
				t.Fatalf("consegna %d: il payload contiene %q: %s", i, banned, req.body)
			}
		}
	}
	if len(byAction) != 5 {
		t.Fatalf("azioni mancanti: %+v", byAction)
	}

	dbIDStr := dbID.String()
	for _, action := range []string{"created", "updated", "lan_enabled", "lan_disabled", "deleted"} {
		p := byAction[action]
		if p.Version != 1 || p.Event != "database" || p.Action != action {
			t.Fatalf("%s: corpo comune %+v", action, p)
		}
		if p.Time == "" || p.Sender.Username != "alice" || p.Sender.ID != aliceID {
			t.Fatalf("%s: corpo comune %+v", action, p)
		}
		if p.Database.ID != dbIDStr || p.Database.Owner != "acme" || p.Database.Name != "erp" || p.Database.PgMajor != 16 {
			t.Fatalf("%s: database %+v", action, p.Database)
		}
	}
	p := byAction["created"]
	if p.SizeBytes == nil || *p.SizeBytes != 1<<30 || p.CPUMillis == nil || *p.CPUMillis != 100 || p.MemoryBytes == nil {
		t.Fatalf("created: risorse %+v", p)
	}
	p = byAction["updated"]
	var ch map[string]map[string]int64
	if err := json.Unmarshal(p.Changes, &ch); err != nil {
		t.Fatalf("updated: changes %v", err)
	}
	if ch["cpuMillis"]["from"] != 100 {
		t.Fatalf("updated: changes %+v", ch)
	}
	// LAN: la porta dell'evento lan_enabled è quella assegnata dal modello; con la LAN spenta la
	// porta è assente (docs/events.md: assente con la LAN spenta, DB16).
	if p := byAction["lan_enabled"]; p.Port == nil || *p.Port != *lan.LANPort {
		t.Fatalf("lan_enabled: port %+v, attesa %d", p.Port, *lan.LANPort)
	}
	if p := byAction["lan_disabled"]; p.Port != nil {
		t.Fatalf("lan_disabled: la porta deve essere assente: %+v", p.Port)
	}
	if byAction["deleted"].DeletedAt == "" {
		t.Fatal("deleted: deletedAt vuoto")
	}

	// Outbox elaborato tutto: una consegna per evento, nessun residuo.
	var outbox int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.event_outbox WHERE webhooked_at IS NULL`).Scan(&outbox); err != nil || outbox != 0 {
		t.Fatalf("outbox non svuotato: %d %v", outbox, err)
	}

	// Un database di un utente (non di un'organizzazione) non ha superficie webhook C6:
	// l'evento si elabora senza consegne e senza errori.
	e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	e.run()
	if n := len(e.recv.requests()); n != 5 {
		t.Fatalf("il database di un utente non deve avere consegne: %d", n)
	}
}

// DB13, DB15, DB20 (GIT-572): backup completato/fallito e ripristino pubblicano — nello stesso
// evento di dominio e nel webhook — i campi di docs/events.md: backup{id, kind, sizeBytes?},
// error solo per backup_failed, backupId e target (same/new) per restored. Nessun segreto in
// nessun corpo: l'error non contiene mai password né DATABASE_URL.
func TestDatabases_WebhookBackupERipristino(t *testing.T) {
	e := newDBHookEnv(t)
	e.hid.orgOwner[acmeID] = []string{aliceID, bobID}
	rec := e.do(http.MethodPost, "/orgs/acme/hooks", "alice", fmt.Sprintf(`{"url":%q,"events":["database"],"secret":"s3cr3t"}`, hookURL))
	e.want(rec, http.StatusCreated, "")

	// Controller con chiave, cartella e dump finto; il fake kube e' quello condiviso, cosi' il
	// test marca pronti gli StatefulSet come fa database_links_restore_integration_test.go.
	dumper := &apiDumper{}
	e.withBackups(t, dumper, map[int]databases.Image{16: {Repository: "docker.io/library/postgres", Tag: "16-alpine", Digest: "sha256:" + strings.Repeat("a", 64)}})
	created := e.create("alice", "acme", `{"name":"erp","pgMajor":16}`, http.StatusAccepted)
	dbID := uuid.UUID(created.Database.Id)
	erpID := dbID.String()
	st := store.New(e.pool)
	ctx := context.Background()

	reconcile := func(owner, name string) {
		t.Helper()
		d, err := st.GetManagedDB(ctx, owner, name)
		if err != nil {
			t.Fatal(err)
		}
		e.proxy.get().Reconcile(ctx, d)
	}
	markReady := func(id uuid.UUID) {
		t.Helper()
		sts, err := e.kube.AppsV1().StatefulSets(databases.Namespace(id)).Get(ctx, databases.WorkloadName, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		sts.Status.ReadyReplicas, sts.Status.UpdatedReplicas = 1, 1
		if _, err := e.kube.AppsV1().StatefulSets(databases.Namespace(id)).UpdateStatus(ctx, sts, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	// Il database arriva a `running` come nei test del ripristino: due giri di riconciliazione.
	reconcile("acme", "erp")
	markReady(dbID)
	reconcile("acme", "erp")
	if d, _ := st.GetManagedDB(ctx, "acme", "erp"); d.Status != store.DBRunning {
		t.Fatalf("stato %s (%v)", d.Status, d.StatusReason)
	}

	// outboxEvents ritorna i payload di dominio di un evento, in ordine di pubblicazione.
	outboxEvents := func(name string) []map[string]any {
		t.Helper()
		rows, err := e.pool.Query(ctx, `SELECT payload FROM core.event_outbox WHERE name = $1 ORDER BY seq`, name)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []map[string]any
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var p map[string]any
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			out = append(out, p)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	lastOutbox := func(name string) map[string]any {
		t.Helper()
		evs := outboxEvents(name)
		if len(evs) == 0 {
			t.Fatalf("nessun evento %s nell'outbox", name)
		}
		return evs[len(evs)-1]
	}
	waitBackup := func(id, status string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			for _, b := range e.backups(t, "acme", "erp") {
				if b.Id.String() == id && string(b.Status) == status {
					return
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("backup %s non arrivato a %s", id, status)
	}

	type payload struct {
		Version int    `json:"version"`
		Event   string `json:"event"`
		Action  string `json:"action"`
		Time    string `json:"time"`
		Sender  struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"sender"`
		Database struct {
			ID      string `json:"id"`
			Owner   string `json:"owner"`
			Name    string `json:"name"`
			PgMajor int    `json:"pgMajor"`
		} `json:"database"`
		Backup struct {
			ID        string `json:"id"`
			Kind      string `json:"kind"`
			SizeBytes *int64 `json:"sizeBytes"`
		} `json:"backup"`
		Error    string `json:"error"`
		BackupID string `json:"backupId"`
		Target   string `json:"target"`
	}
	checkCommon := func(i int, req received, action string) payload {
		t.Helper()
		var p payload
		if err := json.Unmarshal(req.body, &p); err != nil {
			t.Fatalf("consegna %d: %v: %s", i, err, req.body)
		}
		if p.Version != 1 || p.Event != "database" || p.Time == "" {
			t.Fatalf("consegna %d: corpo comune %+v", i, p)
		}
		if action != "" && p.Action != action {
			t.Fatalf("consegna %d: azione %q, attesa %q", i, p.Action, action)
		}
		return p
	}

	// Creazione: consegna `created`.
	e.run()
	got := e.recv.requests()
	if len(got) != 1 {
		t.Fatalf("consegne dopo la creazione: %d", len(got))
	}

	// 1) Backup riuscito: backup{id, kind, sizeBytes} e nessun error, nel dominio e nel webhook.
	rec = e.do(http.MethodPost, "/databases/acme/erp/backups", "alice", "")
	e.want(rec, http.StatusAccepted, "")
	var started openapi.DatabaseBackup
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	waitBackup(started.Id.String(), "completed")

	ev := lastOutbox("database.backup_completed")
	backup, _ := ev["backup"].(map[string]any)
	if backup["id"] != started.Id.String() || backup["kind"] != "on_demand" {
		t.Fatalf("dominio backup_completed: backup %+v", backup)
	}
	if sb, _ := backup["sizeBytes"].(float64); sb <= 0 {
		t.Fatalf("dominio backup_completed: sizeBytes %+v", backup)
	}
	if _, ok := ev["error"]; ok {
		t.Fatalf("backup_completed non porta error: %+v", ev)
	}
	e.run()
	got = e.recv.requests()
	p := checkCommon(len(got)-1, got[len(got)-1], "backup_completed")
	if p.Backup.ID != started.Id.String() || p.Backup.Kind != "on_demand" || p.Backup.SizeBytes == nil || *p.Backup.SizeBytes <= 0 {
		t.Fatalf("backup_completed: backup %+v", p.Backup)
	}
	if p.Error != "" || p.BackupID != "" || p.Target != "" {
		t.Fatalf("backup_completed: campi inattesi %+v", p)
	}

	// 2) Backup fallito: backup{id, kind} senza sizeBytes, con error; il motivo e' lo stesso
	// testo gia' filtrato (niente password né DATABASE_URL) della riga di audit.
	dumper.err = errors.New("pg_dump: server chiuso")
	rec = e.do(http.MethodPost, "/databases/acme/erp/backups", "alice", "")
	e.want(rec, http.StatusAccepted, "")
	var failed openapi.DatabaseBackup
	_ = json.Unmarshal(rec.Body.Bytes(), &failed)
	waitBackup(failed.Id.String(), "failed")

	ev = lastOutbox("database.backup_failed")
	backup, _ = ev["backup"].(map[string]any)
	if backup["id"] != failed.Id.String() || backup["kind"] != "on_demand" {
		t.Fatalf("dominio backup_failed: backup %+v", backup)
	}
	if _, ok := backup["sizeBytes"]; ok {
		t.Fatalf("backup_failed non porta sizeBytes: %+v", backup)
	}
	if msg, _ := ev["error"].(string); !strings.Contains(msg, "server chiuso") {
		t.Fatalf("dominio backup_failed: error %q", msg)
	}
	e.run()
	got = e.recv.requests()
	p = checkCommon(len(got)-1, got[len(got)-1], "backup_failed")
	if p.Backup.ID != failed.Id.String() || p.Backup.Kind != "on_demand" || p.Backup.SizeBytes != nil {
		t.Fatalf("backup_failed: backup %+v", p.Backup)
	}
	if !strings.Contains(p.Error, "server chiuso") {
		t.Fatalf("backup_failed: error %q", p.Error)
	}
	dumper.err = nil

	// 3) Ripristino sul posto: restored con backupId del dump e target same; prima il dump di
	// sicurezza pubblica il suo backup_completed (kind safety).
	path := "/databases/acme/erp/backups/" + started.Id.String() + "/restore"
	rec = e.do(http.MethodPost, path, "alice", `{"target":"same","confirm":"acme/erp"}`)
	e.want(rec, http.StatusAccepted, "")
	reconcile("acme", "erp")
	if d, _ := st.GetManagedDB(ctx, "acme", "erp"); d.Status != store.DBRunning {
		t.Fatalf("dopo il ripristino: stato %s (%v)", d.Status, d.StatusReason)
	}

	ev = lastOutbox("database.restored")
	if bid, _ := ev["backupId"].(string); bid != started.Id.String() {
		t.Fatalf("dominio restored: backupId %q", bid)
	}
	if target, _ := ev["target"].(string); target != "same" {
		t.Fatalf("dominio restored: target %q", target)
	}
	e.run()
	got = e.recv.requests()
	if len(got) != 5 {
		t.Fatalf("consegne dopo il ripristino sul posto: %d", len(got))
	}
	safety, restoredSame := false, false
	for i, req := range got[3:] {
		q := checkCommon(i+3, req, "")
		switch q.Action {
		case "backup_completed":
			if q.Backup.Kind != "safety" || q.Backup.SizeBytes == nil {
				t.Fatalf("dump di sicurezza: %+v", q.Backup)
			}
			safety = true
		case "restored":
			if q.BackupID != started.Id.String() || q.Target != "same" || q.Database.ID != erpID || q.Database.Name != "erp" {
				t.Fatalf("restored same: %+v", q)
			}
			restoredSame = true
		default:
			t.Fatalf("consegna inattesa dopo il ripristino: %+v", q)
		}
	}
	if !safety || !restoredSame {
		t.Fatalf("consegne del ripristino sul posto: safety=%v restored=%v", safety, restoredSame)
	}

	// 4) Ripristino in un database nuovo: created del database nuovo, poi restored con
	// backupId del dump sorgente e target new sul database nuovo.
	rec = e.do(http.MethodPost, path, "alice", `{"target":"new","name":"erp-copia"}`)
	e.want(rec, http.StatusAccepted, "")
	var copyDB openapi.Database
	_ = json.Unmarshal(rec.Body.Bytes(), &copyDB)
	copyID := uuid.UUID(copyDB.Id)
	reconcile("acme", "erp-copia")
	markReady(copyID)
	reconcile("acme", "erp-copia")
	if d, _ := st.GetManagedDB(ctx, "acme", "erp-copia"); d.Status != store.DBRunning {
		t.Fatalf("dopo il ripristino: stato %s (%v)", d.Status, d.StatusReason)
	}

	ev = lastOutbox("database.restored")
	if bid, _ := ev["backupId"].(string); bid != started.Id.String() {
		t.Fatalf("dominio restored (new): backupId %q", bid)
	}
	if target, _ := ev["target"].(string); target != "new" {
		t.Fatalf("dominio restored (new): target %q", target)
	}
	db, _ := ev["database"].(map[string]any)
	if db["id"] != copyID.String() || db["name"] != "erp-copia" {
		t.Fatalf("dominio restored (new): database %+v", db)
	}
	e.run()
	got = e.recv.requests()
	if len(got) != 7 {
		t.Fatalf("consegne dopo il ripristino nel database nuovo: %d", len(got))
	}
	createdCopy, restoredNew := false, false
	for i, req := range got[5:] {
		q := checkCommon(i+5, req, "")
		switch q.Action {
		case "created":
			if q.Database.ID != copyID.String() || q.Database.Name != "erp-copia" {
				t.Fatalf("created del database nuovo: %+v", q.Database)
			}
			createdCopy = true
		case "restored":
			if q.BackupID != started.Id.String() || q.Target != "new" || q.Database.ID != copyID.String() || q.Database.Name != "erp-copia" {
				t.Fatalf("restored new: %+v", q)
			}
			restoredNew = true
		default:
			t.Fatalf("consegna inattesa dopo il ripristino new: %+v", q)
		}
	}
	if !createdCopy || !restoredNew {
		t.Fatalf("consegne del ripristino new: created=%v restored=%v", createdCopy, restoredNew)
	}

	// DB20: nessun segreto in nessun corpo consegnato, di nessuna azione.
	for i, req := range got {
		if h := req.header.Get("X-GitStack-Event"); h != "database" {
			t.Fatalf("consegna %d: X-GitStack-Event %q", i, h)
		}
		body := strings.ToLower(string(req.body))
		for _, banned := range []string{"password", "secret", "database_url"} {
			if strings.Contains(body, banned) {
				t.Fatalf("consegna %d: il payload contiene %q: %s", i, banned, req.body)
			}
		}
	}

	// Outbox elaborato tutto: una consegna per evento, nessun residuo.
	var outbox int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM core.event_outbox WHERE webhooked_at IS NULL`).Scan(&outbox); err != nil || outbox != 0 {
		t.Fatalf("outbox non svuotato: %d %v", outbox, err)
	}
}
