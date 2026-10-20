package ci

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Segreti e variabili della CI (CI4). I segreti sono cifrati con lo stesso keyring dei
// segreti dei webhook (AES-256-GCM, l'id della riga è il dato autenticato) e non si
// rileggono mai: l'elenco dà solo nome e data. Le tabelle sono separate da quelle
// dei webhook e dalle app della v3 (H8).

// Limiti di nomi e valori.
const (
	MaxSecretBytes   = 48 << 10
	MaxVariableBytes = 64 << 10
	MaxNameLen       = 100
	// Un limite sul numero tiene a freno il costo del carico a ogni job.
	MaxPerScope = 1000
)

// Sealer cifra e decifra i segreti (webhooks.Keyring lo implementa).
type Sealer interface {
	Seal(rowID uuid.UUID, secret string) (ciphertext, nonce []byte, keyID string, err error)
	Open(rowID uuid.UUID, ciphertext, nonce []byte, keyID string) (string, error)
}

// ErrTooMany: il bersaglio ha già MaxPerScope voci.
var ErrTooMany = errors.New("troppi segreti o variabili")

// Target è il bersaglio di segreti e variabili: un repo o un'organizzazione.
type Target struct {
	Org    bool
	RepoID uuid.UUID // se !Org
	OrgID  uuid.UUID // se Org
}

func (t Target) scope() string {
	if t.Org {
		return "org"
	}
	return "repo"
}

func (t Target) id() uuid.UUID {
	if t.Org {
		return t.OrgID
	}
	return t.RepoID
}

func (t Target) cond() string {
	if t.Org {
		return `scope = 'org' AND org_id = $1`
	}
	return `scope = 'repo' AND repo_id = $1`
}

// Entry è nome e data di un segreto o di una variabile.
type Entry struct {
	Name      string
	Value     string // solo per le variabili
	UpdatedAt time.Time
}

func table(secret bool) string {
	if secret {
		return "core.ci_secrets"
	}
	return "core.ci_variables"
}

