package appauth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/fathorMB/GitStack/services/gateway/internal/identityclient"
	"github.com/fathorMB/GitStack/services/gateway/internal/middleware"
	"github.com/fathorMB/GitStack/services/gateway/internal/security"
	"github.com/fathorMB/GitStack/services/gateway/internal/trust"
)

// Percorsi del gateway (fuori dal contratto OpenAPI: li usano Traefik e il browser, non i client API).
const (
	PathVerify = "/_appauth/verify"
	PathLogin  = "/_appauth/login"
	PathKeys   = "/.well-known/gitstack-app-keys"

	// CallbackPath è il percorso, sull'host dell'app, con cui il browser torna dal login. Lo serve il
	// forward-auth stesso: non arriva mai all'app.
	CallbackPath = "/_gitstack/callback"

	// AppCookie è il cookie d'app che il gateway rilascia dopo il login. Vale solo sull'host
	// dell'app e non è la sessione GitStack: viene comunque tolto prima di arrivare all'app.
	AppCookie = "gst_app_session"
)

const (
	defaultSessionTTL = 8 * time.Hour
	codeTTL           = time.Minute
	maxCredentialLen  = 512
)

// App è l'app davanti a cui si decide, come l'ha firmata core nell'indirizzo del forward-auth.
type App struct {
	ID     string
	RepoID string
}

// AccessPolicy decide se l'utente può aprire l'app. È l'UNICO punto in cui si decide l'accesso: di
// default `read` sul repo (DefaultAccess); la lista di accesso dei tester (GIT-376) si aggiunge
// qui, come «read sul repo OPPURE membro della lista». Un errore è «non so» e diventa 503: mai
// fail open.
type AccessPolicy func(ctx context.Context, userID string, app App) (bool, error)

// DefaultAccess: passa chi ha `read` sul repo (repo interno → tutti gli utenti attivi, privato →
// chi ha un ruolo, direttamente, via team o come owner/admin) secondo identity.
func DefaultAccess(p identityclient.PermissionChecker) AccessPolicy {
	return func(ctx context.Context, userID string, app App) (bool, error) {
		if p == nil {
			return false, identityclient.ErrUnavailable
		}
		return p.CheckPermission(ctx, userID, app.RepoID, "read")
	}
}

// Config configura il Handler.
type Config struct {
	Verifier    identityclient.Verifier
	Profiles    identityclient.ProfileLookup
	Permissions identityclient.PermissionChecker
	// Access sostituisce DefaultAccess(Permissions) se non nil.
	Access AccessPolicy
	// Secret è il segreto di servizio del gateway (elenco «nuova,vecchia»): verifica la firma
	// dell'indirizzo del forward-auth e firma il cookie d'app e il codice del login.
	Secret string
	// Signer firma le intestazioni. Nil: il login davanti alle app non è configurato e risponde 503.
	Signer *Signer
	// PublicURL è l'indirizzo pubblico di GitStack (es. https://git.acme.it), dove vive /api e il
	// login web; AppsDomain il dominio delle app (H2). Senza uno dei due niente passaggio dal
	// browser: restano i token e un cookie di sessione che arrivi all'host dell'app.
	PublicURL  string
	AppsDomain string
	SessionTTL time.Duration
	Now        func() time.Time
	Logger     *slog.Logger
}

// Handler serve il forward-auth, il passaggio dal login e la pubblicazione delle chiavi.
type Handler struct {
	cfg    Config
	access AccessPolicy
	apps   *regexp.Regexp
}

