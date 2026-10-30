-- v3-c (GIT-461): scadenza del certificato del cliente per cui l'avviso e' gia' partito. L'avviso
-- «scade entro 14 giorni» si manda una volta per scadenza: caricando un certificato nuovo la scadenza
-- cambia e l'avviso puo' ripartire. NULL = nessun avviso mandato.
ALTER TABLE core.app_domains ADD COLUMN expiry_notified_until TIMESTAMPTZ NULL;
