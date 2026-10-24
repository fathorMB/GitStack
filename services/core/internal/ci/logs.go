package ci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Limiti dei log di un job in v2-a. La conservazione a 90 giorni e la pulizia
// sono in logs_purge.go (PurgeOldLogs): qui si scrive soltanto.
const (
	DefaultMaxLogBytes = 64 << 20
	maxLogLineBytes    = 16 << 10
	logTruncatedLine   = "[log troncato: raggiunto il limite di dimensione del job]"
)

// LogLine è una riga di log (runner.v1 LogRow).
type LogLine struct {
	Time    time.Time
	Content string
}

// LogStore scrive i log dei job su disco, nel volume di core (cartella logs/ di ci-data), inclusi nel backup (CI5).
// Un file per job, <Dir>/<jobId>.log, una riga per riga di log: «<RFC3339 UTC> <testo>».
// Il numero di righe e i byte validi stanno in core.ci_job_logs.
type LogStore struct {
	Dir string
	// MaxBytes: oltre questo il log si tronca (0 = DefaultMaxLogBytes).
	MaxBytes int64
}

func (l *LogStore) path(jobID uuid.UUID) string { return filepath.Join(l.Dir, jobID.String()+".log") }

func (l *LogStore) max() int64 {
	if l.MaxBytes > 0 {
		return l.MaxBytes
	}
	return DefaultMaxLogBytes
}

func cleanLogContent(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", " "), "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len(s) > maxLogLineBytes {
		s = strings.ToValidUTF8(s[:maxLogLineBytes], "") + "…"
	}
	return s
}

// Append aggiunge le righe che iniziano all'indice index (come in UpdateLog) e
// ritorna l'indice successivo atteso (ack). È idempotente: le righe già scritte
// si saltano, e se index è oltre la prima riga mancante non scrive nulla e
// risponde con quella, così il runner rimanda dal punto giusto.
func (l *LogStore) Append(ctx context.Context, pool *pgxpool.Pool, now time.Time, jobID uuid.UUID, index int64, rows []LogLine) (int64, error) {
	return l.AppendMasked(ctx, pool, now, jobID, index, rows, nil)
}

// AppendMasked è Append che toglie prima di scrivere i segreti del job (CI4): masker nil non maschera.
func (l *LogStore) AppendMasked(ctx context.Context, pool *pgxpool.Pool, now time.Time, jobID uuid.UUID, index int64, rows []LogLine, mask *Masker) (int64, error) {
	if l.Dir == "" {
		return 0, errors.New("directory dei log non configurata")
	}
	if index < 0 {
		index = 0
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `INSERT INTO core.ci_job_logs (job_id) VALUES ($1) ON CONFLICT DO NOTHING`, jobID); err != nil {
		return 0, err
	}
	var lines, size int64
	var truncated bool
	if err := tx.QueryRow(ctx, `SELECT lines, bytes, truncated FROM core.ci_job_logs WHERE job_id = $1 FOR UPDATE`, jobID).
		Scan(&lines, &size, &truncated); err != nil {
		return 0, err
	}
	if index > lines {
		return lines, nil // buco: il runner rimanda da `lines`
	}
	skip := int(lines - index)
	if skip >= len(rows) {
		return lines, nil // tutte già scritte
	}
	rows = rows[skip:]
	var buf bytes.Buffer
	for _, r := range rows {
		ts := r.Time.UTC()
		if r.Time.IsZero() {
			ts = now.UTC()
		}
		line := ts.Format("2006-01-02T15:04:05.000Z") + " " + cleanLogContent(mask.Mask(r.Content)) + "\n"
		if truncated || size+int64(buf.Len())+int64(len(line)) > l.max() {
			if !truncated {
				truncated = true
				buf.WriteString(now.UTC().Format("2006-01-02T15:04:05.000Z") + " " + logTruncatedLine + "\n")
			}
			// Le righe oltre il limite si contano (il runner non le rimanda) ma non si scrivono.
			lines++
			continue
		}
		buf.WriteString(line)
		lines++
	}
	if buf.Len() > 0 {
		if err := os.MkdirAll(l.Dir, 0o750); err != nil {
			return 0, err
		}
		f, err := os.OpenFile(l.path(jobID), os.O_CREATE|os.O_WRONLY, 0o640)
		if err != nil {
			return 0, err
		}
		// Il file può avere byte oltre quelli registrati (scrittura di una transazione poi annullata): si riparte da size.
		if err := f.Truncate(size); err != nil {
			_ = f.Close()
			return 0, err
		}
		if _, err := f.WriteAt(buf.Bytes(), size); err != nil {
			_ = f.Close()
			return 0, err
		}
		if err := f.Close(); err != nil {
			return 0, err
		}
		size += int64(buf.Len())
	}
	if _, err := tx.Exec(ctx, `UPDATE core.ci_job_logs SET lines = $2, bytes = $3, truncated = $4, updated_at = $5 WHERE job_id = $1`,
		jobID, lines, size, truncated, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return lines, nil
}

// LogSize ritorna i byte validi del log del job (0 se non ha ancora righe).
func (l *LogStore) LogSize(ctx context.Context, pool *pgxpool.Pool, jobID uuid.UUID) (int64, error) {
	var size int64
	err := pool.QueryRow(ctx, `SELECT bytes FROM core.ci_job_logs WHERE job_id = $1`, jobID).Scan(&size)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return size, err
}

// ReadFrom copia in w i byte del log da offset a to (esclusa) e ritorna quanti ne ha copiati.
func (l *LogStore) ReadFrom(jobID uuid.UUID, offset, to int64, w io.Writer) (int64, error) {
	if to <= offset {
		return 0, nil
	}
	f, err := os.Open(l.path(jobID))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	return io.Copy(w, io.NewSectionReader(f, offset, to-offset))
}

// JobRef trova un job di un repo dal suo id (404 se è di un altro repo).
func JobRef(ctx context.Context, pool *pgxpool.Pool, repoID, jobID uuid.UUID) (JobRow, error) {
	j, err := GetJob(ctx, pool, repoID, jobID)
	if err != nil {
		return JobRow{}, fmt.Errorf("job %s: %w", jobID, err)
	}
	return j, nil
}
