package identityclient

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// OrgOwnerLister elenca gli owner di un'organizzazione
// (GET /internal/orgs/{orgId}/owners): gestiscono i webhook di organizzazione
// (C6) e ricevono la notifica di disattivazione (C7).
type OrgOwnerLister interface {
	OrgOwners(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error)
}

// OrgOwners implementa OrgOwnerLister; ErrNotFound se l'organizzazione non esiste.
func (c *Client) OrgOwners(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	var res struct {
		Owners []string `json:"owners"`
	}
	st, err := c.call(ctx, http.MethodGet, "/internal/orgs/"+orgID.String()+"/owners", nil, http.StatusOK, &res)
	if err != nil {
		return nil, err
	}
	switch st {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrNotFound
	default:
		return nil, fmt.Errorf("%w: l'elenco degli owner ha risposto %d", ErrUnavailable, st)
	}
	out := make([]uuid.UUID, 0, len(res.Owners))
	for _, s := range res.Owners {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("%w: owner non valido", ErrUnavailable)
		}
		out = append(out, id)
	}
	return out, nil
}

var _ OrgOwnerLister = (*Client)(nil)
