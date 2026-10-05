package upstream

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/access"
	"github.com/fathorMB/GitStack/services/git/internal/trust"
)

func TestVerifyToken(t *testing.T) {
	var gotAuth, gotBody string
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b := make([]byte, 512)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		switch {
		case strings.Contains(gotBody, "gst_ok"):
			_, _ = w.Write([]byte(`{"active":true,"principal":{"userId":"u1","username":"alice","authMethod":"token","scopes":["read:resource"]}}`))
		case strings.Contains(gotBody, "gst_sess"):
			_, _ = w.Write([]byte(`{"active":true,"principal":{"userId":"u1","username":"alice","authMethod":"password"}}`))
		case strings.Contains(gotBody, "gst_boom"):
			w.WriteHeader(http.StatusInternalServerError)
		default:
			_, _ = w.Write([]byte(`{"active":false}`))
		}
	}))
	defer idp.Close()
	c := New(idp.URL, "http://unused", "segreto", time.Second)

	p, ok, err := c.VerifyToken(context.Background(), "gst_ok")
	if err != nil || !ok || p.Username != "alice" || len(p.Scopes) != 1 {
		t.Fatalf("token valido: %+v %v %v", p, ok, err)
	}
	if gotAuth != "Bearer segreto" || !strings.Contains(gotBody, `"kind":"token"`) {
		t.Errorf("richiesta a identity: %q %q", gotAuth, gotBody)
	}
	if _, ok, err := c.VerifyToken(context.Background(), "gst_revocato"); ok || err != nil {
		t.Errorf("token inattivo: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := c.VerifyToken(context.Background(), "gst_sess"); ok {
		t.Error("una credenziale non-token non deve aprire git")
	}
	if _, ok, err := c.VerifyToken(context.Background(), "gst_boom"); ok || err == nil {
		t.Errorf("errore di identity: ok=%v err=%v (mai fail open)", ok, err)
	}
}

func TestHasRole(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/permissions/check":
			_, _ = w.Write([]byte(`{"allowed":true}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer idp.Close()
	c := New(idp.URL, "", "s", time.Second)
	if ok, err := c.HasRole(context.Background(), "u", "r", "write"); !ok || err != nil {
		t.Fatalf("allowed: %v %v", ok, err)
	}
	c = New(idp.URL+"/x", "", "s", time.Second)
	if ok, err := c.HasRole(context.Background(), "u", "r", "write"); ok || err == nil {
		t.Fatalf("errore upstream: %v %v", ok, err)
	}
}

func TestResolveRepo(t *testing.T) {
	const secret = "s3"
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := trust.Verify(r.Header, secret, time.Now())
		if !ok || id.UserID != "u1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/repos/alice/app":
			_, _ = w.Write([]byte(`{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}`))
		case "/repos/alice/boom":
			w.WriteHeader(http.StatusBadGateway)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer core.Close()
	c := New("", core.URL, secret, time.Second)
	caller := trust.Identity{UserID: "u1", Username: "alice", Scopes: []string{"read:resource"}}

	id, err := c.ResolveRepo(context.Background(), caller, "alice", "app")
	if err != nil || id != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("resolve: %q %v", id, err)
	}
	if _, err := c.ResolveRepo(context.Background(), caller, "alice", "altro"); !errors.Is(err, access.ErrNotFound) {
		t.Errorf("404 deve essere ErrNotFound: %v", err)
	}
	if _, err := c.ResolveRepo(context.Background(), caller, "alice", "boom"); err == nil || errors.Is(err, access.ErrNotFound) {
		t.Errorf("5xx non deve essere ErrNotFound: %v", err)
	}
}
