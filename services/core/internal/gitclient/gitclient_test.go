package gitclient

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

const secret = "segreto-di-servizio"

var caller = trust.Identity{UserID: "11111111-1111-1111-1111-111111111111", Username: "alice", Scopes: []string{"write:resource"}}

func newClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return New(u, secret, 5*time.Second)
}

// Ogni chiamata porta gli header X-Gitstack-* firmati col segreto di servizio.
func TestClient_HeaderFirmati(t *testing.T) {
	id := uuid.New()
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		got, ok := trust.Verify(r.Header, secret, time.Now())
		if !ok || got.UserID != caller.UserID || got.Username != "alice" {
			http.Error(w, "firma", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/internal/git/repos/"+id.String() {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"repoId":"` + id.String() + `","trashed":false,"empty":false,"branches":["develop","main"]}`))
	})
	st, err := c.Get(t.Context(), caller, id)
	if err != nil {
		t.Fatal(err)
	}
	if st.Empty || len(st.Branches) != 2 || st.Branches[1] != "main" {
		t.Fatalf("stato = %+v", st)
	}
}

func TestClient_CreaEMappaGliErrori(t *testing.T) {
	status := http.StatusCreated
	var body CreateInput
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(status)
		if status == http.StatusCreated {
			_, _ = w.Write([]byte(`{"repoId":"x","empty":true}`))
		}
	})
	in := CreateInput{RepoID: uuid.New(), Name: "app", DefaultBranch: "main", Readme: true, Author: Author{Name: "alice", Email: "a@x"}}
	empty, err := c.Create(t.Context(), caller, in)
	if err != nil || !empty {
		t.Fatalf("Create = %v, %v", empty, err)
	}
	if body.RepoID != in.RepoID || !body.Readme || body.Author.Name != "alice" {
		t.Fatalf("corpo inviato: %+v", body)
	}
	for st, want := range map[int]error{
		http.StatusConflict: ErrConflict, http.StatusBadRequest: ErrInvalid, http.StatusInternalServerError: ErrUnavailable,
		http.StatusUnauthorized: ErrUnavailable, http.StatusNotFound: ErrNotFound,
	} {
		status = st
		if _, err := c.Create(t.Context(), caller, in); !errors.Is(err, want) {
			t.Fatalf("stato %d: errore %v, voluto %v", st, err, want)
		}
	}
}

func TestClient_NonRaggiungibile(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u, _ := url.Parse(srv.URL)
	srv.Close()
	c := New(u, secret, time.Second)
	if err := c.Trash(t.Context(), caller, uuid.New()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("errore %v, voluto ErrUnavailable", err)
	}
}
