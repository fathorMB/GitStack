//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/dbtest"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

// storageEnv: due repo (uno interno, uno privato), un'esecuzione col suo job, e il router con l'archiviazione.
func newStorageEnv(t *testing.T, withStorage bool) (*reposEnv, ci.Blobs, ci.JobAuth, uuid.UUID, *ci.LogStore) {
	t.Helper()
	pool, _ := dbtest.NewPool(t)
	id := newFakeIdentity()
	id.sysAdmin[carolID] = true
	git := &ciGit{&readerGit{fakeGit: newFakeGit()}}
	logs := &ci.LogStore{Dir: t.TempDir()}
	blobs := ci.DiskBlobs{Dir: t.TempDir()}
	opts := []httpserver.Option{
		httpserver.WithRepoIdentity(emailIdentity{id}), httpserver.WithReadableLister(id), httpserver.WithGit(git),
		httpserver.WithCILogs(logs), httpserver.WithCICapacity(2),
		httpserver.WithCloneConfig(httpserver.CloneConfig{PublicURL: "https://git.example.com"}),
	}
	if withStorage {
		opts = append(opts, httpserver.WithCIStorage(blobs))
	}
	router := httpserver.NewRouter(pool, events.NoopPublisher{}, trustSecret, opts...)
	e := &reposEnv{t: t, pool: pool, router: router, id: id, git: git.fakeGit}
	repo := e.create("alice", `{"owner":"alice","name":"app","visibility":"internal"}`)
	e.create("alice", `{"owner":"alice","name":"segreto","visibility":"private"}`)
	rec := e.do("POST", "/repos/alice/app/workflows/deploy.yml/dispatches", "alice", `{"ref":"main","inputs":{"env":"prod"}}`)
	e.want(rec, 202)
	var run struct {
		Id   string
		Jobs []struct{ Id string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil || len(run.Jobs) != 1 {
		t.Fatalf("esecuzione: %v %s", err, rec.Body.String())
	}
	auth := ci.JobAuth{JobID: uuid.MustParse(run.Jobs[0].Id), RunID: uuid.MustParse(run.Id), RepoID: uuid.UUID(*repo.Id)}
	return e, blobs, auth, auth.RunID, logs
}

// TestCI_ArtefattiLettura: elenco e download con `read`; un repo che non si legge è 404; l'artefatto
// scaduto sparisce; un altro repo non lo scarica.
func TestCI_ArtefattiLettura(t *testing.T) {
	e, blobs, job, runID, _ := newStorageEnv(t, true)
	ctx := context.Background()
	art, err := ci.SaveArtifact(ctx, e.pool, blobs, time.Now(), job, "dist", strings.NewReader("PK-zip"), ci.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	listPath := "/repos/alice/app/actions/runs/" + runID.String() + "/artifacts"
	rec := e.do("GET", listPath, "bob", "")
	e.want(rec, 200)
	var list openapi.ArtifactList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Items) != 1 || list.Items[0].Name != "dist" || list.Items[0].SizeBytes != 6 {
		t.Fatalf("elenco: %s %v", rec.Body.String(), err)
	}
	if d := time.Until(list.Items[0].ExpiresAt); d < 29*24*time.Hour || d > 30*24*time.Hour+time.Minute {
		t.Errorf("scade fra %v, attesi 30 giorni", d)
	}
	dl := "/repos/alice/app/actions/artifacts/" + art.ID.String() + "/download"
	rec = e.do("GET", dl, "bob", "")
	e.want(rec, 200)
	if rec.Body.String() != "PK-zip" || rec.Header().Get("Content-Type") != "application/zip" ||
		!strings.Contains(rec.Header().Get("Content-Disposition"), `filename="dist.zip"`) {
		t.Errorf("download: %q %v", rec.Body.String(), rec.Header())
	}
	// Repo non leggibile (privato di alice, bob non lo legge): 404; esecuzione di un altro repo: 404.
	e.want(e.do("GET", "/repos/alice/segreto/actions/runs/"+runID.String()+"/artifacts", "bob", ""), 404)
	e.want(e.do("GET", "/repos/alice/segreto/actions/artifacts/"+art.ID.String()+"/download", "alice", ""), 404)
	e.want(e.do("GET", "/repos/alice/app/actions/artifacts/"+uuid.NewString()+"/download", "bob", ""), 404)
	// Scaduto: tolto dall'elenco e non scaricabile.
	if _, err := e.pool.Exec(ctx, `UPDATE core.ci_artifacts SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	rec = e.do("GET", listPath, "bob", "")
	e.want(rec, 200)
	if strings.Contains(rec.Body.String(), "dist") {
		t.Errorf("artefatto scaduto nell'elenco: %s", rec.Body.String())
	}
	e.want(e.do("GET", dl, "bob", ""), 404)
}

func TestCI_ArtefattiSenzaArchiviazione503(t *testing.T) {
	e, blobs, job, _, _ := newStorageEnv(t, false)
	art, err := ci.SaveArtifact(context.Background(), e.pool, blobs, time.Now(), job, "dist", strings.NewReader("x"), ci.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	e.want(e.do("GET", "/repos/alice/app/actions/artifacts/"+art.ID.String()+"/download", "bob", ""), 503)
}

// TestCI_CacheLetturaEPermessi: elenco con quota, lettura con `read`, eliminazione solo con `write`.
func TestCI_CacheLetturaEPermessi(t *testing.T) {
	e, blobs, job, _, _ := newStorageEnv(t, true)
	ctx := context.Background()
	st := ci.DefaultSettings()
	mk := func(key, ref, body string) ci.Cache {
		c, err := ci.ReserveCache(ctx, e.pool, time.Now(), job.RepoID, key, "v1", ref, int64(len(body)), st)
		if err != nil {
			t.Fatal(err)
		}
		c, err = ci.CommitCache(ctx, e.pool, blobs, time.Now(), job.RepoID, c.Seq, strings.NewReader(body), st)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c1 := mk("npm-a", "refs/heads/main", "12345")
	mk("npm-b", "refs/heads/feat", "123")
	mk("go-a", "refs/heads/main", "12")

	rec := e.do("GET", "/repos/alice/app/actions/caches", "bob", "")
	e.want(rec, 200)
	var list openapi.ActionsCacheList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Items) != 3 || list.UsedBytes != 10 || list.QuotaBytes != 10<<30 {
		t.Fatalf("elenco: %s %v", rec.Body.String(), err)
	}
	rec = e.do("GET", "/repos/alice/app/actions/caches?key=npm-&ref=refs/heads/feat", "bob", "")
	e.want(rec, 200)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Items) != 1 || list.Items[0].Key != "npm-b" {
		t.Errorf("filtro: %s", rec.Body.String())
	}
	e.want(e.do("GET", "/repos/alice/app/actions/caches/"+c1.ID.String(), "bob", ""), 200)
	e.want(e.do("GET", "/repos/alice/app/actions/caches/"+uuid.NewString(), "bob", ""), 404)
	e.want(e.do("GET", "/repos/alice/segreto/actions/caches", "bob", ""), 404)
	e.want(e.do("GET", "/repos/alice/segreto/actions/caches/"+c1.ID.String(), "alice", ""), 404)

	// bob legge ma non scrive: 403; chi non legge: 404.
	e.want(e.do("DELETE", "/repos/alice/app/actions/caches/"+c1.ID.String(), "bob", ""), 403)
	e.want(e.do("DELETE", "/repos/alice/app/actions/caches?key=npm-", "bob", ""), 403)
	e.want(e.do("DELETE", "/repos/alice/segreto/actions/caches", "bob", ""), 404)
	// alice (admin) elimina una; poi per prefisso; poi tutte.
	e.want(e.do("DELETE", "/repos/alice/app/actions/caches/"+c1.ID.String(), "alice", ""), 204)
	e.want(e.do("GET", "/repos/alice/app/actions/caches/"+c1.ID.String(), "bob", ""), 404)
	if _, err := blobs.Open(ctx, c1.StorageKey); err == nil {
		t.Error("il file della cache eliminata doveva sparire")
	}
	e.want(e.do("DELETE", "/repos/alice/app/actions/caches/"+c1.ID.String(), "alice", ""), 404)
	rec = e.do("DELETE", "/repos/alice/app/actions/caches?key=npm-", "alice", "")
	e.want(rec, 200)
	if strings.TrimSpace(rec.Body.String()) != `{"deleted":1}` {
		t.Errorf("eliminate per prefisso: %s", rec.Body.String())
	}
	rec = e.do("DELETE", "/repos/alice/app/actions/caches", "alice", "")
	e.want(rec, 200)
	if strings.TrimSpace(rec.Body.String()) != `{"deleted":1}` {
		t.Errorf("eliminate tutte: %s", rec.Body.String())
	}
}

// TestCI_QueueSpazioEImpostazioni: GET /admin/ci/queue (solo amministratore) con attesa media e spazio
// di log, artefatti e cache; GET e PUT /admin/ci/storage, che l'amministratore regola.
func TestCI_QueueSpazioEImpostazioni(t *testing.T) {
	e, blobs, job, _, logs := newStorageEnv(t, true)
	ctx := context.Background()
	now := time.Now()
	e.want(e.do("GET", "/admin/ci/queue", "alice", ""), 403)
	e.want(e.do("GET", "/admin/ci/storage", "alice", ""), 403)
	e.want(e.do("PUT", "/admin/ci/storage", "alice", `{}`), 403)

	// Un job parte dopo 40 s di attesa; un secondo job resta in coda.
	if _, err := e.pool.Exec(ctx, `UPDATE core.ci_jobs SET status = 'in_progress', queued_at = $2, started_at = $3 WHERE id = $1`,
		job.JobID, now.Add(-time.Minute-40*time.Second), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := logs.Append(ctx, e.pool, now, job.JobID, 0, []ci.LogLine{{Time: now, Content: "log"}}); err != nil {
		t.Fatal(err)
	}
	size, _ := logs.LogSize(ctx, e.pool, job.JobID)
	if _, err := ci.SaveArtifact(ctx, e.pool, blobs, now, job, "dist", strings.NewReader("12345"), ci.DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	c, err := ci.ReserveCache(ctx, e.pool, now, job.RepoID, "k", "v", "refs/heads/main", 7, ci.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ci.CommitCache(ctx, e.pool, blobs, now, job.RepoID, c.Seq, strings.NewReader("1234567"), ci.DefaultSettings()); err != nil {
		t.Fatal(err)
	}

	rec := e.do("GET", "/admin/ci/queue", "carol", "")
	e.want(rec, 200)
	var q openapi.CiQueue
	if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil {
		t.Fatal(err)
	}
	if q.RunningJobs != 1 || q.QueuedJobs != 0 || q.MaxConcurrentJobs != 2 {
		t.Errorf("coda: %+v", q)
	}
	if q.AvgQueueWaitSeconds == nil || *q.AvgQueueWaitSeconds != 40 {
		t.Errorf("attesa media: %v", q.AvgQueueWaitSeconds)
	}
	if q.LogBytes == nil || *q.LogBytes != size || q.ArtifactBytes == nil || *q.ArtifactBytes != 5 || q.CacheBytes == nil || *q.CacheBytes != 7 {
		t.Errorf("spazio: log=%v artefatti=%v cache=%v (log atteso %d)", q.LogBytes, q.ArtifactBytes, q.CacheBytes, size)
	}
	if q.ResourceLimits.CpuCores != 2 || q.ResourceLimits.MaxDurationSeconds != 3600 {
		t.Errorf("limiti: %+v", q.ResourceLimits)
	}

	// Impostazioni: default di CI5, poi modifica.
	rec = e.do("GET", "/admin/ci/storage", "carol", "")
	e.want(rec, 200)
	var s openapi.CiStorageSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.LogRetentionDays != 90 || s.ArtifactRetentionDays != 30 || s.ArtifactMaxBytes != 2<<30 || s.CacheQuotaBytes != 10<<30 || s.CacheUnusedDays != 7 {
		t.Errorf("default: %+v", s)
	}
	e.want(e.do("PUT", "/admin/ci/storage", "carol", `{"logRetentionDays":0,"artifactRetentionDays":30,"artifactMaxBytes":1,"cacheQuotaBytes":1,"cacheUnusedDays":7}`), 422)
	rec = e.do("PUT", "/admin/ci/storage", "carol", `{"logRetentionDays":14,"artifactRetentionDays":5,"artifactMaxBytes":1048576,"cacheQuotaBytes":2097152,"cacheUnusedDays":3}`)
	e.want(rec, 200)
	got, err := ci.LoadSettings(ctx, e.pool)
	if err != nil || got != (ci.Settings{LogRetentionDays: 14, ArtifactRetentionDays: 5, ArtifactMaxBytes: 1 << 20, CacheQuotaBytes: 2 << 20, CacheUnusedDays: 3}) {
		t.Errorf("salvate: %+v %v", got, err)
	}
	rec = e.do("GET", "/admin/ci/storage", "carol", "")
	e.want(rec, 200)
	if !strings.Contains(rec.Body.String(), `"logRetentionDays":14`) {
		t.Errorf("lettura dopo la modifica: %s", rec.Body.String())
	}
}
