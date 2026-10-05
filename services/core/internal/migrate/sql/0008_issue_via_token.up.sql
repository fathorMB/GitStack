-- GIT-125 (M-05): token con cui la issue o il commento e' stato creato
-- (mockup 13, "via token"). NULL per le sessioni web. Il nome e' una copia al
-- momento della creazione: il token puo' essere rinominato o revocato dopo.
ALTER TABLE core.issues ADD COLUMN via_token_id UUID NULL, ADD COLUMN via_token_name TEXT NULL;
ALTER TABLE core.issue_comments ADD COLUMN via_token_id UUID NULL, ADD COLUMN via_token_name TEXT NULL;
