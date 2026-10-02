package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/events"
	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"github.com/fathorMB/GitStack/services/core/internal/store"
	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

const (
	defaultPage    = 1
	defaultPerPage = 20
	maxPerPage     = 100
)

// ListResources implementa GET /resources.
func (s *apiServer) ListResources(w http.ResponseWriter, r *http.Request, params openapi.ListResourcesParams) {
	page := defaultPage
	if params.Page != nil {
		page = *params.Page
	}
	perPage := defaultPerPage
	if params.PerPage != nil {
		perPage = *params.PerPage
	}

	if page < 1 {
		writeError(w, http.StatusBadRequest, "invalid_page", "page deve essere >= 1.")
		return
	}
	if perPage < 1 || perPage > maxPerPage {
		writeError(w, http.StatusBadRequest, "invalid_per_page", "perPage deve essere tra 1 e 100.")
		return
	}
	if params.Type != nil && *params.Type == "" {
		writeError(w, http.StatusBadRequest, "invalid_type", "type, se presente, non può essere vuoto.")
		return
	}

	items, total, err := s.resources.List(r.Context(), params.Type, page, perPage)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante la lettura delle risorse.")
		return
	}

	out := make([]openapi.Resource, 0, len(items))
	for _, it := range items {
		out = append(out, toAPIResource(it))
	}

	writeJSON(w, http.StatusOK, openapi.ResourceList{
		Items:   out,
		Page:    page,
		PerPage: perPage,
		Total:   total,
	})
}

// CreateResource implementa POST /resources: crea la risorsa e pubblica
// l'evento di prova (nome/versione in internal/events, criterio aggiuntivo
// confermato dal CTO su GIT-5/GIT-6). La pubblicazione non blocca la
// risposta HTTP in caso di errore: la creazione è già avvenuta ed è quella
// l'operazione principale del contratto.
func (s *apiServer) CreateResource(w http.ResponseWriter, r *http.Request) {
	var body openapi.CreateResourceInput
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Corpo della richiesta non valido: "+err.Error())
		return
	}
	if body.Type == "" || body.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "type e name sono obbligatori.")
		return
	}

	// Chi crea la risorsa ne diventa admin: servono l'identità firmata dal
	// gateway e un identity configurato, altrimenti niente risorsa.
	caller, ok := trust.FromContext(r.Context())
	creatorID, perr := uuid.Parse(caller.UserID)
	if !ok || perr != nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "Identità del chiamante assente o non valida.")
		return
	}
	if s.grants == nil {
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non configurata: impossibile assegnare i permessi sulla nuova risorsa.")
		return
	}

	var attrs map[string]any
	if body.Attributes != nil {
		attrs = *body.Attributes
	}

	created, err := s.resources.Create(r.Context(), store.NewInput{
		Type:       body.Type,
		Name:       body.Name,
		Attributes: attrs,
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "conflict", "Esiste già una risorsa con questo type e name.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante la creazione della risorsa.")
		return
	}

	if err := s.grants.GrantResourceCreator(r.Context(), created.ID, creatorID); err != nil {
		slog.Default().Warn("grant admin al creatore non riuscito: la risorsa viene annullata",
			"resource_id", created.ID, "err", err)
		// Contesto proprio: quello della richiesta può essere già scaduto.
		dctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if derr := s.resources.Delete(dctx, created.ID); derr != nil {
			slog.Default().Error("cancellazione della risorsa senza grant non riuscita: risorsa orfana",
				"resource_id", created.ID, "err", derr)
		}
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "Identity non disponibile: la risorsa non è stata creata.")
		return
	}

	s.publishTestResourceCreated(r.Context(), created)

	w.Header().Set("Location", "/resources/"+created.ID.String())
	writeJSON(w, http.StatusCreated, toAPIResource(created))
}

// publishTestResourceCreated pubblica l'evento di prova in background con un
// timeout proprio, scollegato dal contesto della richiesta HTTP (già
// risposta al client): un bus lento o non raggiungibile non deve far
// scadere o fallire la richiesta di creazione.
func (s *apiServer) publishTestResourceCreated(_ context.Context, created store.Resource) {
	if s.events == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		payload := events.TestResourceCreatedPayload{
			ResourceID: created.ID.String(),
			Type:       created.Type,
			Name:       created.Name,
		}
		if err := s.events.Publish(ctx, events.TestResourceCreatedName, events.TestResourceCreatedVersion, payload); err != nil {
			slog.Default().Error("pubblicazione evento di prova non riuscita",
				"event_name", events.TestResourceCreatedName, "resource_id", created.ID, "err", err)
		}
	}()
}

// GetResource implementa GET /resources/{resourceId}.
func (s *apiServer) GetResource(w http.ResponseWriter, r *http.Request, resourceId openapi.ResourceIdParam) {
	res, err := s.resources.Get(r.Context(), uuid.UUID(resourceId))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Risorsa non trovata.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante la lettura della risorsa.")
		return
	}
	writeJSON(w, http.StatusOK, toAPIResource(res))
}

// UpdateResource implementa PATCH /resources/{resourceId}: aggiornamento
// parziale, solo i campi presenti nel corpo cambiano.
func (s *apiServer) UpdateResource(w http.ResponseWriter, r *http.Request, resourceId openapi.ResourceIdParam) {
	var body openapi.UpdateResourceInput
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Corpo della richiesta non valido: "+err.Error())
		return
	}
	if body.Name == nil && body.Attributes == nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Almeno un campo (name o attributes) deve essere presente.")
		return
	}
	if body.Name != nil && *body.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid_body", "name, se presente, non può essere vuoto.")
		return
	}

	res, err := s.resources.Update(r.Context(), uuid.UUID(resourceId), store.UpdateInput{
		Name:       body.Name,
		Attributes: body.Attributes,
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Risorsa non trovata.")
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "conflict", "Esiste già una risorsa con questo type e name.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante l'aggiornamento della risorsa.")
		return
	}
	writeJSON(w, http.StatusOK, toAPIResource(res))
}

// DeleteResource implementa DELETE /resources/{resourceId}.
func (s *apiServer) DeleteResource(w http.ResponseWriter, r *http.Request, resourceId openapi.ResourceIdParam) {
	err := s.resources.Delete(r.Context(), uuid.UUID(resourceId))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Risorsa non trovata.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Errore interno durante l'eliminazione della risorsa.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toAPIResource(r store.Resource) openapi.Resource {
	id := openapi.ResourceIdParam(r.ID)
	createdAt := r.CreatedAt
	updatedAt := r.UpdatedAt
	attrs := r.Attributes
	if attrs == nil {
		attrs = map[string]any{}
	}
	return openapi.Resource{
		Id:         &id,
		Type:       r.Type,
		Name:       r.Name,
		Attributes: &attrs,
		CreatedAt:  &createdAt,
		UpdatedAt:  &updatedAt,
	}
}
