package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
	"github.com/fathorMB/GitStack/services/git/internal/mirrorpull"
	"github.com/fathorMB/GitStack/services/git/internal/repostore"
)

// mirrorPullInput è il corpo di POST /internal/git/repos/{repoId}/mirror-pull
// (GIT-196). Il token sta solo nel corpo, mai nell'URL né nei log.
type mirrorPullInput struct {
	URL           string `json:"url"`
	Username      string `json:"username"`
	Token         string `json:"token"`
	IP            string `json:"ip"`
	DefaultBranch string `json:"defaultBranch"`
	Owner         string `json:"owner"`
	Name          string `json:"name"`
	Pusher        struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	} `json:"pusher"`
}

// mirrorPull aggiorna il repo dall'origine HTTPS, senza mai sovrascrivere.
// 200 con l'esito per ref (anche con ref divergenti: `ok` e `diverged` lo
// dicono), 400 su input non valido, 404 se il repo non c'è, 409 se un
// aggiornamento per lo stesso repo è già in corso.
func (h *handler) mirrorPull(w http.ResponseWriter, r *http.Request) {
	id, err := repostore.NormalizeID(r.PathValue("repoId"))
	if err != nil {
		h.storeErr(w, "mirror", err)
		return
	}
	var in mirrorPullInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "Corpo JSON non valido.")
		return
	}
	res, err := h.Pull.Pull(r.Context(), id, mirrorpull.Input{
		URL: in.URL, Username: in.Username, Token: in.Token, IP: in.IP, DefaultBranch: in.DefaultBranch,
		Owner: in.Owner, Name: in.Name, PusherID: in.Pusher.ID, PusherUsername: in.Pusher.Username,
	})
	switch {
	case errors.Is(err, mirrorpull.ErrInvalid):
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, mirrorpull.ErrForbidden):
		writeErr(w, http.StatusForbidden, "forbidden_destination", "Destinazione non ammessa.")
	case errors.Is(err, mirrorpull.ErrBusy):
		writeErr(w, http.StatusConflict, "busy", "Un aggiornamento per questo repo è già in corso.")
	case errors.Is(err, repostore.ErrNotFound), errors.Is(err, repostore.ErrInvalidID):
		h.storeErr(w, "mirror", err)
	case err != nil:
		var ge *gitrun.Error
		// Mai il testo grezzo dell'errore: potrebbe contenere l'argv.
		h.Logger.Error("mirror pull non eseguibile", "repoId", id, "gitError", errors.As(err, &ge))
		writeErr(w, http.StatusInternalServerError, "internal", "Errore interno.")
	default:
		h.Logger.Info("mirror pull eseguito", "repoId", id, "ok", res.OK, "diverged", res.Diverged, "refs", len(res.Refs), "changed", res.Changed, "durationMs", res.DurationMs)
		writeJSON(w, http.StatusOK, res)
	}
}
