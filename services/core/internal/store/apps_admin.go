package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AppListFilter restringe l'elenco delle app dell'istanza. Tutti i campi sono facoltativi: vuoto
// vuol dire «tutte le app dei repo non eliminati». GIT-548 riusa la stessa query per le app che
// un utente può vedere (RepoIDs).
type AppListFilter struct {
	// Owner: solo le app dei repo di questo owner (senza distinzione di maiuscole).
	Owner string
	// RepoIDs: se non nil, solo le app di questi repo (nil = nessun filtro, vuoto = nessuna app).
	RepoIDs []uuid.UUID
}

// ListInstanceApps elenca le app dei repo non eliminati ordinate per owner, repo e nome, con il
// totale prima della paginazione. limit <= 0 non pagina.
func (s *Store) ListInstanceApps(ctx context.Context, f AppListFilter, limit, offset int) ([]App, int, error) {
	where := ` WHERE r.deleted_at IS NULL AND ($1 = '' OR lower(r.owner_name) = lower($1)) AND ($2::uuid[] IS NULL OR a.repo_id = ANY($2::uuid[]))`
	var repoIDs any
	if f.RepoIDs != nil {
		repoIDs = f.RepoIDs
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*)`+appFrom+where, f.Owner, repoIDs).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT ` + appCols + appFrom + where + ` ORDER BY lower(r.owner_name), r.name, a.name`
	args := []any{f.Owner, repoIDs}
	if limit > 0 {
		q += ` LIMIT $3 OFFSET $4`
		args = append(args, limit, offset)
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	var out []App
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		out = append(out, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for i := range out {
		if err := s.fillApp(ctx, s.pool, &out[i]); err != nil {
			return nil, 0, err
		}
	}
	return out, total, nil
}

// AppUsageNow è l'uso corrente di un'app: somma, sulle copie, dell'ultimo campione recente.
type AppUsageNow struct {
	CPUMillis   int
	MemoryBytes int64
}

// AppsUsageNow dà l'uso corrente delle app indicate. Un'app senza campioni recenti con CPU (copie
// spente, sorgente metriche muta) non compare: l'uso è zero, non un dato inventato.
func (s *Store) AppsUsageNow(ctx context.Context, ids []uuid.UUID, since time.Time) (map[uuid.UUID]AppUsageNow, error) {
	out := map[uuid.UUID]AppUsageNow{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT app_id, COALESCE(sum(cpu_millicores), 0)::int, COALESCE(sum(memory_bytes), 0)::bigint FROM (
			SELECT DISTINCT ON (app_id, copy) app_id, copy, cpu_millicores, memory_bytes
			FROM core.app_metric_samples WHERE app_id = ANY($1) AND at >= $2
			ORDER BY app_id, copy, at DESC) last
		GROUP BY app_id`, ids, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var u AppUsageNow
		if err := rows.Scan(&id, &u.CPUMillis, &u.MemoryBytes); err != nil {
			return nil, err
		}
		out[id] = u
	}
	return out, rows.Err()
}

// ErrAppLimitTooHigh: parte garantita o tetto oltre il massimo per app dell'istanza.
var ErrAppLimitTooHigh = errors.New("tetto oltre il massimo per app")

// SetAppResources cambia parte garantita e tetto per copia (H11). Crea la versione successiva
// (trigger config, H8) con l'immagine della versione attiva. Rifiuta con CapacityError se le parti
// garantite non stanno nella quota dell'owner o nella riserva delle app (conta solo la differenza
// rispetto a quanto l'app ha già), con ErrAppLimitTooHigh oltre il massimo per app.
func (s *Store) SetAppResources(ctx context.Context, repoID uuid.UUID, name string, guaranteed, limit AppResourceAmount, actor *AppActor, now time.Time) (App, *AppVersion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return App{}, nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	cur, err := scanApp(tx.QueryRow(ctx, `SELECT `+appCols+appFrom+` WHERE a.repo_id = $1 AND a.name = $2 FOR UPDATE OF a`, repoID, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, nil, ErrNotFound
	}
	if err != nil {
		return App{}, nil, err
	}
	// Stesse serrature di CreateApp: quota dell'owner e riserva d'istanza.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('app-quota:' || lower($1), 0))`, cur.Owner); err != nil {
		return App{}, nil, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('app-capacity', 0))`); err != nil {
		return App{}, nil, err
	}
	capacity, err := appCapacity(ctx, tx)
	if err != nil {
		return App{}, nil, err
	}
	if guaranteed.CPUMillis > capacity.Max.CPUMillis || guaranteed.MemoryBytes > capacity.Max.MemoryBytes ||
		limit.CPUMillis > capacity.Max.CPUMillis || limit.MemoryBytes > capacity.Max.MemoryBytes {
		return App{}, nil, ErrAppLimitTooHigh
	}
	if capacity.Used, err = guaranteedApps(ctx, tx); err != nil {
		return App{}, nil, err
	}
	limits, used, _, err := ownerAppQuota(ctx, tx, cur.Owner)
	if err != nil {
		return App{}, nil, err
	}
	// Si toglie il contributo attuale dell'app (garantito x copie) e si somma il nuovo.
	copies := cur.Config.Copies
	oldCPU, oldMem := cur.Config.Guaranteed.CPUMillis*copies, cur.Config.Guaranteed.MemoryBytes*int64(copies)
	used.CPUMillis -= oldCPU
	used.MemoryBytes -= oldMem
	capacity.Used.CPUMillis -= oldCPU
	capacity.Used.MemoryBytes -= oldMem
	need := AppResourceAmount{CPUMillis: guaranteed.CPUMillis * copies, MemoryBytes: guaranteed.MemoryBytes * int64(copies)}
	if err := appCapacityError(cur.Owner, limits, used, need, capacity); err != nil {
		return App{}, nil, err
	}
	cfg := cur.Config
	cfg.Guaranteed, cfg.Limit = guaranteed, limit
	var ver *AppVersion
	if cfg.Guaranteed != cur.Config.Guaranteed || cfg.Limit != cur.Config.Limit {
		var number int
		if err := tx.QueryRow(ctx, `UPDATE core.apps SET next_version = next_version + 1 WHERE id = $1 RETURNING next_version - 1`, cur.ID).Scan(&number); err != nil {
			return App{}, nil, err
		}
		last, err := scanVersion(tx.QueryRow(ctx, `SELECT `+versionCols+` FROM core.app_versions v WHERE v.app_id = $1 AND v.number = $2`, cur.ID, cur.ActiveVersion))
		if err != nil {
			return App{}, nil, err
		}
		if cfg.Variables, cfg.SecretNames, err = envNames(ctx, tx, cur.ID); err != nil {
			return App{}, nil, err
		}
		if cfg.Volumes, err = volumeSpecs(ctx, tx, cur.ID); err != nil {
			return App{}, nil, err
		}
		summary := "risorse"
		if err := insertAppVersion(ctx, tx, cur.ID, number, "config", last.Image, cfg, &summary, nil, last.CommitSha, actor, now); err != nil {
			return App{}, nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO core.app_deploys (id, app_id, version, started_at) VALUES ($1, $2, $3, $4)`, uuid.New(), cur.ID, number, now); err != nil {
			return App{}, nil, err
		}
		v, err := scanVersion(tx.QueryRow(ctx, `SELECT `+versionCols+` FROM core.app_versions v WHERE v.app_id = $1 AND v.number = $2`, cur.ID, number))
		if err != nil {
			return App{}, nil, err
		}
		ver = &v
	}
	if _, err := tx.Exec(ctx, `UPDATE core.apps SET guaranteed_cpu_millis = $2, guaranteed_memory_bytes = $3, limit_cpu_millis = $4,
		limit_memory_bytes = $5, updated_at = $6 WHERE id = $1`, cur.ID, guaranteed.CPUMillis, guaranteed.MemoryBytes, limit.CPUMillis, limit.MemoryBytes, now); err != nil {
		return App{}, nil, err
	}
	out, err := scanApp(tx.QueryRow(ctx, `SELECT `+appCols+appFrom+` WHERE a.id = $1`, cur.ID))
	if err != nil {
		return App{}, nil, err
	}
	if err := s.fillApp(ctx, tx, &out); err != nil {
		return App{}, nil, err
	}
	return out, ver, tx.Commit(ctx)
}
