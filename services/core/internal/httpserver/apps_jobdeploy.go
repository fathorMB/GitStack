package httpserver

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// Deploy con il token del job (v3-a, GIT-349, H7): un workflow con `permissions: packages: write`
// mette in produzione un'immagine in un'app del proprio repo, con POST .../apps/{app}/deploys e il
// token gsj_. Il gateway ammette il token solo su questa rotta (x-job-token); core lo verifica da
// sé: deve valere, il repo dell'URL deve essere quello del job, il livello `packages` del job deve
// essere write. I permessi sono quelli di chi ha causato il run (CI4): deve avere ancora `write`
// sul repo. Il deploy compare nello storico a nome suo.

// deployRepo è appRepo per il deploy: un utente o un agente con `write`, oppure un job (token gsj_).
func (s *apiServer) deployRepo(w http.ResponseWriter, r *http.Request, owner, name string) (store.Repo, openapiActor, bool) {
	if id, has := trust.FromContext(r.Context()); has && isJobIdentity(id) {
		return s.jobDeployRepo(w, r, owner, name)
	}
	return s.appRepo(w, r, owner, name, "write", true)
}

func (s *apiServer) jobDeployRepo(w http.ResponseWriter, r *http.Request, owner, name string) (store.Repo, openapiActor, bool) {
	deny := func(status int, code, msg string) (store.Repo, openapiActor, bool) {
		writeError(w, status, code, msg)
		return store.Repo{}, openapiActor{}, false
	}
	token := strings.TrimSpace(r.Header.Get(trust.HeaderJobToken))
	if token == "" || s.pool == nil || s.repoIdentity == nil {
		return deny(http.StatusUnauthorized, "unauthenticated", "Token del job assente o non valido.")
	}
	a, err := ci.ResolveJobToken(r.Context(), s.pool, s.now(), token)
	if errors.Is(err, ci.ErrJobToken) {
		return deny(http.StatusUnauthorized, "unauthenticated", "Token del job assente o non valido.")
	}
	if err != nil {
		return deny(http.StatusServiceUnavailable, "ci_unavailable", "Impossibile verificare il token del job.")
	}
	// Il repo è quello del job e nessun altro: un altro repo, anche esistente, è 404.
	repo, err := s.resources.GetRepoByName(r.Context(), owner, name)
	if err != nil || repo.ID != a.RepoID || repo.DeletedAt != nil {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return deny(http.StatusInternalServerError, "internal_error", "Errore interno durante la lettura del repo.")
		}
		return deny(http.StatusNotFound, "not_found", "Repo non trovato.")
	}
	var raw []byte
	var actorID *uuid.UUID
	if err := s.pool.QueryRow(r.Context(), `SELECT j.permissions, r.actor_id FROM core.ci_jobs j JOIN core.ci_runs r ON r.id = j.run_id WHERE j.id = $1`, a.JobID).Scan(&raw, &actorID); err != nil {
		return deny(http.StatusServiceUnavailable, "ci_unavailable", "Impossibile leggere i permessi del job.")
	}
	var perms map[string]string
	_ = json.Unmarshal(raw, &perms)
	if perms["packages"] != ci.LevelWrite {
		return deny(http.StatusForbidden, "forbidden", "Il token del job non ha packages: write.")
	}
	if actorID == nil {
		return deny(http.StatusForbidden, "forbidden", "Il run non ha un autore (per esempio una schedule): il deploy ha bisogno dei permessi di chi lo ha causato.")
	}
	has, err := s.repoIdentity.HasRole(r.Context(), *actorID, repo.ID, "write")
	if err != nil {
		slog.Default().Warn("verifica del permesso non riuscita", "err", err)
		return deny(http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: impossibile verificare i permessi.")
	}
	if !has {
		return deny(http.StatusForbidden, "forbidden", "Chi ha causato il run non ha più il permesso write sul repo.")
	}
	if repo.ArchivedAt != nil {
		return deny(http.StatusConflict, "archived", "Il repo è archiviato: le app non accettano modifiche.")
	}
	actor := openapiActor{id: *actorID, username: actorID.String(), kind: "human"}
	if dir, err := s.resolveUsers(r.Context(), []uuid.UUID{*actorID}); err == nil {
		if u, ok := dir[*actorID]; ok {
			actor.username, actor.kind = u.Username, string(u.Kind)
		}
	}
	return repo, actor, true
}
