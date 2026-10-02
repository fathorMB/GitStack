---
{"horizon":"next","id":"OBJ-0820d6e7-7b4f-4c7c-b84d-a3ef18d5bc81","knowledge":["DOC-7d7f0293-9b0f-4175-9bc5-b085d5db5f3f","DOC-7de1b0e3-a859-4ec8-90eb-f05cdec30ab7"],"schema_version":1,"title":"CLI gs e skills per agenti","updated":"2026-09-30T21:15:06.327763500+00:00"}
---

# CLI gs e skills per agenti

## Outcome

Un agente di coding, con la CLI `gs` e il pacchetto di skills, gestisce repo e issues su GitStack come oggi fa con `gh` su GitHub.

## Rationale

D8 e D17: gli agenti sono utenti di prima classe; una CLI con output JSON funziona con qualsiasi agente che usa un terminale; licenza Apache-2.0 per non porre barriere.

## Scope

`gs` in Go sul client generato; login e stato con token e più istanze; comandi repo e issue; comando `api` generico; output JSON stabile e codici di uscita documentati; skills per installazione e login, lavorare su una issue fino al commit, triage, gestione repo; binari multipiattaforma installabili in un comando; prova reale di un agente che chiude una issue solo con `gs` e skills.

Priorità 7 di 9 nella v1.

## Source references

- `.lmbrain-lite/milestones/M-07.md`

