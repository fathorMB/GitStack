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
