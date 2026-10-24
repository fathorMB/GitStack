//go:build integration

package ci

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var storageNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func seedStorageRepo(t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id, owner := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.resources (id, type, name) VALUES ($1, 'repo', $2)`, id, "owner/"+name); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.repositories (resource_id, owner_type, owner_id, name, created_at) VALUES ($1, 'user', $2, $3, $4)`,
		id, owner, name, storageNow.AddDate(-1, 0, 0)); err != nil {
		t.Fatal(err)
	}
	return id
}

// seedStorageJob crea un'esecuzione con un job; queuedAt/startedAt servono all'attesa media.
func seedStorageJob(t *testing.T, pool *pgxpool.Pool, repo uuid.UUID, status string, queuedAt, startedAt *time.Time) JobAuth {
	t.Helper()
	ctx := context.Background()
	runID, jobID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO core.ci_runs (id, repo_id, owner_type, owner_id, workflow_path, event, ref, sha, group_key, status, created_at)
		VALUES ($1, $2, 'user', $3, '.gitstack/workflows/ci.yml', 'push', 'refs/heads/main', $4, '', 'in_progress', $5)`,
		runID, repo, uuid.New(), strings.Repeat("a", 40), storageNow.AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO core.ci_jobs (id, run_id, seq, job_key, name, status, queued_at, started_at)
		VALUES ($1, $2, 0, 'build', 'build', $3, $4, $5)`, jobID, runID, status, queuedAt, startedAt); err != nil {
		t.Fatal(err)
	}
	return JobAuth{JobID: jobID, RunID: runID, RepoID: repo, SHA: strings.Repeat("a", 40), JobKey: "build"}
}

func readAll(t *testing.T, b Blobs, key string) (string, error) {
	t.Helper()
	rc, err := b.Open(context.Background(), key)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	return string(got), err
}

// backends dà i due archivi di CI5: disco e S3 (finto).
func backends(t *testing.T) map[string]Blobs {
	_, s3 := newFakeS3(t)
	return map[string]Blobs{"disco": DiskBlobs{Dir: t.TempDir()}, "s3": s3}
}

func TestStorage_Settings_DefaultESalvataggio(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	got, err := LoadSettings(ctx, pool)
	if err != nil || got != DefaultSettings() {
		t.Fatalf("senza riga valgono i default di CI5: %+v, %v", got, err)
	}
	next := Settings{LogRetentionDays: 14, ArtifactRetentionDays: 3, ArtifactMaxBytes: 1000, CacheQuotaBytes: 5000, CacheUnusedDays: 2}
	if _, err := SaveSettings(ctx, pool, storageNow, uuid.New(), next); err != nil {
		t.Fatal(err)
	}
	if got, _ = LoadSettings(ctx, pool); got != next {
		t.Errorf("dopo il salvataggio: %+v", got)
	}
	next.LogRetentionDays = 30
	if _, err := SaveSettings(ctx, pool, storageNow, uuid.New(), next); err != nil {
		t.Fatal(err)
	}
	if got, _ = LoadSettings(ctx, pool); got.LogRetentionDays != 30 {
		t.Errorf("secondo salvataggio: %+v", got)
	}
	// Il CHECK del database rifiuta i valori fuori scala anche senza passare da Validate.
	bad := next
	bad.CacheUnusedDays = 0
	if _, err := SaveSettings(ctx, pool, storageNow, uuid.New(), bad); err == nil {
		t.Error("il CHECK doveva rifiutare cacheUnusedDays = 0")
	}
}

