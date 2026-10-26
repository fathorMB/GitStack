package httpserver

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/ci"
	"github.com/fathorMB/GitStack/services/core/internal/identityclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/google/uuid"
)

// CI sui repo in mirror in pull (CI3, 2026-10-26): l'interruttore per repo lo
// cambia solo chi ha admin sul repo (hookRepoScope).

func (s *apiServer) pullMirrorCIView(ctx context.Context, repoID uuid.UUID) (openapi.ActionsPullMirrorSettings, error) {
	if eng := s.ciEngine(); eng != nil {
		// Chi l'aveva accesa e non ha più admin: la CI risulta già spenta.
		if err := eng.GuardMirror(ctx, repoID); err != nil {
			return openapi.ActionsPullMirrorSettings{}, err
		}
	}
	m, err := ci.LoadMirrorCI(ctx, s.pool, repoID)
	if err != nil {
		return openapi.ActionsPullMirrorSettings{}, err
	}
	out := openapi.ActionsPullMirrorSettings{PullMirror: m.Mirror, RunOnPullMirror: m.Enabled}
	if m.Enabled && m.EnabledBy != uuid.Nil {
		name := m.EnabledBy.String()
		if look, ok := s.repoIdentity.(identityclient.UserLookup); ok {
			if found, err := look.LookupUsers(ctx, []uuid.UUID{m.EnabledBy}); err == nil {
				if u, ok := found[m.EnabledBy]; ok && u.Username != "" {
					name = u.Username
				}
			}
		}
		out.EnabledBy = &name
	}
	return out, nil
}

func (s *apiServer) GetRepositoryActionsPullMirror(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	sc, ok := s.hookRepoScope(w, r, o, n, false)
	if !ok {
		return
	}
	v, err := s.pullMirrorCIView(r.Context(), sc.repo.ID)
	if err != nil {
		s.ciFailure(w, "lettura della CI del mirror in pull", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *apiServer) PutRepositoryActionsPullMirror(w http.ResponseWriter, r *http.Request, o openapi.RepoOwnerParam, n openapi.RepoNameParam) {
	sc, ok := s.hookRepoScope(w, r, o, n, true)
	if !ok {
		return
	}
	// Struct locale con *bool: il tipo generato non distingue assente da false.
	var in struct {
		RunOnPullMirror *bool `json:"runOnPullMirror"`
	}
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.RunOnPullMirror == nil {
		writeFieldError(w, "runOnPullMirror", "runOnPullMirror: obbligatorio.")
		return
	}
	enable := *in.RunOnPullMirror
	cur, err := ci.LoadMirrorCI(r.Context(), s.pool, sc.repo.ID)
	if err != nil {
		s.ciFailure(w, "lettura della CI del mirror in pull", err)
		return
	}
	if enable && !cur.Mirror {
		writeError(w, http.StatusConflict, "not_a_pull_mirror", "Il repo non è un mirror in pull attivo: l'impostazione non ha effetto.")
		return
	}
	// Riaccendere una CI già accesa passa a chi la riaccende (diventa l'autore).
	if err := ci.SetMirrorCI(r.Context(), s.pool, sc.repo.ID, enable, sc.userID); err != nil {
		s.ciFailure(w, "salvataggio della CI del mirror in pull", err)
		return
	}
	v, err := s.pullMirrorCIView(r.Context(), sc.repo.ID)
	if err != nil {
		s.ciFailure(w, "lettura della CI del mirror in pull", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
