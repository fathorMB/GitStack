package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
)

// Backup, ripristino e aggiornamento minor dei database gestiti (v3-d, GIT-553, ADR 0003, DB6, DB13, DB15).
// Qui stanno i metadati: i dump sono file cifrati su databases-data e non passano mai dal database di core.

// Tipi e stati dei backup (colonne kind e status di core.managed_db_backups).
const (
	BackupScheduled = "scheduled"
	BackupOnDemand  = "on_demand"
	BackupSafety    = "safety"

	BackupInProgress = "in_progress"
	BackupCompleted  = "completed"
	BackupFailed     = "failed"
)

// Modi di ripristino.
const (
	RestoreSame = "same"
	RestoreNew  = "new"
)

// Quanti dump completati si tengono per tipo (ADR 0003, punto 5; la sicurezza e' una scelta di GIT-553).
const (
	KeepScheduled = 7
	KeepOnDemand  = 5
	KeepSafety    = 3
)

var (
	// ErrBackupInProgress: c'e' gia' un dump in corso per il database (409 backup_in_progress).
	ErrBackupInProgress = errors.New("un backup di questo database e' gia' in corso")
	// ErrBackupNotFound: il dump non esiste per questo database.
	ErrBackupNotFound = errors.New("backup non trovato")
	// ErrDBBusy: il database ha gia' un ripristino o un aggiornamento in attesa.
	ErrDBBusy = errors.New("il database ha un'operazione in corso")
)

// ManagedDBBackup e' una riga di core.managed_db_backups.
type ManagedDBBackup struct {
	ID          uuid.UUID
	DBID        uuid.UUID
	Kind        string
	Status      string
	SizeBytes   *int64
	Error       *string
	File        *string
	CreatedBy   *AppActor
	CreatedAt   time.Time
	CompletedAt *time.Time
}

const backupCols = `b.id, b.db_id, b.kind, b.status, b.size_bytes, b.error, b.file, b.created_by, b.created_at, b.completed_at`

func scanBackup(row pgx.Row) (ManagedDBBackup, error) {
	var b ManagedDBBackup
	var by []byte
	err := row.Scan(&b.ID, &b.DBID, &b.Kind, &b.Status, &b.SizeBytes, &b.Error, &b.File, &by, &b.CreatedAt, &b.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrBackupNotFound
	}
	if err != nil {
		return b, err
	}
	if len(by) > 0 {
		var a AppActor
		if json.Unmarshal(by, &a) == nil {
			b.CreatedBy = &a
		}
	}
	return b, nil
}

