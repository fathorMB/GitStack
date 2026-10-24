// Package registry serve il registry OCI di GitStack (RG1, v2-b) agli indirizzi
// <host>/<owner>/<repo>[/<nome>], con distribution
// (github.com/distribution/distribution/v3, licenza Apache-2.0) come motore dei
// protocolli OCI Distribution e dell'archiviazione su disco o S3.
//
// Distribution non conosce utenti, repo né permessi: qui sta tutto il resto.
//
//   - Autenticazione: Basic, la password è un token personale o di agente con scope
//     registry, oppure il token del job (gsj_). Nessun accesso anonimo, nemmeno a /v2/.
//   - Permessi: uguali a quelli del repo (M-03). pull = ruolo read + read:registry;
//     push = ruolo write + write:registry; delete = ruolo admin + admin:registry. Il
//     token del job pubblica solo se il suo livello packages è write, scarica se è
//     read o write, non cancella mai, e vale solo per il repo del suo job.
//   - Un repo archiviato (R10) si scarica ma non riceve push né eliminazioni; un repo
//     inesistente, eliminato o non leggibile risponde sempre 404.
//   - Indirizzi: la parte owner/repo dell'indirizzo si traduce nell'id del repo, e
//     distribution archivia sotto «<id>/<nome>»: rinominare o trasferire il repo non
//     sposta le immagini, eliminarlo (R2) e ripristinarlo entro 7 giorni le riporta
//     com'erano, la cancellazione definitiva le toglie (Purge).
package registry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/distribution/distribution/v3/configuration"
	"github.com/distribution/distribution/v3/registry/api/errcode"
	"github.com/distribution/distribution/v3/registry/auth"
	"github.com/distribution/distribution/v3/registry/handlers"
	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/distribution/distribution/v3/registry/storage/driver/factory"
	// I driver di archiviazione: disco e S3. Nessun altro (gcs, azure) è compilato.
	_ "github.com/distribution/distribution/v3/registry/storage/driver/filesystem"
	_ "github.com/distribution/distribution/v3/registry/storage/driver/s3-aws"
	"github.com/sirupsen/logrus"

	"github.com/fathorMB/GitStack/pkg/scopes"
	"github.com/fathorMB/GitStack/services/git/internal/access"
	"github.com/fathorMB/GitStack/services/git/internal/trust"
)

// Scope dei token che governano il registry (RG1).
const (
	ScopeRead   = "read:registry"
	ScopeWrite  = "write:registry"
	ScopeDelete = "admin:registry"
)

// JobTokenPrefix è il prefisso del token del job (core, ci.JobTokenPrefix).
const JobTokenPrefix = "gsj_"

// authName è il nome con cui il controllo accessi è registrato in distribution.
const authName = "gitstack"

// Azioni (come nel protocollo token di distribution).
const (
	actionPull   = "pull"
	actionPush   = "push"
	actionDelete = "delete"
)

// repoRoot è la cartella, sotto la radice di archiviazione di distribution, dove
// distribution tiene i repository (registry/storage/paths.go).
const repoRoot = "/docker/registry/v2/repositories/"

// Backend è ciò che serve per autenticare e autorizzare: identity, core e il ci-gateway.
type Backend interface {
	access.Identity
	access.Core
	access.Jobs
}

// S3 configura l'archiviazione su un servizio S3 (o compatibile).
type S3 struct {
	Bucket string
	Region string
	// Endpoint è l'URL del servizio (vuoto = AWS).
	Endpoint  string
	AccessKey string
	SecretKey string
	// PathStyle: indirizzi http://endpoint/bucket/chiave (MinIO, S3 finto).
	PathStyle bool
	// Prefix è la cartella dentro il bucket (default «gitstack-registry»).
	Prefix string
	// Insecure: l'endpoint è http.
	Insecure bool
}

