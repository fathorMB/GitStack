// Package gitclient è il client di core verso l'API interna del servizio git
// (tag git-internal del contratto, D-E di GIT-63). Ogni chiamata porta gli
// header X-Gitstack-* firmati con il segreto di servizio (stesso schema di
// internal/trust), con l'identità di chi ha fatto la richiesta a core.
package gitclient

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

	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

var (
	// ErrNotFound: il repo non esiste su disco.
	ErrNotFound = errors.New("repo non trovato nel servizio git")
	// ErrConflict: il repo esiste già (creazione) o lo stato non lo permette.
	ErrConflict = errors.New("conflitto nel servizio git")
	// ErrInvalid: il servizio git ha rifiutato i dati (400).
	ErrInvalid = errors.New("richiesta rifiutata dal servizio git")
	// ErrUnavailable: git non ha risposto o ha risposto con un errore.
	ErrUnavailable = errors.New("servizio git non disponibile")
)

// Author è l'autore del primo commit (R5).
type Author struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// CreateInput è il corpo di POST /internal/git/repos.
type CreateInput struct {
	RepoID            uuid.UUID `json:"repoId"`
	Name              string    `json:"name"`
	Description       string    `json:"description,omitempty"`
	DefaultBranch     string    `json:"defaultBranch,omitempty"`
	Readme            bool      `json:"readme,omitempty"`
	GitignoreTemplate string    `json:"gitignoreTemplate,omitempty"`
	LicenseTemplate   string    `json:"licenseTemplate,omitempty"`
	LicenseHolder     string    `json:"licenseHolder,omitempty"`
	Author            Author    `json:"author"`
}

// State è lo stato di un repo su disco.
type State struct {
	Trashed bool
	// Empty: nessun commit.
	Empty bool
	// Branches sono i branch esistenti (refs/heads), in ordine alfabetico.
	Branches []string
}

// Git è l'API interna di git usata dagli handler. Implementata da Client.
type Git interface {
	Create(ctx context.Context, caller trust.Identity, in CreateInput) (empty bool, err error)
	Get(ctx context.Context, caller trust.Identity, repoID uuid.UUID) (State, error)
	Trash(ctx context.Context, caller trust.Identity, repoID uuid.UUID) error
	Delete(ctx context.Context, caller trust.Identity, repoID uuid.UUID) error
	Restore(ctx context.Context, caller trust.Identity, repoID uuid.UUID) error
}

// Client chiama il servizio git.
type Client struct {
	base   string
	secret string
	http   *http.Client
	now    func() time.Time
}

// New crea il client per la base URL interna di git. Il segreto non viene mai
// loggato.
func New(base *url.URL, secret string, timeout time.Duration) *Client {
	return &Client{
		base:   strings.TrimRight(base.String(), "/"),
		secret: secret,
		http:   &http.Client{Timeout: timeout},
		now:    time.Now,
	}
}

func (c *Client) do(ctx context.Context, caller trust.Identity, method, path string, body any, out any, okStatus ...int) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	trust.Sign(req.Header, c.secret, caller, c.now())

	resp, err := c.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("%w: %s", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	for _, s := range okStatus {
		if resp.StatusCode == s {
			if out != nil {
				if err := json.Unmarshal(data, out); err != nil {
					return fmt.Errorf("%w: risposta non valida", ErrUnavailable)
				}
			}
			return nil
		}
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusConflict:
		return ErrConflict
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s", ErrInvalid, strings.TrimSpace(string(data)))
	}
	return fmt.Errorf("%w: %s %s ha risposto %d", ErrUnavailable, method, path, resp.StatusCode)
}

// Create implementa Git.
func (c *Client) Create(ctx context.Context, caller trust.Identity, in CreateInput) (bool, error) {
	var out struct {
		Empty bool `json:"empty"`
	}
	if err := c.do(ctx, caller, http.MethodPost, "/internal/git/repos", in, &out, http.StatusCreated); err != nil {
		return false, err
	}
	return out.Empty, nil
}

// Get implementa Git.
func (c *Client) Get(ctx context.Context, caller trust.Identity, repoID uuid.UUID) (State, error) {
	var out struct {
		Trashed  bool     `json:"trashed"`
		Empty    bool     `json:"empty"`
		Branches []string `json:"branches"`
	}
	if err := c.do(ctx, caller, http.MethodGet, "/internal/git/repos/"+repoID.String(), nil, &out, http.StatusOK); err != nil {
		return State{}, err
	}
	return State{Trashed: out.Trashed, Empty: out.Empty, Branches: out.Branches}, nil
}

// Trash implementa Git.
func (c *Client) Trash(ctx context.Context, caller trust.Identity, repoID uuid.UUID) error {
	return c.do(ctx, caller, http.MethodPost, "/internal/git/repos/"+repoID.String()+"/trash", nil, nil, http.StatusNoContent)
}

// Restore implementa Git.
func (c *Client) Restore(ctx context.Context, caller trust.Identity, repoID uuid.UUID) error {
	return c.do(ctx, caller, http.MethodPost, "/internal/git/repos/"+repoID.String()+"/restore", nil, nil, http.StatusNoContent)
}

// Delete implementa Git (solo dal cestino).
func (c *Client) Delete(ctx context.Context, caller trust.Identity, repoID uuid.UUID) error {
	return c.do(ctx, caller, http.MethodDelete, "/internal/git/repos/"+repoID.String(), nil, nil, http.StatusNoContent)
}

var _ Git = (*Client)(nil)
