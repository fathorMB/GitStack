---
{"horizon":"next","id":"OBJ-0820d6e7-7b4f-4c7c-b84d-a3ef18d5bc81","knowledge":["DOC-7d7f0293-9b0f-4175-9bc5-b085d5db5f3f","DOC-7de1b0e3-a859-4ec8-90eb-f05cdec30ab7","DOC-84811cef-6f8e-4028-97d6-d47381172907"],"reopen_reason":"Regole G1–G8 su CLI gs e skills (tema consolidato il 2026-10-05).","schema_version":1,"title":"CLI gs e skills per agenti","updated":"2026-10-05T09:50:00+00:00"}
---

# CLI gs e skills per agenti

## Outcome

Un agente di coding, con la CLI `gs` e il pacchetto di skills, gestisce repo e issues su GitStack come oggi fa con `gh` su GitHub.

## Rationale

D8 e D17: gli agenti sono utenti di prima classe; una CLI con output JSON funziona con qualsiasi agente che usa un terminale; licenza Apache-2.0 per non porre barriere.

## Scope

`gs` in Go sul client generato; login e stato con token e più istanze; comandi repo e issue; comando `api` generico; output JSON stabile e codici di uscita documentati; skills per installazione e login, lavorare su una issue fino al commit, triage, gestione repo; binari multipiattaforma installabili in un comando; prova reale di un agente che chiude una issue solo con `gs` e skills.

Regole in [[knowledge/topics/cli-gs-skills]] (G1–G8): comandi e flag familiari come `gh`; autenticazione con token, `GS_TOKEN`/`GS_HOST` e `gs auth setup-git`; `--json` stabile come contratto e codici di uscita documentati; comandi per repo, issue, etichette, milestone, ricerca, notifiche, browse, blame e skills, amministrazione via `gs api`; skills nel formato Agent Skills con alternativa `AGENTS.md`; binari e skills serviti dall'istanza su `/downloads`; istanza e repo ricavati dal remote `git`; conferma e `--yes` sulle operazioni distruttive.

Fuori dalla v1: login automatico dal browser, comandi admin dedicati, gestori di pacchetti.

Priorità 7 di 9 nella v1. Nuova schermata Downloads; mockup 06 e 14.

## Source references

- `.lmbrain-lite/milestones/M-07.md`
- Tema di analisi "CLI gs e skills per agenti: regole di prodotto" (G1–G8)
