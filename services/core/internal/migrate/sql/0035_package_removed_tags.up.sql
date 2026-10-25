-- v2-b (GIT-364, RG2): tag rimossi dalle regole di pulizia del registry.
CREATE TABLE core.repo_package_removed_tags (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repo_id UUID NOT NULL REFERENCES core.repositories (resource_id) ON DELETE CASCADE,
    image TEXT NOT NULL,
    name TEXT NOT NULL,
    digest TEXT NOT NULL,
    rule TEXT NOT NULL,
    removed_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX repo_package_removed_tags_repo_image_removed_idx
    ON core.repo_package_removed_tags (repo_id, image, removed_at DESC);
