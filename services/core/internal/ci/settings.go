package ci

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Default di CI5; l'amministratore li cambia con PUT /admin/ci/storage.
const (
	DefaultArtifactRetentionDays = 30
	DefaultArtifactMaxBytes      = int64(2) << 30
	DefaultCacheQuotaBytes       = int64(10) << 30
	DefaultCacheUnusedDays       = 7
	maxSettingDays               = 3650
)

// Settings sono la conservazione e i limiti di log, artefatti e cache (CI5).
type Settings struct {
	LogRetentionDays      int
	ArtifactRetentionDays int
	ArtifactMaxBytes      int64
	CacheQuotaBytes       int64
	CacheUnusedDays       int
}

// DefaultSettings ritorna i valori di CI5 (i log: la costante DefaultLogRetention di GIT-324).
func DefaultSettings() Settings {
	return Settings{
		LogRetentionDays:      int(DefaultLogRetention / (24 * time.Hour)),
		ArtifactRetentionDays: DefaultArtifactRetentionDays,
		ArtifactMaxBytes:      DefaultArtifactMaxBytes,
		CacheQuotaBytes:       DefaultCacheQuotaBytes,
		CacheUnusedDays:       DefaultCacheUnusedDays,
	}
}

// LogRetention, ArtifactRetention e CacheUnused sono le durate dei giorni impostati.
func (s Settings) LogRetention() time.Duration {
	return time.Duration(s.LogRetentionDays) * 24 * time.Hour
}
func (s Settings) ArtifactRetention() time.Duration {
	return time.Duration(s.ArtifactRetentionDays) * 24 * time.Hour
}
func (s Settings) CacheUnused() time.Duration {
	return time.Duration(s.CacheUnusedDays) * 24 * time.Hour
}

// Validate ritorna il campo (nome del contratto) e il messaggio del primo valore fuori scala.
func (s Settings) Validate() (field, msg string) {
	switch {
	case s.LogRetentionDays < 1 || s.LogRetentionDays > maxSettingDays:
		return "logRetentionDays", "logRetentionDays: da 1 a 3650."
	case s.ArtifactRetentionDays < 1 || s.ArtifactRetentionDays > maxSettingDays:
		return "artifactRetentionDays", "artifactRetentionDays: da 1 a 3650."
	case s.ArtifactMaxBytes < 1:
		return "artifactMaxBytes", "artifactMaxBytes: almeno 1 byte."
	case s.CacheQuotaBytes < 1:
		return "cacheQuotaBytes", "cacheQuotaBytes: almeno 1 byte."
	case s.CacheUnusedDays < 1 || s.CacheUnusedDays > maxSettingDays:
		return "cacheUnusedDays", "cacheUnusedDays: da 1 a 3650."
	}
	return "", ""
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// LoadSettings legge le impostazioni; senza riga ritorna i default.
func LoadSettings(ctx context.Context, q rowQuerier) (Settings, error) {
	var s Settings
	err := q.QueryRow(ctx, `SELECT log_retention_days, artifact_retention_days, artifact_max_bytes, cache_quota_bytes, cache_unused_days
		FROM core.ci_settings WHERE singleton`).Scan(&s.LogRetentionDays, &s.ArtifactRetentionDays, &s.ArtifactMaxBytes, &s.CacheQuotaBytes, &s.CacheUnusedDays)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return Settings{}, err
	}
	return s, nil
}

// SaveSettings sostituisce le impostazioni (già validate) e ritorna quelle salvate.
func SaveSettings(ctx context.Context, q rowQuerier, now time.Time, by uuid.UUID, s Settings) (Settings, error) {
	var out Settings
	err := q.QueryRow(ctx, `
		INSERT INTO core.ci_settings (singleton, log_retention_days, artifact_retention_days, artifact_max_bytes, cache_quota_bytes, cache_unused_days, updated_by, updated_at)
		VALUES (true, $1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (singleton) DO UPDATE SET log_retention_days = EXCLUDED.log_retention_days,
			artifact_retention_days = EXCLUDED.artifact_retention_days, artifact_max_bytes = EXCLUDED.artifact_max_bytes,
			cache_quota_bytes = EXCLUDED.cache_quota_bytes, cache_unused_days = EXCLUDED.cache_unused_days,
			updated_by = EXCLUDED.updated_by, updated_at = EXCLUDED.updated_at
		RETURNING log_retention_days, artifact_retention_days, artifact_max_bytes, cache_quota_bytes, cache_unused_days`,
		s.LogRetentionDays, s.ArtifactRetentionDays, s.ArtifactMaxBytes, s.CacheQuotaBytes, s.CacheUnusedDays, by, now).
		Scan(&out.LogRetentionDays, &out.ArtifactRetentionDays, &out.ArtifactMaxBytes, &out.CacheQuotaBytes, &out.CacheUnusedDays)
	return out, err
}
