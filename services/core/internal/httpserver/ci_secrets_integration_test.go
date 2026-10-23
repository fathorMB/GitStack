//go:build integration

package httpserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// GIT-279 (CI4): segreti e variabili di repo e organizzazione, DELETE e permessi di default del
// token del job. Solo l'admin del repo (e gli owner dell'organizzazione); il valore di un
// segreto non torna da nessuna risposta e nel database è cifrato.
func TestCISecrets_RepoPermessiValoriEDelete(t *testing.T) {
	e := newHookEnv(t)
	repoID := e.repo("app", true) // internal: bob legge
	e.hid.grantWrite(repoID, "bob")
	const secret = "valore-super-segreto-123"
	base := "/repos/alice/app/actions"

	// Solo admin: bob (write) 403, carol senza lettura su repo privato 404.
	for _, p := range []string{"/secrets", "/variables"} {
		e.want(e.do(http.MethodGet, base+p, "bob", ""), http.StatusForbidden, "forbidden")
		e.want(e.do(http.MethodPut, base+p, "bob", `{"name":"A_B","value":"x"}`), http.StatusForbidden, "forbidden")
	}
	e.want(e.do(http.MethodDelete, base+"/secrets/A_B", "bob", ""), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodDelete, base+"/variables/A_B", "bob", ""), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodGet, base+"/permissions", "bob", ""), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodPut, base+"/permissions", "bob", `{"contents":"write","packages":"read","actions":"read"}`), http.StatusForbidden, "forbidden")
	priv := e.repo("riservato", false)
	_ = priv
	e.want(e.do(http.MethodGet, "/repos/alice/riservato/actions/secrets", "carol", ""), http.StatusNotFound, "")
	e.want(e.do(http.MethodDelete, "/repos/alice/riservato/actions/secrets/A_B", "carol", ""), http.StatusNotFound, "")

	// Segreto: creato, sostituito, mai riletto.
	rec := e.do(http.MethodPut, base+"/secrets", "alice", `{"name":"DEPLOY_KEY","value":"`+secret+`"}`)
	e.want(rec, http.StatusOK, "")
	if strings.Contains(rec.Body.String(), secret) || strings.Contains(rec.Body.String(), "value") {
		t.Fatalf("la risposta del PUT contiene il valore: %s", rec.Body.String())
	}
	e.want(e.do(http.MethodPut, base+"/secrets", "alice", `{"name":"DEPLOY_KEY","value":"`+secret+`-2"}`), http.StatusOK, "")
	rec = e.do(http.MethodGet, base+"/secrets", "alice", "")
	e.want(rec, http.StatusOK, "")
	if strings.Contains(rec.Body.String(), "valore-super") || strings.Contains(rec.Body.String(), "value") || !strings.Contains(rec.Body.String(), "DEPLOY_KEY") {
		t.Fatalf("elenco dei segreti: %s", rec.Body.String())
	}
	var n int
	var ct []byte
	if err := e.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM core.ci_secrets), ciphertext FROM core.ci_secrets`).Scan(&n, &ct); err != nil || n != 1 || len(ct) == 0 || strings.Contains(string(ct), "valore-super") {
		t.Fatalf("segreto nel database: n=%d err=%v", n, err)
	}
	// Nomi e valori non validi.
	e.want(e.do(http.MethodPut, base+"/secrets", "alice", `{"name":"minuscolo","value":"x"}`), http.StatusUnprocessableEntity, "")
	e.want(e.do(http.MethodPut, base+"/secrets", "alice", `{"name":"GITHUB_TOKEN","value":"x"}`), http.StatusUnprocessableEntity, "")
	e.want(e.do(http.MethodPut, base+"/secrets", "alice", `{"name":"VUOTO","value":""}`), http.StatusUnprocessableEntity, "")

	// Variabile: il valore si rilegge; DELETE e poi 404.
	e.want(e.do(http.MethodPut, base+"/variables", "alice", `{"name":"REGION","value":"eu"}`), http.StatusOK, "")
	rec = e.do(http.MethodGet, base+"/variables", "alice", "")
	e.want(rec, http.StatusOK, "")
	if !strings.Contains(rec.Body.String(), `"value":"eu"`) {
		t.Fatalf("variabili: %s", rec.Body.String())
	}
	e.want(e.do(http.MethodDelete, base+"/variables/REGION", "alice", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodDelete, base+"/variables/REGION", "alice", ""), http.StatusNotFound, "")
	e.want(e.do(http.MethodDelete, base+"/secrets/DEPLOY_KEY", "alice", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodDelete, base+"/secrets/DEPLOY_KEY", "alice", ""), http.StatusNotFound, "")

	// Permessi di default del token del job: read ovunque finché non cambiano.
	rec = e.do(http.MethodGet, base+"/permissions", "alice", "")
	e.want(rec, http.StatusOK, "")
	if !strings.Contains(rec.Body.String(), `"contents":"read"`) || !strings.Contains(rec.Body.String(), `"actions":"read"`) {
		t.Fatalf("default: %s", rec.Body.String())
	}
	e.want(e.do(http.MethodPut, base+"/permissions", "alice", `{"contents":"write","packages":"none","actions":"read"}`), http.StatusOK, "")
	rec = e.do(http.MethodGet, base+"/permissions", "alice", "")
	if !strings.Contains(rec.Body.String(), `"contents":"write"`) || !strings.Contains(rec.Body.String(), `"packages":"none"`) {
		t.Fatalf("dopo il PUT: %s", rec.Body.String())
	}
	e.want(e.do(http.MethodPut, base+"/permissions", "alice", `{"contents":"admin","packages":"none","actions":"read"}`), http.StatusUnprocessableEntity, "")

	// Repo archiviato: le modifiche sono 409, le letture no.
	e.sql(`UPDATE core.repositories SET archived_at = now() WHERE resource_id = $1`, repoID)
	e.want(e.do(http.MethodDelete, base+"/secrets/DEPLOY_KEY", "alice", ""), http.StatusConflict, "archived")
	e.want(e.do(http.MethodPut, base+"/permissions", "alice", `{"contents":"read","packages":"read","actions":"read"}`), http.StatusConflict, "archived")
	e.want(e.do(http.MethodGet, base+"/secrets", "alice", ""), http.StatusOK, "")
}

func TestCISecrets_Organizzazione(t *testing.T) {
	e := newHookEnv(t)
	e.hid.orgOwner[acmeID] = []string{aliceID}
	e.want(e.do(http.MethodPost, "/repos", "alice", `{"owner":"acme","name":"tools","visibility":"private"}`), http.StatusCreated, "")
	// bob non è owner.
	e.want(e.do(http.MethodGet, "/orgs/acme/actions/secrets", "bob", ""), http.StatusForbidden, "")
	e.want(e.do(http.MethodPut, "/orgs/acme/actions/secrets", "bob", `{"name":"K","value":"x"}`), http.StatusForbidden, "")
	e.want(e.do(http.MethodDelete, "/orgs/acme/actions/secrets/K", "bob", ""), http.StatusForbidden, "")
	e.want(e.do(http.MethodDelete, "/orgs/acme/actions/variables/K", "bob", ""), http.StatusForbidden, "")

	rec := e.do(http.MethodPut, "/orgs/acme/actions/secrets", "alice", `{"name":"ORG_KEY","value":"org-segreto-lungo"}`)
	e.want(rec, http.StatusOK, "")
	if strings.Contains(rec.Body.String(), "org-segreto") {
		t.Fatalf("PUT: %s", rec.Body.String())
	}
	rec = e.do(http.MethodGet, "/orgs/acme/actions/secrets", "alice", "")
	e.want(rec, http.StatusOK, "")
	if strings.Contains(rec.Body.String(), "org-segreto") || !strings.Contains(rec.Body.String(), "ORG_KEY") {
		t.Fatalf("elenco: %s", rec.Body.String())
	}
	e.want(e.do(http.MethodPut, "/orgs/acme/actions/variables", "alice", `{"name":"V","value":"1"}`), http.StatusOK, "")
	e.want(e.do(http.MethodDelete, "/orgs/acme/actions/variables/V", "alice", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodDelete, "/orgs/acme/actions/secrets/ORG_KEY", "alice", ""), http.StatusNoContent, "")
	e.want(e.do(http.MethodDelete, "/orgs/acme/actions/secrets/ORG_KEY", "alice", ""), http.StatusNotFound, "")
	_ = uuid.Nil
}
