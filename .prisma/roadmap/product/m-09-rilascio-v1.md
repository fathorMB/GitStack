---
{"horizon":"now","id":"OBJ-6c5776a1-ec92-4ed8-8bd8-def6ae041f9d","knowledge":["DOC-7de1b0e3-a859-4ec8-90eb-f05cdec30ab7","DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5","DOC-2bb044bc-b64d-4f66-a782-0d25a2866c5d","DOC-3595cb24-e019-4230-a412-009769bae527"],"reopen_reason":"Avanzamento riallineato al commit 4a946f2 (2026-10-18); tappa 2 non ancora fatta.","schema_version":1,"title":"Rilascio v1.0","updated":"2026-10-18T00:04:16.491133500+00:00"}
---


# Rilascio v1.0

## Outcome

GitStack v1.0 è pubblicato open source, sicuro e documentato, e ospita il proprio codice (dogfooding).

## Rationale

Chiude la tappa v1 della visione; il dogfooding sostituisce GitHub come casa del codice. Il mirror in pull serve a tenere su homehub una copia di backup del repo GitHub e, a tendere, ai clienti che migrano da GitHub; l'import una tantum rende evidente il percorso di migrazione.

## Scope

Regole V1–V8 in [[knowledge/topics/rilascio-v1]] e MP1–MP8 in [[knowledge/topics/mirror-in-pull]].

**Passaggio su homehub in due tappe (V7):** tappa 1 copia di backup con il mirror in pull; tappa 2, prima della v1.0 e quando sono vere le cinque condizioni di [[knowledge/topics/rilascio-v1]], casa su GitStack. Le 4 settimane di dogfooding di V1 contano dalla tappa 2, **non ancora fatta** al 2026-10-18 (stato delle condizioni in [[knowledge/topics/stato-di-realizzazione]]).

Avanzamento al commit `4a946f2` (2026-10-18):
- **fatto:** tabella regole→test (V1, GIT-115); scansioni di sicurezza in CI (V2, GIT-114); mirror in push (V8-mirror, GIT-179); mirror in pull e import una tantum (MP1–MP6, MP8); revisione di sicurezza ASVS L2 e threat model (GIT-219) con correzione di F1, F2, F4–F11; test di fumo (V3); lista di controllo della rc e script `release-readiness` (GIT-226); job di rilascio firmato con SBOM e changelog, scritto; sito di documentazione che si costruisce (V6); istruzioni per gli agenti dopo la tappa 2 (condizione 5);
- **resta:** F3 e i bassi F12–F20; scheda Pull mirror nella UI (MP7) e «Sincronizza ora» con stato del mirror in push (GIT-180); prova del rilascio su un tag di prova; pubblicazione del sito; politica di supporto (V4); condizioni 1–4 e tappa 2; dogfooding di almeno 4 settimane (V7); release candidate e criteri di uscita (V1).

Fuori dalla v1: penetration test esterno, test di carico con obiettivi numerici, versioni LTS, documentazione nell'istanza o tradotta, visibilità pubblica dei repo, mirror in pull via SSH o con LFS, importazione di issues da GitHub.

Priorità 9 di 9 nella v1.

## Source references

- `.lmbrain-lite/milestones/M-09.md`
- `docs/rules-coverage.md` (famiglie V e MP), `docs/release-checklist.md`, `docs/security/asvs-l2.md`
- Tema di analisi "Rilascio v1.0: regole di prodotto" (V1–V8)
- Tema di analisi "Mirror in pull" (MP1–MP7) e scelta MP8 del 2026-10-12

