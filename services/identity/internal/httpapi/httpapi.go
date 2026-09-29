// Package httpapi implementa le operazioni HTTP dei tag auth e users del
// contratto (api/openapi.yaml) sopra la ServerInterface generata in
// internal/openapi. Il gateway espone /v1/* e toglie il prefisso prima di
// instradare qui (come per core): questo handler serve percorsi senza /v1.
//
// Autenticazione: solo il cookie di sessione gst_session (i token personali
// sono di un altro item). Autorizzazione: admin per creare ed eliminare
// utenti; admin o l'utente stesso per aggiornare il profilo e cambiare la
// password; qualunque utente autenticato per leggere. Errori nel formato
// Error del contratto; 500 senza dettagli interni.
//
// L'IP per il rate limit del login è r.RemoteAddr (host senza porta), mai
// X-Forwarded-For (falsificabile dal client). Se non è determinabile resta
// vuoto e vale solo il limite per utente.
package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/fathorMB/GitStack/services/identity/internal/apitokens"
	"github.com/fathorMB/GitStack/services/identity/internal/auth"
	"github.com/fathorMB/GitStack/services/identity/internal/openapi"
	"github.com/fathorMB/GitStack/services/identity/internal/userkeys"
	"github.com/fathorMB/GitStack/services/identity/internal/users"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const maxBody = 1 << 20

// New assembla l'handler HTTP di identity per i tag auth e users.
// logger nil usa slog.Default().
func New(a *auth.Service, logger *slog.Logger, opts ...Option) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	s := &server{auth: a, users: a.Users, log: logger}
	for _, o := range opts {
		o(s)
	}
	return openapi.HandlerWithOptions(s, openapi.StdHTTPServerOptions{
		BaseRouter:  http.NewServeMux(),
		Middlewares: []openapi.MiddlewareFunc{s.serviceAuth},
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeError(w, http.StatusBadRequest, "bad_request", "Richiesta non valida.")
		},
	})
}

type server struct {
	auth          *auth.Service
	users         *users.Service
	log           *slog.Logger
	tokens        *apitokens.Service
	keys          *userkeys.Service
	serviceSecret string
}

var _ openapi.ServerInterface = (*server)(nil)

