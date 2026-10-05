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

func TestLookupUsers(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		wantErr error
		wantLen int
	}{
		{"ok", 200, `{"users":[{"id":"` + a.String() + `","username":"alice","kind":"human"},{"id":"` + b.String() + `","username":"botty","kind":"agent"}]}`, nil, 2},
		{"id_senza_utente", 200, `{"users":[{"id":"` + a.String() + `","username":"alice","kind":"human"}]}`, nil, 1},
		{"errore", 500, ``, identityclient.ErrUnavailable, 0},
		{"tipo_non_valido", 200, `{"users":[{"id":"` + a.String() + `","username":"alice","kind":"robot"}]}`, identityclient.ErrUnavailable, 0},
		{"id_non_valido", 200, `{"users":[{"id":"x","username":"alice","kind":"human"}]}`, identityclient.ErrUnavailable, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/internal/users/lookup-ids" || r.Header.Get("Authorization") != "Bearer sec" {
					t.Errorf("richiesta inattesa: %s %s", r.Method, r.URL.Path)
				}
				var in struct {
					IDs []string `json:"ids"`
				}
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.IDs) != 2 {
					t.Errorf("corpo = %+v (%v)", in, err)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			u, _ := url.Parse(srv.URL)
			got, err := identityclient.New(u, "sec", time.Second).LookupUsers(context.Background(), []uuid.UUID{a, b})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, voluto %v", err, tc.wantErr)
			}
			if err == nil && len(got) != tc.wantLen {
				t.Fatalf("utenti = %+v", got)
			}
			if err == nil && got[a].Username != "alice" {
				t.Fatalf("alice = %+v", got[a])
			}
		})
	}
}
