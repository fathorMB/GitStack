//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// GIT-384 (v3-a, H12): gli eventi delle app arrivano a chi ha fatto il deploy. Il deploy fallito e
// l'app fermata avvisano le notifiche; il webhook deployment consegna started, succeeded, failed e
// rolled_back, ma non l'app fermata (non è un'azione del contratto).

// createTestApp crea un'app con la prima versione in corso, fatta da actor.
func createTestApp(t *testing.T, pool *pgxpool.Pool, repo uuid.UUID, owner, name, actorName string) store.App {
	t.Helper()
	port := 8080
	actorID := uuid.MustParse(users[actorName])
	app, _, err := store.New(pool).CreateApp(context.Background(), store.NewApp{
		RepoID: repo, Owner: owner, Name: name,
		Actor: &store.AppActor{ID: actorID, Username: actorName, Type: "human"},
		Image: store.AppImage{Reference: "registry.example/" + owner + "/" + name + ":1", Source: "registry"},
		Config: store.AppConfig{Copies: 1, Port: &port, Variables: map[string]string{},
			Guaranteed: store.AppResourceAmount{CPUMillis: 100, MemoryBytes: 128 << 20},
			Limit:      store.AppResourceAmount{CPUMillis: 500, MemoryBytes: 512 << 20}},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func TestAppEvents_AvvisiAChiHaFattoIlDeploy(t *testing.T) {
	e := newNotifyEnvEmail(t, false)
	ctx := context.Background()
	repo := e.repo("app", false)
	app := createTestApp(t, e.pool, repo, "alice", "web", "alice")
	st := store.New(e.pool)
	now := time.Now().UTC()
	alice := uuid.MustParse(users["alice"])

	// GIT-404: bob è admin ma non ha fatto il deploy, carol ha solo write.
	e.id.grants[repo] = append(e.id.grants[repo], users["bob"])
	e.nid.grantWrite(repo, "carol")

	countFor := func(user uuid.UUID, event string) int {
		t.Helper()
		var n int
		if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM core.notifications WHERE user_id = $1 AND event_name = $2`, user, event).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	countNotices := func(event string) int { return countFor(alice, event) }

	// Il primo deploy fallisce: chi l'ha fatto riceve l'avviso, con il motivo.
	if err := st.FinishAppDeploy(ctx, app.ID, 1, "failed", "controllo di salute non superato", now); err != nil {
		t.Fatal(err)
	}
	e.process()
	if n := countNotices("app_deployment.failed"); n != 1 {
		t.Fatalf("avvisi di deploy fallito a chi l'ha fatto, admin a sua volta: %d (voluto 1, senza doppioni)", n)
	}
	if n := countFor(uuid.MustParse(users["bob"]), "app_deployment.failed"); n != 1 {
		t.Fatalf("avvisi di deploy fallito a un admin non autore: %d (voluto 1)", n)
	}
	if n := countFor(uuid.MustParse(users["carol"]), "app_deployment.failed"); n != 0 {
		t.Fatalf("avvisi di deploy fallito a chi ha solo write: %d (voluto 0)", n)
	}
	var reason string
	if err := e.pool.QueryRow(ctx, `SELECT data->>'reason' FROM core.notifications WHERE event_name = 'app_deployment.failed'`).Scan(&reason); err != nil || reason != "controllo di salute non superato" {
		t.Fatalf("motivo nell'avviso: %q %v", reason, err)
	}

	// Un'app in esecuzione che perde le copie pronte avvisa; restare non pronta no.
	for i := 0; i < 3; i++ {
		if err := st.SetAppObserved(ctx, app.ID, "running", 1); err != nil {
			t.Fatal(err)
		}
		if err := st.SetAppObserved(ctx, app.ID, "unhealthy", 0); err != nil {
			t.Fatal(err)
		}
		e.process()
		if i == 0 {
			if n := countNotices("app_status.unhealthy"); n != 1 {
				t.Fatalf("il primo passaggio a unhealthy avvisa subito: %d (voluto 1)", n)
			}
		}
	}
	if n := countNotices("app_status.unhealthy"); n != 1 {
		t.Fatalf("avvisi di app fermata a chi ha fatto il deploy: %d (voluto 1)", n)
	}
	// L'app fermata resta solo per chi ha fatto il deploy: l'admin non autore non la riceve.
	if n := countFor(uuid.MustParse(users["bob"]), "app_status.unhealthy"); n != 0 {
		t.Fatalf("avvisi di app fermata a un admin non autore: %d (voluto 0)", n)
	}
}

func TestAppEvents_WebhookDeployment(t *testing.T) {
	e := newHookEnv(t)
	ctx := context.Background()
	repo := e.repo("app", false)
	hook := e.repoHook("app", fmt.Sprintf(`{"url":%q,"events":["deployment"]}`, hookURL))
	app := createTestApp(t, e.pool, repo, "alice", "web", "alice")
	st := store.New(e.pool)
	now := time.Now().UTC()
	alice := users["alice"]

	// Primo deploy riuscito, poi un nuovo deploy avviato e fallito.
	if err := st.FinishAppDeploy(ctx, app.ID, 1, "serving", "", now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.BeginAppDeploy(ctx, repo, "web", store.NewAppDeploy{
		Trigger: "manual", Image: store.AppImage{Reference: "registry.example/alice/web:2", Source: "registry"},
		Actor: &store.AppActor{ID: uuid.MustParse(alice), Username: "alice", Type: "human"},
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishAppDeploy(ctx, app.ID, 2, "failed", "avvio fallito", now); err != nil {
		t.Fatal(err)
	}
	e.run()
	reqs := e.recv.requests()
	if len(reqs) != 3 {
		t.Fatalf("consegne del webhook deployment = %d (voluto 3: succeeded, started, failed)", len(reqs))
	}
	var actions []string
	for _, r := range reqs {
		if got := r.header.Get("X-GitStack-Event"); got != "deployment" {
			t.Fatalf("intestazione X-GitStack-Event = %q", got)
		}
		var body map[string]any
		if err := json.Unmarshal(r.body, &body); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, body["action"].(string))
		if body["event"] != "deployment" || body["version"] != float64(1) {
			t.Errorf("corpo comune: %v", body)
		}
		if a := body["app"].(map[string]any); a["name"] != "web" {
			t.Errorf("istantanea dell'app: %v", a)
		}
		if s := body["sender"].(map[string]any); s["username"] != "alice" {
			t.Errorf("chi ha fatto il deploy: %v", s)
		}
		if _, ok := body["repository"]; !ok {
			t.Errorf("manca il repo nel payload: %v", body)
		}
	}
	// Le consegne non hanno un ordine garantito fra eventi diversi (il campo time dà l'ordine): si
	// confronta l'insieme delle azioni.
	sort.Strings(actions)
	if want := []string{"failed", "started", "succeeded"}; !reflect.DeepEqual(actions, want) {
		t.Fatalf("azioni consegnate: %v, voluto %v", actions, want)
	}
	if hook == "" {
		t.Fatal("webhook non creato")
	}

	// L'app fermata non è un'azione del webhook deployment: nessuna consegna in più.
	if err := st.SetAppObserved(ctx, app.ID, "running", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAppObserved(ctx, app.ID, "unhealthy", 0); err != nil {
		t.Fatal(err)
	}
	e.run()
	if got := len(e.recv.requests()); got != 3 {
		t.Fatalf("app fermata: consegne = %d (voluto ancora 3)", got)
	}
}
