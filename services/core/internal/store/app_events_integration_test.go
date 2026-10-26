//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// GIT-384 (v3-a, H12): gli eventi delle app nell'outbox. Ogni esito di deploy e il passaggio a
// «fermata» scrivono una riga nella stessa transazione della modifica; un'azione ripetuta non ne
// scrive una seconda.

type outboxRow struct {
	Name    string
	Payload map[string]any
}

func outboxRows(t *testing.T, pool *pgxpool.Pool) []outboxRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT name, payload FROM core.event_outbox ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		var raw []byte
		if err := rows.Scan(&r.Name, &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &r.Payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func namesOf(rows []outboxRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

func TestStore_EventiDelleAppNelleTransazioni(t *testing.T) {
	ctx := context.Background()
	pool, _ := dbtest.NewPool(t)
	st := store.New(pool)
	repoID, alice := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'alice/web')`, repoID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, owner_name, name)
		VALUES ($1, 'user', $2, 'alice', 'web')`, repoID, alice); err != nil {
		t.Fatal(err)
	}
	actor := &store.AppActor{ID: alice, Username: "alice", Type: "human"}
	port := 8080
	now := time.Now().UTC().Truncate(time.Microsecond)
	app, _, err := st.CreateApp(ctx, store.NewApp{
		RepoID: repoID, Owner: "alice", Name: "web", Actor: actor,
		Image: store.AppImage{Reference: "registry.example/alice/web:1", Source: "registry"},
		Config: store.AppConfig{
			Copies: 1, Port: &port, Variables: map[string]string{},
			Guaranteed: store.AppResourceAmount{CPUMillis: 100, MemoryBytes: 128 << 20},
			Limit:      store.AppResourceAmount{CPUMillis: 500, MemoryBytes: 512 << 20},
		},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := namesOf(outboxRows(t, pool)); len(got) != 0 {
		t.Fatalf("la creazione non scrive eventi di deploy: %v", got)
	}

	// Primo deploy fallito: un evento con il motivo e chi ha fatto il deploy; l'app non è in esecuzione.
	if err := st.FinishAppDeploy(ctx, app.ID, 1, "failed", "controllo di salute non superato", now); err != nil {
		t.Fatal(err)
	}
	rows := outboxRows(t, pool)
	if got := namesOf(rows); len(got) != 1 || got[0] != "app_deployment.failed" {
		t.Fatalf("eventi dopo il fallimento: %v", got)
	}
	f := rows[0].Payload
	if f["reason"] != "controllo di salute non superato" || f["version"] != float64(1) || f["appName"] != "web" {
		t.Errorf("payload del fallimento: %v", f)
	}
	if actorOf(f)["id"] != alice.String() || actorOf(f)["username"] != "alice" {
		t.Errorf("l'avviso va a chi ha fatto il deploy: %v", f["actor"])
	}
	if repo := f["repo"].(map[string]any); repo["id"] != repoID.String() || repo["fullName"] != "alice/web" {
		t.Errorf("repo del payload: %v", repo)
	}
	dep := f["deployment"].(map[string]any)
	if dep["state"] != "failed" || dep["number"] != float64(1) || dep["trigger"] != "create" {
		t.Errorf("istantanea della versione: %v", dep)
	}
	if app := f["app"].(map[string]any); app["status"] != "failed" || app["name"] != "web" {
		t.Errorf("istantanea dell'app: %v", app)
	}

	// Nuovo deploy riuscito, poi rollback riuscito: started, succeeded, started, rolled_back.
	if _, _, err := st.BeginAppDeploy(ctx, repoID, "web", store.NewAppDeploy{
		Trigger: "manual", Image: store.AppImage{Reference: "registry.example/alice/web:2", Source: "registry"}, Actor: actor,
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishAppDeploy(ctx, app.ID, 2, "serving", "", now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.BeginAppDeploy(ctx, repoID, "web", store.NewAppDeploy{
		Trigger: "rollback", RollbackOf: intPtr(1), Image: store.AppImage{Reference: "registry.example/alice/web:1", Source: "registry"}, Actor: actor,
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishAppDeploy(ctx, app.ID, 3, "serving", "", now); err != nil {
		t.Fatal(err)
	}
	want := []string{"app_deployment.failed", "app_deployment.started", "app_deployment.succeeded",
		"app_deployment.started", "app_deployment.rolled_back"}
	if got := namesOf(outboxRows(t, pool)); !equalStrings(got, want) {
		t.Fatalf("sequenza degli eventi: %v, voluta %v", got, want)
	}

	// Un'app in esecuzione con le copie non pronte è «fermata»: un evento, e solo al passaggio.
	if err := st.SetAppObserved(ctx, app.ID, "running", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAppObserved(ctx, app.ID, "unhealthy", 0); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAppObserved(ctx, app.ID, "unhealthy", 0); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAppObserved(ctx, app.ID, "stopped", 0); err != nil {
		t.Fatal(err)
	}
	rows = outboxRows(t, pool)
	last := rows[len(rows)-1]
	if len(rows) != len(want)+1 || last.Name != "app_status.unhealthy" {
		t.Fatalf("un solo evento di fermata: %v", namesOf(rows))
	}
	if actorOf(last.Payload)["id"] != alice.String() {
		t.Errorf("l'avviso di fermata va a chi ha fatto l'ultimo deploy attivo: %v", last.Payload["actor"])
	}
}

// Un'app che non era in esecuzione (starting) e diventa non pronta non è «fermata»: nessun evento.
func TestStore_AppNonInEsecuzioneNonFermata(t *testing.T) {
	ctx := context.Background()
	pool, _ := dbtest.NewPool(t)
	st := store.New(pool)
	repoID, alice := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'alice/api')`, repoID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, owner_name, name)
		VALUES ($1, 'user', $2, 'alice', 'api')`, repoID, alice); err != nil {
		t.Fatal(err)
	}
	port := 8080
	app, _, err := st.CreateApp(ctx, store.NewApp{
		RepoID: repoID, Owner: "alice", Name: "api", Actor: &store.AppActor{ID: alice, Username: "alice", Type: "human"},
		Image: store.AppImage{Reference: "registry.example/alice/api:1", Source: "registry"},
		Config: store.AppConfig{Copies: 1, Port: &port, Variables: map[string]string{},
			Guaranteed: store.AppResourceAmount{CPUMillis: 100, MemoryBytes: 128 << 20},
			Limit:      store.AppResourceAmount{CPUMillis: 500, MemoryBytes: 512 << 20}},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAppObserved(ctx, app.ID, "unhealthy", 0); err != nil {
		t.Fatal(err)
	}
	if got := namesOf(outboxRows(t, pool)); len(got) != 0 {
		t.Fatalf("da «starting» a «unhealthy» non c'è avviso: %v", got)
	}
}

func actorOf(p map[string]any) map[string]any {
	a, _ := p["actor"].(map[string]any)
	return a
}

func intPtr(v int) *int { return &v }

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
