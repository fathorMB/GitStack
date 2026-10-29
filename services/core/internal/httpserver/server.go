package httpserver

import (
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
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
	// userAccess serve GET /users/{username}/access; nil = 503.
	userAccess identityclient.UserAccessReader
	git        gitclient.Git
	clone      CloneConfig
	// attachments: volume e limite degli allegati (I9); Disk nil = 503.
	attachments AttachmentsConfig
	// hooks: cifratura dei segreti e validazione degli indirizzi dei webhook.
	hooks WebhookConfig
	// mirrors: cifratura del token e controllo delle destinazioni dei mirror in push.
	mirrors MirrorConfig
	// emailEnabled: installazione con SMTP (preferenze email, C5).
	emailEnabled bool
	// systemInfo: file delle informazioni dell host (GIT-213).
	systemInfo SystemInfoConfig
	// instanceCA: CA interna dell istanza per GET /instance/ca (GIT-383).
	instanceCA CAConfig
	// ciLogs: i log dei job su disco (GIT-316); nil = 503.
	ciLogs *ci.LogStore
	// ciBlobs: artefatti e cache dei job su disco o S3 (GIT-280); nil = 503.
	ciBlobs ci.Blobs
	// ciCapacity: job in corso in tutto (CICapacity, GIT-277); 0 = ci.DefaultCapacity.
	ciCapacity int
	// now è l'orologio: serve alla scadenza dei 7 giorni di un repo eliminato.
	now func() time.Time
	// appsDomain è il dominio delle app (H2); vuoto = nessun indirizzo.
	appsDomain string
	// appCtl è il controller dei deploy; nil = deploy non disponibili (503).
	appCtl AppController
	// appsOff: hosting spento di proposito (WithAppsHostingOff); la creazione di app risponde 503.
	appsOff bool
	// appEgress: barriere d'istanza per le destinazioni di uscita delle app (H16).
	appEgress AppEgressConfig
	// appDomains: domini propri delle app pubbliche (v3-c, GIT-458).
	appDomains AppDomainsConfig
}

var _ openapi.ServerInterface = (*apiServer)(nil)
