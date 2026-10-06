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

func TestOrgOwners(t *testing.T) {
	org, a := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		wantErr error
		want    int
	}{
		{"ok", 200, `{"owners":["` + a.String() + `"]}`, nil, 1},
		{"vuoto", 200, `{"owners":[]}`, nil, 0},
		{"non_trovata", 404, ``, identityclient.ErrNotFound, 0},
		{"errore", 500, ``, identityclient.ErrUnavailable, 0},
		{"id_non_valido", 200, `{"owners":["x"]}`, identityclient.ErrUnavailable, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/internal/orgs/"+org.String()+"/owners" || r.Header.Get("Authorization") != "Bearer sec" {
					t.Errorf("richiesta inattesa: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			u, _ := url.Parse(srv.URL)
			got, err := identityclient.New(u, "sec", time.Second).OrgOwners(context.Background(), org)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, voluto %v", err, tc.wantErr)
			}
			if err == nil && len(got) != tc.want {
				t.Fatalf("owner = %v", got)
			}
		})
	}
}
