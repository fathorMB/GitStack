package identityclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL + "/") // slash finale: deve essere tollerato
	return NewClient(u, "segreto-di-servizio", 2*time.Second)
}

func TestClient_TokenAttivo(t *testing.T) {
	var gotAuth, gotPath, gotCT string
	var in map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath, gotCT = r.Header.Get("Authorization"), r.URL.Path, r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&in)
		_, _ = w.Write([]byte(`{"active":true,"cacheTtlSeconds":30,"principal":{"userId":"11111111-1111-1111-1111-111111111111","username":"alice","kind":"human","isAdmin":true,"authMethod":"token","scopes":["read:user","write:org"],"expiresAt":"2030-01-01T00:00:00Z"}}`))
	})
	res, err := c.Verify(context.Background(), "gst_abc", KindToken)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer segreto-di-servizio" || gotPath != "/internal/verify" || gotCT != "application/json" {
		t.Errorf("richiesta: auth=%q path=%q ct=%q", gotAuth, gotPath, gotCT)
	}
	if in["credential"] != "gst_abc" || in["kind"] != "token" {
		t.Errorf("corpo = %v", in)
	}
	p := res.Principal
	if !res.Active || p.Username != "alice" || p.UserID != "11111111-1111-1111-1111-111111111111" || !p.IsAdmin ||
		!p.IsToken || len(p.Scopes) != 2 || p.Scopes[1] != "write:org" || p.MustChangePassword || p.ExpiresAt == nil || res.TTL != 30*time.Second {
		t.Errorf("risultato = %+v", res)
	}
}

func TestClient_SessioneMustChange(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"active":true,"principal":{"userId":"11111111-1111-1111-1111-111111111111","username":"admin","kind":"human","isAdmin":true,"authMethod":"password","mustChangePassword":true}}`))
	})
	res, err := c.Verify(context.Background(), "cookie", KindSession)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Active || !res.Principal.MustChangePassword || res.Principal.IsToken || len(res.Principal.Scopes) != 0 {
		t.Errorf("risultato = %+v", res)
	}
}

func TestClient_NonAttivo(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"active":false,"cacheTtlSeconds":5}`))
	})
	res, err := c.Verify(context.Background(), "x", "")
	if err != nil || res.Active || res.TTL != 5*time.Second {
		t.Fatalf("risultato = %+v %v", res, err)
	}
}

func TestClient_ErroriSonoIndisponibilita(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"500":               func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) },
		"503":               func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", http.StatusServiceUnavailable) },
		"segreto sbagliato": func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusUnauthorized) },
		"400":               func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusBadRequest) },
		"JSON rotto":        func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("{")) },
		"active senza principal": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"active":true}`))
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(t, h)
			res, err := c.Verify(context.Background(), "gst_x", KindToken)
			if !errors.Is(err, ErrUnavailable) || res.Active {
				t.Fatalf("res=%+v err=%v; atteso ErrUnavailable senza esito attivo", res, err)
			}
			if strings.Contains(err.Error(), "gst_x") || strings.Contains(err.Error(), "segreto-di-servizio") {
				t.Errorf("l'errore contiene la credenziale o il segreto: %v", err)
			}
		})
	}
}

func TestClient_Irraggiungibile(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:1")
	c := NewClient(u, "s", 500*time.Millisecond)
	if _, err := c.Verify(context.Background(), "gst_x", KindToken); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
