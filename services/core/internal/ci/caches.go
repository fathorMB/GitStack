package ci

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Cache (CI5), compatibili con il protocollo v1 di actions/cache: un job prenota (key, version),
// carica l'archivio e conferma; un job successivo cerca per chiave (e chiavi di ripiego) e scarica.
// Quota per repo (default 10 GB): a quota piena si eliminano le meno usate di recente; le cache
// non usate da CacheUnusedDays (default 7) si eliminano comunque.

var (
	// ErrCacheExists: esiste già una cache con quella chiave, versione e ref (le cache non si sovrascrivono).
	ErrCacheExists = errors.New("cache già presente")
	// ErrCacheNotReady: la cache è prenotata ma non confermata.
	ErrCacheNotReady = errors.New("cache non confermata")
)

// Cache è una cache del repo.
type Cache struct {
	ID         uuid.UUID
	Seq        int64
	RepoID     uuid.UUID
	Key        string
	Version    string
	Ref        string
	SizeBytes  int64
	StorageKey string
	CreatedAt  time.Time
	LastUsedAt time.Time
	Ready      bool
}

func cacheStorageKey(repoID, id uuid.UUID) string {
	return "caches/" + repoID.String() + "/" + id.String()
}

const cacheColumns = `id, seq, repo_id, cache_key, version, ref, size_bytes, storage_key, created_at, last_used_at, ready_at IS NOT NULL`

func scanCache(row pgx.Row) (Cache, error) {
	var c Cache
	err := row.Scan(&c.ID, &c.Seq, &c.RepoID, &c.Key, &c.Version, &c.Ref, &c.SizeBytes, &c.StorageKey, &c.CreatedAt, &c.LastUsedAt, &c.Ready)
	return c, err
}

// ReserveCache prenota una cache per (key, version, ref). ErrTooLarge se size supera la quota del repo;
// ErrCacheExists se c'è già (confermata, o prenotata da meno di un'ora).
func ReserveCache(ctx context.Context, pool *pgxpool.Pool, now time.Time, repoID uuid.UUID, key, version, ref string, size int64, st Settings) (Cache, error) {
	if size > st.CacheQuotaBytes {
		return Cache{}, ErrTooLarge
	}
	// Una prenotazione abbandonata da più di un'ora non blocca chi riprova.
	if _, err := pool.Exec(ctx, `DELETE FROM core.ci_caches WHERE repo_id = $1 AND cache_key = $2 AND version = $3 AND ref = $4
		AND ready_at IS NULL AND created_at < $5`, repoID, key, version, ref, now.Add(-time.Hour)); err != nil {
		return Cache{}, err
	}
	id := uuid.New()
	c, err := scanCache(pool.QueryRow(ctx, `
		INSERT INTO core.ci_caches (id, repo_id, cache_key, version, ref, storage_key, created_at, last_used_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7) RETURNING `+cacheColumns,
		id, repoID, key, version, ref, cacheStorageKey(repoID, id), now))
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return Cache{}, ErrCacheExists
	}
	return c, err
}

// GetCacheBySeq legge una cache del repo dal suo numero di protocollo.
func GetCacheBySeq(ctx context.Context, pool *pgxpool.Pool, repoID uuid.UUID, seq int64) (Cache, error) {
	return scanCache(pool.QueryRow(ctx, `SELECT `+cacheColumns+` FROM core.ci_caches WHERE repo_id = $1 AND seq = $2`, repoID, seq))
}