// BeginManagedDBBackup registra un dump `in_progress`. ErrBackupInProgress se ce n'e' gia' uno (indice unico).
func (s *Store) BeginManagedDBBackup(ctx context.Context, d ManagedDB, kind string, actor *AppActor, now time.Time) (ManagedDBBackup, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return ManagedDBBackup{}, err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO core.managed_db_backups (id, db_id, kind, status, created_by, created_at) VALUES ($1,$2,$3,'in_progress',$4,$5)`,
		id, d.ID, kind, actorJSON(actor), now)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return ManagedDBBackup{}, ErrBackupInProgress
	}
	if err != nil {
		return ManagedDBBackup{}, err
	}
	return ManagedDBBackup{ID: id, DBID: d.ID, Kind: kind, Status: BackupInProgress, CreatedBy: actor, CreatedAt: now}, nil
}

// CompleteManagedDBBackup chiude un dump riuscito: file, dimensione, evento e audit nella stessa transazione.
// Azzera l'errore di backup del database se era dello stesso tipo.
func (s *Store) CompleteManagedDBBackup(ctx context.Context, d ManagedDB, b ManagedDBBackup, file string, size int64, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `UPDATE core.managed_db_backups SET status = 'completed', file = $2, size_bytes = $3, completed_at = $4, error = NULL WHERE id = $1`,
		b.ID, file, size, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE core.managed_dbs SET last_backup_at = $2,
		backup_error = CASE WHEN backup_error_kind = $3 THEN NULL ELSE backup_error END,
		backup_error_kind = CASE WHEN backup_error_kind = $3 THEN NULL ELSE backup_error_kind END WHERE id = $1`, d.ID, now, b.Kind); err != nil {
		return err
	}
	if err := dbAudit(ctx, tx, d, "backup_completed", b.CreatedBy, map[string]any{"backupId": b.ID, "kind": b.Kind, "sizeBytes": size}, now); err != nil {
		return err
	}
	if err := dbEvent(ctx, tx, domainevents.DatabaseBackupCompleted, d, b.CreatedBy, func(p *domainevents.DatabasePayload) {
		p.SizeBytes = &size
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// FailManagedDBBackup chiude un dump fallito e lo rende visibile nel database (backup_error) e nel suo audit.
func (s *Store) FailManagedDBBackup(ctx context.Context, d ManagedDB, b ManagedDBBackup, reason string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `UPDATE core.managed_db_backups SET status = 'failed', error = $2, completed_at = $3 WHERE id = $1`, b.ID, reason, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE core.managed_dbs SET backup_error = $2, backup_error_kind = $3 WHERE id = $1`, d.ID, reason, b.Kind); err != nil {
		return err
	}
	if err := dbAudit(ctx, tx, d, "backup_failed", b.CreatedBy, map[string]any{"backupId": b.ID, "kind": b.Kind, "error": reason}, now); err != nil {
		return err
	}
	if err := dbEvent(ctx, tx, domainevents.DatabaseBackupFailed, d, b.CreatedBy, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ListManagedDBBackups elenca i dump del database, dal piu' recente.
func (s *Store) ListManagedDBBackups(ctx context.Context, dbID uuid.UUID) ([]ManagedDBBackup, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+backupCols+` FROM core.managed_db_backups b WHERE b.db_id = $1 ORDER BY b.created_at DESC, b.id DESC`, dbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ManagedDBBackup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetManagedDBBackup legge un dump di un database.
func (s *Store) GetManagedDBBackup(ctx context.Context, dbID, id uuid.UUID) (ManagedDBBackup, error) {
	return scanBackup(s.pool.QueryRow(ctx, `SELECT `+backupCols+` FROM core.managed_db_backups b WHERE b.db_id = $1 AND b.id = $2`, dbID, id))
}

// LatestManagedDBBackup ritorna l'ultimo dump (di qualunque esito) di un tipo, o ErrBackupNotFound.
func (s *Store) LatestManagedDBBackup(ctx context.Context, dbID uuid.UUID, kind string) (ManagedDBBackup, error) {
	return scanBackup(s.pool.QueryRow(ctx, `SELECT `+backupCols+` FROM core.managed_db_backups b WHERE b.db_id = $1 AND b.kind = $2 ORDER BY b.created_at DESC LIMIT 1`, dbID, kind))
}

// PruneManagedDBBackups applica la retention a un tipo: tiene gli ultimi `keep` completati, toglie gli altri e i
// falliti piu' vecchi del piu' vecchio completato tenuto (o di 7 giorni, se non ce ne sono). Ritorna i file da
// cancellare dal disco. Un dump `in_progress` non si tocca mai.
func (s *Store) PruneManagedDBBackups(ctx context.Context, dbID uuid.UUID, kind string, keep int, now time.Time) ([]string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	rows, err := tx.Query(ctx, `DELETE FROM core.managed_db_backups WHERE id IN (
		SELECT id FROM core.managed_db_backups WHERE db_id = $1 AND kind = $2 AND status = 'completed'
		ORDER BY created_at DESC, id DESC OFFSET $3) RETURNING file`, dbID, kind, keep)
	if err != nil {
		return nil, err
	}
	var files []string
	for rows.Next() {
		var f *string
		if err := rows.Scan(&f); err != nil {
			rows.Close()
			return nil, err
		}
		if f != nil {
			files = append(files, *f)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM core.managed_db_backups WHERE db_id = $1 AND kind = $2 AND status = 'failed' AND created_at < $3`,
		dbID, kind, now.Add(-7*24*time.Hour)); err != nil {
		return nil, err
	}
	return files, tx.Commit(ctx)
}

// FailStaleManagedDBBackups chiude come falliti i dump rimasti `in_progress` senza un processo che li esegua
// (core riavviato a meta'). `live` sono i dump in corso in questo processo. Ritorna quanti ne ha chiusi.
func (s *Store) FailStaleManagedDBBackups(ctx context.Context, live []uuid.UUID, olderThan time.Time, now time.Time) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+backupCols+` FROM core.managed_db_backups b WHERE b.status = 'in_progress' AND b.created_at < $1 AND NOT (b.id = ANY($2))`, olderThan, live)
	if err != nil {
		return 0, err
	}
	var stale []ManagedDBBackup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		stale = append(stale, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, b := range stale {
		d, err := s.GetManagedDBByID(ctx, b.DBID)
		if err != nil {
			continue
		}
		if err := s.FailManagedDBBackup(ctx, d, b, "interrotto: core si e' riavviato durante il dump", now); err == nil {
			n++
		}
	}
	return n, nil
}