// List elenca nomi (e valori, per le variabili) del bersaglio. Mai i valori dei segreti.
func List(ctx context.Context, pool *pgxpool.Pool, t Target, secret bool) ([]Entry, error) {
	col := "value"
	if secret {
		col = "''"
	}
	rows, err := pool.Query(ctx, `SELECT name, `+col+`, updated_at FROM `+table(secret)+` WHERE `+t.cond()+` ORDER BY name`, t.id())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Name, &e.Value, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PutSecret crea o sostituisce un segreto.
func PutSecret(ctx context.Context, pool *pgxpool.Pool, keys Sealer, now time.Time, t Target, by uuid.UUID, name, value string) (Entry, error) {
	id := uuid.New()
	ct, nonce, keyID, err := keys.Seal(id, value)
	if err != nil {
		return Entry{}, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Entry{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var existing uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM core.ci_secrets WHERE `+t.cond()+` AND name = $2 FOR UPDATE`, t.id(), name).Scan(&existing)
	switch {
	case err == nil:
		// L'id è dato autenticato: la riga esistente tiene il suo id, quindi si ricifra con quello.
		if ct, nonce, keyID, err = keys.Seal(existing, value); err != nil {
			return Entry{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE core.ci_secrets SET ciphertext = $2, nonce = $3, secret_key_id = $4, updated_at = $5 WHERE id = $1`,
			existing, ct, nonce, keyID, now); err != nil {
			return Entry{}, err
		}
	case errors.Is(err, pgx.ErrNoRows):
		if err := checkRoom(ctx, tx, t, true); err != nil {
			return Entry{}, err
		}
		repo, org := t.nullable()
		if _, err := tx.Exec(ctx, `INSERT INTO core.ci_secrets (id, scope, repo_id, org_id, name, ciphertext, nonce, secret_key_id, created_by, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`,
			id, t.scope(), repo, org, name, ct, nonce, keyID, by, now); err != nil {
			return Entry{}, err
		}
	default:
		return Entry{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Entry{}, err
	}
	return Entry{Name: name, UpdatedAt: now}, nil
}

func (t Target) nullable() (repo, org *uuid.UUID) {
	if t.Org {
		return nil, &t.OrgID
	}
	return &t.RepoID, nil
}

func checkRoom(ctx context.Context, tx pgx.Tx, t Target, secret bool) error {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+table(secret)+` WHERE `+t.cond(), t.id()).Scan(&n); err != nil {
		return err
	}
	if n >= MaxPerScope {
		return ErrTooMany
	}
	return nil
}

// PutVariable crea o sostituisce una variabile.
func PutVariable(ctx context.Context, pool *pgxpool.Pool, now time.Time, t Target, by uuid.UUID, name, value string) (Entry, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Entry{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE core.ci_variables SET value = $3, updated_at = $4 WHERE `+t.cond()+` AND name = $2`, t.id(), name, value, now)
	if err != nil {
		return Entry{}, err
	}
	if tag.RowsAffected() == 0 {
		if err := checkRoom(ctx, tx, t, false); err != nil {
			return Entry{}, err
		}
		repo, org := t.nullable()
		if _, err := tx.Exec(ctx, `INSERT INTO core.ci_variables (id, scope, repo_id, org_id, name, value, created_by, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`, uuid.New(), t.scope(), repo, org, name, value, by, now); err != nil {
			return Entry{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Entry{}, err
	}
	return Entry{Name: name, Value: value, UpdatedAt: now}, nil
}

// Delete elimina un segreto o una variabile; false se non c'era.
func Delete(ctx context.Context, pool *pgxpool.Pool, t Target, secret bool, name string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM `+table(secret)+` WHERE `+t.cond()+` AND name = $2`, t.id(), name)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// JobEnv sono segreti e variabili in chiaro per un job: quelli di repo prevalgono su
// quelli dell'organizzazione con lo stesso nome.
type JobEnv struct {
	Secrets map[string]string
	Vars    map[string]string
}

// LoadJobEnv decifra segreti e variabili che valgono per il repo (e, se è di un'organizzazione,
// per la sua organizzazione). Un segreto che non si decifra (chiave persa) è un errore: meglio
// fallire il job che farlo girare senza.
func LoadJobEnv(ctx context.Context, pool *pgxpool.Pool, keys Sealer, repoID uuid.UUID) (JobEnv, error) {
	env := JobEnv{Secrets: map[string]string{}, Vars: map[string]string{}}
	var ownerType string
	var ownerID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT owner_type, owner_id FROM core.repositories WHERE resource_id = $1`, repoID).Scan(&ownerType, &ownerID); err != nil {
		return env, fmt.Errorf("repo: %w", err)
	}
	var orgID *uuid.UUID
	if ownerType == "organization" {
		orgID = &ownerID
	}
	// Prima l'organizzazione, poi il repo che la sovrascrive.
	for _, scope := range []string{"org", "repo"} {
		if scope == "org" && orgID == nil {
			continue
		}
		var target uuid.UUID
		col := "repo_id"
		if scope == "org" {
			target, col = *orgID, "org_id"
		} else {
			target = repoID
		}
		rows, err := pool.Query(ctx, `SELECT id, name, ciphertext, nonce, secret_key_id FROM core.ci_secrets WHERE scope = $1 AND `+col+` = $2`, scope, target)
		if err != nil {
			return env, err
		}
		for rows.Next() {
			var id uuid.UUID
			var name, keyID string
			var ct, nonce []byte
			if err := rows.Scan(&id, &name, &ct, &nonce, &keyID); err != nil {
				rows.Close()
				return env, err
			}
			v, err := keys.Open(id, ct, nonce, keyID)
			if err != nil {
				rows.Close()
				return env, fmt.Errorf("segreto %s non decifrabile: %w", name, err)
			}
			env.Secrets[name] = v
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return env, err
		}
		vrows, err := pool.Query(ctx, `SELECT name, value FROM core.ci_variables WHERE scope = $1 AND `+col+` = $2`, scope, target)
		if err != nil {
			return env, err
		}
		for vrows.Next() {
			var name, value string
			if err := vrows.Scan(&name, &value); err != nil {
				vrows.Close()
				return env, err
			}
			env.Vars[name] = value
		}
		vrows.Close()
		if err := vrows.Err(); err != nil {
			return env, err
		}
	}
	return env, nil
}

// HasSecrets dice se il repo (o la sua organizzazione) ha almeno un segreto.
func HasSecrets(ctx context.Context, pool *pgxpool.Pool, repoID uuid.UUID) (bool, error) {
	var has bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM core.ci_secrets WHERE scope = 'repo' AND repo_id = $1)
		    OR EXISTS (SELECT 1 FROM core.ci_secrets s JOIN core.repositories r ON r.owner_id = s.org_id
		               WHERE s.scope = 'org' AND r.resource_id = $1 AND r.owner_type = 'organization')`, repoID).Scan(&has)
	return has, err
}
