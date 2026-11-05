package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/fathorMB/GitStack/services/core/internal/outbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Database gestiti (v3-d, GIT-551, ADR 0003): modello Postgres. Le password non stanno mai qui.

// Stati osservati (colonna status e DatabaseStatus del contratto).
const (
	DBCreating   = "creating"
	DBRunning    = "running"
	DBRestarting = "restarting"
	DBUpdating   = "updating"
	DBRestoring  = "restoring"
	DBDeleting   = "deleting"
	DBFailed     = "failed"
)

// Stati del limite morbido di disco (DB3).
const (
	DiskNone    = "none"
	DiskWarning = "warning"
	DiskFull    = "full"
)

// Valori di default dell'ADR 0003 (punto 3): 1 GiB, 100m, 256 MiB.
const (
	DefaultDBSizeBytes   int64 = 1 << 30
	DefaultDBCPUMillis         = 100
	DefaultDBMemoryBytes int64 = 256 << 20
)

var (
	ErrManagedDBNameTaken = errors.New("esiste gia' un database con questo nome per l'owner")
	ErrManagedDBNotFound  = errors.New("database non trovato")
	ErrManagedDBLinkTaken = errors.New("l'app e' gia' collegata al database")
)

// ManagedDB e' una riga di core.managed_dbs.
type ManagedDB struct {
	ID              uuid.UUID
	OwnerName       string
	OwnerType       string
	OwnerID         uuid.UUID
	Name            string
	PgMajor         int
	SizeBytes       int64
	CPUMillis       int
	MemoryBytes     int64
	Status          string
	StatusReason    *string
	LANEnabled      bool
	LANPort         *int
	ImageDigest     *string
	ImageVerifiedAt *time.Time
	UsageBytes      *int64
	DiskState       string
	Restarts        int
	Generation      int64
	CreatedBy       *AppActor
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LinkCount       int
}

// NewManagedDB sono i dati gia' validati di una creazione.
type NewManagedDB struct {
	ID          uuid.UUID
	OwnerName   string
	OwnerType   string
	OwnerID     uuid.UUID
	Name        string
	PgMajor     int
	SizeBytes   int64
	CPUMillis   int
	MemoryBytes int64
	Actor       *AppActor
}

// ManagedDBChange e' un ridimensionamento dichiarato: i campi nil non cambiano.
type ManagedDBChange struct {
	SizeBytes   *int64
	CPUMillis   *int
	MemoryBytes *int64
}

const managedDBCols = `d.id, d.owner_name, d.owner_type, d.owner_id, d.name, d.pg_major, d.size_bytes, d.cpu_millis, d.memory_bytes,
	d.status, d.status_reason, d.lan_enabled, d.lan_port, d.image_digest, d.image_verified_at, d.usage_bytes, d.disk_state,
	d.restarts, d.generation, d.created_by, d.created_at, d.updated_at,
	(SELECT count(*) FROM core.managed_db_links l WHERE l.db_id = d.id)::int`

func scanManagedDB(row pgx.Row) (ManagedDB, error) {
	var d ManagedDB
	var by []byte
	err := row.Scan(&d.ID, &d.OwnerName, &d.OwnerType, &d.OwnerID, &d.Name, &d.PgMajor, &d.SizeBytes, &d.CPUMillis, &d.MemoryBytes,
		&d.Status, &d.StatusReason, &d.LANEnabled, &d.LANPort, &d.ImageDigest, &d.ImageVerifiedAt, &d.UsageBytes, &d.DiskState,
		&d.Restarts, &d.Generation, &by, &d.CreatedAt, &d.UpdatedAt, &d.LinkCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrManagedDBNotFound
	}
	if err != nil {
		return d, err
	}
	if len(by) > 0 {
		var a AppActor
		if json.Unmarshal(by, &a) == nil {
			d.CreatedBy = &a
		}
	}
	return d, nil
}

func (d ManagedDB) snapshot() domainevents.Database {
	return domainevents.Database{ID: d.ID.String(), Owner: d.OwnerName, Name: d.Name, PgMajor: d.PgMajor}
}

