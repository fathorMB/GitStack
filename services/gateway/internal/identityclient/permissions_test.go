package identityclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

const (
	testUser = "11111111-1111-1111-1111-111111111111"
	testRes  = "00000000-0000-0000-0000-000000000001"
)

func TestCheckPermission_Richiesta(t *testing.T) {
	var gotAuth, gotPath string
	var in map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&in)
		_, _ = w.Write([]byte(`{"allowed":true,"effectiveRole":"admin"}`))
	})
	ok, err := c.CheckPermission(context.Background(), testUser, testRes, "write")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if gotAuth != "Bearer segreto-di-servizio" || gotPath != "/internal/permissions/check" {
		t.Errorf("auth=%q path=%q", gotAuth, gotPath)
	}
	if in["userId"] != testUser || in["resourceId"] != testRes || in["role"] != "write" {
		t.Errorf("corpo = %v", in)
	}
}

func TestCheckPermission_Esiti(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		want    bool
		wantErr bool
	}{
		{"negato", 200, `{"allowed":false}`, false, false},
		{"concesso", 200, `{"allowed":true}`, true, false},
		{"5xx", 500, `{}`, false, true},
		{"segreto sbagliato", 401, `{}`, false, true},
		{"404", 404, `{}`, false, true},
		{"corpo non valido", 200, `non json`, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			ok, err := c.CheckPermission(context.Background(), testUser, testRes, "read")
			if ok != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
			if tc.wantErr && !errors.Is(err, ErrUnavailable) {
				t.Errorf("errore %v non è ErrUnavailable", err)
			}
		})
	}
}

func TestCheckPermission_ArgomentiNonValidi(t *testing.T) {
	c := newTestClient(t, func(http.ResponseWriter, *http.Request) { t.Error("identity interpellata") })
	for _, a := range [][3]string{{"x", testRes, "read"}, {testUser, "x", "read"}, {testUser, testRes, "owner"}} {
		if ok, err := c.CheckPermission(context.Background(), a[0], a[1], a[2]); ok || err == nil {
			t.Errorf("%v: ok=%v err=%v", a, ok, err)
		}
	}
}
