//go:build integration

package migrate_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/migrate"
	"github.com/google/uuid"
)

// TestMigration0034_ArtefattiCacheEImpostazioni (GIT-280, CI5): default di CI5, vincoli, unicità,
// cascata all'eliminazione dell'esecuzione e del repo; il down toglie le tre tabelle e il nuovo up rifunziona.
func TestMigration0034_ArtefattiCacheEImpostazioni(t *testing.T) {
	pool, dsn := dbtest.NewPool(t)
	ctx := context.Background()
	exec := func(q string, args ...any) error {
		_, err := pool.Exec(ctx, q, args...)
		return err
	}
	count := func(table string) int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM core.`+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// Impostazioni: default, riga unica, vincoli.
	if err := exec(`INSERT INTO core.ci_settings DEFAULT VALUES`); err != nil {
		t.Fatal(err)
	}
	var logDays, artDays, cacheDays int
	var artMax, quota int64
	if err := pool.QueryRow(ctx, `SELECT log_retention_days, artifact_retention_days, artifact_max_bytes, cache_quota_bytes, cache_unused_days FROM core.ci_settings`).
		Scan(&logDays, &artDays, &artMax, &quota, &cacheDays); err != nil || logDays != 90 || artDays != 30 || artMax != 2<<30 || quota != 10<<30 || cacheDays != 7 {
		t.Fatalf("default di CI5: %d %d %d %d %d %v", logDays, artDays, artMax, quota, cacheDays, err)
	}
	if exec(`INSERT INTO core.ci_settings DEFAULT VALUES`) == nil {
		t.Error("le impostazioni sono una riga sola")
	}
	for _, q := range []string{
		`UPDATE core.ci_settings SET log_retention_days = 0`,
		`UPDATE core.ci_settings SET artifact_retention_days = 3651`,
		`UPDATE core.ci_settings SET artifact_max_bytes = 0`,
		`UPDATE core.ci_settings SET cache_quota_bytes = 0`,
		`UPDATE core.ci_settings SET cache_unused_days = 0`,
	} {
		if exec(q) == nil {
			t.Errorf("il vincolo doveva rifiutare: %s", q)
		}
	}

	// Repo, esecuzione e job.
	repo, owner, run, job := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := exec(`INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', 'a/r')`, repo); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO core.repositories (resource_id, owner_type, owner_id, name) VALUES ($1, 'user', $2, 'r')`, repo, owner); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO core.ci_runs (id, repo_id, owner_type, owner_id, workflow_path, event, ref, sha, group_key)
		VALUES ($1, $2, 'user', $3, 'w.yml', 'push', 'refs/heads/main', $4, '')`, run, repo, owner, strings.Repeat("a", 40)); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO core.ci_jobs (id, run_id, seq, job_key, name) VALUES ($1, $2, 0, 'b', 'b')`, job, run); err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	// Artefatti: nome unico nell'esecuzione, dimensione non negativa, il job può sparire.
	art := func(name string, size int64) error {
		return exec(`INSERT INTO core.ci_artifacts (id, repo_id, run_id, job_id, name, size_bytes, storage_key, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, 'k', $7, $7)`, uuid.New(), repo, run, job, name, size, now)
	}
	if err := art("dist", 10); err != nil {
		t.Fatal(err)
	}
	if art("dist", 10) == nil {
		t.Error("il nome dell'artefatto è unico nell'esecuzione")
	}
	if art("neg", -1) == nil || art("", 1) == nil {
		t.Error("dimensione >= 0 e nome non vuoto")
	}

	// Cache: (repo, chiave, versione, ref) unico; seq è assegnato dal database.
	cache := func(key, ref string) error {
		return exec(`INSERT INTO core.ci_caches (id, repo_id, cache_key, version, ref, storage_key, created_at, last_used_at)
			VALUES ($1, $2, $3, 'v1', $4, 'k', $5, $5)`, uuid.New(), repo, key, ref, now)
	}
	if err := cache("npm", "refs/heads/main"); err != nil {
		t.Fatal(err)
	}
	if err := cache("npm", "refs/heads/feat"); err != nil {
		t.Fatal(err)
	}
	if cache("npm", "refs/heads/main") == nil {
		t.Error("chiave, versione e ref sono unici per repo")
	}
	var seqs int
	if err := pool.QueryRow(ctx, `SELECT count(DISTINCT seq) FROM core.ci_caches`).Scan(&seqs); err != nil || seqs != 2 {
		t.Fatalf("seq distinti: %d %v", seqs, err)
	}

	// Il job sparisce: l'artefatto resta (job_id NULL). L'esecuzione sparisce: l'artefatto cade. Il repo: le cache cadono.
	if err := exec(`DELETE FROM core.ci_jobs WHERE id = $1`, job); err != nil {
		t.Fatal(err)
	}
	if n := count("ci_artifacts"); n != 1 {
		t.Fatalf("artefatti dopo l'eliminazione del job: %d", n)
	}
	if err := exec(`DELETE FROM core.ci_runs WHERE id = $1`, run); err != nil {
		t.Fatal(err)
	}
	if n := count("ci_artifacts"); n != 0 {
		t.Fatalf("artefatti dopo l'eliminazione dell'esecuzione: %d", n)
	}
	if err := exec(`DELETE FROM core.repositories WHERE resource_id = $1`, repo); err != nil {
		t.Fatal(err)
	}
	if n := count("ci_caches"); n != 0 {
		t.Fatalf("cache dopo l'eliminazione del repo: %d", n)
	}

	if err := migrate.Down(ctx, pool, dsn, 6); err != nil {
		t.Fatalf("down 0034: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name IN ('ci_settings', 'ci_artifacts', 'ci_caches')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tabelle dopo il down: %d %v", n, err)
	}
	if err := migrate.Up(ctx, pool, dsn); err != nil {
		t.Fatalf("up dopo il down: %v", err)
	}
}
