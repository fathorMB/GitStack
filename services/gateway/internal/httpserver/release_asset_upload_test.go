package httpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/gateway/internal/identityclient"
	"github.com/fathorMB/GitStack/services/gateway/internal/proxy"
)

const (
	relAssetPath = "/v1/repos/alice/app/releases/00000000-0000-0000-0000-0000000000aa/assets?name=build.zip"
	relJSONPath  = "/v1/repos/alice/app/releases"
)

// assetCore è un core finto che legge il corpo in streaming, ne calcola lo
// sha256 e risponde 201 con {"sha256":...}; segnala quando ha letto
// `signalAt` byte.
type assetCore struct {
	srv      *httptest.Server
	mu       sync.Mutex
	calls    int
	signalAt int64
	signal   chan struct{}
	once     sync.Once
}

func newAssetCore(t *testing.T, signalAt int64) *assetCore {
	t.Helper()
	c := &assetCore{signalAt: signalAt, signal: make(chan struct{})}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.calls++
		c.mu.Unlock()
		h := sha256.New()
		buf := make([]byte, 32<<10)
		var n int64
		for {
			k, err := r.Body.Read(buf)
			h.Write(buf[:k])
			n += int64(k)
			if c.signalAt > 0 && n >= c.signalAt {
				c.once.Do(func() { close(c.signal) })
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"sha256": hex.EncodeToString(h.Sum(nil)), "sizeBytes": n})
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *assetCore) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func newAssetRouter(t *testing.T, coreURL string, coreTimeout, uploadTimeout time.Duration) http.Handler {
	t.Helper()
	cfg := newTestConfig(t, coreURL)
	cfg.IdentityTimeout = time.Second
	cfg.CoreTimeout = coreTimeout
	cfg.UploadTimeout = uploadTimeout
	v := &stubVerifier{fallback: &identityclient.Result{Active: true, Principal: identityclient.Caller{
		UserID: aliceID, Username: "alice", AuthMethod: "token", IsToken: true, Scopes: allScopes,
	}}}
	return NewRouter(cfg, discardLogger(), WithVerifier(v), WithPermissionChecker(&stubPermissions{}))
}

func assetRequest(path, token string, body io.Reader, length int64) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.ContentLength = length
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/octet-stream")
	return req
}

// pipeWriter scrive size byte a blocchi da chunk, aspettando delay tra uno e
// l'altro; written conta i byte già consegnati al lettore.
func pipeWriter(size, chunk int, delay time.Duration, written *atomic.Int64) (*io.PipeReader, []byte) {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i*31 + 7)
	}
	pr, pw := io.Pipe()
	go func() {
		for off := 0; off < size; off += chunk {
			end := min(off+chunk, size)
			if _, err := pw.Write(data[off:end]); err != nil {
				return
			}
			written.Add(int64(end - off))
			if delay > 0 {
				time.Sleep(delay)
			}
		}
		_ = pw.Close()
	}()
	return pr, data
}

