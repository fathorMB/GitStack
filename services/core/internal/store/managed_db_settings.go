package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Impostazioni d'istanza dei database gestiti (v3-d, GIT-557, ADR 0003, DB23). Una sola riga di
// core.managed_db_settings; un campo nil e' «nessun valore scelto dalla console»: vale quello del chart
// (major, porte LAN) o il predefinito (limiti, conservazione dei backup).

// Predefiniti dei limiti per database (massimi che la console puo' alzare o abbassare). I valori di default di
// un database nuovo restano DefaultDB* (1 GiB, 100m, 256 MiB): questi sono i tetti che un owner non supera.
const (
	DefaultMaxDBSizeBytes   int64 = 100 << 30
	DefaultMaxDBCPUMillis         = 2000
	DefaultMaxDBMemoryBytes int64 = 4 << 30
)

// ManagedDBSettings sono le scelte della console; nil = non scelto.
type ManagedDBSettings struct {
	AllowedMajors  []int
	MaxSizeBytes   *int64
	MaxCPUMillis   *int
	MaxMemoryBytes *int64
	LANPortFrom    *int
	LANPortTo      *int
	KeepScheduled  *int
	KeepOnDemand   *int
}

const managedDBSettingsCols = `allowed_majors, max_size_bytes, max_cpu_millis, max_memory_bytes, lan_port_from, lan_port_to, keep_scheduled, keep_on_demand`

func scanManagedDBSettings(row pgx.Row) (ManagedDBSettings, error) {
	var st ManagedDBSettings
	var majors []int32
	err := row.Scan(&majors, &st.MaxSizeBytes, &st.MaxCPUMillis, &st.MaxMemoryBytes, &st.LANPortFrom, &st.LANPortTo, &st.KeepScheduled, &st.KeepOnDemand)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedDBSettings{}, nil
	}
	for _, m := range majors {
		st.AllowedMajors = append(st.AllowedMajors, int(m))
	}
	return st, err
}

// GetManagedDBSettings legge le scelte della console (tutte nil se non ce n'e').
func (s *Store) GetManagedDBSettings(ctx context.Context) (ManagedDBSettings, error) {
	return scanManagedDBSettings(s.pool.QueryRow(ctx, `SELECT `+managedDBSettingsCols+` FROM core.managed_db_settings WHERE singleton`))
}

// PutManagedDBSettings sostituisce le scelte della console e scrive nell'audit (DB20) chi ha cambiato cosa,
// nella stessa transazione. L'audit usa db_id nullo (uuid.Nil): non riguarda un database.
func (s *Store) PutManagedDBSettings(ctx context.Context, in ManagedDBSettings, actor *AppActor, now time.Time) (ManagedDBSettings, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ManagedDBSettings{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	prev, err := scanManagedDBSettings(tx.QueryRow(ctx, `SELECT `+managedDBSettingsCols+` FROM core.managed_db_settings WHERE singleton FOR UPDATE`))
	if err != nil {
		return ManagedDBSettings{}, err
	}
	majors := make([]int32, 0, len(in.AllowedMajors))
	for _, m := range in.AllowedMajors {
		majors = append(majors, int32(m))
	}
	var majorsArg any
	if len(majors) > 0 {
		majorsArg = majors
	}
	if _, err := tx.Exec(ctx, `INSERT INTO core.managed_db_settings (singleton, allowed_majors, max_size_bytes, max_cpu_millis, max_memory_bytes, lan_port_from, lan_port_to, keep_scheduled, keep_on_demand, updated_at)
		VALUES (true, $1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (singleton) DO UPDATE SET allowed_majors = EXCLUDED.allowed_majors, max_size_bytes = EXCLUDED.max_size_bytes, max_cpu_millis = EXCLUDED.max_cpu_millis,
			max_memory_bytes = EXCLUDED.max_memory_bytes, lan_port_from = EXCLUDED.lan_port_from, lan_port_to = EXCLUDED.lan_port_to,
			keep_scheduled = EXCLUDED.keep_scheduled, keep_on_demand = EXCLUDED.keep_on_demand, updated_at = EXCLUDED.updated_at`,
		majorsArg, in.MaxSizeBytes, in.MaxCPUMillis, in.MaxMemoryBytes, in.LANPortFrom, in.LANPortTo, in.KeepScheduled, in.KeepOnDemand, now); err != nil {
		return ManagedDBSettings{}, err
	}
	detail, err := json.Marshal(map[string]any{"prima": prev, "dopo": in})
	if err != nil {
		return ManagedDBSettings{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO core.managed_db_audit (db_id, owner_name, name, action, actor, detail, at) VALUES ($1,'','',$2,$3,$4,$5)`,
		uuid.Nil, "settings_update", actorJSON(actor), detail, now); err != nil {
		return ManagedDBSettings{}, err
	}
	return in, tx.Commit(ctx)
}
