// Package upstream contiene i client del servizio git verso identity
// (/internal/verify, /internal/permissions/check, Bearer con il segreto di
// servizio) e verso core (GET /repos/{owner}/{repo} con l'identità firmata
// dell'utente, che applica da sé la lettura). Il segreto non finisce mai in
// un messaggio d'errore.
package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/access"
	"github.com/fathorMB/GitStack/services/git/internal/trust"
)

// Client chiama identity e core.
type Client struct {
	identity string
	core     string
	secret   string
	http     *http.Client
	now      func() time.Time
}

// New crea il client. Le due basi sono URL interni dei Service.
func New(identityURL, coreURL, secret string, timeout time.Duration) *Client {
	return &Client{
		identity: strings.TrimRight(identityURL, "/"),
		core:     strings.TrimRight(coreURL, "/"),
		secret:   secret,
		http:     &http.Client{Timeout: timeout},
		now:      time.Now,
	}
}

var (
	_ access.Identity = (*Client)(nil)
	_ access.Core     = (*Client)(nil)
)

func sanitize(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

// VerifyToken implementa access.Identity: solo i token personali (kind
// token); una sessione web non apre l'accesso git.
func (c *Client) VerifyToken(ctx context.Context, token string) (access.Principal, bool, error) {
	if token == "" || len(token) > 512 {
		return access.Principal{}, false, nil
	}
	body, _ := json.Marshal(map[string]string{"credential": token, "kind": "token"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.identity+"/internal/verify", bytes.NewReader(body))
	if err != nil {
		return access.Principal{}, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.secret)
	resp, err := c.http.Do(req)
	if err != nil {
		return access.Principal{}, false, errors.New(sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return access.Principal{}, false, fmt.Errorf("/internal/verify ha risposto %d", resp.StatusCode)
	}
	var out struct {
		Active    bool `json:"active"`
		Principal *struct {
			UserID     string   `json:"userId"`
			Username   string   `json:"username"`
			AuthMethod string   `json:"authMethod"`
			Scopes     []string `json:"scopes"`
		} `json:"principal"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return access.Principal{}, false, errors.New("risposta di verify non valida")
	}
	if !out.Active {
		return access.Principal{}, false, nil
	}
	if out.Principal == nil || out.Principal.UserID == "" {
		return access.Principal{}, false, errors.New("verify: active senza principal")
	}
	if out.Principal.AuthMethod != "token" {
		return access.Principal{}, false, nil
	}
	return access.Principal{UserID: out.Principal.UserID, Username: out.Principal.Username, Scopes: out.Principal.Scopes}, true, nil
}

// HasRole implementa access.Identity.
func (c *Client) HasRole(ctx context.Context, userID, resourceID, role string) (bool, error) {
	body, _ := json.Marshal(map[string]string{"userId": userID, "resourceId": resourceID, "role": role})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.identity+"/internal/permissions/check", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.secret)
	resp, err := c.http.Do(req)
	if err != nil {
		return false, errors.New(sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("permissions/check ha risposto %d", resp.StatusCode)
	}
	var out struct {
		Allowed *bool `json:"allowed"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.Allowed == nil {
		return false, errors.New("risposta di permissions/check non valida")
	}
	return *out.Allowed, nil
}

// ResolveRepo implementa access.Core: GET /repos/{owner}/{repo} firmato con
// l'identità dell'utente. 404 = inesistente, eliminato o non leggibile.
func (c *Client) ResolveRepo(ctx context.Context, caller trust.Identity, owner, name string) (string, error) {
	u := c.core + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	trust.Sign(req.Header, c.secret, caller, c.now())
	resp, err := c.http.Do(req)
	if err != nil {
		return "", errors.New(sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusBadRequest:
		return "", access.ErrNotFound
	default:
		return "", fmt.Errorf("core ha risposto %d", resp.StatusCode)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.ID == "" {
		return "", errors.New("risposta di core non valida")
	}
	return out.ID, nil
}