// dbCapacityError calcola cosa manca alla quota dell'owner e alla riserva d'istanza per un
// incremento (need), nel formato di details.missing di insufficient_capacity (DB3).
func dbCapacityError(owner string, limits, used AppQuotaAmounts, needCPU int, needMem, needDisk int64, capacity AppCapacity) error {
	missing := map[string]int64{}
	if needCPU > 0 && int64(used.CPUMillis)+int64(needCPU) > int64(limits.CPUMillis) {
		missing["cpuMillis"] = int64(used.CPUMillis) + int64(needCPU) - int64(limits.CPUMillis)
	}
	if needMem > 0 && used.MemoryBytes+needMem > limits.MemoryBytes {
		missing["memoryBytes"] = used.MemoryBytes + needMem - limits.MemoryBytes
	}
	if needDisk > 0 && used.DiskBytes+needDisk > limits.DiskBytes {
		missing["diskBytes"] = used.DiskBytes + needDisk - limits.DiskBytes
	}
	if needCPU > 0 && capacity.Used.CPUMillis+needCPU > capacity.Share.CPUMillis {
		missing["reserveCpuMillis"] = int64(capacity.Used.CPUMillis + needCPU - capacity.Share.CPUMillis)
	}
	if needMem > 0 && capacity.Used.MemoryBytes+needMem > capacity.Share.MemoryBytes {
		missing["reserveMemoryBytes"] = capacity.Used.MemoryBytes + needMem - capacity.Share.MemoryBytes
	}
	if len(missing) == 0 {
		return nil
	}
	return &CapacityError{Owner: owner, Missing: missing, Limits: limits, Used: used}
}

// lockOwnerQuota prende le serrature della quota dell'owner e della riserva d'istanza, nello stesso
// ordine di CreateApp: due scritture concorrenti non superano insieme il tetto (DB3).
func lockOwnerQuota(ctx context.Context, tx pgx.Tx, owner string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('app-quota:' || lower($1), 0))`, owner); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('app-capacity', 0))`)
	return err
}

func dbAudit(ctx context.Context, tx pgx.Tx, d ManagedDB, action string, actor *AppActor, detail any, now time.Time) error {
	var raw any
	if detail != nil {
		b, err := json.Marshal(detail)
		if err != nil {
			return err
		}
		raw = b
	}
	_, err := tx.Exec(ctx, `INSERT INTO core.managed_db_audit (db_id, owner_name, name, action, actor, detail, at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		d.ID, d.OwnerName, d.Name, action, actorJSON(actor), raw, now)
	return err
}

func dbEvent(ctx context.Context, tx outbox.Execer, name string, d ManagedDB, actor *AppActor, fill func(*domainevents.DatabasePayload)) error {
	p := domainevents.DatabasePayload{Database: d.snapshot(), Actor: actorUser(actor)}
	if fill != nil {
		fill(&p)
	}
	return outbox.Enqueue(ctx, tx, name, domainevents.Version, p)
}

// CreateManagedDB crea il database contando CPU, RAM e disco nella quota dell'owner (la stessa delle
// app, DB3). 409 name_conflict (ErrManagedDBNameTaken) se il nome e' gia' usato dall'owner (DB1).
func (s *Store) CreateManagedDB(ctx context.Context, in NewManagedDB, now time.Time) (ManagedDB, error) {
	owner := strings.ToLower(in.OwnerName)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ManagedDB{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := lockOwnerQuota(ctx, tx, owner); err != nil {
		return ManagedDB{}, err
	}
	limits, used, _, err := ownerAppQuota(ctx, tx, owner)
	if err != nil {
		return ManagedDB{}, err
	}
	capacity, err := appCapacity(ctx, tx)
	if err != nil {
		return ManagedDB{}, err
	}
	if capacity.Used, err = guaranteedApps(ctx, tx); err != nil {
		return ManagedDB{}, err
	}
	if err := dbCapacityError(owner, limits, used, in.CPUMillis, in.MemoryBytes, in.SizeBytes, capacity); err != nil {
		return ManagedDB{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.managed_dbs (id, owner_name, owner_type, owner_id, name, pg_major, size_bytes, cpu_millis,
			memory_bytes, created_by, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)`,
		in.ID, owner, in.OwnerType, in.OwnerID, in.Name, in.PgMajor, in.SizeBytes, in.CPUMillis, in.MemoryBytes, actorJSON(in.Actor), now)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return ManagedDB{}, ErrManagedDBNameTaken
		}
		return ManagedDB{}, err
	}
	d, err := scanManagedDB(tx.QueryRow(ctx, `SELECT `+managedDBCols+` FROM core.managed_dbs d WHERE d.id = $1`, in.ID))
	if err != nil {
		return ManagedDB{}, err
	}
	if err := dbAudit(ctx, tx, d, "created", in.Actor, map[string]any{"pgMajor": d.PgMajor, "sizeBytes": d.SizeBytes, "cpuMillis": d.CPUMillis, "memoryBytes": d.MemoryBytes}, now); err != nil {
		return ManagedDB{}, err
	}
	if err := dbEvent(ctx, tx, domainevents.DatabaseCreated, d, in.Actor, func(p *domainevents.DatabasePayload) {
		p.SizeBytes, p.CPUMillis, p.MemoryBytes = &d.SizeBytes, &d.CPUMillis, &d.MemoryBytes
	}); err != nil {
		return ManagedDB{}, err
	}
	return d, tx.Commit(ctx)
}