// CommitCache scrive i byte di r come cache prenotata (seq) e la conferma. Oltre la quota del repo: ErrTooLarge,
// e la prenotazione cade. Poi applica la pulizia del repo (EvictCaches).
func CommitCache(ctx context.Context, pool *pgxpool.Pool, blobs Blobs, now time.Time, repoID uuid.UUID, seq int64, r io.Reader, st Settings) (Cache, error) {
	c, err := GetCacheBySeq(ctx, pool, repoID, seq)
	if err != nil {
		return Cache{}, err
	}
	if c.Ready {
		return Cache{}, ErrCacheExists
	}
	size, err := blobs.Put(ctx, c.StorageKey, r, st.CacheQuotaBytes)
	if err != nil {
		if errors.Is(err, ErrTooLarge) {
			_, _ = pool.Exec(context.WithoutCancel(ctx), `DELETE FROM core.ci_caches WHERE id = $1 AND ready_at IS NULL`, c.ID)
		}
		return Cache{}, err
	}
	c, err = scanCache(pool.QueryRow(ctx, `UPDATE core.ci_caches SET size_bytes = $2, ready_at = $3, last_used_at = $3
		WHERE id = $1 AND ready_at IS NULL RETURNING `+cacheColumns, c.ID, size, now))
	if err != nil {
		_ = blobs.Delete(context.WithoutCancel(ctx), c.StorageKey)
		if errors.Is(err, pgx.ErrNoRows) {
			return Cache{}, ErrCacheExists
		}
		return Cache{}, err
	}
	if _, err := EvictCaches(ctx, pool, blobs, now, &repoID, st); err != nil {
		return c, err
	}
	return c, nil
}

// FindCache cerca la cache da ripristinare: per ogni chiave in ordine, prima la corrispondenza esatta e poi
// quella per prefisso (la più recente); vale per la stessa versione e per il ref del job o il ramo predefinito.
// A parità di chiave vince il ref del job. Segna l'uso (last_used_at). pgx.ErrNoRows se non c'è.
func FindCache(ctx context.Context, pool *pgxpool.Pool, now time.Time, repoID uuid.UUID, keys []string, version, ref, defaultRef string) (Cache, error) {
	for _, k := range keys {
		if k == "" {
			continue
		}
		for _, exact := range []bool{true, false} {
			cond := `starts_with(cache_key, $2)`
			if exact {
				cond = `cache_key = $2`
			}
			c, err := scanCache(pool.QueryRow(ctx, `UPDATE core.ci_caches SET last_used_at = $5 WHERE id = (
					SELECT id FROM core.ci_caches
					WHERE repo_id = $1 AND `+cond+` AND version = $3 AND ready_at IS NOT NULL AND ref IN ($4, $6)
					ORDER BY (ref = $4) DESC, last_used_at DESC, created_at DESC LIMIT 1)
				RETURNING `+cacheColumns, repoID, k, version, ref, now, defaultRef))
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return c, err
		}
	}
	return Cache{}, pgx.ErrNoRows
}

// CacheUsedBytes sono i byte occupati dalle cache confermate di un repo.
func CacheUsedBytes(ctx context.Context, pool *pgxpool.Pool, repoID uuid.UUID) (int64, error) {
	var n int64
	err := pool.QueryRow(ctx, `SELECT COALESCE(sum(size_bytes), 0) FROM core.ci_caches WHERE repo_id = $1 AND ready_at IS NOT NULL`, repoID).Scan(&n)
	return n, err
}