// Config configura il registry. Con S3 non nil l'archiviazione è S3, altrimenti Dir.
type Config struct {
	// Dir è la cartella su disco dei dati del registry.
	Dir string
	S3  *S3
	// Secret è il segreto da cui si deriva quello degli upload di distribution
	// (deve essere uguale fra le repliche). Vuoto = casuale a ogni avvio.
	Secret string
	// Realm è il realm del Basic auth (default «GitStack registry»).
	Realm  string
	Logger *slog.Logger
	// KeepUploadPurging lascia attiva la pulizia periodica degli upload interrotti di
	// distribution; i test la spengono per non lasciare goroutine.
	KeepUploadPurging bool

	// QuotaBytes è la quota di ogni owner (RG2); 0 = nessuna quota. Con più repo dello stesso
	// owner si sommano i blob di tutti i suoi repo (uno condiviso si conta una volta).
	QuotaBytes int64
	// SettingsTTL: per quanto si tengono le impostazioni di un repo lette da core
	// (default 10 s; negativo = mai in cache). UsageTTL vale per il conteggio della quota (default 15 s).
	SettingsTTL, UsageTTL time.Duration
	// CleanupInterval: ogni quanto gira la pulizia (RG2); 0 = non parte da sola (Cleanup resta
	// chiamabile).
	CleanupInterval time.Duration
	// BlobGrace: i blob non referenziati più giovani di così non si cancellano (default 24 h):
	// un push in corso li ha caricati prima del manifest.
	BlobGrace time.Duration
	// Now è l'orologio (default time.Now); i test lo sostituiscono.
	Now func() time.Time

	// testInMemory: archiviazione in memoria. Solo per i test sugli host dove il driver su
	// disco di distribution non gira (Windows: rinomina di file ancora aperti).
	testInMemory bool
}

// Handler è il registry OCI.
type Handler struct {
	backend Backend
	app     *handlers.App
	driver  storagedriver.StorageDriver
	realm   string
	log     *slog.Logger

	// Impostazioni, quota e pulizia (GIT-325, RG2). settings è nil se il backend non le dà.
	settings  *settingsCache
	quota     int64
	now       func() time.Time
	blobGrace time.Duration
	usageTTL  time.Duration
	usageMu   sync.Mutex
	usage     map[string]usageEntry
	cleanup   *cleaner
}

func (c Config) storage() (configuration.Storage, error) {
	st := configuration.Storage{
		// L'eliminazione di manifest e blob è l'azione admin:registry.
		"delete": configuration.Parameters{"enabled": true},
		// I blob passano sempre dal registry: S3 non è raggiungibile dal client e
		// l'autorizzazione resta una sola.
		"redirect": configuration.Parameters{"disable": true},
		"cache":    configuration.Parameters{"blobdescriptor": "inmemory"},
	}
	if !c.KeepUploadPurging {
		st["maintenance"] = configuration.Parameters{"uploadpurging": map[any]any{"enabled": false}}
	}
	if c.S3 != nil {
		s := c.S3
		if s.Bucket == "" {
			return nil, errors.New("registry: bucket S3 mancante")
		}
		region := s.Region
		if region == "" {
			region = "us-east-1"
		}
		prefix := s.Prefix
		if prefix == "" {
			prefix = "gitstack-registry"
		}
		p := configuration.Parameters{
			"bucket":         s.Bucket,
			"region":         region,
			"accesskey":      s.AccessKey,
			"secretkey":      s.SecretKey,
			"rootdirectory":  "/" + strings.Trim(prefix, "/"),
			"forcepathstyle": s.PathStyle,
			"secure":         !s.Insecure,
			"v4auth":         true,
		}
		if s.Endpoint != "" {
			p["regionendpoint"] = s.Endpoint
		}
		st["s3"] = p
		return st, nil
	}
	if c.testInMemory {
		raw := make([]byte, 8)
		_, _ = io.ReadFull(rand.Reader, raw)
		st[sharedMemoryDriver] = configuration.Parameters{"id": hex.EncodeToString(raw)}
		return st, nil
	}
	if c.Dir == "" {
		return nil, errors.New("registry: cartella dei dati mancante")
	}
	st["filesystem"] = configuration.Parameters{"rootdirectory": c.Dir}
	return st, nil
}

