package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/fathorMB/GitStack/pkg/names"
	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/google/uuid"
)

// Operazioni del tag `repos` (M-03, GIT-67): creazione, lettura, elenco,
// impostazioni e archiviazione. Eliminazione e ripristino: repos_trash.go.
// Regole: R1-R12 di repository-git.md; contratto e scelte di
// permesso in docs/repos.md.

const (
	defaultBranchName   = "main"
	maxDescriptionRunes = 1024
	maxBranchNameLen    = 255
	// emptyLookupWorkers: chiamate parallele a git per il campo `empty` di un
	// elenco.
	emptyLookupWorkers = 8
)

// CloneConfig è la configurazione dell'installazione da cui si compongono
// gli indirizzi di clone (R1, R7).
type CloneConfig struct {
	// PublicURL è la base HTTPS pubblica (es. https://git.example.com).
	PublicURL string
	// SSHHost è l'host SSH; vuoto = l'host di PublicURL.
	SSHHost string
	// SSHPort è la porta SSH; 0 = 2222 (R7).
	SSHPort int
}

func (c CloneConfig) sshPort() int {
	if c.SSHPort == 0 {
		return 2222
	}
	return c.SSHPort
}

// publicBase è la base pubblica dell'installazione: PublicURL se impostata;
// altrimenti si ricava dalla richiesta (X-Forwarded-Proto e X-Forwarded-Host
// scritti dal gateway, poi r.Host con http).
func (c CloneConfig) publicBase(r *http.Request) string {
	if c.PublicURL != "" {
		return strings.TrimRight(c.PublicURL, "/")
	}
	first := func(v string) string {
		v, _, _ = strings.Cut(v, ",")
		return strings.TrimSpace(v)
	}
	scheme, host := "http", ""
	if r != nil {
		if p := first(r.Header.Get("X-Forwarded-Proto")); p == "http" || p == "https" {
			scheme = p
		}
		if host = first(r.Header.Get("X-Forwarded-Host")); host == "" {
			host = r.Host
		}
	}
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host
}

func (c CloneConfig) sshHost(r *http.Request) string {
	if c.SSHHost != "" {
		return c.SSHHost
	}
	if u, err := url.Parse(c.publicBase(r)); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return "localhost"
}

// urls compone gli indirizzi di clone. SSH è sempre l'indirizzo completo
// `ssh://git@host:porta/owner/repo.git`; la forma corta `git@host:owner/repo.git`
// vale solo con la porta 22 (R7) e solo allora è valorizzata.
func (c CloneConfig) urls(r *http.Request, owner, name string) openapi.RepoCloneUrls {
	path := owner + "/" + name + ".git"
	host, port := c.sshHost(r), c.sshPort()
	out := openapi.RepoCloneUrls{
		Https: c.publicBase(r) + "/" + path,
		Ssh:   fmt.Sprintf("ssh://git@%s:%d/%s", hostForURL(host), port, path),
	}
	if port == 22 {
		short := "git@" + host + ":" + path
		out.SshShort = &short
	}
	return out
}

// hostForURL mette fra parentesi un indirizzo IPv6.
func hostForURL(h string) string {
	if strings.Contains(h, ":") && !strings.HasPrefix(h, "[") {
		return "[" + h + "]"
	}
	return h
}

// callerIdentity legge l'identità firmata dal gateway; senza, scrive 401.
func callerIdentity(w http.ResponseWriter, r *http.Request) (trust.Identity, uuid.UUID, bool) {
	caller, ok := trust.FromContext(r.Context())
	id, err := uuid.Parse(caller.UserID)
	if !ok || err != nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "Identità del chiamante assente o non valida.")
		return trust.Identity{}, uuid.Nil, false
	}
	return caller, id, true
}

// reposReady verifica che identity e git siano configurati: senza, i repo
// rispondono 503 (mai un repo senza permessi o senza storage).
func (s *apiServer) reposReady(w http.ResponseWriter) bool {
	if s.repoIdentity == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile applicare i permessi sui repo.")
		return false
	}
	if s.git == nil {
		writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Servizio git non configurato.")
		return false
	}
	return true
}