// ListCaches elenca le cache confermate del repo, filtrate per prefisso della chiave e ref (vuoti = tutte).
func ListCaches(ctx context.Context, pool *pgxpool.Pool, repoID uuid.UUID, keyPrefix, ref string) ([]Cache, error) {
	rows, err := pool.Query(ctx, `SELECT `+cacheColumns+` FROM core.ci_caches
		WHERE repo_id = $1 AND ready_at IS NOT NULL AND ($2 = '' OR starts_with(cache_key, $2)) AND ($3 = '' OR ref = $3)
		ORDER BY last_used_at DESC, id`, repoID, keyPrefix, ref)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Cache
	for rows.Next() {
		c, err := scanCache(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCache legge una cache confermata del repo dall'id; pgx.ErrNoRows se non c'è.
func GetCache(ctx context.Context, pool *pgxpool.Pool, repoID, id uuid.UUID) (Cache, error) {
	return scanCache(pool.QueryRow(ctx, `SELECT `+cacheColumns+` FROM core.ci_caches WHERE repo_id = $1 AND id = $2 AND ready_at IS NOT NULL`, repoID, id))
}

// DeleteCaches elimina le cache del repo (file e righe), filtrate come ListCaches, e ritorna quante.
func DeleteCaches(ctx context.Context, pool *pgxpool.Pool, blobs Blobs, repoID uuid.UUID, keyPrefix, ref string) (int, error) {
	rows, err := pool.Query(ctx, `SELECT id, storage_key FROM core.ci_caches
		WHERE repo_id = $1 AND ($2 = '' OR starts_with(cache_key, $2)) AND ($3 = '' OR ref = $3)`, repoID, keyPrefix, ref)
	if err != nil {
		return 0, err
	}
	victims, err := collectVictims(rows)
	if err != nil {
		return 0, err
	}
	return deleteVictims(ctx, pool, blobs, victims)
}

// DeleteCache elimina una cache del repo; pgx.ErrNoRows se non c'è.
func DeleteCache(ctx context.Context, pool *pgxpool.Pool, blobs Blobs, repoID, id uuid.UUID) error {
	c, err := GetCache(ctx, pool, repoID, id)
	if err != nil {
		return err
	}
	_, err = deleteVictims(ctx, pool, blobs, []victim{{c.ID, c.StorageKey}})
	return err
}

type victim struct {
	id  uuid.UUID
	key string
}

func collectVictims(rows pgx.Rows) ([]victim, error) {
	defer rows.Close()
	var out []victim
	for rows.Next() {
		var v victim
		if err := rows.Scan(&v.id, &v.key); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func deleteVictims(ctx context.Context, pool *pgxpool.Pool, blobs Blobs, vs []victim) (int, error) {
	n := 0
	for _, v := range vs {
		if err := blobs.Delete(ctx, v.key); err != nil {
			return n, err
		}
		tag, err := pool.Exec(ctx, `DELETE FROM core.ci_caches WHERE id = $1`, v.id)
		if err != nil {
			return n, err
		}
		n += int(tag.RowsAffected())
	}
	return n, nil
}

// EvictCaches applica la pulizia di CI5: elimina le prenotazioni abbandonate (più di un'ora), le cache non usate da
// CacheUnusedDays e, per ogni repo (o solo repoID, se non nil) sopra la quota, le meno usate di recente fino a rientrare.
// Ritorna quante cache ha eliminato.
func EvictCaches(ctx context.Context, pool *pgxpool.Pool, blobs Blobs, now time.Time, repoID *uuid.UUID, st Settings) (int, error) {
	var scope any
	if repoID != nil {
		scope = *repoID
	}
	rows, err := pool.Query(ctx, `SELECT id, storage_key FROM core.ci_caches
		WHERE ($1::uuid IS NULL OR repo_id = $1)
		AND ((ready_at IS NULL AND created_at < $2) OR (ready_at IS NOT NULL AND last_used_at < $3))`,
		scope, now.Add(-time.Hour), now.Add(-st.CacheUnused()))
	if err != nil {
		return 0, err
	}
	vs, err := collectVictims(rows)
	if err != nil {
		return 0, err
	}
	total, err := deleteVictims(ctx, pool, blobs, vs)
	if err != nil {
		return total, err
	}
	// Quota: per i repo sopra, dalle meno usate, finché la somma rientra.
	over, err := pool.Query(ctx, `SELECT repo_id FROM core.ci_caches WHERE ready_at IS NOT NULL AND ($1::uuid IS NULL OR repo_id = $1)
		GROUP BY repo_id HAVING sum(size_bytes) > $2`, scope, st.CacheQuotaBytes)
	if err != nil {
		return total, err
	}
	var repos []uuid.UUID
	for over.Next() {
		var id uuid.UUID
		if err := over.Scan(&id); err != nil {
			over.Close()
			return total, err
		}
		repos = append(repos, id)
	}
	over.Close()
	if err := over.Err(); err != nil {
		return total, err
	}
	for _, rid := range repos {
		used, err := CacheUsedBytes(ctx, pool, rid)
		if err != nil {
			return total, err
		}
		crows, err := pool.Query(ctx, `SELECT id, storage_key, size_bytes FROM core.ci_caches WHERE repo_id = $1 AND ready_at IS NOT NULL
			ORDER BY last_used_at, created_at, id`, rid)
		if err != nil {
			return total, err
		}
		var pick []victim
		for crows.Next() && used > st.CacheQuotaBytes {
			var v victim
			var size int64
			if err := crows.Scan(&v.id, &v.key, &size); err != nil {
				crows.Close()
				return total, err
			}
			pick = append(pick, v)
			used -= size
		}
		crows.Close()
		n, err := deleteVictims(ctx, pool, blobs, pick)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