// GIT-571: 5 MiB su uploadReleaseAsset attraversano il gateway con token
// utente e con token del job, in streaming (a core arrivano mentre il client
// sta ancora scrivendo), e core vede lo sha256 giusto.
func TestReleaseAssetUpload_5MiBInStreaming(t *testing.T) {
	for _, token := range []string{"gst_prova", "gsj_token-del-job"} {
		t.Run(token, func(t *testing.T) {
			const size = 5 << 20
			core := newAssetCore(t, 256<<10)
			h := newAssetRouter(t, core.srv.URL, 5*time.Second, 0)
			var written atomic.Int64
			body, data := pipeWriter(size, 64<<10, 0, &written)

			var atSignal atomic.Int64
			go func() {
				<-core.signal
				atSignal.Store(written.Load())
			}()

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, assetRequest(relAssetPath, token, body, size))
			if rec.Code != http.StatusCreated {
				t.Fatalf("status %d, voluto 201: %s", rec.Code, rec.Body.String())
			}
			sum := sha256.Sum256(data)
			var got struct {
				Sha256    string `json:"sha256"`
				SizeBytes int64  `json:"sizeBytes"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Sha256 != hex.EncodeToString(sum[:]) || got.SizeBytes != size {
				t.Errorf("core ha visto sha256=%s size=%d", got.Sha256, got.SizeBytes)
			}
			// Streaming: quando core aveva letto 256 KiB il client non aveva
			// ancora scritto tutto (un gateway che bufferizza avrebbe già letto 5 MiB).
			if w := atSignal.Load(); w == 0 || w >= size/2 {
				t.Errorf("a core i primi 256 KiB sono arrivati con %d byte scritti dal client su %d: il corpo non passa in streaming", w, size)
			}
		})
	}
}

// Oltre 2 GiB dichiarati: 413 dal gateway, senza arrivare a core. Le altre
// rotte, POST /releases in JSON compreso, restano a 1 MiB.
func TestReleaseAssetUpload_TettoEAltreRotteA1MiB(t *testing.T) {
	core := newAssetCore(t, 0)
	h := newAssetRouter(t, core.srv.URL, 5*time.Second, 0)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, assetRequest(relAssetPath, "gst_prova", strings.NewReader("x"), proxy.DefaultMaxReleaseAssetBytes+1))
	if rec.Code != http.StatusRequestEntityTooLarge || errorCode(t, rec) != "body_too_large" {
		t.Fatalf("oltre 2 GiB: status %d %s", rec.Code, rec.Body.String())
	}
	if core.count() != 0 {
		t.Errorf("richieste a core = %d, voluto 0", core.count())
	}

	// Esattamente 2 GiB dichiarati passa il controllo del gateway (non si invia il corpo).
	// Qui basta provare 1 MiB + 1 sulle altre rotte.
	for _, path := range []string{relJSONPath, "/v1/repos/alice/app/issues"} {
		rec = httptest.NewRecorder()
		req := assetRequest(path, "gst_prova", strings.NewReader("x"), proxy.DefaultMaxBodyBytes+1)
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s: status %d, voluto 413 oltre 1 MiB", path, rec.Code)
		}
	}
	if core.count() != 0 {
		t.Errorf("richieste a core = %d, voluto 0", core.count())
	}
}

// Il trasferimento del corpo di un allegato non è tagliato da coreTimeout
// (200ms) ma da uploadTimeout; le altre rotte restano sotto coreTimeout.
func TestReleaseAssetUpload_NonTagliatoDaCoreTimeout(t *testing.T) {
	const size = 5 * 64 << 10 // 5 blocchi da 64 KiB, uno ogni 120 ms: ~600 ms totali

	core := newAssetCore(t, 0)
	h := newAssetRouter(t, core.srv.URL, 200*time.Millisecond, 10*time.Second)

	var written atomic.Int64
	body, _ := pipeWriter(size, 64<<10, 120*time.Millisecond, &written)
	start := time.Now()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, assetRequest(relAssetPath, "gst_prova", body, size))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload lento: status %d, voluto 201: %s", rec.Code, rec.Body.String())
	}
	if time.Since(start) < 400*time.Millisecond {
		t.Fatalf("il corpo doveva superare coreTimeout, durata %s", time.Since(start))
	}

	// Stesso corpo lento su una rotta normale: tagliato da coreTimeout (503).
	written.Store(0)
	body, _ = pipeWriter(size, 64<<10, 120*time.Millisecond, &written)
	rec = httptest.NewRecorder()
	req := assetRequest(relJSONPath, "gst_prova", body, size)
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("rotta normale lenta: status %d, voluto 503 core_unavailable", rec.Code)
	}
}

// uploadTimeout si applica: con un timeout degli upload corto un corpo lento cade.
func TestReleaseAssetUpload_UploadTimeoutConfigurabile(t *testing.T) {
	core := newAssetCore(t, 0)
	h := newAssetRouter(t, core.srv.URL, 5*time.Second, 200*time.Millisecond)
	var written atomic.Int64
	body, _ := pipeWriter(5*64<<10, 64<<10, 120*time.Millisecond, &written)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, assetRequest(relAssetPath, "gst_prova", body, 5*64<<10))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, voluto 503 per uploadTimeout scaduto", rec.Code)
	}
}
