package httpserver

import (
	"errors"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Volumi persistenti delle app (v3-b, GIT-356, H3). `read` elenca; aggiungere ed eliminare richiede
// `admin`. Ogni modifica crea una versione di configurazione (trigger config) con il suo deploy,
// che il controller porta in esecuzione con aggiornamento Recreate; la dimensione conta nella quota
// disco dell'owner e un'app con volumi gira in una sola copia (409 single_copy alla scala).

func volumeToAPI(v store.AppVolume) openapi.AppVolume {
	return openapi.AppVolume{Name: v.Name, MountPath: v.MountPath, SizeBytes: v.SizeBytes, Version: v.Version,
		CreatedAt: v.CreatedAt, CreatedBy: actorToAPI(v.CreatedBy)}
}

func volumesToAPI(vs []store.AppVolume) *[]openapi.AppVolume {
	if len(vs) == 0 {
		return nil
	}
	out := make([]openapi.AppVolume, 0, len(vs))
	for _, v := range vs {
		out = append(out, volumeToAPI(v))
	}
	return &out
}

func volumeSpecsToAPI(vs []store.AppVolumeSpec) *[]openapi.AppVolumeSpec {
	if len(vs) == 0 {
		return nil
	}
	out := make([]openapi.AppVolumeSpec, 0, len(vs))
	for _, v := range vs {
		out = append(out, openapi.AppVolumeSpec{Name: v.Name, MountPath: v.MountPath, SizeBytes: v.SizeBytes})
	}
	return &out
}

// ListAppVolumes implementa GET .../apps/{app}/volumes: `read`.
func (s *apiServer) ListAppVolumes(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	rp, _, ok := s.appRepo(w, r, owner, repo, "read", false)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, rp, app)
	if !ok {
		return
	}
	vs, err := s.resources.ListAppVolumes(r.Context(), a.ID)
	if err != nil {
		appInternalError(w, "elenco dei volumi dell'app", err)
		return
	}
	limits, used, isDefault, err := s.resources.OwnerAppQuota(r.Context(), a.Owner)
	if err != nil {
		appInternalError(w, "quota delle app", err)
		return
	}
	out := openapi.AppVolumeList{Items: []openapi.AppVolume{}, Quota: quotaToAPI(store.AppOwnerQuota{Owner: a.Owner, Limits: limits, Used: used, IsDefault: isDefault})}
	for _, v := range vs {
		out.Items = append(out.Items, volumeToAPI(v))
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateAppVolume implementa POST .../apps/{app}/volumes: `admin`.
func (s *apiServer) CreateAppVolume(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	rp, actor, ok := s.appRepo(w, r, owner, repo, "admin", true)
	if !ok {
		return
	}
	var in openapi.AppVolumeInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if !appNameRe.MatchString(in.Name) {
		writeFieldError(w, "name", "name: lettere minuscole, cifre e trattini, 1-30 caratteri.")
		return
	}
	if msg := store.ValidateVolumeMountPath(in.MountPath); msg != "" {
		writeFieldError(w, "mountPath", msg)
		return
	}
	if in.SizeBytes < store.MinVolumeBytes {
		writeFieldError(w, "sizeBytes", "sizeBytes: almeno 1 MiB.")
		return
	}
	a, ok := s.appFor(w, r, rp, app)
	if !ok {
		return
	}
	ch, err := s.resources.AddAppVolume(r.Context(), a.ID, store.AppVolumeSpec{Name: in.Name, MountPath: in.MountPath, SizeBytes: in.SizeBytes}, actor.store(), s.now())
	if err != nil {
		s.volumeFailure(w, "aggiunta del volume dell'app", err)
		return
	}
	s.kickApps()
	writeJSON(w, http.StatusAccepted, s.volumeChangeToAPI(a, ch))
}

// DeleteAppVolume implementa DELETE .../apps/{app}/volumes/{volume}: `admin`, 404 se il nome non c'è.
func (s *apiServer) DeleteAppVolume(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, volume openapi.AppVolumeParam) {
	rp, actor, ok := s.appRepo(w, r, owner, repo, "admin", true)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, rp, app)
	if !ok {
		return
	}
	ch, err := s.resources.DeleteAppVolume(r.Context(), a.ID, volume, actor.store(), s.now())
	if err != nil {
		s.volumeFailure(w, "eliminazione del volume dell'app", err)
		return
	}
	s.kickApps()
	writeJSON(w, http.StatusAccepted, s.volumeChangeToAPI(a, ch))
}

func (s *apiServer) volumeChangeToAPI(a store.App, ch store.AppVolumeChange) openapi.AppVolumeChange {
	return openapi.AppVolumeChange{
		Volume:  volumeToAPI(ch.Volume),
		Version: s.versionToAPI(ch.Version, a.ActiveVersion, previousVersion(a.ActiveVersion)),
		Quota:   quotaToAPI(store.AppOwnerQuota{Owner: a.Owner, Limits: ch.Limits, Used: ch.Used}),
	}
}

func (s *apiServer) volumeFailure(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Non trovato.")
	case s.writeAppConflict(w, err):
	default:
		appInternalError(w, what, err)
	}
}

// kickApps sveglia il controller, se c'è, perché porti subito il nuovo deploy nel cluster.
func (s *apiServer) kickApps() {
	if s.appCtl != nil {
		s.appCtl.Kick()
	}
}
