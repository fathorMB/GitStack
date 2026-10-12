-- M-09/A3 (GIT-197): notifiche del mirror in pull. Il motivo resta `mirror`
-- (stessa casella, stesse preferenze del mirror in push); cambia il legame:
-- `pull_mirror_id` punta al mirror in pull, come `mirror_id` al mirror in push.
-- Una notifica `mirror` ha esattamente uno dei due.
ALTER TABLE core.notifications ADD COLUMN pull_mirror_id UUID NULL REFERENCES core.repo_pull_mirrors (id) ON DELETE CASCADE;
ALTER TABLE core.notifications DROP CONSTRAINT notifications_mirror_matches_reason;
ALTER TABLE core.notifications ADD CONSTRAINT notifications_mirror_matches_reason CHECK (
    (reason = 'mirror') = (mirror_id IS NOT NULL OR pull_mirror_id IS NOT NULL)
    AND NOT (mirror_id IS NOT NULL AND pull_mirror_id IS NOT NULL));
