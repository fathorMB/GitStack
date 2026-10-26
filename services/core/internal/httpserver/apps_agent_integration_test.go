//go:build integration

package httpserver_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/httpserver"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-354 (H13, P4): un agente registrato agisce con la sua identità, e i grant lo limitano.

func (e *appsEnv) registerBot() {
	users["bot"] = botID
	e.id.owners["bot"] = identityclient.Owner{Type: "user", ID: uuid.MustParse(botID), Name: "bot"}
}

// Il grant P4 che manca o è troppo basso nega: read non fa nulla, write non crea né tocca i segreti.
// Con write l'agente fa deploy, rollback, riavvio e scala davvero, e il suo deploy compare nello storico
// con la sua identità; quello di una persona con write resta human.
func TestApps_AgenteLimitatoDaiGrantP4(t *testing.T) {
	e := newAppsEnv(t, httpserver.WithAppController(commitNoopController{}))
	e.registerBot()
	repoID := e.repo("handbook", true) // internal: bot legge, nient'altro
	base := "/repos/alice/handbook/apps"
	e.want(e.do(http.MethodPost, base, "alice", createWeb), http.StatusAccepted, "")
	e.finishCommitDeploy(t, "web")

	deploy := struct{ method, path, body string }{http.MethodPost, base + "/web/deploys", `{"image":"sha-2"}`}
	rollback := struct{ method, path, body string }{http.MethodPost, base + "/web/rollback", `{"version":1}`}
	restart := struct{ method, path, body string }{http.MethodPost, base + "/web/restart", ``}
	scale := struct{ method, path, body string }{http.MethodPut, base + "/web/scale", `{"copies":2}`}
	all := []struct{ method, path, body string }{deploy, rollback, restart, scale}

	// Solo read: ogni operazione di scrittura è negata (403), anche il deploy, e i log.
	for _, c := range all {
		e.want(e.do(c.method, c.path, "bot", c.body), http.StatusForbidden, "forbidden")
	}
	e.want(e.do(http.MethodGet, base+"/web/logs", "bot", ""), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodGet, base, "bot", ""), http.StatusOK, "")
	if n := len(e.versions(base+"/web/versions", "alice").Items); n != 1 {
		t.Fatalf("le negazioni non devono creare versioni: %d", n)
	}

	// Con write: il deploy dell'agente riesce (202) e finisce nello storico come agent/bot.
	e.id.grantWrite(repoID, "bot")
	e.want(e.do(deploy.method, deploy.path, "bot", deploy.body), http.StatusAccepted, "")
	l := e.versions(base+"/web/versions", "alice")
	if len(l.Items) != 2 {
		t.Fatalf("versioni %d, attese 2", len(l.Items))
	}
	if v := l.Items[0]; v.Number != 2 || v.CreatedBy == nil || v.CreatedBy.Username != "bot" || v.CreatedBy.Type != openapi.AppActorTypeAgent || v.CreatedBy.Id.String() != botID {
		t.Fatalf("v2 deve essere dell'agente bot: %+v", v)
	}
	if v := l.Items[1]; v.Number != 1 || v.CreatedBy == nil || v.CreatedBy.Username != "alice" || v.CreatedBy.Type != openapi.AppActorTypeHuman {
		t.Fatalf("v1 deve restare di alice (human): %+v", v)
	}
	e.finishCommitDeploy(t, "web")
	e.sql(`UPDATE core.apps SET active_version = 2 WHERE name = 'web'`) // come farebbe il controller a deploy riuscito

	// Le altre mutazioni con write rispondono 2xx.
	e.want(e.do(rollback.method, rollback.path, "bot", rollback.body), http.StatusAccepted, "")
	if v := e.versions(base+"/web/versions", "alice").Items[0]; v.Number != 3 || v.CreatedBy == nil || v.CreatedBy.Username != "bot" || v.CreatedBy.Type != openapi.AppActorTypeAgent {
		t.Fatalf("il rollback dell'agente è una versione dell'agente: %+v", v)
	}
	e.finishCommitDeploy(t, "web")
	e.want(e.do(restart.method, restart.path, "bot", restart.body), http.StatusAccepted, "")
	e.want(e.do(scale.method, scale.path, "bot", scale.body), http.StatusOK, "")
	e.want(e.do(http.MethodGet, base+"/web/logs", "bot", ""), http.StatusOK, "")

	// Il caso opposto: il deploy di una persona con write è human (il tipo dipende dal chiamante).
	e.id.grantWrite(repoID, "bob")
	e.want(e.do(deploy.method, deploy.path, "bob", `{"image":"sha-3"}`), http.StatusAccepted, "")
	if v := e.versions(base+"/web/versions", "alice").Items[0]; v.Number != 4 || v.CreatedBy == nil || v.CreatedBy.Username != "bob" || v.CreatedBy.Type != openapi.AppActorTypeHuman {
		t.Fatalf("v4 deve essere di bob (human): %+v", v)
	}

	// Le operazioni da admin restano negate all'agente con write.
	e.want(e.do(http.MethodPost, base, "bot", `{"name":"altra","image":"sha-1","port":8080}`), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodPut, base+"/web/secrets/TOKEN", "bot", `{"value":"x"}`), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodDelete, base+"/web", "bot", ""), http.StatusForbidden, "forbidden")
}

// Anche la creazione con grant admin porta l'identità dell'agente (versione 1 e app).
func TestApps_AgenteNelloStoricoConLaSuaIdentita(t *testing.T) {
	e := newAppsEnv(t, httpserver.WithAppController(commitNoopController{}))
	e.registerBot()
	repoID := e.repo("handbook", true)
	e.id.fakeIdentity.mu.Lock()
	e.id.fakeIdentity.grants[repoID] = append(e.id.fakeIdentity.grants[repoID], botID) // grant admin all'agente
	e.id.fakeIdentity.mu.Unlock()
	base := "/repos/alice/handbook/apps"

	e.want(e.do(http.MethodPost, base, "bot", `{"name":"agentapp","image":"sha-1","port":8080}`), http.StatusAccepted, "")
	e.want(e.do(http.MethodPost, base, "alice", `{"name":"humanapp","image":"sha-1","port":8080}`), http.StatusAccepted, "")

	check := func(app, user, typ string) {
		t.Helper()
		l := e.versions(base+"/"+app+"/versions", "alice")
		if len(l.Items) != 1 {
			t.Fatalf("%s: versioni %d", app, len(l.Items))
		}
		by := l.Items[0].CreatedBy
		if by == nil || by.Username != user || by.Type != openapi.AppActorType(typ) || by.Id.String() == "" {
			t.Fatalf("%s: createdBy %+v, voluto %s/%s", app, by, user, typ)
		}
		a := e.app(e.do(http.MethodGet, base+"/"+app, "alice", ""))
		if a.CreatedBy == nil || a.CreatedBy.Username != user || a.CreatedBy.Type != openapi.AppActorType(typ) {
			t.Fatalf("%s: app createdBy %+v", app, a.CreatedBy)
		}
	}
	check("agentapp", "bot", "agent")
	check("humanapp", "alice", "human")
}
