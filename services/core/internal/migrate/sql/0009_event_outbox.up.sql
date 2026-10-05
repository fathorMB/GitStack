-- M-06/B (GIT-130): outbox transazionale degli eventi di dominio di core.
-- Ogni modifica che deve pubblicare un evento (issue, issue_comment,
-- repository) scrive qui una riga NELLA STESSA transazione: se la
-- transazione fallisce non resta niente, se riesce l'evento non si perde
-- anche con NATS irraggiungibile. Un relay in background (internal/outbox)
-- pubblica le righe non inviate con ack di JetStream, le segna inviate e
-- ritenta con attesa crescente. `id` e' l'id della busta (envelope.id) e
-- anche il Nats-Msg-Id: un doppio invio e' idempotente per i consumatori.
-- Come in 0004 nessuna FK verso identity.
CREATE TABLE core.event_outbox (
    seq             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    id              uuid        NOT NULL UNIQUE,
    name            text        NOT NULL,
    version         integer     NOT NULL,
    payload         jsonb       NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT clock_timestamp(),
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    attempts        integer     NOT NULL DEFAULT 0,
    last_error      text,
    sent_at         timestamptz
);

-- Righe da inviare, in ordine di inserimento.
CREATE INDEX event_outbox_pending ON core.event_outbox (next_attempt_at, seq) WHERE sent_at IS NULL;
-- Pulizia delle righe inviate.
CREATE INDEX event_outbox_sent ON core.event_outbox (sent_at) WHERE sent_at IS NOT NULL;
