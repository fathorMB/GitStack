package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Variabili e segreti delle app (v3-a, GIT-348, H8). Stanno in tabelle proprie (core.app_variables,
// core.app_secrets), separate da quelle della CI (core.ci_secrets, core.ci_variables): nessun join
// e nessuna query in comune. Ogni modifica crea una versione di configurazione (trigger config) con
// il suo deploy; la versione copia le variabili (valori) e i nomi dei segreti, mai i valori dei segreti.

// ErrEnvNameInUse: il nome è già una variabile o un segreto dell'app (409 name_in_use).
var ErrEnvNameInUse = errors.New("il nome è già usato da una variabile o da un segreto dell'app")

// execQuerier è ciò che serve per leggere e scrivere dentro o fuori una transazione.
type execQuerier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// AppVariable è una variabile dell'app, con la versione che l'ha impostata.
type AppVariable struct {
	Name      string
	Value     string
	Version   int
	UpdatedAt time.Time
	UpdatedBy *AppActor
}

// AppSecretMeta sono i metadati di un segreto dell'app: mai il valore.
type AppSecretMeta struct {
	Name      string
	Version   int
	UpdatedAt time.Time
	UpdatedBy *AppActor
}

// lockApp blocca la riga dell'app: le modifiche di variabili e segreti si serializzano tra loro e
// con UpdateApp, così i numeri di versione non si contendono. ErrNotFound se l'app non c'è.
func lockApp(ctx context.Context, tx pgx.Tx, appID uuid.UUID) error {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM core.apps WHERE id = $1 FOR UPDATE`, appID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// allocVersion prende il numero della versione successiva dell'app.
func allocVersion(ctx context.Context, tx pgx.Tx, appID uuid.UUID, now time.Time) (int, error) {
	var number int
	err := tx.QueryRow(ctx, `UPDATE core.apps SET next_version = next_version + 1, updated_at = $2 WHERE id = $1
		RETURNING next_version - 1`, appID, now).Scan(&number)
	return number, err
}

// currentConfig è la configurazione che avrebbe una nuova versione: i campi dell'app più variabili
// e nomi dei segreti attuali.
func currentConfig(ctx context.Context, tx pgx.Tx, appID uuid.UUID) (AppConfig, error) {
	app, err := scanApp(tx.QueryRow(ctx, `SELECT `+appCols+appFrom+` WHERE a.id = $1`, appID))
	if err != nil {
		return AppConfig{}, err
	}
	cfg := app.Config
	cfg.Variables, cfg.SecretNames, err = envNames(ctx, tx, appID)
	return cfg, err
}

// writeConfigVersion scrive la versione number con la configurazione data e il suo deploy.
// L'immagine è quella dell'ultima versione, come in UpdateApp.
func writeConfigVersion(ctx context.Context, tx pgx.Tx, appID uuid.UUID, number int, cfg AppConfig, summary string, actor *AppActor, now time.Time) (AppVersion, error) {
	last, err := scanVersion(tx.QueryRow(ctx, `SELECT `+versionCols+` FROM core.app_versions v WHERE v.app_id = $1 ORDER BY v.number DESC LIMIT 1`, appID))
	if err != nil {
		return AppVersion{}, err
	}
	if err := insertAppVersion(ctx, tx, appID, number, "config", last.Image, cfg, &summary, nil, nil, actor, now); err != nil {
		return AppVersion{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO core.app_deploys (id, app_id, version, started_at) VALUES ($1, $2, $3, $4)`, uuid.New(), appID, number, now); err != nil {
		return AppVersion{}, err
	}
	return scanVersion(tx.QueryRow(ctx, `SELECT `+versionCols+` FROM core.app_versions v WHERE v.app_id = $1 AND v.number = $2`, appID, number))
}

