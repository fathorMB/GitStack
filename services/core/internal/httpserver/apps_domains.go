package httpserver

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
)

// Domini propri delle app pubbliche (v3-c, GIT-458, ADR 0002 «Controllo del DNS»). Serve `admin`
// sull'app (H5). Solo un'app con accesso `public` e a interruttore d'istanza acceso ha domini.

// AppDomainsConfig configura i domini propri delle app pubbliche. InstanceHost è il dominio di GitStack
// (riservato, con i suoi sotto-domini, insieme al dominio delle app); Domains il controllo del DNS
// (resolver, grazia): il dominio delle app lo prende da WithAppsDomain.
type AppDomainsConfig struct {
	InstanceHost string
	Domains      apps.DomainConfig
	// CertRoots: radici con cui si chiude la catena di un certificato del cliente; nil usa quelle di
	// sistema (i test passano la propria CA).
	CertRoots *x509.CertPool
}

// WithAppDomains imposta i domini propri delle app: dominio di GitStack e controllo del DNS.
func WithAppDomains(c AppDomainsConfig) Option {
	return func(o *routerOptions) { o.appDomains = c }
}

// routeRefresher è la parte del controller che riallinea subito la rotta di un'app (dominio tolto o
// verificato). Se il controller manca, la rotta si riallinea al giro di sync.
type routeRefresher interface {
	RefreshRoute(ctx context.Context, app store.App)
}

type dnsAlt = struct {
	Type  openapi.AppDomainDnsRecordAlternativesType `json:"type"`
	Value string                                     `json:"value"`
}

func (s *apiServer) domainConfig() apps.DomainConfig {
	d := s.appDomains.Domains
	d.AppsDomain = s.appsDomain
	return d
}

func (s *apiServer) refreshRoute(ctx context.Context, a store.App) {
	if r, ok := s.appCtl.(routeRefresher); ok {
		r.RefreshRoute(ctx, a)
	}
}

func domainToAPI(dc apps.DomainConfig, a store.App, d store.AppDomain, publicIPs []string) openapi.AppDomain {
	rec := dc.ExpectedRecord(d.Normalized, a.AddressLabel, publicIPs)
	dns := openapi.AppDomainDnsRecord{Type: openapi.AppDomainDnsRecordType(rec.Type), Name: rec.Name, Value: rec.Value, Alternatives: make([]dnsAlt, 0, len(rec.Alternatives))}
	for _, alt := range rec.Alternatives {
		dns.Alternatives = append(dns.Alternatives, dnsAlt{Type: openapi.AppDomainDnsRecordAlternativesType(alt.Type), Value: alt.Value})
	}
	return openapi.AppDomain{
		Id:           d.ID,
		Domain:       d.Domain,
		Status:       openapi.AppDomainStatus(d.Status),
		StatusReason: d.StatusReason,
		Dns:          dns,
		Certificate: openapi.AppDomainCertificate{
			Mode: openapi.AppDomainCertificateMode(d.CertMode), State: openapi.AppDomainCertificateState(d.CertState),
			ValidFrom: d.CertValidFrom, ValidUntil: d.CertValidUntil, Issuer: d.CertIssuer, Error: d.CertError, LastAttemptAt: d.CertLastAttempt,
		},
		LastCheckedAt: d.LastChecked,
		VerifiedAt:    d.VerifiedAt,
		CreatedAt:     d.CreatedAt,
		CreatedBy:     actorToAPI(d.CreatedBy),
	}
}

// writeDomain risponde con il dominio nella forma del contratto.
func (s *apiServer) writeDomain(w http.ResponseWriter, r *http.Request, status int, a store.App, d store.AppDomain) {
	ips, err := s.resources.PublicIPs(r.Context())
	if err != nil {
		appInternalError(w, "lettura degli IP pubblici", err)
		return
	}
	writeJSON(w, status, domainToAPI(s.domainConfig(), a, d, ips))
}

// requirePublic: i domini si aggiungono e si ricontrollano solo su un'app pubblica a interruttore acceso.
func (s *apiServer) requirePublic(w http.ResponseWriter, r *http.Request, a store.App) bool {
	on, err := s.resources.PublicAppsEnabled(r.Context())
	if err != nil {
		appInternalError(w, "lettura delle app pubbliche", err)
		return false
	}
	if a.Access != "public" || !on {
		writeError(w, http.StatusConflict, "app_not_public", "I domini propri richiedono un'app con accesso public e le app pubbliche abilitate dall'amministratore.")
		return false
	}
	return true
}

// domainFor risolve il dominio dell'app dal path (nome normalizzato); 404 se l'app non lo ha.
func (s *apiServer) domainFor(w http.ResponseWriter, r *http.Request, a store.App, name string) (store.AppDomain, bool) {
	norm, err := apps.NormalizeDomain(name, "", "")
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Dominio non trovato.")
		return store.AppDomain{}, false
	}
	d, err := s.resources.GetAppDomain(r.Context(), a.ID, norm)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Dominio non trovato.")
		return store.AppDomain{}, false
	}
	if err != nil {
		appInternalError(w, "lettura del dominio", err)
		return store.AppDomain{}, false
	}
	return d, true
}

