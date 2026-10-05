---
{"created":"2026-10-05T19:48:05.981947900+00:00","id":"FEEDBACK-90fe87f3-43f3-4f01-b70a-d42fc8cccea3","related":[],"schema_version":1,"title":"prisma_proposals_list fallisce su una proposta con campo `targets`","updated":"2026-10-05T19:48:05.981947900+00:00"}
---

## Observed issue

L'elenco delle proposte non si può leggere: una proposta salvata contiene un campo `targets` che lo schema attuale non accetta, quindi l'intero elenco va in errore.

## Evidence

Chiamata prisma_proposals_list il 2026-10-05 in sessione di analisi: "invalid_input: unknown field `targets`, expected one of `kind`, `summary`, `operations`, `note_id`, `note_digest` at line 9 column 13".