func TestStorage_Artefatti(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	repo := seedStorageRepo(t, pool, "app")
	other := seedStorageRepo(t, pool, "altro")
	job := seedStorageJob(t, pool, repo, "in_progress", nil, nil)
	for name, blobs := range backends(t) {
		t.Run(name, func(t *testing.T) {
			st := DefaultSettings()
			st.ArtifactMaxBytes = 10
			art, err := SaveArtifact(ctx, pool, blobs, storageNow, job, "dist-"+name, strings.NewReader("0123456789"), st)
			if err != nil {
				t.Fatal(err)
			}
			if art.SizeBytes != 10 || !art.ExpiresAt.Equal(storageNow.AddDate(0, 0, 30)) {
				t.Errorf("artefatto: %+v (scade a 30 giorni)", art)
			}
			if got, err := readAll(t, blobs, art.StorageKey); err != nil || got != "0123456789" {
				t.Errorf("contenuto: %q, %v", got, err)
			}
			// Stesso nome nella stessa esecuzione: rifiutato, il file nuovo non resta.
			if _, err := SaveArtifact(ctx, pool, blobs, storageNow, job, "dist-"+name, strings.NewReader("x"), st); !errors.Is(err, ErrArtifactExists) {
				t.Errorf("duplicato: %v", err)
			}
			// Oltre il limite regolabile: ErrTooLarge e nessuna riga.
			if _, err := SaveArtifact(ctx, pool, blobs, storageNow, job, "grande-"+name, strings.NewReader("01234567890"), st); !errors.Is(err, ErrTooLarge) {
				t.Errorf("oltre ArtifactMaxBytes: %v", err)
			}
			if _, err := SaveArtifact(ctx, pool, blobs, storageNow, job, "", strings.NewReader("x"), st); !errors.Is(err, ErrArtifactName) {
				t.Errorf("nome vuoto: %v", err)
			}
			list, err := ListArtifacts(ctx, pool, storageNow, repo, job.RunID)
			if err != nil || len(list) == 0 {
				t.Fatalf("elenco: %v, %v", list, err)
			}
			for _, a := range list {
				if strings.HasPrefix(a.Name, "grande") {
					t.Errorf("artefatto rifiutato nell'elenco: %+v", a)
				}
			}
			// Un altro repo non vede l'esecuzione né l'artefatto.
			if _, err := ListArtifacts(ctx, pool, storageNow, other, job.RunID); !errors.Is(err, pgx.ErrNoRows) {
				t.Errorf("esecuzione di un altro repo: %v", err)
			}
			if _, err := GetArtifact(ctx, pool, storageNow, other, art.ID); !errors.Is(err, pgx.ErrNoRows) {
				t.Errorf("artefatto di un altro repo: %v", err)
			}
			if _, err := GetArtifact(ctx, pool, storageNow, repo, art.ID); err != nil {
				t.Errorf("get: %v", err)
			}
			// A 29 giorni c'è ancora; a 31 non si elenca più e la pulizia elimina file e riga.
			if _, err := GetArtifact(ctx, pool, storageNow.AddDate(0, 0, 29), repo, art.ID); err != nil {
				t.Errorf("a 29 giorni: %v", err)
			}
			late := storageNow.AddDate(0, 0, 31)
			if _, err := GetArtifact(ctx, pool, late, repo, art.ID); !errors.Is(err, pgx.ErrNoRows) {
				t.Errorf("a 31 giorni l'artefatto è scaduto: %v", err)
			}
			if n, err := PurgeExpiredArtifacts(ctx, pool, blobs, storageNow.AddDate(0, 0, 29)); err != nil || n != 0 {
				t.Errorf("la pulizia a 29 giorni non elimina: %d, %v", n, err)
			}
			n, err := PurgeExpiredArtifacts(ctx, pool, blobs, late)
			if err != nil || n < 1 {
				t.Fatalf("pulizia: %d, %v", n, err)
			}
			if _, err := readAll(t, blobs, art.StorageKey); !errors.Is(err, ErrBlobNotFound) {
				t.Errorf("il file doveva essere eliminato: %v", err)
			}
			if n, _ := PurgeExpiredArtifacts(ctx, pool, blobs, late); n != 0 {
				t.Errorf("la pulizia è idempotente: %d", n)
			}
		})
	}
}

func saveCache(t *testing.T, pool *pgxpool.Pool, blobs Blobs, now time.Time, repo uuid.UUID, key, version, ref, body string, st Settings) Cache {
	t.Helper()
	c, err := ReserveCache(context.Background(), pool, now, repo, key, version, ref, int64(len(body)), st)
	if err != nil {
		t.Fatalf("ReserveCache(%s): %v", key, err)
	}
	c, err = CommitCache(context.Background(), pool, blobs, now, repo, c.Seq, strings.NewReader(body), st)
	if err != nil {
		t.Fatalf("CommitCache(%s): %v", key, err)
	}
	return c
}