// PendingRestore e' un ripristino in attesa sul database (colonne restore_*).
type PendingRestore struct {
	BackupID    uuid.UUID
	File        string
	Mode        string
	RequestedBy *AppActor
}

// UpgradeRequest e' una richiesta di aggiornamento minor in attesa.
type UpgradeRequest struct {
	RequestedAt time.Time
	RequestedBy *AppActor
}

// ManagedDBOps raccoglie lo stato delle operazioni di manutenzione di un database: ultimo backup, errori e
// richieste pendenti (letti insieme a ManagedDB, ma fuori dalla riga base per non toccare le sue query).
type ManagedDBOps struct {
	LastBackupAt    *time.Time
	BackupError     *string
	BackupErrorKind *string
	UpgradeError    *string
	UpgradeRequest  *UpgradeRequest
	RestoreError    *string
	Restore         *PendingRestore
}

const opsCols = `last_backup_at, backup_error, backup_error_kind, upgrade_error, upgrade_requested_at, upgrade_requested_by,
	restore_error, restore_backup_id, restore_file, restore_mode, restore_requested_by`

func scanOps(row pgx.Row) (ManagedDBOps, error) {
	var o ManagedDBOps
	var upAt *time.Time
	var upBy, rsBy []byte
	var rsID *uuid.UUID
	var rsFile, rsMode *string
	err := row.Scan(&o.LastBackupAt, &o.BackupError, &o.BackupErrorKind, &o.UpgradeError, &upAt, &upBy, &o.RestoreError, &rsID, &rsFile, &rsMode, &rsBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, ErrManagedDBNotFound
	}
	if err != nil {
		return o, err
	}
	if upAt != nil {
		o.UpgradeRequest = &UpgradeRequest{RequestedAt: *upAt, RequestedBy: decodeActor(upBy)}
	}
	if rsID != nil && rsFile != nil && rsMode != nil {
		o.Restore = &PendingRestore{BackupID: *rsID, File: *rsFile, Mode: *rsMode, RequestedBy: decodeActor(rsBy)}
	}
	return o, nil
}

// ManagedDBOperations legge lo stato di manutenzione di un database.
func (s *Store) ManagedDBOperations(ctx context.Context, id uuid.UUID) (ManagedDBOps, error) {
	return scanOps(s.pool.QueryRow(ctx, `SELECT `+opsCols+` FROM core.managed_dbs WHERE id = $1`, id))
}

// ManagedDBsWithBackupProblems elenca i database con un errore di backup, di ripristino o di aggiornamento
// ancora aperto: li legge `gitstack status` (via psql) e li stampa il portale.
func (s *Store) ManagedDBsWithBackupProblems(ctx context.Context) ([]ManagedDB, error) {
	return s.listManagedDBs(ctx, `WHERE d.backup_error IS NOT NULL OR d.restore_error IS NOT NULL OR d.upgrade_error IS NOT NULL`)
}

