package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// AppCapacity descrive le risorse del nodo che H11 separa tra servizi e app.
// I job CI hanno priorita' inferiore e non consumano la riserva delle app.
type AppCapacity struct {
	Server   AppResourceAmount
	Services AppResourceAmount
	Share    AppResourceAmount
	Used     AppResourceAmount
	Max      AppResourceAmount
	Default  bool
}

var DefaultAppCapacity = defaultAppCapacity()

var DefaultAppMax = AppResourceAmount{CPUMillis: 4000, MemoryBytes: 8 << 30}

// defaultAppCapacity usa l'hardware dell'host quando core non riceve
// Allocatable da Kubernetes. La riserva iniziale e' meta' del nodo.
func defaultAppCapacity() AppCapacity {
	server := AppResourceAmount{CPUMillis: runtime.NumCPU() * 1000, MemoryBytes: hostMemoryBytes()}
	return capacityForHardware(server)
}

// capacityForHardware separa il calcolo dal rilevatore dell'host: i test e
// un futuro provider Kubernetes possono fornire l'Allocatable del nodo.
func capacityForHardware(server AppResourceAmount) AppCapacity {
	if server.CPUMillis < 1000 {
		server.CPUMillis = 1000
	}
	if server.MemoryBytes < 1<<30 {
		server.MemoryBytes = 1 << 30
	}
	return AppCapacity{Server: server, Services: AppResourceAmount{CPUMillis: server.CPUMillis / 4, MemoryBytes: server.MemoryBytes / 4}, Share: AppResourceAmount{CPUMillis: server.CPUMillis / 2, MemoryBytes: server.MemoryBytes / 2}, Max: AppResourceAmount{CPUMillis: 4000, MemoryBytes: 8 << 30}, Default: true}
}

func hostMemoryBytes() int64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 16 << 30
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "MemTotal:" {
			if kb, err := strconv.ParseInt(f[1], 10, 64); err == nil {
				return kb << 10
			}
		}
	}
	return 16 << 30
}

// ReserveCapacityError indica che la riserva non lascia spazio ai servizi o
// e' gia' impegnata dalle parti garantite delle app.
type ReserveCapacityError struct {
	Missing map[string]int64
}

func (e *ReserveCapacityError) Error() string { return "riserva delle app insufficiente" }

func (s *Store) AppCapacity(ctx context.Context) (AppCapacity, error) {
	c, err := appCapacity(ctx, s.pool)
	if err != nil {
		return c, err
	}
	c.Used, err = guaranteedApps(ctx, s.pool)
	return c, err
}

func appCapacity(ctx context.Context, q queryer) (AppCapacity, error) {
	c := DefaultAppCapacity
	err := q.QueryRow(ctx, `SELECT COALESCE(apps_cpu_millis, $1), COALESCE(apps_memory_bytes, $2), app_max_cpu_millis, app_max_memory_bytes FROM core.app_capacity_settings WHERE singleton`, c.Share.CPUMillis, c.Share.MemoryBytes).
		Scan(&c.Share.CPUMillis, &c.Share.MemoryBytes, &c.Max.CPUMillis, &c.Max.MemoryBytes)
	if err == nil {
		// Una riga puo' esistere solo per maxPerApp: la riserva resta allora
		// calcolata dall'hardware e va esposta come predefinita.
		var overridden bool
		if err := q.QueryRow(ctx, `SELECT apps_cpu_millis IS NOT NULL FROM core.app_capacity_settings WHERE singleton`).Scan(&overridden); err != nil {
			return c, err
		}
		c.Default = !overridden
		return c, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return c, nil
	}
	return c, err
}