// ---- risposte ----

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErrorDetails(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	var body openapi.Error
	body.Error.Code = code
	body.Error.Message = message
	if details != nil {
		body.Error.Details = &details
	}
	writeJSON(w, status, body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeErrorDetails(w, status, code, message, nil)
}

func (s *server) internal(w http.ResponseWriter, r *http.Request, err error) {
	// Si logga solo l'errore (mai corpi di richiesta, password o cookie).
	s.log.Error("errore interno", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno.")
}

func unauthenticated(w http.ResponseWriter) {
	writeError(w, http.StatusUnauthorized, "unauthenticated", "Autenticazione richiesta.")
}

func forbidden(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, "forbidden", "Permesso negato.")
}

func validationFailed(w http.ResponseWriter, fields map[string]string) {
	f := make(map[string]any, len(fields))
	for k, v := range fields {
		f[k] = v
	}
	writeErrorDetails(w, http.StatusUnprocessableEntity, "validation_failed", "Dati non validi.", map[string]any{"fields": f})
}

// decode legge il corpo JSON (max 1 MiB, campi sconosciuti vietati come da
// additionalProperties: false). Ritorna anche i byte, per chi deve
// distinguere null da assente.
func decode(w http.ResponseWriter, r *http.Request, dst any) ([]byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Corpo della richiesta non leggibile.")
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil || dec.More() {
		writeError(w, http.StatusBadRequest, "bad_request", "Corpo della richiesta non valido.")
		return nil, false
	}
	return raw, true
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	if _, err := net.ResolveIPAddr("ip", host); err != nil || net.ParseIP(host) == nil {
		return ""
	}
	return host
}

// current risolve il chiamante dal cookie; se manca scrive 401.
func (s *server) current(w http.ResponseWriter, r *http.Request) (auth.Current, bool) {
	c, err := r.Cookie(auth.CookieName)
	if err != nil {
		unauthenticated(w)
		return auth.Current{}, false
	}
	cur, err := s.auth.Session(r.Context(), c.Value)
	if errors.Is(err, auth.ErrUnauthenticated) {
		unauthenticated(w)
		return auth.Current{}, false
	}
	if err != nil {
		s.internal(w, r, err)
		return auth.Current{}, false
	}
	return cur, true
}

// ---- conversioni ----

func toUser(u users.User, full bool) openapi.User {
	id := openapi_types.UUID(u.ID)
	bio := u.Bio
	out := openapi.User{
		Id: &id, Username: u.Username, Kind: openapi.UserKind(u.Kind),
		DisplayName: u.DisplayName, Bio: &bio, AvatarUrl: u.AvatarURL,
	}
	if full {
		out.IsAdmin, out.IsActive = &u.IsAdmin, &u.IsActive
		ca := u.CreatedAt
		out.CreatedAt = &ca
		if u.Email != nil {
			e := openapi_types.Email(*u.Email)
			out.Email = &e
		}
	}
	return out
}

func visible(viewer users.User, target users.User) bool {
	return viewer.IsAdmin || viewer.ID == target.ID
}

// ---- auth ----

func (s *server) Login(w http.ResponseWriter, r *http.Request) {
	var in openapi.LoginInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	f := map[string]string{}
	if n := utf8.RuneCountInString(in.Username); n < 1 || n > 254 {
		f["username"] = "da 1 a 254 caratteri"
	}
	if in.Password == nil || *in.Password == "" || utf8.RuneCountInString(*in.Password) > 1024 {
		f["password"] = "da 1 a 1024 caratteri"
	}
	if len(f) > 0 {
		validationFailed(w, f)
		return
	}
	res, err := s.auth.Login(r.Context(), auth.LoginInput{
		Login: in.Username, Password: *in.Password, IP: clientIP(r), UserAgent: r.UserAgent(),
	})
	var rl *auth.RateLimitedError
	switch {
	case errors.As(err, &rl):
		secs := int((rl.RetryAfter + 999_999_999) / 1_000_000_000) // per eccesso
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", "Troppi tentativi di accesso: riprova più tardi.")
		return
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Credenziali non valide.")
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	http.SetCookie(w, auth.SessionCookie(res.SessionValue, res.Session.ExpiresAt))
	exp := res.Session.ExpiresAt
	writeJSON(w, http.StatusOK, openapi.CurrentSession{
		User: toUser(res.User, true), AuthMethod: openapi.CurrentSessionAuthMethod("password"), ExpiresAt: &exp,
	})
}

func (s *server) Logout(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(auth.CookieName)
	if err != nil {
		unauthenticated(w)
		return
	}
	err = s.auth.Logout(r.Context(), c.Value)
	if errors.Is(err, auth.ErrUnauthenticated) {
		unauthenticated(w)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	http.SetCookie(w, auth.ClearCookie())
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) GetCurrentSession(w http.ResponseWriter, r *http.Request) {
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	exp := cur.Session.ExpiresAt
	writeJSON(w, http.StatusOK, openapi.CurrentSession{
		User: toUser(cur.User, true), AuthMethod: openapi.CurrentSessionAuthMethod(cur.Session.AuthMethod), ExpiresAt: &exp,
	})
}

func notImplemented(w http.ResponseWriter) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Login OIDC non ancora disponibile.")
}

func (s *server) ListOidcProviders(w http.ResponseWriter, r *http.Request) { notImplemented(w) }
func (s *server) StartOidcLogin(w http.ResponseWriter, r *http.Request, _ openapi.OidcProviderParam, _ openapi.StartOidcLoginParams) {
	notImplemented(w)
}
func (s *server) FinishOidcLogin(w http.ResponseWriter, r *http.Request, _ openapi.OidcProviderParam, _ openapi.FinishOidcLoginParams) {
	notImplemented(w)
}

// ---- users ----

func (s *server) ListUsers(w http.ResponseWriter, r *http.Request, p openapi.ListUsersParams) {
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	page, perPage, q := 1, 20, ""
	if p.Page != nil {
		page = *p.Page
	}
	if p.PerPage != nil {
		perPage = *p.PerPage
	}
	if p.Q != nil {
		q = *p.Q
		if n := utf8.RuneCountInString(q); n < 1 || n > 64 {
			writeError(w, http.StatusBadRequest, "bad_request", "q deve avere da 1 a 64 caratteri.")
			return
		}
	}
	if page < 1 || perPage < 1 || perPage > 100 {
		writeError(w, http.StatusBadRequest, "bad_request", "page >= 1 e perPage tra 1 e 100.")
		return
	}
	list, total, err := s.users.List(r.Context(), q, page, perPage)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	items := make([]openapi.User, 0, len(list))
	for _, u := range list {
		items = append(items, toUser(u, visible(cur.User, u)))
	}
	writeJSON(w, http.StatusOK, openapi.UserList{Items: items, Page: page, PerPage: perPage, Total: total})
}

func (s *server) CreateUser(w http.ResponseWriter, r *http.Request) {
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	if !cur.User.IsAdmin {
		forbidden(w)
		return
	}
	var in openapi.CreateUserInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	ci := users.CreateInput{Username: in.Username}
	if in.Kind != nil {
		ci.Kind = string(*in.Kind)
	}
	if in.Email != nil {
		ci.Email = string(*in.Email)
	}
	if in.DisplayName != nil {
		ci.DisplayName = *in.DisplayName
	}
	if in.Password != nil {
		ci.Password = *in.Password
	}
	if in.IsAdmin != nil {
		ci.IsAdmin = *in.IsAdmin
	}
	u, err := s.users.Create(r.Context(), ci)
	if !s.userError(w, r, err) {
		return
	}
	w.Header().Set("Location", "/v1/users/"+u.Username)
	writeJSON(w, http.StatusCreated, toUser(u, true))
}

func (s *server) GetUser(w http.ResponseWriter, r *http.Request, username openapi.UsernameParam) {
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	u, err := s.users.Get(r.Context(), username)
	if !s.userError(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, toUser(u, visible(cur.User, u)))
}

func (s *server) UpdateUser(w http.ResponseWriter, r *http.Request, username openapi.UsernameParam) {
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	// Chi non è admin né l'utente stesso non deve nemmeno sapere se esiste.
	if !cur.User.IsAdmin && cur.User.Username != username {
		forbidden(w)
		return
	}
	var in openapi.UpdateUserInput
	raw, ok := decode(w, r, &in)
	if !ok {
		return
	}
	var present map[string]json.RawMessage
	_ = json.Unmarshal(raw, &present)
	if len(present) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "Serve almeno un campo da aggiornare.")
		return
	}
	if !cur.User.IsAdmin && (in.IsAdmin != nil || in.IsActive != nil) {
		forbidden(w)
		return
	}
	ui := users.UpdateInput{DisplayName: in.DisplayName, Bio: in.Bio, AvatarURL: in.AvatarUrl, IsAdmin: in.IsAdmin, IsActive: in.IsActive}
	if v, ok := present["avatarUrl"]; ok && string(bytes.TrimSpace(v)) == "null" {
		empty := ""
		ui.AvatarURL = &empty
	}
	if in.Email != nil {
		e := string(*in.Email)
		ui.Email = &e
	}
	u, err := s.users.Update(r.Context(), username, ui)
	if !s.userError(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, toUser(u, true))
}

