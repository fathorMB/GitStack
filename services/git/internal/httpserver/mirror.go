package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
	"github.com/fathorMB/GitStack/services/git/internal/mirrorpush"
	"github.com/fathorMB/GitStack/services/git/internal/repostore"
)

// mirrorPushInput è il corpo di POST /internal/git/repos/{repoId}/mirror-push
// (GIT-179). Il token sta solo nel corpo, mai nell'URL né nei log.
type mirrorPushInput struct {
	URL           string `json:"url"`
	Username      string `json:"username"`
	Token         string `json:"token"`
	IP            string `json:"ip"`
	DefaultBranch string `json:"defaultBranch"`
}

// mirrorPush spinge branch principale e tag su un remote HTTPS, non forzato.
// 200 con l'esito per ref (anche se la destinazione ha rifiutato: `ok` e
// `diverged` lo dicono), 400 su input non valido, 404 se il repo non c'è, 409
// se un push per lo stesso mirror è già in corso.
func (h *handler) mirrorPush(w http.ResponseWriter, r *http.Request) {
	id, err := repostore.NormalizeID(r.PathValue("repoId"))
	if err != nil {
		h.storeErr(w, "mirror", err)
		return
	}
	var in mirrorPushInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "Corpo JSON non valido.")
		return
	}
	res, err := h.Mirror.Push(r.Context(), id, mirrorpush.Input{
		URL: in.URL, Username: in.Username, Token: in.Token, IP: in.IP, DefaultBranch: in.DefaultBranch,
	})
	switch {
	case errors.Is(err, mirrorpush.ErrInvalid):
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, mirrorpush.ErrBusy):
		writeErr(w, http.StatusConflict, "busy", "Un push per questo mirror è già in corso.")
	case errors.Is(err, repostore.ErrNotFound), errors.Is(err, repostore.ErrInvalidID):
		h.storeErr(w, "mirror", err)
	case err != nil:
		var ge *gitrun.Error
		// Mai il testo grezzo dell'errore: potrebbe contenere l'argv.
		h.Logger.Error("mirror push non eseguibile", "repoId", id, "gitError", errors.As(err, &ge))
		writeErr(w, http.StatusInternalServerError, "internal", "Errore interno.")
	default:
		h.Logger.Info("mirror push eseguito", "repoId", id, "ok", res.OK, "diverged", res.Diverged, "refs", len(res.Refs), "durationMs", res.DurationMs)
		writeJSON(w, http.StatusOK, res)
	}
}
