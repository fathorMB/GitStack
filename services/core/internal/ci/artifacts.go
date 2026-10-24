package ci

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Artefatti (CI5): file prodotti da un job, conservati per ArtifactRetentionDays (default 30)
// fino a ArtifactMaxBytes ciascuno (default 2 GB), su disco o su S3. Un artefatto è
// dell'esecuzione: il nome è unico nell'esecuzione (come upload-artifact v4).

// ErrArtifactExists: l'esecuzione ha già un artefatto con quel nome.
var ErrArtifactExists = errors.New("artefatto già presente")

// ErrArtifactName: nome vuoto, troppo lungo o con caratteri di controllo.
var ErrArtifactName = errors.New("nome di artefatto non valido")

// Artifact è un artefatto conservato.
type Artifact struct {
	ID         uuid.UUID
	RepoID     uuid.UUID
	RunID      uuid.UUID
	Name       string
	SizeBytes  int64
	StorageKey string
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

// ValidArtifactName: 1-255 caratteri UTF-8 senza controlli né barre rovesciate.
func ValidArtifactName(name string) bool {
	if name == "" || len(name) > 255 || !utf8.ValidString(name) || strings.ContainsAny(name, "\\") {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func artifactKey(repoID, id uuid.UUID) string {
	return "artifacts/" + repoID.String() + "/" + id.String()
}

const artifactColumns = `id, repo_id, run_id, name, size_bytes, storage_key, created_at, expires_at`

func scanArtifact(row pgx.Row) (Artifact, error) {
	var a Artifact
	err := row.Scan(&a.ID, &a.RepoID, &a.RunID, &a.Name, &a.SizeBytes, &a.StorageKey, &a.CreatedAt, &a.ExpiresAt)
	return a, err
}

// SaveArtifact scrive l'artefatto del job a. Oltre st.ArtifactMaxBytes: ErrTooLarge. Se l'esecuzione ne ha già
// uno con quel nome: ErrArtifactExists. Il file è scritto prima della riga: un errore non lascia righe senza file.
func SaveArtifact(ctx context.Context, pool *pgxpool.Pool, blobs Blobs, now time.Time, a JobAuth, name string, r io.Reader, st Settings) (Artifact, error) {
	if !ValidArtifactName(name) {
		return Artifact{}, ErrArtifactName
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.ci_artifacts WHERE run_id = $1 AND name = $2)`, a.RunID, name).Scan(&exists); err != nil {
		return Artifact{}, err
	}
	if exists {
		return Artifact{}, ErrArtifactExists
	}
	id := uuid.New()
	key := artifactKey(a.RepoID, id)
	size, err := blobs.Put(ctx, key, r, st.ArtifactMaxBytes)
	if err != nil {
		return Artifact{}, err
	}
	art, err := scanArtifact(pool.QueryRow(ctx, `
		INSERT INTO core.ci_artifacts (id, repo_id, run_id, job_id, name, size_bytes, storage_key, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING `+artifactColumns,
		id, a.RepoID, a.RunID, a.JobID, name, size, key, now, now.Add(st.ArtifactRetention())))
	if err != nil {
		_ = blobs.Delete(context.WithoutCancel(ctx), key)
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return Artifact{}, ErrArtifactExists
		}
		return Artifact{}, err
	}
	return art, nil
}

// ListArtifacts elenca gli artefatti non scaduti di un'esecuzione del repo; pgx.ErrNoRows se l'esecuzione non è del repo.
func ListArtifacts(ctx context.Context, pool *pgxpool.Pool, now time.Time, repoID, runID uuid.UUID) ([]Artifact, error) {
	var one int
	if err := pool.QueryRow(ctx, `SELECT 1 FROM core.ci_runs WHERE id = $1 AND repo_id = $2`, runID, repoID).Scan(&one); err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, `SELECT `+artifactColumns+` FROM core.ci_artifacts
		WHERE run_id = $1 AND repo_id = $2 AND expires_at > $3 ORDER BY created_at, name`, runID, repoID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetArtifact legge un artefatto non scaduto del repo; pgx.ErrNoRows se non c'è, è scaduto o è di un altro repo.
func GetArtifact(ctx context.Context, pool *pgxpool.Pool, now time.Time, repoID, id uuid.UUID) (Artifact, error) {
	return scanArtifact(pool.QueryRow(ctx, `SELECT `+artifactColumns+` FROM core.ci_artifacts
		WHERE id = $1 AND repo_id = $2 AND expires_at > $3`, id, repoID, now))
}

// PurgeExpiredArtifacts elimina gli artefatti scaduti (file e riga). Idempotente; ritorna quanti ne ha eliminati.
func PurgeExpiredArtifacts(ctx context.Context, pool *pgxpool.Pool, blobs Blobs, now time.Time) (int, error) {
	total := 0
	for {
		rows, err := pool.Query(ctx, `SELECT id, storage_key FROM core.ci_artifacts WHERE expires_at <= $1 ORDER BY expires_at LIMIT 200`, now)
		if err != nil {
			return total, err
		}
		type item struct {
			id  uuid.UUID
			key string
		}
		var items []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.id, &it.key); err != nil {
				rows.Close()
				return total, err
			}
			items = append(items, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return total, err
		}
		for _, it := range items {
			if err := blobs.Delete(ctx, it.key); err != nil {
				return total, fmt.Errorf("artefatto %s: %w", it.id, err)
			}
			if _, err := pool.Exec(ctx, `DELETE FROM core.ci_artifacts WHERE id = $1`, it.id); err != nil {
				return total, err
			}
			total++
		}
		if len(items) < 200 {
			return total, nil
		}
	}
}
