package pushevent

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/git/internal/access"
	"github.com/fathorMB/GitStack/services/git/internal/gitrun"
)

// Publisher pubblica un evento con conferma (events.Publisher di
// pkg/events, adattato da NATSPublisher).
type Publisher interface {
	Publish(ctx context.Context, name string, version int, payload any) error
}

// Valori di default della pubblicazione.
const (
	DefaultAttemptTimeout = 5 * time.Second
	DefaultAttempts       = 3
	DefaultBackoff        = time.Second
)

// Notifier pubblica git.push dopo i push riusciti. Un *Notifier nil non fa
// niente: è il caso di un servizio senza NATS configurato.
type Notifier struct {
	Pub    Publisher
	Git    *gitrun.Runner
	Logger *slog.Logger
	// AttemptTimeout è il tempo di ogni tentativo; Attempts i tentativi;
	// Backoff l'attesa fra due tentativi (raddoppia a ogni volta). Zero =
	// valori di default.
	AttemptTimeout time.Duration
	Attempts       int
	Backoff        time.Duration

	wg sync.WaitGroup
}

func (n *Notifier) logger() *slog.Logger {
	if n.Logger != nil {
		return n.Logger
	}
	return slog.Default()
}

// Target è il repo su cui si fa il push.
type Target struct {
	Dir           string
	RepoID        string
	Owner, Name   string
	DefaultBranch string
}

// TargetOf ricava il Target da quello che l'autorizzazione ha risolto.
func TargetOf(dir, owner, name string, ref access.RepoRef) Target {
	// Nome canonico di core, non quello scritto nell'indirizzo (R11).
	if ref.Owner != "" && ref.Name != "" {
		owner, name = ref.Owner, ref.Name
	}
	return Target{Dir: dir, RepoID: ref.ID, Owner: owner, Name: name, DefaultBranch: ref.DefaultBranch}
}

// Push è un push in corso: ha lo stato dei ref prima del receive-pack.
type Push struct {
	n      *Notifier
	target Target
	pusher gitpush.Pusher
	before Snapshot
}

// Begin legge i ref prima del receive-pack. Se la lettura fallisce lo
// dice nel log e ritorna nil: il push prosegue senza evento. Va chiamata
// prima di lanciare git receive-pack.
func (n *Notifier) Begin(ctx context.Context, t Target, p access.Principal) *Push {
	if n == nil || n.Pub == nil {
		return nil
	}
	before, err := Take(ctx, n.Git, t.Dir)
	if err != nil {
		n.logger().Error("git.push: lettura dei ref prima del push non riuscita, evento non pubblicato", "repo", t.Owner+"/"+t.Name, "err", err)
		return nil
	}
	kind := p.Kind
	if kind != gitpush.PusherAgent {
		kind = gitpush.PusherHuman
	}
	return &Push{n: n, target: t, before: before,
		pusher: gitpush.Pusher{ID: p.UserID, Username: p.Username, Type: kind}}
}

// Done va chiamata dopo un receive-pack finito con successo. Legge i ref
// dopo il push e pubblica l'evento in background: non blocca la risposta al
// client e nessun errore le torna. Se nessun ref è cambiato non pubblica.
func (p *Push) Done() {
	if p == nil {
		return
	}
	n := p.n
	log := n.logger().With("repo", p.target.Owner+"/"+p.target.Name)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	after, err := Take(ctx, n.Git, p.target.Dir)
	cancel()
	if err != nil {
		log.Error("git.push: lettura dei ref dopo il push non riuscita, evento non pubblicato", "err", err)
		return
	}
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		p.publish(after, log)
	}()
}

func (p *Push) publish(after Snapshot, log *slog.Logger) {
	n := p.n
	bctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	refs, err := Build(bctx, n.Git, p.target.Dir, p.before, after, p.target.DefaultBranch)
	cancel()
	if err != nil {
		// Un commit non leggibile non fa perdere l'evento: il ref c'è, la
		// sua lista di commit può essere vuota.
		log.Warn("git.push: dettagli dei ref incompleti", "err", err)
	}
	if len(refs) == 0 {
		return
	}
	payload := gitpush.Payload{
		Repo:   gitpush.Repo{ID: p.target.RepoID, FullName: p.target.Owner + "/" + p.target.Name, DefaultBranch: p.target.DefaultBranch},
		Pusher: p.pusher,
		Refs:   refs,
	}
	attempts, backoff, timeout := n.Attempts, n.Backoff, n.AttemptTimeout
	if attempts <= 0 {
		attempts = DefaultAttempts
	}
	if backoff <= 0 {
		backoff = DefaultBackoff
	}
	if timeout <= 0 {
		timeout = DefaultAttemptTimeout
	}
	for i := 1; ; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err := n.Pub.Publish(ctx, gitpush.Name, gitpush.Version, payload)
		cancel()
		if err == nil {
			return
		}
		if i >= attempts {
			log.Error("git.push: pubblicazione non riuscita, evento perso (il push è stato accettato)",
				"attempts", i, "refs", len(refs), "err", err)
			return
		}
		log.Warn("git.push: pubblicazione non riuscita, ritento", "attempt", i, "err", err)
		time.Sleep(backoff)
		backoff *= 2
	}
}

// Wait attende le pubblicazioni in corso, fino alla scadenza di ctx: serve
// all'arresto del servizio e ai test. Ritorna true se sono finite.
func (n *Notifier) Wait(ctx context.Context) bool {
	if n == nil {
		return true
	}
	done := make(chan struct{})
	go func() { n.wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}
