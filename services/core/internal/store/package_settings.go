package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DefaultDeleteUntaggedAfterDays è il default di RG2: le immagini senza tag si eliminano
// dopo 7 giorni.
const DefaultDeleteUntaggedAfterDays = 7
const RecentRemovedTagsPerImage = 20

// PackageSettings sono le impostazioni del registry di un repo (GIT-325, RG2).
type PackageSettings struct {
	ProtectedTagPatterns    []string
	DeleteUntaggedAfterDays int
	IncludeInBackup         bool
	// KeepLastTags e DeleteShaTagsAfterDays sono facoltative: nil = regola spenta.
	KeepLastTags           *int
	DeleteShaTagsAfterDays *int
	UpdatedAt              *time.Time
}

// RemovedPackageTag è un tag tolto dalla pulizia automatica del registry (RG2).
type RemovedPackageTag struct {
	Image     string    `json:"image"`
	Name      string    `json:"name"`
	Digest    string    `json:"digest"`
	Rule      string    `json:"rule"`
	RemovedAt time.Time `json:"removedAt"`
}

// RecordRemovedPackageTag conserva un tag eliminato dalla pulizia del registry.
func (s *Store) RecordRemovedPackageTag(ctx context.Context, repoID uuid.UUID, tag RemovedPackageTag) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO core.repo_package_removed_tags (repo_id, image, name, digest, rule, removed_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, repoID, tag.Image, tag.Name, tag.Digest, tag.Rule, tag.RemovedAt)
	return err
}

// ListRemovedPackageTags elenca i tag rimossi, dai più recenti, per immagine.
func (s *Store) ListRemovedPackageTags(ctx context.Context, repoID uuid.UUID) ([]RemovedPackageTag, error) {
	rows, err := s.pool.Query(ctx, `SELECT image, name, digest, rule, removed_at FROM (
		SELECT image, name, digest, rule, removed_at,
			ROW_NUMBER() OVER (PARTITION BY image ORDER BY removed_at DESC) AS ordinal
		FROM core.repo_package_removed_tags WHERE repo_id = $1
	) recent WHERE ordinal <= $2 ORDER BY image, removed_at DESC`, repoID, RecentRemovedTagsPerImage)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RemovedPackageTag
	for rows.Next() {
		var tag RemovedPackageTag
		if err := rows.Scan(&tag.Image, &tag.Name, &tag.Digest, &tag.Rule, &tag.RemovedAt); err != nil {
			return nil, err
		}
		out = append(out, tag)
	}
	return out, rows.Err()
}

// DefaultPackageSettings sono i valori di un repo che non le ha mai salvate.
func DefaultPackageSettings() PackageSettings {
	return PackageSettings{ProtectedTagPatterns: []string{}, DeleteUntaggedAfterDays: DefaultDeleteUntaggedAfterDays}
}

// GetPackageSettings legge le impostazioni del repo; senza riga ritorna i default.
func (s *Store) GetPackageSettings(ctx context.Context, repoID uuid.UUID) (PackageSettings, error) {
	var p PackageSettings
	err := s.pool.QueryRow(ctx, `SELECT protected_tag_patterns, delete_untagged_after_days, include_in_backup,
		keep_last_tags, delete_sha_tags_after_days, updated_at
		FROM core.repo_package_settings WHERE repo_id = $1`, repoID).
		Scan(&p.ProtectedTagPatterns, &p.DeleteUntaggedAfterDays, &p.IncludeInBackup, &p.KeepLastTags, &p.DeleteShaTagsAfterDays, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultPackageSettings(), nil
	}
	if err != nil {
		return PackageSettings{}, err
	}
	if p.ProtectedTagPatterns == nil {
		p.ProtectedTagPatterns = []string{}
	}
	return p, nil
}

// PutPackageSettings salva le impostazioni del repo (sostituisce tutto) e ritorna quelle salvate.
func (s *Store) PutPackageSettings(ctx context.Context, repoID, by uuid.UUID, p PackageSettings) (PackageSettings, error) {
	if p.ProtectedTagPatterns == nil {
		p.ProtectedTagPatterns = []string{}
	}
	var out PackageSettings
	err := s.pool.QueryRow(ctx, `INSERT INTO core.repo_package_settings
		(repo_id, protected_tag_patterns, delete_untagged_after_days, include_in_backup, keep_last_tags, delete_sha_tags_after_days, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (repo_id) DO UPDATE SET protected_tag_patterns = EXCLUDED.protected_tag_patterns,
			delete_untagged_after_days = EXCLUDED.delete_untagged_after_days, include_in_backup = EXCLUDED.include_in_backup,
			keep_last_tags = EXCLUDED.keep_last_tags, delete_sha_tags_after_days = EXCLUDED.delete_sha_tags_after_days,
			updated_by = EXCLUDED.updated_by, updated_at = now()
		RETURNING protected_tag_patterns, delete_untagged_after_days, include_in_backup, keep_last_tags, delete_sha_tags_after_days, updated_at`,
		repoID, p.ProtectedTagPatterns, p.DeleteUntaggedAfterDays, p.IncludeInBackup, p.KeepLastTags, p.DeleteShaTagsAfterDays, by).
		Scan(&out.ProtectedTagPatterns, &out.DeleteUntaggedAfterDays, &out.IncludeInBackup, &out.KeepLastTags, &out.DeleteShaTagsAfterDays, &out.UpdatedAt)
	return out, err
}

// OwnerRepoIDs elenca gli id dei repo (anche eliminati da meno di 7 giorni, che conservano le
// immagini) dello stesso owner del repo dato, per la quota (RG2).
func (s *Store) OwnerRepoIDs(ctx context.Context, repoID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT b.resource_id FROM core.repositories a
		JOIN core.repositories b ON b.owner_type = a.owner_type AND b.owner_id = a.owner_id
		WHERE a.resource_id = $1`, repoID)
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

// BackupRepoIDs elenca i repo le cui immagini vanno nel backup (includeInBackup).
func (s *Store) BackupRepoIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT repo_id FROM core.repo_package_settings WHERE include_in_backup`)
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

// GetRepoByID legge un repo dal suo id (anche se eliminato: ArchivedAt/DeletedAt dicono lo stato).
func (s *Store) GetRepoByID(ctx context.Context, id uuid.UUID) (Repo, error) {
	repo, err := scanRepo(s.pool.QueryRow(ctx, `SELECT `+repoCols+` FROM core.repositories r WHERE r.resource_id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Repo{}, ErrNotFound
	}
	return repo, err
}
