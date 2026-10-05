// Package identityclient è il client di core verso gli endpoint /internal di
// identity (serviceAuth: Bearer con il segreto di servizio). Core non scrive
// nello schema identity: i grant passano da qui.
package identityclient

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

	"github.com/google/uuid"
)

// ErrUnavailable: identity non ha risposto o ha rifiutato la richiesta.
// Core non decide da sé: la creazione si annulla e risponde 503.
var ErrUnavailable = errors.New("identity non disponibile")

// CreatorGranter assegna il ruolo admin al creatore di una risorsa
// (POST /internal/resources/{resourceId}/grants/creator). Implementato da
// Client.
type CreatorGranter interface {
	GrantResourceCreator(ctx context.Context, resourceID, userID uuid.UUID) error
}

// ReadableLister elenca le risorse leggibili da un utente
// (POST /internal/permissions/readable-resources). all è vero solo per
// l'amministratore di sistema (ids vuoto: vede tutto). Implementato da
// Client.
type ReadableLister interface {
	ReadableResources(ctx context.Context, userID uuid.UUID) (all bool, ids []uuid.UUID, err error)
}

// Client chiama identity con il segreto di servizio.
type Client struct {
	base   string
	secret string
	http   *http.Client
}

// New crea il client per la base URL di identity. Il segreto non viene mai
// loggato.
func New(base *url.URL, secret string, timeout time.Duration) *Client {
	return &Client{
		base:   strings.TrimRight(base.String(), "/"),
		secret: secret,
		http:   &http.Client{Timeout: timeout},
	}
}

// GrantResourceCreator implementa CreatorGranter. Successo: 200 o 201.
// Errori di trasporto, timeout e qualunque altro stato (401 segreto
// sbagliato, 404 utente inesistente, 5xx) sono ErrUnavailable.
func (c *Client) GrantResourceCreator(ctx context.Context, resourceID, userID uuid.UUID) error {
	body, err := json.Marshal(map[string]string{"userId": userID.String()})
	if err != nil {
		return err
	}
	endpoint := c.base + "/internal/resources/" + resourceID.String() + "/grants/creator"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.secret)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("%w: il grant ha risposto %d", ErrUnavailable, resp.StatusCode)
	}
	return nil
}

// ReadableResources implementa ReadableLister. Errori di trasporto, timeout,
// stato diverso da 200 e risposte malformate (all mancante, id non uuid)
// sono ErrUnavailable: core non deve mai ripiegare su un elenco non filtrato.
func (c *Client) ReadableResources(ctx context.Context, userID uuid.UUID) (bool, []uuid.UUID, error) {
	body, err := json.Marshal(map[string]string{"userId": userID.String()})
	if err != nil {
		return false, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/internal/permissions/readable-resources", bytes.NewReader(body))
	if err != nil {
		return false, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.secret)

	resp, err := c.http.Do(req)
	if err != nil {
		return false, nil, fmt.Errorf("%w: %s", ErrUnavailable, sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return false, nil, fmt.Errorf("%w: l'elenco delle risorse leggibili ha risposto %d", ErrUnavailable, resp.StatusCode)
	}
	var out struct {
		All         *bool    `json:"all"`
		ResourceIDs []string `json:"resourceIds"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&out); err != nil {
		return false, nil, fmt.Errorf("%w: risposta non valida", ErrUnavailable)
	}
	if out.All == nil {
		return false, nil, fmt.Errorf("%w: risposta senza il campo all", ErrUnavailable)
	}
	ids := make([]uuid.UUID, 0, len(out.ResourceIDs))
	for _, raw := range out.ResourceIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return false, nil, fmt.Errorf("%w: id di risorsa non valido", ErrUnavailable)
		}
		ids = append(ids, id)
	}
	return *out.All, ids, nil
}

var (
	_ CreatorGranter = (*Client)(nil)
	_ ReadableLister = (*Client)(nil)
)

// sanitize toglie dall'errore l'URL (può contenere credenziali).
func sanitize(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}
