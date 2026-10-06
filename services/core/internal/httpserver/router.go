package httpserver

import (
	"net/http"
	"time"

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
	var o routerOptions
	for _, opt := range opts {
		opt(&o)
	}
	now := o.now
	if now == nil {
		now = time.Now
	}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", healthz)
	mux.HandleFunc("GET /readyz", readyz(pool))

	server := &apiServer{
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
		now:          now,
	}

	openapi.HandlerWithOptions(server, openapi.StdHTTPServerOptions{
		BaseRouter: mux,
	})
	// x-path-tail di getRepositoryRawByPath: il ref può avere `/`, quindi la
	// coda occupa più segmenti (il codice generato la vede come uno solo).
	mux.HandleFunc("GET /repos/{owner}/{repo}/raw/{refAndPath...}", func(w http.ResponseWriter, r *http.Request) {
		server.GetRepositoryRawByPath(w, r, r.PathValue("owner"), r.PathValue("repo"), r.PathValue("refAndPath"))
	})
	return trust.Require(serviceSecret, publicPath, o.now)(mux)
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

type routerOptions struct {
	now          func() time.Time
	grants       identityclient.CreatorGranter
	readable     identityclient.ReadableLister
	repoIdentity identityclient.RepoIdentity
	userAccess   identityclient.UserAccessReader
	git          gitclient.Git
	clone        CloneConfig
	attachments  AttachmentsConfig
	hooks        WebhookConfig
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
