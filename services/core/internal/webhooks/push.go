package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	pkgevents "github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/pkg/events/gitpush"
	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// EnqueuePush crea le consegne dell'evento git.push: un messaggio per ref
// (docs/webhooks.md). Idempotente: l'id di origine di ogni consegna deriva da
// busta e ref, quindi una riconsegna del messaggio da NATS non la duplica.
func (e *Engine) EnqueuePush(ctx context.Context, env pkgevents.Envelope, p gitpush.Payload) error {
	e.defaults()
	repoID, err := uuid.Parse(p.Repo.ID)
	if err != nil {
		return fmt.Errorf("git.push con repo.id non valido: %w", err)
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	targets, err := matching(ctx, tx, repoID, EventPush)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}
	repo, err := repoFromDB(ctx, tx, repoID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // repo eliminato definitivamente: niente da consegnare
	}
	if err != nil {
		return err
	}
	// Il branch principale al momento del push è quello dell'evento (R4).
	if p.Repo.DefaultBranch != "" {
		repo.DefaultBranch = p.Repo.DefaultBranch
	}
	pusher := &domainevents.User{ID: p.Pusher.ID, Username: p.Pusher.Username, Type: p.Pusher.Type}
	envTime := env.Time
	if envTime.IsZero() {
		envTime = e.Now()
	}
	for _, ref := range p.Refs {
		b := &builder{e: e, body: obj{}}
		b.common(EventPush, "", envTime)
		if err := b.repository(ctx, tx, repo); err != nil {
			return err
		}
		if err := b.sender(ctx, pusher); err != nil {
			return err
		}
		commits := make([]gitpush.Commit, 0, len(ref.Commits))
		commits = append(commits, ref.Commits...)
		b.body["ref"] = ref.Ref
		b.body["before"] = ref.Before
		b.body["after"] = ref.After
		b.body["created"] = ref.Before == gitpush.ZeroSHA
		b.body["deleted"] = ref.After == gitpush.ZeroSHA
		b.body["forced"] = ref.Forced
		b.body["isDefaultBranch"] = ref.IsDefaultBranch
		b.body["commits"] = commits
		b.body["commitsTruncated"] = ref.CommitsTruncated
		raw, err := json.Marshal(b.body)
		if err != nil {
			return err
		}
		source := uuid.NewSHA1(uuid.NameSpaceOID, []byte(env.ID+"\x00"+ref.Ref))
		if err := e.enqueue(ctx, tx, targets, EventPush, "", source, raw); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func repoFromDB(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domainevents.Repo, error) {
	var owner, name string
	r := domainevents.Repo{ID: id.String()}
	var archived *time.Time
	err := tx.QueryRow(ctx, `SELECT owner_name, name, default_branch, visibility, archived_at FROM core.repositories WHERE resource_id = $1`, id).
		Scan(&owner, &name, &r.DefaultBranch, &r.Visibility, &archived)
	if err != nil {
		return r, err
	}
	r.FullName = strings.Join([]string{owner, name}, "/")
	r.Archived = archived != nil
	return r, nil
}
