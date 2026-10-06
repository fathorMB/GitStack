-- M-06/E (GIT-133): motore delle notifiche in-app. Il notifier legge gli
-- eventi di dominio dall'outbox (0009), nello stesso ordine di inserimento, e
-- scrive core.notifications. notified_at e' il suo segno di lettura: NULL =
-- evento non ancora elaborato. Un flag per riga, non un cursore su seq,
-- perche' due transazioni possono confermare fuori ordine (seq 5 dopo seq 6):
-- un cursore salterebbe l'evento in ritardo, il flag lo trova comunque.
-- Gli eventi gia' presenti non generano notifiche (nascono prima del motore).
ALTER TABLE core.event_outbox ADD COLUMN notified_at timestamptz;
UPDATE core.event_outbox SET notified_at = clock_timestamp();
-- Eventi da elaborare, in ordine di inserimento.
CREATE INDEX event_outbox_unnotified ON core.event_outbox (seq) WHERE notified_at IS NULL;
