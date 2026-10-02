package identityclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// PermissionChecker chiede a identity se un utente ha almeno un ruolo su una
// risorsa (POST /internal/permissions/check). Implementato da Client.
type PermissionChecker interface {
	CheckPermission(ctx context.Context, userID, resourceID, role string) (bool, error)
}

// CheckPermission implementa PermissionChecker. allowed=false non è un
// errore. Errori di trasporto, 5xx, 401/403 (segreto sbagliato) e 404 sono
// ErrUnavailable: il gateway non decide da sé (mai fail open). Gli id non
// UUID non arrivano mai a identity: il chiamante li rifiuta prima.
func (c *Client) CheckPermission(ctx context.Context, userID, resourceID, role string) (bool, error) {
	var in CheckPermissionInput
	if err := in.UserId.UnmarshalText([]byte(userID)); err != nil {
		return false, fmt.Errorf("userId non valido: %w", err)
	}
	if err := in.ResourceId.UnmarshalText([]byte(resourceID)); err != nil {
		return false, fmt.Errorf("resourceId non valido: %w", err)
	}
	in.Role = ResourceRole(role)
	if !in.Role.Valid() {
		return false, fmt.Errorf("ruolo %q non valido", role)
	}
	body, err := json.Marshal(in)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.permissionsEndpoint, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.secret)

	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("%w: %s", ErrUnavailable, sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, fmt.Errorf("%w: lettura della risposta: %s", ErrUnavailable, sanitize(err))
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%w: /internal/permissions/check ha risposto %d", ErrUnavailable, resp.StatusCode)
	}
	var out CheckPermissionResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return false, fmt.Errorf("%w: risposta non valida", ErrUnavailable)
	}
	return out.Allowed, nil
}

var _ PermissionChecker = (*Client)(nil)
