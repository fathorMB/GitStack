-- Rollback di 0015: via il mirror in pull, il registro e i ref divergenti.
DROP TABLE IF EXISTS core.repo_pull_mirror_refs;
DROP TABLE IF EXISTS core.repo_pull_mirror_runs;
DROP TABLE IF EXISTS core.repo_pull_mirrors;
