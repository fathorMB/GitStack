package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/domainevents"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Deploy, rollback, scala e stato osservato delle app (v3-a, GIT-343, H7/H12). Il controller
// (services/core/internal/apps) legge da qui i deploy in corso e scrive l'esito; gli handler HTTP
// creano versione e deploy nella stessa transazione.

// Errori del dominio dei deploy.
var (
	ErrAppDeployInProgress = errors.New("c'è già un deploy in corso per l'app")
	ErrAppVersionNotFound  = errors.New("versione dell'app non trovata")
)

// NewAppDeploy è l'input di un deploy a mano o di un rollback.
type NewAppDeploy struct {
	Trigger    string // manual | rollback | branch_push
	Image      AppImage
	RollbackOf *int
	Summary    *string
	Branch     *string
	CommitSha  *string
	Actor      *AppActor
	// ConfigFrom: per un rollback, la versione la cui configurazione si ripristina. Zero: quella corrente.
	ConfigFrom int
	// PauseAutoDeploy sospende i deploy automatici dal branch seguito (rollback, H7).
	PauseAutoDeploy bool
}

// BeginAppDeploy crea la versione successiva e il suo deploy «in corso». Solo un deploy alla volta
// per app (ErrAppDeployInProgress). Le copie sono sempre quelle correnti dell'app: la scala non
// crea versioni.
func (s *Store) BeginAppDeploy(ctx context.Context, repoID uuid.UUID, name string, in NewAppDeploy, now time.Time) (App, AppVersion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return App{}, AppVersion{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	cur, err := scanApp(tx.QueryRow(ctx, `SELECT `+appCols+appFrom+` WHERE a.repo_id = $1 AND a.name = $2 FOR UPDATE OF a`, repoID, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, AppVersion{}, ErrNotFound
	}
	if err != nil {
		return App{}, AppVersion{}, err
	}
	var busy bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.app_deploys WHERE app_id = $1 AND state = 'in_progress')`, cur.ID).Scan(&busy); err != nil {
		return App{}, AppVersion{}, err
	}
	if busy {
		return App{}, AppVersion{}, ErrAppDeployInProgress
	}
	src := cur.ActiveVersion
	if in.ConfigFrom > 0 {
		src = in.ConfigFrom
	} else {
		// L'ultima versione scritta porta variabili e nomi dei segreti correnti.
		if err := tx.QueryRow(ctx, `SELECT max(number) FROM core.app_versions WHERE app_id = $1`, cur.ID).Scan(&src); err != nil {
			return App{}, AppVersion{}, err
		}
	}
	base, err := scanVersion(tx.QueryRow(ctx, `SELECT `+versionCols+` FROM core.app_versions v WHERE v.app_id = $1 AND v.number = $2`, cur.ID, src))
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, AppVersion{}, ErrAppVersionNotFound
	}
	if err != nil {
		return App{}, AppVersion{}, err
	}
	cfg := base.Config
	cfg.Copies = cur.Config.Copies
	if in.ConfigFrom == 0 {
		cfg.Port, cfg.HealthCheck, cfg.Restricted = cur.Config.Port, cur.Config.HealthCheck, cur.Config.Restricted
	}
	// Il rollback ripristina i nomi della versione, ma non un vecchio ciphertext: i valori sono
	// sempre quelli correnti. Se un nome e' stato eliminato, rifiuta prima di scrivere il deploy.
	if err := missingAppSecrets(ctx, tx, cur.ID, cfg.SecretNames); err != nil {
		return App{}, AppVersion{}, err
	}
	var number int
	if err := tx.QueryRow(ctx, `UPDATE core.apps SET next_version = next_version + 1, updated_at = $2 WHERE id = $1 RETURNING next_version - 1`, cur.ID, now).Scan(&number); err != nil {
		return App{}, AppVersion{}, err
	}
	if err := insertAppVersion(ctx, tx, cur.ID, number, in.Trigger, in.Image, cfg, in.Summary, in.Branch, in.CommitSha, in.Actor, now); err != nil {
		return App{}, AppVersion{}, err
	}
	if in.RollbackOf != nil {
		if _, err := tx.Exec(ctx, `UPDATE core.app_versions SET rollback_of = $3 WHERE app_id = $1 AND number = $2`, cur.ID, number, *in.RollbackOf); err != nil {
			return App{}, AppVersion{}, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO core.app_deploys (id, app_id, version, started_at) VALUES ($1, $2, $3, $4)`, uuid.New(), cur.ID, number, now); err != nil {
		return App{}, AppVersion{}, err
	}
	if in.ConfigFrom > 0 {
		if _, err := tx.Exec(ctx, `UPDATE core.apps SET port = $2, health_check = $3, restricted = $4 WHERE id = $1`,
			cur.ID, cfg.Port, healthJSON(cfg.HealthCheck), cfg.Restricted); err != nil {
			return App{}, AppVersion{}, err
		}
	}
	if in.PauseAutoDeploy {
		if _, err := tx.Exec(ctx, `UPDATE core.apps SET auto_deploy_paused = true WHERE id = $1`, cur.ID); err != nil {
			return App{}, AppVersion{}, err
		}
	}
	app, err := scanApp(tx.QueryRow(ctx, `SELECT `+appCols+appFrom+` WHERE a.id = $1`, cur.ID))
	if err != nil {
		return App{}, AppVersion{}, err
	}
	if err := s.fillApp(ctx, tx, &app); err != nil {
		return App{}, AppVersion{}, err
	}
	ver, err := scanVersion(tx.QueryRow(ctx, `SELECT `+versionCols+` FROM core.app_versions v WHERE v.app_id = $1 AND v.number = $2`, cur.ID, number))
	if err != nil {
		return App{}, AppVersion{}, err
	}
	if err := enqueueAppDeployment(ctx, tx, app, ver, domainevents.AppDeploymentStarted, ""); err != nil {
		return App{}, AppVersion{}, err
	}
	return app, ver, tx.Commit(ctx)
}

