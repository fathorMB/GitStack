package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/trust"
)

func TestCallerAllowed(t *testing.T) {
	cases := []struct {
		caller, method, path string
		want                 bool
	}{
		{trust.CallerGit, http.MethodGet, "/repos/alice/demo", true},
		{trust.CallerGit, http.MethodGet, "/repos/alice/demo/", true},
		{trust.CallerGit, http.MethodPost, "/repos/alice/demo", false},
		{trust.CallerGit, http.MethodGet, "/repos/alice", false},
		{trust.CallerGit, http.MethodGet, "/repos/alice/demo/issues", false},
		{trust.CallerGit, http.MethodGet, "/resources", false},
		{trust.CallerGit, http.MethodGet, "/internal/registry/settings/0b1f6c1e-8a4c-4a35-9c1a-0f7a2f6f2d11", true},
		{trust.CallerGit, http.MethodGet, "/internal/registry/settings/", false},
		{trust.CallerGit, http.MethodGet, "/internal/registry/settings/a/b", false},
		{trust.CallerGit, http.MethodPost, "/internal/registry/settings/0b1f6c1e-8a4c-4a35-9c1a-0f7a2f6f2d11", false},
		{trust.CallerGit, http.MethodGet, "/repos//demo", false},
		{trust.CallerGateway, http.MethodPost, "/resources", true},
		{"", http.MethodDelete, "/repos/alice/demo", true},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		if got := callerAllowed(c.caller, req); got != c.want {
			t.Errorf("callerAllowed(%q, %s %s) = %v, voluto %v", c.caller, c.method, c.path, got, c.want)
		}
	}
}
