// Package store legge e scrive la risorsa di prova (contratto T-03, schema
// generico D15 [c_4ef209e79c5b2ddb]) sullo schema Postgres dedicato "core"
// (D6 [c_4df04d65b3ac4910]). Nessuna query verso tabelle di altri servizi.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound indica che la risorsa richiesta non esiste (mappato a 404 dal
// livello HTTP).
var ErrNotFound = errors.New("risorsa non trovata")

// ErrConflict indica che la risorsa viola il vincolo di unicità
// (type, name) (mappato a 409 dal livello HTTP, come previsto dal
// contratto per POST /resources).
var ErrConflict = errors.New("risorsa in conflitto: type e name già usati insieme")

// pgUniqueViolation è il codice SQLSTATE di Postgres per la violazione di un
// vincolo unique.
const pgUniqueViolation = "23505"

// Resource è la rappresentazione di dominio della risorsa generica di
// prova, indipendente dai tipi generati dal contratto OpenAPI: il pacchetto
// httpserver la converte da/verso openapi.Resource.
type Resource struct {
	ID         uuid.UUID
	Type       string
	Name       string
	Attributes map[string]any
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// NewInput sono i campi accettati alla creazione.
type NewInput struct {
	Type       string
	Name       string
	Attributes map[string]any
}

// UpdateInput sono i campi accettati in un aggiornamento parziale: un
// puntatore nil significa "non cambiare questo campo", coerente con
// UpdateResourceInput del contratto (PATCH).
type UpdateInput struct {
	Name       *string
	Attributes *map[string]any
}

// Store è il repository Postgres delle risorse, con un pool pgx condiviso.
type Store struct {
	pool *pgxpool.Pool
}

// New costruisce un Store sul pool di connessioni dato.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// List elenca le risorse, filtrate opzionalmente per tipo e per id, in ordine
// di creazione, paginate. page e perPage sono già validati (>=1) dal
// chiamante. visible nil = nessun filtro sugli id (amministratore di
// sistema); altrimenti solo le risorse con quegli id, filtrate nella query
// (total e paginazione contano solo le visibili). Una lista vuota dà
// risultato vuoto e total 0 senza interrogare il database.
func (s *Store) List(ctx context.Context, resourceType *string, visible []uuid.UUID, page, perPage int) (items []Resource, total int, err error) {
	if visible != nil && len(visible) == 0 {
		return []Resource{}, 0, nil
	}
	offset := (page - 1) * perPage

	where := ""
	var args []any
	if resourceType != nil && *resourceType != "" {
		args = append(args, *resourceType)
		where += fmt.Sprintf(" AND type = $%d", len(args))
	}
	if visible != nil {
		args = append(args, visible)
		where += fmt.Sprintf(" AND id = ANY($%d::uuid[])", len(args))
	}
	if where != "" {
		where = " WHERE " + where[len(" AND "):]
	}

	if err = s.pool.QueryRow(ctx, "SELECT count(*) FROM core.resources"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	listArgs := append(append([]any{}, args...), perPage, offset)
	q := fmt.Sprintf(`SELECT id, type, name, attributes, created_at, updated_at
		FROM core.resources%s ORDER BY created_at ASC, id ASC LIMIT $%d OFFSET $%d`, where, len(args)+1, len(args)+2)
	rows, err := s.pool.Query(ctx, q, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items, err = scanResources(rows)
	return items, total, err
}

// Create inserisce una nuova risorsa con un id generato lato applicazione
// (uuid v7, ordinabile nel tempo), evitando di dipendere da estensioni
// Postgres (es. pgcrypto) che l'utente DB con permessi limitati allo schema
// core (D6) potrebbe non avere il diritto di installare.
func (s *Store) Create(ctx context.Context, in NewInput) (Resource, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Resource{}, err
	}

	attrs := in.Attributes
	if attrs == nil {
		attrs = map[string]any{}
	}

	const q = `INSERT INTO core.resources (id, type, name, attributes)
		VALUES ($1, $2, $3, $4)
		RETURNING id, type, name, attributes, created_at, updated_at`

	row := s.pool.QueryRow(ctx, q, id, in.Type, in.Name, attrs)
	res, err := scanResource(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return Resource{}, ErrConflict
		}
		return Resource{}, err
	}
	return res, nil
}

// Get legge una risorsa per id. Ritorna ErrNotFound se non esiste.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Resource, error) {
	const q = `SELECT id, type, name, attributes, created_at, updated_at
		FROM core.resources WHERE id = $1`

	row := s.pool.QueryRow(ctx, q, id)
	res, err := scanResource(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Resource{}, ErrNotFound
	}
	return res, err
}

// Update applica un aggiornamento parziale. Ritorna ErrNotFound se la
// risorsa non esiste.
func (s *Store) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (Resource, error) {
	const q = `UPDATE core.resources SET
			name = COALESCE($2, name),
			attributes = COALESCE($3, attributes),
			updated_at = now()
		WHERE id = $1
		RETURNING id, type, name, attributes, created_at, updated_at`

	row := s.pool.QueryRow(ctx, q, id, in.Name, in.Attributes)
	res, err := scanResource(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Resource{}, ErrNotFound
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return Resource{}, ErrConflict
		}
		return Resource{}, err
	}
	return res, nil
}

// Delete elimina una risorsa. Ritorna ErrNotFound se non esiste.
func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM core.resources WHERE id = $1`

	tag, err := s.pool.Exec(ctx, q, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type row interface {
	Scan(dest ...any) error
}

func scanResource(r row) (Resource, error) {
	var res Resource
	if err := r.Scan(&res.ID, &res.Type, &res.Name, &res.Attributes, &res.CreatedAt, &res.UpdatedAt); err != nil {
		return Resource{}, err
	}
	return res, nil
}

func scanResources(rows pgx.Rows) ([]Resource, error) {
	items := make([]Resource, 0)
	for rows.Next() {
		res, err := scanResource(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, res)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
