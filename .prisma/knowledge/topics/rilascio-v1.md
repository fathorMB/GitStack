---
{"area":"delivery","id":"DOC-2bb044bc-b64d-4f66-a782-0d25a2866c5d","related":["TOP-7b3e077e-45ff-4aee-8aba-f41d636f7fe9","DOC-bcd6d2f7-d45a-4d19-8714-66a45a9c484b","DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e","OBJ-6c5776a1-ec92-4ed8-8bd8-def6ae041f9d","DOC-3595cb24-e019-4230-a412-009769bae527","TOP-0b04024c-6f6b-4d35-b790-97d7591d6c08","DOC-258621ff-aafe-4a28-8a49-b7e1c152530e","DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5"],"reopen_reason":"Soglia dei problemi di sicurezza per la v1.0 scelta dall'operatore il 2026-10-18 (opzione 3: vale V1 così com'è).","schema_version":1,"sources":[],"tags":["rilascio","sicurezza","versioni","dogfooding","m-09"],"title":"Rilascio v1.0: regole di prodotto","updated":"2026-10-18T00:05:17.618400400+00:00"}
---



# Rilascio v1.0: regole di prodotto

## Context

Regole per M-09 confermate dall'operatore il 2026-10-05 nel tema di analisi "Rilascio v1.0: regole di prodotto". Completano D12, D13, D16–D19 ([[knowledge/topics/decisioni]]), il modo di lavorare di [[knowledge/topics/sviluppo-e-qualita]] e i requisiti di [[knowledge/topics/installazione-e-deploy]]. P2 (nessun accesso anonimo) resta invariata.

## Confirmed decisions

| # | Regola |
|---|---|
| V1 | **Criteri di uscita, tutti obbligatori:** M-01…M-08 completate e ogni regola decisa (R, I, C, B, G, N, W, MP) coperta da almeno un test automatico; nessun bug aperto critico o alto (i medi nelle note di rilascio); revisione di sicurezza chiusa e test di fumo superati; installazione, aggiornamento dalla release candidate e ripristino verdi in CI su tutti i sistemi supportati; dogfooding di almeno 4 settimane senza perdite di dati, contate dal passaggio della casa su GitStack (tappa 2 di V7); un agente chiude una issue end-to-end solo con `gs` e skills. Prima della v1.0 una o più `v1.0.0-rc.N`. |
| V2 | **Sicurezza, revisione interna:** checklist OWASP ASVS livello 2, threat model delle aree a rischio (permessi, SSRF dei webhook e dei mirror, Markdown, raw e SVG, token degli agenti), ogni problema trovato diventa un test, scansioni in CI (dipendenze, codice, immagini) bloccanti. Nessun penetration test esterno nella v1.0. |
| V3 | **Carico, solo test di fumo:** qualche utente e agente contemporaneo su clone, push, API e issues senza errori; nessun obiettivo numerico. Le capacità di N1 sono dichiarate **indicative, non misurate**. |
| V4 | **Versioni e supporto:** SemVer; correzioni sull'ultima minore; patch di sicurezza anche sulla minore precedente per 6 mesi; aggiornamento diretto da una minore a qualsiasi successiva; `SECURITY.md` con segnalazione privata, risposta entro 5 giorni lavorativi e avviso pubblico. Nessuna LTS. |
| V5 | **Catena di fornitura:** immagini, chart e binari `gs` firmati con Sigstore/cosign dalla CI; SBOM per ogni rilascio (anche per la compatibilità AGPL); verifica automatica delle firme in `install.sh` e `gitstack upgrade` (eccezione esplicita per mirror interni non firmati); checksum SHA-256. |
| V6 | **Documentazione:** sito pubblico in inglese (utente, admin, API, CLI); nessuna copia nell'istanza e nessuna traduzione nella v1. |
| V7 | **Dogfooding presto:** appena M-03…M-05 sono usabili, istanza su Ubuntu Server installata con l'installer vero, aggiornata a ogni release candidate o build notturna, con backup giornalieri e un ripristino di prova; ospita codice e issues di GitStack; gli agenti lavorano con `gs` e skills; le issues di sviluppo passano gradualmente da GalaxyLab a GitStack. |
| V8 | **Pubblicazione:** GitStack è la casa del lavoro; GitHub è un mirror pubblico in sola lettura aggiornato a ogni push sul branch principale e sui tag; segnalazioni pubbliche dalle issues di GitHub o via `SECURITY.md`. Nessuna visibilità pubblica in GitStack. |

### V1 e V2: soglia dei problemi di sicurezza per la v1.0 (2026-10-18)

Scelta dell'operatore del 2026-10-18 (opzione 3), dopo il confronto tra V1 e `docs/release-checklist.md`: **vale V1 così com'è**. La revisione di sicurezza (V2) è "chiusa" per la v1.0 quando nel registro di `docs/security/asvs-l2.md` **non resta aperto nessun problema critico o alto**. I problemi medi e bassi ancora aperti non bloccano la v1.0: vanno elencati nelle **note di rilascio** come rischi noti. Al 2026-10-18 questo vale per F3 (medio, header di sicurezza del web: nessuna CSP come secondo livello dopo la sanificazione del Markdown) e per F12–F20 (bassi).

