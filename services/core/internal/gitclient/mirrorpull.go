package gitclient

import (
	"context"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// MirrorPullPusher è l'autore dell'evento git.push che il servizio git
// pubblica dopo un aggiornamento: un id utente valido per core.
type MirrorPullPusher struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// MirrorPullInput è il corpo di POST /internal/git/repos/{repoId}/mirror-pull
// (GIT-196, MP1). Il token vive solo qui e nel corpo della richiesta interna.
type MirrorPullInput struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Token    string `json:"token"`
	// IP è l'indirizzo scelto da core dopo il controllo di egress (C8).
	IP            string           `json:"ip"`
	DefaultBranch string           `json:"defaultBranch"`
	Owner         string           `json:"owner"`
	Name          string           `json:"name"`
	Pusher        MirrorPullPusher `json:"pusher"`
}

// String non stampa il token.
func (in MirrorPullInput) String() string {
	return "MirrorPullInput{url: " + in.URL + ", username: " + in.Username + ", token: ***}"
}

// MirrorPullRef è l'esito di un ref: created | updated | up-to-date |
// diverged | error.
type MirrorPullRef struct {
	Ref    string `json:"ref"`
	Status string `json:"status"`
	// SHA locale dopo l'aggiornamento (o quello rimasto, se diverged).
	SHA string `json:"sha,omitempty"`
	// Origin: lo sha dell'origine, se diverso da quello locale.
	Origin string `json:"origin,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// MirrorPullResult è l'esito di un aggiornamento eseguito dal servizio git.
type MirrorPullResult struct {
	OK         bool            `json:"ok"`
	Diverged   bool            `json:"diverged"`
	Refs       []MirrorPullRef `json:"refs"`
	HeadBranch string          `json:"headBranch,omitempty"`
	Changed    int             `json:"changed"`
	Error      string          `json:"error,omitempty"`
	DurationMs int64           `json:"durationMs"`
}

// MirrorPuller è ciò che il motore del mirror in pull chiede al servizio git.
type MirrorPuller interface {
	MirrorPull(ctx context.Context, caller trust.Identity, repoID uuid.UUID, in MirrorPullInput) (MirrorPullResult, error)
}

// MirrorPull implementa MirrorPuller. La durata la limita ctx.
func (c *Client) MirrorPull(ctx context.Context, caller trust.Identity, repoID uuid.UUID, in MirrorPullInput) (MirrorPullResult, error) {
	var out MirrorPullResult
	err := c.doWith(c.stream, ctx, caller, http.MethodPost, "/internal/git/repos/"+repoID.String()+"/mirror-pull", in, &out, http.StatusOK)
	return out, err
}

var _ MirrorPuller = (*Client)(nil)
