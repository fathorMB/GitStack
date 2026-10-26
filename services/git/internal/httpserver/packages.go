package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/fathorMB/GitStack/services/git/internal/registry"
	"github.com/fathorMB/GitStack/services/git/internal/repostore"
)

// Packages è la parte del registry usata dall'API interna (GIT-361).
type Packages interface {
	ListPackages(ctx context.Context, repoID string) ([]registry.Package, error)
	DeletePackage(ctx context.Context, repoID, image, tag string) error
	// ImageRevision dà l'etichetta revision di un tag o digest (GIT-395), senza elencare il repo.
	ImageRevision(ctx context.Context, repoID, image, tag, digest string) (string, error)
}

func (h *handler) mountPackages(api *http.ServeMux) {
	api.HandleFunc("GET /internal/git/repos/{repoId}/packages/revision", h.imageRevision)
	api.HandleFunc("GET /internal/git/repos/{repoId}/packages", h.listPackages)
	api.HandleFunc("DELETE /internal/git/repos/{repoId}/packages", h.deletePackage)
}

// listPackages risponde {"items":[...]} con le immagini del repo. Chi può vederle l'ha già
// deciso core: qui non c'è alcuna identità da valutare, solo la firma del servizio.
func (h *handler) listPackages(w http.ResponseWriter, r *http.Request) {
	id, err := repostore.NormalizeID(r.PathValue("repoId"))
	if err != nil {
		h.storeErr(w, "pacchetti", err)
		return
	}
	items, err := h.Packages.ListPackages(r.Context(), id)
	if err != nil {
		h.packagesErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// imageRevision risponde {"revision":"…"} per ?image= (vuoto = immagine senza nome) e ?digest= o
// ?tag=. revision è vuota se l'etichetta manca o non è un commit; 404 se l'immagine o il tag non
// ci sono; 400 se manca sia digest sia tag.
func (h *handler) imageRevision(w http.ResponseWriter, r *http.Request) {
	id, err := repostore.NormalizeID(r.PathValue("repoId"))
	if err != nil {
		h.storeErr(w, "pacchetti", err)
		return
	}
	q := r.URL.Query()
	if q.Get("digest") == "" && q.Get("tag") == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request", "Serve digest o tag.")
		return
	}
	rev, err := h.Packages.ImageRevision(r.Context(), id, q.Get("image"), q.Get("tag"), q.Get("digest"))
	if err != nil {
		h.packagesErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"revision": rev})
}

// deletePackage elimina l'immagine ?image= (vuoto = immagine senza nome) o solo il suo ?tag=.
// 404 se non c'è, 409 se un tag è protetto, 400 se il nome non è valido.
func (h *handler) deletePackage(w http.ResponseWriter, r *http.Request) {
	id, err := repostore.NormalizeID(r.PathValue("repoId"))
	if err != nil {
		h.storeErr(w, "pacchetti", err)
		return
	}
	q := r.URL.Query()
	if err := h.Packages.DeletePackage(r.Context(), id, q.Get("image"), q.Get("tag")); err != nil {
		h.packagesErr(w, err)
		return
	}
	h.Logger.Info("pacchetto eliminato", "repoId", id, "image", q.Get("image"), "tag", q.Get("tag"))
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) packagesErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, registry.ErrPackageNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "Pacchetto non trovato.")
	case errors.Is(err, registry.ErrProtectedTag):
		writeErr(w, http.StatusConflict, "protected_tag", err.Error())
	case errors.Is(err, registry.ErrInvalidImage):
		writeErr(w, http.StatusBadRequest, "invalid_request", "Nome dell'immagine non valido.")
	default:
		h.Logger.Error("operazione sui pacchetti non riuscita", "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "Errore interno.")
	}
}
