package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrAttachmentNotLinkable: uno degli allegati indicati non esiste, è di un
// altro repo o di un altro utente, o è già collegato (GIT-104 lo traduce in 422).
var ErrAttachmentNotLinkable = errors.New("allegato non collegabile")

// Attachment sono i metadati di un allegato (I9); il file sta sul volume.
type Attachment struct {
	ID          uuid.UUID
	RepoID      uuid.UUID
	IssueID     *uuid.UUID
	CommentID   *uuid.UUID
	UploaderID  uuid.UUID
	Filename    string
	ContentType string
	Size        int64
	CreatedAt   time.Time
	// IssueHidden: l'allegato è di una issue nascosta (solo da GetAttachment).
	IssueHidden bool
}

const attachmentCols = `a.id, a.repo_id, a.issue_id, a.comment_id, a.uploader_id, a.filename, a.content_type, a.size_bytes, a.created_at`

func scanAttachment(r row) (Attachment, error) {
	var a Attachment
	err := r.Scan(&a.ID, &a.RepoID, &a.IssueID, &a.CommentID, &a.UploaderID, &a.Filename, &a.ContentType, &a.Size, &a.CreatedAt)
	return a, err
}

// CreateAttachment salva i metadati di un allegato appena scritto su disco:
// nasce non collegato. createdAt vale `now`, così la pulizia usa lo stesso
// orologio dell'applicazione.
func (s *Store) CreateAttachment(ctx context.Context, a Attachment, now time.Time) (Attachment, error) {
	row := s.pool.QueryRow(ctx, `INSERT INTO core.issue_attachments AS a (id, repo_id, uploader_id, filename, content_type, size_bytes, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+attachmentCols,
		a.ID, a.RepoID, a.UploaderID, a.Filename, a.ContentType, a.Size, now)
	return scanAttachment(row)
}

// GetAttachment legge un allegato del repo; ErrNotFound se non c'è o è di un
// altro repo.
func (s *Store) GetAttachment(ctx context.Context, repoID, id uuid.UUID) (Attachment, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+attachmentCols+`, COALESCE(i.hidden, false)
		FROM core.issue_attachments a LEFT JOIN core.issues i ON i.id = a.issue_id
		WHERE a.id = $1 AND a.repo_id = $2`, id, repoID)
	var a Attachment
	err := row.Scan(&a.ID, &a.RepoID, &a.IssueID, &a.CommentID, &a.UploaderID, &a.Filename, &a.ContentType, &a.Size, &a.CreatedAt, &a.IssueHidden)
	if errors.Is(err, pgx.ErrNoRows) {
		return Attachment{}, ErrNotFound
	}
	return a, err
}

// LinkAttachments collega, dentro la transazione del chiamante, gli allegati
// non ancora collegati dello stesso repo e dello stesso utente alla issue
// (e, se commentID non è nil, al commento). Ripetizioni nell'elenco contano
// una volta. Se anche uno solo non è collegabile ritorna
// ErrAttachmentNotLinkable e il chiamante annulla la transazione. Il
// controllo e il collegamento sono un solo UPDATE: un allegato preso nello
// stesso momento dalla pulizia degli orfani o da un'altra richiesta non si
// collega due volte.
func LinkAttachments(ctx context.Context, tx pgx.Tx, repoID, uploaderID, issueID uuid.UUID, commentID *uuid.UUID, ids []uuid.UUID) error {
	uniq := make([]uuid.UUID, 0, len(ids))
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	if len(uniq) == 0 {
		return nil
	}
	tag, err := tx.Exec(ctx, `UPDATE core.issue_attachments SET issue_id = $1, comment_id = $2
		WHERE id = ANY($3::uuid[]) AND repo_id = $4 AND uploader_id = $5 AND issue_id IS NULL`,
		issueID, commentID, uniq, repoID, uploaderID)
	if err != nil {
		return fmt.Errorf("collegamento degli allegati: %w", err)
	}
	if tag.RowsAffected() != int64(len(uniq)) {
		return ErrAttachmentNotLinkable
	}
	return nil
}

// BeginTx apre una transazione sul pool (per chi deve passarla a
// LinkAttachments insieme ad altre scritture).
func (s *Store) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return s.pool.Begin(ctx)
}

// DeleteOrphanAttachments elimina gli allegati non collegati creati prima di
// `before`: per ciascuno chiama remove (il file) e poi cancella la riga, in
// una transazione con FOR UPDATE SKIP LOCKED (più repliche si dividono il
// lavoro; un collegamento concorrente aspetta e poi trova la riga sparita).
// Ritorna quanti ne ha eliminati; un errore di remove ferma la corsa e la
// riga resta per la prossima.
func (s *Store) DeleteOrphanAttachments(ctx context.Context, before time.Time, remove func(Attachment) error) (int, error) {
	total := 0
	for {
		n, err := s.deleteOrphanBatch(ctx, before, remove)
		total += n
		if err != nil || n == 0 {
			return total, err
		}
	}
}

const orphanBatch = 100

func (s *Store) deleteOrphanBatch(ctx context.Context, before time.Time, remove func(Attachment) error) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT `+attachmentCols+` FROM core.issue_attachments a
		WHERE a.issue_id IS NULL AND a.created_at < $1
		ORDER BY a.created_at LIMIT $2 FOR UPDATE SKIP LOCKED`, before, orphanBatch)
	if err != nil {
		return 0, err
	}
	var list []Attachment
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	done := make([]uuid.UUID, 0, len(list))
	var removeErr error
	for _, a := range list {
		if err := remove(a); err != nil {
			removeErr = fmt.Errorf("allegato %s: %w", a.ID, err)
			break
		}
		done = append(done, a.ID)
	}
	if len(done) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM core.issue_attachments WHERE id = ANY($1::uuid[])`, done); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(done), removeErr
}