func TestStorage_Cache_RicercaScopeEDuplicati(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	repo := seedStorageRepo(t, pool, "app")
	for name, blobs := range backends(t) {
		t.Run(name, func(t *testing.T) {
			st := DefaultSettings()
			main, feat := "refs/heads/main", "refs/heads/feat"
			k := "npm-" + name + "-"
			saveCache(t, pool, blobs, storageNow, repo, k+"abc", "v1", main, "MAIN", st)
			saveCache(t, pool, blobs, storageNow, repo, k+"abc", "v1", feat, "FEAT", st)
			saveCache(t, pool, blobs, storageNow.Add(time.Minute), repo, k+"def", "v1", main, "RECENTE", st)

			// Esatta: nel ref del job vince quella del job, altrimenti il ramo predefinito.
			c, err := FindCache(ctx, pool, storageNow, repo, []string{k + "abc"}, "v1", feat, main)
			if err != nil || c.Ref != feat {
				t.Fatalf("esatta nel ref del job: %+v, %v", c, err)
			}
			c, err = FindCache(ctx, pool, storageNow, repo, []string{k + "abc"}, "v1", "refs/heads/altro", main)
			if err != nil || c.Ref != main {
				t.Fatalf("ripiego sul ramo predefinito: %+v, %v", c, err)
			}
			// Un ramo non predefinito non vede le cache di un altro ramo.
			if _, err := FindCache(ctx, pool, storageNow, repo, []string{k + "abc"}, "v1", "refs/heads/altro", "refs/heads/zzz"); !errors.Is(err, pgx.ErrNoRows) {
				t.Errorf("cache di un ramo estraneo: %v", err)
			}
			// Versione diversa: nessuna.
			if _, err := FindCache(ctx, pool, storageNow, repo, []string{k + "abc"}, "v2", main, main); !errors.Is(err, pgx.ErrNoRows) {
				t.Errorf("versione diversa: %v", err)
			}
			// Chiavi di ripiego: prefisso, la più recente.
			c, err = FindCache(ctx, pool, storageNow, repo, []string{"nessuna", k}, "v1", main, main)
			if err != nil || c.Key != k+"def" {
				t.Errorf("per prefisso: %+v, %v", c, err)
			}
			if got, err := readAll(t, blobs, c.StorageKey); err != nil || got != "RECENTE" {
				t.Errorf("contenuto: %q, %v", got, err)
			}
			// Non si sovrascrive.
			if _, err := ReserveCache(ctx, pool, storageNow, repo, k+"abc", "v1", main, 4, st); !errors.Is(err, ErrCacheExists) {
				t.Errorf("duplicato: %v", err)
			}
			// Una prenotazione non confermata non si trova e non conta.
			res, err := ReserveCache(ctx, pool, storageNow, repo, k+"nuova", "v1", main, 4, st)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := FindCache(ctx, pool, storageNow, repo, []string{k + "nuova"}, "v1", main, main); !errors.Is(err, pgx.ErrNoRows) {
				t.Errorf("cache non confermata trovata: %v", err)
			}
			if _, err := ReserveCache(ctx, pool, storageNow, repo, k+"nuova", "v1", main, 4, st); !errors.Is(err, ErrCacheExists) {
				t.Errorf("seconda prenotazione: %v", err)
			}
			// Dopo un'ora una prenotazione abbandonata non blocca più.
			if _, err := ReserveCache(ctx, pool, storageNow.Add(2*time.Hour), repo, k+"nuova", "v1", main, 4, st); err != nil {
				t.Errorf("prenotazione abbandonata: %v", err)
			}
			_ = res
			// Più grande della quota: rifiutata subito.
			small := st
			small.CacheQuotaBytes = 3
			if _, err := ReserveCache(ctx, pool, storageNow, repo, k+"enorme", "v1", main, 4, small); !errors.Is(err, ErrTooLarge) {
				t.Errorf("oltre la quota: %v", err)
			}
		})
	}
}

