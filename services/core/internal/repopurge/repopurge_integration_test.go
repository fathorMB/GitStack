//go:build integration

package repopurge_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/repopurge"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var t0 = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

// fakeGit conta le chiamate e simula il cestino: Delete vale solo dal cestino.
type fakeGit struct {
	mu      sync.Mutex
	trashed map[uuid.UUID]bool
	deleted map[uuid.UUID]int
	failFor uuid.UUID
}

func (g *fakeGit) Create(context.Context, trust.Identity, gitclient.CreateInput) (bool, error) {
	return true, nil
}
func (g *fakeGit) Get(context.Context, trust.Identity, uuid.UUID) (gitclient.State, error) {
	return gitclient.State{}, nil
}
func (g *fakeGit) Restore(context.Context, trust.Identity, uuid.UUID) error { return nil }
func (g *fakeGit) Trash(_ context.Context, _ trust.Identity, id uuid.UUID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.trashed[id] = true
	return nil
}
func (g *fakeGit) Delete(_ context.Context, _ trust.Identity, id uuid.UUID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if id == g.failFor {
		return gitclient.ErrUnavailable
	}
	if !g.trashed[id] {
		return gitclient.ErrNotFound
	}
	g.deleted[id]++
	return nil
}

type fakeAccess struct {
	mu     sync.Mutex
	purged map[uuid.UUID]int
}

func (a *fakeAccess) PurgeResource(_ context.Context, id uuid.UUID) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.purged[id]++
	return nil
}

type env struct {
	pool *pgxpool.Pool
	st   *store.Store
	git  *fakeGit
	acc  *fakeAccess
	now  time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	return &env{pool: pool, st: store.New(pool), now: t0,
		git: &fakeGit{trashed: map[uuid.UUID]bool{}, deleted: map[uuid.UUID]int{}},
		acc: &fakeAccess{purged: map[uuid.UUID]int{}}}
}

func (e *env) job() *repopurge.Job {
	return &repopurge.Job{Store: e.st, Git: e.git, Identity: e.acc, Now: func() time.Time { return e.now }}
}

