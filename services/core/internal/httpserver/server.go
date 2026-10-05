package httpserver

import (
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// apiServer implementa openapi.ServerInterface: le operazioni del contratto
// leggono/scrivono sullo store Postgres (schema dedicato "core", D6) e, per
// la creazione, pubblicano l'evento di prova tramite events.Publisher.
// Implementare l'interfaccia generata dal contratto, invece di definire le
// rotte a mano, impedisce a core di discostarsi dal contratto: se il
// contratto cambia, il generato cambia, e questo file smette di compilare
// finché non lo si adegua (stessa convenzione del gateway, vedi
// services/gateway/internal/httpserver/server.go).
type apiServer struct {
	pool      *pgxpool.Pool
	resources *store.Store
	events    events.Publisher
	// grants assegna il grant admin al creatore di una risorsa; nil se
	// identity non è configurata (POST /resources risponde 503).
	grants identityclient.CreatorGranter
	// readable elenca le risorse leggibili dal chiamante; nil se identity non
	// è configurata (GET /resources risponde 503).
	readable identityclient.ReadableLister
	// repoIdentity e git servono alle operazioni sui repo (M-03); nil = 503.
	repoIdentity identityclient.RepoIdentity
	git          gitclient.Git
	clone        CloneConfig
	// now è l'orologio: serve alla scadenza dei 7 giorni di un repo eliminato.
	now func() time.Time
}

var _ openapi.ServerInterface = (*apiServer)(nil)
