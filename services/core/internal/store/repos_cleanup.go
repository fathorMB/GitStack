package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// IsRepo dice se la risorsa è un repo vero: ha la riga di dettaglio in
// core.repositories.
func (s *Store) IsRepo(ctx context.Context, id uuid.UUID) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.repositories WHERE resource_id = $1)`, id).Scan(&ok)
	return ok, err
}

// RepoCleanup è un repo già cancellato da core a cui restano da togliere le
// cose fuori da Postgres (disco, registry, app e volumi, allegati, grant).
type RepoCleanup struct {
	RepoID    uuid.UUID
	OwnerName string
	Name      string
	// AppIDs sono le app che il repo aveva: servono per togliere namespace e PVC dal cluster, perché le
	// righe di core.apps sono già cadute con il repo.
	AppIDs []uuid.UUID
	// Source: "delete" (eliminazione con l'API) o "trash" (era nel cestino
	// prima di GIT-530).
	Source   string
	Attempts int
}

// DeleteRepo elimina per sempre il repo già bloccato con Lock (R2, GIT-530):
// scrive la riga nella coda repo_cleanup e cancella la riga di
// core.resources, che porta con sé dettaglio, contatore, issue, PR, release,
// webhook, mirror e credenziali. Il nome è libero alla fine della
// transazione.
func (t *RepoTx) DeleteRepo(ctx context.Context, r Repo) error {
	if _, err := t.tx.Exec(ctx, `INSERT INTO core.repo_cleanup (repo_id, owner_name, name, app_ids, source)
		VALUES ($1, $2, $3, COALESCE((SELECT array_agg(id) FROM core.apps WHERE repo_id = $1), '{}'), 'delete') ON CONFLICT (repo_id) DO NOTHING`, r.ID, r.OwnerName, r.Name); err != nil {
		return err
	}
	tag, err := t.tx.Exec(ctx, `DELETE FROM core.resources WHERE id = $1`, r.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

// CleanupRepo esegue cleanup sul repo della coda `only` (uuid.Nil = il
// primo non in `skip`). La riga è presa con FOR UPDATE SKIP LOCKED, quindi
// due repliche non toccano lo stesso repo. Se cleanup riesce la riga esce
// dalla coda; se fallisce resta, con un tentativo in più e l'errore.
// Ritorna l'id trattato (Nil se non ce ne sono) e l'esito.
func (s *Store) CleanupRepo(ctx context.Context, only uuid.UUID, skip []uuid.UUID, cleanup func(context.Context, RepoCleanup) error) (uuid.UUID, error) {
	if skip == nil {
		skip = []uuid.UUID{} // un array NULL farebbe valere <> ALL(NULL) per nessuna riga
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var c RepoCleanup
	err = tx.QueryRow(ctx, `SELECT repo_id, owner_name, name, app_ids, source, attempts FROM core.repo_cleanup
		WHERE ($1::uuid = '00000000-0000-0000-0000-000000000000'::uuid OR repo_id = $1) AND repo_id <> ALL($2::uuid[])
		ORDER BY queued_at ASC, repo_id ASC LIMIT 1 FOR UPDATE SKIP LOCKED`, only, skip).
		Scan(&c.RepoID, &c.OwnerName, &c.Name, &c.AppIDs, &c.Source, &c.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	if cerr := cleanup(ctx, c); cerr != nil {
		if _, err := tx.Exec(context.WithoutCancel(ctx), `UPDATE core.repo_cleanup SET attempts = attempts + 1, last_error = $2 WHERE repo_id = $1`, c.RepoID, truncate(cerr.Error(), 500)); err == nil {
			_ = tx.Commit(context.WithoutCancel(ctx))
		}
		return c.RepoID, cerr
	}
	if _, err := tx.Exec(ctx, `DELETE FROM core.repo_cleanup WHERE repo_id = $1`, c.RepoID); err != nil {
		return c.RepoID, err
	}
	return c.RepoID, tx.Commit(ctx)
}

// CleanupQueued ritorna quanti repo aspettano ancora la pulizia fuori da Postgres.
func (s *Store) CleanupQueued(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM core.repo_cleanup`).Scan(&n)
	return n, err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

