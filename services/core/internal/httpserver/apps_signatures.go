package httpserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/gitclient"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Firme al deploy e deploy automatico da branch (v3-a, GIT-349, H7/H10).
//
// La CI firma con la chiave dell'istanza ogni immagine che pubblica con il token del job. Qui si
// decide che cosa farne: il deploy automatico accetta solo immagini firmate dalla CI di questo
// stesso repo; il deploy a mano accetta anche le altre, etichettate «non verificata»
// (Verified=false), a meno che l'amministratore abbia acceso «solo immagini firmate» (allora
// valgono anche le chiavi fidate aggiuntive). La verifica la fa il servizio git, che ha il registry.

// appSignerCI è il valore di AppImage.SignedBy per una firma della CI del repo: «owner/repo CI».
func appSignerCI(repo store.Repo) string { return repo.OwnerName + "/" + repo.Name + " CI" }

// instanceKeyNamespace dà un id stabile alla chiave dell'istanza, che non sta nel database.
var instanceKeyNamespace = uuid.MustParse("6f1d2a52-5a54-4c0e-9d0a-7b3c8f6e4a10")

// imageSignature è l'esito del controllo della firma di un'immagine.
type imageSignature struct {
	Verified bool
	// ByCI: firmata dalla CI di questo repo (chiave dell'istanza). Solo questa vale per il deploy automatico.
	ByCI     bool
	SignedBy string
	Digest   string
}

// checkSignature chiede al servizio git se l'immagine ha una firma valida. Con withTrusted
// valgono anche le chiavi fidate aggiuntive. Un'immagine esterna, mancante dal registry, o un git
// che non sa verificare danno «non verificata», non un errore; un git che non risponde sì.
func (s *apiServer) checkSignature(ctx context.Context, repo store.Repo, img store.AppImage, withTrusted bool) (imageSignature, error) {
	if img.Source != "registry" {
		return imageSignature{}, nil
	}
	iv, ok := s.git.(gitclient.ImageVerifier)
	if !ok {
		return imageSignature{}, nil
	}
	caller, ok := trust.FromContext(ctx)
	if !ok {
		return imageSignature{}, nil
	}
	image, tag, digest := s.registryParts(img.Reference, repo)
	if img.Digest != nil {
		digest = *img.Digest
	}
	if digest == "" && tag == "" {
		return imageSignature{}, nil
	}
	var keys []store.AppTrustedKey
	var pems []string
	if withTrusted {
		var err error
		if keys, err = s.resources.ListAppTrustedKeys(ctx); err != nil {
			return imageSignature{}, err
		}
		for _, k := range keys {
			pems = append(pems, k.PublicKey)
		}
	}
	res, err := iv.VerifyImage(ctx, caller, repo.ID, image, tag, digest, repo.OwnerName+"/"+repo.Name, pems)
	if errors.Is(err, gitclient.ErrNotFound) {
		return imageSignature{}, nil
	}
	if err != nil {
		return imageSignature{}, err
	}
	switch res.Signer {
	case gitclient.SignerInstance:
		return imageSignature{Verified: true, ByCI: true, SignedBy: appSignerCI(repo), Digest: res.Digest}, nil
	case gitclient.SignerTrusted:
		if res.KeyIndex >= 0 && res.KeyIndex < len(keys) {
			return imageSignature{Verified: true, SignedBy: keys[res.KeyIndex].Name, Digest: res.Digest}, nil
		}
	}
	return imageSignature{Digest: res.Digest}, nil
}

