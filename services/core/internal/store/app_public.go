package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// App pubbliche (v3-c, GIT-456, H1): l'interruttore d'istanza e lo storico dei cambi di accesso.
// L'interruttore sta sulla riga singleton delle impostazioni delle app. Il modo `public` di
// un'app resta salvato anche a interruttore spento: l'esposizione la decide chi legge entrambi.

// Tipi di evento dello storico degli accessi.
const (
	AppAccessEventPublicApps = "public_apps_enabled"
	AppAccessEventAppAccess  = "app_access"
)

// AppAccessEvent è una riga dello storico: chi ha cambiato cosa e quando.
type AppAccessEvent struct {
	ID        int64
	Kind      string
	AppID     *uuid.UUID
	OldValue  *string
	NewValue  string
	Actor     *AppActor
	CreatedAt time.Time
}

// PublicAppsEnabled dice se l'amministratore ha abilitato le app pubbliche (spento di default).
func (s *Store) PublicAppsEnabled(ctx context.Context) (bool, error) {
	var on bool
	err := s.pool.QueryRow(ctx, `SELECT public_apps_enabled FROM core.app_capacity_settings WHERE singleton`).Scan(&on)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return on, err
}

// SetPublicAppsEnabled accende o spegne l'interruttore e, se il valore cambia, scrive l'evento nello
// stesso commit. Ritorna se è cambiato. I modi di accesso delle app non si toccano.
func (s *Store) SetPublicAppsEnabled(ctx context.Context, on bool, by *AppActor, now time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO core.app_capacity_settings (singleton, updated_at) VALUES (true, now()) ON CONFLICT (singleton) DO NOTHING`); err != nil {
		return false, err
	}
	var prev bool
	if err := tx.QueryRow(ctx, `SELECT public_apps_enabled FROM core.app_capacity_settings WHERE singleton FOR UPDATE`).Scan(&prev); err != nil {
		return false, err
	}
	if prev == on {
		return false, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `UPDATE core.app_capacity_settings SET public_apps_enabled = $1, updated_at = now() WHERE singleton`, on); err != nil {
		return false, err
	}
	old := boolText(prev)
	if _, err := tx.Exec(ctx, `INSERT INTO core.app_access_events (kind, app_id, old_value, new_value, actor, created_at) VALUES ($1, NULL, $2, $3, $4, $5)`,
		AppAccessEventPublicApps, old, boolText(on), actorJSON(by), now); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// SetAppAccessAudited imposta il modo di accesso dell'app e scrive l'evento con l'attore (H1, H6)
// nella stessa transazione. Non crea una versione. ErrNotFound se l'app non c'è.
func (s *Store) SetAppAccessAudited(ctx context.Context, repoID uuid.UUID, name, mode string, by *AppActor, now time.Time) (App, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return App{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id uuid.UUID
	var prev string
	err = tx.QueryRow(ctx, `SELECT id, access_mode FROM core.apps WHERE repo_id = $1 AND name = $2 FOR UPDATE`, repoID, name).Scan(&id, &prev)
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, ErrNotFound
	}
	if err != nil {
		return App{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE core.apps SET access_mode = $2, updated_at = $3 WHERE id = $1`, id, mode, now); err != nil {
		return App{}, err
	}
	if prev != mode {
		if _, err := tx.Exec(ctx, `INSERT INTO core.app_access_events (kind, app_id, old_value, new_value, actor, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
			AppAccessEventAppAccess, id, prev, mode, actorJSON(by), now); err != nil {
			return App{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return App{}, err
	}
	return s.GetApp(ctx, repoID, name)
}

// ListAppAccessEvents elenca lo storico, dal più recente. appID nil = tutto; con appID, gli eventi
// di quell'app. instance=true aggiunge (o, con appID nil, limita a) quelli dell'interruttore.
func (s *Store) ListAppAccessEvents(ctx context.Context, appID *uuid.UUID, includeInstance bool, limit int) ([]AppAccessEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, kind, app_id, old_value, new_value, actor, created_at FROM core.app_access_events
		WHERE ($1::uuid IS NULL OR app_id = $1 OR ($2 AND app_id IS NULL)) ORDER BY id DESC LIMIT $3`, appID, includeInstance, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppAccessEvent{}
	for rows.Next() {
		var e AppAccessEvent
		var by []byte
		if err := rows.Scan(&e.ID, &e.Kind, &e.AppID, &e.OldValue, &e.NewValue, &by, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Actor = decodeActor(by)
		out = append(out, e)
	}
	return out, rows.Err()
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
