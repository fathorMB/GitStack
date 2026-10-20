ALTER TABLE core.ci_repo_state
    DROP COLUMN IF EXISTS token_actions,
    DROP COLUMN IF EXISTS token_packages,
    DROP COLUMN IF EXISTS token_contents;
DROP TABLE IF EXISTS core.ci_variables;
DROP TABLE IF EXISTS core.ci_secrets;
