package httpserver

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Deploy a mano, rollback, riavvio e scala (v3-a, GIT-343, H7/H12). Gli handler scrivono il modello
// (versione + deploy «in corso», copie) e svegliano il controller, che porta il cluster allo stato
// voluto: l'esito si legge da getAppVersion o dall'ultimo deploy dell'app.

// appController dà il controller o risponde 503 se i deploy non sono abilitati.
func (s *apiServer) appController(w http.ResponseWriter) (AppController, bool) {
	if s.appCtl == nil {
		writeError(w, http.StatusServiceUnavailable, "apps_unavailable", "I deploy delle app non sono abilitati su questa istanza: l'amministratore li accende con «gitstack config set apps on».")
		return nil, false
	}
	return s.appCtl, true
}

func (s *apiServer) writeDeployConflict(w http.ResponseWriter, err error) bool {
	if errors.Is(err, store.ErrAppDeployInProgress) {
		writeError(w, http.StatusConflict, "deploy_in_progress", "C'è già un deploy in corso per l'app: aspetta che finisca.")
		return true
	}
	var missing *store.MissingAppSecretsError
	if errors.As(err, &missing) {
		writeErrorDetails(w, http.StatusConflict, "app_secrets_missing", "La versione richiede segreti dell'app che non esistono piu'.", map[string]any{"names": missing.Names})
		return true
	}
	return s.writeAppConflict(w, err)
}

// DeployApp implementa POST /repos/{owner}/{repo}/apps/{app}/deploys.
func (s *apiServer) DeployApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam) {
	repo, actor, ok := s.appRepo(w, r, owner, name, "write", true)
	if !ok {
		return
	}
	ctl, ok := s.appController(w)
	if !ok {
		return
	}
	var in openapi.AppDeployInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if _, ok := s.appFor(w, r, repo, app); !ok {
		return
	}
	img, msg := s.resolveAppImage(in.Image, repo)
	if msg != "" {
		writeFieldError(w, "image", msg)
		return
	}
	a, ver, err := s.resources.BeginAppDeploy(r.Context(), repo.ID, app, store.NewAppDeploy{
		Trigger: "manual", Image: img, Actor: actor.store(), CommitSha: s.imageCommit(r.Context(), repo, img),
	}, s.now())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "App non trovata.")
		} else if !s.writeDeployConflict(w, err) {
			appInternalError(w, "deploy dell'app", err)
		}
		return
	}
	ctl.Kick()
	writeJSON(w, http.StatusAccepted, s.versionToAPI(ver, a.ActiveVersion, previousVersion(a.ActiveVersion)))
}

// RollbackApp implementa POST /repos/{owner}/{repo}/apps/{app}/rollback: una nuova versione con
// immagine e configurazione di quella scelta (H8, H12). La precedente a quella attiva è sempre
// disponibile (H4); le più vecchie solo se la pulizia del registry non ne ha tolto l'immagine.
func (s *apiServer) RollbackApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam) {
	repo, actor, ok := s.appRepo(w, r, owner, name, "write", true)
	if !ok {
		return
	}
	ctl, ok := s.appController(w)
	if !ok {
		return
	}
	var in openapi.AppRollbackInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	cur, ok := s.appFor(w, r, repo, app)
	if !ok {
		return
	}
	target, err := s.resources.GetAppVersion(r.Context(), cur.ID, in.Version)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrAppVersionNotFound) {
		writeFieldError(w, "version", fmt.Sprintf("version: la versione %d non esiste.", in.Version))
		return
	}
	if err != nil {
		appInternalError(w, "lettura della versione", err)
		return
	}
	switch {
	case target.Number == cur.ActiveVersion && !cur.RunningPreviousVer:
		writeFieldError(w, "version", "version: è la versione già in esecuzione.")
		return
	case target.State == "failed":
		writeFieldError(w, "version", "version: quella versione non è mai partita.")
		return
	}
	if target.Number != previousVersion(cur.ActiveVersion) && target.Number != cur.ActiveVersion && target.Image.Source == "registry" {
		removed, err := s.resources.AppImageRemoved(r.Context(), repo.ID, target.Image.Reference)
		if err != nil {
			appInternalError(w, "verifica dell'immagine", err)
			return
		}
		if removed {
			writeError(w, http.StatusConflict, "image_unavailable", "L'immagine di quella versione non c'è più nel registry (pulizia).")
			return
		}
	}
	summary := fmt.Sprintf("ritorno alla versione %d", target.Number)
	pause := in.PauseAutoDeploy != nil && *in.PauseAutoDeploy
	_, ver, err := s.resources.BeginAppDeploy(r.Context(), repo.ID, app, store.NewAppDeploy{
		Trigger: "rollback", Image: target.Image, RollbackOf: &target.Number, Summary: &summary,
		ConfigFrom: target.Number, PauseAutoDeploy: pause, Actor: actor.store(),
		// Stessa immagine della versione di destinazione: ne riprende il commit (H14).
		CommitSha: target.CommitSha,
	}, s.now())
	if err != nil {
		if !s.writeDeployConflict(w, err) {
			appInternalError(w, "rollback dell'app", err)
		}
		return
	}
	ctl.Kick()
	writeJSON(w, http.StatusAccepted, s.versionToAPI(ver, cur.ActiveVersion, previousVersion(cur.ActiveVersion)))
}

