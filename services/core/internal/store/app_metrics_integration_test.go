//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// GIT-384 (H12): campioni delle metriche per copia. Riavvii = aumenti fra campioni consecutivi della
// stessa copia (un pod nuovo non conta); la sorgente risponde se c'è un campione con CPU; la pulizia
// toglie i campioni più vecchi; le serie sono medie per passo, sommate sulle copie.
func TestStore_CampioniDelleMetriche(t *testing.T) {
	ctx := context.Background()
	pool, _ := dbtest.NewPool(t)
	st := store.New(pool)
	repoID, owner := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'alice/m')`, repoID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, owner_name, name)
		VALUES ($1, 'user', $2, 'alice', 'm')`, repoID, owner); err != nil {
		t.Fatal(err)
	}
	port := 8080
	base := time.Now().UTC().Truncate(time.Minute).Add(-30 * time.Minute)
	app, _, err := st.CreateApp(ctx, store.NewApp{
		RepoID: repoID, Owner: "alice", Name: "m", Actor: &store.AppActor{ID: owner, Username: "alice", Type: "human"},
		Image: store.AppImage{Reference: "registry.example/alice/m:1", Source: "registry"},
		Config: store.AppConfig{Copies: 2, Port: &port, Variables: map[string]string{},
			Guaranteed: store.AppResourceAmount{CPUMillis: 100, MemoryBytes: 128 << 20},
			Limit:      store.AppResourceAmount{CPUMillis: 500, MemoryBytes: 512 << 20}},
	}, base)
	if err != nil {
		t.Fatal(err)
	}
	cpu := func(v int) *int { return &v }
	mem := func(v int64) *int64 { return &v }
	sample := func(at time.Time, copy string, restarts, c int) store.AppMetricSample {
		return store.AppMetricSample{Copy: copy, At: at, Ready: true, Restarts: restarts, CPUMillis: cpu(c), MemoryBytes: mem(1000)}
	}
	// web-1: 0, poi 2 riavvii; web-2: un pod nuovo che parte da 5 (non conta come riavvio).
	samples := []store.AppMetricSample{
		sample(base, "web-1", 0, 10), sample(base, "web-2", 5, 30),
		sample(base.Add(30*time.Second), "web-1", 2, 20), sample(base.Add(30*time.Second), "web-2", 5, 40),
	}
	if err := st.AppendAppMetricSamples(ctx, app.ID, samples); err != nil {
		t.Fatal(err)
	}
	// Dopo i campioni, web-2 è sostituito: il nuovo pod parte da 0 e non ha CPU (sorgente non risponde).
	if err := st.AppendAppMetricSamples(ctx, app.ID, []store.AppMetricSample{
		{Copy: "web-3", At: base.Add(time.Minute), Ready: false, Restarts: 0},
	}); err != nil {
		t.Fatal(err)
	}

	if n, err := st.AppRestartsSince(ctx, app.ID, base.Add(-time.Hour)); err != nil || n != 2 {
		t.Fatalf("riavvii: %d %v (voluti 2: web-1 da 0 a 2; il pod nuovo di web-2 non conta)", n, err)
	}
	if src, err := st.AppMetricsSourceOK(ctx, app.ID, base.Add(-time.Hour)); err != nil || !src {
		t.Fatalf("sorgente: %v %v", src, err)
	}
	copies, err := st.AppCopyStates(ctx, app.ID, base.Add(-time.Hour))
	if err != nil || len(copies) != 3 {
		t.Fatalf("copie: %+v %v (voluto l'ultimo campione di ciascuna delle tre)", copies, err)
	}
	for _, c := range copies {
		if c.Copy == "web-1" && (c.Restarts != 2 || !c.Ready) {
			t.Errorf("web-1: %+v", c)
		}
		if c.Copy == "web-3" && c.Ready {
			t.Errorf("web-3 non è pronto: %+v", c)
		}
	}

	// Serie: passo di 60 s; lo store dà la media per copia, la somma sulle copie la fa l’API.
	buckets, err := st.AppMetricBuckets(ctx, app.ID, base.Add(-time.Hour), 60, "")
	if err != nil {
		t.Fatal(err)
	}
	perCopy := map[string]float64{}
	for _, b := range buckets {
		if b.CPUMillis != nil && b.Time.Equal(base.Truncate(time.Minute)) {
			perCopy[b.Copy] = *b.CPUMillis
		}
	}
	// Il minuto contiene i campioni di base (10 e 30) e di base+30 s (20 e 40): media 15 e 35.
	if perCopy["web-1"] != 15 || perCopy["web-2"] != 35 {
		t.Fatalf("medie per copia nel primo minuto: %v (voluti web-1=15, web-2=35)", perCopy)
	}
	one, err := st.AppMetricBuckets(ctx, app.ID, base.Add(-time.Hour), 60, "web-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range one {
		if b.Copy != "web-1" {
			t.Errorf("il filtro per copia lascia passare %s", b.Copy)
		}
	}

	// Pulizia: prima di base+1 min se ne vanno i campioni di base e base+30 s.
	removed, err := st.PruneAppMetrics(ctx, base.Add(time.Minute))
	if err != nil || removed != 4 {
		t.Fatalf("pulizia: %d %v (voluti 4)", removed, err)
	}
	if src, _ := st.AppMetricsSourceOK(ctx, app.ID, base.Add(-time.Hour)); src {
		t.Error("senza campioni con CPU la sorgente non risponde")
	}
}
