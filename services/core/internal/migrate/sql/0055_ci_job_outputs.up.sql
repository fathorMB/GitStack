-- GIT-501: outputs del job (jobs.<id>.outputs valutati dal runner a fine esecuzione), passati ai job che ne dipendono (needs.<job>.outputs).
ALTER TABLE core.ci_jobs ADD COLUMN outputs JSONB NOT NULL DEFAULT '{}'::jsonb;
