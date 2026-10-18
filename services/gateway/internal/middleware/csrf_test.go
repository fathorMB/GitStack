package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/services/gateway/internal/identityclient"
	"github.com/fathorMB/GitStack/services/gateway/internal/security"
)

type activeVerifier struct{}

func (activeVerifier) Verify(_ context.Context, _ string, kind identityclient.Kind) (identityclient.Result, error) {
	return identityclient.Result{Active: true, Principal: identityclient.Caller{
		UserID: "u1", Username: "alice", IsToken: kind == identityclient.KindToken,
		Scopes: []string{"read:user", "write:user", "read:resource", "write:resource"},
	}}, nil
}

// F16 (V3.4.4, V4.2.2, V13.2.3): un metodo non sicuro autenticato dal cookie
// con Origin o Sec-Fetch-Site estranei è rifiutato con 403 prima di
// raggiungere il servizio; il token Bearer, le richieste senza header di
// browser e le stesse origini passano come prima.
func TestCookieAuthRejectsForeignOrigin(t *testing.T) {
	reached := 0
	table, err := security.NewTable(security.Routes)
	if err != nil {
		t.Fatal(err)
	}
	h := Auth(AuthConfig{Table: table, Verifier: activeVerifier{}, Prefix: "/v1"})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reached++
			w.WriteHeader(http.StatusNoContent)
		}))

	do := func(method string, cookie bool, hdr map[string]string) int {
		path := "/v1/user/tokens"
		if method == "DELETE" {
			path += "/00000000-0000-0000-0000-000000000001"
		}
		req := httptest.NewRequest(method, "http://git.example.test"+path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		if cookie {
			req.AddCookie(&http.Cookie{Name: "__Host-gst_session", Value: "v"})
		} else {
			req.Header.Set("Authorization", "Bearer gst_x")
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	cases := []struct {
		name   string
		method string
		cookie bool
		hdr    map[string]string
		want   int
	}{
		{"Origin estraneo", "POST", true, map[string]string{"Origin": "https://evil.example"}, 403},
		{"Origin null", "POST", true, map[string]string{"Origin": "null"}, 403},
		{"sottodominio dello stesso sito", "POST", true, map[string]string{"Origin": "https://app.git.example.test"}, 403},
		{"Sec-Fetch-Site cross-site", "POST", true, map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"Sec-Fetch-Site same-site", "DELETE", true, map[string]string{"Sec-Fetch-Site": "same-site"}, 403},
		{"stessa origine", "POST", true, map[string]string{"Origin": "http://git.example.test", "Sec-Fetch-Site": "same-origin"}, 204},
		{"stessa origine dietro proxy", "POST", true, map[string]string{"Origin": "https://git.example.test"}, 204},
		{"Host senza porta (nginx del chart)", "POST", true, map[string]string{"Origin": "http://git.example.test:8080"}, 204},
		{"altro host con porta", "POST", true, map[string]string{"Origin": "http://git.example.test.evil.example:8080"}, 403},
		{"Origin uguale a X-Forwarded-Host", "POST", true, map[string]string{"Origin": "https://git.public.test", "X-Forwarded-Host": "git.public.test"}, 204},
		{"azione diretta dell'utente (none)", "POST", true, map[string]string{"Sec-Fetch-Site": "none"}, 204},
		{"client non browser, senza header", "POST", true, nil, 204},
		{"GET con origine estranea resta lecito", "GET", true, map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}, 204},
		{"token Bearer con origine estranea", "POST", false, map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}, 204},
	}
	for _, c := range cases {
		before := reached
		if got := do(c.method, c.cookie, c.hdr); got != c.want {
			t.Errorf("%s: status %d, voluto %d", c.name, got, c.want)
		}
		if c.want == 403 && reached != before {
			t.Errorf("%s: la richiesta è arrivata al servizio", c.name)
		}
	}
}

// Il cookie con prefisso __Host- e quello storico sono entrambi credenziali
// di sessione: le sessioni aperte prima del cambio restano valide.
func TestSessionCookieBothNames(t *testing.T) {
	for _, name := range []string{"__Host-gst_session", "gst_session"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/auth/session", nil)
		req.AddCookie(&http.Cookie{Name: name, Value: "abc"})
		cred, kind := credentialFrom(req)
		if cred != "abc" || kind != identityclient.KindSession {
			t.Errorf("%s: cred=%q kind=%q", name, cred, kind)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/session", nil)
	req.AddCookie(&http.Cookie{Name: "gst_session", Value: "old"})
	req.AddCookie(&http.Cookie{Name: "__Host-gst_session", Value: "new"})
	if cred, _ := credentialFrom(req); cred != "new" {
		t.Errorf("con entrambi vale il cookie __Host-: %q", cred)
	}
}