// verifyAppImage mette su img l'esito della verifica (Verified, SignedBy) e applica la regola
// «solo immagini firmate» dell'istanza (H10): se è accesa e l'immagine non è firmata risponde 422
// image_not_allowed. Se la verifica non è possibile e la regola è accesa risponde 503; altrimenti
// l'immagine resta «non verificata» e il deploy va avanti.
func (s *apiServer) verifyAppImage(w http.ResponseWriter, ctx context.Context, repo store.Repo, img store.AppImage) (store.AppImage, imageSignature, bool) {
	only, err := s.resources.AppSignedImagesOnly(ctx)
	if err != nil {
		appInternalError(w, "lettura di «solo immagini firmate»", err)
		return img, imageSignature{}, false
	}
	sig, err := s.checkSignature(ctx, repo, img, true)
	if err != nil {
		slog.Default().Warn("verifica della firma dell'immagine non riuscita", "err", err, "image", img.Reference)
		if only {
			writeError(w, http.StatusServiceUnavailable, "signature_check_unavailable", "Non riesco a verificare la firma dell'immagine: riprova fra poco.")
			return img, imageSignature{}, false
		}
		sig = imageSignature{}
	}
	if only && !sig.Verified {
		writeError(w, http.StatusUnprocessableEntity, "image_not_allowed", "L'istanza accetta solo immagini firmate: questa non lo è (dalla CI del repo o da una chiave fidata).")
		return img, sig, false
	}
	img.Verified = sig.Verified
	img.SignedBy = nil
	if sig.Verified {
		by := sig.SignedBy
		img.SignedBy = &by
	}
	return img, sig, true
}

// requireCISigned risponde 422 follow_branch_unsigned se l'immagine non è firmata dalla CI di
// questo repo: seguire un branch vuol dire che ogni deploy futuro lo sarà (H7, H10).
func (s *apiServer) requireCISigned(w http.ResponseWriter, ctx context.Context, repo store.Repo, img store.AppImage) bool {
	sig, err := s.checkSignature(ctx, repo, img, false)
	if err != nil {
		slog.Default().Warn("verifica della firma dell'immagine non riuscita", "err", err, "image", img.Reference)
		writeError(w, http.StatusServiceUnavailable, "signature_check_unavailable", "Non riesco a verificare la firma dell'immagine: riprova fra poco.")
		return false
	}
	if !sig.ByCI {
		writeError(w, http.StatusUnprocessableEntity, "follow_branch_unsigned", "Il deploy automatico accetta solo immagini firmate dalla CI di questo repo: questa immagine non lo è.")
		return false
	}
	return true
}

// --- chiavi fidate e impostazione «solo immagini firmate» (amministratore) ---

// parseTrustedKey legge una chiave pubblica cosign (ECDSA) e ne dà l'impronta: SHA-256 del DER.
func parseTrustedKey(pemText string) (fingerprint string, ok bool) {
	blk, _ := pem.Decode([]byte(pemText))
	if blk == nil || blk.Type != "PUBLIC KEY" {
		return "", false
	}
	k, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if err != nil {
		return "", false
	}
	if _, isEC := k.(*ecdsa.PublicKey); !isEC {
		return "", false
	}
	sum := sha256.Sum256(blk.Bytes)
	return hex.EncodeToString(sum[:]), true
}

func trustedKeyToAPI(k store.AppTrustedKey) openapi.AppTrustedKey {
	pk := k.PublicKey
	return openapi.AppTrustedKey{Id: openapi_types.UUID(k.ID), Name: k.Name, Fingerprint: k.Fingerprint, PublicKey: &pk,
		InstanceKey: false, CreatedAt: k.CreatedAt, CreatedBy: actorToAPI(k.CreatedBy)}
}

// instanceKey è la chiave della CI dell'istanza, letta dal servizio git (sempre presente, H10).
func (s *apiServer) instanceKey(ctx context.Context, caller trust.Identity) (openapi.AppTrustedKey, error) {
	iv, ok := s.git.(gitclient.ImageVerifier)
	if !ok {
		return openapi.AppTrustedKey{}, errors.New("il servizio git non sa dare la chiave di firma")
	}
	pemText, err := iv.InstanceSigningKey(ctx, caller)
	if err != nil {
		return openapi.AppTrustedKey{}, err
	}
	fp, ok := parseTrustedKey(pemText)
	if !ok {
		return openapi.AppTrustedKey{}, errors.New("chiave di firma dell'istanza non valida")
	}
	return openapi.AppTrustedKey{Id: openapi_types.UUID(uuid.NewSHA1(instanceKeyNamespace, []byte(fp))), Name: "Chiave della CI dell'istanza",
		Fingerprint: fp, PublicKey: &pemText, InstanceKey: true, CreatedAt: time.Unix(0, 0).UTC()}, nil
}

