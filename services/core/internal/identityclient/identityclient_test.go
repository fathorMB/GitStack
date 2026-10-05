package identityclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/google/uuid"
)

func TestGrantResourceCreator(t *testing.T) {
	rid, uid := uuid.New(), uuid.New()
	for _, tc := range []struct {
		status  int
		wantErr bool
	}{{200, false}, {201, false}, {404, true}, {401, true}, {500, true}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if r.Method != http.MethodPost || r.URL.Path != "/internal/resources/"+rid.String()+"/grants/creator" ||
				r.Header.Get("Authorization") != "Bearer sec" || in["userId"] != uid.String() {
				t.Errorf("richiesta inattesa: %s %s %v", r.Method, r.URL.Path, in)
			}
			w.WriteHeader(tc.status)
		}))
		u, _ := url.Parse(srv.URL + "/")
		err := identityclient.New(u, "sec", time.Second).GrantResourceCreator(context.Background(), rid, uid)
		srv.Close()
		if (err != nil) != tc.wantErr || (err != nil && !errors.Is(err, identityclient.ErrUnavailable)) {
			t.Errorf("status %d: err=%v", tc.status, err)
		}
	}
}

func TestGrantResourceCreatorNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u, _ := url.Parse(srv.URL)
	srv.Close()
	err := identityclient.New(u, "sec", time.Second).GrantResourceCreator(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, identityclient.ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
}

func TestReadableResources(t *testing.T) {
	uid, r1, r2 := uuid.New(), uuid.New(), uuid.New()
	for name, tc := range map[string]struct {
		status  int
		body    string
		wantAll bool
		wantIDs []uuid.UUID
		wantErr bool
	}{
		"ids":             {200, `{"all":false,"resourceIds":["` + r1.String() + `","` + r2.String() + `"]}`, false, []uuid.UUID{r1, r2}, false},
		"admin":           {200, `{"all":true,"resourceIds":[]}`, true, nil, false},
		"vuoto":           {200, `{"all":false,"resourceIds":[]}`, false, nil, false},
		"all mancante":    {200, `{"resourceIds":[]}`, false, nil, true},
		"id non uuid":     {200, `{"all":false,"resourceIds":["x"]}`, false, nil, true},
		"json non valido": {200, `{`, false, nil, true},
		"401":             {401, `{}`, false, nil, true},
		"500":             {500, `{}`, false, nil, true},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var in map[string]string
				_ = json.NewDecoder(r.Body).Decode(&in)
				if r.Method != http.MethodPost || r.URL.Path != "/internal/permissions/readable-resources" ||
					r.Header.Get("Authorization") != "Bearer sec" || in["userId"] != uid.String() {
					t.Errorf("richiesta inattesa: %s %s %v", r.Method, r.URL.Path, in)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			u, _ := url.Parse(srv.URL + "/")
			all, ids, err := identityclient.New(u, "sec", time.Second).ReadableResources(context.Background(), uid)
			if (err != nil) != tc.wantErr || (err != nil && !errors.Is(err, identityclient.ErrUnavailable)) {
				t.Fatalf("err=%v", err)
			}
			if err == nil {
				if all != tc.wantAll || len(ids) != len(tc.wantIDs) {
					t.Fatalf("all=%v ids=%v", all, ids)
				}
				for i := range ids {
					if ids[i] != tc.wantIDs[i] {
						t.Fatalf("ids=%v", ids)
					}
				}
			}
		})
	}
}

func TestReadableResourcesNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u, _ := url.Parse(srv.URL)
	srv.Close()
	if _, _, err := identityclient.New(u, "sec", time.Second).ReadableResources(context.Background(), uuid.New()); !errors.Is(err, identityclient.ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
}

func newIdentity(t *testing.T, h http.HandlerFunc) *identityclient.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return identityclient.New(u, "sec", time.Second)
}

func TestResolveOwner(t *testing.T) {
	id := uuid.New()
	status := 200
	c := newIdentity(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/internal/owners/acme" || r.Header.Get("Authorization") != "Bearer sec" {
			t.Errorf("richiesta inattesa: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(status)
		if status == 200 {
			_, _ = w.Write([]byte(`{"type":"organization","id":"` + id.String() + `","name":"acme"}`))
		}
	})
	o, err := c.ResolveOwner(context.Background(), "acme")
	if err != nil || o.Type != "organization" || o.ID != id || o.Name != "acme" {
		t.Fatalf("owner = %+v, %v", o, err)
	}
	status = 404
	if _, err := c.ResolveOwner(context.Background(), "acme"); !errors.Is(err, identityclient.ErrNotFound) {
		t.Fatalf("404: %v", err)
	}
	status = 500
	if _, err := c.ResolveOwner(context.Background(), "acme"); !errors.Is(err, identityclient.ErrUnavailable) {
		t.Fatalf("500: %v", err)
	}
}

func TestSetResourceAttributes(t *testing.T) {
	rid, oid := uuid.New(), uuid.New()
	status := 204
	c := newIdentity(t, func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		if r.Method != http.MethodPut || r.URL.Path != "/internal/resources/"+rid.String()+"/attributes" ||
			in["ownerType"] != "user" || in["ownerId"] != oid.String() || in["visibility"] != "private" {
			t.Errorf("richiesta inattesa: %s %s %v", r.Method, r.URL.Path, in)
		}
		w.WriteHeader(status)
	})
	if err := c.SetResourceAttributes(context.Background(), rid, "user", oid, "private"); err != nil {
		t.Fatal(err)
	}
	status = 409
	if err := c.SetResourceAttributes(context.Background(), rid, "user", oid, "private"); !errors.Is(err, identityclient.ErrOwnerConflict) {
		t.Fatalf("409: %v", err)
	}
	status = 503
	if err := c.SetResourceAttributes(context.Background(), rid, "user", oid, "private"); !errors.Is(err, identityclient.ErrUnavailable) {
		t.Fatalf("503: %v", err)
	}
}

func TestHasRole(t *testing.T) {
	uid, rid := uuid.New(), uuid.New()
	allowed := `{"allowed":true,"effectiveRole":"admin"}`
	status := 200
	c := newIdentity(t, func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		if r.URL.Path != "/internal/permissions/check" || in["userId"] != uid.String() || in["resourceId"] != rid.String() || in["role"] != "admin" {
			t.Errorf("richiesta inattesa: %s %v", r.URL.Path, in)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(allowed))
	})
	if ok, err := c.HasRole(context.Background(), uid, rid, "admin"); err != nil || !ok {
		t.Fatalf("HasRole = %v, %v", ok, err)
	}
	allowed = `{"allowed":false}`
	if ok, err := c.HasRole(context.Background(), uid, rid, "admin"); err != nil || ok {
		t.Fatalf("HasRole = %v, %v", ok, err)
	}
	status = 500
	if _, err := c.HasRole(context.Background(), uid, rid, "admin"); !errors.Is(err, identityclient.ErrUnavailable) {
		t.Fatalf("500: %v", err)
	}
}