- Rischio accettato: F3 resta un rischio medio noto nella v1.0; il sanificatore del Markdown è l'unica difesa tra un autore di Markdown e la sessione di chi legge.
- Conseguenza per il repo (compito dei coding agent, non di Prisma): `docs/release-checklist.md` (passi 3 e 4) e il controllo A2 di `scripts/release-readiness.go` oggi chiedono ogni F<n> «Resolved» o `ok`, anche i bassi; vanno allineati a questa soglia, e la riga V2 di `docs/rules-coverage.md` può passare a `coperta` quando non restano critici o alti.
- Le correzioni di F3 e dei bassi restano possibili prima o dopo la v1.0; non sono obiettivi di prodotto nuovi.

### V7 dettagliata: passaggio a GitStack in due tappe (2026-10-12)

Scelta dell'operatore del 2026-10-12, su indicazione del CEO ("fase 3", item GalaxyLab GL-31). V7 e V8 restano l'obiettivo della v1.0; si arriva per tappe:

1. **Copia di backup (ora):** l'istanza homehub tiene una copia del repo GitHub di GitStack con il **mirror in pull** (regole MP1–MP7 in [[knowledge/topics/mirror-in-pull]]), primo pezzo di M-09. GitHub resta la casa del codice; il codice su homehub è in sola lettura, le issues si possono già usare.
2. **Casa su GitStack (prima della v1.0):** si interrompe il mirror in pull (MP6) e si attiva il mirror in push verso GitHub (V8-mirror); gli agenti fanno push su homehub con `gs` e skills; le issues di sviluppo passano gradualmente da GalaxyLab. Il periodo di almeno 4 settimane di dogfooding richiesto da V1 conta da questa tappa.

**Condizioni per la tappa 2 (scelta A dell'operatore, 2026-10-12)** — nessuna data fissa; quando sono tutte vere l'operatore sceglie il giorno, fa «Interrompi» sul mirror in pull e attiva il mirror in push:

1. il mirror in pull gira su homehub da almeno 2 settimane senza errori non spiegati;
2. su homehub i backup giornalieri sono attivi ed è riuscito un ripristino di prova su una macchina nuova;
3. la prova reale di un agente con `gs` e skills (GIT-174) è superata su homehub;
4. il mirror in push verso GitHub è stato provato su un repo di prova, senza `diverged`;
5. le istruzioni di sviluppo per gli agenti sono aggiornate (push su homehub, test in locale prima di unire).

Al 2026-10-18 la tappa 2 **non è ancora fatta**; stato delle condizioni in [[knowledge/topics/stato-di-realizzazione]].

**CI ponte dopo la tappa 2 (scelta B dell'operatore, 2026-10-12):** finché GitStack non ha la sua CI (v2-a, dopo la v1.0), GitHub Actions gira **solo su `main` e sui tag**, ricevuti dal mirror in push. Regola di sviluppo per gli agenti che lavorano su GitStack: **eseguire i test in locale prima di unire su `main`**. Rischio accettato: un errore che sfugge ai test locali arriva su `main` e viene segnalato da GitHub Actions dopo l'unione. Nessuna modifica al prodotto. La regola va riportata nelle istruzioni di sviluppo del repo (compito dei coding agent, non di Prisma).

### V8 dettagliata: mirror in push (GIT-179)

**V8-mirror:** un repo può avere mirror verso un remote HTTPS (gestiti da chi ha `admin` sul repo). A ogni push che aggiorna il **branch principale** o un **tag**, e su richiesta («Sincronizza ora»), core mette il mirror in coda e il servizio git spinge **solo il branch principale e i tag**, con push **mai forzato** (niente `--force`, niente `+` nei refspec, niente cancellazioni di ref). Se la destinazione ha una storia diversa (non fast-forward, o un tag già presente con un altro commit) il mirror si ferma (`diverged`), non sovrascrive niente, avvisa gli admin del repo e riparte solo con «Sincronizza ora». Gli altri errori si ritentano con attesa crescente (30 s, 2 min, 10 min, 1 h, 3 h, 6 h); un blocco della policy di uscita (C8) non si ritenta. Un solo push alla volta per mirror, con un timeout, e un log delle ultime esecuzioni (50, 30 giorni). La destinazione è solo `https://`, senza credenziali nell'indirizzo, controllata con `pkg/egress` (loopback, cluster, link-local e intervalli speciali bloccati, deny e allow dell'amministratore); la credenziale (utente e token) è cifrata come i segreti dei webhook, non torna mai nell'API e non compare né nei log né nella riga di comando di git.

## Open questions

Nessuna sul dogfooding né sulla soglia di sicurezza.

## Fuori dalla v1

Penetration test esterno (V2), test di carico con obiettivi numerici (V3), versioni LTS (V4), documentazione nell'istanza o tradotta (V6), visibilità pubblica dei repo in GitStack (V8), obbligo di chiudere i problemi di sicurezza medi e bassi prima della v1.0 (V1, scelta del 2026-10-18).

## Related topics

- [[knowledge/topics/sviluppo-e-qualita]]
- [[knowledge/topics/installazione-e-deploy]]
- [[knowledge/topics/decisioni]]
- [[knowledge/topics/mirror-in-pull]]
- [[knowledge/topics/ci-registry-release]]
- [[knowledge/topics/stato-di-realizzazione]]

