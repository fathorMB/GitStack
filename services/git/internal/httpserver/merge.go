package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/git/internal/gitmerge"
	"github.com/fathorMB/GitStack/services/git/internal/gitread"
	"github.com/fathorMB/GitStack/services/git/internal/gitref"
)

// mountCompare aggiunge il confronto fra branch (GIT-260). Con Merge attivo la
// risposta porta anche lo stato di unibilità.
func (h *handler) mountCompare(api *http.ServeMux) {
	api.HandleFunc("GET /internal/git/repos/{repoId}/compare", h.compare)
}

// mountMerge aggiunge unibilità, merge ed eliminazione dei branch (GIT-260).
func (h *handler) mountMerge(api *http.ServeMux) {
	api.HandleFunc("GET /internal/git/repos/{repoId}/mergeability", h.mergeability)
	api.HandleFunc("POST /internal/git/repos/{repoId}/merge", h.merge)
	api.HandleFunc("DELETE /internal/git/repos/{repoId}/branches", h.deleteBranch)
}

type compareResponse struct {
	*gitread.Comparison
	Mergeability *gitmerge.Status `json:"mergeability,omitempty"`
}

// compare: GET …/compare?base&head&ignoreWhitespace. base e head sono branch,
// tag o sha. 200 con merge-base, commit e diff con i limiti di B6.
func (h *handler) compare(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ws, ok := boolParam(w, q.Get("ignoreWhitespace"), "ignoreWhitespace")
	if !ok {
		return
	}
	cmp, err := h.Reads.Compare(r.Context(), r.PathValue("repoId"), q.Get("base"), q.Get("head"), ws)
	if err != nil {
		h.readErr(w, "confronto", err)
		return
	}
	res := compareResponse{Comparison: cmp}
	if h.Merge != nil {
		st, err := h.Merge.Mergeability(r.Context(), r.PathValue("repoId"), q.Get("base"), q.Get("head"))
		switch {
		case err == nil:
			res.Mergeability = st
		case errors.Is(err, gitref.ErrRefNotFound):
			// base non è un branch (es. un tag): il confronto c'è, l'unibilità no.
		default:
			h.mergeErr(w, "unibilità", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handler) mergeability(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	st, err := h.Merge.Mergeability(r.Context(), r.PathValue("repoId"), q.Get("base"), q.Get("head"))
	if err != nil {
		h.mergeErr(w, "unibilità", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

type pusherInput struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Type     string `json:"type"`
}

func (p pusherInput) target(owner, name, defaultBranch string) gitmerge.Target {
	kind := p.Type
	if kind != gitpush.PusherAgent {
		kind = gitpush.PusherHuman
	}
	return gitmerge.Target{
		Owner: owner, Name: name, DefaultBranch: defaultBranch,
		Pusher: gitpush.Pusher{ID: p.ID, Username: p.Username, Type: kind},
	}
}

// mergeInput è il corpo di POST …/merge.
type mergeInput struct {
	Base            string             `json:"base"`
	Head            string             `json:"head"`
	Method          string             `json:"method"`
	Message         string             `json:"message"`
	ExpectedBaseSHA string             `json:"expectedBaseSha"`
	ExpectedHeadSHA string             `json:"expectedHeadSha"`
	Author          gitmerge.Identity  `json:"author"`
	Committer       *gitmerge.Identity `json:"committer"`
	Owner           string             `json:"owner"`
	Name            string             `json:"name"`
	DefaultBranch   string             `json:"defaultBranch"`
	Pusher          pusherInput        `json:"pusher"`
}

// merge: POST …/merge. 200 con il nuovo commit; 409 `stale` se base (o head)
// non punta più allo sha atteso, `conflicts` con l'elenco dei file,
// `nothing_to_merge`, `unrelated_histories`; 400 su input non valido.
func (h *handler) merge(w http.ResponseWriter, r *http.Request) {
	var in mergeInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<18)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "Corpo JSON non valido.")
		return
	}
	mi := gitmerge.MergeInput{
		Base: in.Base, Head: in.Head, Method: in.Method, Message: in.Message,
		ExpectedBaseSHA: in.ExpectedBaseSHA, ExpectedHeadSHA: in.ExpectedHeadSHA,
		Author: in.Author, Target: in.Pusher.target(in.Owner, in.Name, in.DefaultBranch),
	}
	if in.Committer != nil {
		mi.Committer = *in.Committer
	}
	res, err := h.Merge.Merge(r.Context(), r.PathValue("repoId"), mi)
	if err != nil {
		h.mergeErr(w, "merge", err)
		return
	}
	h.Logger.Info("merge eseguito", "repoId", r.PathValue("repoId"), "method", res.Method, "base", res.Base, "sha", res.SHA)
	writeJSON(w, http.StatusOK, res)
}

type deleteBranchInput struct {
	Branch        string      `json:"branch"`
	ExpectedSHA   string      `json:"expectedSha"`
	Owner         string      `json:"owner"`
	Name          string      `json:"name"`
	DefaultBranch string      `json:"defaultBranch"`
	Pusher        pusherInput `json:"pusher"`
}

// deleteBranch: DELETE …/branches con corpo {branch, expectedSha?, …}. 200
// con lo sha che il branch aveva; 409 sul branch principale o se lo sha non
// combacia; 404 se il branch non c'è.
func (h *handler) deleteBranch(w http.ResponseWriter, r *http.Request) {
	var in deleteBranchInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "Corpo JSON non valido.")
		return
	}
	sha, err := h.Merge.DeleteBranch(r.Context(), r.PathValue("repoId"), gitmerge.DeleteInput{
		Branch: in.Branch, ExpectedSHA: in.ExpectedSHA, Target: in.Pusher.target(in.Owner, in.Name, in.DefaultBranch),
	})
	if err != nil {
		h.mergeErr(w, "eliminazione del branch", err)
		return
	}
	h.Logger.Info("branch eliminato", "repoId", r.PathValue("repoId"), "branch", in.Branch)
	writeJSON(w, http.StatusOK, map[string]string{"branch": in.Branch, "sha": sha})
}

func (h *handler) mergeErr(w http.ResponseWriter, what string, err error) {
	var ce *gitmerge.ConflictError
	var se *gitmerge.StaleError
	switch {
	case errors.As(err, &ce):
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{
			"code": "conflicts", "message": "Il merge ha conflitti.", "files": ce.Files}})
	case errors.As(err, &se):
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{
			"code": "stale", "message": "Il ref è cambiato: ricarica e riprova.", "ref": se.Ref, "currentSha": se.Current}})
	case errors.Is(err, gitmerge.ErrNothingToMerge):
		writeErr(w, http.StatusConflict, "nothing_to_merge", "Niente da unire: head è già contenuto in base.")
	case errors.Is(err, gitmerge.ErrUnrelated):
		writeErr(w, http.StatusConflict, "unrelated_histories", "I branch non hanno storia in comune.")
	case errors.Is(err, gitmerge.ErrDefaultBranch):
		writeErr(w, http.StatusConflict, "default_branch", "Il branch principale non si elimina.")
	case errors.Is(err, gitmerge.ErrInvalid):
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
	default:
		h.readErr(w, what, err)
	}
}
