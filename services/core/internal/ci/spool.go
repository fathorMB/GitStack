package ci

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// File d'appoggio dei pezzi di una cache in caricamento (protocollo v1 di actions/cache):
// <dir>/cache-<seq>.part. Se il job muore a metà il file resterebbe fuori da ogni quota.

// SpoolStaleAfter: un file d'appoggio più vecchio di così e senza prenotazione viva si elimina.
const SpoolStaleAfter = time.Hour

var spoolNameRe = regexp.MustCompile(`^cache-(\d+)\.part$`)

// SpoolDirOrDefault ritorna dir, o la cartella temporanea di sistema se è vuota.
func SpoolDirOrDefault(dir string) string {
	if dir == "" {
		return filepath.Join(os.TempDir(), "gitstack-ci-cache")
	}
	return dir
}

// SpoolPath è il file d'appoggio della cache con quel numero di protocollo.
func SpoolPath(dir string, seq int64) string {
	return filepath.Join(SpoolDirOrDefault(dir), "cache-"+strconv.FormatInt(seq, 10)+".part")
}

// PurgeSpool elimina i cache-*.part più vecchi di SpoolStaleAfter il cui seq non ha più una riga
// prenotata (ready_at IS NULL). Ritorna quanti ne ha eliminati. Una cartella che manca non è un errore.
func PurgeSpool(ctx context.Context, pool *pgxpool.Pool, dir string, now time.Time) (int, error) {
	entries, err := os.ReadDir(SpoolDirOrDefault(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		m := spoolNameRe.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) <= SpoolStaleAfter {
			continue
		}
		seq, _ := strconv.ParseInt(m[1], 10, 64)
		var live bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.ci_caches WHERE seq = $1 AND ready_at IS NULL)`, seq).Scan(&live); err != nil {
			return n, err
		}
		if live {
			continue
		}
		if err := os.Remove(filepath.Join(SpoolDirOrDefault(dir), e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return n, err
		}
		n++
	}
	return n, nil
}

// PurgeTombstones elimina i file (disco o S3) delle righe di artefatti e cache cadute, anche in
// cascata, e poi la tombstone. Idempotente. Ritorna quanti file ha trattato.
func PurgeTombstones(ctx context.Context, pool *pgxpool.Pool, blobs Blobs) (int, error) {
	total := 0
	for {
		rows, err := pool.Query(ctx, `SELECT storage_key FROM core.ci_blob_tombstones ORDER BY deleted_at, storage_key LIMIT 200`)
		if err != nil {
			return total, err
		}
		var keys []string
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return total, err
			}
			keys = append(keys, k)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return total, err
		}
		for _, k := range keys {
			if err := blobs.Delete(ctx, k); err != nil {
				return total, err
			}
			if _, err := pool.Exec(ctx, `DELETE FROM core.ci_blob_tombstones WHERE storage_key = $1`, k); err != nil {
				return total, err
			}
			total++
		}
		if len(keys) < 200 {
			return total, nil
		}
	}
}
