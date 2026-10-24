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
	"github.com/fathorMB/GitStack/services/git/internal/registry"
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
			_, _ = w.Write([]byte(`{"active":true,"principal":{"userId":"u1","username":"alice","kind":"agent","authMethod":"token","scopes":["read:resource"]}}`))
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
	if err != nil || !ok || p.Username != "alice" || p.Kind != "agent" || len(p.Scopes) != 1 {
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
		case "/repos/alice/arch":
			_, _ = w.Write([]byte(`{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","archived":true}`))
		case "/repos/alice/trunk":
			_, _ = w.Write([]byte(`{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","defaultBranch":"trunk","protectDefaultBranch":true}`))
		case "/repos/alice/mirror":
			_, _ = w.Write([]byte(`{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","mirror":{"origin":"https://github.com/o/r.git","lastSyncedAt":null}}`))
		case "/repos/alice/boom":
			w.WriteHeader(http.StatusBadGateway)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer core.Close()
	c := New("", core.URL, secret, time.Second)
	caller := trust.Identity{UserID: "u1", Username: "alice", Scopes: []string{"read:resource"}}

	ref, err := c.ResolveRepo(context.Background(), caller, "alice", "app")
	if err != nil || ref.ID != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" || ref.Archived {
		t.Fatalf("resolve: %+v %v", ref, err)
	}
	if ref, err := c.ResolveRepo(context.Background(), caller, "alice", "arch"); err != nil || !ref.Archived {
		t.Fatalf("archiviato: %+v %v", ref, err)
	}
	// R9: branch principale e protezione devono arrivare da core, altrimenti la
	// protezione si spegne senza che nessuno se ne accorga.
	if ref, err := c.ResolveRepo(context.Background(), caller, "alice", "trunk"); err != nil || ref.DefaultBranch != "trunk" || !ref.ProtectDefaultBranch {
		t.Fatalf("defaultBranch/protectDefaultBranch: %+v %v", ref, err)
	}
	// MP2: l'origine del mirror in pull arriva da core (il push è rifiutato).
	if ref, err := c.ResolveRepo(context.Background(), caller, "alice", "mirror"); err != nil || ref.MirrorOrigin != "https://github.com/o/r.git" {
		t.Fatalf("mirror: %+v %v", ref, err)
	}
	if ref, _ := c.ResolveRepo(context.Background(), caller, "alice", "app"); ref.MirrorOrigin != "" {
		t.Errorf("repo normale con origine: %+v", ref)
	}
	if ref, _ := c.ResolveRepo(context.Background(), caller, "alice", "app"); ref.ProtectDefaultBranch || ref.DefaultBranch != "" {
		t.Errorf("senza i campi la protezione non deve risultare attiva: %+v", ref)
	}
	if _, err := c.ResolveRepo(context.Background(), caller, "alice", "altro"); !errors.Is(err, access.ErrNotFound) {
		t.Errorf("404 deve essere ErrNotFound: %v", err)
	}
	if _, err := c.ResolveRepo(context.Background(), caller, "alice", "boom"); err == nil || errors.Is(err, access.ErrNotFound) {
		t.Errorf("5xx non deve essere ErrNotFound: %v", err)
	}
}

func TestLookupKey(t *testing.T) {
	const fp = "SHA256:abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO+/1234567"
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sec" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Il fingerprint con '/' e '+' deve arrivare come un solo segmento.
		if !strings.HasPrefix(r.URL.EscapedPath(), "/internal/ssh-keys/") || strings.Contains(strings.TrimPrefix(r.URL.EscapedPath(), "/internal/ssh-keys/"), "/") {
			t.Errorf("percorso non escapato: %s", r.URL.EscapedPath())
		}
		switch r.URL.Path {
		case "/internal/ssh-keys/" + fp:
			_, _ = w.Write([]byte(`{"key":{},"user":{"id":"u1","username":"alice","kind":"agent","isActive":true}}`))
		case "/internal/ssh-keys/SHA256:off":
			_, _ = w.Write([]byte(`{"user":{"id":"u2","username":"carol","isActive":false}}`))
		case "/internal/ssh-keys/SHA256:noflag":
			_, _ = w.Write([]byte(`{"user":{"id":"u3","username":"dan"}}`))
		case "/internal/ssh-keys/SHA256:boom":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer idp.Close()
	c := New(idp.URL, "", "sec", time.Second)
	ctx := context.Background()
	if u, err := c.LookupKey(ctx, fp); err != nil || u != (access.KeyOwner{UserID: "u1", Username: "alice", Kind: "agent", Active: true}) {
		t.Fatalf("chiave valida: %+v %v", u, err)
	}
	if u, err := c.LookupKey(ctx, "SHA256:off"); err != nil || u.Active {
		t.Errorf("disattivato: %+v %v", u, err)
	}
	if u, err := c.LookupKey(ctx, "SHA256:noflag"); err != nil || u.Active {
		t.Errorf("senza isActive deve essere non attivo: %+v %v", u, err)
	}
	if _, err := c.LookupKey(ctx, "SHA256:ignota"); !errors.Is(err, access.ErrUnknownKey) {
		t.Errorf("ignota: %v", err)
	}
	if _, err := c.LookupKey(ctx, "SHA256:boom"); !errors.Is(err, access.ErrUnavailable) {
		t.Errorf("500: %v", err)
	}
	if _, err := New("http://127.0.0.1:1", "", "sec", time.Second).LookupKey(ctx, fp); !errors.Is(err, access.ErrUnavailable) {
		t.Errorf("identity spenta: %v", err)
	}
}

func TestPackageSettings(t *testing.T) {
	var gotPath string
	var gotID trust.Identity
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotID, _ = trust.Verify(r.Header, "segreto", time.Now())
		if strings.HasSuffix(r.URL.Path, "/sconosciuto") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"repoId":"r1","owner":"alice","name":"app","archived":false,"protectedTagPatterns":["v*"],` +
			`"deleteUntaggedAfterDays":3,"keepLastTags":5,"deleteShaTagsAfterDays":null,"includeInBackup":true,"ownerRepoIds":["r1","r2"]}`))
	}))
	defer core.Close()
	c := New("http://unused", core.URL, "segreto", time.Second)
	st, err := c.PackageSettings(context.Background(), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/internal/registry/settings/r1" || gotID.Username == "" {
		t.Errorf("richiesta a core: %q %+v", gotPath, gotID)
	}
	if st.Owner != "alice" || st.KeepLastTags != 5 || st.DeleteShaTagsAfterDays != 0 || st.DeleteUntaggedAfterDays != 3 ||
		!st.IncludeInBackup || len(st.ProtectedTagPatterns) != 1 || len(st.OwnerRepoIDs) != 2 {
		t.Errorf("impostazioni: %+v", st)
	}
	if _, err := c.PackageSettings(context.Background(), "sconosciuto"); !errors.Is(err, registry.ErrSettingsNotFound) {
		t.Errorf("repo sconosciuto: %v", err)
	}
}