func TestStorage_Cache_QuotaEInutilizzate(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	repo := seedStorageRepo(t, pool, "app")
	other := seedStorageRepo(t, pool, "altro")
	for name, blobs := range backends(t) {
		t.Run(name, func(t *testing.T) {
			st := DefaultSettings()
			st.CacheQuotaBytes = 25
			ref := "refs/heads/main"
			a := saveCache(t, pool, blobs, storageNow, repo, name+"-a", "v", ref, strings.Repeat("a", 10), st)
			b := saveCache(t, pool, blobs, storageNow.Add(time.Minute), repo, name+"-b", "v", ref, strings.Repeat("b", 10), st)
			// Un altro repo ha la sua quota: non si tocca.
			o := saveCache(t, pool, blobs, storageNow, other, name+"-o", "v", ref, strings.Repeat("o", 20), st)
			// «a» è usata di recente: la meno usata diventa «b».
			if _, err := FindCache(ctx, pool, storageNow.Add(2*time.Minute), repo, []string{name + "-a"}, "v", ref, ref); err != nil {
				t.Fatal(err)
			}
			// La terza (10 byte) manda il repo a 30 > 25: via la meno usata di recente.
			c := saveCache(t, pool, blobs, storageNow.Add(3*time.Minute), repo, name+"-c", "v", ref, strings.Repeat("c", 10), st)
			if _, err := GetCache(ctx, pool, repo, b.ID); !errors.Is(err, pgx.ErrNoRows) {
				t.Errorf("«b» (la meno usata) doveva essere eliminata: %v", err)
			}
			if _, err := readAll(t, blobs, b.StorageKey); !errors.Is(err, ErrBlobNotFound) {
				t.Errorf("file di «b»: %v", err)
			}
			for _, keep := range []Cache{a, c} {
				if _, err := GetCache(ctx, pool, repo, keep.ID); err != nil {
					t.Errorf("%s doveva restare: %v", keep.Key, err)
				}
			}
			if _, err := GetCache(ctx, pool, other, o.ID); err != nil {
				t.Errorf("la cache di un altro repo non si tocca: %v", err)
			}
			if used, _ := CacheUsedBytes(ctx, pool, repo); used != 20 {
				t.Errorf("uso del repo = %d, atteso 20", used)
			}
			// Inutilizzate da 7 giorni: «a» (usata a +2 min) e «c» (+3 min) escono a +7 giorni e 4 minuti.
			if n, err := EvictCaches(ctx, pool, blobs, storageNow.AddDate(0, 0, 7), nil, st); err != nil || n != 0 {
				t.Errorf("a 7 giorni dall'inizio nulla è ancora scaduto: %d, %v", n, err)
			}
			n, err := EvictCaches(ctx, pool, blobs, storageNow.AddDate(0, 0, 7).Add(4*time.Minute), nil, st)
			if err != nil || n < 2 {
				t.Fatalf("a 7 giorni dall'ultimo uso: %d, %v", n, err)
			}
			if l, _ := ListCaches(ctx, pool, repo, "", ""); len(l) != 0 {
				t.Errorf("cache rimaste: %+v", l)
			}
			if _, err := readAll(t, blobs, a.StorageKey); !errors.Is(err, ErrBlobNotFound) {
				t.Errorf("file di «a»: %v", err)
			}
		})
	}
}

func TestStorage_Cache_UnitàDiQuotaDelRegolabile(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	repo := seedStorageRepo(t, pool, "app")
	blobs := DiskBlobs{Dir: t.TempDir()}
	st := DefaultSettings()
	ref := "refs/heads/main"
	saveCache(t, pool, blobs, storageNow, repo, "uno", "v", ref, "1234567890", st)
	saveCache(t, pool, blobs, storageNow, repo, "due", "v", "refs/heads/x", "1234567890", st)
	// L'amministratore abbassa il tetto: il giro di pulizia lo applica.
	st.CacheQuotaBytes = 10
	n, err := EvictCaches(ctx, pool, blobs, storageNow, nil, st)
	if err != nil || n != 1 {
		t.Fatalf("pulizia con quota ridotta: %d, %v", n, err)
	}
	// Eliminazione per prefisso e per ref.
	saveCache(t, pool, blobs, storageNow, repo, "pre-1", "v", ref, "a", DefaultSettings())
	saveCache(t, pool, blobs, storageNow, repo, "pre-2", "v", "refs/heads/y", "b", DefaultSettings())
	if l, _ := ListCaches(ctx, pool, repo, "pre-", "refs/heads/y"); len(l) != 1 || l[0].Key != "pre-2" {
		t.Errorf("filtro key+ref: %+v", l)
	}
	if n, err := DeleteCaches(ctx, pool, blobs, repo, "pre-", "refs/heads/y"); err != nil || n != 1 {
		t.Errorf("DeleteCaches per ref: %d, %v", n, err)
	}
	if n, err := DeleteCaches(ctx, pool, blobs, repo, "", ""); err != nil || n < 1 {
		t.Errorf("DeleteCaches tutte: %d, %v", n, err)
	}
	if l, _ := ListCaches(ctx, pool, repo, "", ""); len(l) != 0 {
		t.Errorf("rimaste: %+v", l)
	}
}

