package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrArchived: il repo è archiviato e rifiuta la modifica (R10).
var ErrArchived = errors.New("repo archiviato")

// Repo è un repo: la riga di core.resources (type='repo') più il dettaglio di
// core.repositories (D-A di GIT-63).
type Repo struct {
	ID                   uuid.UUID
	OwnerType            string
	OwnerID              uuid.UUID
	OwnerName            string
	Name                 string
	Description          string
	Visibility           string
	DefaultBranch        string
	ProtectDefaultBranch bool
	ArchivedAt           *time.Time
	DeletedAt            *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// NewRepo sono i campi della creazione.
type NewRepo struct {
	ID            uuid.UUID
	OwnerType     string
	OwnerID       uuid.UUID
	OwnerName     string
	Name          string
	Description   string
	Visibility    string
	DefaultBranch string
}

// RepoUpdate è una modifica parziale: nil = non cambiare.
type RepoUpdate struct {
	Description          *string
	Visibility           *string
	DefaultBranch        *string
	ProtectDefaultBranch *bool
	Archived             *bool
}

const repoCols = `r.resource_id, r.owner_type, r.owner_id, r.owner_name, r.name, r.description, r.visibility,
	r.default_branch, r.protect_default_branch, r.archived_at, r.deleted_at, r.created_at, r.updated_at`

func scanRepo(r row) (Repo, error) {
	var x Repo
	err := r.Scan(&x.ID, &x.OwnerType, &x.OwnerID, &x.OwnerName, &x.Name, &x.Description, &x.Visibility,
		&x.DefaultBranch, &x.ProtectDefaultBranch, &x.ArchivedAt, &x.DeletedAt, &x.CreatedAt, &x.UpdatedAt)
	return x, err
}

// RepoTx è una transazione sui repo: la creazione e la modifica restano
// aperte mentre core parla con git e identity, e si annullano se uno dei due
// fallisce (nessun repo a metà).
type RepoTx struct {
	tx pgx.Tx
}

// BeginRepo apre una transazione.
func (s *Store) BeginRepo(ctx context.Context) (*RepoTx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &RepoTx{tx: tx}, nil
}

// Commit conferma la transazione.
func (t *RepoTx) Commit(ctx context.Context) error { return t.tx.Commit(ctx) }

// Rollback annulla la transazione (innocuo dopo Commit).
func (t *RepoTx) Rollback(ctx context.Context) { _ = t.tx.Rollback(ctx) }

// Insert crea nella stessa transazione la risorsa, il dettaglio e il
// contatore #n del repo (I1, parte da 1). Ritorna ErrConflict se il nome è
// già occupato per quell'owner (anche da un repo eliminato, R2).
func (t *RepoTx) Insert(ctx context.Context, in NewRepo) (Repo, error) {
	if _, err := t.tx.Exec(ctx, `INSERT INTO core.resources (id, type, name, attributes) VALUES ($1, 'repo', $2, '{}'::jsonb)`,
		in.ID, in.OwnerName+"/"+in.Name); err != nil {
		return Repo{}, err
	}
	row := t.tx.QueryRow(ctx, `INSERT INTO core.repositories AS r
		(resource_id, owner_type, owner_id, owner_name, name, description, visibility, default_branch)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+repoCols,
		in.ID, in.OwnerType, in.OwnerID, in.OwnerName, in.Name, in.Description, in.Visibility, in.DefaultBranch)
	repo, err := scanRepo(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return Repo{}, ErrConflict
		}
		return Repo{}, err
	}
	if _, err := t.tx.Exec(ctx, `INSERT INTO core.repo_counters (repo_id, next_number) VALUES ($1, 1)`, in.ID); err != nil {
		return Repo{}, err
	}
	return repo, nil
}

// Lock legge il repo con un blocco di riga (FOR UPDATE): le modifiche alle
// impostazioni dello stesso repo si serializzano. ErrNotFound se non esiste o
// è eliminato.
func (t *RepoTx) Lock(ctx context.Context, id uuid.UUID) (Repo, error) {
	row := t.tx.QueryRow(ctx, `SELECT `+repoCols+` FROM core.repositories r WHERE r.resource_id = $1 AND r.deleted_at IS NULL FOR UPDATE`, id)
	repo, err := scanRepo(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Repo{}, ErrNotFound
	}
	return repo, err
}

// Update applica una modifica parziale al repo già bloccato con Lock. Un repo
// archiviato rifiuta tutto (ErrArchived) tranne la riattivazione da sola
// (`archived: false` e nient'altro, R10).
func (t *RepoTx) Update(ctx context.Context, cur Repo, upd RepoUpdate) (Repo, error) {
	if cur.ArchivedAt != nil {
		onlyUnarchive := upd.Archived != nil && !*upd.Archived &&
			upd.Description == nil && upd.Visibility == nil && upd.DefaultBranch == nil && upd.ProtectDefaultBranch == nil
		if !onlyUnarchive {
			return Repo{}, ErrArchived
		}
	}
	row := t.tx.QueryRow(ctx, `UPDATE core.repositories r SET
			description = COALESCE($2, description),
			visibility = COALESCE($3, visibility),
			default_branch = COALESCE($4, default_branch),
			protect_default_branch = COALESCE($5, protect_default_branch),
			archived_at = CASE WHEN $6::boolean IS NULL THEN archived_at
				WHEN $6 THEN COALESCE(archived_at, now()) ELSE NULL END,
			updated_at = now()
		WHERE r.resource_id = $1
		RETURNING `+repoCols,
		cur.ID, upd.Description, upd.Visibility, upd.DefaultBranch, upd.ProtectDefaultBranch, upd.Archived)
	return scanRepo(row)
}

// GetRepoByName legge un repo non eliminato per nome dell'owner e del repo.
func (s *Store) GetRepoByName(ctx context.Context, ownerName, name string) (Repo, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+repoCols+` FROM core.repositories r
		WHERE r.owner_name = $1 AND r.name = $2 AND r.deleted_at IS NULL`, ownerName, name)
	repo, err := scanRepo(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Repo{}, ErrNotFound
	}
	return repo, err
}

// ListRepos elenca i repo non eliminati, in ordine di creazione. visible nil =
// nessun filtro (amministratore di sistema); altrimenti solo gli id dati.
// ownerName, se presente, filtra per owner.
func (s *Store) ListRepos(ctx context.Context, visible []uuid.UUID, ownerName *string, page, perPage int) ([]Repo, int, error) {
	if visible != nil && len(visible) == 0 {
		return []Repo{}, 0, nil
	}
	where := " WHERE r.deleted_at IS NULL"
	var args []any
	if visible != nil {
		args = append(args, visible)
		where += fmt.Sprintf(" AND r.resource_id = ANY($%d::uuid[])", len(args))
	}
	if ownerName != nil {
		args = append(args, *ownerName)
		where += fmt.Sprintf(" AND r.owner_name = $%d", len(args))
	}
	var total int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM core.repositories r"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, perPage, (page-1)*perPage)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT %s FROM core.repositories r%s
		ORDER BY r.created_at ASC, r.resource_id ASC LIMIT $%d OFFSET $%d`, repoCols, where, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]Repo, 0)
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, r)
	}
	return items, total, rows.Err()
}
