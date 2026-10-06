package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

func TestBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"git.acme.test":         "https://git.acme.test/api/v1",
		"git.acme.test:8443/":   "https://git.acme.test:8443/api/v1",
		"http://127.0.0.1:8080": "http://127.0.0.1:8080/api/v1",
		" https://h.test/ ":     "https://h.test/api/v1",
	} {
		if got := BaseURL(in); got != want {
			t.Errorf("BaseURL(%q) = %q, atteso %q", in, got, want)
		}
	}
	if UserAgent("1.2.3") != "gs/1.2.3" || UserAgent("") != "gs/dev" {
		t.Error("UserAgent")
	}
}

func TestGeneratoEDoHannoBaseUserAgentEBearer(t *testing.T) {
	var seen []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"authMethod":"token","mustChangePassword":false,"user":{"username":"alice","displayName":"Alice","kind":"user"}}`)
	}))
	defer srv.Close()
	c, err := New(Options{Host: srv.URL, Token: "gst_abc", Version: "9.9.9", HTTP: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Generated().GetCurrentSessionWithResponse(context.Background())
	if err != nil || resp.JSON200 == nil || resp.JSON200.User.Username != "alice" {
		t.Fatalf("generato: %+v %v", resp, err)
	}
	raw, err := c.Do(context.Background(), "GET", "auth/session", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = raw.Body.Close()
	if len(seen) != 2 {
		t.Fatalf("richieste: %d", len(seen))
	}
	for i, r := range seen {
		if r.URL.Path != "/api/v1/auth/session" {
			t.Errorf("%d: percorso %q", i, r.URL.Path)
		}
		if r.Header.Get("User-Agent") != "gs/9.9.9" {
			t.Errorf("%d: User-Agent %q", i, r.Header.Get("User-Agent"))
		}
		if r.Header.Get("Authorization") != "Bearer gst_abc" {
			t.Errorf("%d: Authorization %q", i, r.Header.Get("Authorization"))
		}
	}
}

func TestSenzaTokenNienteAuthorization(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Header["Authorization"]; ok {
			t.Error("Authorization presente senza token")
		}
	}))
	defer srv.Close()
	c, _ := New(Options{Host: srv.URL, HTTP: srv.Client()})
	resp, err := c.Do(context.Background(), "GET", "/x", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

func TestDoCorpoEContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || string(b) != `{"a":1}` || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s %q %q", r.Method, b, r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	c, _ := New(Options{Host: srv.URL, HTTP: srv.Client()})
	resp, err := c.Do(context.Background(), "POST", "/x", strings.NewReader(`{"a":1}`), nil)
	if err != nil || resp.StatusCode != 201 {
		t.Fatalf("%v %v", resp, err)
	}
	_ = resp.Body.Close()
}

func TestRisposteNon2xxDiventanoErrori(t *testing.T) {
	cases := []struct {
		status   int
		body     string
		code     string
		wantExit int
	}{
		{401, `{"error":{"code":"unauthenticated","message":"token non valido"}}`, "unauthenticated", 4},
		{403, `{"error":{"code":"forbidden","message":"vietato"}}`, "forbidden", 5},
		{403, `{"error":{"code":"insufficient_scope","message":"serve repo:write"}}`, "insufficient_scope", 5},
		{404, `{"error":{"code":"not_found","message":"non trovato"}}`, "not_found", 6},
		{404, `<html>proxy</html>`, "", 6},
		{422, `{"error":{"code":"validation_failed","message":"nome non valido","details":{"fields":{}}}}`, "validation_failed", 1},
		{500, ``, "", 1},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		}))
		cl, _ := New(Options{Host: srv.URL, HTTP: srv.Client()})
		_, err := cl.Do(context.Background(), "GET", "/x", nil, nil)
		srv.Close()
		var ae *cmdutil.APIError
		if !errors.As(err, &ae) || ae.Status != c.status || ae.Code != c.code {
			t.Errorf("%d: %+v", c.status, err)
			continue
		}
		if ae.Message == "" {
			t.Errorf("%d: messaggio vuoto", c.status)
		}
		if got := cmdutil.ExitCode(err); got != c.wantExit {
			t.Errorf("%d %s: exit %d, atteso %d", c.status, c.code, got, c.wantExit)
		}
	}
}

func TestCheckStatus(t *testing.T) {
	if CheckStatus(204, nil) != nil || CheckStatus(200, nil) != nil {
		t.Error("2xx deve essere nil")
	}
	if cmdutil.ExitCode(CheckStatus(404, []byte(`{"error":{"code":"not_found","message":"x"}}`))) != 6 {
		t.Error("404")
	}
}

func TestNewSenzaHost(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Error("atteso errore")
	}
}