func TestStorage_PurgeStorageOnce_ConservazioneRegolabile(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	repo := seedStorageRepo(t, pool, "app")
	logs := &LogStore{Dir: t.TempDir()}
	blobs := DiskBlobs{Dir: t.TempDir()}
	done := storageNow.AddDate(0, 0, -20)
	job := seedStorageJob(t, pool, repo, "completed", nil, nil)
	if _, err := pool.Exec(ctx, `UPDATE core.ci_jobs SET completed_at = $2, conclusion = 'success' WHERE id = $1`, job.JobID, done); err != nil {
		t.Fatal(err)
	}
	if _, err := logs.Append(ctx, pool, done, job.JobID, 0, []LogLine{{Time: done, Content: "riga"}}); err != nil {
		t.Fatal(err)
	}
	// Default (90 giorni): il log di 20 giorni resta.
	if l, _, _, err := PurgeStorageOnce(ctx, pool, logs, blobs, storageNow); err != nil || l != 0 {
		t.Fatalf("a 90 giorni: %d, %v", l, err)
	}
	// L'amministratore porta la conservazione a 14 giorni: il giro successivo lo elimina.
	st := DefaultSettings()
	st.LogRetentionDays = 14
	if _, err := SaveSettings(ctx, pool, storageNow, uuid.New(), st); err != nil {
		t.Fatal(err)
	}
	l, _, _, err := PurgeStorageOnce(ctx, pool, logs, blobs, storageNow)
	if err != nil || l != 1 {
		t.Fatalf("a 14 giorni: %d, %v", l, err)
	}
}

func TestStorage_QueueStats(t *testing.T) {
	pool, _ := dbtest.NewPool(t)
	ctx := context.Background()
	repo := seedStorageRepo(t, pool, "app")
	logs := &LogStore{Dir: t.TempDir()}
	blobs := DiskBlobs{Dir: t.TempDir()}

	s, err := ReadQueueStats(ctx, pool, storageNow)
	if err != nil || s != (QueueStats{}) {
		t.Fatalf("a vuoto tutto vale 0: %+v, %v", s, err)
	}
	at := func(d time.Duration) *time.Time { v := storageNow.Add(d); return &v }
	// Due job partiti nelle ultime 24 ore: attese 10 s e 30 s, media 20 s. Uno partito 3 giorni fa non conta.
	j1 := seedStorageJob(t, pool, repo, "in_progress", at(-time.Hour-10*time.Second), at(-time.Hour))
	seedStorageJob(t, pool, repo, "completed", at(-2*time.Hour-30*time.Second), at(-2*time.Hour))
	seedStorageJob(t, pool, repo, "completed", at(-72*time.Hour-time.Hour), at(-72*time.Hour))
	seedStorageJob(t, pool, repo, "queued", at(-time.Minute), nil)
	seedStorageJob(t, pool, repo, "queued", at(-time.Minute), nil)
	if _, err := logs.Append(ctx, pool, storageNow, j1.JobID, 0, []LogLine{{Time: storageNow, Content: "dodici byte"}}); err != nil {
		t.Fatal(err)
	}
	logSize, _ := logs.LogSize(ctx, pool, j1.JobID)

	st := DefaultSettings()
	if _, err := SaveArtifact(ctx, pool, blobs, storageNow, j1, "uno", strings.NewReader("12345"), st); err != nil {
		t.Fatal(err)
	}
	old, err := SaveArtifact(ctx, pool, blobs, storageNow.AddDate(0, 0, -40), j1, "scaduto", strings.NewReader("123456789"), st)
	if err != nil {
		t.Fatal(err)
	}
	_ = old
	saveCache(t, pool, blobs, storageNow, repo, "k", "v", "refs/heads/main", "1234567", st)
	if _, err := ReserveCache(ctx, pool, storageNow, repo, "prenotata", "v", "refs/heads/main", 100, st); err != nil {
		t.Fatal(err)
	}

	s, err = ReadQueueStats(ctx, pool, storageNow)
	if err != nil {
		t.Fatal(err)
	}
	want := QueueStats{Queued: 2, Running: 1, AvgWaitSeconds: 20, LogBytes: logSize, ArtifactBytes: 5, CacheBytes: 7}
	if s != want {
		t.Errorf("statistiche = %+v, attese %+v", s, want)
	}
}
