//go:build integration

package repopurge_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/fathorMB/GitStack/services/core/internal/repopurge"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
	dsn  string
	st   *store.Store
	git  *fakeGit
	acc  *fakeAccess
	log  *syncBuffer
}

// syncBuffer è un bytes.Buffer sicuro in concorrenza: i Job del test
// girano in parallelo e scrivono tutti sullo stesso logger.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool, dsn := dbtest.NewPool(t)
	return &env{pool: pool, dsn: dsn, st: store.New(pool), log: &syncBuffer{},
		git: &fakeGit{trashed: map[uuid.UUID]bool{}, deleted: map[uuid.UUID]int{}},
		acc: &fakeAccess{purged: map[uuid.UUID]int{}}}
}

func (e *env) job() *repopurge.Job {
	return &repopurge.Job{Store: e.st, Git: e.git, Identity: e.acc,
		Log: slog.New(slog.NewTextHandler(e.log, &slog.HandlerOptions{Level: slog.LevelInfo}))}
}

// add inserisce un repo vivo.
func (e *env) add(t *testing.T, owner, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO core.resources (id, type, name, attributes) VALUES ($1, 'repo', $2, '{}'::jsonb)`, id, owner+"/"+name); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, owner_name, name)
		VALUES ($1, 'user', $2, $3, $4)`, id, uuid.New(), owner, name); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO core.repo_counters (repo_id) VALUES ($1)`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

// del elimina il repo come fa DELETE /repos: la riga cade e il repo entra in coda.
func (e *env) del(t *testing.T, id uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tx, err := e.st.BeginRepo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	cur, err := tx.Lock(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.DeleteRepo(ctx, cur); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func (e *env) exists(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.repositories WHERE resource_id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func (e *env) queued(t *testing.T) int {
	t.Helper()
	n, err := e.st.CleanupQueued(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Un repo eliminato non c'è più subito (nome libero), e la coda lo toglie da disco e da identity una volta sola.
func TestJob_PurgeSubitoENomeLibero(t *testing.T) {
	e := newEnv(t)
	id := e.add(t, "alice", "taken")
	keep := e.add(t, "alice", "keep")
	e.del(t, id)
	if e.exists(t, id) {
		t.Fatal("la riga del repo c'è ancora")
	}
	e.add(t, "alice", "taken") // nome subito libero, prima di ogni pulizia
	if e.queued(t) != 1 || len(e.git.deleted) != 0 {
		t.Fatalf("prima della pulizia: in coda %d, git %v", e.queued(t), e.git.deleted)
	}
	if err := e.job().Purge(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if e.git.deleted[id] != 1 || e.acc.purged[id] != 1 || e.queued(t) != 0 {
		t.Fatalf("disco %d, grant %d, in coda %d", e.git.deleted[id], e.acc.purged[id], e.queued(t))
	}
	if !e.exists(t, keep) || len(e.git.deleted) != 1 || len(e.acc.purged) != 1 {
		t.Fatalf("toccato un repo che non era stato eliminato: %v %v", e.git.deleted, e.acc.purged)
	}
	// Una seconda pulizia dello stesso repo non fa niente.
	if err := e.job().Purge(context.Background(), id); err != nil || e.git.deleted[id] != 1 {
		t.Fatalf("seconda Purge: %v, disco %d", err, e.git.deleted[id])
	}
}

func TestJob_DueEsecuzioniInParallelo(t *testing.T) {
	e := newEnv(t)
	var ids []uuid.UUID
	for i := 0; i < 12; i++ {
		id := e.add(t, "alice", "r"+string(rune('a'+i)))
		e.del(t, id)
		ids = append(ids, id)
	}
	keep := e.add(t, "alice", "keep")

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
		t.Fatalf("puliti %d in totale, voluti %d", total, len(ids))
	}
	for _, id := range ids {
		if e.git.deleted[id] != 1 || e.acc.purged[id] != 1 {
			t.Fatalf("repo %s: disco=%d grant=%d (voluto una sola volta)", id, e.git.deleted[id], e.acc.purged[id])
		}
	}
	if !e.exists(t, keep) {
		t.Fatal("il repo vivo è stato toccato")
	}
}

func TestJob_ErroreSuUnRepoNonFermaGliAltri(t *testing.T) {
	e := newEnv(t)
	bad := e.add(t, "alice", "bad")
	good := e.add(t, "alice", "good")
	e.del(t, bad)
	e.del(t, good)
	e.git.failFor = bad

	n, err := e.job().RunOnce(context.Background())
	if n != 1 || err == nil || !errors.Is(err, gitclient.ErrUnavailable) {
		t.Fatalf("RunOnce = %d, %v; voluto 1 e l'errore di git", n, err)
	}
	if e.queued(t) != 1 || e.git.deleted[good] != 1 {
		t.Fatalf("in coda %d, good su disco %d", e.queued(t), e.git.deleted[good])
	}
	if e.acc.purged[bad] != 0 {
		t.Fatal("grant tolti a un repo il cui disco non è stato cancellato")
	}
	var attempts int
	var lastErr string
	if err := e.pool.QueryRow(context.Background(), `SELECT attempts, last_error FROM core.repo_cleanup WHERE repo_id = $1`, bad).Scan(&attempts, &lastErr); err != nil || attempts != 1 || lastErr == "" {
		t.Fatalf("attempts=%d last_error=%q err=%v", attempts, lastErr, err)
	}
	// Alla corsa dopo, con git di nuovo su, si completa.
	e.git.failFor = uuid.Nil
	if n, err := e.job().RunOnce(context.Background()); err != nil || n != 1 || e.queued(t) != 0 {
		t.Fatalf("seconda corsa = %d, %v", n, err)
	}
}

// Criterio 4 di GIT-530: la migrazione 0056 toglie dal cestino, per sempre, i repo già eliminati
// (il nome si libera subito: su homehub admin/odin), li mette in coda, e il passo di core all'avvio
// (RunOnce) li cancella da disco e identity scrivendo una riga di log per ciascuno. Un repo vivo non si tocca.
func TestUpgrade_RepoNelCestinoCancellatiConUnaRigaDiLogPerOgnuno(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	// Torna allo schema precedente (0055), dove il cestino esisteva.
	if err := migrate.Down(ctx, e.pool, e.dsn, 1); err != nil {
		t.Fatal(err)
	}
	odin := e.add(t, "admin", "odin")
	old := e.add(t, "bob", "vecchio")
	live := e.add(t, "alice", "live")
	e.git.trashed[odin], e.git.trashed[old] = true, true
	for _, id := range []uuid.UUID{odin, old} {
		if _, err := e.pool.Exec(ctx, `UPDATE core.repositories SET deleted_at = now() - interval '2 days' WHERE resource_id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	// Un'app del repo nel cestino: gli id vanno in coda per toglierla dal cluster.
	port := 8080
	app, _, err := e.st.CreateApp(ctx, store.NewApp{RepoID: odin, Owner: "admin", Name: "web",
		Image: store.AppImage{Reference: "registry.example/admin/odin:v1", Source: "registry"},
		Config: store.AppConfig{Copies: 1, Port: &port, Variables: map[string]string{},
			Guaranteed: store.AppResourceAmount{CPUMillis: 100, MemoryBytes: 128 << 20},
			Limit:      store.AppResourceAmount{CPUMillis: 500, MemoryBytes: 512 << 20}},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	appID := app.ID

	if err := migrate.Up(ctx, e.pool, e.dsn); err != nil {
		t.Fatalf("up 0056: %v", err)
	}
	// Dopo la migrazione i due repo non ci sono più e il nome è libero.
	if e.exists(t, odin) || e.exists(t, old) || !e.exists(t, live) {
		t.Fatal("la migrazione ha cancellato i repo sbagliati")
	}
	e.add(t, "admin", "odin")
	if e.queued(t) != 2 {
		t.Fatalf("in coda %d, voluti 2", e.queued(t))
	}
	var ids []uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT app_ids FROM core.repo_cleanup WHERE repo_id = $1`, odin).Scan(&ids); err != nil || len(ids) != 1 || ids[0] != appID {
		t.Fatalf("app_ids = %v (%v)", ids, err)
	}

	forgot := &recordingApps{}
	job := e.job()
	job.Apps = forgot
	n, err := job.RunOnce(ctx)
	if err != nil || n != 2 {
		t.Fatalf("RunOnce = %d, %v", n, err)
	}
	if e.queued(t) != 0 || e.git.deleted[odin] != 1 || e.git.deleted[old] != 1 || e.acc.purged[odin] != 1 || e.acc.purged[old] != 1 {
		t.Fatalf("in coda %d, disco %v, grant %v", e.queued(t), e.git.deleted, e.acc.purged)
	}
	if len(forgot.ids) != 1 || forgot.ids[0] != appID {
		t.Fatalf("app tolte dal cluster: %v", forgot.ids)
	}
	if e.git.deleted[live] != 0 || e.acc.purged[live] != 0 {
		t.Fatal("il repo vivo è stato toccato")
	}
	out := e.log.String()
	if got := strings.Count(out, "upgrade: repo che era nel cestino eliminato definitivamente"); got != 2 {
		t.Fatalf("log: %d righe di upgrade, volute 2:\n%s", got, out)
	}
	for _, want := range []string{"repo=admin/odin", "repo=bob/vecchio"} {
		if strings.Count(out, want) != 1 {
			t.Fatalf("log: voluta una riga per %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "alice/live") {
		t.Fatalf("il repo vivo compare nel log:\n%s", out)
	}
	// Il giro dopo non trova più niente.
	if n, err := job.RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("secondo RunOnce = %d, %v", n, err)
	}
}

type recordingApps struct {
	mu  sync.Mutex
	ids []uuid.UUID
}

func (a *recordingApps) ForgetApps(_ context.Context, ids []uuid.UUID) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ids = append(a.ids, ids...)
	return nil
}
