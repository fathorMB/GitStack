// Package api è il client di gs verso il gateway: sopra client/go (generato
// dall'OpenAPI) aggiunge base /api/v1, User-Agent gs/<versione>, token Bearer
// e la conversione delle risposte non-2xx negli errori di cmdutil.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	gitstack "github.com/fathorMB/GitStack/client/go"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// BasePath è il prefisso dell'API pubblica sul gateway.
const BasePath = "/api/v1"

// maxErrorBody limita quanto del corpo di un errore si legge.
const maxErrorBody = 1 << 20

// Client parla con il gateway di una istanza.
type Client struct {
	baseURL   string // https://host/api/v1 (senza slash finale)
	token     string
	userAgent string
	http      *http.Client
	gen       *gitstack.ClientWithResponses
}

// Options configura un Client.
type Options struct {
	Host    string // host[:porta]; con schema esplicito (http://...) si usa quello
	Token   string
	Version string // versione di gs, per lo User-Agent
	HTTP    *http.Client
}

// BaseURL costruisce la base dell'API per un host: https://<host>/api/v1.
// Un host con schema esplicito (http://localhost:8080) lo mantiene.
func BaseURL(host string) string {
	host = strings.TrimRight(strings.TrimSpace(host), "/")
	if !strings.Contains(host, "://") {
		host = "https://" + host
	}
	return host + BasePath
}

// UserAgent è `gs/<versione>`.
func UserAgent(version string) string {
	if version == "" {
		version = "dev"
	}
	return "gs/" + version
}

// New crea il client.
func New(o Options) (*Client, error) {
	if strings.TrimSpace(o.Host) == "" {
		return nil, errors.New("host dell'istanza mancante")
	}
	hc := o.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	c := &Client{
		baseURL:   BaseURL(o.Host),
		token:     o.Token,
		userAgent: UserAgent(o.Version),
		http:      hc,
	}
	gen, err := gitstack.NewClientWithResponses(c.baseURL,
		gitstack.WithHTTPClient(hc),
		gitstack.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			c.decorate(req)
			return nil
		}))
	if err != nil {
		return nil, err
	}
	c.gen = gen
	return c, nil
}

// FromFactory crea il client per l'istanza e il token risolti dalla Factory.
func FromFactory(f *cmdutil.Factory) (*Client, error) {
	host, err := f.Host()
	if err != nil {
		return nil, err
	}
	tok, err := f.Token(host)
	if err != nil {
		return nil, err
	}
	return New(Options{Host: host, Token: tok, Version: f.Version, HTTP: f.HTTPClient()})
}

// Generated è il client generato da OpenAPI, già con header e token.
func (c *Client) Generated() *gitstack.ClientWithResponses { return c.gen }

// BaseURL della istanza (https://host/api/v1).
func (c *Client) BaseURL() string { return c.baseURL }

func (c *Client) decorate(req *http.Request) {
	req.Header.Set("User-Agent", c.userAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
}

// Do è la chiamata grezza per le rotte che il client generato non copre (e per
// `gs api`). path è relativo alla base (/user, /repos/a/b?x=1). La risposta
// 2xx si restituisce intatta (chiudere il corpo); una non-2xx diventa errore.
func (c *Client) Do(ctx context.Context, method, path string, body io.Reader, header http.Header) (*http.Response, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	c.decorate(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, ErrorFromResponse(resp.StatusCode, b)
	}
	return resp, nil
}

// ErrorFromResponse converte una risposta non-2xx in *cmdutil.APIError,
// leggendo il formato errore unico ({"error":{"code","message"}}). Un corpo
// diverso (HTML di un proxy, vuoto) dà un errore con solo lo stato.
func ErrorFromResponse(status int, body []byte) error {
	ae := &cmdutil.APIError{Status: status}
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil {
		ae.Code, ae.Message = e.Error.Code, e.Error.Message
	}
	if ae.Message == "" {
		ae.Message = fmt.Sprintf("richiesta rifiutata dal gateway: %s", http.StatusText(status))
	}
	return ae
}

// CheckStatus è l'helper per le risposte del client generato: nil se lo
// stato è 2xx, altrimenti l'errore mappato.
func CheckStatus(status int, body []byte) error {
	if status >= 200 && status <= 299 {
		return nil
	}
	return ErrorFromResponse(status, body)
}