// UpdateManagedDB ridimensiona la dimensione dichiarata (e CPU/RAM). La quota si ricontrolla sul solo
// incremento (DB3); lo stato passa a `updating` e la generazione sale, cosi' il controller riallinea.
func (s *Store) UpdateManagedDB(ctx context.Context, id uuid.UUID, ch ManagedDBChange, actor *AppActor, now time.Time) (ManagedDB, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ManagedDB{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	cur, err := scanManagedDB(tx.QueryRow(ctx, `SELECT `+managedDBCols+` FROM core.managed_dbs d WHERE d.id = $1`, id))
	if err != nil {
		return ManagedDB{}, err
	}
	if err := lockOwnerQuota(ctx, tx, cur.OwnerName); err != nil {
		return ManagedDB{}, err
	}
	// Rilegge sotto serratura: un altro ridimensionamento puo' averlo cambiato.
	if cur, err = scanManagedDB(tx.QueryRow(ctx, `SELECT `+managedDBCols+` FROM core.managed_dbs d WHERE d.id = $1 FOR UPDATE`, id)); err != nil {
		return ManagedDB{}, err
	}
	next := cur
	changes := map[string]map[string]int64{}
	if ch.SizeBytes != nil && *ch.SizeBytes != cur.SizeBytes {
		next.SizeBytes = *ch.SizeBytes
		changes["sizeBytes"] = map[string]int64{"from": cur.SizeBytes}
	}
	if ch.CPUMillis != nil && *ch.CPUMillis != cur.CPUMillis {
		next.CPUMillis = *ch.CPUMillis
		changes["cpuMillis"] = map[string]int64{"from": int64(cur.CPUMillis)}
	}
	if ch.MemoryBytes != nil && *ch.MemoryBytes != cur.MemoryBytes {
		next.MemoryBytes = *ch.MemoryBytes
		changes["memoryBytes"] = map[string]int64{"from": cur.MemoryBytes}
	}
	if len(changes) == 0 {
		return cur, nil
	}
	limits, used, _, err := ownerAppQuota(ctx, tx, cur.OwnerName)
	if err != nil {
		return ManagedDB{}, err
	}
	capacity, err := appCapacity(ctx, tx)
	if err != nil {
		return ManagedDB{}, err
	}
	if capacity.Used, err = guaranteedApps(ctx, tx); err != nil {
		return ManagedDB{}, err
	}
	if err := dbCapacityError(cur.OwnerName, limits, used, next.CPUMillis-cur.CPUMillis, next.MemoryBytes-cur.MemoryBytes, next.SizeBytes-cur.SizeBytes, capacity); err != nil {
		return ManagedDB{}, err
	}
	// Il limite morbido cambia: lo stato del disco si ricalcola alla prossima misura di pg_database_size.
	if _, err := tx.Exec(ctx, `UPDATE core.managed_dbs SET size_bytes = $2, cpu_millis = $3, memory_bytes = $4, generation = generation + 1,
			status = CASE WHEN status = 'running' THEN 'updating' ELSE status END, updated_at = $5 WHERE id = $1`,
		id, next.SizeBytes, next.CPUMillis, next.MemoryBytes, now); err != nil {
		return ManagedDB{}, err
	}
	d, err := scanManagedDB(tx.QueryRow(ctx, `SELECT `+managedDBCols+` FROM core.managed_dbs d WHERE d.id = $1`, id))
	if err != nil {
		return ManagedDB{}, err
	}
	raw, _ := json.Marshal(changes)
	if err := dbAudit(ctx, tx, d, "updated", actor, changes, now); err != nil {
		return ManagedDB{}, err
	}
	if err := dbEvent(ctx, tx, domainevents.DatabaseUpdated, d, actor, func(p *domainevents.DatabasePayload) { p.Changes = raw }); err != nil {
		return ManagedDB{}, err
	}
	return d, tx.Commit(ctx)
}

// GetManagedDB legge un database per owner e nome (owner minuscolo).
func (s *Store) GetManagedDB(ctx context.Context, owner, name string) (ManagedDB, error) {
	return scanManagedDB(s.pool.QueryRow(ctx, `SELECT `+managedDBCols+` FROM core.managed_dbs d WHERE d.owner_name = lower($1) AND d.name = $2`, owner, name))
}

// GetManagedDBByID legge un database per id.
func (s *Store) GetManagedDBByID(ctx context.Context, id uuid.UUID) (ManagedDB, error) {
	return scanManagedDB(s.pool.QueryRow(ctx, `SELECT `+managedDBCols+` FROM core.managed_dbs d WHERE d.id = $1`, id))
}

func (s *Store) listManagedDBs(ctx context.Context, where string, args ...any) ([]ManagedDB, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+managedDBCols+` FROM core.managed_dbs d `+where+` ORDER BY d.owner_name, d.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ManagedDB
	for rows.Next() {
		d, err := scanManagedDB(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListManagedDBs elenca i database di un owner.
func (s *Store) ListManagedDBs(ctx context.Context, owner string) ([]ManagedDB, error) {
	return s.listManagedDBs(ctx, `WHERE d.owner_name = lower($1)`, owner)
}

// ListAllManagedDBs elenca tutti i database (per il controller).
func (s *Store) ListAllManagedDBs(ctx context.Context) ([]ManagedDB, error) {
	return s.listManagedDBs(ctx, ``)
}

// MarkManagedDBDeleting porta il database in `deleting` (idempotente) e scrive l'audit: da qui il
// nome e' ancora occupato finche' il controller non ha tolto le risorse (RemoveManagedDB).
func (s *Store) MarkManagedDBDeleting(ctx context.Context, id uuid.UUID, actor *AppActor, now time.Time) (ManagedDB, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ManagedDB{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	d, err := scanManagedDB(tx.QueryRow(ctx, `SELECT `+managedDBCols+` FROM core.managed_dbs d WHERE d.id = $1 FOR UPDATE`, id))
	if err != nil {
		return ManagedDB{}, err
	}
	if d.Status == DBDeleting {
		return d, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE core.managed_dbs SET status = 'deleting', status_reason = NULL, generation = generation + 1, updated_at = $2 WHERE id = $1`, id, now); err != nil {
		return ManagedDB{}, err
	}
	d.Status = DBDeleting
	if err := dbAudit(ctx, tx, d, "delete_requested", actor, nil, now); err != nil {
		return ManagedDB{}, err
	}
	return d, tx.Commit(ctx)
}

