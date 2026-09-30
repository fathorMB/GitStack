// Package permissions implementa il modello dei permessi su risorse generiche
// (D15: tipo + id, qui l'id di core.resources) sulla tabella
// identity.resource_grants e sulle membership di organizzazioni e team.
//
// Ruolo effettivo di un utente su una risorsa: il massimo, nell'ordine
// read < write < admin, fra
//
//   - il grant diretto all'utente;
//   - i grant ai team di cui l'utente è membro;
//   - i grant ai team di un'organizzazione di cui l'utente è owner
//     (ereditarietà organizzazione → team → utente: chi possiede
//     l'organizzazione ha sui team dell'organizzazione lo stesso ruolo dei
//     loro membri);
//   - admin, se l'utente è amministratore di sistema (users.is_admin).
//
// Un utente disattivato o inesistente non ha nessun ruolo.
package permissions

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

// Role è un ruolo su una risorsa. Il valore vuoto è "nessun accesso".
type Role string

const (
	RoleNone  Role = ""
	RoleRead  Role = "read"
	RoleWrite Role = "write"
	RoleAdmin Role = "admin"
)

// Valid dice se r è uno dei tre ruoli assegnabili.
func (r Role) Valid() bool { return r == RoleRead || r == RoleWrite || r == RoleAdmin }

// Rank ordina i ruoli: nessuno 0, read 1, write 2, admin 3.
func (r Role) Rank() int {
	switch r {
	case RoleRead:
		return 1
	case RoleWrite:
		return 2
	case RoleAdmin:
		return 3
	}
	return 0
}

// AtLeast dice se r include il ruolo min (admin ⊇ write ⊇ read).
func (r Role) AtLeast(min Role) bool { return r.Rank() >= min.Rank() && min.Rank() > 0 }

// Max ritorna il ruolo più alto fra a e b.
func Max(a, b Role) Role {
	if b.Rank() > a.Rank() {
		return b
	}
	return a
}

// SubjectType è il tipo di soggetto di un grant.
type SubjectType string

const (
	SubjectUser SubjectType = "user"
	SubjectTeam SubjectType = "team"
)

// Errori del pacchetto.
var (
	// ErrNotFound: grant inesistente (o di un'altra risorsa).
	ErrNotFound = errors.New("grant non trovato")
	// ErrSubjectNotFound: l'utente o il team del grant non esiste.
	ErrSubjectNotFound = errors.New("soggetto del grant non trovato")
	// ErrConflict: il soggetto ha già un grant su quella risorsa.
	ErrConflict = errors.New("il soggetto ha già un grant sulla risorsa")
)

// ValidationError elenca i campi non validi (422).
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "dati del grant non validi" }

// Grant è una riga di identity.resource_grants.
type Grant struct {
	ID          uuid.UUID
	ResourceID  uuid.UUID
	SubjectType SubjectType
	SubjectID   uuid.UUID
	Role        Role
	CreatedAt   time.Time
}

// Service è il repository dei grant e il calcolo del ruolo effettivo.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New crea il servizio. now nil usa time.Now.
func New(pool *pgxpool.Pool, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, now: now}
}

// effectiveSQL calcola in una sola query il ruolo effettivo. $1 = utente,
// $2 = risorsa. Niente riga (utente inesistente o disattivato) = nessun
// accesso.
const effectiveSQL = `
SELECT CASE
         WHEN u.is_admin THEN 3
         ELSE COALESCE((
           SELECT max(CASE g.role WHEN 'admin' THEN 3 WHEN 'write' THEN 2 WHEN 'read' THEN 1 ELSE 0 END)
           FROM identity.resource_grants g
           WHERE g.resource_id = $2
             AND (
                  g.user_id = u.id
               OR g.team_id IN (SELECT tm.team_id FROM identity.team_members tm WHERE tm.user_id = u.id)
               OR g.team_id IN (
                    SELECT t.id FROM identity.teams t
                    JOIN identity.org_members om ON om.org_id = t.org_id
                    WHERE om.user_id = u.id AND om.role = 'owner')
             )
         ), 0)
       END
FROM identity.users u
WHERE u.id = $1 AND u.is_active`

// EffectiveRole ritorna il ruolo effettivo dell'utente sulla risorsa
// (RoleNone se non ha accesso, l'utente non esiste o è disattivato).
func (s *Service) EffectiveRole(ctx context.Context, userID, resourceID uuid.UUID) (Role, error) {
	var rank int
	err := s.pool.QueryRow(ctx, effectiveSQL, userID, resourceID).Scan(&rank)
	if errors.Is(err, pgx.ErrNoRows) {
		return RoleNone, nil
	}
	if err != nil {
		return RoleNone, fmt.Errorf("calcolo del ruolo effettivo non riuscito: %w", err)
	}
	switch rank {
	case 3:
		return RoleAdmin, nil
	case 2:
		return RoleWrite, nil
	case 1:
		return RoleRead, nil
	}
	return RoleNone, nil
}

