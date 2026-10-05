package permissions

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OwnerType è il tipo di owner di una risorsa.
type OwnerType string

const (
	OwnerUser         OwnerType = "user"
	OwnerOrganization OwnerType = "organization"
)

// Visibility è la visibilità di una risorsa (P3): non esiste il livello
// pubblico.
type Visibility string

const (
	VisibilityPrivate  Visibility = "private"
	VisibilityInternal Visibility = "internal"
)

var (
	// ErrOwnerNotFound: l'utente o l'organizzazione owner non esiste.
	ErrOwnerNotFound = errors.New("owner non trovato")
	// ErrOwnerChanged: la risorsa ha già un altro owner, che non cambia (R3).
	ErrOwnerChanged = errors.New("l'owner della risorsa non può cambiare")
	// ErrNameNotFound: nessun utente né organizzazione con quel nome.
	ErrNameNotFound = errors.New("nome non trovato")
)

// Owner è il risultato di ResolveOwner.
type Owner struct {
	Type OwnerType
	ID   uuid.UUID
	Name string
}

// SetAttributes imposta owner e visibilità di una risorsa. Idempotente: con
// lo stesso owner aggiorna solo la visibilità. Un owner diverso da quello già
// registrato è ErrOwnerChanged, un owner inesistente ErrOwnerNotFound.
func (s *Service) SetAttributes(ctx context.Context, resourceID uuid.UUID, ot OwnerType, ownerID uuid.UUID, vis Visibility) error {
	if (ot != OwnerUser && ot != OwnerOrganization) || (vis != VisibilityPrivate && vis != VisibilityInternal) {
		return &ValidationError{Fields: map[string]string{"ownerType": "user o organization", "visibility": "private o internal"}}
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM identity.owner_names WHERE owner_type = $1 AND owner_id = $2)`,
		string(ot), ownerID).Scan(&exists); err != nil {
		return fmt.Errorf("verifica dell'owner non riuscita: %w", err)
	}
	if !exists {
		return ErrOwnerNotFound
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO identity.resource_attributes AS a (resource_id, owner_type, owner_id, visibility, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (resource_id) DO UPDATE SET visibility = EXCLUDED.visibility, updated_at = EXCLUDED.updated_at
		WHERE a.owner_type = EXCLUDED.owner_type AND a.owner_id = EXCLUDED.owner_id
		RETURNING resource_id`, resourceID, string(ot), ownerID, string(vis), s.now()).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrOwnerChanged
	}
	if err != nil {
		return fmt.Errorf("salvataggio degli attributi non riuscito: %w", err)
	}
	return nil
}

// ResolveOwner risolve un nome nello spazio di nomi unico di utenti e
// organizzazioni (R1).
func (s *Service) ResolveOwner(ctx context.Context, name string) (Owner, error) {
	var o Owner
	var t string
	err := s.pool.QueryRow(ctx, `SELECT owner_type, owner_id, name FROM identity.owner_names WHERE name = $1`, name).Scan(&t, &o.ID, &o.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return Owner{}, ErrNameNotFound
	}
	if err != nil {
		return Owner{}, fmt.Errorf("risoluzione del nome non riuscita: %w", err)
	}
	o.Type = OwnerType(t)
	return o, nil
}

// PurgeResource toglie tutti i grant e gli attributi di una risorsa
// cancellata definitivamente da core. Idempotente: senza righe non fa niente.
func (s *Service) PurgeResource(ctx context.Context, resourceID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("apertura della transazione non riuscita: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM identity.resource_grants WHERE resource_id = $1`, resourceID); err != nil {
		return fmt.Errorf("cancellazione dei grant non riuscita: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM identity.resource_attributes WHERE resource_id = $1`, resourceID); err != nil {
		return fmt.Errorf("cancellazione degli attributi non riuscita: %w", err)
	}
	return tx.Commit(ctx)
}
