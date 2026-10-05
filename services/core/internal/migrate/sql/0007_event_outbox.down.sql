-- Rollback di 0007: si perdono gli eventi non ancora pubblicati.
DROP TABLE IF EXISTS core.event_outbox;
