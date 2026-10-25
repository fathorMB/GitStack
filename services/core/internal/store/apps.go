package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Modello dati delle app (v3-a, GIT-342, H5/H8). L'app appartiene a un repo; ogni modifica di
// configurazione è una versione numerata (immagine + configurazione, i segreti solo per nome).
// Archiviazione (R10) e cestino (R2) non toccano le righe: lo stato «ferma» si ricava dal repo
// (App.Effective), così al ripristino l'app è di nuovo avviabile; il purge del repo (dopo i 7
// giorni) cancella app, versioni e deploy a cascata.

// Errori del dominio delle app.
var (
	ErrAppNameTaken       = errors.New("il repo ha già un'app con questo nome")
	ErrAppAddressConflict = errors.New("l'indirizzo automatico è già usato da un'altra app")
)

// CapacityError dice cosa manca nella quota dell'owner (H11).
type CapacityError struct {
	Owner   string
	Missing map[string]int64 // cpuMillis, memoryBytes: quanto manca
	Limits  AppQuotaAmounts
	Used    AppQuotaAmounts
}

func (e *CapacityError) Error() string {
	return fmt.Sprintf("quota delle app di %s insufficiente", e.Owner)
}

// AppQuotaAmounts sono CPU e RAM garantite e disco dei volumi (v3-b).
type AppQuotaAmounts struct {
	CPUMillis   int
	MemoryBytes int64
	DiskBytes   int64
}

// Quota di default dell'istanza per un owner senza riga in app_owner_quotas.
var DefaultAppQuota = AppQuotaAmounts{CPUMillis: 4000, MemoryBytes: 8 << 30, DiskBytes: 20 << 30}

// AppResourceAmount è CPU e memoria per copia.
type AppResourceAmount struct {
	CPUMillis   int   `json:"cpuMillis"`
	MemoryBytes int64 `json:"memoryBytes"`
}

// AppHealthCheck è il controllo di salute HTTP.
type AppHealthCheck struct {
	Path            string `json:"path"`
	IntervalSeconds int    `json:"intervalSeconds"`
	TimeoutSeconds  *int   `json:"timeoutSeconds,omitempty"`
}

// AppConfig è la configurazione congelata in una versione (H8).
type AppConfig struct {
	Port        *int              `json:"port,omitempty"`
	HealthCheck *AppHealthCheck   `json:"healthCheck,omitempty"`
	Variables   map[string]string `json:"variables"`
	SecretNames []string          `json:"secretNames"`
	Guaranteed  AppResourceAmount `json:"guaranteed"`
	Limit       AppResourceAmount `json:"limit"`
	Copies      int               `json:"copies"`
	Restricted  bool              `json:"restricted"`
}

// AppActor è chi ha fatto un'azione (H13).
type AppActor struct {
	ID       uuid.UUID `json:"id"`
	Username string    `json:"username"`
	Type     string    `json:"type"`
}

// AppImage è l'immagine di una versione.
type AppImage struct {
	Reference  string
	Digest     *string
	Source     string // registry | external
	Verified   bool
	SignedBy   *string
	RunsAsRoot *bool
}

// AppVersion è una versione dell'app.
type AppVersion struct {
	AppID       uuid.UUID
	Number      int
	Trigger     string
	State       string
	StateReason *string
	Image       AppImage
	Branch      *string
	CommitSha   *string
	Summary     *string
	RollbackOf  *int
	Config      AppConfig
	CreatedBy   *AppActor
	CreatedAt   time.Time
}

// AppDeploy è un tentativo di deploy.
type AppDeploy struct {
	Version    int
	State      string
	Reason     *string
	StartedAt  time.Time
	FinishedAt *time.Time
}

// App è la riga dell'app con i dati del repo che servono a derivarne lo stato.
type App struct {
	ID                 uuid.UUID
	RepoID             uuid.UUID
	Name               string
	AddressLabel       *string
	Status             string
	Access             string
	Config             AppConfig
	ReadyCopies        int
	FollowBranch       *string
	AutoDeployPaused   bool
	ActiveVersion      int
	Image              AppImage // dell'ultima versione attiva
	LastDeploy         *AppDeploy
	CreatedBy          *AppActor
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Owner              string
	RepoName           string
	RepoArchivedAt     *time.Time
	RepoDeletedAt      *time.Time
	RunningPreviousVer bool
}

