package identityclient_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/google/uuid"
)

func TestUserAccess(t *testing.T) {
	uid, rid := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		wantErr error
		check   func(t *testing.T, r identityclient.UserAccessResult)
	}{
		{"ok", 200, `{"admin":false,"items":[{"resourceId":"` + rid.String() + `","role":"write","sources":[{"kind":"team","role":"write","organization":"acme","team":"web"}]}]}`, nil,
			func(t *testing.T, r identityclient.UserAccessResult) {
				if r.Admin || len(r.Items) != 1 || r.Items[0].ResourceID != rid || r.Items[0].Role != "write" ||
					r.Items[0].Sources[0] != (identityclient.AccessSource{Kind: "team", Role: "write", Organization: "acme", Team: "web"}) {
					t.Errorf("risultato: %+v", r)
				}
			}},
		{"admin", 200, `{"admin":true,"items":[]}`, nil, func(t *testing.T, r identityclient.UserAccessResult) {
			if !r.Admin || len(r.Items) != 0 {
				t.Errorf("risultato: %+v", r)
			}
		}},
		{"non_trovato", 404, ``, identityclient.ErrNotFound, nil},
		{"errore", 500, ``, identityclient.ErrUnavailable, nil},
		{"senza_admin", 200, `{"items":[]}`, identityclient.ErrUnavailable, nil},
		{"id_non_valido", 200, `{"admin":false,"items":[{"resourceId":"x","role":"read","sources":[]}]}`, identityclient.ErrUnavailable, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/internal/permissions/user-access" || r.Header.Get("Authorization") != "Bearer sec" {
					t.Errorf("richiesta inattesa: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			u, _ := url.Parse(srv.URL)
			res, err := identityclient.New(u, "sec", time.Second).UserAccess(context.Background(), uid)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, voluto %v", err, tc.wantErr)
			}
			if tc.check != nil {
				tc.check(t, res)
			}
		})
	}
}
