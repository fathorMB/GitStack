package httpserver

import (
	"context"
	"encoding/json"
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
	if _, ok := h.Packages.(ImageVerifier); ok {
		api.HandleFunc("POST /internal/git/repos/{repoId}/packages/verify", h.verifyImage)
	}
	if _, ok := h.Packages.(SigningKeyReader); ok {
		api.HandleFunc("GET /internal/git/registry/signing-key", h.signingKey)
	}
}

// SigningKeyReader dà la chiave pubblica con cui il registry firma le immagini della CI.
type SigningKeyReader interface {
	PublicKeyPEM() ([]byte, error)
}

// signingKey risponde {"publicKey":"<PEM>"}: core la mostra all'amministratore (H10).
func (h *handler) signingKey(w http.ResponseWriter, r *http.Request) {
	pem, err := h.Packages.(SigningKeyReader).PublicKeyPEM()
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "Firma del registry non configurata.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"publicKey": string(pem)})
}

// ImageVerifier è la verifica della firma cosign di un'immagine (H10, GIT-349); il registry la
// implementa, i fake dei test no.
type ImageVerifier interface {
	VerifyImage(ctx context.Context, repoID, image, tag, digest, repoRef string, trusted [][]byte) (registry.SignatureCheck, error)
}

// verifyImage risponde {"digest","signer","keyIndex"} per il corpo {image, tag, digest, repo,
// trustedKeys[]}: signer è «instance», «trusted» o vuoto. 404 se l'immagine o il tag non ci sono,
// 400 per un nome o una chiave non validi.
func (h *handler) verifyImage(w http.ResponseWriter, r *http.Request) {
	id, err := repostore.NormalizeID(r.PathValue("repoId"))
	if err != nil {
		h.storeErr(w, "pacchetti", err)
		return
	}
	var in struct {
		Image       string   `json:"image"`
		Tag         string   `json:"tag"`
		Digest      string   `json:"digest"`
		Repo        string   `json:"repo"`
		TrustedKeys []string `json:"trustedKeys"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil || in.Repo == "" || (in.Tag == "" && in.Digest == "") {
		writeErr(w, http.StatusBadRequest, "invalid_request", "Servono repo e tag o digest.")
		return
	}
	keys := make([][]byte, 0, len(in.TrustedKeys))
	for _, k := range in.TrustedKeys {
		keys = append(keys, []byte(k))
	}
	res, err := h.Packages.(ImageVerifier).VerifyImage(r.Context(), id, in.Image, in.Tag, in.Digest, in.Repo, keys)
	if errors.Is(err, registry.ErrInvalidTrustedKey) {
		writeErr(w, http.StatusBadRequest, "invalid_request", "Chiave fidata non valida.")
		return
	}
	if err != nil {
		h.packagesErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
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
