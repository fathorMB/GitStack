---
{"depends_on":["TOP-74180f5f-ae9e-4bbf-b220-4b52950e5038","TOP-5dd4b056-b64a-4878-9266-6dc2e134dd00","TOP-062637bf-9acd-47cb-a089-3fb3bccee486","TOP-f4845d97-2a90-48c2-9a81-93e78e48c537","TOP-5cc14fc3-52fa-4baa-ae1b-6cd27bf145e4","TOP-e3e42f34-b1db-4b54-8b6d-3a4f1a2845a5","TOP-9b160889-2953-4eaf-b316-1fb22dc8f061"],"id":"TOP-7b3e077e-45ff-4aee-8aba-f41d636f7fe9","knowledge":["DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-7de1b0e3-a859-4ec8-90eb-f05cdec30ab7","DOC-bcd6d2f7-d45a-4d19-8714-66a45a9c484b","DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e","DOC-2bb044bc-b64d-4f66-a782-0d25a2866c5d"],"schema_version":1,"state":"consolidated","title":"Rilascio v1.0: regole di prodotto","updated":"2026-10-05T14:10:00+00:00"}
---

# Rilascio v1.0: regole di prodotto

## Expected learning

Regole per M-09: quando la v1.0 è "pronta", come si verifica la sicurezza, che carico deve reggere, come si versiona e si supporta, come si firmano i rilasci, dove sta la documentazione, come avviene il dogfooding e dove si pubblica il codice open source.

## Già deciso (da altri temi)

- D12 tutto open source con ricavi da supporto e servizi; D13 server AGPL-3.0, D17 CLI e skills Apache-2.0; D16 operatore + agenti AI con test forti; D18 upgrade con backup e rollback; D19 backup e ripristino testati in CI.
- P2 nessun accesso anonimo: anche per clone e pull.
- N1 profilo consigliato fino a ~100 utenti; N2 Ubuntu Server e Pop!_OS; W1 Windows di prova.

## Scelte confermate dall'operatore (2026-10-05)

