package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/fathorMB/GitStack/services/gateway/internal/identityclient"
	"github.com/fathorMB/GitStack/services/gateway/internal/security"
)

const testSecret = "segreto-di-servizio-di-prova"

// stubVerifier è un identityclient.Verifier finto: risponde in base alla
// credenziale e conta le chiamate.
type stubVerifier struct {
	mu      sync.Mutex
	results map[string]identityclient.Result
	// fallback: risposta per le credenziali non in results (default: non attiva).
	fallback *identityclient.Result
	err      error
	calls    int
}

func (s *stubVerifier) Verify(_ context.Context, credential string, _ identityclient.Kind) (identityclient.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return identityclient.Result{}, s.err
	}
	if r, ok := s.results[credential]; ok {
		return r, nil
	}
	if s.fallback != nil {
		return *s.fallback, nil
	}
	return identityclient.Result{}, nil
}

var allScopes = []string{"read:user", "write:user", "read:org", "write:org", "admin:org", "read:resource", "write:resource"}

// allowAll accetta qualunque credenziale come token con tutti gli scope.
func allowAll() *stubVerifier {
	return &stubVerifier{fallback: &identityclient.Result{Active: true, Principal: identityclient.Caller{
		UserID: "11111111-1111-1111-1111-111111111111", Username: "alice", AuthMethod: "token", IsToken: true, Scopes: allScopes,
	}}}
}

// authedRequestFor come authedRequest, ma con la credenziale ammessa dalla
// rotta dichiarata (cookie per le rotte solo-sessione, come il logout).
func authedRequestFor(table *security.Table, method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	if m, ok := table.Lookup(method, strings.TrimPrefix(target, "/v1")); ok && !m.Route.Public && !m.Route.Accepts(security.CredentialToken) {
		req.AddCookie(&http.Cookie{Name: "gst_session", Value: "sessione-prova"})
		return req
	}
	req.Header.Set("Authorization", "Bearer gst_prova")
	return req
}

// authedRequest crea una richiesta con un token qualsiasi.
func authedRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Authorization", "Bearer gst_prova")
	return req
}