// New costruisce il registry. Distribution registra il controllo accessi per nome in
// un registro globale: per questo il backend passa dalle opzioni, non da una variabile.
func New(cfg Config, b Backend) (h *Handler, err error) {
	if b == nil {
		return nil, errors.New("registry: backend mancante")
	}
	st, err := cfg.storage()
	if err != nil {
		return nil, err
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	secret := ""
	if cfg.Secret != "" {
		sum := sha256.Sum256([]byte("gitstack-registry-upload|" + cfg.Secret))
		secret = hex.EncodeToString(sum[:])
	} else {
		raw := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, raw); err != nil {
			return nil, err
		}
		secret = hex.EncodeToString(raw)
	}
	realm := cfg.Realm
	if realm == "" {
		realm = "GitStack registry"
	}
	h = &Handler{backend: b, realm: realm, log: log, quota: cfg.QuotaBytes, usage: map[string]usageEntry{}}
	h.now = cfg.Now
	if h.now == nil {
		h.now = time.Now
	}
	h.blobGrace = cfg.BlobGrace
	if h.blobGrace <= 0 {
		h.blobGrace = 24 * time.Hour
	}
	h.usageTTL = cfg.UsageTTL
	if h.usageTTL == 0 {
		h.usageTTL = 15 * time.Second
	}
	if src, ok := b.(SettingsSource); ok {
		ttl := cfg.SettingsTTL
		if ttl == 0 {
			ttl = 10 * time.Second
		}
		h.settings = newSettingsCache(src, ttl)
		h.settings.now = h.now
	}

	// Il log di distribution (logrus) è spento: registra ogni 404 normale di un HEAD e il
	// nome interno dei repo. Gli errori del server li registra ServeHTTP.
	logrus.SetOutput(io.Discard)

	dc := &configuration.Configuration{
		Version: "0.1",
		Storage: st,
		Auth:    configuration.Auth{authName: configuration.Parameters{"controller": &controller{realm: realm}}},
		HTTP:    configuration.HTTP{Secret: secret, RelativeURLs: true},
	}
	dc.Log.Level = "warn"

	// NewApp va in panic se il driver non si configura (cartella, credenziali S3).
	defer func() {
		if r := recover(); r != nil {
			h, err = nil, fmt.Errorf("registry: archiviazione non avviabile: %v", r)
		}
	}()
	h.app = handlers.NewApp(context.Background(), dc)
	params := dc.Storage.Parameters()
	h.driver, err = factory.Create(context.Background(), dc.Storage.Type(), params)
	if err != nil {
		return nil, fmt.Errorf("registry: archiviazione non avviabile: %w", err)
	}
	if h.settings != nil {
		h.cleanup = newCleaner(h)
		if cfg.CleanupInterval > 0 {
			h.cleanup.start(cfg.CleanupInterval)
		}
	}
	return h, nil
}

func init() {
	if err := auth.Register(authName, func(o map[string]any) (auth.AccessController, error) {
		c, ok := o["controller"].(*controller)
		if !ok {
			return nil, errors.New("registry: controllo accessi non configurato")
		}
		return c, nil
	}); err != nil {
		panic(err)
	}
}

// Close ferma i lavori in background di distribution.
func (h *Handler) Close() error {
	if h.cleanup != nil {
		h.cleanup.stop()
	}
	return h.app.Shutdown()
}

