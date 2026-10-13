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
