-- REL1 (GIT-289): metadati delle release nel backup Postgres; i byte degli
-- allegati restano nel volume e non sono inclusi dal dump predefinito.
CREATE TABLE core.releases (
    id UUID PRIMARY KEY,
    repo_id UUID NOT NULL REFERENCES core.resources(id) ON DELETE CASCADE,
    tag_name TEXT NOT NULL,
    name TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    draft BOOLEAN NOT NULL DEFAULT false,
    prerelease BOOLEAN NOT NULL DEFAULT false,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ NULL,
    UNIQUE (repo_id, tag_name)
);
CREATE INDEX releases_repo_created_idx ON core.releases (repo_id, created_at DESC);
CREATE TABLE core.release_assets (
    id UUID PRIMARY KEY,
    release_id UUID NOT NULL REFERENCES core.releases(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (release_id, name)
);
