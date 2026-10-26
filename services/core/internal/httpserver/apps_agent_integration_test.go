//go:build integration

package httpserver_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-354 (H13, P4): un agente registrato agisce con la sua identità, e i grant lo limitano.

func (e *appsEnv) registerBot() {
	users["bot"] = botID
	e.id.owners["bot"] = identityclient.Owner{Type: "user", ID: uuid.MustParse(botID), Name: "bot"}
}

// Il grant P4 che manca o è troppo basso nega: read non fa nulla, write non crea né modifica la configurazione,
// e solo con il grant giusto l'azione passa i controlli.
func TestApps_AgenteLimitatoDaiGrantP4(t *testing.T) {
	e := newAppsEnv(t)
	e.registerBot()
	repoID := e.repo("handbook", true) // internal: bot legge, nient'altro
	base := "/repos/alice/handbook/apps"
	e.want(e.do(http.MethodPost, base, "alice", createWeb), http.StatusAccepted, "")

	mutations := []struct{ method, path, body string }{
		{http.MethodPost, base + "/web/deploys", `{"image":"sha-1"}`},
		{http.MethodPost, base + "/web/rollback", `{"version":1}`},
		{http.MethodPost, base + "/web/restart", ``},
		{http.MethodPut, base + "/web/scale", `{"copies":2}`},
	}
	// Solo read: ogni operazione di scrittura è negata (403), anche il deploy.
	for _, c := range mutations {
		e.want(e.do(c.method, c.path, "bot", c.body), http.StatusForbidden, "forbidden")
	}
	e.want(e.do(http.MethodGet, base+"/web/logs", "bot", ""), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodGet, base, "bot", ""), http.StatusOK, "")

	// Con write passa il controllo del permesso (qui l'hosting non ha controller: 503, non 403), ma le
	// operazioni da admin restano negate.
	e.id.grantWrite(repoID, "bot")
	for _, c := range mutations {
		e.want(e.do(c.method, c.path, "bot", c.body), http.StatusServiceUnavailable, "apps_unavailable")
	}
	e.want(e.do(http.MethodPost, base, "bot", `{"name":"altra","image":"sha-1","port":8080}`), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodPut, base+"/web/secrets/TOKEN", "bot", `{"value":"x"}`), http.StatusForbidden, "forbidden")
	e.want(e.do(http.MethodDelete, base+"/web", "bot", ""), http.StatusForbidden, "forbidden")
}

// Il deploy di un agente compare nello storico con la sua identità e il tipo `agent`; quello di una persona
// resta `human`.
func TestApps_AgenteNelloStoricoConLaSuaIdentita(t *testing.T) {
	e := newAppsEnv(t)
	e.registerBot()
	repoID := e.repo("handbook", true)
	e.id.fakeIdentity.mu.Lock()
	e.id.fakeIdentity.grants[repoID] = append(e.id.fakeIdentity.grants[repoID], botID) // grant admin all'agente
	e.id.fakeIdentity.mu.Unlock()
	base := "/repos/alice/handbook/apps"

	rec := e.do(http.MethodPost, base, "bot", `{"name":"agentapp","image":"sha-1","port":8080}`)
	e.want(rec, http.StatusAccepted, "")
	rec = e.do(http.MethodPost, base, "alice", `{"name":"humanapp","image":"sha-1","port":8080}`)
	e.want(rec, http.StatusAccepted, "")

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
