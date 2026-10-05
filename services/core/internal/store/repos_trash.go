package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Retention è il periodo in cui un repo eliminato resta ripristinabile (R2).
const Retention = 7 * 24 * time.Hour

// PurgeAt è l'istante dopo il quale un repo eliminato in deletedAt viene
// cancellato definitivamente.
func PurgeAt(deletedAt time.Time) time.Time { return deletedAt.Add(Retention) }

// MarkDeleted segna eliminato (in `now`) il repo già bloccato con Lock. Il
// nome resta occupato: la riga non si tocca, e l'unicità non filtra su
// deleted_at.
func (t *RepoTx) MarkDeleted(ctx context.Context, id uuid.UUID, now time.Time) (Repo, error) {
	row := t.tx.QueryRow(ctx, `UPDATE core.repositories r SET deleted_at = $2, updated_at = $2
		WHERE r.resource_id = $1 AND r.deleted_at IS NULL RETURNING `+repoCols, id, now)
	repo, err := scanRepo(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Repo{}, ErrNotFound
	}
	return repo, err
}

// LockDeleted legge con FOR UPDATE un repo eliminato e ancora ripristinabile
// (eliminato dopo `now - Retention`). ErrNotFound altrimenti.
func (t *RepoTx) LockDeleted(ctx context.Context, id uuid.UUID, now time.Time) (Repo, error) {
	row := t.tx.QueryRow(ctx, `SELECT `+repoCols+` FROM core.repositories r
		WHERE r.resource_id = $1 AND r.deleted_at IS NOT NULL AND r.deleted_at > $2 FOR UPDATE`, id, now.Add(-Retention))
	repo, err := scanRepo(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Repo{}, ErrNotFound
	}
	return repo, err
}

// Restore toglie l'eliminazione al repo già bloccato con LockDeleted.
func (t *RepoTx) Restore(ctx context.Context, id uuid.UUID, now time.Time) (Repo, error) {
	row := t.tx.QueryRow(ctx, `UPDATE core.repositories r SET deleted_at = NULL, updated_at = $2
		WHERE r.resource_id = $1 AND r.deleted_at IS NOT NULL RETURNING `+repoCols, id, now)
	repo, err := scanRepo(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Repo{}, ErrNotFound
	}
	return repo, err
}

// GetDeletedRepo legge un repo eliminato e ancora ripristinabile.
func (s *Store) GetDeletedRepo(ctx context.Context, id uuid.UUID, now time.Time) (Repo, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+repoCols+` FROM core.repositories r
		WHERE r.resource_id = $1 AND r.deleted_at IS NOT NULL AND r.deleted_at > $2`, id, now.Add(-Retention))
	repo, err := scanRepo(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Repo{}, ErrNotFound
	}
	return repo, err
}

// ListDeletedRepos elenca i repo eliminati e ancora ripristinabili, i più
// recenti per primi. ownerName, se presente, filtra per owner.
func (s *Store) ListDeletedRepos(ctx context.Context, ownerName *string, now time.Time) ([]Repo, error) {
	q := `SELECT ` + repoCols + ` FROM core.repositories r
		WHERE r.deleted_at IS NOT NULL AND r.deleted_at > $1`
	args := []any{now.Add(-Retention)}
	if ownerName != nil {
		q += ` AND r.owner_name = $2`
		args = append(args, *ownerName)
	}
	rows, err := s.pool.Query(ctx, q+` ORDER BY r.deleted_at DESC, r.resource_id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Repo, 0)
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return items, rows.Err()
}

// PurgeDue cancella definitivamente i repo eliminati da più di Retention
// rispetto a `now`. Per ciascuno, in una transazione propria, blocca la riga
// con FOR UPDATE SKIP LOCKED (due repliche non prendono lo stesso repo, e un
// ripristino in corso lo fa saltare), chiama cleanup (disco, grant) e solo se
// riesce cancella la riga di core.resources, che porta con sé dettaglio,
// contatore e PR. Un repo che fallisce resta com'è e riprova alla corsa
// dopo; gli altri proseguono. Ritorna quanti ne ha cancellati e gli errori.
func (s *Store) PurgeDue(ctx context.Context, now time.Time, cleanup func(context.Context, Repo) error) (int, []error) {
	var errs []error
	skip := []uuid.UUID{}
	purged := 0
	for ctx.Err() == nil {
		done, ok, err := s.purgeOne(ctx, now, skip, cleanup)
		if err != nil {
			errs = append(errs, err)
			if done == uuid.Nil {
				return purged, errs
			}
			skip = append(skip, done)
			continue
		}
		if done == uuid.Nil {
			break
		}
		if ok {
			purged++
		}
	}
	return purged, errs
}

// purgeOne tratta un repo scaduto. Ritorna l'id trattato (Nil se non ce ne
// sono più o se l'errore è prima della selezione).
func (s *Store) purgeOne(ctx context.Context, now time.Time, skip []uuid.UUID, cleanup func(context.Context, Repo) error) (uuid.UUID, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	row := tx.QueryRow(ctx, `SELECT `+repoCols+` FROM core.repositories r
		WHERE r.deleted_at IS NOT NULL AND r.deleted_at <= $1 AND r.resource_id <> ALL($2::uuid[])
		ORDER BY r.deleted_at ASC, r.resource_id ASC LIMIT 1 FOR UPDATE SKIP LOCKED`, now.Add(-Retention), skip)
	repo, err := scanRepo(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	if err := cleanup(ctx, repo); err != nil {
		return repo.ID, false, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM core.resources WHERE id = $1`, repo.ID); err != nil {
		return repo.ID, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return repo.ID, false, err
	}
	return repo.ID, true, nil
}
