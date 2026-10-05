package httpserver

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/fathorMB/GitStack/services/git/internal/gitread"
	"github.com/fathorMB/GitStack/services/git/internal/gitref"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
	"github.com/fathorMB/GitStack/services/git/internal/repostore"
)

// mountReads aggiunge le letture sulla storia (M-04/C) all'API interna.
func (h *handler) mountReads(api *http.ServeMux) {
	api.HandleFunc("GET /internal/git/repos/{repoId}/commits", h.commits)
	api.HandleFunc("GET /internal/git/repos/{repoId}/commits/{sha}", h.commit)
	api.HandleFunc("GET /internal/git/repos/{repoId}/commits/{sha}/diff", h.download(gitread.FormatDiff))
	api.HandleFunc("GET /internal/git/repos/{repoId}/commits/{sha}/patch", h.download(gitread.FormatPatch))
	api.HandleFunc("GET /internal/git/repos/{repoId}/blame", h.blame)
	api.HandleFunc("GET /internal/git/repos/{repoId}/tree", h.tree)
	api.HandleFunc("GET /internal/git/repos/{repoId}/contents", h.contents)
	api.HandleFunc("GET /internal/git/repos/{repoId}/readme", h.readme)
	api.HandleFunc("GET /internal/git/repos/{repoId}/raw", h.raw)
	api.HandleFunc("GET /internal/git/repos/{repoId}/branches", h.branches)
	api.HandleFunc("GET /internal/git/repos/{repoId}/tags", h.tags)
	api.HandleFunc("GET /internal/git/repos/{repoId}/archive", h.archive)
	api.HandleFunc("GET /internal/git/repos/{repoId}/languages", h.languages)
	api.HandleFunc("GET /internal/git/repos/{repoId}/files", h.files)
	api.HandleFunc("GET /internal/git/repos/{repoId}/search", h.search)
}

// languages: byte per lingua a ref (gitGetLanguages).
func (h *handler) languages(w http.ResponseWriter, r *http.Request) {
	res, err := h.Reads.Languages(r.Context(), r.PathValue("repoId"), r.URL.Query().Get("ref"))
	if err != nil {
		h.readErr(w, "lingue", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// files: percorsi dei file a ref (gitListFiles, B5).
func (h *handler) files(w http.ResponseWriter, r *http.Request) {
	res, err := h.Reads.Files(r.Context(), r.PathValue("repoId"), r.URL.Query().Get("ref"))
	if err != nil {
		h.readErr(w, "elenco dei file", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// search: ricerca testuale a ref (gitSearchCode, B5).
func (h *handler) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := h.Reads.SearchCode(r.Context(), r.PathValue("repoId"), q.Get("ref"), q.Get("q"))
	if err != nil {
		h.readErr(w, "ricerca nel codice", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handler) commits(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, ok := intParam(w, q.Get("page"), "page")
	if !ok {
		return
	}
	perPage, ok := intParam(w, q.Get("perPage"), "perPage")
	if !ok {
		return
	}
	res, err := h.Reads.Commits(r.Context(), r.PathValue("repoId"), gitread.CommitsQuery{
		Ref: q.Get("ref"), Author: q.Get("author"), Path: q.Get("path"), Page: page, PerPage: perPage,
	})
	if err != nil {
		h.readErr(w, "storico", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handler) commit(w http.ResponseWriter, r *http.Request) {
	ws, ok := boolParam(w, r.URL.Query().Get("ignoreWhitespace"), "ignoreWhitespace")
	if !ok {
		return
	}
	res, err := h.Reads.Commit(r.Context(), r.PathValue("repoId"), r.PathValue("sha"), ws)
	if err != nil {
		h.readErr(w, "dettaglio del commit", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handler) blame(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := h.Reads.Blame(r.Context(), r.PathValue("repoId"), q.Get("ref"), q.Get("path"))
	if err != nil {
		h.readErr(w, "blame", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// download manda il diff completo come allegato, in streaming: lo sha si
// risolve prima, così gli errori escono come JSON prima del primo byte.
func (h *handler) download(format string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws, ok := boolParam(w, r.URL.Query().Get("ignoreWhitespace"), "ignoreWhitespace")
		if !ok {
			return
		}
		d, err := h.Reads.PrepareDownload(r.Context(), r.PathValue("repoId"), r.PathValue("sha"), format, ws)
		if err != nil {
			h.readErr(w, "download del diff", err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+d.Filename+`"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if err := d.WriteTo(r.Context(), w); err != nil {
			// Le intestazioni sono già partite: si interrompe la risposta
			// perché il client non scambi un file tronco per completo.
			h.Logger.Error("download del diff interrotto", "sha", d.SHA, "err", err)
			panic(http.ErrAbortHandler)
		}
	}
}

func intParam(w http.ResponseWriter, v, name string) (int, bool) {
	if v == "" {
		return 0, true
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", name+" non è un intero.")
		return 0, false
	}
	return n, true
}

func boolParam(w http.ResponseWriter, v, name string) (bool, bool) {
	switch v {
	case "", "false", "0":
		return false, true
	case "true", "1":
		return true, true
	}
	writeErr(w, http.StatusBadRequest, "invalid_request", name+" deve essere true o false.")
	return false, false
}

func (h *handler) readErr(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, repostore.ErrInvalidID):
		writeErr(w, http.StatusBadRequest, "invalid_request", "repoId non è un UUID valido.")
	case errors.Is(err, repostore.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "Repo non trovato.")
	case errors.Is(err, gitref.ErrInvalidRef):
		writeErr(w, http.StatusBadRequest, "invalid_ref", err.Error())
	case errors.Is(err, gitref.ErrRefNotFound):
		writeErr(w, http.StatusNotFound, "ref_not_found", "Ref o commit non trovato.")
	case errors.Is(err, gitref.ErrInvalidPath):
		writeErr(w, http.StatusBadRequest, "invalid_path", err.Error())
	case errors.Is(err, gitref.ErrInvalidSHA):
		writeErr(w, http.StatusBadRequest, "invalid_request", "sha non è esadecimale (da 7 a 64 cifre).")
	case errors.Is(err, gitread.ErrInvalidInput):
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, gitread.ErrBlameUnavailable):
		writeErr(w, http.StatusBadRequest, "blame_unavailable", err.Error())
	case errors.Is(err, gitread.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "Percorso non trovato.")
	case errors.Is(err, gitrun.ErrOutputTooLarge):
		writeErr(w, http.StatusInternalServerError, "internal", "Risultato troppo grande.")
	default:
		h.Logger.Error("lettura non riuscita", "op", what, "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "Errore interno.")
	}
}
