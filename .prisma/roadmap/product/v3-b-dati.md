---
{"horizon":"now","id":"OBJ-990d3f4a-a762-48d5-a0a1-4147fca8b7d1","knowledge":["DOC-3e900450-a39c-42b0-a7e1-d6108b0f4363","DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e"],"reopen_reason":"Orizzonte now dal 2026-10-26.","schema_version":1,"title":"v3-b — Dati persistenti delle app","updated":"2026-10-25T23:32:19.375458500+00:00"}
---



# v3-b — Dati persistenti delle app

## Outcome

Un'app può conservare dati su volumi persistenti che entrano nei backup di GitStack e tornano con il ripristino, così wiki, CMS e piccoli strumenti interni girano su GitStack.

## Rationale

H3: alla maggior parte delle aziende on-prem i dati servono prima dell'esposizione su internet; i database gestiti restano in «Oltre». Orizzonte now dal 2026-10-26; retail.it valuterà di portarci il proprio PostgreSQL di test.

## Scope

Volumi di dimensione fissa nella quota dell'owner; app con volume a una sola copia; volumi inclusi di default nei backup ed escludibili per app; avviso e guida al dump per i database dentro un container (H3, D19).

Secondo dei tre passi della v3, in parallelo agli altri.

## Source references

- Tema di analisi "v3 — Hosting di app e container: regole di prodotto" (H3)

