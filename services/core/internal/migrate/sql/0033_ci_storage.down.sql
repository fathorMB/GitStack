DROP TRIGGER IF EXISTS ci_caches_tombstone ON core.ci_caches;
DROP TRIGGER IF EXISTS ci_artifacts_tombstone ON core.ci_artifacts;
DROP FUNCTION IF EXISTS core.ci_blob_tombstone();
DROP TABLE IF EXISTS core.ci_blob_tombstones;
DROP TABLE IF EXISTS core.ci_caches;
DROP TABLE IF EXISTS core.ci_artifacts;
DROP TABLE IF EXISTS core.ci_settings;
