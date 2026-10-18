//go:build integration

package httpserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/google/uuid"
)

// nameConflict è il corpo del 409 di creazione e import: code, messaggio e
// details (solo con name_in_trash).
type nameConflict struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func decodeNameConflict(t *testing.T, rec *httptest.ResponseRecorder) nameConflict {
	t.Helper()
	if rec.Code != http.StatusConflict {
		t.Fatalf("risposta %d %s, voluto 409", rec.Code, rec.Body.String())
	}
	var c nameConflict
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatalf("risposta non valida: %v: %s", err, rec.Body.String())
	}
	return c
}

// GET /repos/deleted senza owner elenca anche i repo eliminati delle
// organizzazioni su cui il chiamante è admin (R12), e la creazione con quel
// nome risponde name_in_trash (R2) finché il repo non viene ripristinato.
func TestRepos_CestinoDiOrganizzazione(t *testing.T) {
	e := newReposEnv(t, httpserver.CloneConfig{PublicURL: "https://git.example.com", SSHPort: 2222})
	mine := e.create("alice", `{"owner":"alice","name":"mio"}`)
	org := e.create("alice", `{"owner":"acme","name":"site"}`)
	orgID := uuid.UUID(*org.Id)
	e.want(e.do(http.MethodDelete, "/repos/alice/mio", "alice", ""), http.StatusNoContent)
	e.want(e.do(http.MethodDelete, "/repos/acme/site", "alice", ""), http.StatusNoContent)
	_ = mine

	t.Run("elenco_senza_owner_include_le_organizzazioni", func(t *testing.T) {
		items := decodeDeleted(t, e.do(http.MethodGet, "/repos/deleted", "alice", "").Body.Bytes())
		seen := map[string]string{}
		for _, it := range items {
			seen[it.Owner.Name+"/"+it.Name] = string(it.Owner.Type)
			if d := it.PurgeAt.Sub(it.DeletedAt); d != 7*24*time.Hour {
				t.Fatalf("purgeAt - deletedAt = %v", d)
			}
		}
		if len(items) != 2 || seen["alice/mio"] != "user" || seen["acme/site"] != "organization" {
			t.Fatalf("elenco = %v", seen)
		}
		// Con owner=acme solo quello dell'organizzazione; bob non è admin.
		if got := decodeDeleted(t, e.do(http.MethodGet, "/repos/deleted?owner=acme", "alice", "").Body.Bytes()); len(got) != 1 || uuid.UUID(got[0].Id) != orgID {
			t.Fatalf("owner=acme: %+v", got)
		}
		if n := len(decodeDeleted(t, e.do(http.MethodGet, "/repos/deleted", "bob", "").Body.Bytes())); n != 0 {
			t.Fatalf("bob non è admin ma vede %d voci", n)
		}
	})

	t.Run("creazione_con_nome_nel_cestino", func(t *testing.T) {
		c := decodeNameConflict(t, e.do(http.MethodPost, "/repos", "alice", `{"owner":"acme","name":"site"}`))
		if c.Error.Code != "name_in_trash" {
			t.Fatalf("code = %q, voluto name_in_trash", c.Error.Code)
		}
		purge, err := time.Parse(time.RFC3339, fmt.Sprint(c.Error.Details["purgeAt"]))
		if err != nil || time.Until(purge) < 6*24*time.Hour || time.Until(purge) > 7*24*time.Hour+time.Minute {
			t.Fatalf("purgeAt = %v (%v)", c.Error.Details["purgeAt"], err)
		}
		if c.Error.Details["repoId"] != orgID.String() || c.Error.Details["owner"] != "acme" {
			t.Fatalf("details = %v", c.Error.Details)
		}
		// Il repo personale: stessa risposta, con il suo owner.
		c = decodeNameConflict(t, e.do(http.MethodPost, "/repos", "alice", `{"owner":"alice","name":"mio"}`))
		if c.Error.Code != "name_in_trash" || c.Error.Details["owner"] != "alice" {
			t.Fatalf("repo personale: %+v", c.Error)
		}
	})

	t.Run("repo_attivo_resta_conflict", func(t *testing.T) {
		e.create("alice", `{"owner":"acme","name":"vivo"}`)
		c := decodeNameConflict(t, e.do(http.MethodPost, "/repos", "alice", `{"owner":"acme","name":"vivo"}`))
		if c.Error.Code != "conflict" || c.Error.Details != nil {
			t.Fatalf("repo attivo: %+v", c.Error)
		}
	})

	t.Run("creazione_con_maiuscole_diverse", func(t *testing.T) {
		// L'unicità è su lower(name) (0013): SITE urta acme/site nel cestino.
		c := decodeNameConflict(t, e.do(http.MethodPost, "/repos", "alice", `{"owner":"acme","name":"SITE"}`))
		if c.Error.Code != "name_in_trash" || c.Error.Details["repoId"] != orgID.String() || c.Error.Details["owner"] != "acme" {
			t.Fatalf("maiuscole diverse: %+v", c.Error)
		}
	})

	t.Run("ripristino_dal_link_e_poi_conflict", func(t *testing.T) {
		e.want(e.do(http.MethodPost, "/repos/deleted/"+orgID.String()+"/restore", "alice", ""), http.StatusOK)
		c := decodeNameConflict(t, e.do(http.MethodPost, "/repos", "alice", `{"owner":"acme","name":"site"}`))
		if c.Error.Code != "conflict" {
			t.Fatalf("dopo il ripristino code = %q", c.Error.Code)
		}
	})
}

// L'import (e il mirror in pull) con un nome tenuto da un repo nel cestino
// risponde name_in_trash; con un repo attivo resta conflict.
func TestPullMirror_Import_NomeNelCestino(t *testing.T) {
	e := newPullEnv(t)
	e.repo("vecchio", true)
	e.repo("attivo", true)
	e.want(e.do(http.MethodDelete, "/repos/alice/vecchio", "alice", ""), http.StatusNoContent, "")
	imp := func(name string) *httptest.ResponseRecorder {
		return e.do(http.MethodPost, "/repos/import", "alice",
			fmt.Sprintf(`{"owner":"alice","name":%q,"url":%q,"mode":"mirror"}`, name, pullURL))
	}

	c := decodeNameConflict(t, imp("vecchio"))
	if c.Error.Code != "name_in_trash" || c.Error.Details["purgeAt"] == nil || c.Error.Details["owner"] != "alice" {
		t.Fatalf("import nel cestino: %+v", c.Error)
	}
	if _, err := uuid.Parse(fmt.Sprint(c.Error.Details["repoId"])); err != nil {
		t.Fatalf("repoId (alice è admin) = %v", c.Error.Details["repoId"])
	}
	if c := decodeNameConflict(t, imp("attivo")); c.Error.Code != "conflict" || c.Error.Details != nil {
		t.Fatalf("import su repo attivo: %+v", c.Error)
	}
	if n := e.countRows(`SELECT count(*) FROM core.repo_pull_mirrors`); n != 0 {
		t.Fatalf("un import rifiutato non lascia mirror: %d", n)
	}
}