// ListAppDomains implementa GET /repos/{owner}/{repo}/apps/{app}/domains.
func (s *apiServer) ListAppDomains(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam) {
	repo, _, ok := s.appRepo(w, r, owner, name, "admin", false)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, repo, app)
	if !ok {
		return
	}
	ds, err := s.resources.ListAppDomains(r.Context(), a.ID)
	if err != nil {
		appInternalError(w, "lettura dei domini", err)
		return
	}
	ips, err := s.resources.PublicIPs(r.Context())
	if err != nil {
		appInternalError(w, "lettura degli IP pubblici", err)
		return
	}
	out := openapi.AppDomainList{Items: make([]openapi.AppDomain, 0, len(ds))}
	for _, d := range ds {
		out.Items = append(out.Items, domainToAPI(s.domainConfig(), a, d, ips))
	}
	writeJSON(w, http.StatusOK, out)
}

// AddAppDomain implementa POST /repos/{owner}/{repo}/apps/{app}/domains. Il dominio nasce pending_dns e
// il DNS si controlla subito: la risposta ha già lo stato.
func (s *apiServer) AddAppDomain(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam) {
	repo, actor, ok := s.appRepo(w, r, owner, name, "admin", true)
	if !ok {
		return
	}
	var in openapi.AppDomainInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIssueRequestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Corpo della richiesta non valido.")
		return
	}
	a, ok := s.appFor(w, r, repo, app)
	if !ok {
		return
	}
	if !s.requirePublic(w, r, a) {
		return
	}
	norm, err := apps.NormalizeDomain(in.Domain, s.appDomains.InstanceHost, s.appsDomain)
	switch {
	case errors.Is(err, apps.ErrDomainReserved):
		writeError(w, http.StatusUnprocessableEntity, "domain_reserved", err.Error())
		return
	case err != nil:
		writeError(w, http.StatusUnprocessableEntity, "invalid_domain", err.Error())
		return
	}
	d, err := s.resources.AddAppDomain(r.Context(), a.ID, norm, norm, actor.store(), s.now())
	if errors.Is(err, store.ErrDomainTaken) {
		writeError(w, http.StatusConflict, "domain_taken", "Il dominio "+norm+" è già usato da un'app.")
		return
	}
	if err != nil {
		appInternalError(w, "aggiunta del dominio", err)
		return
	}
	ips, _ := s.resources.PublicIPs(r.Context())
	// Il primo controllo è sincrono, con il resolver configurato: un dominio già puntato risulta subito verified.
	if upd, changed, err := apps.RecheckDomain(r.Context(), s.resources, s.domainConfig(), a, d, ips, slog.Default()); err == nil {
		d = upd
		if changed {
			s.refreshRoute(r.Context(), a)
		}
	}
	s.writeDomain(w, r, http.StatusCreated, a, d)
}

// GetAppDomain implementa GET .../domains/{domain}.
func (s *apiServer) GetAppDomain(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam, domain openapi.AppDomainParam) {
	repo, _, ok := s.appRepo(w, r, owner, name, "admin", false)
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
	s.writeDomain(w, r, http.StatusOK, a, d)
}

// RemoveAppDomain implementa DELETE .../domains/{domain}: toglie il dominio e la sua rotta.
func (s *apiServer) RemoveAppDomain(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam, domain openapi.AppDomainParam) {
	repo, _, ok := s.appRepo(w, r, owner, name, "admin", true)
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
	if err := s.resources.DeleteAppDomain(r.Context(), a.ID, d.Normalized); err != nil && !errors.Is(err, store.ErrNotFound) {
		appInternalError(w, "rimozione del dominio", err)
		return
	}
	// La rotta senza l'host del dominio si riscrive subito, senza aspettare il giro di sync.
	s.refreshRoute(r.Context(), a)
	w.WriteHeader(http.StatusNoContent)
}

// RecheckAppDomain implementa POST .../domains/{domain}/recheck: un esito negativo è 200 con lo stato.
func (s *apiServer) RecheckAppDomain(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, name openapi.RepoNameParam, app openapi.AppParam, domain openapi.AppDomainParam) {
	repo, _, ok := s.appRepo(w, r, owner, name, "admin", true)
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
	if !s.requirePublic(w, r, a) {
		return
	}
	ips, err := s.resources.PublicIPs(r.Context())
	if err != nil {
		appInternalError(w, "lettura degli IP pubblici", err)
		return
	}
	upd, changed, err := apps.RecheckDomain(r.Context(), s.resources, s.domainConfig(), a, d, ips, slog.Default())
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Dominio non trovato.")
		return
	}
	if err != nil {
		appInternalError(w, "controllo del dominio", err)
		return
	}
	if changed {
		s.refreshRoute(r.Context(), a)
	}
	s.writeDomain(w, r, http.StatusOK, a, upd)
}
