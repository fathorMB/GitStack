DROP INDEX IF EXISTS core.ci_jobs_runner_idx;
ALTER TABLE core.ci_jobs DROP COLUMN IF EXISTS runner_id;
ALTER TABLE core.ci_jobs DROP COLUMN IF EXISTS runs_on;
DROP TABLE IF EXISTS core.ci_runners;
DROP TABLE IF EXISTS core.ci_runner_registration_tokens;
