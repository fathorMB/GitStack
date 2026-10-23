package httpserver

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/google/uuid"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
)

// Impostazioni del registry di un repo (GIT-325, RG2): tag protetti, pulizia, backup.
// Le legge e le modifica chi ha `admin` sul repo (come l'eliminazione delle immagini, RG1).
// Il servizio git le legge da /internal/registry/settings/{repoId} con la sua chiave.

const (
	maxProtectedTagPatterns = 20
	maxTagPatternLen        = 128
	maxRetentionDays        = 3650
	maxKeepLastTags         = 10000
)

func packageSettingsToAPI(p store.PackageSettings) openapi.PackageSettings {
	out := openapi.PackageSettings{
		ProtectedTagPatterns:    p.ProtectedTagPatterns,
		DeleteUntaggedAfterDays: p.DeleteUntaggedAfterDays,
		IncludeInBackup:         p.IncludeInBackup,
		KeepLastTags:            p.KeepLastTags,
		DeleteShaTagsAfterDays:  p.DeleteShaTagsAfterDays,
	}
	if out.ProtectedTagPatterns == nil {
		out.ProtectedTagPatterns = []string{}
	}
	return out
}

// validatePackageSettings controlla l'input; ritorna il campo e il messaggio del primo errore.
func validatePackageSettings(in openapi.PackageSettingsInput) (string, string) {
	if len(in.ProtectedTagPatterns) > maxProtectedTagPatterns {
		return "protectedTagPatterns", "protectedTagPatterns: al massimo 20 modelli."
	}
	seen := map[string]bool{}
	for _, p := range in.ProtectedTagPatterns {
		if p == "" || len(p) > maxTagPatternLen || strings.ContainsAny(p, "/\\ \t\r\n") {
			return "protectedTagPatterns", "protectedTagPatterns: ogni modello è lungo 1-128 caratteri, senza spazi né barre."
		}
		if _, err := path.Match(p, "v1.0"); err != nil {
			return "protectedTagPatterns", "protectedTagPatterns: modello non valido (usa *, ? e [..])."
		}
		if seen[p] {
			return "protectedTagPatterns", "protectedTagPatterns: modello ripetuto."
		}
		seen[p] = true
	}
	if in.DeleteUntaggedAfterDays < 1 || in.DeleteUntaggedAfterDays > maxRetentionDays {
		return "deleteUntaggedAfterDays", "deleteUntaggedAfterDays: da 1 a 3650."
	}
	if in.DeleteShaTagsAfterDays != nil && (*in.DeleteShaTagsAfterDays < 1 || *in.DeleteShaTagsAfterDays > maxRetentionDays) {
		return "deleteShaTagsAfterDays", "deleteShaTagsAfterDays: da 1 a 3650."
	}
	if in.KeepLastTags != nil && (*in.KeepLastTags < 1 || *in.KeepLastTags > maxKeepLastTags) {
		return "keepLastTags", "keepLastTags: da 1 a 10000."
	}
	return "", ""
}

// packageRepo risolve il repo e verifica admin. mutate: rifiuta il repo archiviato (R10).
func (s *apiServer) packageRepo(w http.ResponseWriter, r *http.Request, owner, name string, mutate bool) (store.Repo, uuid.UUID, bool) {
	_, userID, ok := callerIdentity(w, r)
	if !ok || s.repoIdentity == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile verificare i permessi.")
		}
		return store.Repo{}, uuid.Nil, false
	}
	repo, ok := s.lookupReadable(w, r, userID, owner, name)
	if !ok {
		return store.Repo{}, uuid.Nil, false
	}
	admin, err := s.repoIdentity.HasRole(r.Context(), userID, repo.ID, "admin")
	if err != nil {
		slog.Default().Warn("verifica del permesso admin non riuscita", "err", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile verificare i permessi.")
		return store.Repo{}, uuid.Nil, false
	}
	if !admin {
		writeError(w, http.StatusForbidden, "forbidden", "Serve il permesso admin sul repo.")
		return store.Repo{}, uuid.Nil, false
	}
	if mutate && repo.ArchivedAt != nil {
		writeError(w, http.StatusConflict, "archived", "Il repo è archiviato: non accetta modifiche.")
		return store.Repo{}, uuid.Nil, false
	}
	return repo, userID, true
}

