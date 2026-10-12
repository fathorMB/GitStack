package pushevent

import (
	"context"

	"github.com/fathorMB/GitStack/pkg/events/gitpush"
)

// BeginAs è Begin per chi aggiorna i ref senza passare da un push di un
// utente: il mirror in pull (GIT-196). L'autore dell'evento è indicato
// esplicitamente. Un *Notifier nil o senza Pub ritorna nil (Done è nil-safe).
func (n *Notifier) BeginAs(ctx context.Context, t Target, pusher gitpush.Pusher) *Push {
	if n == nil || n.Pub == nil {
		return nil
	}
	before, err := Take(ctx, n.Git, t.Dir)
	if err != nil {
		n.logger().Error("git.push: lettura dei ref prima dell'aggiornamento non riuscita, evento non pubblicato", "repo", t.Owner+"/"+t.Name, "err", err)
		return nil
	}
	return &Push{n: n, target: t, before: before, pusher: pusher}
}

// SetDefaultBranch aggiorna il branch principale dichiarato nell'evento: serve
// quando il primo giro di un mirror lo scopre dopo il Begin.
func (p *Push) SetDefaultBranch(name string) {
	if p != nil && name != "" {
		p.target.DefaultBranch = name
	}
}