func (s *server) DeleteUser(w http.ResponseWriter, r *http.Request, username openapi.UsernameParam) {
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	if !cur.User.IsAdmin {
		forbidden(w)
		return
	}
	if !s.userError(w, r, s.users.Delete(r.Context(), username)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) ChangePassword(w http.ResponseWriter, r *http.Request, username openapi.UsernameParam) {
	cur, ok := s.current(w, r)
	if !ok {
		return
	}
	self := cur.User.Username == username
	if !self && !cur.User.IsAdmin {
		forbidden(w)
		return
	}
	var in openapi.ChangePasswordInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	if in.NewPassword == nil {
		validationFailed(w, map[string]string{"newPassword": "obbligatoria"})
		return
	}
	ci := users.ChangePasswordInput{
		Username: username, NewPassword: *in.NewPassword,
		// Un amministratore non deve dare la password attuale di un altro
		// utente; sulla propria sì, come tutti.
		Admin: cur.User.IsAdmin && !self,
	}
	if in.CurrentPassword != nil {
		ci.CurrentPassword = *in.CurrentPassword
	}
	if self {
		id := cur.Session.ID
		ci.KeepSessionID = &id // "revoca le altre sessioni"
	}
	err := s.users.ChangePassword(r.Context(), ci)
	if errors.Is(err, users.ErrInvalidCredentials) {
		// 403 e non 401: il chiamante è già autenticato (decisione del CTO).
		writeError(w, http.StatusForbidden, "forbidden", "Password attuale errata.")
		return
	}
	if !s.userError(w, r, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// userError mappa gli errori di dominio; ritorna true se err è nil (si può
// proseguire), altrimenti ha già risposto.
func (s *server) userError(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return true
	}
	var ve *users.ValidationError
	var ae *users.AlreadyExistsError
	switch {
	case errors.As(err, &ve):
		validationFailed(w, ve.Fields)
	case errors.As(err, &ae):
		writeErrorDetails(w, http.StatusConflict, "already_exists", ae.Field+" già in uso.", map[string]any{"field": ae.Field})
	case errors.Is(err, users.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Utente non trovato.")
	case errors.Is(err, users.ErrLastAdmin):
		writeError(w, http.StatusConflict, "last_admin", "Non si può togliere l'ultimo amministratore.")
	default:
		s.internal(w, r, err)
	}
	return false
}
