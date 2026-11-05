package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/fathorMB/GitStack/services/core/internal/databases"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/webhooks"
)

// Collegamenti app-database (v3-d, GIT-552, ADR 0003 DB9-DB12). Permessi: collegare vuole `admin` sull'owner
// del database E `admin` sull'app (stesso owner, repo anche diversi); scollegare e ruotare bastano `admin`
// sull'owner O `admin` sull'app. Le password si vedono solo nella risposta di creazione e rotazione (DB22).

func linkToAPI(l store.ManagedDBLink, app store.ManagedDBAppRef) openapi.DatabaseLink {
	out := openapi.DatabaseLink{Id: openapi_types.UUID(l.ID), Mode: openapi.DatabaseLinkMode(l.Mode), State: openapi.DatabaseLinkState(l.State), CreatedAt: l.CreatedAt}
	out.App.Owner, out.App.Repo, out.App.Name = app.Owner, app.Repo, app.Name
	if l.CreatedBy != nil {
		out.CreatedBy = actorToAPI(l.CreatedBy)
	}
	return out
}

func linkCredentialsToAPI(c databases.LinkCredentials) openapi.DatabaseLinkCredentials {
	return openapi.DatabaseLinkCredentials{Username: c.Username, Password: c.Password, Host: c.Host, Port: openapi.DatabaseLinkCredentialsPort(c.Port),
		Database: c.Database, SslMode: openapi.DatabaseLinkCredentialsSslMode(c.SSLMode), SslRootcert: c.SSLRootCert}
}

// appRefOf legge owner/repo/nome dell'app di un collegamento (vuoti se l'app non c'e' piu').
func (s *apiServer) appRefOf(ctx context.Context, l store.ManagedDBLink) store.ManagedDBAppRef {
	a, err := s.resources.GetAppByID(ctx, l.AppID)
	if err != nil {
		return store.ManagedDBAppRef{}
	}
	return store.ManagedDBAppRef{Owner: a.Owner, Repo: a.RepoName, Name: a.Name}
}

// ListDatabaseLinks implementa GET /databases/{owner}/{name}/links: `admin` sull'owner (DB2).
func (s *apiServer) ListDatabaseLinks(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	d, _, ok := s.databaseFor(w, r, string(owner), string(name))
	if !ok {
		return
	}
	links, err := s.resources.ListManagedDBLinks(r.Context(), d.ID)
	if err != nil {
		appInternalError(w, "elenco dei collegamenti", err)
		return
	}
	out := openapi.DatabaseLinkList{Items: make([]openapi.DatabaseLink, 0, len(links))}
	for _, l := range links {
		out.Items = append(out.Items, linkToAPI(l, s.appRefOf(r.Context(), l)))
	}
	writeJSON(w, http.StatusOK, out)
}

// writeLinkError traduce gli errori di collegamento e rotazione; false se non e' uno di quelli.
func writeLinkError(w http.ResponseWriter, err error) bool {
	var vc *store.DBVariableConflictError
	switch {
	case errors.Is(err, databases.ErrNotReady):
		writeError(w, http.StatusUnprocessableEntity, "database_not_ready", "Il database non e' ancora pronto: riprova quando e' `running`.")
	case errors.Is(err, databases.ErrNoRootCA):
		writeError(w, http.StatusServiceUnavailable, "database_ca_unavailable", "La CA dei database non e' completa su questa installazione: l'amministratore esegue `gitstack databases ensure`.")
	case errors.As(err, &vc):
		writeErrorDetails(w, http.StatusConflict, "variable_conflict", "L'app ha gia' variabili o segreti con questi nomi: rinominali o toglili prima di collegare il database.",
			map[string]any{"names": vc.Names})
	case errors.Is(err, store.ErrManagedDBLinkTaken):
		writeError(w, http.StatusConflict, "already_linked", "L'app e' gia' collegata a questo database.")
	case errors.Is(err, store.ErrManagedDBDeleting):
		writeError(w, http.StatusConflict, "conflict", "Il database e' in eliminazione.")
	case errors.Is(err, databases.ErrLinkUnlinking):
		writeError(w, http.StatusConflict, "link_unlinking", "Il collegamento e' in scollegamento.")
	case errors.Is(err, webhooks.ErrNoKey):
		writeError(w, http.StatusServiceUnavailable, "app_secrets_unavailable", "I segreti non sono configurati su questo server (GITSTACK_WEBHOOK_SECRET_KEY).")
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrManagedDBNotFound), errors.Is(err, store.ErrManagedDBLinkNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Non trovato.")
	default:
		return false
	}
	return true
}

