package identityclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestLookupProfile(t *testing.T) {
	id := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/users/profile" || r.Header.Get("Authorization") != "Bearer seg" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Query().Get("case") {
		case "boom":
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"id":"` + id + `","username":"alice","kind":"human","active":true,"email":"a@example.com","teams":["acme/web"]}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	c := NewClient(u, "seg", time.Second)
	p, err := c.LookupProfile(context.Background(), id)
	if err != nil || p.Username != "alice" || p.Email != "a@example.com" || len(p.Teams) != 1 || p.Teams[0] != "acme/web" || !p.Active {
		t.Fatalf("profilo: %+v %v", p, err)
	}
	if _, err := c.LookupProfile(context.Background(), "non-uuid"); err == nil {
		t.Fatal("id non valido")
	}
	bad := NewClient(u, "sbagliato", time.Second)
	if _, err := bad.LookupProfile(context.Background(), id); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("segreto sbagliato: mai fail open, %v", err)
	}
}
