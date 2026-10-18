package identityclient

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// CredentialEvent è un avviso sulle credenziali di un utente (F13):
// identity lo scrive quando cambia una password, nasce un token o una chiave
// SSH, o un token sta per scadere. Mai segreti.
type CredentialEvent struct {
	ID        int64
	UserID    uuid.UUID
	Kind      string
	Detail    map[string]any
	CreatedAt time.Time
}

// CredentialEvents legge fino a limit eventi non ancora confermati
// (GET /internal/credential-events). Errori e risposte malformate sono
// ErrUnavailable.
func (c *Client) CredentialEvents(ctx context.Context, limit int) ([]CredentialEvent, error) {
	var res struct {
		Items []struct {
			ID        int64          `json:"id"`
			UserID    string         `json:"userId"`
			Kind      string         `json:"kind"`
			Detail    map[string]any `json:"detail"`
			CreatedAt time.Time      `json:"createdAt"`
		} `json:"items"`
	}
	st, err := c.call(ctx, http.MethodGet, fmt.Sprintf("/internal/credential-events?limit=%d", limit), nil, http.StatusOK, &res)
	if err != nil {
		return nil, err
	}
	if st != http.StatusOK {
		return nil, fmt.Errorf("%w: la lettura degli eventi sulle credenziali ha risposto %d", ErrUnavailable, st)
	}
	out := make([]CredentialEvent, 0, len(res.Items))
	for _, it := range res.Items {
		id, perr := uuid.Parse(it.UserID)
		if perr != nil || it.Kind == "" {
			return nil, fmt.Errorf("%w: evento sulle credenziali non valido", ErrUnavailable)
		}
		out = append(out, CredentialEvent{ID: it.ID, UserID: id, Kind: it.Kind, Detail: it.Detail, CreatedAt: it.CreatedAt})
	}
	return out, nil
}

// AckCredentialEvents conferma gli eventi fino a upTo compreso
// (POST /internal/credential-events/ack).
func (c *Client) AckCredentialEvents(ctx context.Context, upTo int64) error {
	st, err := c.call(ctx, http.MethodPost, "/internal/credential-events/ack", map[string]int64{"upTo": upTo}, http.StatusNoContent, nil)
	if err != nil {
		return err
	}
	if st != http.StatusNoContent {
		return fmt.Errorf("%w: la conferma degli eventi sulle credenziali ha risposto %d", ErrUnavailable, st)
	}
	return nil
}
