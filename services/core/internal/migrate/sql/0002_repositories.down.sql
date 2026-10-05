-- Rollback di 0002_repositories. Le righe di core.resources con type='repo'
-- non possono convivere con l'indice parziale che non le copre: se esistono
-- duplicati (type, name) fra repo, il ripristino del vincolo originale
-- fallirebbe, quindi si eliminano prima i repo (la FK a cascata toglie anche
-- i dettagli).
DROP TABLE IF EXISTS core.pull_requests;
DROP TABLE IF EXISTS core.repo_counters;
DROP TABLE IF EXISTS core.repositories;
DELETE FROM core.resources WHERE type = 'repo';
DROP INDEX IF EXISTS core.resources_type_name_non_repo_key;
ALTER TABLE core.resources ADD CONSTRAINT resources_type_name_key UNIQUE (type, name);