// Purge cancella le immagini del repo (cancellazione definitiva, R2): i suoi repository
// e i tag. I blob che nessun repo referenzia restano finché la pulizia (RG2) non li
// raccoglie; non si cancellano qui perché possono essere condivisi con altri repo.
func (h *Handler) Purge(ctx context.Context, repoID string) error {
	if !uuidRe.MatchString(repoID) {
		return fmt.Errorf("registry: id repo non valido")
	}
	err := h.driver.Delete(ctx, repoRoot+repoID)
	var nf storagedriver.PathNotFoundError
	if err != nil && !errors.As(err, &nf) {
		return fmt.Errorf("registry: cancellazione delle immagini: %w", err)
	}
	return nil
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// route è un percorso /v2/<owner>/<repo>[/<nome>]/<risorsa>.
type route struct {
	owner, repo string
	// image è il nome sotto il repo (vuoto = l'immagine senza nome).
	image string
	// tail è «manifests/<ref>», «blobs/<digest>», «blobs/uploads/[<uuid>]», «tags/list» o «referrers/<digest>».
	tail string
}

var (
	routeRe = regexp.MustCompile(`^/v2/(.+?)/((?:manifests|referrers)/[^/]+|blobs/uploads(?:/[^/]*)?|blobs/[^/]+|tags/list)$`)
)

// parseRoute riconosce un percorso del protocollo; ok false = non è una rotta del registry
// legata a un repo (per esempio /v2/_catalog).
func parseRoute(p string) (route, bool) {
	m := routeRe.FindStringSubmatch(p)
	if m == nil {
		return route{}, false
	}
	parts := strings.Split(m[1], "/")
	if len(parts) < 2 {
		return route{}, false
	}
	rt := route{owner: parts[0], repo: parts[1], image: strings.Join(parts[2:], "/"), tail: m[2]}
	return rt, rt.owner != "" && rt.repo != ""
}

func (rt route) upstreamName(repoID string) string {
	if rt.image == "" {
		return repoID
	}
	return repoID + "/" + rt.image
}

// actionFor dà l'azione richiesta da un metodo su una rotta. Annullare un upload in corso
// (DELETE su blobs/uploads/…) è parte della pubblicazione, non un'eliminazione.
func actionFor(method, tail string) string {
	switch method {
	case http.MethodGet, http.MethodHead:
		return actionPull
	case http.MethodDelete:
		if strings.HasPrefix(tail, "blobs/uploads") {
			return actionPush
		}
		return actionDelete
	default:
		return actionPush
	}
}

func scopeFor(action string) string {
	switch action {
	case actionPush:
		return ScopeWrite
	case actionDelete:
		return ScopeDelete
	}
	return ScopeRead
}

// grant è l'esito di un'autorizzazione, passato a distribution nel contesto della richiesta.
type grant struct {
	user    string
	repoID  string
	actions map[string]bool
	// from è il repo da cui si monta un blob (solo pull).
	fromID string
}

type grantKey struct{}

// controller è l'AccessController di distribution: ha già deciso ServeHTTP, qui si
// verifica soltanto che ciò che distribution sta per fare sia ciò che è stato autorizzato.
type controller struct{ realm string }

type challenge struct{ realm string }

func (c challenge) Error() string { return "autenticazione richiesta" }
func (c challenge) SetHeaders(_ *http.Request, w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", fmt.Sprintf("Basic realm=%q", c.realm))
}

func (c *controller) Authorized(r *http.Request, records ...auth.Access) (*auth.Grant, error) {
	g, ok := r.Context().Value(grantKey{}).(*grant)
	if !ok {
		return nil, challenge{realm: c.realm}
	}
	for _, rec := range records {
		if rec.Type != "repository" {
			return nil, errors.New("risorsa non autorizzata")
		}
		id, _, _ := strings.Cut(rec.Name, "/")
		switch {
		case id == g.repoID && g.actions[rec.Action]:
		case g.fromID != "" && id == g.fromID && rec.Action == actionPull:
		default:
			return nil, errors.New("accesso non autorizzato")
		}
	}
	return &auth.Grant{User: auth.UserInfo{Name: g.user}}, nil
}

// ServeHTTP implementa http.Handler per i percorsi /v2/.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	cred, ok := basicPassword(r)
	if !ok {
		h.challenge(w)
		return
	}
	// Il token è verificato prima di guardare il percorso: nessuna differenza fra repo
	// che esistono e che non esistono per chi non ha credenziali valide (P2).
	cl, ok := h.authenticate(w, r, cred)
	if !ok {
		return
	}
	p := r.URL.Path
	if p == "/v2" || p == "/v2/" {
		if cl.job && !h.jobTokenValid(w, r, cl.token) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
		return
	}
	rt, ok := parseRoute(p)
	if !ok {
		serveErr(w, errcode.ErrorCodeUnsupported.WithMessage("operazione non supportata"))
		return
	}
	action := actionFor(r.Method, rt.tail)
	g, ok := h.authorize(w, r, cl, rt.owner, rt.repo, action)
	if !ok {
		return
	}
	g.user = cl.name
	if !h.guard(w, r, rt, action, g.repoID) {
		return
	}
	if action == actionPush && r.Method == http.MethodDelete {
		// Distribution chiede l'azione delete anche per annullare un upload: qui basta write.
		g.actions[actionDelete] = true
	}

	// Il montaggio di un blob da un altro repo vale solo se il chiamante lo può leggere:
	// altrimenti si scarta il parametro e il push avviene con un upload normale.
	q := r.URL.Query()
	if from := q.Get("from"); from != "" {
		g.fromID = ""
		if frt, fok := parseFrom(from); fok && action == actionPush {
			if fid, ok := h.resolveFrom(r.Context(), cl, frt); ok {
				g.fromID = fid
				q.Set("from", frt.upstreamName(fid))
			}
		}
		if g.fromID == "" {
			q.Del("from")
			q.Del("mount")
		}
	}

	r2 := r.Clone(context.WithValue(r.Context(), grantKey{}, g.grant))
	name := rt.upstreamName(g.repoID)
	r2.URL.Path = "/v2/" + name + "/" + rt.tail
	r2.URL.RawPath = ""
	r2.URL.RawQuery = q.Encode()
	r2.RequestURI = r2.URL.RequestURI()
	// Distribution mette nelle intestazioni Location/Link il nome interno: si riporta
	// all'indirizzo con cui il chiamante lo ha chiesto.
	canon := g.canonical
	if canon == "" {
		canon = rt.owner + "/" + rt.repo
	}
	rw := &rewriteWriter{ResponseWriter: w, from: "/v2/" + g.repoID, to: "/v2/" + canon}
	h.app.ServeHTTP(rw, r2)
	if rw.status >= 500 {
		h.log.Error("il registry ha risposto con un errore", "status", rw.status, "method", r.Method, "path", rt.tail, "repoId", g.repoID)
	}
}

