DROP TABLE IF EXISTS core.ci_job_logs;
DROP TABLE IF EXISTS core.ci_job_tokens;
DROP INDEX IF EXISTS core.ci_jobs_task_id_key;
ALTER TABLE core.ci_jobs DROP COLUMN IF EXISTS task_id;
