-- F13 (GIT-239): avvisi sulle credenziali dell'utente (cambio password, nuovo
-- token, nuova chiave SSH, token in scadenza, ASVS V2.2.3, V2.3.3, V2.5.5).
-- Nuovo motivo `credential`: nessun repo, nessuna issue; il nome dell'evento
-- (event_name, es. credential.password_changed) e i dettagli (data: nome del
-- token o della chiave, mai segreti) bastano per il testo. Non e' un tipo che
-- l'utente puo' spegnere dalle preferenze email: e' un avviso di sicurezza.
ALTER TABLE core.notifications DROP CONSTRAINT notifications_reason_check;
ALTER TABLE core.notifications ADD CONSTRAINT notifications_reason_check CHECK (reason IN (
    'assigned', 'mentioned', 'participating', 'subscribed', 'commit_linked', 'state_change', 'webhook', 'mirror', 'credential'));
