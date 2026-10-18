package httpapi

import (
	"net/http"
	"time"

	"github.com/fathorMB/GitStack/services/identity/internal/credevents"
	"github.com/fathorMB/GitStack/services/identity/internal/openapi"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// WithCredentialEvents abilita gli avvisi sulle credenziali (F13): gli eventi
// si scrivono a ogni cambio e core li legge da /internal/credential-events.
// Senza, i cambi non lasciano traccia e gli endpoint rispondono 501.
func WithCredentialEvents(c *credevents.Service) Option { return func(s *server) { s.cred = c } }

// credEvent registra un evento sulle credenziali di userID. Un errore non
// annulla il cambio già riuscito: si scrive nel log di sicurezza.
func (s *server) credEvent(r *http.Request, userID uuid.UUID, kind string, detail map[string]any) {
	if s.cred == nil {
		return
	}
	if err := s.cred.Record(r.Context(), userID, kind, detail); err != nil {
		s.log.Error("evento sulle credenziali non registrato", "kind", kind, "user", userID.String(), "err", err)
	}
}

// retentionOfDelivered: gli eventi consegnati si tengono 30 giorni.
const retentionOfDelivered = 30 * 24 * time.Hour

// ListCredentialEvents implementa GET /internal/credential-events.
func (s *server) ListCredentialEvents(w http.ResponseWriter, r *http.Request, p openapi.ListCredentialEventsParams) {
	if s.cred == nil {
		unavailable(w)
		return
	}
	limit := 100
	if p.Limit != nil {
		limit = *p.Limit
	}
	ctx := r.Context()
	if _, err := s.cred.ScanExpiring(ctx); err != nil {
		s.internal(w, r, err)
		return
	}
	list, err := s.cred.Pending(ctx, limit)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	items := make([]openapi.CredentialEvent, 0, len(list))
	for _, e := range list {
		items = append(items, openapi.CredentialEvent{
			Id: e.ID, UserId: openapi_types.UUID(e.UserID), Kind: openapi.CredentialEventKind(e.Kind),
			Detail: e.Detail, CreatedAt: e.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, openapi.CredentialEventList{Items: items})
}

// AckCredentialEvents implementa POST /internal/credential-events/ack.
func (s *server) AckCredentialEvents(w http.ResponseWriter, r *http.Request) {
	if s.cred == nil {
		unavailable(w)
		return
	}
	var in openapi.CredentialEventsAckInput
	if _, ok := decode(w, r, &in); !ok {
		return
	}
	if in.UpTo < 0 {
		validationFailed(w, map[string]string{"upTo": "non negativo"})
		return
	}
	if _, err := s.cred.Ack(r.Context(), in.UpTo); err != nil {
		s.internal(w, r, err)
		return
	}
	_, _ = s.cred.Purge(r.Context(), time.Now().Add(-retentionOfDelivered))
	w.WriteHeader(http.StatusNoContent)
}
