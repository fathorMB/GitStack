package identityclient

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// AccessSource è una fonte del ruolo di un utente su una risorsa: kind è
// direct, team, owner, internal.
type AccessSource struct {
	Kind         string
	Role         string
	Organization string
	Team         string
}

// ResourceAccess è l'accesso effettivo di un utente a una risorsa.
type ResourceAccess struct {
	ResourceID uuid.UUID
	Role       string
	Sources    []AccessSource
}

// UserAccessResult è la risposta di POST /internal/permissions/user-access.
// Admin è vero per l'amministratore di sistema: Items è vuoto e ha admin su
// tutte le risorse.
type UserAccessResult struct {
	Admin bool
	Items []ResourceAccess
}

// UserAccessReader legge l'accesso effettivo di un utente
// (POST /internal/permissions/user-access). Implementato da Client; ErrNotFound
// se l'utente non esiste.
type UserAccessReader interface {
	UserAccess(ctx context.Context, userID uuid.UUID) (UserAccessResult, error)
}

// UserAccess implementa UserAccessReader. Risposte malformate e ogni stato
// diverso da 200 e 404 sono ErrUnavailable.
func (c *Client) UserAccess(ctx context.Context, userID uuid.UUID) (UserAccessResult, error) {
	var out struct {
		Admin *bool `json:"admin"`
		Items []struct {
			ResourceID string `json:"resourceId"`
			Role       string `json:"role"`
			Sources    []struct {
				Kind         string `json:"kind"`
				Role         string `json:"role"`
				Organization string `json:"organization"`
				Team         string `json:"team"`
			} `json:"sources"`
		} `json:"items"`
	}
	st, err := c.call(ctx, http.MethodPost, "/internal/permissions/user-access", map[string]string{"userId": userID.String()}, http.StatusOK, &out)
	if err != nil {
		return UserAccessResult{}, err
	}
	switch st {
	case http.StatusOK:
	case http.StatusNotFound:
		return UserAccessResult{}, ErrNotFound
	default:
		return UserAccessResult{}, fmt.Errorf("%w: l'accesso dell'utente ha risposto %d", ErrUnavailable, st)
	}
	if out.Admin == nil {
		return UserAccessResult{}, fmt.Errorf("%w: risposta senza il campo admin", ErrUnavailable)
	}
	res := UserAccessResult{Admin: *out.Admin, Items: make([]ResourceAccess, 0, len(out.Items))}
	for _, it := range out.Items {
		id, perr := uuid.Parse(it.ResourceID)
		if perr != nil {
			return UserAccessResult{}, fmt.Errorf("%w: id di risorsa non valido", ErrUnavailable)
		}
		ra := ResourceAccess{ResourceID: id, Role: it.Role, Sources: make([]AccessSource, 0, len(it.Sources))}
		for _, s := range it.Sources {
			ra.Sources = append(ra.Sources, AccessSource{Kind: s.Kind, Role: s.Role, Organization: s.Organization, Team: s.Team})
		}
		res.Items = append(res.Items, ra)
	}
	return res, nil
}

var _ UserAccessReader = (*Client)(nil)
