-- M-06/G (GIT-135): consegna dei webhook. Il motore dei webhook legge gli eventi
-- di dominio dall'outbox (0009) e crea le consegne (core.webhook_deliveries,
-- 0006) nella STESSA transazione in cui segna l'evento come elaborato:
-- webhooked_at e' il suo segno di lettura, indipendente da notified_at (0010)
-- cosi' notifiche e webhook non si aspettano a vicenda. Come per notified_at,
-- un flag per riga e non un cursore su seq: due transazioni possono
-- confermare fuori ordine. Gli eventi gia' presenti non generano consegne
-- (nascono prima del motore).
ALTER TABLE core.event_outbox ADD COLUMN webhooked_at timestamptz;
UPDATE core.event_outbox SET webhooked_at = clock_timestamp();
-- Eventi da elaborare, in ordine di inserimento.
CREATE INDEX event_outbox_unwebhooked ON core.event_outbox (seq) WHERE webhooked_at IS NULL;