// envNames legge variabili (nome→valore) e nomi dei segreti dell'app, in ordine di nome.
func envNames(ctx context.Context, q execQuerier, appID uuid.UUID) (map[string]string, []string, error) {
	vars := map[string]string{}
	rows, err := q.Query(ctx, `SELECT name, value FROM core.app_variables WHERE app_id = $1`, appID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var n, v string
		if err := rows.Scan(&n, &v); err != nil {
			rows.Close()
			return nil, nil, err
		}
		vars[n] = v
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	names := []string{}
	nrows, err := q.Query(ctx, `SELECT name FROM core.app_secrets WHERE app_id = $1 ORDER BY name`, appID)
	if err != nil {
		return nil, nil, err
	}
	for nrows.Next() {
		var n string
		if err := nrows.Scan(&n); err != nil {
			nrows.Close()
			return nil, nil, err
		}
		names = append(names, n)
	}
	nrows.Close()
	return vars, names, nrows.Err()
}

// nameTakenByOther dice se il nome è già usato dall'altro tipo (segreto o variabile) dell'app.
func nameTakenByOther(ctx context.Context, tx pgx.Tx, appID uuid.UUID, name string, secret bool) (bool, error) {
	table := "core.app_variables"
	if !secret {
		table = "core.app_secrets"
	}
	var taken bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE app_id = $1 AND name = $2)`, appID, name).Scan(&taken)
	return taken, err
}

// scanActor legge l'attore salvato in JSONB (nil se assente).
func scanActor(b []byte) (*AppActor, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var a AppActor
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// ListAppVariables elenca le variabili dell'app per nome.
func (s *Store) ListAppVariables(ctx context.Context, appID uuid.UUID) ([]AppVariable, error) {
	rows, err := s.pool.Query(ctx, `SELECT name, value, version, updated_at, updated_by FROM core.app_variables
		WHERE app_id = $1 ORDER BY name`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppVariable{}
	for rows.Next() {
		var v AppVariable
		var by []byte
		if err := rows.Scan(&v.Name, &v.Value, &v.Version, &v.UpdatedAt, &by); err != nil {
			return nil, err
		}
		if v.UpdatedBy, err = scanActor(by); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// PutAppVariable crea o sostituisce una variabile e crea la versione di configurazione con il
// suo deploy. ErrEnvNameInUse se il nome è già un segreto dell'app.
func (s *Store) PutAppVariable(ctx context.Context, appID uuid.UUID, name, value string, actor *AppActor, now time.Time) (AppVariable, AppVersion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AppVariable{}, AppVersion{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := lockApp(ctx, tx, appID); err != nil {
		return AppVariable{}, AppVersion{}, err
	}
	if taken, err := nameTakenByOther(ctx, tx, appID, name, false); err != nil {
		return AppVariable{}, AppVersion{}, err
	} else if taken {
		return AppVariable{}, AppVersion{}, ErrEnvNameInUse
	}
	number, err := allocVersion(ctx, tx, appID, now)
	if err != nil {
		return AppVariable{}, AppVersion{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO core.app_variables (app_id, name, value, version, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (app_id, name) DO UPDATE SET value = EXCLUDED.value, version = EXCLUDED.version,
			updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at`,
		appID, name, value, number, actorJSON(actor), now); err != nil {
		return AppVariable{}, AppVersion{}, err
	}
	cfg, err := currentConfig(ctx, tx, appID)
	if err != nil {
		return AppVariable{}, AppVersion{}, err
	}
	ver, err := writeConfigVersion(ctx, tx, appID, number, cfg, "variabile "+name, actor, now)
	if err != nil {
		return AppVariable{}, AppVersion{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AppVariable{}, AppVersion{}, err
	}
	return AppVariable{Name: name, Value: value, Version: number, UpdatedAt: now, UpdatedBy: actor}, ver, nil
}

// DeleteAppVariable elimina una variabile e crea la versione di configurazione. ErrNotFound se
// il nome non c'è.
func (s *Store) DeleteAppVariable(ctx context.Context, appID uuid.UUID, name string, actor *AppActor, now time.Time) (AppVersion, error) {
	return s.removeAppEnv(ctx, appID, name, false, actor, now)
}

// removeAppEnv toglie una variabile (secret false) o un segreto (secret true) e crea la versione.
func (s *Store) removeAppEnv(ctx context.Context, appID uuid.UUID, name string, secret bool, actor *AppActor, now time.Time) (AppVersion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AppVersion{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := lockApp(ctx, tx, appID); err != nil {
		return AppVersion{}, err
	}
	table, kind := "core.app_variables", "variabile "
	if secret {
		table, kind = "core.app_secrets", "segreto "
	}
	tag, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE app_id = $1 AND name = $2`, appID, name)
	if err != nil {
		return AppVersion{}, err
	}
	if tag.RowsAffected() == 0 {
		return AppVersion{}, ErrNotFound
	}
	number, err := allocVersion(ctx, tx, appID, now)
	if err != nil {
		return AppVersion{}, err
	}
	cfg, err := currentConfig(ctx, tx, appID)
	if err != nil {
		return AppVersion{}, err
	}
	ver, err := writeConfigVersion(ctx, tx, appID, number, cfg, kind+name+" rimossa", actor, now)
	if err != nil {
		return AppVersion{}, err
	}
	return ver, tx.Commit(ctx)
}