// RequestManagedDBRestore mette un ripristino in attesa sul database `d` (il controller lo esegue quando il
// database e' `running`). Mode `same` porta il database in `restoring`; per `new` il database nasce gia' in
// `creating` e resta tale finche' non e' pronto. ErrDBBusy se c'e' gia' un ripristino o un aggiornamento in attesa.
func (s *Store) RequestManagedDBRestore(ctx context.Context, d ManagedDB, backupID uuid.UUID, file, mode string, actor *AppActor, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE core.managed_dbs SET restore_backup_id = $2, restore_file = $3, restore_mode = $4, restore_requested_by = $5,
		restore_error = NULL, updated_at = $6,
		status = CASE WHEN $4 = 'same' THEN 'restoring' ELSE status END,
		status_reason = CASE WHEN $4 = 'same' THEN NULL ELSE status_reason END
		WHERE id = $1 AND status NOT IN ('deleting', 'failed') AND restore_backup_id IS NULL AND upgrade_requested_at IS NULL
		AND ($4 = 'new' OR status = 'running')`, d.ID, backupID, file, mode, actorJSON(actor), now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrDBBusy
	}
	if err := dbAudit(ctx, tx, d, "restore_requested", actor, map[string]any{"backupId": backupID, "mode": mode}, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// FinishManagedDBRestore chiude il ripristino pendente: ok azzera l'errore ed emette database.restored, altrimenti
// scrive restore_error (visibile nel database). Lo stato lo riscrive il controller.
func (s *Store) FinishManagedDBRestore(ctx context.Context, d ManagedDB, p PendingRestore, runErr error, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var msg *string
	if runErr != nil {
		m := runErr.Error()
		msg = &m
	}
	if _, err := tx.Exec(ctx, `UPDATE core.managed_dbs SET restore_backup_id = NULL, restore_file = NULL, restore_mode = NULL, restore_requested_by = NULL,
		restore_error = $2, updated_at = $3 WHERE id = $1`, d.ID, msg, now); err != nil {
		return err
	}
	if runErr == nil {
		if err := dbAudit(ctx, tx, d, "restored", p.RequestedBy, map[string]any{"backupId": p.BackupID, "mode": p.Mode}, now); err != nil {
			return err
		}
		if err := dbEvent(ctx, tx, domainevents.DatabaseRestored, d, p.RequestedBy, nil); err != nil {
			return err
		}
	} else if err := dbAudit(ctx, tx, d, "restore_failed", p.RequestedBy, map[string]any{"backupId": p.BackupID, "mode": p.Mode, "error": msg}, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RequestManagedDBMinorUpgrade registra la richiesta di aggiornamento minor del database (DB6). Ritorna false se
// non serve (richiesta gia' pendente o database non in `running`); ErrDBBusy se c'e' un ripristino in attesa.
func (s *Store) RequestManagedDBMinorUpgrade(ctx context.Context, d ManagedDB, actor *AppActor, now time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var busy bool
	if err := tx.QueryRow(ctx, `SELECT restore_backup_id IS NOT NULL OR status NOT IN ('running') FROM core.managed_dbs WHERE id = $1 FOR UPDATE`, d.ID).Scan(&busy); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrManagedDBNotFound
		}
		return false, err
	}
	if busy {
		return false, ErrDBBusy
	}
	tag, err := tx.Exec(ctx, `UPDATE core.managed_dbs SET upgrade_requested_at = $2, upgrade_requested_by = $3, upgrade_error = NULL, updated_at = $2
		WHERE id = $1 AND upgrade_requested_at IS NULL`, d.ID, now, actorJSON(actor))
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if err := dbAudit(ctx, tx, d, "minor_upgrade_requested", actor, nil, now); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// FinishManagedDBMinorUpgrade chiude la richiesta pendente. Con runErr scrive upgrade_error (il database resta
// sul digest di prima); con ok registra l'audit del nuovo digest.
func (s *Store) FinishManagedDBMinorUpgrade(ctx context.Context, d ManagedDB, by *AppActor, newDigest string, runErr error, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var msg *string
	if runErr != nil {
		m := runErr.Error()
		msg = &m
	}
	if _, err := tx.Exec(ctx, `UPDATE core.managed_dbs SET upgrade_requested_at = NULL, upgrade_requested_by = NULL, upgrade_error = $2, updated_at = $3 WHERE id = $1`, d.ID, msg, now); err != nil {
		return err
	}
	if runErr != nil {
		if err := dbAudit(ctx, tx, d, "minor_upgrade_failed", by, map[string]any{"error": msg}, now); err != nil {
			return err
		}
	} else if err := dbAudit(ctx, tx, d, "minor_upgraded", by, map[string]any{"digest": newDigest}, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RequestAllManagedDBMinorUpgrades registra la richiesta su tutti i database `running` (amministratore d'istanza).
// Ritorna quanti database hanno una richiesta nuova.
func (s *Store) RequestAllManagedDBMinorUpgrades(ctx context.Context, actor *AppActor, now time.Time) (int, error) {
	dbs, err := s.listManagedDBs(ctx, `WHERE d.status = 'running'`)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range dbs {
		ok, err := s.RequestManagedDBMinorUpgrade(ctx, d, actor, now)
		if errors.Is(err, ErrDBBusy) || errors.Is(err, ErrManagedDBNotFound) {
			continue
		}
		if err != nil {
			return n, err
		}
		if ok {
			n++
		}
	}
	return n, nil
}