func (s *apiServer) toAPIRepo(req *http.Request, r store.Repo, empty bool) openapi.Repository {
	id := openapi_types.UUID(r.ID)
	createdAt, updatedAt := r.CreatedAt, r.UpdatedAt
	return openapi.Repository{
		Id:                   &id,
		Owner:                openapi.RepoOwner{Type: openapi.OwnerType(r.OwnerType), Name: r.OwnerName},
		Name:                 r.Name,
		FullName:             r.OwnerName + "/" + r.Name,
		Description:          r.Description,
		Visibility:           openapi.RepoVisibility(r.Visibility),
		DefaultBranch:        r.DefaultBranch,
		ProtectDefaultBranch: r.ProtectDefaultBranch,
		Archived:             r.ArchivedAt != nil,
		ArchivedAt:           r.ArchivedAt,
		Empty:                empty,
		CloneUrls:            s.clone.urls(req, r.OwnerName, r.Name),
		CreatedAt:            &createdAt,
		UpdatedAt:            &updatedAt,
	}
}

// isEmpty chiede a git se il repo è vuoto. Se git non risponde il campo
// ripiega su false (i metadati restano leggibili) e lo si logga.
func (s *apiServer) isEmpty(ctx context.Context, caller trust.Identity, id uuid.UUID) bool {
	if s.git == nil {
		return false
	}
	st, err := s.git.Get(ctx, caller, id)
	if err != nil {
		slog.Default().Warn("stato del repo non leggibile da git", "repo_id", id, "err", err)
		return false
	}
	return st.Empty
}

// lookupReadable trova owner/repo e verifica la lettura. Un repo inesistente
// e uno non leggibile danno lo stesso 404, per non rivelarne l'esistenza.
// Ritorna ok=false dopo aver già risposto.
func (s *apiServer) lookupReadable(w http.ResponseWriter, r *http.Request, userID uuid.UUID, owner, name string) (store.Repo, bool) {
	repo, err := s.resources.GetRepoByName(r.Context(), owner, name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Repo non trovato.")
			return store.Repo{}, false
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante la lettura del repo.")
		return store.Repo{}, false
	}
	can, err := s.repoIdentity.HasRole(r.Context(), userID, repo.ID, "read")
	if err != nil {
		slog.Default().Warn("verifica del permesso di lettura non riuscita", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile verificare i permessi.")
		return store.Repo{}, false
	}
	if !can {
		writeError(w, http.StatusNotFound, "not_found", "Repo non trovato.")
		return store.Repo{}, false
	}
	return repo, true
}

// ListRepositories implementa GET /repos: i repo che il chiamante può leggere
// (readable-resources di identity), esclusi gli eliminati.
func (s *apiServer) ListRepositories(w http.ResponseWriter, r *http.Request, params openapi.ListRepositoriesParams) {
	page, perPage := defaultPage, defaultPerPage
	if params.Page != nil {
		page = *params.Page
	}
	if params.PerPage != nil {
		perPage = *params.PerPage
	}
	if page < 1 {
		writeError(w, http.StatusBadRequest, "invalid_page", "page deve essere >= 1.")
		return
	}
	if perPage < 1 || perPage > maxPerPage {
		writeError(w, http.StatusBadRequest, "invalid_per_page", "perPage deve essere tra 1 e 100.")
		return
	}
	caller, userID, ok := callerIdentity(w, r)
	if !ok {
		return
	}
	if s.repoIdentity == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile filtrare i repo per permesso.")
		return
	}
	all, ids, err := s.repoIdentity.ReadableResources(r.Context(), userID)
	if err != nil {
		slog.Default().Warn("elenco delle risorse leggibili non riuscito", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile filtrare i repo per permesso.")
		return
	}
	var visible []uuid.UUID
	if !all {
		visible = ids
		if visible == nil {
			visible = []uuid.UUID{}
		}
	}
	var owner *string
	if params.Owner != nil {
		o := *params.Owner
		owner = &o
	}
	items, total, err := s.resources.ListRepos(r.Context(), visible, owner, page, perPage)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante la lettura dei repo.")
		return
	}

	empties := make([]bool, len(items))
	var wg sync.WaitGroup
	sem := make(chan struct{}, emptyLookupWorkers)
	for i := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			empties[i] = s.isEmpty(r.Context(), caller, items[i].ID)
		}()
	}
	wg.Wait()

	out := make([]openapi.Repository, 0, len(items))
	for i, it := range items {
		out = append(out, s.toAPIRepo(r, it, empties[i]))
	}
	writeJSON(w, http.StatusOK, openapi.RepositoryList{Items: out, Page: page, PerPage: perPage, Total: total})
}

