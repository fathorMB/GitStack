package httpserver

import (
	"context"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/google/uuid"
)

// Indirizzo pubblico e elenco admin delle app pubbliche (v3-c, GIT-473).

// publicAddress è l'indirizzo con cui l'app si apre: il dominio proprio verificato più vecchio, ma solo
// se l'app è `public` e l'interruttore d'istanza è acceso (altrimenti la rotta del dominio non c'è);
// altrimenti l'indirizzo automatico. domains è nell'ordine di creazione.
func (s *apiServer) publicAddress(a store.App, publicOn bool, domains []store.AppDomain) *string {
	if a.Access == "public" && publicOn {
		for _, d := range domains {
			if d.Status == store.DomainVerified {
				addr := "https://" + d.Normalized
				return &addr
			}
		}
	}
	return s.appAddress(a.AddressLabel)
}

// appToAPIWith è appToAPI con publicAddress calcolato dai domini dati.
func (s *apiServer) appToAPIWith(a store.App, publicOn bool, domains []store.AppDomain) openapi.App {
	out := s.appToAPI(a)
	out.PublicAddress = s.publicAddress(a, publicOn, domains)
	return out
}

// appDomainsOf dà i domini dell'app solo quando servono a publicAddress (app pubblica).
func (s *apiServer) appDomainsOf(ctx context.Context, a store.App, publicOn bool) ([]store.AppDomain, error) {
	if a.Access != "public" || !publicOn {
		return nil, nil
	}
	return s.resources.ListAppDomains(ctx, a.ID)
}

// appResponse costruisce l'App del contratto con publicAddress; su errore risponde 500 e dà ok falso.
func (s *apiServer) appResponse(w http.ResponseWriter, r *http.Request, a store.App) (openapi.App, bool) {
	publicOn, err := s.resources.PublicAppsEnabled(r.Context())
	if err != nil {
		appInternalError(w, "lettura delle app pubbliche", err)
		return openapi.App{}, false
	}
	ds, err := s.appDomainsOf(r.Context(), a, publicOn)
	if err != nil {
		appInternalError(w, "lettura dei domini", err)
		return openapi.App{}, false
	}
	return s.appToAPIWith(a, publicOn, ds), true
}

// ListAdminPublicApps implementa GET /admin/apps/public: solo l'amministratore dell'installazione.
// Con l'interruttore d'istanza spento le app non sono esposte e l'elenco è vuoto.
func (s *apiServer) ListAdminPublicApps(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireInstanceAdmin(w, r, "vedere le app pubbliche"); !ok {
		return
	}
	out := openapi.AdminPublicAppList{Items: []openapi.AdminPublicApp{}}
	publicOn, err := s.resources.PublicAppsEnabled(r.Context())
	if err != nil {
		appInternalError(w, "lettura delle app pubbliche", err)
		return
	}
	if !publicOn {
		writeJSON(w, http.StatusOK, out)
		return
	}
	list, err := s.resources.ListPublicApps(r.Context())
	if err != nil {
		appInternalError(w, "elenco delle app pubbliche", err)
		return
	}
	all, err := s.resources.ListAppDomainsForCheck(r.Context())
	if err != nil {
		appInternalError(w, "lettura dei domini", err)
		return
	}
	byApp := map[uuid.UUID][]store.AppDomain{}
	for _, d := range all {
		byApp[d.AppID] = append(byApp[d.AppID], d)
	}
	for _, a := range list {
		ds := byApp[a.ID]
		status, _ := a.Effective()
		row := openapi.AdminPublicApp{
			Owner: a.Owner, Repo: a.RepoName, Name: a.Name, Status: openapi.AppStatus(status),
			Address: s.appAddress(a.AddressLabel), PublicAddress: s.publicAddress(a, publicOn, ds),
			Domains: make([]openapi.AdminPublicAppDomain, 0, len(ds)),
		}
		for _, d := range ds {
			row.Domains = append(row.Domains, openapi.AdminPublicAppDomain{
				Domain: d.Domain, Status: openapi.AppDomainStatus(d.Status), StatusReason: d.StatusReason,
				Certificate: openapi.AppDomainCertificate{
					Mode: openapi.AppDomainCertificateMode(d.CertMode), State: openapi.AppDomainCertificateState(d.CertState),
					ValidFrom: d.CertValidFrom, ValidUntil: d.CertValidUntil, Issuer: d.CertIssuer, Error: d.CertError, LastAttemptAt: d.CertLastAttempt,
				},
			})
		}
		out.Items = append(out.Items, row)
	}
	out.Total = len(out.Items)
	writeJSON(w, http.StatusOK, out)
}