// RestartApp implementa POST /repos/{owner}/{repo}/apps/{app}/restart.
func (s *apiServer) RestartApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam) {
	repo, _, ok := s.appRepo(w, r, owner, name, "write", true)
	if !ok {
		return
	}
	ctl, ok := s.appController(w)
	if !ok {
		return
	}
	cur, ok := s.appFor(w, r, repo, app)
	if !ok {
		return
	}
	if cur.LastDeploy != nil && cur.LastDeploy.State == "in_progress" {
		writeError(w, http.StatusConflict, "deploy_in_progress", "C'è già un deploy in corso per l'app: aspetta che finisca.")
		return
	}
	if st, _ := cur.Effective(); st == "stopped" {
		writeError(w, http.StatusConflict, "app_stopped", "L'app è ferma: non c'è niente da riavviare.")
		return
	}
	if err := ctl.Restart(r.Context(), cur); err != nil {
		if errors.Is(err, apps.ErrNotDeployed) {
			writeError(w, http.StatusConflict, "not_deployed", "L'app non ha ancora una versione in esecuzione.")
			return
		}
		slog.Default().Error("riavvio dell'app", "err", err)
		writeError(w, http.StatusBadGateway, "cluster_error", "Il cluster non ha accettato il riavvio.")
		return
	}
	out, err := s.resources.GetAppByID(r.Context(), cur.ID)
	if err != nil {
		appInternalError(w, "lettura dell'app", err)
		return
	}
	writeJSON(w, http.StatusAccepted, s.appToAPI(out))
}

// ScaleApp implementa PUT /repos/{owner}/{repo}/apps/{app}/scale: scala a mano entro la quota.
func (s *apiServer) ScaleApp(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam) {
	repo, _, ok := s.appRepo(w, r, owner, name, "write", true)
	if !ok {
		return
	}
	ctl, ok := s.appController(w)
	if !ok {
		return
	}
	var in openapi.AppScaleInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	if in.Copies < 0 || in.Copies > maxAppCopies {
		writeFieldError(w, "copies", "copies: da 0 a 50.")
		return
	}
	if _, ok := s.appFor(w, r, repo, app); !ok {
		return
	}
	out, err := s.resources.SetAppCopies(r.Context(), repo.ID, app, in.Copies, s.now())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "App non trovata.")
		} else if !s.writeAppConflict(w, err) {
			appInternalError(w, "scala dell'app", err)
		}
		return
	}
	// Il modello è già scritto: se il cluster non risponde, il sync del controller rimette le copie.
	if err := ctl.Scale(r.Context(), out); err != nil {
		slog.Default().Warn("scala dell'app sul cluster non riuscita", "app", out.ID, "err", err)
	}
	writeJSON(w, http.StatusOK, s.appToAPI(out))
}
