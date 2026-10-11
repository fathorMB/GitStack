---
{"horizon":"next","id":"OBJ-0820d6e7-7b4f-4c7c-b84d-a3ef18d5bc81","knowledge":["DOC-7d7f0293-9b0f-4175-9bc5-b085d5db5f3f","DOC-7de1b0e3-a859-4ec8-90eb-f05cdec30ab7","DOC-84811cef-6f8e-4028-97d6-d47381172907","DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5"],"reopen_reason":"M-07 completata: famiglia G1–G8 coperta da test, guida alla prova reale con un agente.","schema_version":1,"title":"CLI gs e skills per agenti","updated":"2026-10-11T22:20:26.314956700+00:00"}
---


# CLI gs e skills per agenti

## Outcome

Un agente di coding, con la CLI `gs` e il pacchetto di skills, gestisce repo e issues su GitStack come oggi fa con `gh` su GitHub.

**Stato: completata** (main al commit `e708707`, 2026-10-12): famiglia G1–G8 tutta coperta da test; binari e skills serviti da `/downloads`; guida e script per la prova reale di un agente (GIT-174, `docs/agent-trial.md`). La prova "un agente chiude una issue end-to-end" resta anche un criterio di uscita di V1 (M-09). Dettagli in [[knowledge/topics/stato-di-realizzazione]].

## Rationale

D8 e D17: gli agenti sono utenti di prima classe; licenza Apache-2.0.

## Scope

Regole in [[knowledge/topics/cli-gs-skills]] (G1–G8). Fuori dalla v1: login automatico dal browser, comandi admin dedicati, gestori di pacchetti.

Priorità 7 di 9 nella v1.

## Source references

- `.lmbrain-lite/milestones/M-07.md`
- `docs/rules-coverage.md` (famiglia G)

