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
