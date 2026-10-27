package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/fathorMB/GitStack/pkg/egress"
	"github.com/fathorMB/GitStack/services/core/internal/apps"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// Uscita delle app (v3-a, GIT-385, H16). L'amministratore dell'installazione apre destinazioni private
// precise (`GET/PUT /admin/apps/egress`); chi ha `admin` sull'app può restringere la sua uscita a un
// elenco (`GET/PUT/DELETE .../apps/{app}/egress`), mai oltre quanto l'amministratore permette. Le
// regole di fondo sono quelle di pkg/egress (C8): loopback, link-local, cluster, nodo e porte di
// Kubernetes non si aprono da nessuna parte.

// egressRefresher è implementato dal controller: riapplica la NetworkPolicy delle app.
type egressRefresher interface {
	RefreshEgress(ctx context.Context, ids []uuid.UUID) error
}

func (s *apiServer) egressPolicy() *egress.Policy {
	return apps.EgressPolicy(s.appEgress.ClusterCIDRs, s.appEgress.NodeAddresses, s.appEgress.Deny)
}

// refreshEgress riapplica la policy; un errore si registra e non cambia l'esito: le impostazioni sono
// scritte e il controller le riprende al prossimo deploy.
func (s *apiServer) refreshEgress(ctx context.Context, ids []uuid.UUID) {
	if f, ok := s.appCtl.(egressRefresher); ok {
		if err := f.RefreshEgress(context.WithoutCancel(ctx), ids); err != nil {
			slog.Warn("apps: policy di uscita non riapplicata", "err", err)
		}
	}
}

// egressDestinationError scrive il 422 di una voce rifiutata (vietata, non risolvibile, non valida).
func egressDestinationError(w http.ResponseWriter, field, entry string, err error) {
	details := map[string]any{"destination": entry}
	switch {
	case errors.Is(err, apps.ErrDestinationUnresolved):
		writeErrorDetails(w, http.StatusUnprocessableEntity, "destination_unresolved", "Il nome non si risolve in nessun indirizzo: "+entry+".", details)
	case errors.Is(err, egress.ErrBlocked):
		writeErrorDetails(w, http.StatusUnprocessableEntity, "destination_blocked", "Destinazione non ammessa: "+err.Error(), details)
	default:
		writeFieldError(w, field, field+": "+err.Error())
	}
}

func privateDestinationOut(d store.AppPrivateDestination) openapi.AppPrivateDestination {
	created := d.CreatedAt
	return openapi.AppPrivateDestination{Host: d.Host, Port: d.Port, Note: d.Note, CreatedAt: &created, CreatedBy: actorToAPI(d.CreatedBy)}
}

func privateDestinationsOut(list []store.AppPrivateDestination) []openapi.AppPrivateDestination {
	out := make([]openapi.AppPrivateDestination, 0, len(list))
	for _, d := range list {
		out = append(out, privateDestinationOut(d))
	}
	return out
}

