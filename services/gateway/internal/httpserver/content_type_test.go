package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/gateway/internal/identityclient"
)

// F15 (V13.1.5, V13.2.5): una rotta con corpo JSON rifiuta con 415 un corpo
// di altro tipo (un form text/plain cross-site non arriva a core); senza
// corpo, con application/json (anche con charset) e sulle rotte multipart
// passa come prima.
func TestJSONBodyNeedsContentType_415(t *testing.T) {
	core := newReadingCore(t)
	cfg := newTestConfig(t, core.srv.URL)
	cfg.IdentityTimeout = time.Second
	cfg.CoreTimeout = 5 * time.Second
	v := &stubVerifier{fallback: &identityclient.Result{Active: true, Principal: identityclient.Caller{
		UserID: aliceID, Username: "alice", AuthMethod: "token", IsToken: true, Scopes: allScopes,
	}}}
	h := NewRouter(cfg, discardLogger(), WithVerifier(v), WithPermissionChecker(&stubPermissions{}))

	do := func(method, path, contentType, body string) *httptest.ResponseRecorder {
		var req *http.Request
		if body == "" {
			req = httptest.NewRequest(method, path, nil)
		} else {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		}
		req.Header.Set("Authorization", "Bearer gst_prova")
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	const issues = "/v1/repos/alice/prova/issues"

	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", "application/xml", ""} {
		before := core.count()
		rec := do(http.MethodPost, issues, ct, `{"title":"x"}`)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %q: status %d, voluto 415 (%s)", ct, rec.Code, rec.Body.String())
			continue
		}
		if got := errorCode(t, rec); got != "unsupported_media_type" {
			t.Errorf("Content-Type %q: code = %s", ct, got)
		}
		if core.count() != before {
			t.Errorf("Content-Type %q: la richiesta è arrivata a core", ct)
		}
	}

	for _, ct := range []string{"application/json", "application/json; charset=utf-8", "Application/JSON"} {
		if rec := do(http.MethodPost, issues, ct, `{"title":"x"}`); rec.Code != http.StatusNoContent {
			t.Errorf("Content-Type %q: status %d, voluto 204", ct, rec.Code)
		}
	}

	// Senza corpo il Content-Type non conta (azioni come reopen).
	if rec := do(http.MethodPost, issues+"/1/reopen", "", ""); rec.Code != http.StatusNoContent {
		t.Errorf("POST senza corpo: status %d, voluto 204 (%s)", rec.Code, rec.Body.String())
	}
	// Gli upload multipart non sono rotte JSON.
	if rec := do(http.MethodPost, "/v1/repos/alice/prova/issue-attachments", "multipart/form-data; boundary=x", "--x--"); rec.Code != http.StatusNoContent {
		t.Errorf("upload multipart: status %d, voluto 204 (%s)", rec.Code, rec.Body.String())
	}
}