// Effective dà lo stato che l'API mostra e il motivo dello stop: repo eliminato (R2) o archiviato
// (R10) fermano l'app subito, copie a zero la fermano «a mano». Tornato il repo attivo, l'app
// riprende lo stato osservato (avviabile di nuovo).
func (a App) Effective() (status string, reason *string) {
	r := func(s string) *string { return &s }
	switch {
	case a.RepoDeletedAt != nil:
		return "stopped", r("repo_deleted")
	case a.RepoArchivedAt != nil:
		return "stopped", r("repo_archived")
	case a.Config.Copies == 0:
		return "stopped", r("manual")
	}
	return a.Status, nil
}

// NewApp è l'input di creazione. Image è già risolta dall'handler.
type NewApp struct {
	RepoID       uuid.UUID
	Owner        string
	Name         string
	AddressLabel *string
	Image        AppImage
	Config       AppConfig
	FollowBranch *string
	Actor        *AppActor
}

const appCols = `a.id, a.repo_id, a.name, a.address_label, a.status, a.access_mode, a.copies, a.ready_copies, a.port,
	a.health_check, a.follow_branch, a.auto_deploy_paused, a.restricted, a.guaranteed_cpu_millis,
	a.guaranteed_memory_bytes, a.limit_cpu_millis, a.limit_memory_bytes, a.active_version, a.created_by,
	a.created_at, a.updated_at, r.owner_name, r.name, r.archived_at, r.deleted_at`

func scanApp(row pgx.Row) (App, error) {
	var a App
	var hc, by []byte
	err := row.Scan(&a.ID, &a.RepoID, &a.Name, &a.AddressLabel, &a.Status, &a.Access, &a.Config.Copies, &a.ReadyCopies,
		&a.Config.Port, &hc, &a.FollowBranch, &a.AutoDeployPaused, &a.Config.Restricted, &a.Config.Guaranteed.CPUMillis,
		&a.Config.Guaranteed.MemoryBytes, &a.Config.Limit.CPUMillis, &a.Config.Limit.MemoryBytes, &a.ActiveVersion, &by,
		&a.CreatedAt, &a.UpdatedAt, &a.Owner, &a.RepoName, &a.RepoArchivedAt, &a.RepoDeletedAt)
	if err != nil {
		return App{}, err
	}
	if len(hc) > 0 {
		a.Config.HealthCheck = &AppHealthCheck{}
		if err := json.Unmarshal(hc, a.Config.HealthCheck); err != nil {
			return App{}, err
		}
	}
	if len(by) > 0 {
		a.CreatedBy = &AppActor{}
		if err := json.Unmarshal(by, a.CreatedBy); err != nil {
			return App{}, err
		}
	}
	return a, nil
}

const appFrom = ` FROM core.apps a JOIN core.repositories r ON r.resource_id = a.repo_id`

const versionCols = `v.app_id, v.number, v.trigger, v.state, v.state_reason, v.image_reference, v.image_digest, v.image_source,
	v.image_verified, v.signed_by, v.runs_as_root, v.branch, v.commit_sha, v.summary, v.rollback_of, v.config, v.created_by, v.created_at`

func scanVersion(row pgx.Row) (AppVersion, error) {
	var v AppVersion
	var cfg, by []byte
	err := row.Scan(&v.AppID, &v.Number, &v.Trigger, &v.State, &v.StateReason, &v.Image.Reference, &v.Image.Digest, &v.Image.Source,
		&v.Image.Verified, &v.Image.SignedBy, &v.Image.RunsAsRoot, &v.Branch, &v.CommitSha, &v.Summary, &v.RollbackOf, &cfg, &by, &v.CreatedAt)
	if err != nil {
		return AppVersion{}, err
	}
	if err := json.Unmarshal(cfg, &v.Config); err != nil {
		return AppVersion{}, err
	}
	if v.Config.Variables == nil {
		v.Config.Variables = map[string]string{}
	}
	if v.Config.SecretNames == nil {
		v.Config.SecretNames = []string{}
	}
	if len(by) > 0 {
		v.CreatedBy = &AppActor{}
		if err := json.Unmarshal(by, v.CreatedBy); err != nil {
			return AppVersion{}, err
		}
	}
	return v, nil
}