// RemoveManagedDB toglie la riga (e i link a cascata) dopo che il controller ha eliminato
// namespace, PVC e Secret: il nome si libera (DB19). Scrive audit ed evento database.deleted.
func (s *Store) RemoveManagedDB(ctx context.Context, id uuid.UUID, actor *AppActor, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	d, err := scanManagedDB(tx.QueryRow(ctx, `SELECT `+managedDBCols+` FROM core.managed_dbs d WHERE d.id = $1 FOR UPDATE`, id))
	if errors.Is(err, ErrManagedDBNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM core.managed_dbs WHERE id = $1`, id); err != nil {
		return err
	}
	if err := dbAudit(ctx, tx, d, "deleted", actor, nil, now); err != nil {
		return err
	}
	if err := dbEvent(ctx, tx, domainevents.DatabaseDeleted, d, actor, func(p *domainevents.DatabasePayload) {
		p.DeletedAt = now.UTC().Format(time.RFC3339)
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeletingActor ritorna chi ha chiesto l'eliminazione (ultima riga di audit delete_requested).
func (s *Store) DeletingActor(ctx context.Context, id uuid.UUID) (*AppActor, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT actor FROM core.managed_db_audit WHERE db_id = $1 AND action = 'delete_requested' ORDER BY id DESC LIMIT 1`, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) || len(raw) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var a AppActor
	if json.Unmarshal(raw, &a) != nil {
		return nil, nil
	}
	return &a, nil
}

