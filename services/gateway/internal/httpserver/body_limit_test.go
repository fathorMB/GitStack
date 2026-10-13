package httpserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/gateway/internal/identityclient"
	"github.com/fathorMB/GitStack/services/gateway/internal/proxy"
)

// readingCore è un core finto che legge tutto il corpo e risponde 204: così
// il tetto del gateway si vede anche quando il corpo non ha Content-Length.
type readingCore struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls int
	bytes int64
}

func newReadingCore(t *testing.T) *readingCore {
	t.Helper()
	c := &readingCore{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		c.mu.Lock()
		c.calls++
		c.bytes += n
		c.mu.Unlock()
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *readingCore) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// zeroReader produce n byte senza Content-Length noto.
type zeroReader struct{ left int64 }

func (z *zeroReader) Read(p []byte) (int, error) {
	if z.left <= 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > z.left {
		n = z.left
	}
	z.left -= n
	return int(n), nil
}

// Un corpo oltre il tetto non arriva a core: 413 body_too_large con l'errore
// del contratto, sia con Content-Length dichiarato sia con corpo chunked; gli
// upload di allegati hanno un tetto più ampio, e un corpo piccolo passa.
func TestBodyTooLarge_413(t *testing.T) {
	const issuesPath = "/v1/repos/alice/prova/issues"
	const attachPath = "/v1/repos/alice/prova/issue-attachments"

	newRouter := func(t *testing.T, maxBody, maxAttach int64) (http.Handler, *readingCore) {
		t.Helper()
		core := newReadingCore(t)
		cfg := newTestConfig(t, core.srv.URL)
		cfg.MaxBodyBytes, cfg.MaxAttachmentBytes = maxBody, maxAttach
		cfg.IdentityTimeout = time.Second
		cfg.CoreTimeout = 5 * time.Second
		v := &stubVerifier{fallback: &identityclient.Result{Active: true, Principal: identityclient.Caller{
			UserID: aliceID, Username: "alice", AuthMethod: "token", IsToken: true, Scopes: allScopes,
		}}}
		return NewRouter(cfg, discardLogger(), WithVerifier(v), WithPermissionChecker(&stubPermissions{})), core
	}
	do := func(h http.Handler, path string, body io.Reader, length int64) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, body)
		req.ContentLength = length
		req.Header.Set("Authorization", "Bearer gst_prova")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	t.Run("Content-Length oltre il tetto", func(t *testing.T) {
		h, core := newRouter(t, 1024, 0)
		rec := do(h, issuesPath, strings.NewReader(strings.Repeat("a", 4096)), 4096)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status %d, voluto 413 (%s)", rec.Code, rec.Body.String())
		}
		if got := errorCode(t, rec); got != "body_too_large" {
			t.Errorf("code = %s, voluto body_too_large", got)
		}
		if core.count() != 0 {
			t.Errorf("richieste a core = %d, voluto 0", core.count())
		}
	})

	t.Run("corpo chunked oltre il tetto", func(t *testing.T) {
		h, _ := newRouter(t, 1024, 0)
		rec := do(h, issuesPath, &zeroReader{left: 1 << 20}, -1)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status %d, voluto 413 (%s)", rec.Code, rec.Body.String())
		}
		if got := errorCode(t, rec); got != "body_too_large" {
			t.Errorf("code = %s, voluto body_too_large", got)
		}
	})

	t.Run("corpo entro il tetto passa", func(t *testing.T) {
		h, core := newRouter(t, 1024, 0)
		rec := do(h, issuesPath, strings.NewReader(`{"title":"x"}`), 13)
		if rec.Code != http.StatusNoContent || core.count() != 1 {
			t.Fatalf("status %d, richieste a core %d: voluto 204 e 1", rec.Code, core.count())
		}
	})

	t.Run("tetto di default 1 MiB", func(t *testing.T) {
		h, _ := newRouter(t, 0, 0)
		rec := do(h, issuesPath, &zeroReader{left: proxy.DefaultMaxBodyBytes + 1}, -1)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status %d, voluto 413", rec.Code)
		}
	})

	t.Run("allegato: tetto più ampio del JSON, ma esiste", func(t *testing.T) {
		h, core := newRouter(t, 1024, 64<<10)
		// 100 KiB: oltre il tetto JSON (1 KiB), entro quello degli allegati.
		rec := do(h, attachPath, &zeroReader{left: 100 << 10}, 100<<10)
		if rec.Code != http.StatusNoContent || core.count() != 1 {
			t.Fatalf("allegato entro il tetto: status %d, core %d", rec.Code, core.count())
		}
		rec = do(h, attachPath, &zeroReader{left: 1 << 20}, -1)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("allegato oltre il tetto: status %d, voluto 413", rec.Code)
		}
	})
}