// CreateRepository implementa POST /repos.
//
// Ordine: validazione → owner (404) → attributi in identity e verifica che il
// creatore sia admin del nuovo repo, cioè owner dell'organizzazione, sé stesso
// o amministratore di sistema (P1, P6; 403) → transazione con risorsa,
// dettaglio e contatore #n (409 se il nome è occupato) → git crea il repo su
// disco → grant admin al creatore → commit. Se git o il grant falliscono la
// transazione si annulla: nessuna riga in core e, se il repo su disco era già
// nato, viene rimosso.
func (s *apiServer) CreateRepository(w http.ResponseWriter, r *http.Request) {
	var body openapi.CreateRepositoryInput
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Corpo della richiesta non valido: "+err.Error())
		return
	}
	if body.Owner == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "owner è obbligatorio.")
		return
	}
	if err := names.ValidateRepoName(body.Name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_name", "Nome del repo non valido: "+err.Error())
		return
	}
	description := ""
	if body.Description != nil {
		description = *body.Description
		if utf8.RuneCountInString(description) > maxDescriptionRunes {
			writeError(w, http.StatusBadRequest, "invalid_body", "description supera i 1024 caratteri.")
			return
		}
	}
	visibility := string(openapi.RepoVisibilityPrivate) // P7: privato anche se il campo manca
	if body.Visibility != nil {
		visibility = string(*body.Visibility)
		if !validVisibility(visibility) {
			writeError(w, http.StatusBadRequest, "invalid_body", "visibility deve essere private o internal.")
			return
		}
	}

	caller, creatorID, ok := callerIdentity(w, r)
	if !ok {
		return
	}
	if !s.reposReady(w) {
		return
	}

	owner, err := s.repoIdentity.ResolveOwner(r.Context(), body.Owner)
	if err != nil {
		if errors.Is(err, identityclient.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Owner non trovato.")
			return
		}
		slog.Default().Warn("risoluzione dell'owner non riuscita", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile.")
		return
	}

	repoID, err := uuid.NewV7()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante la creazione del repo.")
		return
	}
	// Gli attributi vanno in identity prima di tutto: da lì identity calcola
	// P1/P6 e il permesso di creare si verifica sul repo che sta nascendo.
	// Un rifiuto qui lascia solo una riga di attributi per un id mai usato.
	if err := s.repoIdentity.SetResourceAttributes(r.Context(), repoID, owner.Type, owner.ID, visibility); err != nil {
		slog.Default().Warn("attributi del repo non impostati", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: il repo non è stato creato.")
		return
	}
	allowed, err := s.repoIdentity.HasRole(r.Context(), creatorID, repoID, "admin")
	if err != nil {
		slog.Default().Warn("verifica del permesso di creazione non riuscita", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: il repo non è stato creato.")
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden", "Non puoi creare repo per questo owner.")
		return
	}

	tx, err := s.resources.BeginRepo(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante la creazione del repo.")
		return
	}
	defer tx.Rollback(context.WithoutCancel(r.Context()))
	repo, err := tx.Insert(r.Context(), store.NewRepo{
		ID: repoID, OwnerType: owner.Type, OwnerID: owner.ID, OwnerName: owner.Name, Name: body.Name,
		Description: description, Visibility: visibility, DefaultBranch: defaultBranchName,
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "conflict", "Esiste già un repo con questo nome per questo owner.")
			return
		}
		slog.Default().Error("inserimento del repo non riuscito", "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante la creazione del repo.")
		return
	}

	in := gitclient.CreateInput{
		RepoID:        repoID,
		Name:          body.Name,
		Description:   description,
		DefaultBranch: defaultBranchName,
		Author:        gitclient.Author{Name: caller.Username, Email: caller.Username + "@users.noreply." + s.clone.sshHost(r)},
	}
	if body.Readme != nil {
		in.Readme = *body.Readme
	}
	if body.GitignoreTemplate != nil {
		in.GitignoreTemplate = string(*body.GitignoreTemplate)
	}
	if body.LicenseTemplate != nil {
		in.LicenseTemplate = string(*body.LicenseTemplate)
	}
	empty, err := s.git.Create(r.Context(), caller, in)
	if err != nil {
		if errors.Is(err, gitclient.ErrInvalid) {
			writeError(w, http.StatusBadRequest, "invalid_body", "Contenuto iniziale non valido (modello sconosciuto).")
			return
		}
		slog.Default().Warn("creazione del repo su disco non riuscita: nessun repo creato", "repo_id", repoID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Servizio git non disponibile: il repo non è stato creato.")
		return
	}

	if err := s.repoIdentity.GrantResourceCreator(r.Context(), repoID, creatorID); err != nil {
		slog.Default().Warn("grant admin al creatore non riuscito: il repo viene annullato", "repo_id", repoID, "err", err)
		s.discardOnDisk(caller, repoID)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: il repo non è stato creato.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		slog.Default().Error("commit della creazione del repo non riuscito", "repo_id", repoID, "err", err)
		s.discardOnDisk(caller, repoID)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante la creazione del repo.")
		return
	}

	w.Header().Set("Location", "/repos/"+repo.OwnerName+"/"+repo.Name)
	writeJSON(w, http.StatusCreated, s.toAPIRepo(r, repo, empty))
}

// discardOnDisk toglie dal disco un repo appena creato la cui creazione in
// core è stata annullata (cestino + cancellazione definitiva), con un
// contesto proprio: quello della richiesta può essere già scaduto.
func (s *apiServer) discardOnDisk(caller trust.Identity, id uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.git.Trash(ctx, caller, id); err != nil {
		slog.Default().Error("pulizia del repo su disco non riuscita: repo orfano", "repo_id", id, "err", err)
		return
	}
	if err := s.git.Delete(ctx, caller, id); err != nil {
		slog.Default().Error("cancellazione del repo su disco non riuscita: repo nel cestino", "repo_id", id, "err", err)
	}
}

// GetRepository implementa GET /repos/{owner}/{repo}.
func (s *apiServer) GetRepository(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	caller, userID, ok := callerIdentity(w, r)
	if !ok {
		return
	}
	if s.repoIdentity == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile applicare i permessi sui repo.")
		return
	}
	repo, ok := s.lookupReadable(w, r, userID, owner, name)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.toAPIRepo(r, repo, s.isEmpty(r.Context(), caller, repo.ID)))
}

