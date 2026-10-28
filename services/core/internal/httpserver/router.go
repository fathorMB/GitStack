package httpserver

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewRouter assembla il router HTTP completo di core:
//   - /healthz, /readyz: probe k8s, non versionate. /readyz verifica la
//     connessione a Postgres.
//   - /health, /resources, /resources/{resourceId}: le operazioni del
//     contratto OpenAPI (generate in internal/openapi), servite senza
//     prefisso di versione: è il gateway che espone /v1/* e rimuove il
//     prefisso instradando a core (vedi
//     services/gateway/internal/proxy.stripV1).
//
// Identità: core non autentica nessuno, si fida del gateway. Tutte le rotte
// tranne i probe e /health (pubblica nel contratto) richiedono l'identità
// firmata dal gateway con serviceSecret (package trust): senza, o con una
// firma non valida, rispondono 401. Così chi raggiunge core direttamente
// (senza passare dal gateway) non può spacciarsi per un utente.
func NewRouter(pool *pgxpool.Pool, publisher events.Publisher, serviceSecret string, opts ...Option) http.Handler {
	server, o := newAPIServerWithOptions(pool, publisher, opts...)
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", healthz)
	mux.HandleFunc("GET /readyz", readyz(pool))

	openapi.HandlerWithOptions(server, openapi.StdHTTPServerOptions{
		BaseRouter: mux,
	})
	// x-path-tail di getRepositoryRawByPath: il ref può avere `/`, quindi la
	// coda occupa più segmenti (il codice generato la vede come uno solo).
	mux.HandleFunc("GET /repos/{owner}/{repo}/raw/{refAndPath...}", func(w http.ResponseWriter, r *http.Request) {
		server.GetRepositoryRawByPath(w, r, r.PathValue("owner"), r.PathValue("repo"), r.PathValue("refAndPath"))
	})
	// Impostazioni del registry per il servizio git (GIT-325): solo con la sua chiave.
	mux.HandleFunc("GET "+RegistrySettingsPathPrefix+"{repoId}", server.registrySettings)
	mux.HandleFunc("POST "+RegistryRemovedTagPathPrefix+"{repoId}", server.registryRemovedTag)
	mux.HandleFunc("POST "+RegistryImagePushedPathPrefix+"{repoId}", server.registryImagePushed)
	mux.HandleFunc("GET "+RegistryRemovedTagPathPrefix+"{repoId}", server.registryRemovedTags)
	// Un segreto per chiamante (F9): il gateway può fare tutto, il servizio git
	// solo risolvere owner/repo. Il Keyring del solo gateway è il caso senza git.
	ring := trust.NewKeyring(map[string]string{trust.CallerGateway: serviceSecret, trust.CallerGit: o.gitSecret})
	return trust.RequireRing(ring, callerAllowed, publicPath, o.now)(jobGuard(limitBody(mux)))
}

// newAPIServer costruisce il server con le opzioni del router; serve anche ai
// consumatori di eventi che riusano la logica delle richieste (pulls_push.go).
func newAPIServer(pool *pgxpool.Pool, publisher events.Publisher, opts ...Option) *apiServer {
	s, _ := newAPIServerWithOptions(pool, publisher, opts...)
	return s
}

func newAPIServerWithOptions(pool *pgxpool.Pool, publisher events.Publisher, opts ...Option) (*apiServer, routerOptions) {
	var o routerOptions
	for _, opt := range opts {
		opt(&o)
	}
	now := o.now
	if now == nil {
		now = time.Now
	}
	return &apiServer{
		pool:         pool,
		resources:    store.New(pool),
		events:       publisher,
		grants:       o.grants,
		readable:     o.readable,
		repoIdentity: o.repoIdentity,
		userAccess:   o.userAccess,
		git:          o.git,
		clone:        o.clone,
		attachments:  o.attachments,
		hooks:        o.hooks,
		mirrors:      o.mirrors,
		emailEnabled: o.emailEnabled,
		systemInfo:   o.systemInfo,
		instanceCA:   o.instanceCA,
		ciLogs:       o.ciLogs,
		ciBlobs:      o.ciBlobs,
		ciCapacity:   o.ciCapacity,
		now:          now,
		appsDomain:   o.appsDomain,
		appCtl:       o.appCtl,
		appsOff:      o.appsOff,
		appEgress:    o.appEgress,
		appDomains:   o.appDomains,
	}, o
}

