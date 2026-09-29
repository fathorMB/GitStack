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
)

// Kind è il tipo di credenziale, se il gateway lo sa dal modo in cui è
// arrivata (cookie o header Authorization).
type Kind string

const (
	KindSession Kind = "session"
	KindToken   Kind = "token"
)

// ErrUnavailable: identity non ha risposto o ha risposto in modo inatteso.
// Il gateway non decide da sé: risponde 503 (mai fail open).
var ErrUnavailable = errors.New("identity non disponibile")

// Verifier verifica una credenziale. Implementato dal Client (chiamata a
// identity) e da Cache (che lo avvolge).
type Verifier interface {
	Verify(ctx context.Context, credential string, kind Kind) (Result, error)
}

// Result è l'esito della verifica. Principal è valorizzato solo se Active.
type Result struct {
	Active    bool
	Principal Caller
	// TTL suggerito da identity per la cache (0 = nessun suggerimento).
	TTL time.Duration
}

// Caller è chi ha presentato la credenziale (vedi schema Principal del
// contratto).
type Caller struct {
	UserID             string
	Username           string
	IsAdmin            bool
	AuthMethod         string
	Scopes             []string
	MustChangePassword bool
	ExpiresAt          *time.Time
	// IsToken: la credenziale è un token personale (ha scope); false per le
	// sessioni web, che non hanno scope.
	IsToken bool
}

// Client chiama POST /internal/verify di identity, autenticandosi con il
// segreto di servizio.
type Client struct {
	endpoint string
	secret   string
	http     *http.Client
}

// NewClient crea il client per la base URL di identity. secret è il segreto
// di servizio (Bearer verso /internal/*); non viene mai loggato.
func NewClient(base *url.URL, secret string, timeout time.Duration) *Client {
	return &Client{
		endpoint: strings.TrimRight(base.String(), "/") + "/internal/verify",
		secret:   secret,
		http:     &http.Client{Timeout: timeout},
	}
}

// Verify implementa Verifier. Gli errori di trasporto, un 5xx o un 401/403
// (segreto sbagliato: errore di configurazione, non "credenziale non
// valida") sono ErrUnavailable.
func (c *Client) Verify(ctx context.Context, credential string, kind Kind) (Result, error) {
	in := VerifyCredentialInput{Credential: &credential}
	if kind != "" {
		k := VerifyCredentialInputKind(kind)
		in.Kind = &k
	}
	body, err := json.Marshal(in)
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.secret)

	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %s", ErrUnavailable, sanitize(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Result{}, fmt.Errorf("%w: lettura della risposta: %s", ErrUnavailable, sanitize(err))
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("%w: /internal/verify ha risposto %d", ErrUnavailable, resp.StatusCode)
	}
	var out VerifyCredentialResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return Result{}, fmt.Errorf("%w: risposta non valida", ErrUnavailable)
	}

	res := Result{Active: out.Active}
	if out.CacheTtlSeconds != nil && *out.CacheTtlSeconds > 0 {
		res.TTL = time.Duration(*out.CacheTtlSeconds) * time.Second
	}
	if !out.Active {
		return res, nil
	}
	p := out.Principal
	if p == nil {
		// active senza principal: risposta incoerente, mai fail open.
		return Result{}, fmt.Errorf("%w: active senza principal", ErrUnavailable)
	}
	res.Principal = Caller{
		UserID:     p.UserId.String(),
		Username:   p.Username,
		IsAdmin:    p.IsAdmin,
		AuthMethod: string(p.AuthMethod),
		ExpiresAt:  p.ExpiresAt,
		IsToken:    p.AuthMethod == "token",
	}
	if p.MustChangePassword != nil {
		res.Principal.MustChangePassword = *p.MustChangePassword
	}
	if p.Scopes != nil {
		for _, s := range *p.Scopes {
			res.Principal.Scopes = append(res.Principal.Scopes, string(s))
		}
	}
	return res, nil
}

// sanitize riduce un errore di trasporto al solo messaggio, senza URL (che
// non contiene segreti, ma il messaggio resta breve nei log).
func sanitize(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}