// SetAppMax salva il tetto per copia fissato dall'amministratore.
func (s *Store) SetAppMax(ctx context.Context, max AppResourceAmount) (AppCapacity, error) {
	if max.CPUMillis < 1 || max.MemoryBytes < 1 {
		return AppCapacity{}, fmt.Errorf("massimo per app non positivo")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO core.app_capacity_settings (singleton, app_max_cpu_millis, app_max_memory_bytes, updated_at)
		VALUES (true, $1, $2, now()) ON CONFLICT (singleton) DO UPDATE SET app_max_cpu_millis = EXCLUDED.app_max_cpu_millis, app_max_memory_bytes = EXCLUDED.app_max_memory_bytes, updated_at = now()`, max.CPUMillis, max.MemoryBytes)
	if err != nil {
		return AppCapacity{}, err
	}
	return s.AppCapacity(ctx)
}

func guaranteedApps(ctx context.Context, q queryer) (AppResourceAmount, error) {
	var out AppResourceAmount
	err := q.QueryRow(ctx, `SELECT COALESCE(sum(guaranteed_cpu_millis::bigint * copies), 0)::int,
		COALESCE(sum(guaranteed_memory_bytes * copies), 0)::bigint FROM core.apps`).Scan(&out.CPUMillis, &out.MemoryBytes)
	return out, err
}

// SetAppCapacity cambia l'override solo se non sottrae spazio ai servizi e
// non invalida il garantito gia' assegnato.
func (s *Store) SetAppCapacity(ctx context.Context, share AppResourceAmount) (AppCapacity, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AppCapacity{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('app-capacity', 0))`); err != nil {
		return AppCapacity{}, err
	}
	c, err := appCapacity(ctx, tx)
	if err != nil {
		return AppCapacity{}, err
	}
	used, err := guaranteedApps(ctx, tx)
	if err != nil {
		return AppCapacity{}, err
	}
	missing := map[string]int64{}
	if share.CPUMillis > c.Server.CPUMillis-c.Services.CPUMillis {
		missing["cpuMillis"] = int64(share.CPUMillis - (c.Server.CPUMillis - c.Services.CPUMillis))
	}
	if share.MemoryBytes > c.Server.MemoryBytes-c.Services.MemoryBytes {
		missing["memoryBytes"] = share.MemoryBytes - (c.Server.MemoryBytes - c.Services.MemoryBytes)
	}
	if share.CPUMillis < used.CPUMillis {
		missing["cpuMillis"] = int64(used.CPUMillis - share.CPUMillis)
	}
	if share.MemoryBytes < used.MemoryBytes {
		missing["memoryBytes"] = used.MemoryBytes - share.MemoryBytes
	}
	if len(missing) != 0 {
		return AppCapacity{}, &ReserveCapacityError{Missing: missing}
	}
	_, err = tx.Exec(ctx, `INSERT INTO core.app_capacity_settings (singleton, apps_cpu_millis, apps_memory_bytes, updated_at)
		VALUES (true, $1, $2, now()) ON CONFLICT (singleton) DO UPDATE SET apps_cpu_millis = EXCLUDED.apps_cpu_millis, apps_memory_bytes = EXCLUDED.apps_memory_bytes, updated_at = now()`, share.CPUMillis, share.MemoryBytes)
	if err != nil {
		return AppCapacity{}, err
	}
	c.Share, c.Used, c.Default = share, used, false
	if err := tx.Commit(ctx); err != nil {
		return AppCapacity{}, err
	}
	return c, nil
}

func appCapacityError(owner string, limits, used AppQuotaAmounts, need AppResourceAmount, capacity AppCapacity) error {
	missing := map[string]int64{}
	if int64(used.CPUMillis)+int64(need.CPUMillis) > int64(limits.CPUMillis) {
		missing["cpuMillis"] = int64(used.CPUMillis) + int64(need.CPUMillis) - int64(limits.CPUMillis)
	}
	if used.MemoryBytes+need.MemoryBytes > limits.MemoryBytes {
		missing["memoryBytes"] = used.MemoryBytes + need.MemoryBytes - limits.MemoryBytes
	}
	if capacity.Used.CPUMillis+need.CPUMillis > capacity.Share.CPUMillis {
		missing["reserveCpuMillis"] = int64(capacity.Used.CPUMillis + need.CPUMillis - capacity.Share.CPUMillis)
	}
	if capacity.Used.MemoryBytes+need.MemoryBytes > capacity.Share.MemoryBytes {
		missing["reserveMemoryBytes"] = capacity.Used.MemoryBytes + need.MemoryBytes - capacity.Share.MemoryBytes
	}
	if len(missing) == 0 {
		return nil
	}
	return &CapacityError{Owner: owner, Missing: missing, Limits: limits, Used: used}
}

// ListAppOwnerQuotas include owner con repo oppure una quota esplicita.
func (s *Store) ListAppOwnerQuotas(ctx context.Context) ([]AppOwnerQuota, error) {
	rows, err := s.pool.Query(ctx, `SELECT owner_name FROM core.app_owner_quotas UNION SELECT lower(owner_name) FROM core.repositories ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppOwnerQuota
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			return nil, err
		}
		limits, used, def, err := s.OwnerAppQuota(ctx, owner)
		if err != nil {
			return nil, err
		}
		out = append(out, AppOwnerQuota{Owner: owner, Limits: limits, Used: used, IsDefault: def})
	}
	return out, rows.Err()
}

type AppOwnerQuota struct {
	Owner        string
	Limits, Used AppQuotaAmounts
	IsDefault    bool
}

// AppOwnerExists evita quote orfane: core conosce un owner quando possiede
// almeno un repository; identity resta l'autorita' per utenti e gruppi.
func (s *Store) AppOwnerExists(ctx context.Context, owner string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.repositories WHERE lower(owner_name) = lower($1))`, owner).Scan(&exists)
	return exists, err
}

var ErrAppOwnerNotFound = errors.New("owner senza repo")

func (s *Store) DeleteOwnerAppQuota(ctx context.Context, owner string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM core.app_owner_quotas WHERE owner_name = lower($1)`, owner)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAppOwnerNotFound
	}
	return nil
}

func (s *Store) ValidateOwnerQuota(ctx context.Context, owner string, next AppQuotaAmounts) (AppQuotaAmounts, error) {
	_, used, _, err := s.OwnerAppQuota(ctx, owner)
	if err != nil {
		return AppQuotaAmounts{}, err
	}
	if next.CPUMillis < used.CPUMillis || next.MemoryBytes < used.MemoryBytes || next.DiskBytes < used.DiskBytes {
		return used, fmt.Errorf("quota sotto l'uso: %w", &CapacityError{Owner: owner, Used: used, Limits: next})
	}
	return used, nil
}
