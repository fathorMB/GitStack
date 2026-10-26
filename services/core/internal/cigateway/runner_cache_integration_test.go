//go:build integration

package cigateway_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	runnerv1 "code.gitea.io/actions-proto-go/runner/v1"
	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/cigateway"
	"github.com/fathorMB/GitStack/services/core/internal/cirunner"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// TestMain: con CIGATEWAY_FAKE_ACT=1 il binario di test fa da `act` e, al posto del passo
// `actions/cache`, parla il protocollo v1 col gateway usando solo le variabili d'ambiente che il
// runner dà ai job (ACTIONS_CACHE_URL e ACTIONS_RUNTIME_TOKEN), come fa il toolkit di GitHub.
func TestMain(m *testing.M) {
	if os.Getenv("CIGATEWAY_FAKE_ACT") == "1" {
		os.Exit(fakeActCache())
	}
	os.Exit(m.Run())
}

const cacheKey = "npm-lock-abc"

var cachePayload = bytes.Repeat([]byte("contenuto-della-cache-"), 50)

// fakeActCache: ACTIONS_RUNTIME_TOKEN non e' il token opaco del job ma il JWT che lo avvolge
// (cirunner.RuntimeToken, GIT-393), come per actions/cache; il Bearer resta il token avvolto.
func fakeActCache() int {
	base, tok := os.Getenv("ACTIONS_CACHE_URL"), os.Getenv("ACTIONS_RUNTIME_TOKEN")
	if base == "" || tok == "" || os.Getenv("GITHUB_TOKEN") == "" || tok != cirunner.RuntimeToken(os.Getenv("GITHUB_TOKEN")) {
		fmt.Println("Error: ACTIONS_CACHE_URL/ACTIONS_RUNTIME_TOKEN assenti nell'ambiente del job")
		return 1
	}
	api := base + "_apis/artifactcache/"
	do := func(method, u, ctype, crange string, body []byte) (int, []byte) {
		req, _ := http.NewRequest(method, u, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Accept", "application/json;api-version=6.0-preview.1")
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		if crange != "" {
			req.Header.Set("Content-Range", crange)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Println("Error:", err)
			return 0, nil
		}
		defer func() { _ = res.Body.Close() }()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	// Ripristino.
	st, b := do("GET", api+"cache?keys="+url.QueryEscape(cacheKey)+"&version=v1", "", "", nil)
	switch st {
	case http.StatusOK:
		var hit struct{ ArchiveLocation string }
		if err := json.Unmarshal(b, &hit); err != nil {
			fmt.Println("Error: risposta di ricerca:", err)
			return 1
		}
		st, data := do("GET", hit.ArchiveLocation, "", "", nil)
		if st != http.StatusOK || !bytes.Equal(data, cachePayload) {
			fmt.Printf("Error: download della cache: %d (%d byte)\n", st, len(data))
			return 1
		}
		fmt.Println("Cache restored from key: " + cacheKey)
		return 0
	case http.StatusNoContent:
		fmt.Println("Cache not found for input keys: " + cacheKey)
	default:
		fmt.Printf("Error: ricerca: %d %s\n", st, b)
		return 1
	}
	// Salvataggio in due pezzi.
	st, b = do("POST", api+"caches", "application/json", "", []byte(fmt.Sprintf(`{"key":%q,"version":"v1","cacheSize":%d}`, cacheKey, len(cachePayload))))
	var res struct {
		CacheID int64 `json:"cacheId"`
	}
	if st != http.StatusCreated || json.Unmarshal(b, &res) != nil {
		fmt.Printf("Error: prenotazione: %d %s\n", st, b)
		return 1
	}
	half := len(cachePayload) / 2
	id := fmt.Sprint(res.CacheID)
	if st, b = do("PATCH", api+"caches/"+id, "application/octet-stream", fmt.Sprintf("bytes 0-%d/*", half-1), cachePayload[:half]); st != http.StatusNoContent {
		fmt.Printf("Error: pezzo 1: %d %s\n", st, b)
		return 1
	}
	if st, b = do("PATCH", api+"caches/"+id, "application/octet-stream", fmt.Sprintf("bytes %d-%d/*", half, len(cachePayload)-1), cachePayload[half:]); st != http.StatusNoContent {
		fmt.Printf("Error: pezzo 2: %d %s\n", st, b)
		return 1
	}
	if st, b = do("POST", api+"caches/"+id, "application/json", "", []byte(fmt.Sprintf(`{"size":%d}`, len(cachePayload)))); st != http.StatusNoContent {
		fmt.Printf("Error: conferma: %d %s\n", st, b)
		return 1
	}
	fmt.Println("Cache saved with key: " + cacheKey)
	return 0
}

// tarReader serve al runner un archivio tar.gz vero del repo del job.
type tarReader struct{}

func (tarReader) ReadJSON(context.Context, trust.Identity, uuid.UUID, string, url.Values) (json.RawMessage, error) {
	return nil, fmt.Errorf("non usata")
}

func (tarReader) OpenStream(context.Context, trust.Identity, uuid.UUID, string, url.Values) (*gitclient.Stream, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	_ = tw.WriteHeader(&tar.Header{Name: "repo/", Typeflag: tar.TypeDir, Mode: 0o755})
	body := "ciao"
	_ = tw.WriteHeader(&tar.Header{Name: "repo/README.md", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
	_, _ = tw.Write([]byte(body))
	_ = tw.Close()
	_ = zw.Close()
	return &gitclient.Stream{Header: http.Header{}, Body: io.NopCloser(&buf)}, nil
}

const cacheWF = `name: cache
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo cache
`

// TestRunner_ActionsCacheSalvaERipristina: due job dello stesso repo girano con cirunner.Run contro
// il ci-gateway vero (Postgres reale). Il primo salva la cache, il secondo la ripristina, usando solo
// ACTIONS_CACHE_URL e ACTIONS_RUNTIME_TOKEN che il runner mette nell'ambiente. Poi: la cache è nel
// database, un altro repo non la vede e il token del job non vale più a job concluso.
func TestRunner_ActionsCacheSalvaERipristina(t *testing.T) {
	e := newEnv(t)
	repo := e.repo("app")
	push := func(commit string) {
		t.Helper()
		e.src.files[sha(commit)] = []ci.File{{Path: ".github/workflows/ci.yml", Content: []byte(cacheWF)}}
		ids, err := e.eng.OnPush(context.Background(), ci.Push{RepoID: repo, Pusher: uuid.New(),
			Refs: []ci.PushRef{{Ref: "refs/heads/main", Before: sha("0"), After: sha(commit)}}})
		if err != nil || len(ids) != 1 {
			t.Fatalf("push: %v %v", ids, err)
		}
	}
	// Gateway con archiviazione, che ai pod comunica il proprio indirizzo.
	srv := httptest.NewUnstartedServer(nil)
	srv.Config.Handler = cigateway.New(cigateway.Config{Pool: e.pool, Source: e.src, Reader: tarReader{}, Logs: e.logs,
		Blobs: ci.DiskBlobs{Dir: t.TempDir()}, SpoolDir: t.TempDir(), ArchiveURL: "http://" + srv.Listener.Addr().String(),
		Now: e.now, PollTimeout: 400 * time.Millisecond, PollEvery: 25 * time.Millisecond})
	srv.Start()
	t.Cleanup(srv.Close)

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CIGATEWAY_FAKE_ACT", "1")
	runJob := func() cirunner.Result {
		t.Helper()
		_, _, ticket, ok, err := ci.ClaimNextWithTicketReported(context.Background(), e.pool, e.now(), 3, "internal-pod", nil)
		if err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
		res, err := cirunner.Run(context.Background(), cirunner.Config{GatewayURL: srv.URL, Secret: ticket, Name: "pod-test", ActBin: exe,
			WorkDir: t.TempDir(), FetchTimeout: 3 * time.Second, FlushEvery: 20 * time.Millisecond, HTTP: srv.Client()})
		if err != nil || res.Conclusion != runnerv1.Result_RESULT_SUCCESS {
			t.Fatalf("job: %+v %v", res, err)
		}
		return res
	}

	push("a")
	runJob() // salva
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM core.ci_caches WHERE repo_id = $1 AND cache_key = $2`, repo, cacheKey).Scan(&n); err != nil || n != 1 {
		t.Fatalf("cache nel database: %d %v", n, err)
	}
	e.advance(time.Minute)
	push("b")
	runJob() // ripristina: il finto act fallisce se la cache non c'è o è diversa
	logs := e.jobLog(t, repo)
	if !strings.Contains(logs, "Cache saved with key: "+cacheKey) || !strings.Contains(logs, "Cache restored from key: "+cacheKey) {
		t.Errorf("log dei due job:\n%s", logs)
	}
}

// jobLog ritorna il log di tutti i job del repo, in ordine di esecuzione.
func (e *env) jobLog(t *testing.T, repo uuid.UUID) string {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT j.id FROM core.ci_jobs j JOIN core.ci_runs r ON r.id = j.run_id
		WHERE r.repo_id = $1 ORDER BY r.created_at, j.id`, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var all strings.Builder
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		size, err := e.logs.LogSize(context.Background(), e.pool, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.logs.ReadFrom(id, 0, size, &all); err != nil {
			t.Fatal(err)
		}
	}
	return all.String()
}

// TestStorage_ArtefattiV4Rifiutati: upload-artifact@v4 (Twirp su ACTIONS_RESULTS_URL) riceve un
// errore chiaro, non un 404 muto; senza token del job, 401.
func TestStorage_ArtefattiV4Rifiutati(t *testing.T) {
	e := newEnv(t)
	e.queue("app", "a")
	tok := e.jobToken()
	srv := storageGateway(t, e, ci.DiskBlobs{Dir: t.TempDir()})
	path := cigateway.TwirpArtifactPath + "CreateArtifact"
	if st, _ := call(t, srv, "POST", path, "", "application/json", "", []byte(`{}`)); st != http.StatusUnauthorized {
		t.Errorf("senza token: %d", st)
	}
	st, body := call(t, srv, "POST", path, tok, "application/json", "", []byte(`{}`))
	var tw struct{ Code, Msg string }
	if st != http.StatusNotImplemented || json.Unmarshal(body, &tw) != nil || tw.Code != "unimplemented" ||
		!strings.Contains(tw.Msg, "upload-artifact@v4") || !strings.Contains(tw.Msg, cigateway.ArtifactsPath[:len(cigateway.ArtifactsPath)-1]) {
		t.Errorf("v4: %d %s", st, body)
	}
	if st, _ := call(t, srv, "GET", path, tok, "", "", nil); st != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", st)
	}
	if st, _ := call(t, srv, "POST", "/twirp/altro.Servizio/Metodo", tok, "application/json", "", []byte(`{}`)); st != http.StatusNotFound {
		t.Errorf("altro servizio: %d", st)
	}
}