// publicPath sono le rotte raggiungibili senza identità: probe k8s e
// /health, dichiarata `security: []` nel contratto.
func publicPath(r *http.Request) bool {
	switch r.URL.Path {
	case "/healthz", "/readyz", "/health":
		return true
	}
	return false
}

// Option personalizza NewRouter (usata dai test).
type Option func(*routerOptions)

// WithGitSecret imposta il segreto del servizio git (GITSTACK_SERVICE_SECRET_GIT):
// un'identità firmata con quella chiave è accettata solo per la risoluzione
// di owner/repo (callerAllowed). Un elenco "nuova,vecchia" accetta entrambe.
func WithGitSecret(secret string) Option { return func(o *routerOptions) { o.gitSecret = secret } }

// callerAllowed limita ciò che un servizio diverso dal gateway può fare con
// un'identità firmata: git risolve owner/repo di un utente già autenticato
// (GET /repos/{owner}/{repo}) e nient'altro (F9, ASVS V2.10.1).
func callerAllowed(caller string, r *http.Request) bool {
	if caller != trust.CallerGit {
		return true
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, RegistrySettingsPathPrefix) {
		return len(strings.Split(strings.Trim(r.URL.Path, "/"), "/")) == 4
	}
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, RegistryRemovedTagPathPrefix) {
		return len(strings.Split(strings.Trim(r.URL.Path, "/"), "/")) == 4
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, RegistryRemovedTagPathPrefix) {
		return len(strings.Split(strings.Trim(r.URL.Path, "/"), "/")) == 4
	}
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, RegistryImagePushedPathPrefix) {
		return len(strings.Split(strings.Trim(r.URL.Path, "/"), "/")) == 4
	}
	if r.Method != http.MethodGet {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	return len(parts) == 3 && parts[0] == "repos" && parts[1] != "" && parts[2] != ""
}

type routerOptions struct {
	gitSecret    string
	now          func() time.Time
	appsDomain   string
	appCtl       AppController
	appsOff      bool
	appEgress    AppEgressConfig
	appDomains   AppDomainsConfig
	grants       identityclient.CreatorGranter
	readable     identityclient.ReadableLister
	repoIdentity identityclient.RepoIdentity
	userAccess   identityclient.UserAccessReader
	git          gitclient.Git
	clone        CloneConfig
	attachments  AttachmentsConfig
	hooks        WebhookConfig
	mirrors      MirrorConfig
	emailEnabled bool
	systemInfo   SystemInfoConfig
	instanceCA   CAConfig
	ciLogs       *ci.LogStore
	ciBlobs      ci.Blobs
	ciCapacity   int
}

// WithCreatorGranter imposta il client di identity con cui core assegna il
// grant admin a chi crea una risorsa. Senza, POST /resources risponde 503.
// Se g implementa anche identityclient.ReadableLister (come
// *identityclient.Client) viene usato pure per filtrare GET /resources, a
// meno che non lo imposti prima WithReadableLister.
func WithCreatorGranter(g identityclient.CreatorGranter) Option {
	return func(o *routerOptions) {
		o.grants = g
		if l, ok := g.(identityclient.ReadableLister); ok && o.readable == nil {
			o.readable = l
		}
	}
}

// WithReadableLister imposta il client di identity da cui GET /resources
// ottiene le risorse leggibili dal chiamante. Senza, GET /resources risponde
// 503 (mai un elenco non filtrato).
func WithReadableLister(l identityclient.ReadableLister) Option {
	return func(o *routerOptions) { o.readable = l }
}