// GetAdminAppsEgress implementa GET /admin/apps/egress.
func (s *apiServer) GetAdminAppsEgress(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireInstanceAdmin(w, r, "vedere le destinazioni private delle app"); !ok {
		return
	}
	list, err := s.resources.ListAppPrivateDestinations(r.Context())
	if err != nil {
		appInternalError(w, "lettura delle destinazioni private delle app", err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.AppsEgressSettings{PrivateDestinations: privateDestinationsOut(list)})
}

// UpdateAdminAppsEgress implementa PUT /admin/apps/egress: sostituisce l'elenco.
func (s *apiServer) UpdateAdminAppsEgress(w http.ResponseWriter, r *http.Request) {
	adminID, ok := s.requireInstanceAdmin(w, r, "modificare le destinazioni private delle app")
	if !ok {
		return
	}
	caller, _ := trust.FromContext(r.Context())
	var in openapi.AppsEgressSettingsInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	policy := s.egressPolicy()
	seen := map[string]bool{}
	var list []store.AppPrivateDestination
	for _, e := range in.PrivateDestinations {
		if e.Port < 1 || e.Port > 65535 {
			writeFieldError(w, "privateDestinations", "port: da 1 a 65535.")
			return
		}
		d, err := egress.NewDestinationPort(e.Host, uint16(e.Port))
		if err != nil {
			writeFieldError(w, "privateDestinations", "privateDestinations: "+err.Error())
			return
		}
		note := ""
		if e.Note != nil {
			note = strings.TrimSpace(*e.Note)
		}
		if len([]rune(note)) > 200 {
			writeFieldError(w, "privateDestinations", "note: al massimo 200 caratteri.")
			return
		}
		ips, err := apps.ResolveDestination(r.Context(), s.appEgress.Resolver, d)
		if err == nil {
			err = apps.CheckOpenable(policy, d, ips)
		}
		if err != nil {
			egressDestinationError(w, "privateDestinations", d.String(), err)
			return
		}
		if seen[d.String()] {
			continue
		}
		seen[d.String()] = true
		list = append(list, store.AppPrivateDestination{Host: d.Host, Port: int(d.Port), Note: note})
	}
	saved, err := s.resources.ReplaceAppPrivateDestinations(r.Context(), list, &store.AppActor{ID: adminID, Username: caller.Username, Type: "human"}, s.now())
	if err != nil {
		appInternalError(w, "salvataggio delle destinazioni private delle app", err)
		return
	}
	s.refreshEgress(r.Context(), nil)
	writeJSON(w, http.StatusOK, openapi.AppsEgressSettings{PrivateDestinations: privateDestinationsOut(saved)})
}

func (s *apiServer) appEgressOut(r *http.Request, a store.App) (openapi.AppEgress, error) {
	opened, err := s.resources.ListAppPrivateDestinations(r.Context())
	if err != nil {
		return openapi.AppEgress{}, err
	}
	rs, err := s.resources.GetAppEgressRestriction(r.Context(), a.ID)
	if err != nil {
		return openapi.AppEgress{}, err
	}
	out := openapi.AppEgress{Destinations: []openapi.AppEgressDestination{}, AllowedPrivate: privateDestinationsOut(opened)}
	if rs != nil {
		out.Restricted = true
		for _, d := range rs.Destinations {
			out.Destinations = append(out.Destinations, openapi.AppEgressDestination{Host: d.Host, Port: d.Port})
		}
		at := rs.UpdatedAt
		out.UpdatedAt, out.UpdatedBy = &at, actorToAPI(rs.UpdatedBy)
	}
	return out, nil
}

// GetAppEgress implementa GET .../apps/{app}/egress: `write`.
func (s *apiServer) GetAppEgress(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	rp, _, ok := s.appRepo(w, r, owner, repo, "write", false)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, rp, app)
	if !ok {
		return
	}
	out, err := s.appEgressOut(r, a)
	if err != nil {
		appInternalError(w, "lettura dell'uscita dell'app", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// SetAppEgress implementa PUT .../apps/{app}/egress: `admin`.
func (s *apiServer) SetAppEgress(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	rp, actor, ok := s.appRepo(w, r, owner, repo, "admin", true)
	if !ok {
		return
	}
	var in openapi.AppEgressInput
	if !decodeIssueJSON(w, r, &in, false) {
		return
	}
	a, ok := s.appFor(w, r, rp, app)
	if !ok {
		return
	}
	openedList, err := s.resources.ListAppPrivateDestinations(r.Context())
	if err != nil {
		appInternalError(w, "lettura delle destinazioni private delle app", err)
		return
	}
	policy := s.egressPolicy()
	opened := apps.OpenedAddrs(r.Context(), policy, s.appEgress.Resolver, openedList)
	seen := map[string]bool{}
	list := []store.AppEgressDestination{}
	for _, e := range in.Destinations {
		if e.Port < 1 || e.Port > 65535 {
			writeFieldError(w, "destinations", "port: da 1 a 65535.")
			return
		}
		d, err := egress.NewDestinationPort(e.Host, uint16(e.Port))
		if err != nil {
			writeFieldError(w, "destinations", "destinations: "+err.Error())
			return
		}
		ips, err := apps.ResolveDestination(r.Context(), s.appEgress.Resolver, d)
		if err == nil {
			err = apps.CheckAppDestination(policy, opened, d, ips)
		}
		if err != nil {
			egressDestinationError(w, "destinations", d.String(), err)
			return
		}
		if !seen[d.String()] {
			seen[d.String()] = true
			list = append(list, store.AppEgressDestination{Host: d.Host, Port: int(d.Port)})
		}
	}
	if err := s.resources.SetAppEgressRestriction(r.Context(), a.ID, list, actor.store(), s.now()); err != nil {
		appInternalError(w, "salvataggio dell'uscita dell'app", err)
		return
	}
	s.refreshEgress(r.Context(), []uuid.UUID{a.ID})
	out, err := s.appEgressOut(r, a)
	if err != nil {
		appInternalError(w, "lettura dell'uscita dell'app", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// DeleteAppEgress implementa DELETE .../apps/{app}/egress: `admin`, idempotente.
func (s *apiServer) DeleteAppEgress(w http.ResponseWriter, r *http.Request, owner openapi.RepoOwnerParam, repo openapi.RepoNameParam, app openapi.AppParam) {
	rp, _, ok := s.appRepo(w, r, owner, repo, "admin", true)
	if !ok {
		return
	}
	a, ok := s.appFor(w, r, rp, app)
	if !ok {
		return
	}
	if err := s.resources.DeleteAppEgressRestriction(r.Context(), a.ID); err != nil {
		appInternalError(w, "eliminazione dell'uscita dell'app", err)
		return
	}
	s.refreshEgress(r.Context(), []uuid.UUID{a.ID})
	w.WriteHeader(http.StatusNoContent)
}
