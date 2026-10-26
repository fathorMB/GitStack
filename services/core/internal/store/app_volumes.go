package store

import (
	"context"
	"errors"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Volumi persistenti delle app (v3-b, GIT-356, H3). Un volume è un PVC di dimensione fissa (local-path
// di k3s) montato in un percorso del container. Le dimensioni contano nella quota disco dell'owner
// (core.app_owner_quotas.disk_bytes) e un'app con volumi gira in una sola copia. Ogni modifica crea
// una versione di configurazione che ne copia l'elenco in config.volumes (trigger config).
//
// I volumi sono dati, non configurazione: il rollback ripristina immagine, variabili e nomi dei
// segreti ma non toglie né rimette volumi (restano quelli attuali, vedi BeginAppDeploy).

// Errori del dominio dei volumi.
var (
	// ErrAppSingleCopy: l'app ha volumi (o li avrebbe) e non può avere più di una copia (H3).
	ErrAppSingleCopy = errors.New("un'app con volumi gira in una sola copia")
	// ErrVolumeNameTaken: l'app ha già un volume con quel nome.
	ErrVolumeNameTaken = errors.New("l'app ha già un volume con questo nome")
	// ErrVolumeMountTaken: un altro volume dell'app è montato nello stesso percorso.
	ErrVolumeMountTaken = errors.New("un altro volume dell'app è montato in questo percorso")
	// ErrTooManyVolumes: oltre MaxAppVolumes volumi per app.
	ErrTooManyVolumes = errors.New("troppi volumi per l'app")
)

const (
	// MaxAppVolumes è il massimo di volumi di un'app.
	MaxAppVolumes = 8
	// MinVolumeBytes è la dimensione minima di un volume.
	MinVolumeBytes int64 = 1 << 20
)

// AppVolumeSpec è un volume come lo congela una versione: PVC `vol-<Name>` di SizeBytes montato in MountPath.
type AppVolumeSpec struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	SizeBytes int64  `json:"sizeBytes"`
}

// AppVolume è la riga di un volume dell'app, con la versione che l'ha creato.
type AppVolume struct {
	AppVolumeSpec
	Version   int
	CreatedAt time.Time
	CreatedBy *AppActor
}

// ValidateVolumeMountPath dà il motivo se il percorso non può essere un mount, "" se va bene:
// assoluto, già pulito (niente `..`, `//`, `/.`), non la radice e non dentro /proc, /sys, /dev.
func ValidateVolumeMountPath(p string) string {
	switch {
	case !strings.HasPrefix(p, "/") || len(p) > 512:
		return "mountPath: percorso assoluto, al massimo 512 caratteri."
	case strings.ContainsAny(p, "\x00\r\n\t:,"):
		return "mountPath: caratteri non ammessi."
	case path.Clean(p) != p:
		return "mountPath: percorso non normalizzato (niente `..`, `.`, `//`, barra finale)."
	case p == "/":
		return "mountPath: la radice del container non si monta."
	}
	for _, reserved := range []string{"/proc", "/sys", "/dev"} {
		if p == reserved || strings.HasPrefix(p, reserved+"/") {
			return "mountPath: " + reserved + " è riservato al sistema."
		}
	}
	return ""
}

// volumeSpecs legge i volumi dell'app, in ordine di nome.
func volumeSpecs(ctx context.Context, q execQuerier, appID uuid.UUID) ([]AppVolumeSpec, error) {
	rows, err := q.Query(ctx, `SELECT name, mount_path, size_bytes FROM core.app_volumes WHERE app_id = $1 ORDER BY name`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppVolumeSpec
	for rows.Next() {
		var v AppVolumeSpec
		if err := rows.Scan(&v.Name, &v.MountPath, &v.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// volumesOfApp legge i volumi dell'app con versione e autore, in ordine di nome.
func volumesOfApp(ctx context.Context, q execQuerier, appID uuid.UUID) ([]AppVolume, error) {
	rows, err := q.Query(ctx, `SELECT name, mount_path, size_bytes, version, created_at, created_by FROM core.app_volumes
		WHERE app_id = $1 ORDER BY name`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppVolume{}
	for rows.Next() {
		var v AppVolume
		var by []byte
		if err := rows.Scan(&v.Name, &v.MountPath, &v.SizeBytes, &v.Version, &v.CreatedAt, &by); err != nil {
			return nil, err
		}
		if v.CreatedBy, err = scanActor(by); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListAppVolumes elenca i volumi dell'app per nome.
func (s *Store) ListAppVolumes(ctx context.Context, appID uuid.UUID) ([]AppVolume, error) {
	return volumesOfApp(ctx, s.pool, appID)
}

// AppVolumeChange è l'esito di una modifica ai volumi: la versione creata e la quota disco
// dell'owner dopo la modifica (per l'avviso all'80%).
type AppVolumeChange struct {
	Volume  AppVolume
	Version AppVersion
	Limits  AppQuotaAmounts
	Used    AppQuotaAmounts
}

// AddAppVolume aggiunge un volume e crea la versione di configurazione col suo deploy. Rifiuta con
// ErrAppSingleCopy se l'app ha più di una copia (H3), con CapacityError se la quota disco dell'owner
// non basta (Missing["diskBytes"]), con ErrVolumeNameTaken / ErrVolumeMountTaken / ErrTooManyVolumes.
// Il conteggio della quota è serializzato per owner come in CreateApp e SetAppCopies.
func (s *Store) AddAppVolume(ctx context.Context, appID uuid.UUID, in AppVolumeSpec, actor *AppActor, now time.Time) (AppVolumeChange, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AppVolumeChange{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	cur, err := scanApp(tx.QueryRow(ctx, `SELECT `+appCols+appFrom+` WHERE a.id = $1 FOR UPDATE OF a`, appID))
	if errors.Is(err, pgx.ErrNoRows) {
		return AppVolumeChange{}, ErrNotFound
	}
	if err != nil {
		return AppVolumeChange{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('app-quota:' || lower($1), 0))`, cur.Owner); err != nil {
		return AppVolumeChange{}, err
	}
	if cur.Config.Copies > 1 {
		return AppVolumeChange{}, ErrAppSingleCopy
	}
	existing, err := volumeSpecs(ctx, tx, appID)
	if err != nil {
		return AppVolumeChange{}, err
	}
	if len(existing) >= MaxAppVolumes {
		return AppVolumeChange{}, ErrTooManyVolumes
	}
	for _, v := range existing {
		switch {
		case v.Name == in.Name:
			return AppVolumeChange{}, ErrVolumeNameTaken
		case v.MountPath == in.MountPath:
			return AppVolumeChange{}, ErrVolumeMountTaken
		}
	}
	limits, used, _, err := ownerAppQuota(ctx, tx, cur.Owner)
	if err != nil {
		return AppVolumeChange{}, err
	}
	if used.DiskBytes+in.SizeBytes > limits.DiskBytes {
		return AppVolumeChange{}, &CapacityError{Owner: cur.Owner, Missing: map[string]int64{"diskBytes": used.DiskBytes + in.SizeBytes - limits.DiskBytes}, Limits: limits, Used: used}
	}
	number, err := allocVersion(ctx, tx, appID, now)
	if err != nil {
		return AppVolumeChange{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO core.app_volumes (app_id, name, mount_path, size_bytes, version, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, appID, in.Name, in.MountPath, in.SizeBytes, number, actorJSON(actor), now); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			if pgErr.ConstraintName == "app_volumes_mount" {
				return AppVolumeChange{}, ErrVolumeMountTaken
			}
			return AppVolumeChange{}, ErrVolumeNameTaken
		}
		return AppVolumeChange{}, err
	}
	cfg, err := currentConfig(ctx, tx, appID)
	if err != nil {
		return AppVolumeChange{}, err
	}
	ver, err := writeConfigVersion(ctx, tx, appID, number, cfg, "volume "+in.Name+" aggiunto", actor, now)
	if err != nil {
		return AppVolumeChange{}, err
	}
	used.DiskBytes += in.SizeBytes
	out := AppVolumeChange{Volume: AppVolume{AppVolumeSpec: in, Version: number, CreatedAt: now, CreatedBy: actor}, Version: ver, Limits: limits, Used: used}
	return out, tx.Commit(ctx)
}

// DeleteAppVolume toglie un volume e crea la versione di configurazione. I dati si perdono: il
// controller elimina il PVC quando la nuova versione è in servizio. ErrNotFound se il nome non c'è.
func (s *Store) DeleteAppVolume(ctx context.Context, appID uuid.UUID, name string, actor *AppActor, now time.Time) (AppVolumeChange, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AppVolumeChange{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	cur, err := scanApp(tx.QueryRow(ctx, `SELECT `+appCols+appFrom+` WHERE a.id = $1 FOR UPDATE OF a`, appID))
	if errors.Is(err, pgx.ErrNoRows) {
		return AppVolumeChange{}, ErrNotFound
	}
	if err != nil {
		return AppVolumeChange{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('app-quota:' || lower($1), 0))`, cur.Owner); err != nil {
		return AppVolumeChange{}, err
	}
	var gone AppVolume
	err = tx.QueryRow(ctx, `DELETE FROM core.app_volumes WHERE app_id = $1 AND name = $2
		RETURNING name, mount_path, size_bytes, version, created_at`, appID, name).
		Scan(&gone.Name, &gone.MountPath, &gone.SizeBytes, &gone.Version, &gone.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppVolumeChange{}, ErrNotFound
	}
	if err != nil {
		return AppVolumeChange{}, err
	}
	number, err := allocVersion(ctx, tx, appID, now)
	if err != nil {
		return AppVolumeChange{}, err
	}
	cfg, err := currentConfig(ctx, tx, appID)
	if err != nil {
		return AppVolumeChange{}, err
	}
	ver, err := writeConfigVersion(ctx, tx, appID, number, cfg, "volume "+name+" rimosso", actor, now)
	if err != nil {
		return AppVolumeChange{}, err
	}
	limits, used, _, err := ownerAppQuota(ctx, tx, cur.Owner)
	if err != nil {
		return AppVolumeChange{}, err
	}
	return AppVolumeChange{Volume: gone, Version: ver, Limits: limits, Used: used}, tx.Commit(ctx)
}
