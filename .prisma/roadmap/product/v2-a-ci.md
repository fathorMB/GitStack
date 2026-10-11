---
{"horizon":"later","id":"OBJ-dd71564f-d3a2-411d-bae7-f9395b449131","knowledge":["DOC-258621ff-aafe-4a28-8a49-b7e1c152530e","DOC-3d5a5862-5bff-4e92-821c-c5caa78ca358"],"reopen_reason":"Primo passo della v2 (operatore, 2026-10-12).","schema_version":1,"title":"v2-a — CI in stile GitHub Actions","updated":"2026-10-11T22:57:32.340694400+00:00"}
---


# v2-a — CI in stile GitHub Actions

## Outcome

I workflow in formato GitHub Actions girano su GitStack a ogni push, PR, avvio manuale o orario; i risultati compaiono come stati dei commit e gli agenti li seguono con `gs run`.

## Rationale

CI1–CI7: la parte più grande della v2 e quella che serve subito al dogfooding, perché dopo la tappa 2 i branch di lavoro di GitStack non hanno più la CI di GitHub.

## Scope

Workflow compatibili con GitHub Actions; runner integrato isolato più runner esterni; eventi push, pull_request, workflow_dispatch, schedule; segreti, variabili e token del job con tetto sui permessi; log, artefatti e cache; scheda Actions, notifiche, webhook, `gs run` e `gs workflow`, skills aggiornate; limiti di risorse automatici. Regole in [[knowledge/topics/ci-registry-release]].

Primo dei tre passi della v2; dipende dagli stati dei commit della v1.1 (PR9).

## Source references

- Tema di analisi "v2 — CI e registry container: regole di prodotto" (CI1–CI7)

