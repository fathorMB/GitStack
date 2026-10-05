package gitclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// maxJSONRead è il tetto di una risposta JSON di lettura (albero, storico,
// dettaglio di un commit con diff: già limitati dal servizio git).
const maxJSONRead = 64 << 20

// APIError è un rifiuto del servizio git su una lettura (404 o 400): porta il
// codice e il messaggio con cui git lo ha descritto. Il messaggio di git non
// contiene dati del repo (è un testo fisso o riprende il ref chiesto).
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("git ha risposto %d %s", e.Status, e.Code)
}

// Stream è una risposta di git da inoltrare byte per byte (raw, archivi,
// .diff e .patch). Chi la riceve deve chiudere Body.
type Stream struct {
	Header http.Header
	Body   io.ReadCloser
}

// Reader sono le letture del codice (M-04) dell'API interna di git. Le
// implementa Client; il tipo Git resta quello di M-03, così i fake esistenti
// non devono cambiare.
type Reader interface {
	// ReadJSON chiama GET /internal/git/repos/{repoId}/<path> e ritorna il
	// corpo JSON. Un 400 o 404 di git è un *APIError.
	ReadJSON(ctx context.Context, caller trust.Identity, repoID uuid.UUID, path string, q url.Values) (json.RawMessage, error)
	// OpenStream come ReadJSON ma senza leggere il corpo: nessun buffer, e
	// nessun timeout complessivo (vale il contesto della richiesta).
	OpenStream(ctx context.Context, caller trust.Identity, repoID uuid.UUID, path string, q url.Values) (*Stream, error)
}

func (c *Client) readRequest(ctx context.Context, caller trust.Identity, repoID uuid.UUID, path string, q url.Values) (*http.Request, error) {
	u := c.base + "/internal/git/repos/" + repoID.String() + "/" + strings.TrimLeft(path, "/")
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	trust.Sign(req.Header, c.secret, caller, c.now())
	return req, nil
}

// apiError trasforma una risposta non 200 di git.
func apiError(resp *http.Response, path string) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusBadRequest:
		var body struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &body)
		if body.Code == "" {
			body.Code = map[int]string{http.StatusNotFound: "not_found", http.StatusBadRequest: "invalid_request"}[resp.StatusCode]
		}
		return &APIError{Status: resp.StatusCode, Code: body.Code, Message: body.Message}
	}
	return fmt.Errorf("%w: GET %s ha risposto %d", ErrUnavailable, path, resp.StatusCode)
}

func transportError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return fmt.Errorf("%w: %s", ErrUnavailable, err)
}

// ReadJSON implementa Reader.
func (c *Client) ReadJSON(ctx context.Context, caller trust.Identity, repoID uuid.UUID, path string, q url.Values) (json.RawMessage, error) {
	req, err := c.readRequest(ctx, caller, repoID, path, q)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, transportError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp, path)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxJSONRead+1))
	if err != nil || len(data) > maxJSONRead {
		return nil, fmt.Errorf("%w: risposta non leggibile", ErrUnavailable)
	}
	return data, nil
}

// OpenStream implementa Reader.
func (c *Client) OpenStream(ctx context.Context, caller trust.Identity, repoID uuid.UUID, path string, q url.Values) (*Stream, error) {
	req, err := c.readRequest(ctx, caller, repoID, path, q)
	if err != nil {
		return nil, err
	}
	resp, err := c.stream.Do(req)
	if err != nil {
		return nil, transportError(err)
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		return nil, apiError(resp, path)
	}
	return &Stream{Header: resp.Header, Body: resp.Body}, nil
}

var _ Reader = (*Client)(nil)