// fill completa immagine, ultimo deploy e «gira ancora la precedente» di un'app.
func (s *Store) fillApp(ctx context.Context, q queryer, a *App) error {
	v, err := scanVersion(q.QueryRow(ctx, `SELECT `+versionCols+` FROM core.app_versions v WHERE v.app_id = $1 AND v.number = $2`, a.ID, a.ActiveVersion))
	if err != nil {
		return err
	}
	a.Image = v.Image
	var d AppDeploy
	err = q.QueryRow(ctx, `SELECT version, state, reason, started_at, finished_at FROM core.app_deploys
		WHERE app_id = $1 ORDER BY started_at DESC, version DESC LIMIT 1`, a.ID).
		Scan(&d.Version, &d.State, &d.Reason, &d.StartedAt, &d.FinishedAt)
	if err == nil {
		a.LastDeploy = &d
		a.RunningPreviousVer = (d.State == "failed" || d.State == "rolled_back") && d.Version != a.ActiveVersion
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return nil
}

func actorJSON(a *AppActor) any {
	if a == nil {
		return nil
	}
	b, _ := json.Marshal(a)
	return b
}

func configJSON(c AppConfig) []byte {
	if c.Variables == nil {
		c.Variables = map[string]string{}
	}
	if c.SecretNames == nil {
		c.SecretNames = []string{}
	}
	b, _ := json.Marshal(c)
	return b
}

func healthJSON(h *AppHealthCheck) any {
	if h == nil {
		return nil
	}
	b, _ := json.Marshal(h)
	return b
}

// OwnerAppQuota calcola limiti e uso dell'owner: il garantito per copia per le copie di tutte le
// app dei suoi repo (anche archiviati: tornano avviabili). Gli eliminati non contano oltre i 7
// giorni perché il purge li cancella; nel cestino contano, perché il ripristino li riavvia.
func (s *Store) OwnerAppQuota(ctx context.Context, owner string) (limits, used AppQuotaAmounts, isDefault bool, err error) {
	return ownerAppQuota(ctx, s.pool, owner)
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func ownerAppQuota(ctx context.Context, q queryer, owner string) (limits, used AppQuotaAmounts, isDefault bool, err error) {
	limits, isDefault = DefaultAppQuota, true
	e := q.QueryRow(ctx, `SELECT cpu_millis, memory_bytes, disk_bytes FROM core.app_owner_quotas WHERE owner_name = lower($1)`, owner).
		Scan(&limits.CPUMillis, &limits.MemoryBytes, &limits.DiskBytes)
	if e == nil {
		isDefault = false
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return limits, used, false, e
	}
	err = q.QueryRow(ctx, `SELECT COALESCE(sum(a.guaranteed_cpu_millis::bigint * a.copies), 0)::int,
			COALESCE(sum(a.guaranteed_memory_bytes * a.copies), 0)::bigint
		FROM core.apps a JOIN core.repositories r ON r.resource_id = a.repo_id WHERE lower(r.owner_name) = lower($1)`, owner).
		Scan(&used.CPUMillis, &used.MemoryBytes)
	return limits, used, isDefault, err
}

// SetOwnerAppQuota imposta la quota di un owner (usata da chi amministra l'istanza).
func (s *Store) SetOwnerAppQuota(ctx context.Context, owner string, q AppQuotaAmounts) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO core.app_owner_quotas (owner_name, cpu_millis, memory_bytes, disk_bytes, updated_at)
		VALUES (lower($1), $2, $3, $4, now())
		ON CONFLICT (owner_name) DO UPDATE SET cpu_millis = EXCLUDED.cpu_millis, memory_bytes = EXCLUDED.memory_bytes,
			disk_bytes = EXCLUDED.disk_bytes, updated_at = now()`, owner, q.CPUMillis, q.MemoryBytes, q.DiskBytes)
	return err
}

// CreateApp crea l'app, la versione 1 (trigger create) e il primo deploy, contando la quota
// dell'owner in una transazione serializzata per owner (advisory lock): due creazioni
// concorrenti non superano insieme la quota. 409 per nome o indirizzo già usati.
func (s *Store) CreateApp(ctx context.Context, in NewApp, now time.Time) (App, AppVersion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return App{}, AppVersion{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('app-quota:' || lower($1), 0))`, in.Owner); err != nil {
		return App{}, AppVersion{}, err
	}
	limits, used, _, err := ownerAppQuota(ctx, tx, in.Owner)
	if err != nil {
		return App{}, AppVersion{}, err
	}
	needCPU := int64(in.Config.Guaranteed.CPUMillis) * int64(in.Config.Copies)
	needMem := in.Config.Guaranteed.MemoryBytes * int64(in.Config.Copies)
	missing := map[string]int64{}
	if int64(used.CPUMillis)+needCPU > int64(limits.CPUMillis) {
		missing["cpuMillis"] = int64(used.CPUMillis) + needCPU - int64(limits.CPUMillis)
	}
	if used.MemoryBytes+needMem > limits.MemoryBytes {
		missing["memoryBytes"] = used.MemoryBytes + needMem - limits.MemoryBytes
	}
	if len(missing) > 0 {
		return App{}, AppVersion{}, &CapacityError{Owner: in.Owner, Missing: missing, Limits: limits, Used: used}
	}
	id := uuid.New()
	c := in.Config
	_, err = tx.Exec(ctx, `INSERT INTO core.apps (id, repo_id, name, address_label, copies, port, health_check, follow_branch,
			restricted, guaranteed_cpu_millis, guaranteed_memory_bytes, limit_cpu_millis, limit_memory_bytes,
			active_version, next_version, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,1,2,$14,$15,$15)`,
		id, in.RepoID, in.Name, in.AddressLabel, c.Copies, c.Port, healthJSON(c.HealthCheck), in.FollowBranch, c.Restricted,
		c.Guaranteed.CPUMillis, c.Guaranteed.MemoryBytes, c.Limit.CPUMillis, c.Limit.MemoryBytes, actorJSON(in.Actor), now)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			if pgErr.ConstraintName == "apps_address_label_key" {
				return App{}, AppVersion{}, ErrAppAddressConflict
			}
			return App{}, AppVersion{}, ErrAppNameTaken
		}
		return App{}, AppVersion{}, err
	}
	if err := insertAppVersion(ctx, tx, id, 1, "create", in.Image, in.Config, nil, nil, nil, in.Actor, now); err != nil {
		return App{}, AppVersion{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO core.app_deploys (id, app_id, version, started_at) VALUES ($1, $2, 1, $3)`, uuid.New(), id, now); err != nil {
		return App{}, AppVersion{}, err
	}
	app, err := scanApp(tx.QueryRow(ctx, `SELECT `+appCols+appFrom+` WHERE a.id = $1`, id))
	if err != nil {
		return App{}, AppVersion{}, err
	}
	if err := s.fillApp(ctx, tx, &app); err != nil {
		return App{}, AppVersion{}, err
	}
	ver, err := scanVersion(tx.QueryRow(ctx, `SELECT `+versionCols+` FROM core.app_versions v WHERE v.app_id = $1 AND v.number = 1`, id))
	if err != nil {
		return App{}, AppVersion{}, err
	}
	return app, ver, tx.Commit(ctx)
}

func insertAppVersion(ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, appID uuid.UUID, number int, trigger string, img AppImage, cfg AppConfig, summary *string, branch, sha *string, actor *AppActor, now time.Time) error {
	_, err := q.Exec(ctx, `INSERT INTO core.app_versions (app_id, number, trigger, image_reference, image_digest, image_source,
			image_verified, signed_by, runs_as_root, branch, commit_sha, summary, config, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		appID, number, trigger, img.Reference, img.Digest, img.Source, img.Verified, img.SignedBy, img.RunsAsRoot,
		branch, sha, summary, configJSON(cfg), actorJSON(actor), now)
	return err
}

// GetApp legge un'app del repo per nome (anche se il repo è archiviato o eliminato).
func (s *Store) GetApp(ctx context.Context, repoID uuid.UUID, name string) (App, error) {
	a, err := scanApp(s.pool.QueryRow(ctx, `SELECT `+appCols+appFrom+` WHERE a.repo_id = $1 AND a.name = $2`, repoID, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return App{}, ErrNotFound
	}
	if err != nil {
		return App{}, err
	}
	return a, s.fillApp(ctx, s.pool, &a)
}

// ListApps elenca le app del repo per nome.
func (s *Store) ListApps(ctx context.Context, repoID uuid.UUID) ([]App, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+appCols+appFrom+` WHERE a.repo_id = $1 ORDER BY a.name`, repoID)
	if err != nil {
		return nil, err
	}
	var out []App
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.fillApp(ctx, s.pool, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// DeleteApp elimina l'app e, a cascata, versioni e deploy. ErrNotFound se non c'è.
func (s *Store) DeleteApp(ctx context.Context, repoID uuid.UUID, name string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM core.apps WHERE repo_id = $1 AND name = $2`, repoID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AppPatch è la modifica di un'app. I campi con Set* vero sono quelli da cambiare.
type AppPatch struct {
	SetPort          bool
	Port             *int
	SetHealth        bool
	HealthCheck      *AppHealthCheck
	SetRestricted    bool
	Restricted       bool
	SetFollow        bool
	FollowBranch     *string
	SetPaused        bool
	AutoDeployPaused bool
	// AddressLabel è l'etichetta da riservare se l'app non ne ha ancora una (porta data dopo la
	// creazione, H2). Ignorata se l'app ce l'ha già: togliere la porta non la libera.
	AddressLabel *string
}

// UpdateApp applica la modifica. Se cambia qualcosa che entra in una versione (porta, controllo
// di salute, restricted) crea la versione successiva (trigger config) e un deploy; altrimenti no
// (H8). Ritorna la versione creata, nil se nessuna.
func (s *Store) UpdateApp(ctx context.Context, repoID uuid.UUID, name string, p AppPatch, actor *AppActor, now time.Time) (App, *AppVersion, error) {
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
	cfg := cur.Config
	var changes []string
	if p.SetPort && !intPtrEq(cfg.Port, p.Port) {
		cfg.Port = p.Port
		changes = append(changes, "porta")
	}
	if p.SetHealth && !healthEq(cfg.HealthCheck, p.HealthCheck) {
		cfg.HealthCheck = p.HealthCheck
		changes = append(changes, "controllo di salute")
	}
	if p.SetRestricted && cfg.Restricted != p.Restricted {
		cfg.Restricted = p.Restricted
		changes = append(changes, "restricted")
	}
	follow, paused := cur.FollowBranch, cur.AutoDeployPaused
	if p.SetFollow {
		follow = p.FollowBranch
	}
	if p.SetPaused {
		paused = p.AutoDeployPaused
	}
	var ver *AppVersion
	if len(changes) > 0 {
		// L'immagine di una nuova versione di configurazione è quella dell'ultima versione.
		var number int
		if err := tx.QueryRow(ctx, `UPDATE core.apps SET next_version = next_version + 1 WHERE id = $1 RETURNING next_version - 1`, cur.ID).Scan(&number); err != nil {
			return App{}, nil, err
		}
		last, err := scanVersion(tx.QueryRow(ctx, `SELECT `+versionCols+` FROM core.app_versions v WHERE v.app_id = $1 ORDER BY v.number DESC LIMIT 1`, cur.ID))
		if err != nil {
			return App{}, nil, err
		}
		// Ogni versione copia variabili e nomi dei segreti attuali (GIT-348): senza, una modifica di
		// porta li cancellerebbe dalla storia.
		if cfg.Variables, cfg.SecretNames, err = envNames(ctx, tx, cur.ID); err != nil {
			return App{}, nil, err
		}
		summary := strings.Join(changes, ", ")
		if err := insertAppVersion(ctx, tx, cur.ID, number, "config", last.Image, cfg, &summary, nil, nil, actor, now); err != nil {
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
	if _, err := tx.Exec(ctx, `UPDATE core.apps SET port = $2, health_check = $3, restricted = $4, follow_branch = $5,
			auto_deploy_paused = $6, updated_at = $7, address_label = COALESCE(address_label, $8) WHERE id = $1`,
		cur.ID, cfg.Port, healthJSON(cfg.HealthCheck), cfg.Restricted, follow, paused, now, p.AddressLabel); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == "apps_address_label_key" {
			return App{}, nil, ErrAppAddressConflict
		}
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

func intPtrEq(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func healthEq(a, b *AppHealthCheck) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Path == b.Path && a.IntervalSeconds == b.IntervalSeconds && intPtrEq(a.TimeoutSeconds, b.TimeoutSeconds)
}

// ListAppVersions elenca le versioni dalla più recente, con il totale.
func (s *Store) ListAppVersions(ctx context.Context, appID uuid.UUID, limit, offset int) ([]AppVersion, int, error) {
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM core.app_versions WHERE app_id = $1`, appID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+versionCols+` FROM core.app_versions v WHERE v.app_id = $1
		ORDER BY v.number DESC LIMIT $2 OFFSET $3`, appID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AppVersion{}
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, v)
	}
	return out, total, rows.Err()
}

// GetAppVersion legge una versione per numero.
func (s *Store) GetAppVersion(ctx context.Context, appID uuid.UUID, number int) (AppVersion, error) {
	v, err := scanVersion(s.pool.QueryRow(ctx, `SELECT `+versionCols+` FROM core.app_versions v WHERE v.app_id = $1 AND v.number = $2`, appID, number))
	if errors.Is(err, pgx.ErrNoRows) {
		return AppVersion{}, ErrNotFound
	}
	return v, err
}
