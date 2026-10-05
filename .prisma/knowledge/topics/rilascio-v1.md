---
{"area":"delivery","id":"DOC-2bb044bc-b64d-4f66-a782-0d25a2866c5d","related":["TOP-7b3e077e-45ff-4aee-8aba-f41d636f7fe9","DOC-bcd6d2f7-d45a-4d19-8714-66a45a9c484b","DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e","OBJ-6c5776a1-ec92-4ed8-8bd8-def6ae041f9d"],"schema_version":1,"sources":[],"tags":["rilascio","sicurezza","versioni","dogfooding","m-09"],"title":"Rilascio v1.0: regole di prodotto","updated":"2026-10-05T14:10:00+00:00"}
---

# Rilascio v1.0: regole di prodotto

## Context

Regole per M-09 confermate dall'operatore il 2026-10-05 nel tema di analisi "Rilascio v1.0: regole di prodotto". Completano D12, D13, D16–D19 ([[knowledge/topics/decisioni]]), il modo di lavorare di [[knowledge/topics/sviluppo-e-qualita]] e i requisiti di [[knowledge/topics/installazione-e-deploy]]. P2 (nessun accesso anonimo) resta invariata.

## Confirmed decisions

| # | Regola |
|---|---|
| V1 | **Criteri di uscita, tutti obbligatori:** M-01…M-08 completate e ogni regola decisa (R, I, C, B, G, N, W) coperta da almeno un test automatico; nessun bug aperto critico o alto (i medi nelle note di rilascio); revisione di sicurezza chiusa e test di fumo superati; installazione, aggiornamento dalla release candidate e ripristino verdi in CI su tutti i sistemi supportati; dogfooding di almeno 4 settimane senza perdite di dati; un agente chiude una issue end-to-end solo con `gs` e skills. Prima della v1.0 una o più `v1.0.0-rc.N`. |
| V2 | **Sicurezza, revisione interna:** checklist OWASP ASVS livello 2, threat model delle aree a rischio (permessi, SSRF dei webhook, Markdown, raw e SVG, token degli agenti), ogni problema trovato diventa un test, scansioni in CI (dipendenze, codice, immagini) bloccanti. Nessun penetration test esterno nella v1.0. |
| V3 | **Carico, solo test di fumo:** qualche utente e agente contemporaneo su clone, push, API e issues senza errori; nessun obiettivo numerico. Le capacità di N1 sono dichiarate **indicative, non misurate**. |
| V4 | **Versioni e supporto:** SemVer; correzioni sull'ultima minore; patch di sicurezza anche sulla minore precedente per 6 mesi; aggiornamento diretto da una minore a qualsiasi successiva; `SECURITY.md` con segnalazione privata, risposta entro 5 giorni lavorativi e avviso pubblico. Nessuna LTS. |
| V5 | **Catena di fornitura:** immagini, chart e binari `gs` firmati con Sigstore/cosign dalla CI; SBOM per ogni rilascio (anche per la compatibilità AGPL); verifica automatica delle firme in `install.sh` e `gitstack upgrade` (eccezione esplicita per mirror interni non firmati); checksum SHA-256. |
| V6 | **Documentazione:** sito pubblico in inglese (utente, admin, API, CLI); nessuna copia nell'istanza e nessuna traduzione nella v1. |
| V7 | **Dogfooding presto:** appena M-03…M-05 sono usabili, istanza su Ubuntu Server installata con l'installer vero, aggiornata a ogni release candidate o build notturna, con backup giornalieri e un ripristino di prova; ospita codice e issues di GitStack; gli agenti lavorano con `gs` e skills; le issues di sviluppo passano gradualmente da GalaxyLab a GitStack. |
| V8 | **Pubblicazione:** GitStack è la casa del lavoro; GitHub è un mirror pubblico in sola lettura aggiornato a ogni push sul branch principale e sui tag; segnalazioni pubbliche dalle issues di GitHub o via `SECURITY.md`. Nessuna visibilità pubblica in GitStack. |

## Fuori dalla v1

Penetration test esterno (V2), test di carico con obiettivi numerici (V3), versioni LTS (V4), documentazione nell'istanza o tradotta (V6), visibilità pubblica dei repo in GitStack (V8).

## Related topics

- [[knowledge/topics/sviluppo-e-qualita]]
- [[knowledge/topics/installazione-e-deploy]]
- [[knowledge/topics/decisioni]]
