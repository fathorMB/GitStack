package appauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/gateway/internal/identityclient"
	"github.com/fathorMB/GitStack/services/gateway/internal/trust"
)

// Lista di accesso dell'app (GIT-376, H6 del 2026-10-26). La lista vive in core; il gateway chiede a
// core se l'utente (già identificato) è nella lista, direttamente o tramite un team. Non è un ruolo
// sul repo: serve solo ad aprire l'app.

// Membership dice se un utente è nella lista di accesso di un'app. Un errore è «non so».
type Membership interface {
	IsMember(ctx context.Context, userID, appID string) (bool, error)
}

// WithAccessList compone la regola di accesso: passa chi ha `read` sul repo (base) OPPURE è nella
// lista. Se base nega, la lista decide; un errore di base o della lista resta un errore (mai fail
// open), a meno che l'altra via abbia già detto sì.
func WithAccessList(base AccessPolicy, m Membership) AccessPolicy {
	return func(ctx context.Context, userID string, app App) (bool, error) {
		ok, baseErr := base(ctx, userID, app)
		if baseErr == nil && ok {
			return true, nil
		}
		member, err := m.IsMember(ctx, userID, app.ID)
		if err == nil && member {
			return true, nil
		}
		if baseErr != nil {
			return false, baseErr
		}
		return false, err
	}
}

// CoreMembership chiede a core (GET /apps/{appId}/access-membership) con l'identità dell'utente
// firmata col segreto di servizio, come fa il proxy per le richieste dei client.
type CoreMembership struct {
	base   *url.URL
	secret string
	http   *http.Client
	now    func() time.Time
}

// NewCoreMembership costruisce il client; now nil = time.Now.
func NewCoreMembership(base *url.URL, secret string, timeout time.Duration, now func() time.Time) *CoreMembership {
	if now == nil {
		now = time.Now
	}
	return &CoreMembership{base: base, secret: secret, http: &http.Client{Timeout: timeout}, now: now}
}

// IsMember implementa Membership. Ogni risposta diversa da 200 con il campo member è un errore.
func (c *CoreMembership) IsMember(ctx context.Context, userID, appID string) (bool, error) {
	if !isUUID(userID) || !isUUID(appID) {
		return false, errors.New("id non valido")
	}
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/apps/" + appID + "/access-membership"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false, err
	}
	trust.Sign(req.Header, c.secret, trust.Identity{UserID: userID, Username: "appauth"}, c.now())
	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("%w: lista di accesso: %v", identityclient.ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return false, fmt.Errorf("%w: la lista di accesso ha risposto %d", identityclient.ErrUnavailable, resp.StatusCode)
	}
	var out struct {
		Member *bool `json:"member"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out); err != nil || out.Member == nil {
		return false, fmt.Errorf("%w: risposta della lista di accesso non valida", identityclient.ErrUnavailable)
	}
	return *out.Member, nil
}
