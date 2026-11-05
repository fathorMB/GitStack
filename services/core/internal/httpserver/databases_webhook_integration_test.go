//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/fathorMB/GitStack/services/core/internal/databases"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
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

// TestDatabases_WebhookConsegnaSenzaSegreti crea, ridimensiona ed elimina un database di
// un'organizzazione e verifica le tre consegne: azioni, schema del payload e divieto di segreti.
func TestDatabases_WebhookConsegnaSenzaSegreti(t *testing.T) {
	e := newDBHookEnv(t)
	// alice è owner dell'organizzazione acme; l'org ha un repo, che risolve owner_name -> org_id
	// per la consegna C6.
	e.hid.orgOwner[acmeID] = []string{aliceID, bobID}
	e.want(e.do(http.MethodPost, "/repos", "alice", `{"owner":"acme","name":"tools","visibility":"private"}`), http.StatusCreated, "")
	rec := e.do(http.MethodPost, "/orgs/acme/hooks", "alice", fmt.Sprintf(`{"url":%q,"events":["database"],"secret":"s3cr3t"}`, hookURL))
	e.want(rec, http.StatusCreated, "")

	// Creazione: 202 con la password una tantum (DB22): non deve finire MAI nel webhook.
	created := e.create("alice", "acme", `{"name":"erp","pgMajor":16}`, http.StatusAccepted)
	e.run()

	// Ridimensionamento.
	e.want(e.do(http.MethodPatch, "/databases/acme/erp", "alice", `{"cpuMillis":200}`), http.StatusOK, "")
	// Eliminazione (conferma owner/nome).
	e.want(e.do(http.MethodDelete, "/databases/acme/erp?confirm=acme/erp", "alice", ""), http.StatusNoContent, "")
	e.run()

	got := e.recv.requests()
	if len(got) != 3 {
		t.Fatalf("consegne: %d", len(got))
	}

	type payload struct {
		Version   int    `json:"version"`
		Event     string `json:"event"`
		Action    string `json:"action"`
		Time      string `json:"time"`
		Sender    struct {
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
	if len(byAction) != 3 {
		t.Fatalf("azioni mancanti: %+v", byAction)
	}

	dbID := uuid.UUID(created.Database.Id).String()
	for _, action := range []string{"created", "updated", "deleted"} {
		p := byAction[action]
		if p.Version != 1 || p.Event != "database" || p.Action != action {
			t.Fatalf("%s: corpo comune %+v", action, p)
		}
		if p.Time == "" || p.Sender.Username != "alice" || p.Sender.ID != aliceID {
			t.Fatalf("%s: corpo comune %+v", action, p)
		}
		if p.Database.ID != dbID || p.Database.Owner != "acme" || p.Database.Name != "erp" || p.Database.PgMajor != 16 {
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
	if byAction["deleted"].DeletedAt == "" {
		t.Fatal("deleted: deletedAt vuoto")
	}

	// L'evento della creazione del repo non è database: una sola consegna per evento.
	var outbox int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.event_outbox WHERE webhooked_at IS NULL`).Scan(&outbox); err != nil || outbox != 0 {
		t.Fatalf("outbox non svuotato: %d %v", outbox, err)
	}

	// Un database di un utente (non di un'organizzazione) non ha superficie webhook C6:
	// l'evento si elabora senza consegne e senza errori.
	e.create("alice", "alice", `{"name":"shop","pgMajor":17}`, http.StatusAccepted)
	e.run()
	if n := len(e.recv.requests()); n != 3 {
		t.Fatalf("il database di un utente non deve avere consegne: %d", n)
	}
}
