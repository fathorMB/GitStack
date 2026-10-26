-- GIT-374 (CI3 del 2026-10-26): chi ha acceso la CI sul mirror in pull. E' l'autore degli
-- eventi della sincronizzazione e il tetto dei permessi del token del job. Se perde admin
-- sul repo, la CI sul mirror si spegne (si verifica quando serve, in internal/ci).
ALTER TABLE core.ci_repo_state ADD COLUMN run_on_pull_mirror_by UUID NULL;