// GetRepositoryPackageSettings implementa GET /repos/{owner}/{repo}/packages/settings.
func (s *apiServer) GetRepositoryPackageSettings(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	repo, _, ok := s.packageRepo(w, r, o, n, false)
	if !ok {
		return
	}
	p, err := s.resources.GetPackageSettings(r.Context(), repo.ID)
	if err != nil {
		slog.Default().Error("lettura delle impostazioni dei pacchetti", "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno.")
		return
	}
	writeJSON(w, http.StatusOK, packageSettingsToAPI(p))
}

// UpdateRepositoryPackageSettings implementa PATCH /repos/{owner}/{repo}/packages/settings:
// il corpo sostituisce le impostazioni (i campi facoltativi assenti spengono la regola).
func (s *apiServer) UpdateRepositoryPackageSettings(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	repo, userID, ok := s.packageRepo(w, r, o, n, true)
	if !ok {
		return
	}
	var in openapi.PackageSettingsInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if field, msg := validatePackageSettings(in); field != "" {
		writeFieldError(w, field, msg)
		return
	}
	p, err := s.resources.PutPackageSettings(r.Context(), repo.ID, userID, store.PackageSettings{
		ProtectedTagPatterns:    in.ProtectedTagPatterns,
		DeleteUntaggedAfterDays: in.DeleteUntaggedAfterDays,
		IncludeInBackup:         in.IncludeInBackup,
		KeepLastTags:            in.KeepLastTags,
		DeleteShaTagsAfterDays:  in.DeleteShaTagsAfterDays,
	})
	if err != nil {
		slog.Default().Error("salvataggio delle impostazioni dei pacchetti", "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno.")
		return
	}
	writeJSON(w, http.StatusOK, packageSettingsToAPI(p))
}

// RegistrySettingsPathPrefix è la rotta interna con cui il servizio git legge le impostazioni.
const RegistrySettingsPathPrefix = "/internal/registry/settings/"

// registrySettings risponde al servizio git (solo con la sua chiave): le impostazioni del
// repo con i dati che servono alla pulizia e alla quota (archiviato, repo dello stesso owner).
// Un repo sconosciuto o eliminato dà 404: il servizio git non tocca le sue immagini.
func (s *apiServer) registrySettings(w http.ResponseWriter, r *http.Request) {
	if trust.CallerFromContext(r.Context()) != trust.CallerGit {
		writeError(w, http.StatusForbidden, "forbidden", "Il servizio chiamante non può fare questa operazione.")
		return
	}
	id, err := uuid.Parse(r.PathValue("repoId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Repo non trovato.")
		return
	}
	repo, err := s.resources.GetRepoByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && repo.DeletedAt != nil) {
		writeError(w, http.StatusNotFound, "not_found", "Repo non trovato.")
		return
	}
	if err != nil {
		slog.Default().Error("lettura del repo per il registry", "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno.")
		return
	}
	p, err := s.resources.GetPackageSettings(r.Context(), id)
	if err != nil {
		slog.Default().Error("lettura delle impostazioni dei pacchetti", "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno.")
		return
	}
	ids, err := s.resources.OwnerRepoIDs(r.Context(), id)
	if err != nil {
		slog.Default().Error("elenco dei repo dell'owner", "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno.")
		return
	}
	owner := make([]string, 0, len(ids))
	for _, x := range ids {
		owner = append(owner, x.String())
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"repoId":                  id.String(),
		"owner":                   repo.OwnerName,
		"name":                    repo.Name,
		"archived":                repo.ArchivedAt != nil,
		"protectedTagPatterns":    packageSettingsToAPI(p).ProtectedTagPatterns,
		"deleteUntaggedAfterDays": p.DeleteUntaggedAfterDays,
		"keepLastTags":            p.KeepLastTags,
		"deleteShaTagsAfterDays":  p.DeleteShaTagsAfterDays,
		"includeInBackup":         p.IncludeInBackup,
		"ownerRepoIds":            owner,
	})
}
