-- v2-b (GIT-325, RG2): impostazioni del registry di un repo (tag protetti, pulizia, backup).
-- Una riga per repo, creata al primo salvataggio: senza riga valgono i default di RG2
-- (nessun tag protetto, immagini senza tag eliminate dopo 7 giorni, fuori dai backup).
CREATE TABLE core.repo_package_settings (
    repo_id UUID PRIMARY KEY REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    protected_tag_patterns TEXT[] NOT NULL DEFAULT '{}',
    delete_untagged_after_days INTEGER NOT NULL DEFAULT 7 CHECK (delete_untagged_after_days >= 1),
    include_in_backup BOOLEAN NOT NULL DEFAULT false,
    keep_last_tags INTEGER NULL CHECK (keep_last_tags IS NULL OR keep_last_tags >= 1),
    delete_sha_tags_after_days INTEGER NULL CHECK (delete_sha_tags_after_days IS NULL OR delete_sha_tags_after_days >= 1),
    updated_by UUID NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