// GetAppByID legge un'app per id.
func (s *Store) GetAppByID(ctx context.Context, id uuid.UUID) (App, error) {
	a, err := scanApp(s.pool.QueryRow(ctx, `SELECT `+appCols+appFrom+` WHERE a.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, ErrNotFound
	}
	if err != nil {
		return App{}, err
	}
	return a, s.fillApp(ctx, s.pool, &a)
}

// PendingAppDeploy è un deploy in corso: l'app e la versione da portare in esecuzione.
type PendingAppDeploy struct {
	App       App
	Version   AppVersion
	StartedAt time.Time
}

// PendingAppDeploys elenca i deploy in corso, dal più vecchio.
func (s *Store) PendingAppDeploys(ctx context.Context) ([]PendingAppDeploy, error) {
	rows, err := s.pool.Query(ctx, `SELECT app_id, version, started_at FROM core.app_deploys WHERE state = 'in_progress' ORDER BY started_at, version`)
	if err != nil {
		return nil, err
	}
	type key struct {
		app     uuid.UUID
		version int
		at      time.Time
	}
	var keys []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.app, &k.version, &k.at); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []PendingAppDeploy
	for _, k := range keys {
		a, err := s.GetAppByID(ctx, k.app)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		v, err := s.GetAppVersion(ctx, k.app, k.version)
		if err != nil {
			return nil, err
		}
		out = append(out, PendingAppDeploy{App: a, Version: v, StartedAt: k.at})
	}
	return out, nil
}

// FinishAppDeploy scrive l'esito del deploy della versione: serving (diventa l'attiva, la
// precedente passa a superseded) o failed (resta l'attiva di prima, con il motivo). Idempotente
// sul deploy già concluso.
func (s *Store) FinishAppDeploy(ctx context.Context, appID uuid.UUID, version int, state, reason string, now time.Time) error {
	if state != "serving" && state != "failed" {
		return errors.New("stato di deploy non valido: " + state)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var active int
	var prevState string
	if err := tx.QueryRow(ctx, `SELECT a.active_version, v.state FROM core.apps a
		JOIN core.app_versions v ON v.app_id = a.id AND v.number = a.active_version WHERE a.id = $1 FOR UPDATE OF a`, appID).Scan(&active, &prevState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var r any
	if strings.TrimSpace(reason) != "" {
		r = reason
	}
	tag, err := tx.Exec(ctx, `UPDATE core.app_deploys SET state = $3, reason = $4, finished_at = $5
		WHERE app_id = $1 AND version = $2 AND state = 'in_progress'`, appID, version, state, r, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE core.app_versions SET state = $3, state_reason = $4 WHERE app_id = $1 AND number = $2`, appID, version, state, r); err != nil {
		return err
	}
	switch {
	case state == "serving":
		if active != version {
			if _, err := tx.Exec(ctx, `UPDATE core.app_versions SET state = 'superseded' WHERE app_id = $1 AND number = $2 AND state = 'serving'`, appID, active); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE core.apps SET active_version = $2, status = 'running', updated_at = $3 WHERE id = $1`, appID, version, now)
	case prevState != "serving" || active == version:
		// Nessuna versione precedente in esecuzione (primo deploy): l'app è «fallita».
		_, err = tx.Exec(ctx, `UPDATE core.apps SET status = 'failed', ready_copies = 0, updated_at = $2 WHERE id = $1`, appID, now)
	default:
		_, err = tx.Exec(ctx, `UPDATE core.apps SET status = 'running', updated_at = $2 WHERE id = $1`, appID, now)
	}
	if err != nil {
		return err
	}
	if err := s.enqueueDeployOutcome(ctx, tx, appID, version, state, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// enqueueDeployOutcome scrive l'esito del deploy (GIT-384): fallito, riuscito o rollback riuscito.
// Lo stato è quello dopo la modifica, nella stessa transazione.
func (s *Store) enqueueDeployOutcome(ctx context.Context, tx pgx.Tx, appID uuid.UUID, version int, state, reason string) error {
	a, err := s.loadAppForEvent(ctx, tx, appID)
	if err != nil {
		return err
	}
	v, err := scanVersion(tx.QueryRow(ctx, `SELECT `+versionCols+` FROM core.app_versions v WHERE v.app_id = $1 AND v.number = $2`, appID, version))
	if err != nil {
		return err
	}
	action := domainevents.AppDeploymentSucceeded
	switch {
	case state == "failed":
		action = domainevents.AppDeploymentFailed
	case v.Trigger == "rollback":
		action = domainevents.AppDeploymentRolledBack
	}
	return enqueueAppDeployment(ctx, tx, a, v, action, reason)
}

// loadAppForEvent legge l'app con l'immagine e il deploy correnti, dentro la transazione.
func (s *Store) loadAppForEvent(ctx context.Context, tx pgx.Tx, appID uuid.UUID) (App, error) {
	a, err := scanApp(tx.QueryRow(ctx, `SELECT `+appCols+appFrom+` WHERE a.id = $1`, appID))
	if err != nil {
		return App{}, err
	}
	return a, s.fillApp(ctx, tx, &a)
}

// SetAppObserved aggiorna stato osservato e copie pronte (controller). Non tocca updated_at. Il
// passaggio da «running» a «unhealthy» (copie non più pronte) scrive app_status.unhealthy (GIT-384):
// l'avviso va a chi ha fatto il deploy della versione attiva.
func (s *Store) SetAppObserved(ctx context.Context, appID uuid.UUID, status string, ready int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var prev string
	if err := tx.QueryRow(ctx, `SELECT status FROM core.apps WHERE id = $1 FOR UPDATE`, appID).Scan(&prev); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE core.apps SET status = $2, ready_copies = $3 WHERE id = $1`, appID, status, ready); err != nil {
		return err
	}
	if prev == "running" && status == "unhealthy" {
		a, err := s.loadAppForEvent(ctx, tx, appID)
		if err != nil {
			return err
		}
		v, err := scanVersion(tx.QueryRow(ctx, `SELECT `+versionCols+` FROM core.app_versions v WHERE v.app_id = $1 AND v.number = $2`, appID, a.ActiveVersion))
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var actor *AppActor
		if err == nil {
			actor = v.CreatedBy
		}
		if err := enqueueAppUnhealthy(ctx, tx, a, actor); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SetAppCopies imposta le copie a mano (H12), entro la quota dell'owner (H11): CapacityError con
// quanto manca se le parti garantite non ci stanno. Non crea versioni.
func (s *Store) SetAppCopies(ctx context.Context, repoID uuid.UUID, name string, copies int, now time.Time) (App, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return App{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	cur, err := scanApp(tx.QueryRow(ctx, `SELECT `+appCols+appFrom+` WHERE a.repo_id = $1 AND a.name = $2 FOR UPDATE OF a`, repoID, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, ErrNotFound
	}
	if err != nil {
		return App{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('app-quota:' || lower($1), 0))`, cur.Owner); err != nil {
		return App{}, err
	}
	if copies > cur.Config.Copies {
		limits, used, _, err := ownerAppQuota(ctx, tx, cur.Owner)
		if err != nil {
			return App{}, err
		}
		extra := int64(copies - cur.Config.Copies)
		needCPU := int64(cur.Config.Guaranteed.CPUMillis) * extra
		needMem := cur.Config.Guaranteed.MemoryBytes * extra
		missing := map[string]int64{}
		if int64(used.CPUMillis)+needCPU > int64(limits.CPUMillis) {
			missing["cpuMillis"] = int64(used.CPUMillis) + needCPU - int64(limits.CPUMillis)
		}
		if used.MemoryBytes+needMem > limits.MemoryBytes {
			missing["memoryBytes"] = used.MemoryBytes + needMem - limits.MemoryBytes
		}
		if len(missing) > 0 {
			return App{}, &CapacityError{Owner: cur.Owner, Missing: missing, Limits: limits, Used: used}
		}
	}
	status := cur.Status
	if copies == 0 {
		status = "stopped"
	} else if cur.Config.Copies == 0 {
		status = "starting"
	}
	if _, err := tx.Exec(ctx, `UPDATE core.apps SET copies = $2, status = $3, updated_at = $4 WHERE id = $1`, cur.ID, copies, status, now); err != nil {
		return App{}, err
	}
	out, err := scanApp(tx.QueryRow(ctx, `SELECT `+appCols+appFrom+` WHERE a.id = $1`, cur.ID))
	if err != nil {
		return App{}, err
	}
	if err := s.fillApp(ctx, tx, &out); err != nil {
		return App{}, err
	}
	return out, tx.Commit(ctx)
}

// AppImageRemoved dice se la pulizia del registry (RG2) ha tolto il tag o il digest
// dell'immagine di un'app del repo: allora non si può più riattivare (H12).
func (s *Store) AppImageRemoved(ctx context.Context, repoID uuid.UUID, reference string) (bool, error) {
	ref := strings.ToLower(reference)
	digest := ""
	if i := strings.Index(ref, "@"); i >= 0 {
		digest = ref[i+1:]
		ref = ref[:i]
	}
	tag := ""
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		tag = ref[i+1:]
		ref = ref[:i]
	}
	var removed bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM core.repo_package_removed_tags t WHERE t.repo_id = $1 AND (
			($2 <> '' AND lower(t.digest) = $2) OR
			($3 <> '' AND lower(t.name) = $3 AND ($4 LIKE '%' || lower(t.image) OR t.image = ''))))`,
		repoID, digest, tag, ref).Scan(&removed)
	return removed, err
}

// AppIDs elenca gli id di tutte le app (sync e pulizia dei namespace del controller).
func (s *Store) AppIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM core.apps`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