// SetManagedDBStatus scrive lo stato osservato dal controller. Non tocca un database in `deleting`
// (a meno che sia lo stato voluto): l'eliminazione non si annulla.
func (s *Store) SetManagedDBStatus(ctx context.Context, id uuid.UUID, status string, reason *string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE core.managed_dbs SET status = $2, status_reason = $3, updated_at = $4
		WHERE id = $1 AND (status <> 'deleting' OR $2 = 'deleting') AND (status <> $2 OR status_reason IS DISTINCT FROM $3)`, id, status, reason, now)
	return err
}

// SetManagedDBImage registra digest ed esito cosign (DB5).
func (s *Store) SetManagedDBImage(ctx context.Context, id uuid.UUID, digest string, verifiedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE core.managed_dbs SET image_digest = $2, image_verified_at = $3 WHERE id = $1`, id, digest, verifiedAt)
	return err
}

// SetManagedDBRestarts aggiorna il conteggio dei riavvii letto dallo stato del pod (DB21).
func (s *Store) SetManagedDBRestarts(ctx context.Context, id uuid.UUID, restarts int) error {
	_, err := s.pool.Exec(ctx, `UPDATE core.managed_dbs SET restarts = $2 WHERE id = $1 AND restarts <> $2`, id, restarts)
	return err
}

// SetManagedDBUsage scrive la dimensione misurata e lo stato del limite morbido (80% warning, 100% full).
// Ritorna true se lo stato del disco e' cambiato (chi chiama pubblica l'avviso, una volta per stato).
func (s *Store) SetManagedDBUsage(ctx context.Context, id uuid.UUID, usage int64, diskState string) (changed bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var old string
	if err := tx.QueryRow(ctx, `SELECT disk_state FROM core.managed_dbs WHERE id = $1 FOR UPDATE`, id).Scan(&old); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE core.managed_dbs SET usage_bytes = $2, disk_state = $3 WHERE id = $1`, id, usage, diskState); err != nil {
		return false, err
	}
	return old != diskState, tx.Commit(ctx)
}

// DiskStateFor applica le soglie dell'ADR 0003: 80% avviso, 100% pieno.
func DiskStateFor(usage, size int64) string {
	switch {
	case size > 0 && usage >= size:
		return DiskFull
	case size > 0 && float64(usage) >= .8*float64(size):
		return DiskWarning
	}
	return DiskNone
}

// EnqueueManagedDBQuotaWarning scrive l'avviso di disco nell'outbox (una volta per cambio di stato).
func (s *Store) EnqueueManagedDBQuotaWarning(ctx context.Context, d ManagedDB, usage int64, state string) error {
	return dbEvent(ctx, s.pool, domainevents.DatabaseQuotaWarning, d, nil, func(p *domainevents.DatabasePayload) {
		p.DiskState, p.UsageBytes = state, &usage
	})
}

// RecordManagedDBAudit scrive un'azione senza altro cambiamento di stato (rotazione, collegamenti...).
func (s *Store) RecordManagedDBAudit(ctx context.Context, d ManagedDB, action string, actor *AppActor, detail any, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := dbAudit(ctx, tx, d, action, actor, detail, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ManagedDBAuditRow e' una riga di audit (per i test e per gli admin).
type ManagedDBAuditRow struct {
	Action string
	Actor  *AppActor
	Detail json.RawMessage
	At     time.Time
}

// ManagedDBAudit elenca l'audit di un database, dal piu' vecchio.
func (s *Store) ManagedDBAudit(ctx context.Context, id uuid.UUID) ([]ManagedDBAuditRow, error) {
	rows, err := s.pool.Query(ctx, `SELECT action, actor, detail, at FROM core.managed_db_audit WHERE db_id = $1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ManagedDBAuditRow
	for rows.Next() {
		var r ManagedDBAuditRow
		var actor, detail []byte
		if err := rows.Scan(&r.Action, &actor, &detail, &r.At); err != nil {
			return nil, err
		}
		if len(actor) > 0 {
			var a AppActor
			if json.Unmarshal(actor, &a) == nil {
				r.Actor = &a
			}
		}
		r.Detail = detail
		out = append(out, r)
	}
	return out, rows.Err()
}

