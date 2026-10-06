-- GIT-178 (R11 rivista): i nomi dei repo conservano le maiuscole e l'unicita' per
-- owner non distingue le maiuscole (come GitHub). Il nome resta occupato anche
-- nel cestino (R2): l'indice non filtra su deleted_at. I percorsi su disco sono
-- per id, quindi non cambiano.
ALTER TABLE core.repositories DROP CONSTRAINT repositories_owner_name_key;
ALTER TABLE core.repositories DROP CONSTRAINT repositories_name_check;
ALTER TABLE core.repositories ADD CONSTRAINT repositories_name_check
    CHECK (name ~ '^[A-Za-z0-9_-][A-Za-z0-9._-]{0,99}$' AND name NOT ILIKE '%.git');
CREATE UNIQUE INDEX repositories_owner_lower_name_key
    ON core.repositories (owner_type, owner_id, lower(name));
CREATE INDEX repositories_owner_name_lower_idx ON core.repositories (lower(owner_name), lower(name));
