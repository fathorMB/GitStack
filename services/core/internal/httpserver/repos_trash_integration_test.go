//go:build integration

package httpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

func decodeDeleted(t *testing.T, body []byte) []openapi.DeletedRepository {
	t.Helper()
	var l openapi.DeletedRepositoryList
	if err := json.Unmarshal(body, &l); err != nil {
		t.Fatalf("risposta non valida: %v: %s", err, body)
	}
	return l.Items
}

// ageDeleted retrodata l'eliminazione di un repo (il tempo di core è quello
// del database per le righe, e le richieste usano l'orologio reale).
func (e *reposEnv) ageDeleted(id uuid.UUID, by time.Duration) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE core.repositories SET deleted_at = deleted_at - make_interval(secs => $2) WHERE resource_id = $1`,
		id, by.Seconds()); err != nil {
		e.t.Fatal(err)
	}
}

func TestRepos_EliminazioneERipristino(t *testing.T) {
	e := newReposEnv(t, httpserver.CloneConfig{PublicURL: "https://git.example.com", SSHPort: 2222})
	r := e.create("alice", `{"owner":"alice","name":"gone","visibility":"internal","description":"d","readme":true}`)
	id := uuid.UUID(*r.Id)
	before := decodeRepo(t, e.do(http.MethodGet, "/repos/alice/gone", "alice", ""))

	t.Run("senza_admin_403_o_404", func(t *testing.T) {
		// bob legge (interno) ma non è admin: 403. carol non c'è nemmeno
		// come lettore di un repo privato: 404.
		e.want(e.do(http.MethodDelete, "/repos/alice/gone", "bob", ""), http.StatusForbidden)
		e.create("alice", `{"owner":"alice","name":"secret"}`)
		e.want(e.do(http.MethodDelete, "/repos/alice/secret", "carol", ""), http.StatusNotFound)
		e.want(e.do(http.MethodGet, "/repos/alice/gone", "alice", ""), http.StatusOK)
		e.want(e.do(http.MethodGet, "/repos/alice/secret", "alice", ""), http.StatusOK)
	})

	t.Run("elimina_sparisce_subito", func(t *testing.T) {
		e.want(e.do(http.MethodDelete, "/repos/alice/gone", "alice", ""), http.StatusNoContent)
		e.want(e.do(http.MethodGet, "/repos/alice/gone", "alice", ""), http.StatusNotFound)
		e.want(e.do(http.MethodGet, "/repos/alice/gone", "bob", ""), http.StatusNotFound)
		e.want(e.do(http.MethodDelete, "/repos/alice/gone", "alice", ""), http.StatusNotFound)
		rec := e.do(http.MethodGet, "/repos", "alice", "")
		e.want(rec, http.StatusOK)
		var l openapi.RepositoryList
		if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		for _, it := range l.Items {
			if it.Name == "gone" {
				t.Fatal("il repo eliminato compare ancora nell'elenco")
			}
		}
		// Il servizio git lo vede nel cestino.
		if st, _ := e.git.Get(context.Background(), trust.Identity{}, id); !st.Trashed {
			t.Fatal("git non ha cestinato il repo")
		}
	})

	t.Run("nome_occupato", func(t *testing.T) {
		e.want(e.do(http.MethodPost, "/repos", "alice", `{"owner":"alice","name":"gone"}`), http.StatusConflict)
	})

	t.Run("elenco_eliminati_con_giorni_rimasti", func(t *testing.T) {
		rec := e.do(http.MethodGet, "/repos/deleted", "alice", "")
		e.want(rec, http.StatusOK)
		items := decodeDeleted(t, rec.Body.Bytes())
		if len(items) != 1 || uuid.UUID(items[0].Id) != id || items[0].Name != "gone" || items[0].Owner.Name != "alice" {
			t.Fatalf("elenco = %+v", items)
		}
		if d := items[0].PurgeAt.Sub(items[0].DeletedAt); d != 7*24*time.Hour {
			t.Fatalf("purgeAt - deletedAt = %v, voluto 7 giorni", d)
		}
		// Il filtro per owner, e chi non è admin non vede niente.
		e.want(e.do(http.MethodGet, "/repos/deleted?owner=alice", "alice", ""), http.StatusOK)
		if n := len(decodeDeleted(t, e.do(http.MethodGet, "/repos/deleted?owner=bob", "alice", "").Body.Bytes())); n != 0 {
			t.Fatalf("owner=bob: %d voci", n)
		}
		if n := len(decodeDeleted(t, e.do(http.MethodGet, "/repos/deleted", "bob", "").Body.Bytes())); n != 0 {
			t.Fatalf("bob non è admin ma vede %d voci", n)
		}
	})

	t.Run("ripristino_senza_admin", func(t *testing.T) {
		e.want(e.do(http.MethodPost, "/repos/deleted/"+id.String()+"/restore", "bob", ""), http.StatusForbidden)
		e.want(e.do(http.MethodPost, "/repos/deleted/"+uuid.NewString()+"/restore", "alice", ""), http.StatusNotFound)
	})

	t.Run("ripristino_torna_com_era", func(t *testing.T) {
		rec := e.do(http.MethodPost, "/repos/deleted/"+id.String()+"/restore", "alice", "")
		e.want(rec, http.StatusOK)
		after := decodeRepo(t, e.do(http.MethodGet, "/repos/alice/gone", "alice", ""))
		after.UpdatedAt, before.UpdatedAt = nil, nil
		a, _ := json.Marshal(after)
		b, _ := json.Marshal(before)
		if string(a) != string(b) {
			t.Fatalf("dopo il ripristino:\n%s\nprima:\n%s", a, b)
		}
		e.want(e.do(http.MethodGet, "/repos/alice/gone", "bob", ""), http.StatusOK)
		if st, _ := e.git.Get(context.Background(), trust.Identity{}, id); st.Trashed {
			t.Fatal("git ha ancora il repo nel cestino")
		}
		if n := len(decodeDeleted(t, e.do(http.MethodGet, "/repos/deleted", "alice", "").Body.Bytes())); n != 0 {
			t.Fatalf("elenco eliminati: %d voci dopo il ripristino", n)
		}
		// Non è più eliminato: un secondo ripristino è 404.
		e.want(e.do(http.MethodPost, "/repos/deleted/"+id.String()+"/restore", "alice", ""), http.StatusNotFound)
	})

	t.Run("scaduto_non_si_ripristina", func(t *testing.T) {
		e.want(e.do(http.MethodDelete, "/repos/alice/gone", "alice", ""), http.StatusNoContent)
		e.ageDeleted(id, 7*24*time.Hour+time.Minute)
		e.want(e.do(http.MethodPost, "/repos/deleted/"+id.String()+"/restore", "alice", ""), http.StatusNotFound)
		if n := len(decodeDeleted(t, e.do(http.MethodGet, "/repos/deleted", "alice", "").Body.Bytes())); n != 0 {
			t.Fatalf("scaduto ma elencato: %d voci", n)
		}
	})

	t.Run("amministratore_di_sistema", func(t *testing.T) {
		e.id.mu.Lock()
		e.id.sysAdmin[carolID] = true
		e.id.mu.Unlock()
		r2 := e.create("bob", `{"owner":"bob","name":"b1"}`)
		e.want(e.do(http.MethodDelete, "/repos/bob/b1", "carol", ""), http.StatusNoContent)
		e.want(e.do(http.MethodPost, "/repos/deleted/"+uuid.UUID(*r2.Id).String()+"/restore", "carol", ""), http.StatusOK)
	})
}
