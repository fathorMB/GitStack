---
{"horizon":"now","id":"OBJ-6c5776a1-ec92-4ed8-8bd8-def6ae041f9d","knowledge":["DOC-7de1b0e3-a859-4ec8-90eb-f05cdec30ab7","DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5","DOC-2bb044bc-b64d-4f66-a782-0d25a2866c5d","DOC-3595cb24-e019-4230-a412-009769bae527"],"reopen_reason":"Stato comunicato dall'operatore la sera del 2026-10-18: rilascio di prova verde (GIT-247), condizioni 3 e 4 superate (GIT-254, GIT-293), condizione 2 aperta per il restore (GIT-297).","schema_version":1,"title":"Rilascio v1.0","updated":"2026-10-18T22:00:09.022029900+00:00"}
---



# Rilascio v1.0

## Outcome

GitStack v1.0 è pubblicato open source, sicuro e documentato, e ospita il proprio codice (dogfooding).

## Rationale

Chiude la tappa v1 della visione; il dogfooding sostituisce GitHub come casa del codice. Il mirror in pull serve a tenere su homehub una copia di backup del repo GitHub e, a tendere, ai clienti che migrano da GitHub; l'import una tantum rende evidente il percorso di migrazione.

## Scope

Regole V1–V8 in [[knowledge/topics/rilascio-v1]] e MP1–MP8 in [[knowledge/topics/mirror-in-pull]].

**Passaggio su homehub in due tappe (V7):** tappa 1 copia di backup con il mirror in pull; tappa 2, prima della v1.0 e quando sono vere le condizioni 2–5 di [[knowledge/topics/rilascio-v1]] (la 1 è tolta il 2026-10-18), casa su GitStack. Le almeno 2 settimane di dogfooding di V1 contano dalla tappa 2, **non ancora fatta** al 2026-10-18 (stato delle condizioni in [[knowledge/topics/stato-di-realizzazione]]).

Avanzamento al 2026-10-18 sera (commit `4a946f2` più lo stato comunicato dall'operatore):
- **fatto:** tabella regole→test (V1, GIT-115); scansioni di sicurezza in CI (V2, GIT-114); mirror in push (V8-mirror, GIT-179); mirror in pull e import una tantum (MP1–MP6, MP8); revisione di sicurezza ASVS L2 e threat model (GIT-219) con correzione di F1, F2, F4–F11; test di fumo (V3); lista di controllo della rc e script `release-readiness` (GIT-226), allineati alla soglia di sicurezza (GIT-251, GIT-252); job di rilascio firmato con SBOM e changelog, provato sul tag `v0.0.0-test.9` e verificato con cosign (GIT-247); sito di documentazione che si costruisce (V6); condizioni 3, 4 e 5 della tappa 2 (GIT-254, GIT-293, `AGENTS.md`);
- **resta:** F3 e i bassi F12–F20; scheda Pull mirror nella UI (MP7) e «Sincronizza ora» con stato del mirror in push (GIT-180); pubblicazione del sito; politica di supporto (V4); condizione 2 (restore senza passi a mano, GIT-297, e nuova prova di ripristino) e tappa 2; dogfooding di almeno 2 settimane (V7); release candidate e criteri di uscita (V1).

Fuori dalla v1: penetration test esterno, test di carico con obiettivi numerici, versioni LTS, documentazione nell'istanza o tradotta, visibilità pubblica dei repo, mirror in pull via SSH o con LFS, importazione di issues da GitHub.

Priorità 9 di 9 nella v1.

## Source references

- `.lmbrain-lite/milestones/M-09.md`
- `docs/rules-coverage.md` (famiglie V e MP), `docs/release-checklist.md`, `docs/security/asvs-l2.md`
- Tema di analisi "Rilascio v1.0: regole di prodotto" (V1–V8) e scelta del 2026-10-18 sul dogfooding accorciato
- Tema di analisi "Mirror in pull" (MP1–MP7) e scelta MP8 del 2026-10-12


