-- Le versioni nate dalla regola sui tag tornano «manual»: il vecchio vincolo non conosce tag_push.
UPDATE core.app_versions SET trigger = 'manual' WHERE trigger = 'tag_push';
ALTER TABLE core.app_versions DROP CONSTRAINT app_versions_trigger_check;
ALTER TABLE core.app_versions ADD CONSTRAINT app_versions_trigger_check
    CHECK (trigger IN ('create', 'manual', 'branch_push', 'config', 'rollback'));

ALTER TABLE core.apps DROP CONSTRAINT IF EXISTS apps_follow_one_source;
ALTER TABLE core.apps DROP COLUMN IF EXISTS follow_tag_pattern;