func validVisibility(v string) bool { return v == "private" || v == "internal" }

// UpdateRepository implementa PATCH /repos/{owner}/{repo}: descrizione,
// visibilità, branch principale (R4), protezione (R9), archiviazione (R10).
// Serve admin (403 se legge soltanto, 404 se non legge). Un repo archiviato
// rifiuta tutto con 409 tranne `archived: false` da solo.
func (s *apiServer) UpdateRepository(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam) {
	var body openapi.UpdateRepositoryInput
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Corpo della richiesta non valido: "+err.Error())
		return
	}
	if body.Description == nil && body.Visibility == nil && body.DefaultBranch == nil && body.ProtectDefaultBranch == nil && body.Archived == nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Almeno un campo deve essere presente.")
		return
	}
	if body.Description != nil && utf8.RuneCountInString(*body.Description) > maxDescriptionRunes {
		writeError(w, http.StatusBadRequest, "invalid_body", "description supera i 1024 caratteri.")
		return
	}
	upd := store.RepoUpdate{Description: body.Description, ProtectDefaultBranch: body.ProtectDefaultBranch, Archived: body.Archived}
	if body.Visibility != nil {
		v := string(*body.Visibility)
		if !validVisibility(v) {
			writeError(w, http.StatusBadRequest, "invalid_body", "visibility deve essere private o internal.")
			return
		}
		upd.Visibility = &v
	}
	if body.DefaultBranch != nil {
		b := *body.DefaultBranch
		if b == "" || len(b) > maxBranchNameLen {
			writeError(w, http.StatusBadRequest, "invalid_body", "defaultBranch deve avere da 1 a 255 caratteri.")
			return
		}
		upd.DefaultBranch = &b
	}

	caller, userID, ok := callerIdentity(w, r)
	if !ok {
		return
	}
	if !s.reposReady(w) {
		return
	}
	found, ok := s.lookupReadable(w, r, userID, owner, name)
	if !ok {
		return
	}
	isAdmin, err := s.repoIdentity.HasRole(r.Context(), userID, found.ID, "admin")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile verificare i permessi.")
		return
	}
	if !isAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "Servono i permessi di admin sul repo.")
		return
	}

	tx, err := s.resources.BeginRepo(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante l'aggiornamento del repo.")
		return
	}
	defer tx.Rollback(context.WithoutCancel(r.Context()))
	cur, err := tx.Lock(r.Context(), found.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Repo non trovato.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante l'aggiornamento del repo.")
		return
	}

	// R4: il branch principale è fra quelli esistenti.
	if upd.DefaultBranch != nil && *upd.DefaultBranch != cur.DefaultBranch && cur.ArchivedAt == nil {
		st, err := s.git.Get(r.Context(), caller, cur.ID)
		if err != nil {
			slog.Default().Warn("branch del repo non leggibili da git", "repo_id", cur.ID, "err", err)
			writeError(w, http.StatusServiceUnavailable, "git_unavailable", "Servizio git non disponibile: impossibile verificare il branch.")
			return
		}
		if !slices.Contains(st.Branches, *upd.DefaultBranch) {
			writeError(w, http.StatusBadRequest, "invalid_branch", "Il branch principale deve essere un branch esistente.")
			return
		}
	}

	updated, err := tx.Update(r.Context(), cur, upd)
	if err != nil {
		if errors.Is(err, store.ErrArchived) {
			writeError(w, http.StatusConflict, "archived", "Il repo è archiviato: si può solo riattivarlo.")
			return
		}
		slog.Default().Error("aggiornamento del repo non riuscito", "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante l'aggiornamento del repo.")
		return
	}
	if updated.Visibility != cur.Visibility {
		if err := s.repoIdentity.SetResourceAttributes(r.Context(), updated.ID, updated.OwnerType, updated.OwnerID, updated.Visibility); err != nil {
			slog.Default().Warn("visibilità non aggiornata in identity: modifica annullata", "repo_id", updated.ID, "err", err)
			writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: la modifica non è stata applicata.")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		if updated.Visibility != cur.Visibility {
			// Identity ha già la nuova visibilità: la si riporta indietro.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.repoIdentity.SetResourceAttributes(ctx, cur.ID, cur.OwnerType, cur.OwnerID, cur.Visibility)
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante l'aggiornamento del repo.")
		return
	}
	writeJSON(w, http.StatusOK, s.toAPIRepo(r, updated, s.isEmpty(r.Context(), caller, updated.ID)))
}