// jobTokenValid verifica un token del job senza un repo: il ci-gateway lo convalida prima
// di cercare il repo, quindi «non trovato» vuol dire che il token è buono.
func (h *Handler) jobTokenValid(w http.ResponseWriter, r *http.Request, token string) bool {
	_, err := h.backend.JobAccess(r.Context(), token, "", "")
	switch {
	case errors.Is(err, access.ErrNotFound), err == nil:
		return true
	case errors.Is(err, access.ErrInvalidJobToken):
		h.challenge(w)
	default:
		h.log.Error("verifica del token del job non riuscita", "err", err)
		serveErr(w, errcode.ErrorCodeUnavailable)
	}
	return false
}

// parseFrom legge il parametro from (<owner>/<repo>[/<nome>]) di un montaggio.
func parseFrom(from string) (route, bool) {
	parts := strings.Split(from, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return route{}, false
	}
	return route{owner: parts[0], repo: parts[1], image: strings.Join(parts[2:], "/")}, true
}

func (h *Handler) challenge(w http.ResponseWriter) {
	challenge{realm: h.realm}.SetHeaders(nil, w)
	serveErr(w, errcode.ErrorCodeUnauthorized)
}

func serveErr(w http.ResponseWriter, err error) {
	_ = errcode.ServeJSON(w, err)
}

func basicPassword(r *http.Request) (string, bool) {
	_, pw, ok := r.BasicAuth()
	if !ok || pw == "" {
		return "", false
	}
	return pw, true
}

// who è il chiamante autenticato.
type who struct {
	name string
	// job è true per un token del job.
	job       bool
	token     string
	principal access.Principal
}

// authenticate verifica la credenziale. Un token del job si convalida per repo, in authorize.
func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request, cred string) (who, bool) {
	if strings.HasPrefix(cred, JobTokenPrefix) {
		return who{name: "job", job: true, token: cred}, true
	}
	p, valid, err := h.backend.VerifyToken(r.Context(), cred)
	if err != nil {
		h.log.Error("verifica del token non riuscita", "err", err)
		serveErr(w, errcode.ErrorCodeUnavailable)
		return who{}, false
	}
	if !valid {
		h.challenge(w)
		return who{}, false
	}
	return who{name: p.Username, principal: p}, true
}

type authzGrant struct {
	*grant
	canonical string
}

// authorize decide l'azione su owner/repo e ritorna il permesso da passare a distribution.
// Risponde da sé a ogni rifiuto (ok false).
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, c who, owner, repo, action string) (*authzGrant, bool) {
	ctx := r.Context()
	if c.job {
		return h.authorizeJob(w, r, c, owner, repo, action)
	}
	if !scopes.Grants(c.principal.Scopes, scopeFor(action)) {
		serveErr(w, errcode.ErrorCodeDenied.WithMessage("il token non ha lo scope "+scopeFor(action)))
		return nil, false
	}
	caller := trust.Identity{UserID: c.principal.UserID, Username: c.principal.Username, Scopes: c.principal.Scopes}
	ref, err := h.backend.ResolveRepo(ctx, caller, owner, repo)
	if err != nil {
		if errors.Is(err, access.ErrNotFound) {
			serveErr(w, errcode.ErrorCodeNameUnknown.WithMessage("repository non trovato"))
			return nil, false
		}
		h.log.Error("risoluzione del repo non riuscita", "err", err)
		serveErr(w, errcode.ErrorCodeUnavailable)
		return nil, false
	}
	if action != actionPull {
		role := "write"
		if action == actionDelete {
			role = "admin"
		}
		ok, err := h.backend.HasRole(ctx, c.principal.UserID, ref.ID, role)
		if err != nil {
			h.log.Error("verifica del ruolo non riuscita", "err", err)
			serveErr(w, errcode.ErrorCodeUnavailable)
			return nil, false
		}
		if !ok {
			serveErr(w, errcode.ErrorCodeDenied.WithMessage("serve il ruolo "+role+" sul repository"))
			return nil, false
		}
		if ref.Archived {
			serveErr(w, errcode.ErrorCodeDenied.WithMessage("repository archiviato: le immagini si scaricano ma non si pubblicano né si eliminano"))
			return nil, false
		}
	}
	return newGrant(ref.ID, action, ref.Owner, ref.Name), true
}

