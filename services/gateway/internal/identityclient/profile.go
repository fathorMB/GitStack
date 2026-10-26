package identityclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Profile è nome, email e team di un utente (POST /internal/users/profile), per le intestazioni
// firmate del login davanti alle app (v3-a, GIT-347, H6).
type Profile struct {
	ID       string
	Username string
	Email    string
	// Teams sono «organizzazione/team».
	Teams  []string
	Active bool
}

// ProfileLookup legge il profilo di un utente. Implementato da Client.
type ProfileLookup interface {
	LookupProfile(ctx context.Context, userID string) (Profile, error)
}

// LookupProfile implementa ProfileLookup. Un utente che non esiste (404) è ErrUnavailable come
// ogni risposta inattesa: il login non decide da sé (mai fail open).
func (c *Client) LookupProfile(ctx context.Context, userID string) (Profile, error) {
	in := UserProfileInput{}
	if err := in.UserId.UnmarshalText([]byte(userID)); err != nil {
		return Profile{}, fmt.Errorf("userId non valido: %w", err)
	}
	body, err := json.Marshal(in)
	if err != nil {
		return Profile{}, err
	}
	endpoint := strings.TrimSuffix(c.endpoint, "/internal/verify") + "/internal/users/profile"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Profile{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.secret)
	resp, err := c.http.Do(req)
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %s", ErrUnavailable, sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Profile{}, fmt.Errorf("%w: lettura della risposta: %s", ErrUnavailable, sanitize(err))
	}
	if resp.StatusCode != http.StatusOK {
		return Profile{}, fmt.Errorf("%w: /internal/users/profile ha risposto %d", ErrUnavailable, resp.StatusCode)
	}
	var out UserProfileResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return Profile{}, fmt.Errorf("%w: risposta non valida", ErrUnavailable)
	}
	p := Profile{ID: out.Id.String(), Username: string(out.Username), Active: out.Active, Teams: out.Teams}
	if out.Email != nil {
		p.Email = *out.Email
	}
	return p, nil
}

var _ ProfileLookup = (*Client)(nil)