- **V1 — Criteri di uscita misurabili, tutti obbligatori:** (1) M-01…M-08 completate e ogni regola di prodotto decisa (R, I, C, B, G, N, W) coperta da almeno un test automatico; (2) nessun bug aperto di gravità critica o alta, i medi elencati nelle note di rilascio; (3) revisione di sicurezza chiusa e test di carico superati; (4) installazione, aggiornamento dalla release candidate e ripristino verdi in CI su tutti i sistemi supportati (N2); (5) dogfooding: il codice di GitStack vive su GitStack da almeno 4 settimane senza perdite di dati; (6) un agente chiude una issue end-to-end usando solo `gs` e le skills. Prima della v1.0 una o più release candidate `v1.0.0-rc.N`.
- **V2 — Revisione di sicurezza interna, senza esterni:** checklist basata su OWASP ASVS livello 2; threat model delle aree a rischio (permessi P1–P7, SSRF dei webhook C8, Markdown B2, raw e SVG B1/B3, token degli agenti P4/P5); ogni problema trovato diventa un test automatico; scansioni in CI (dipendenze vulnerabili, analisi del codice, immagini container) bloccanti per il rilascio. Nessun penetration test esterno per la v1.0; resta un'opzione per versioni successive.
- **V3 — Test di carico: solo test di fumo:** si verifica che un'installazione regga un uso con qualche utente e agente contemporaneo (clone, push, API, issues) senza errori, senza obiettivi numerici. Conseguenza: le capacità indicate in N1 ("fino a ~20" e "fino a ~100 utenti") restano **indicative, non misurate**, e la documentazione lo dichiara. Test di carico con obiettivi numerici candidati per versioni successive.
- **V4 — SemVer, supporto dell'ultima minore, `SECURITY.md`:** versioni `MAGGIORE.MINORE.PATCH` (patch = correzioni, minore = funzioni compatibili, maggiore = cambi incompatibili), coerenti con contratto API e `gs`. Correzioni sempre sull'ultima minore; patch di sicurezza anche sulla minore precedente per 6 mesi dall'uscita della successiva. Aggiornamento diretto da una minore a qualsiasi successiva con `gitstack upgrade` (D18). `SECURITY.md` con indirizzo per segnalazioni private, risposta entro 5 giorni lavorativi e avviso pubblico con la versione corretta. Nessuna versione LTS.
- **V5 — Firma di tutto, SBOM e verifica automatica:** immagini container, chart Helm e binari di `gs` firmati con Sigstore/cosign dalla CI (senza chiavi private gestite a mano); SBOM per ogni rilascio (anche come controllo della compatibilità AGPL delle dipendenze); `install.sh` e `gitstack upgrade` verificano le firme e si fermano se non tornano, salvo opzione esplicita per mirror interni non firmati; checksum SHA-256 per tutti i download.
- **V6 — Documentazione solo in inglese, solo sito pubblico:** un sito di documentazione pubblico (utente, admin, API, CLI) in inglese. Nessuna copia servita dall'istanza e nessuna traduzione italiana nella v1 (la UI resta comunque in inglese con italiano incluso). Coerente con N4: le installazioni senza internet non sono nella v1.
- **V7 — Dogfooding presto, su un server dedicato:** appena M-03, M-04 e M-05 sono usabili, un'istanza su Ubuntu Server (N2) installata con l'installer vero ospita codice e issues del progetto GitStack; si aggiorna a ogni release candidate o build notturna con `gitstack upgrade` (prova continua di D18), con backup giornalieri e almeno un ripristino di prova (D19). Gli agenti di sviluppo lavorano con `gs` e le skills (prova reale di V1, punto 6). Le issues di sviluppo passano gradualmente da GalaxyLab a GitStack. GitHub resta come copia aggiornata in automatico (vedi domanda 8).
- **V8 — GitStack casa del lavoro, GitHub vetrina pubblica:** lo sviluppo (codice, issues, agenti) avviene sull'istanza GitStack (V7); un mirror in sola lettura su GitHub si aggiorna in automatico a ogni push sul branch principale e sui tag, e da lì il pubblico legge, clona e scarica le release. Segnalazioni pubbliche dalle issues di GitHub (riportate su GitStack dai maintainer) o via email per la sicurezza (V4). P2 resta invariata: nessuna visibilità pubblica in GitStack nella v1; rivalutabile dopo la v1 se richiesta.

## Open questions

1. ~~Criteri di uscita~~ (V1).
2. ~~Revisione di sicurezza~~ (V2, solo interna).
3. ~~Test di carico~~ (V3, solo test di fumo).
4. ~~Versioni e supporto~~ (V4).
5. ~~Catena di fornitura~~ (V5).
6. ~~Documentazione~~ (V6).
7. ~~Dogfooding~~ (V7).
8. ~~Pubblicazione~~ (V8).

## Consolidation summary

Consolidato il 2026-10-05 con conferma dell'operatore. Regole V1–V8: sei criteri di uscita misurabili e obbligatori con release candidate; revisione di sicurezza solo interna (ASVS L2, threat model, scansioni bloccanti); solo test di fumo, con capacità di N1 indicative; SemVer con supporto dell'ultima minore e patch di sicurezza sulla precedente per 6 mesi, `SECURITY.md`; firma con cosign, SBOM e verifica automatica; documentazione pubblica solo in inglese; dogfooding anticipato su Ubuntu Server con passaggio graduale delle issues da GalaxyLab; GitHub come mirror pubblico in sola lettura, P2 invariata. Conflitti risolti con l'operatore: GitHub non è più temporaneo ma mirror permanente; il tracciamento passa gradualmente a GitStack. Riportato in [[knowledge/topics/rilascio-v1]], nell'obiettivo M-09, in [[knowledge/topics/decisioni]], [[knowledge/topics/sviluppo-e-qualita]] e [[knowledge/topics/installazione-e-deploy]].