// ListAdminAppTrustedKeys implementa GET /admin/apps/trusted-keys: prima la chiave dell'istanza.
func (s *apiServer) ListAdminAppTrustedKeys(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireInstanceAdmin(w, r, "vedere le chiavi fidate"); !ok {
		return
	}
	caller, _ := trust.FromContext(r.Context())
	inst, err := s.instanceKey(r.Context(), caller)
	if err != nil {
		slog.Default().Error("chiave di firma dell'istanza non leggibile", "err", err)
		writeError(w, http.StatusServiceUnavailable, "signing_key_unavailable", "La chiave di firma dell'istanza non è disponibile: il servizio git non risponde o il registry non firma.")
		return
	}
	keys, err := s.resources.ListAppTrustedKeys(r.Context())
	if err != nil {
		appInternalError(w, "elenco delle chiavi fidate", err)
		return
	}
	out := openapi.AppTrustedKeyList{Items: []openapi.AppTrustedKey{inst}}
	for _, k := range keys {
		out.Items = append(out.Items, trustedKeyToAPI(k))
	}
	writeJSON(w, http.StatusOK, out)
}

// AddAdminAppTrustedKey implementa POST /admin/apps/trusted-keys.
func (s *apiServer) AddAdminAppTrustedKey(w http.ResponseWriter, r *http.Request) {
	adminID, ok := s.requireInstanceAdmin(w, r, "aggiungere chiavi fidate")
	if !ok {
		return
	}
	var in openapi.AppTrustedKeyInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 100 {
		writeFieldError(w, "name", "name: da 1 a 100 caratteri.")
		return
	}
	fp, valid := parseTrustedKey(in.PublicKey)
	if !valid {
		writeError(w, http.StatusUnprocessableEntity, "invalid_key", "Non è una chiave pubblica cosign valida: serve una chiave ECDSA in PEM («-----BEGIN PUBLIC KEY-----»).")
		return
	}
	caller, _ := trust.FromContext(r.Context())
	if inst, err := s.instanceKey(r.Context(), caller); err == nil && inst.Fingerprint == fp {
		writeError(w, http.StatusConflict, "key_exists", "È già la chiave della CI dell'istanza.")
		return
	}
	k, err := s.resources.AddAppTrustedKey(r.Context(), store.AppTrustedKey{
		ID: uuid.New(), Name: name, Fingerprint: fp, PublicKey: strings.TrimSpace(in.PublicKey) + "\n", CreatedAt: s.now(),
		CreatedBy: &store.AppActor{ID: adminID, Username: caller.Username, Type: "human"},
	})
	if errors.Is(err, store.ErrAppTrustedKeyExists) {
		writeError(w, http.StatusConflict, "key_exists", "Questa chiave è già fidata.")
		return
	}
	if err != nil {
		appInternalError(w, "aggiunta della chiave fidata", err)
		return
	}
	writeJSON(w, http.StatusCreated, trustedKeyToAPI(k))
}

// DeleteAdminAppTrustedKey implementa DELETE /admin/apps/trusted-keys/{keyId}.
func (s *apiServer) DeleteAdminAppTrustedKey(w http.ResponseWriter, r *http.Request, keyId openapi.AppTrustedKeyIdParam) {
	if _, ok := s.requireInstanceAdmin(w, r, "togliere chiavi fidate"); !ok {
		return
	}
	caller, _ := trust.FromContext(r.Context())
	if inst, err := s.instanceKey(r.Context(), caller); err == nil && uuid.UUID(inst.Id) == uuid.UUID(keyId) {
		writeError(w, http.StatusConflict, "instance_key", "La chiave della CI dell'istanza non si toglie.")
		return
	}
	err := s.resources.DeleteAppTrustedKey(r.Context(), uuid.UUID(keyId))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Chiave non trovata.")
		return
	}
	if err != nil {
		appInternalError(w, "eliminazione della chiave fidata", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
