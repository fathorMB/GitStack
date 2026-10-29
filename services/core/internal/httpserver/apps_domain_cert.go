package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Certificati dei domini propri (v3-c, GIT-460). La chiave privata entra dal corpo della PUT, si valida,
// si cifra nello store e arriva nel Secret del namespace Traefik: non è mai nella risposta, nei log,
// negli eventi. Serve `admin` sull'app (H5).

// maxCertificateRequestBytes: certificato (64 KiB) + chiave (16 KiB) + JSON.
const maxCertificateRequestBytes = 96 << 10

// customCertSyncer è la parte del controller che riallinea il cluster ai certificati del cliente. Senza
// controller il riallineamento avviene al giro periodico.
type customCertSyncer interface {
	SyncCustomCerts(ctx context.Context)
}

func (s *apiServer) syncCustomCerts(ctx context.Context) {
	if c, ok := s.appCtl.(customCertSyncer); ok {
		c.SyncCustomCerts(ctx)
	}
}

// UploadAppDomainCertificate implementa PUT .../domains/{domain}/certificate.
func (s *apiServer) UploadAppDomainCertificate(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repoName openapi.RepoNameParam, app openapi.AppParam, domain openapi.AppDomainParam) {
	repo, _, ok := s.appRepo(w, r, owner, repoName, "admin", true)
	if !ok {
		return
	}
	var in openapi.AppDomainCertificateInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCertificateRequestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Corpo della richiesta non valido.")
		return
	}
	if !s.hooks.Keys.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "app_secrets_unavailable", "I segreti non sono configurati su questo server (GITSTACK_WEBHOOK_SECRET_KEY).")
		return
	}
	a, ok := s.appFor(w, r, repo, app)
	if !ok {
		return
	}
	d, ok := s.domainFor(w, r, a, domain)
	if !ok {
		return
	}
	if !s.requirePublic(w, r, a) {
		return
	}
	var key string
	if in.PrivateKey != nil {
		key = *in.PrivateKey
	}
	info, err := apps.ValidateCustomCertificate(d.Normalized, in.Certificate, key, apps.CertCheckOptions{Now: s.now(), Roots: s.appDomains.CertRoots})
	if errors.Is(err, apps.ErrCertificateInvalid) {
		writeError(w, http.StatusUnprocessableEntity, "certificate_invalid", err.Error())
		return
	}
	if err != nil {
		appInternalError(w, "controllo del certificato", err)
		return
	}
	upd, err := s.resources.SetAppDomainCustomCert(r.Context(), s.hooks.Keys, d.ID, store.CustomCertInput{
		ChainPEM: info.ChainPEM, KeyPEM: info.KeyPEM, ValidFrom: info.ValidFrom, ValidUntil: info.ValidUntil, Issuer: info.Issuer,
	}, s.now())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Dominio non trovato.")
		return
	}
	if err != nil {
		appInternalError(w, "salvataggio del certificato", err)
		return
	}
	s.syncCustomCerts(r.Context())
	s.refreshRoute(r.Context(), a)
	s.writeDomain(w, r, http.StatusOK, a, upd)
}

// RemoveAppDomainCertificate implementa DELETE .../domains/{domain}/certificate: torna a Let's Encrypt.
func (s *apiServer) RemoveAppDomainCertificate(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repoName openapi.RepoNameParam, app openapi.AppParam, domain openapi.AppDomainParam) {
	repo, _, ok := s.appRepo(w, r, owner, repoName, "admin", true)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, repo, app)
	if !ok {
		return
	}
	d, ok := s.domainFor(w, r, a, domain)
	if !ok {
		return
	}
	upd, err := s.resources.ClearAppDomainCustomCert(r.Context(), d.ID)
	if errors.Is(err, store.ErrNotCustom) {
		writeError(w, http.StatusConflict, "not_custom", "Il dominio usa già Let's Encrypt: non c'è un certificato caricato da togliere.")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Dominio non trovato.")
		return
	}
	if err != nil {
		appInternalError(w, "rimozione del certificato", err)
		return
	}
	s.syncCustomCerts(r.Context())
	s.refreshRoute(r.Context(), a)
	s.writeDomain(w, r, http.StatusOK, a, upd)
}
