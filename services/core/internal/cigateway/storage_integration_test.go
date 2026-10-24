//go:build integration

package cigateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/cigateway"
	"github.com/google/uuid"
)

// storageGateway serve, sullo stesso database di e, un gateway con l'archiviazione di artefatti e cache.
func storageGateway(t *testing.T, e *env, blobs ci.Blobs) *httptest.Server {
	t.Helper()
	h := cigateway.New(cigateway.Config{Pool: e.pool, Source: e.src, Reader: e.rd, Logs: e.logs, Blobs: blobs, SpoolDir: t.TempDir(),
		Now: e.now, PollTimeout: 400 * time.Millisecond})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func call(t *testing.T, srv *httptest.Server, method, path, token, ctype, contentRange string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if contentRange != "" {
		req.Header.Set("Content-Range", contentRange)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

// jobToken mette in corso il prossimo job in coda e ne ritorna il token.
func (e *env) jobToken() string {
	e.t.Helper()
	task, err := e.fetch(runnerSecret)
	if err != nil || task == nil {
		e.t.Fatalf("fetch: %v %v", task, err)
	}
	return taskToken(e.t, task)
}

// TestStorage_ArtefattiDalGateway: il job carica un artefatto col suo token; il nome è unico
// nell'esecuzione, il limite regolabile dall'amministratore si applica, senza token o senza
// archiviazione non si carica.
func TestStorage_ArtefattiDalGateway(t *testing.T) {
	e := newEnv(t)
	e.queue("app", "a")
	tok := e.jobToken()
	blobs := ci.DiskBlobs{Dir: t.TempDir()}
	srv := storageGateway(t, e, blobs)

	if st, _ := call(t, srv, "PUT", cigateway.ArtifactsPath+"dist", "", "", "", []byte("zip")); st != http.StatusUnauthorized {
		t.Errorf("senza token: %d", st)
	}
	if st, _ := call(t, srv, "PUT", cigateway.ArtifactsPath+"dist", ci.JobTokenPrefix+"sconosciuto", "", "", []byte("zip")); st != http.StatusUnauthorized {
		t.Errorf("token sconosciuto: %d", st)
	}
	st, body := call(t, srv, "PUT", cigateway.ArtifactsPath+"dist", tok, "application/zip", "", []byte("PK-contenuto"))
	if st != http.StatusCreated {
		t.Fatalf("upload: %d %s", st, body)
	}
	var out struct {
		ID        uuid.UUID
		SizeBytes int64
		ExpiresAt time.Time
	}
	if err := json.Unmarshal(body, &out); err != nil || out.SizeBytes != 12 || !out.ExpiresAt.Equal(e.now().AddDate(0, 0, 30)) {
		t.Fatalf("risposta: %+v %v", out, err)
	}
	if st, _ := call(t, srv, "PUT", cigateway.ArtifactsPath+"dist", tok, "", "", []byte("altro")); st != http.StatusConflict {
		t.Errorf("stesso nome: %d", st)
	}
	if st, _ := call(t, srv, "PUT", cigateway.ArtifactsPath+"..%2Fx%00", tok, "", "", []byte("x")); st != http.StatusBadRequest {
		t.Errorf("nome non valido: %d", st)
	}
	if st, _ := call(t, srv, "GET", cigateway.ArtifactsPath+"dist", tok, "", "", nil); st != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", st)
	}
	// L'amministratore abbassa il limite: oltre, 413.
	set := ci.DefaultSettings()
	set.ArtifactMaxBytes = 4
	if _, err := ci.SaveSettings(context.Background(), e.pool, e.now(), uuid.New(), set); err != nil {
		t.Fatal(err)
	}
	if st, _ := call(t, srv, "PUT", cigateway.ArtifactsPath+"grande", tok, "", "", []byte("12345")); st != http.StatusRequestEntityTooLarge {
		t.Errorf("oltre il limite: %d", st)
	}
	if st, _ := call(t, srv, "PUT", cigateway.ArtifactsPath+"piccolo", tok, "", "", []byte("1234")); st != http.StatusCreated {
		t.Errorf("entro il limite: %d", st)
	}
	// Senza archiviazione configurata: 503.
	bare := storageGateway(t, e, nil)
	if st, _ := call(t, bare, "PUT", cigateway.ArtifactsPath+"x", tok, "", "", []byte("x")); st != http.StatusServiceUnavailable {
		t.Errorf("senza archiviazione: %d", st)
	}
}

// TestStorage_CacheProtocolloV1: prenota, carica in due pezzi, conferma; poi la ricerca per chiave
// trova la cache e l'archiveLocation la scarica. Un altro repo non la vede né la scarica.
func TestStorage_CacheProtocolloV1(t *testing.T) {
	e := newEnv(t)
	e.queue("app", "a")
	e.queue("altro", "b")
	tokA := e.jobToken()
	tokB := e.jobToken()
	srv := storageGateway(t, e, ci.DiskBlobs{Dir: t.TempDir()})
	api := cigateway.CachePath + "_apis/artifactcache/"

	if st, _ := call(t, srv, "GET", api+"cache?keys=npm-1&version=v1", "", "", "", nil); st != http.StatusUnauthorized {
		t.Errorf("senza token: %d", st)
	}
	if st, _ := call(t, srv, "GET", api+"cache?keys=npm-1&version=v1", tokA, "", "", nil); st != http.StatusNoContent {
		t.Errorf("miss: %d", st)
	}
	archive := []byte("0123456789ABCDEF")
	st, body := call(t, srv, "POST", api+"caches", tokA, "application/json", "", []byte(`{"key":"npm-1","version":"v1","cacheSize":16}`))
	if st != http.StatusCreated {
		t.Fatalf("prenotazione: %d %s", st, body)
	}
	var res struct {
		CacheID int64 `json:"cacheId"`
	}
	if err := json.Unmarshal(body, &res); err != nil || res.CacheID < 1 {
		t.Fatalf("cacheId: %s %v", body, err)
	}
	cpath := fmt.Sprintf("%scaches/%d", api, res.CacheID)
	// Finché non è confermata non si trova.
	if st, _ := call(t, srv, "GET", api+"cache?keys=npm-1&version=v1", tokA, "", "", nil); st != http.StatusNoContent {
		t.Errorf("cache non confermata: %d", st)
	}
	// I pezzi possono arrivare in qualunque ordine.
	if st, b := call(t, srv, "PATCH", cpath, tokA, "application/octet-stream", "bytes 8-15/*", archive[8:]); st != http.StatusNoContent {
		t.Fatalf("pezzo 2: %d %s", st, b)
	}
	if st, b := call(t, srv, "PATCH", cpath, tokA, "application/octet-stream", "bytes 0-7/*", archive[:8]); st != http.StatusNoContent {
		t.Fatalf("pezzo 1: %d %s", st, b)
	}
	if st, _ := call(t, srv, "PATCH", cpath, tokA, "", "bytes 0-7/*", archive[:4]); st != http.StatusBadRequest {
		t.Errorf("pezzo di dimensione diversa: %d", st)
	}
	if st, _ := call(t, srv, "POST", cpath, tokA, "application/json", "", []byte(`{"size":99}`)); st != http.StatusBadRequest {
		t.Errorf("conferma con dimensione sbagliata: %d", st)
	}
	if st, b := call(t, srv, "POST", cpath, tokA, "application/json", "", []byte(`{"size":16}`)); st != http.StatusNoContent {
		t.Fatalf("conferma: %d %s", st, b)
	}
	// Doppia prenotazione: 409.
	if st, _ := call(t, srv, "POST", api+"caches", tokA, "application/json", "", []byte(`{"key":"npm-1","version":"v1","cacheSize":16}`)); st != http.StatusConflict {
		t.Errorf("duplicata: %d", st)
	}
	// Ricerca con chiave di ripiego (prefisso).
	st, body = call(t, srv, "GET", api+"cache?keys=nessuna,npm-&version=v1", tokA, "", "", nil)
	if st != http.StatusOK {
		t.Fatalf("hit: %d %s", st, body)
	}
	var hit struct{ CacheKey, ArchiveLocation string }
	if err := json.Unmarshal(body, &hit); err != nil || hit.CacheKey != "npm-1" || !strings.Contains(hit.ArchiveLocation, cigateway.CachePath+"blob/") {
		t.Fatalf("hit: %s %v", body, err)
	}
	dl := strings.TrimPrefix(hit.ArchiveLocation, srv.URL)
	st, got := call(t, srv, "GET", dl, tokA, "", "", nil)
	if st != http.StatusOK || !bytes.Equal(got, archive) {
		t.Errorf("download: %d %q", st, got)
	}
	// Un altro repo non la trova, non la scarica e non scrive nei suoi pezzi.
	if st, _ := call(t, srv, "GET", api+"cache?keys=npm-1&version=v1", tokB, "", "", nil); st != http.StatusNoContent {
		t.Errorf("altro repo, ricerca: %d", st)
	}
	if st, _ := call(t, srv, "GET", dl, tokB, "", "", nil); st != http.StatusNotFound {
		t.Errorf("altro repo, download: %d", st)
	}
	if st, _ := call(t, srv, "PATCH", cpath, tokB, "", "bytes 0-3/*", archive[:4]); st != http.StatusNotFound {
		t.Errorf("altro repo, scrittura: %d", st)
	}
	// Oltre la quota regolabile: 413 alla prenotazione.
	set := ci.DefaultSettings()
	set.CacheQuotaBytes = 8
	if _, err := ci.SaveSettings(context.Background(), e.pool, e.now(), uuid.New(), set); err != nil {
		t.Fatal(err)
	}
	if st, _ := call(t, srv, "POST", api+"caches", tokA, "application/json", "", []byte(`{"key":"grande","version":"v1","cacheSize":16}`)); st != http.StatusRequestEntityTooLarge {
		t.Errorf("oltre la quota: %d", st)
	}
	// Altri percorsi del gateway restano 404.
	if st, _ := call(t, srv, "GET", "/ci/v1/altro", tokA, "", "", nil); st != http.StatusNotFound {
		t.Errorf("percorso fuori protocollo: %d", st)
	}
}
