package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/httpapi"
	"github.com/fathorMB/GitStack/services/identity/internal/oidc"
)

func get(h http.Handler, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// Senza file OIDC il login è spento: elenco vuoto, start e callback 404.
func TestOIDCDisabled(t *testing.T) {
	for name, opts := range map[string][]httpapi.Option{
		"senza WithOIDC": nil,
		"servizio senza provider": {httpapi.WithOIDC(func() *oidc.Service {
			s, err := oidc.New(nil, oidc.Config{}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}())},
	} {
		t.Run(name, func(t *testing.T) {
			h := httpapi.New(&auth.Service{}, nil, opts...)
			rec := get(h, "/auth/oidc/providers")
			if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"items":[]}` {
				t.Errorf("providers: %d %s", rec.Code, rec.Body.String())
			}
			if rec := get(h, "/auth/oidc/keycloak/start"); rec.Code != http.StatusNotFound {
				t.Errorf("start: %d", rec.Code)
			}
			if rec := get(h, "/auth/oidc/keycloak/callback?code=c&state=s"); rec.Code != http.StatusNotFound {
				t.Errorf("callback: %d", rec.Code)
			}
		})
	}
}

// redirectTo si valida prima di contattare il provider.
func TestOIDCStartRejectsBadRedirect(t *testing.T) {
	ps, err := oidc.Parse([]byte(`[{"slug":"kc","displayName":"KC","issuer":"https://kc.example/realms/x","clientId":"c","clientSecret":"s"}]`), oidc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := oidc.New(nil, oidc.Config{
		Providers: ps, Key: []byte("0123456789abcdef0123456789abcdef"), KeyID: "k", PublicURL: "https://git.test",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := httpapi.New(&auth.Service{}, nil, httpapi.WithOIDC(svc))
	for _, bad := range []string{"https://evil.example", "//evil.example", "/%5Cevil", "x"} {
		rec := get(h, "/auth/oidc/kc/start?redirectTo="+bad)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("redirectTo %q: %d %s", bad, rec.Code, rec.Body.String())
		}
	}
	// La lista mostra il provider senza segreti.
	rec := get(h, "/auth/oidc/providers")
	if !strings.Contains(rec.Body.String(), `"slug":"kc"`) || strings.Contains(rec.Body.String(), "issuer") {
		t.Errorf("providers: %s", rec.Body.String())
	}
}