// Check dice se l'utente ha almeno il ruolo min sulla risorsa, e quale ruolo
// effettivo ha.
func (s *Service) Check(ctx context.Context, userID, resourceID uuid.UUID, min Role) (bool, Role, error) {
	eff, err := s.EffectiveRole(ctx, userID, resourceID)
	if err != nil {
		return false, RoleNone, err
	}
	return eff.AtLeast(min), eff, nil
}

const grantColumns = `id, resource_id, COALESCE(user_id, team_id), CASE WHEN user_id IS NOT NULL THEN 'user' ELSE 'team' END, role, created_at`

func scanGrant(row pgx.Row) (Grant, error) {
	var g Grant
	var st, role string
	err := row.Scan(&g.ID, &g.ResourceID, &g.SubjectID, &st, &role, &g.CreatedAt)
	g.SubjectType, g.Role = SubjectType(st), Role(role)
	return g, err
}

// CreateInput sono i dati di un nuovo grant. GrantedBy può essere nil.
type CreateInput struct {
	ResourceID  uuid.UUID
	SubjectType SubjectType
	SubjectID   uuid.UUID
	Role        Role
	GrantedBy   *uuid.UUID
}

// Create assegna un ruolo a un utente o a un team. Un solo grant per
// (risorsa, soggetto): il secondo è ErrConflict.
func (s *Service) Create(ctx context.Context, in CreateInput) (Grant, error) {
	f := map[string]string{}
	if in.SubjectType != SubjectUser && in.SubjectType != SubjectTeam {
		f["subjectType"] = "user o team"
	}
	if !in.Role.Valid() {
		f["role"] = "read, write o admin"
	}
	if len(f) > 0 {
		return Grant{}, &ValidationError{Fields: f}
	}
	var userID, teamID *uuid.UUID
	var table string
	if in.SubjectType == SubjectUser {
		userID, table = &in.SubjectID, "identity.users"
	} else {
		teamID, table = &in.SubjectID, "identity.teams"
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id = $1)`, in.SubjectID).Scan(&exists); err != nil {
		return Grant{}, fmt.Errorf("verifica del soggetto non riuscita: %w", err)
	}
	if !exists {
		return Grant{}, ErrSubjectNotFound
	}
	g, err := scanGrant(s.pool.QueryRow(ctx, `
		INSERT INTO identity.resource_grants (id, resource_id, user_id, team_id, role, granted_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+grantColumns,
		uuid.New(), in.ResourceID, userID, teamID, string(in.Role), in.GrantedBy, s.now()))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Grant{}, ErrConflict
	}
	if err != nil {
		return Grant{}, fmt.Errorf("creazione del grant non riuscita: %w", err)
	}
	return g, nil
}

// List ritorna i grant di una risorsa, dal più vecchio, con il totale.
func (s *Service) List(ctx context.Context, resourceID uuid.UUID, page, perPage int) ([]Grant, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM identity.resource_grants WHERE resource_id = $1`, resourceID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("conteggio dei grant non riuscito: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+grantColumns+` FROM identity.resource_grants
		WHERE resource_id = $1
		ORDER BY created_at, id LIMIT $2 OFFSET $3`,
		resourceID, perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0, fmt.Errorf("elenco dei grant non riuscito: %w", err)
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("lettura di un grant non riuscita: %w", err)
		}
		out = append(out, g)
	}
	return out, total, rows.Err()
}

// UpdateRole cambia il ruolo di un grant della risorsa.
func (s *Service) UpdateRole(ctx context.Context, resourceID, grantID uuid.UUID, role Role) (Grant, error) {
	if !role.Valid() {
		return Grant{}, &ValidationError{Fields: map[string]string{"role": "read, write o admin"}}
	}
	g, err := scanGrant(s.pool.QueryRow(ctx, `
		UPDATE identity.resource_grants SET role = $3
		WHERE id = $1 AND resource_id = $2
		RETURNING `+grantColumns, grantID, resourceID, string(role)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Grant{}, ErrNotFound
	}
	if err != nil {
		return Grant{}, fmt.Errorf("aggiornamento del grant non riuscito: %w", err)
	}
	return g, nil
}

// Delete revoca un grant della risorsa.
func (s *Service) Delete(ctx context.Context, resourceID, grantID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM identity.resource_grants WHERE id = $1 AND resource_id = $2`, grantID, resourceID)
	if err != nil {
		return fmt.Errorf("revoca del grant non riuscita: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
