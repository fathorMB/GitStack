package gitclient

import (
	"context"
	"net/http"

	"github.com/fathorMB/GitStack/services/core/internal/trust"
	"github.com/google/uuid"
)

// MirrorPushInput è il corpo di POST /internal/git/repos/{repoId}/mirror-push
// (GIT-179). Il token vive solo qui e nel corpo della richiesta interna: mai
// in un log né in un URL.
type MirrorPushInput struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Token    string `json:"token"`
	// IP è l'indirizzo scelto da core dopo il controllo di egress (C8): il
	// servizio git lo fissa per la connessione (nessun DNS rebinding).
	IP            string `json:"ip"`
	DefaultBranch string `json:"defaultBranch"`
}

// String non stampa il token: un %v o %+v accidentale non lo fa uscire.
func (in MirrorPushInput) String() string {
	return "MirrorPushInput{url: " + in.URL + ", username: " + in.Username + ", token: ***}"
}

// MirrorRef è l'esito di un ref.
type MirrorRef struct {
	Ref    string `json:"ref"`
	Status string `json:"status"` // pushed | up-to-date | rejected | error
	SHA    string `json:"sha,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// MirrorPushResult è l'esito di un push eseguito dal servizio git.
type MirrorPushResult struct {
	OK         bool        `json:"ok"`
	Diverged   bool        `json:"diverged"`
	Refs       []MirrorRef `json:"refs"`
	Error      string      `json:"error"`
	DurationMs int64       `json:"durationMs"`
}

// MirrorPusher è ciò che il motore dei mirror chiede al servizio git.
type MirrorPusher interface {
	MirrorPush(ctx context.Context, caller trust.Identity, repoID uuid.UUID, in MirrorPushInput) (MirrorPushResult, error)
}

// MirrorPush implementa MirrorPusher. La durata la limita ctx (il servizio git
// ha il suo timeout): non si usa il timeout breve delle altre chiamate.
func (c *Client) MirrorPush(ctx context.Context, caller trust.Identity, repoID uuid.UUID, in MirrorPushInput) (MirrorPushResult, error) {
	var out MirrorPushResult
	err := c.doWith(c.stream, ctx, caller, http.MethodPost, "/internal/git/repos/"+repoID.String()+"/mirror-push", in, &out, http.StatusOK)
	return out, err
}

var _ MirrorPusher = (*Client)(nil)
