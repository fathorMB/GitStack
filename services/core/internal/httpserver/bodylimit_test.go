package httpserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type unknownLen struct{ r io.Reader }

func (u unknownLen) Read(p []byte) (int, error) { return u.r.Read(p) }

// Il tetto globale di core: oltre 1 MiB 413 body_too_large prima
// dell'handler; entro il tetto il corpo arriva intero; l'upload di un
// allegato ne è escluso (ha il suo tetto).
func TestLimitBody(t *testing.T) {
	var gotLen int
	h := limitBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotLen = len(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	big := strings.Repeat("a", int(maxRequestBodyBytes)+1)

	do := func(method, path string, body io.Reader, length int64) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, body)
		req.ContentLength = length
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	gotLen = -1
	rec := do(http.MethodPost, "/repos/a/b/webhooks", strings.NewReader(big), int64(len(big)))
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), `"body_too_large"`) || gotLen != -1 {
		t.Fatalf("Content-Length: status %d body %s handler=%d", rec.Code, rec.Body.String(), gotLen)
	}
	rec = do(http.MethodPatch, "/repos/a/b/labels/x", unknownLen{strings.NewReader(big)}, -1)
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), `"body_too_large"`) || gotLen != -1 {
		t.Fatalf("chunked: status %d body %s handler=%d", rec.Code, rec.Body.String(), gotLen)
	}
	rec = do(http.MethodPost, "/repos/a/b/webhooks", strings.NewReader(`{"a":1}`), 7)
	if rec.Code != http.StatusNoContent || gotLen != 7 {
		t.Fatalf("corpo piccolo: status %d, letti %d", rec.Code, gotLen)
	}
	rec = do(http.MethodPost, "/repos/a/b/issue-attachments", strings.NewReader(big), int64(len(big)))
	if rec.Code != http.StatusNoContent || gotLen != len(big) {
		t.Fatalf("allegato: status %d, letti %d", rec.Code, gotLen)
	}
	// GIT-571: anche l'upload di un allegato di release e' escluso, ma solo la rotta esatta.
	gotLen = -1
	rec = do(http.MethodPost, "/repos/a/b/releases/"+"11111111-1111-1111-1111-111111111111"+"/assets", strings.NewReader(big), int64(len(big)))
	if rec.Code != http.StatusNoContent || gotLen != len(big) {
		t.Fatalf("allegato di release: status %d, letti %d", rec.Code, gotLen)
	}
	gotLen = -1
	for _, p := range []string{"/repos/a/b/releases/x/y/assets", "/repos/a/b/releases"} {
		rec = do(http.MethodPost, p, strings.NewReader(big), int64(len(big)))
		if rec.Code != http.StatusRequestEntityTooLarge || gotLen != -1 {
			t.Fatalf("%s: status %d, letti %d", p, rec.Code, gotLen)
		}
	}
}