func newGrant(repoID, action, owner, name string) *authzGrant {
	g := &grant{repoID: repoID, actions: map[string]bool{action: true}}
	// Una sessione che pubblica deve poter anche rileggere ciò che pubblica (HEAD dei blob).
	if action != actionPull {
		g.actions[actionPull] = true
	}
	return &authzGrant{grant: g, canonical: canonicalName(owner, name)}
}

func canonicalName(owner, name string) string {
	if owner == "" || name == "" {
		return ""
	}
	return owner + "/" + name
}

func (h *Handler) authorizeJob(w http.ResponseWriter, r *http.Request, c who, owner, repo, action string) (*authzGrant, bool) {
	ja, err := h.backend.JobAccess(r.Context(), c.token, owner, repo)
	switch {
	case err == nil:
	case errors.Is(err, access.ErrInvalidJobToken):
		h.challenge(w)
		return nil, false
	case errors.Is(err, access.ErrNotFound):
		serveErr(w, errcode.ErrorCodeNameUnknown.WithMessage("repository non trovato"))
		return nil, false
	default:
		h.log.Error("verifica del token del job non riuscita", "err", err)
		serveErr(w, errcode.ErrorCodeUnavailable)
		return nil, false
	}
	switch action {
	case actionPull:
		if ja.Packages != "read" && ja.Packages != "write" {
			serveErr(w, errcode.ErrorCodeDenied.WithMessage("il token del job non ha il permesso packages: read"))
			return nil, false
		}
	case actionPush:
		if ja.Packages != "write" {
			serveErr(w, errcode.ErrorCodeDenied.WithMessage("il token del job non ha il permesso packages: write"))
			return nil, false
		}
		if ja.Archived {
			serveErr(w, errcode.ErrorCodeDenied.WithMessage("repository archiviato: le immagini si scaricano ma non si pubblicano"))
			return nil, false
		}
	default:
		serveErr(w, errcode.ErrorCodeDenied.WithMessage("il token del job non elimina immagini"))
		return nil, false
	}
	return newGrant(ja.RepoID, action, ja.Owner, ja.Name), true
}

// resolveFrom dice se il chiamante può leggere il repo sorgente di un montaggio.
func (h *Handler) resolveFrom(ctx context.Context, c who, rt route) (string, bool) {
	if c.job {
		ja, err := h.backend.JobAccess(ctx, c.token, rt.owner, rt.repo)
		if err != nil || (ja.Packages != "read" && ja.Packages != "write") {
			return "", false
		}
		return ja.RepoID, true
	}
	if !scopes.Grants(c.principal.Scopes, ScopeRead) {
		return "", false
	}
	caller := trust.Identity{UserID: c.principal.UserID, Username: c.principal.Username, Scopes: c.principal.Scopes}
	ref, err := h.backend.ResolveRepo(ctx, caller, rt.owner, rt.repo)
	if err != nil {
		return "", false
	}
	return ref.ID, true
}

// rewriteWriter riporta l'id del repo all'indirizzo owner/repo nelle intestazioni
// che distribution costruisce con il nome interno.
type rewriteWriter struct {
	http.ResponseWriter
	from, to string
	wrote    bool
	status   int
}

func (w *rewriteWriter) WriteHeader(code int) {
	if !w.wrote {
		w.wrote = true
		w.status = code
		h := w.Header()
		for _, k := range []string{"Location", "Link"} {
			if vals := h.Values(k); len(vals) > 0 {
				out := make([]string, len(vals))
				for i, v := range vals {
					out[i] = w.rewrite(v)
				}
				h[k] = out
			}
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *rewriteWriter) rewrite(v string) string {
	if u, err := url.Parse(v); err == nil && u.IsAbs() {
		// Con relativeurls distribution non scrive URL assoluti; se capitasse, si lascia com'è.
		return v
	}
	return strings.ReplaceAll(v, w.from, w.to)
}

func (w *rewriteWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *rewriteWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap serve a http.ResponseController.
func (w *rewriteWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