// WithRepoIdentity imposta il client di identity per le operazioni sui repo
// (owner, attributi, permessi); imposta anche grant e elenco leggibili se non
// già impostati. Senza, le operazioni sui repo rispondono 503.
func WithRepoIdentity(i identityclient.RepoIdentity) Option {
	return func(o *routerOptions) {
		o.repoIdentity = i
		if o.grants == nil {
			o.grants = i
		}
		if o.readable == nil {
			o.readable = i
		}
	}
}

// WithUserAccess imposta il client di identity da cui GET
// /users/{username}/access legge le fonti di accesso di un utente. Senza,
// risponde 503.
func WithUserAccess(u identityclient.UserAccessReader) Option {
	return func(o *routerOptions) { o.userAccess = u }
}

// WithGit imposta il client dell'API interna del servizio git. Senza, creare
// e modificare i repo risponde 503.
func WithGit(g gitclient.Git) Option {
	return func(o *routerOptions) { o.git = g }
}

// WithCloneConfig imposta la configurazione degli indirizzi di clone (R7).
func WithCloneConfig(c CloneConfig) Option {
	return func(o *routerOptions) { o.clone = c }
}

// WithAttachments imposta il volume e il limite degli allegati (I9). Senza,
// upload e download di allegati rispondono 503.
func WithAttachments(c AttachmentsConfig) Option {
	return func(o *routerOptions) { o.attachments = c }
}

// WithMirrors imposta la cifratura del token e il controllo delle destinazioni
// dei mirror in push (V8, GIT-179).
func WithMirrors(c MirrorConfig) Option {
	return func(o *routerOptions) { o.mirrors = c }
}

// WithWebhooks imposta la cifratura dei segreti e il controllo degli indirizzi
// dei webhook (M-06/G).
func WithWebhooks(c WebhookConfig) Option {
	return func(o *routerOptions) { o.hooks = c }
}

// WithClock sostituisce l'orologio con cui si controlla il timestamp della
// firma.
func WithClock(now func() time.Time) Option {
	return func(o *routerOptions) { o.now = now }
}

// WithAppsDomain imposta il dominio delle app (H2): l'indirizzo automatico di un'app è
// https://<app>-<repo>-<owner>.<dominio>. Vuoto: le app non hanno indirizzo.
func WithAppsDomain(domain string) Option {
	return func(o *routerOptions) { o.appsDomain = domain }
}

// AppController è il controller dei deploy (services/core/internal/apps): gli handler delle app
// scrivono il modello e lo svegliano; riavvio e scala passano da qui.
type AppController interface {
	Kick()
	Restart(ctx context.Context, app store.App) error
	Scale(ctx context.Context, app store.App) error
}

// AppEgressConfig sono le barriere d'istanza che l'API dell'uscita delle app (H16) usa per rifiutare le
// voci vietate: gli stessi cluster e nodo di webhook e mirror. Resolver nil usa il DNS del sistema.
type AppEgressConfig struct {
	ClusterCIDRs  []string
	NodeAddresses []string
	Deny          []string
	Resolver      apps.Resolver
	// ProxyConfigured: l'istanza ha un proxy di uscita N9; le app ristrette non lo usano.
	ProxyConfigured bool
}

// WithAppEgress imposta le barriere con cui si valutano le destinazioni di uscita delle app (H16).
func WithAppEgress(c AppEgressConfig) Option {
	return func(o *routerOptions) { o.appEgress = c }
}

// WithAppsHostingOff dichiara l'hosting delle app spento di proposito (GITSTACK_CORE_APPS_ENABLED
// non è true): la creazione di un'app risponde 503 `apps_unavailable` e non scrive nulla, così non
// restano app nel modello che nessun controller porterà mai in cluster.
func WithAppsHostingOff() Option {
	return func(o *routerOptions) { o.appsOff = true }
}

// WithAppController abilita deploy, rollback, riavvio e scala delle app (H7, H12). Senza, rispondono 503.
func WithAppController(c AppController) Option {
	return func(o *routerOptions) { o.appCtl = c }
}
