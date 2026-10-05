package httpserver

import (
	"net/http"
	"strconv"
)

// Letture del codice (M-04/B): albero, file, README, raw, branch, tag e archivi.

func (h *handler) tree(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := h.Reads.Tree(r.Context(), r.PathValue("repoId"), q.Get("ref"), q.Get("path"))
	if err != nil {
		h.readErr(w, "albero", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handler) contents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := h.Reads.File(r.Context(), r.PathValue("repoId"), q.Get("ref"), q.Get("path"))
	if err != nil {
		h.readErr(w, "contenuto del file", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handler) readme(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := h.Reads.Readme(r.Context(), r.PathValue("repoId"), q.Get("ref"), q.Get("path"))
	if err != nil {
		h.readErr(w, "README", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handler) branches(w http.ResponseWriter, r *http.Request) {
	res, err := h.Reads.Branches(r.Context(), r.PathValue("repoId"))
	if err != nil {
		h.readErr(w, "branch", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handler) tags(w http.ResponseWriter, r *http.Request) {
	res, err := h.Reads.Tags(r.Context(), r.PathValue("repoId"))
	if err != nil {
		h.readErr(w, "tag", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// securityHeaders sono le intestazioni della regola B3: le scrive il
// servizio, né nginx né core le riscrivono.
func securityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
}

// raw manda i byte del file in streaming. Content-Type e disposizione sono
// decisi prima del primo byte; mai text/html né image/svg+xml.
func (h *handler) raw(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f, err := h.Reads.PrepareRaw(r.Context(), r.PathValue("repoId"), q.Get("ref"), q.Get("path"))
	if err != nil {
		h.readErr(w, "raw", err)
		return
	}
	securityHeaders(w)
	w.Header().Set("Content-Type", f.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(f.Size, 10))
	if f.Attachment {
		w.Header().Set("Content-Disposition", `attachment; filename="`+f.Filename+`"`)
	}
	if err := f.WriteTo(r.Context(), w); err != nil {
		h.Logger.Error("raw interrotto", "sha", f.SHA, "err", err)
		panic(http.ErrAbortHandler)
	}
}

func (h *handler) archive(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	a, err := h.Reads.PrepareArchive(r.Context(), r.PathValue("repoId"), q.Get("ref"), q.Get("format"), q.Get("name"))
	if err != nil {
		h.readErr(w, "archivio", err)
		return
	}
	securityHeaders(w)
	w.Header().Set("Content-Type", a.ContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+a.Filename+`"`)
	if err := a.WriteTo(r.Context(), w); err != nil {
		h.Logger.Error("archivio interrotto", "sha", a.SHA, "err", err)
		panic(http.ErrAbortHandler)
	}
}