// New costruisce il Handler.
func New(cfg Config) *Handler {
	h := &Handler{cfg: cfg, access: cfg.Access}
	if h.access == nil {
		h.access = DefaultAccess(cfg.Permissions)
	}
	if cfg.SessionTTL <= 0 {
		h.cfg.SessionTTL = defaultSessionTTL
	}
	if cfg.Now == nil {
		h.cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		h.cfg.Logger = slog.Default()
	}
	h.cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	h.cfg.AppsDomain = strings.ToLower(strings.Trim(cfg.AppsDomain, "."))
	if h.cfg.AppsDomain != "" {
		h.apps = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.` + regexp.QuoteMeta(h.cfg.AppsDomain) + `(:[0-9]{1,5})?$`)
	}
	return h
}

// ServeKeys pubblica le chiavi pubbliche di verifica (GET /.well-known/gitstack-app-keys).
func (h *Handler) ServeKeys(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Signer == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": h.cfg.Signer.PublicKeys()})
}

func (h *Handler) deny(w http.ResponseWriter, status int, reason, msg string) {
	h.cfg.Logger.Info("appauth: richiesta negata", "reason", reason, "status", status)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(msg + "\n"))
}

// ServeVerify è il forward-auth: 2xx con le intestazioni per l'app, altrimenti il rifiuto o il
// redirect che Traefik inoltra al client.
func (h *Handler) ServeVerify(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Signer == nil || h.cfg.Secret == "" {
		h.deny(w, http.StatusServiceUnavailable, "not_configured", "Il login davanti alle app non è configurato.")
		return
	}
	app, ok := h.appFromQuery(r)
	if !ok {
		h.deny(w, http.StatusForbidden, "bad_signature", "Richiesta non valida.")
		return
	}
	host := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Host")))
	if host == "" || (h.apps != nil && !h.apps.MatchString(host)) {
		h.deny(w, http.StatusForbidden, "bad_host", "Indirizzo non valido.")
		return
	}
	method := r.Header.Get("X-Forwarded-Method")
	if method == "" {
		method = r.Method
	}
	rawURI := r.Header.Get("X-Forwarded-Uri")
	if rawURI == "" || rawURI[0] != '/' {
		rawURI = "/"
	}
	path, query, _ := strings.Cut(rawURI, "?")
	scheme := "https"
	if p := r.Header.Get("X-Forwarded-Proto"); p == "http" {
		scheme = "http"
	}

	if path == CallbackPath && method == http.MethodGet {
		h.callback(w, r, app, host, scheme, query)
		return
	}

	// Chi è: un token con read (agenti), la sessione GitStack o il cookie d'app.
	userID, status, reason := h.identify(r, host)
	if userID == "" {
		if status == http.StatusUnauthorized && h.browserNavigation(r, method) && h.cfg.PublicURL != "" && h.cfg.AppsDomain != "" {
			h.cfg.Logger.Info("appauth: login richiesto", "reason", reason)
			loginURL := h.cfg.PublicURL + "/api" + PathLogin + "?" + url.Values{"host": {host}, "rd": {rawURI}}.Encode()
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, loginURL, http.StatusFound)
			return
		}
		h.deny(w, status, reason, "Serve il login GitStack.")
		return
	}

	prof, ok := h.profile(w, r, userID)
	if !ok {
		return
	}
	allowed, err := h.access(r.Context(), userID, app)
	if err != nil {
		h.cfg.Logger.Error("appauth: verifica dell'accesso non riuscita", "err", err.Error())
		h.deny(w, http.StatusServiceUnavailable, "identity_unavailable", "Il servizio identity non ha risposto.")
		return
	}
	if !allowed {
		h.deny(w, http.StatusForbidden, "no_read", "Accesso negato: serve il permesso read sul repo dell'app.")
		return
	}

	h.writeSigned(w, r, prof, method, host, path)
}

// appFromQuery legge app e repo dall'indirizzo del forward-auth e ne verifica la firma HMAC con
// tutte le chiavi del segreto (rotazione).
func (h *Handler) appFromQuery(r *http.Request) (App, bool) {
	q := r.URL.Query()
	app, repo, sig := q.Get("app"), q.Get("repo"), q.Get("sig")
	if !isUUID(app) || !isUUID(repo) || sig == "" {
		return App{}, false
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return App{}, false
	}
	for _, key := range trust.ParseKeys(h.cfg.Secret) {
		m := hmac.New(sha256.New, []byte(key))
		m.Write([]byte("gitstack-appauth-v1\n" + app + "\n" + repo))
		if hmac.Equal(got, m.Sum(nil)) {
			return App{ID: app, RepoID: repo}, true
		}
	}
	return App{}, false
}

var uuidRE = regexp.MustCompile("^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")

func isUUID(s string) bool { return uuidRE.MatchString(s) }

func (h *Handler) browserNavigation(r *http.Request, method string) bool {
	return method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html")
}

// identify ricava l'utente dalla richiesta originale. Ritorna "" con lo stato e il motivo del rifiuto.
func (h *Handler) identify(r *http.Request, host string) (userID string, status int, reason string) {
	if h.cfg.Verifier == nil {
		return "", http.StatusServiceUnavailable, "identity_unavailable"
	}
	// Token d'agente (o sessione, se il cookie di GitStack arriva fin qui).
	if cred, kind := middleware.CredentialFrom(r); cred != "" {
		if len(cred) > maxCredentialLen {
			return "", http.StatusUnauthorized, "credential_too_long"
		}
		res, err := h.cfg.Verifier.Verify(r.Context(), cred, kind)
		if err != nil {
			h.cfg.Logger.Error("appauth: verifica della credenziale non riuscita", "err", err.Error())
			return "", http.StatusServiceUnavailable, "identity_unavailable"
		}
		if !res.Active {
			return "", http.StatusUnauthorized, "credential_inactive"
		}
		p := res.Principal
		if p.MustChangePassword || p.MustEnrollTotp {
			return "", http.StatusForbidden, "account_setup_required"
		}
		// Un token vale se ha `read`: uno scope più stretto (es. solo write:issues) non basta.
		if p.IsToken && len(security.Missing(p.Scopes, []string{"read:resource"})) > 0 {
			return "", http.StatusForbidden, "insufficient_scope"
		}
		return p.UserID, 0, ""
	}
	if c, err := r.Cookie(AppCookie); err == nil && c.Value != "" {
		if uid, ok := h.openToken(c.Value, tokenSession, host); ok {
			return uid, 0, ""
		}
		return "", http.StatusUnauthorized, "app_session_invalid"
	}
	return "", http.StatusUnauthorized, "missing_credential"
}

func (h *Handler) profile(w http.ResponseWriter, r *http.Request, userID string) (identityclient.Profile, bool) {
	if h.cfg.Profiles == nil {
		h.deny(w, http.StatusServiceUnavailable, "identity_unavailable", "Il servizio identity non è configurato.")
		return identityclient.Profile{}, false
	}
	prof, err := h.cfg.Profiles.LookupProfile(r.Context(), userID)
	if err != nil {
		h.cfg.Logger.Error("appauth: lettura del profilo non riuscita", "err", err.Error())
		h.deny(w, http.StatusServiceUnavailable, "identity_unavailable", "Il servizio identity non ha risposto.")
		return identityclient.Profile{}, false
	}
	if !prof.Active {
		h.deny(w, http.StatusForbidden, "user_inactive", "Utente disattivato.")
		return identityclient.Profile{}, false
	}
	return prof, true
}

// writeSigned scrive le intestazioni firmate e le credenziali ripulite, poi 200.
func (h *Handler) writeSigned(w http.ResponseWriter, r *http.Request, prof identityclient.Profile, method, host, path string) {
	in := Signed{
		Method: method, Host: host, Path: path,
		AuthTime: strconv.FormatInt(h.cfg.Now().Unix(), 10),
		User:     safe(prof.Username), Email: safe(prof.Email), Teams: safe(strings.Join(prof.Teams, ",")),
	}
	kid, sig := h.cfg.Signer.Sign(in.Message())
	hd := w.Header()
	hd.Set(HeaderUser, in.User)
	hd.Set(HeaderEmail, in.Email)
	hd.Set(HeaderTeams, in.Teams)
	hd.Set(HeaderAuthTime, in.AuthTime)
	hd.Set(HeaderKeyID, kid)
	hd.Set(HeaderSignature, sig)
	// Traefik sostituisce Cookie e Authorization della richiesta con questi (e, se qui mancano, li
	// toglie): l'app non vede né la sessione GitStack né il token.
	if c := stripCookies(r.Header.Get("Cookie")); c != "" {
		hd.Set("Cookie", c)
	}
	if a := r.Header.Get("Authorization"); a != "" && !isGitStackBearer(a) {
		hd.Set("Authorization", a)
	}
	hd.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

// safe toglie i caratteri di controllo e gli spazi ai bordi: il valore resta uguale a quello che
// l'app leggerà dall'intestazione, e quindi la firma torna.
func safe(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
}

func isGitStackBearer(h string) bool {
	scheme, value, ok := strings.Cut(h, " ")
	return ok && strings.EqualFold(scheme, "Bearer") && strings.HasPrefix(strings.TrimSpace(value), "gst_")
}

// gitstackCookies sono i cookie che non devono mai arrivare all'app.
var gitstackCookies = map[string]bool{
	middleware.SessionCookie: true, middleware.HostSessionCookie: true, AppCookie: true,
}

// stripCookies toglie dall'intestazione Cookie la sessione GitStack e il cookie d'app, lasciando
// intatti (e nello stesso ordine) i cookie dell'app.
func stripCookies(header string) string {
	if header == "" {
		return ""
	}
	var keep []string
	for _, part := range strings.Split(header, ";") {
		name, _, _ := strings.Cut(strings.TrimSpace(part), "=")
		if part = strings.TrimSpace(part); part != "" && !gitstackCookies[name] {
			keep = append(keep, part)
		}
	}
	return strings.Join(keep, "; ")
}

// ---- passaggio dal login GitStack ----

// ServeLogin (GET /_appauth/login?host=&rd=) gira sull'host di GitStack: chi ha la sessione riceve
// un codice di breve durata legato all'host dell'app e torna lì. Il permesso non si decide qui
// (non sa quale repo): lo decide il forward-auth al ritorno.
func (h *Handler) ServeLogin(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(r.URL.Query().Get("host"))
	rd := r.URL.Query().Get("rd")
	if h.cfg.Signer == nil || h.apps == nil || !h.apps.MatchString(host) || !safeRedirect(rd) {
		h.deny(w, http.StatusBadRequest, "bad_login_request", "Richiesta non valida.")
		return
	}
	loginPage := h.cfg.PublicURL + "/login"
	var sess string
	for _, name := range []string{middleware.HostSessionCookie, middleware.SessionCookie} {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			sess = c.Value
			break
		}
	}
	if sess == "" || h.cfg.Verifier == nil || len(sess) > maxCredentialLen {
		http.Redirect(w, r, loginPage, http.StatusFound)
		return
	}
	res, err := h.cfg.Verifier.Verify(r.Context(), sess, identityclient.KindSession)
	if err != nil {
		h.cfg.Logger.Error("appauth: verifica della sessione non riuscita", "err", err.Error())
		h.deny(w, http.StatusServiceUnavailable, "identity_unavailable", "Il servizio identity non ha risposto.")
		return
	}
	if !res.Active || res.Principal.MustChangePassword || res.Principal.MustEnrollTotp {
		http.Redirect(w, r, loginPage, http.StatusFound)
		return
	}
	code := h.sealToken(tokenCode, res.Principal.UserID, host, codeTTL)
	dest := "https://" + host + CallbackPath + "?" + url.Values{"code": {code}, "rd": {rd}}.Encode()
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, dest, http.StatusFound)
}

// callback chiude il passaggio: sull'host dell'app, con il codice, rilascia il cookie d'app e
// rimanda alla pagina richiesta. Passa dal forward-auth, quindi arriva con app e repo firmati.
func (h *Handler) callback(w http.ResponseWriter, r *http.Request, app App, host, scheme, rawQuery string) {
	q, _ := url.ParseQuery(rawQuery)
	rd := q.Get("rd")
	uid, ok := h.openToken(q.Get("code"), tokenCode, host)
	if !ok || !safeRedirect(rd) {
		h.deny(w, http.StatusForbidden, "bad_code", "Codice di accesso non valido o scaduto: riapri l'app.")
		return
	}
	// L'accesso si decide anche qui: un utente senza read non riceve nemmeno il cookie.
	if _, ok := h.profile(w, r, uid); !ok {
		return
	}
	allowed, err := h.access(r.Context(), uid, app)
	if err != nil {
		h.deny(w, http.StatusServiceUnavailable, "identity_unavailable", "Il servizio identity non ha risposto.")
		return
	}
	if !allowed {
		h.deny(w, http.StatusForbidden, "no_read", "Accesso negato: serve il permesso read sul repo dell'app.")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: AppCookie, Value: h.sealToken(tokenSession, uid, host, h.cfg.SessionTTL), Path: "/",
		MaxAge: int(h.cfg.SessionTTL / time.Second), HttpOnly: true, Secure: scheme == "https", SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, rd, http.StatusFound)
}

// safeRedirect: solo un percorso relativo dell'host (niente // né schemi), contro l'open redirect.
func safeRedirect(rd string) bool {
	if rd == "" || rd[0] != '/' || strings.HasPrefix(rd, "//") || strings.HasPrefix(rd, "/\\") || len(rd) > 2048 {
		return false
	}
	for _, c := range rd {
		if c < 0x20 || c == 0x7f || c == '\\' {
			return false
		}
	}
	return true
}

// ---- token firmati con HMAC (codice del login e cookie d'app) ----

type tokenKind string

const (
	tokenCode    tokenKind = "code"
	tokenSession tokenKind = "session"
)

type tokenPayload struct {
	Kind string `json:"k"`
	User string `json:"u"`
	Host string `json:"h"`
	Exp  int64  `json:"e"`
}

func (h *Handler) macOf(key string, payload []byte) []byte {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte("gitstack-appauth-token-v1\n"))
	m.Write(payload)
	return m.Sum(nil)
}

// sealToken: base64url(payload) "." base64url(hmac), con la prima chiave del segreto.
func (h *Handler) sealToken(kind tokenKind, user, host string, ttl time.Duration) string {
	payload, _ := json.Marshal(tokenPayload{Kind: string(kind), User: user, Host: host, Exp: h.cfg.Now().Add(ttl).Unix()})
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(h.macOf(trust.SigningKey(h.cfg.Secret), payload))
}

// openToken verifica firma (con tutte le chiavi), tipo, host e scadenza.
func (h *Handler) openToken(tok string, kind tokenKind, host string) (string, bool) {
	if len(tok) > 1024 {
		return "", false
	}
	p, m, ok := strings.Cut(tok, ".")
	if !ok {
		return "", false
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(p)
	mac, err2 := base64.RawURLEncoding.DecodeString(m)
	if err1 != nil || err2 != nil {
		return "", false
	}
	valid := false
	for _, key := range trust.ParseKeys(h.cfg.Secret) {
		if subtle.ConstantTimeCompare(mac, h.macOf(key, payload)) == 1 {
			valid = true
		}
	}
	if !valid {
		return "", false
	}
	var tp tokenPayload
	if err := json.Unmarshal(payload, &tp); err != nil {
		return "", false
	}
	if tp.Kind != string(kind) || tp.Host != host || tp.User == "" || h.cfg.Now().Unix() >= tp.Exp {
		return "", false
	}
	return tp.User, true
}