// add inserisce un repo; deletedAgo nil = non eliminato.
func (e *env) add(t *testing.T, name string, deletedAgo *time.Duration) uuid.UUID {
	t.Helper()
	id := uuid.New()
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO core.resources (id, type, name, attributes) VALUES ($1, 'repo', $2, '{}'::jsonb)`, id, "alice/"+name); err != nil {
		t.Fatal(err)
	}
	var del *time.Time
	if deletedAgo != nil {
		d := e.now.Add(-*deletedAgo)
		del = &d
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, owner_name, name, deleted_at)
		VALUES ($1, 'user', $2, 'alice', $3, $4)`, id, uuid.New(), name, del); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO core.repo_counters (repo_id) VALUES ($1)`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) exists(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.repositories WHERE resource_id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func dur(d time.Duration) *time.Duration { return &d }

func TestJob_CancellaSoloOltreSetteGiorni(t *testing.T) {
	e := newEnv(t)
	active := e.add(t, "active", nil)
	recent := e.add(t, "recent", dur(7*24*time.Hour-time.Minute))
	old := e.add(t, "old", dur(7*24*time.Hour+time.Minute))
	older := e.add(t, "older", dur(30*24*time.Hour))

	n, err := e.job().RunOnce(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("RunOnce = %d, %v; voluti 2 cancellati", n, err)
	}
	if !e.exists(t, active) || !e.exists(t, recent) || e.exists(t, old) || e.exists(t, older) {
		t.Fatal("cancellati i repo sbagliati")
	}
	for _, id := range []uuid.UUID{old, older} {
		if e.git.deleted[id] != 1 || e.acc.purged[id] != 1 {
			t.Fatalf("repo %s: disco %d, grant %d", id, e.git.deleted[id], e.acc.purged[id])
		}
	}
	if len(e.git.deleted) != 2 || len(e.acc.purged) != 2 {
		t.Fatalf("toccati repo che non scadevano: %v %v", e.git.deleted, e.acc.purged)
	}

	// Con il tempo che passa scade anche il terzo; la corsa dopo non fa nulla.
	e.now = e.now.Add(2 * time.Minute)
	if n, err := e.job().RunOnce(context.Background()); err != nil || n != 1 || e.exists(t, recent) {
		t.Fatalf("RunOnce = %d, %v; voluto 1", n, err)
	}
	if n, err := e.job().RunOnce(context.Background()); err != nil || n != 0 {
		t.Fatalf("terza corsa = %d, %v; voluto 0", n, err)
	}
	if !e.exists(t, active) {
		t.Fatal("il repo attivo è stato cancellato")
	}
}

func TestJob_NomeLiberoSoloDopo(t *testing.T) {
	e := newEnv(t)
	id := e.add(t, "taken", dur(24*time.Hour))
	insert := func() error {
		_, err := e.pool.Exec(context.Background(), `INSERT INTO core.repositories (resource_id, owner_type, owner_id, owner_name, name)
			SELECT $1, 'user', owner_id, 'alice', 'taken' FROM core.repositories WHERE resource_id = $2`, uuid.New(), id)
		return err
	}
	if err := insert(); err == nil {
		t.Fatal("il nome di un repo eliminato è riusabile prima dei 7 giorni")
	}
	if n, err := e.job().RunOnce(context.Background()); err != nil || n != 0 {
		t.Fatalf("RunOnce = %d, %v", n, err)
	}
	e.now = e.now.Add(7 * 24 * time.Hour)
	if n, err := e.job().RunOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("RunOnce = %d, %v", n, err)
	}
	// La riga è sparita e con lei l'unicità: il nome torna libero.
	e.add(t, "taken", nil)
}

func TestJob_DueEsecuzioniInParallelo(t *testing.T) {
	e := newEnv(t)
	var ids []uuid.UUID
	for i := 0; i < 12; i++ {
		ids = append(ids, e.add(t, "r"+string(rune('a'+i)), dur(10*24*time.Hour)))
	}
	keep := e.add(t, "keep", dur(time.Hour))

	var wg sync.WaitGroup
	var total int
	var mu sync.Mutex
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := e.job().RunOnce(context.Background())
			mu.Lock()
			total += n
			mu.Unlock()
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if total != len(ids) {
		t.Fatalf("cancellati %d in totale, voluti %d", total, len(ids))
	}
	for _, id := range ids {
		if e.exists(t, id) || e.git.deleted[id] != 1 || e.acc.purged[id] != 1 {
			t.Fatalf("repo %s: esiste=%v disco=%d grant=%d (voluto una sola volta)", id, e.exists(t, id), e.git.deleted[id], e.acc.purged[id])
		}
	}
	if !e.exists(t, keep) {
		t.Fatal("il repo non scaduto è stato cancellato")
	}
}

func TestJob_ErroreSuUnRepoNonFermaGliAltri(t *testing.T) {
	e := newEnv(t)
	bad := e.add(t, "bad", dur(9*24*time.Hour))
	good := e.add(t, "good", dur(10*24*time.Hour))
	e.git.failFor = bad

	n, err := e.job().RunOnce(context.Background())
	if n != 1 || err == nil || !errors.Is(err, gitclient.ErrUnavailable) {
		t.Fatalf("RunOnce = %d, %v; voluto 1 e l'errore di git", n, err)
	}
	if !e.exists(t, bad) || e.exists(t, good) {
		t.Fatal("il repo in errore deve restare, l'altro sparire")
	}
	if e.acc.purged[bad] != 0 {
		t.Fatal("grant tolti a un repo il cui disco non è stato cancellato")
	}
	// Alla corsa dopo, con git di nuovo su, si completa.
	e.git.failFor = uuid.Nil
	if n, err := e.job().RunOnce(context.Background()); err != nil || n != 1 || e.exists(t, bad) {
		t.Fatalf("seconda corsa = %d, %v", n, err)
	}
}