// CreateDatabaseLink implementa POST /databases/{owner}/{name}/links.
func (s *apiServer) CreateDatabaseLink(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam) {
	d, _, ok := s.databaseFor(w, r, string(owner), string(name))
	if !ok {
		return
	}
	var in openapi.DatabaseLinkCreateInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	mode := string(in.Mode)
	if mode != databases.ModeRW && mode != databases.ModeRO {
		writeFieldError(w, "mode", "mode: rw o ro.")
		return
	}
	if in.App.Owner == "" || in.App.Repo == "" || in.App.Name == "" {
		writeFieldError(w, "app", "app: owner, repo e name sono obbligatori.")
		return
	}
	// DB9: l'app deve avere lo stesso owner del database (i repo possono essere diversi).
	if !strings.EqualFold(in.App.Owner, d.OwnerName) {
		writeError(w, http.StatusUnprocessableEntity, "owner_mismatch", "L'app e il database devono avere lo stesso owner.")
		return
	}
	// H5: serve `admin` sull'app (e quindi sul suo repo).
	rp, actor, ok := s.appRepo(w, r, in.App.Owner, in.App.Repo, "admin", true)
	if !ok {
		return
	}
	app, ok := s.appFor(w, r, rp, in.App.Name)
	if !ok {
		return
	}
	if !strings.EqualFold(app.Owner, d.OwnerName) { // l'owner del repo puo' essere stato rinominato dal path richiesto
		writeError(w, http.StatusUnprocessableEntity, "owner_mismatch", "L'app e il database devono avere lo stesso owner.")
		return
	}
	if !s.hooks.Keys.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "app_secrets_unavailable", "I segreti non sono configurati su questo server (GITSTACK_WEBHOOK_SECRET_KEY).")
		return
	}
	link, creds, err := s.databases.Controller.LinkApp(r.Context(), s.hooks.Keys, d, app, mode, actor.store())
	if err != nil {
		if writeLinkError(w, err) {
			return
		}
		appInternalError(w, "collegamento del database", err)
		return
	}
	writeJSON(w, http.StatusCreated, openapi.DatabaseLinkCreated{
		Link:        linkToAPI(link, store.ManagedDBAppRef{Owner: app.Owner, Repo: app.RepoName, Name: app.Name}),
		Credentials: linkCredentialsToAPI(creds),
	})
}

// linkFor risolve database e collegamento per scollegare o ruotare: serve `admin` sull'owner o `admin`
// sull'app (DB2, DB9); chi non ha nessuno dei due riceve 404.
func (s *apiServer) linkFor(w http.ResponseWriter, r *http.Request, ownerParam openapi.RepoOwnerParam, name openapi.DatabaseNameParam, linkID openapi_types.UUID) (store.ManagedDB, store.ManagedDBLink, trustUser, bool) {
	owner, u, ownerAdmin, ok := s.databaseOwnerAccess(w, r, string(ownerParam))
	if !ok {
		return store.ManagedDB{}, store.ManagedDBLink{}, trustUser{}, false
	}
	d, err := s.resources.GetManagedDB(r.Context(), owner.Name, string(name))
	if err == nil {
		var l store.ManagedDBLink
		if l, err = s.resources.GetManagedDBLink(r.Context(), d.ID, linkID); err == nil {
			allowed := ownerAdmin
			if !allowed {
				app, aerr := s.resources.GetAppByID(r.Context(), l.AppID)
				if aerr == nil && s.repoIdentity != nil {
					allowed, aerr = s.repoIdentity.HasRole(r.Context(), u.ID, app.RepoID, "admin")
				}
				if aerr != nil && !errors.Is(aerr, store.ErrNotFound) {
					slog.Default().Warn("verifica del permesso sull'app non riuscita", "err", aerr)
					writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile.")
					return store.ManagedDB{}, store.ManagedDBLink{}, trustUser{}, false
				}
			}
			if !allowed {
				writeError(w, http.StatusNotFound, "not_found", "Non trovato.")
				return store.ManagedDB{}, store.ManagedDBLink{}, trustUser{}, false
			}
			return d, l, u, true
		}
	}
	if errors.Is(err, store.ErrManagedDBNotFound) || errors.Is(err, store.ErrManagedDBLinkNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Non trovato.")
		return store.ManagedDB{}, store.ManagedDBLink{}, trustUser{}, false
	}
	appInternalError(w, "lettura del collegamento", err)
	return store.ManagedDB{}, store.ManagedDBLink{}, trustUser{}, false
}

// DeleteDatabaseLink implementa DELETE /databases/{owner}/{name}/links/{linkId}: 202. L'ordine e' quello
// dell'ADR (DB12); se le sessioni non si chiudono subito il collegamento resta `unlinking` e il controller ritenta.
func (s *apiServer) DeleteDatabaseLink(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, linkId openapi.DatabaseLinkIdParam) {
	d, l, u, ok := s.linkFor(w, r, owner, name, linkId)
	if !ok {
		return
	}
	ref := s.appRefOf(r.Context(), l)
	if _, err := s.databases.Controller.Unlink(r.Context(), d, l, u.actor()); err != nil {
		slog.Default().Error("scollegamento del database non riuscito", "db", d.ID, "link", l.ID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "cluster_unavailable", "Lo scollegamento non e' completo: il collegamento resta `unlinking` e il controller ritenta.")
		return
	}
	l.State = "unlinking"
	writeJSON(w, http.StatusAccepted, linkToAPI(l, ref))
}

// RotateDatabaseLink implementa POST /databases/{owner}/{name}/links/{linkId}/rotate.
func (s *apiServer) RotateDatabaseLink(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.DatabaseNameParam, linkId openapi.DatabaseLinkIdParam) {
	d, l, u, ok := s.linkFor(w, r, owner, name, linkId)
	if !ok {
		return
	}
	if !s.hooks.Keys.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "app_secrets_unavailable", "I segreti non sono configurati su questo server (GITSTACK_WEBHOOK_SECRET_KEY).")
		return
	}
	creds, err := s.databases.Controller.RotateLink(r.Context(), s.hooks.Keys, d, l, u.actor())
	if err != nil {
		if writeLinkError(w, err) {
			return
		}
		appInternalError(w, "rotazione della password del collegamento", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.DatabaseLinkRotated{Link: linkToAPI(l, s.appRefOf(r.Context(), l)), Credentials: linkCredentialsToAPI(creds)})
}
