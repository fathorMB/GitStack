package appauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/gateway/internal/identityclient"
	"github.com/fathorMB/GitStack/services/gateway/internal/trust"
)

type fakeMembership struct {
	member bool
	err    error
	calls  int
}

func (f *fakeMembership) IsMember(context.Context, string, string) (bool, error) {
	f.calls++
	return f.member, f.err
}

func baseAccess(ok bool, err error) AccessPolicy {
	return func(context.Context, string, App) (bool, error) { return ok, err }
}

// GIT-376 (H6): read sul repo OPPURE membro della lista; un «non so» non apre mai.
func TestWithAccessList_Combinazioni(t *testing.T) {
	boom := errors.New("identity giù")
	cases := []struct {
		name    string
		base    AccessPolicy
		m       *fakeMembership
		want    bool
		wantErr bool
		listed  bool // la lista viene interrogata
	}{
		{"read sul repo: passa senza chiedere la lista", baseAccess(true, nil), &fakeMembership{}, true, false, false},
		{"senza read ma in lista: passa", baseAccess(false, nil), &fakeMembership{member: true}, true, false, true},
		{"senza read e fuori lista: rifiutato", baseAccess(false, nil), &fakeMembership{}, false, false, true},
		{"identity non risponde ma in lista: passa", baseAccess(false, boom), &fakeMembership{member: true}, true, false, true},
		{"identity non risponde e fuori lista: errore, non apre", baseAccess(false, boom), &fakeMembership{}, false, true, true},
		{"senza read e la lista non risponde: errore, non apre", baseAccess(false, nil), &fakeMembership{err: boom}, false, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := WithAccessList(c.base, c.m)(context.Background(), aliceID, App{ID: appID, RepoID: repoID})
			if got != c.want || (err != nil) != c.wantErr {
				t.Fatalf("got %v, %v", got, err)
			}
			if (c.m.calls > 0) != c.listed {
				t.Fatalf("la lista è stata interrogata %d volte", c.m.calls)
			}
		})
	}
}

// Il client firma l'identità dell'utente col segreto di servizio e chiede a core la lista dell'app.
func TestCoreMembership_FirmaERisposte(t *testing.T) {
	const secret = "segreto-di-servizio"
	now := time.Unix(1700000000, 0)
	status, body := http.StatusOK, `{"member":true}`
	var gotPath, gotUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := http.Header{}
		trust.Sign(want, secret, trust.Identity{UserID: r.Header.Get(trust.HeaderUserID), Username: "appauth"}, now)
		if want.Get(trust.HeaderSignature) != r.Header.Get(trust.HeaderSignature) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		gotPath, gotUser = r.URL.Path, r.Header.Get(trust.HeaderUserID)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	base, _ := url.Parse(srv.URL + "/")
	c := NewCoreMembership(base, secret, time.Second, func() time.Time { return now })

	if ok, err := c.IsMember(context.Background(), bobID, appID); err != nil || !ok {
		t.Fatalf("membro: %v %v", ok, err)
	}
	if gotPath != "/apps/"+appID+"/access-membership" || gotUser != bobID {
		t.Fatalf("richiesta: %s %s", gotPath, gotUser)
	}
	body = `{"member":false}`
	if ok, err := c.IsMember(context.Background(), bobID, appID); err != nil || ok {
		t.Fatalf("non membro: %v %v", ok, err)
	}
	for _, tc := range []struct {
		st   int
		body string
	}{{http.StatusServiceUnavailable, `{}`}, {http.StatusNotFound, `{}`}, {http.StatusOK, `{}`}, {http.StatusOK, `non json`}} {
		status, body = tc.st, tc.body
		if ok, err := c.IsMember(context.Background(), bobID, appID); err == nil || ok || !errors.Is(err, identityclient.ErrUnavailable) {
			t.Fatalf("%d %q: deve essere un errore, non un sì: %v %v", tc.st, tc.body, ok, err)
		}
	}
	// Segreto sbagliato: core risponde 401, quindi errore.
	bad := NewCoreMembership(base, "altro-segreto", time.Second, func() time.Time { return now })
	if ok, err := bad.IsMember(context.Background(), bobID, appID); err == nil || ok {
		t.Fatalf("segreto sbagliato: %v %v", ok, err)
	}
	if _, err := c.IsMember(context.Background(), "non-uuid", appID); err == nil {
		t.Fatal("un id non UUID non deve partire")
	}
}
