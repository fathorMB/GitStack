package httpserver

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Volumi persistenti delle app (v3-b, GIT-356, H3). `read` elenca; aggiungere ed eliminare richiede
// `admin`. Ogni modifica crea una versione di configurazione (trigger config) con il suo deploy,
// che il controller porta in esecuzione con aggiornamento Recreate; la dimensione conta nella quota
// disco dell'owner e un'app con volumi gira in una sola copia (409 single_copy alla scala).

// dbHint riconosce un database dal percorso di mount, dal nome del volume o dall'immagine (D19): la
// stessa regola della UI (web/src/lib/appVolumes.ts).
var dbHint = regexp.MustCompile(`(?i)postgres|pgdata|mysql|mariadb|mongo|redis|couchdb|influx|cassandra|clickhouse|sqlite|.db`)

// looksLikeDatabase è l'avviso database (D19): la copia del backup si fa con l'app accesa, quindi per
// un database dentro un container la coerenza non è garantita.
func looksLikeDatabase(v store.AppVolume, imageRef string) bool {
	return dbHint.MatchString(v.MountPath) || dbHint.MatchString(v.Name) || dbHint.MatchString(imageRef)
}

func volumeToAPI(v store.AppVolume, imageRef string) openapi.AppVolume {
	return openapi.AppVolume{Name: v.Name, MountPath: v.MountPath, SizeBytes: v.SizeBytes, Version: v.Version,
		CreatedAt: v.CreatedAt, CreatedBy: actorToAPI(v.CreatedBy), IncludeInBackup: v.IncludeInBackup,
		LooksLikeDatabase: looksLikeDatabase(v, imageRef)}
}

func volumesToAPI(vs []store.AppVolume, imageRef string) *[]openapi.AppVolume {
	if len(vs) == 0 {
		return nil
	}
	out := make([]openapi.AppVolume, 0, len(vs))
	for _, v := range vs {
		out = append(out, volumeToAPI(v, imageRef))
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
		out.Items = append(out.Items, volumeToAPI(v, a.Image.Reference))
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

// UpdateAppVolume implementa PATCH .../apps/{app}/volumes/{volume}: `admin`. Include o esclude il
// volume dal backup (D19); non crea versioni né deploy.
func (s *apiServer) UpdateAppVolume(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam, volume openapi.AppVolumeParam) {
	rp, _, ok := s.appRepo(w, r, owner, repo, "admin", true)
	if !ok {
		return
	}
	// Il tipo generato non distingue assente da false: un corpo senza il campo non deve escludere il volume.
	var in struct {
		IncludeInBackup *bool `json:"includeInBackup"`
	}
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.IncludeInBackup == nil {
		writeFieldError(w, "includeInBackup", "includeInBackup: obbligatorio (true o false).")
		return
	}
	a, ok := s.appFor(w, r, rp, app)
	if !ok {
		return
	}
	v, err := s.resources.SetAppVolumeBackup(r.Context(), a.ID, volume, *in.IncludeInBackup)
	if err != nil {
		s.volumeFailure(w, "aggiornamento del volume dell'app", err)
		return
	}
	writeJSON(w, http.StatusOK, volumeToAPI(v, a.Image.Reference))
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
		Volume:  volumeToAPI(ch.Volume, a.Image.Reference),
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
