---
{"horizon":"now","id":"OBJ-20366828-5e70-4aba-9c46-1575a1bb7b18","knowledge":["DOC-3e900450-a39c-42b0-a7e1-d6108b0f4363","DOC-258621ff-aafe-4a28-8a49-b7e1c152530e","DOC-3595cb24-e019-4230-a412-009769bae527","DOC-2bb044bc-b64d-4f66-a782-0d25a2866c5d"],"reopen_reason":"Domande aperte risolte il 2026-10-26 (CI2, CI3, H6, H7, H14–H16); nessuna data, tempi gestiti dall'operatore.","schema_version":1,"sources":[{"source_id":"SRC-380ef9a9-8ee9-4334-bd62-3af9cb80cda0"}],"title":"Pilota retail.it — ambiente di test su GitStack","updated":"2026-10-25T23:32:19.378568700+00:00"}
---


# Pilota retail.it — ambiente di test su GitStack

## Outcome

Il progetto retail.it (primo repo ospitato ufficialmente) ha su GitStack un ambiente di test: il repo è un mirror in pull da GitHub, la CI di GitStack costruisce le immagini di API, sito e backoffice e le pubblica nel registry del repo, le immagini girano come app GitStack interne accessibili solo ai tester, e i tester aprono issue con screenshot sul repo.

## Rationale

Scelta dell'operatore del 2026-10-26: il pilota corre **in parallelo alla v1.0**, che resta il primo rilascio pubblico con i criteri V1. È il primo cliente reale della v3-a: collaudarla su un caso vero vale più di un collaudo interno. Nessuna data: i tempi li gestisce l'operatore, nel quadro della scelta di chiudere in parallelo tutti gli obiettivi decisi.

## Scope

**Prima parte (P1 della richiesta, serve per il primo rilascio in test)** — tutte coperte da regole decise, manca il codice:

1. CI su un repo in mirror in pull, accesa per repo da chi ha `admin`; ogni sincronizzazione con ref nuovi genera eventi push e tag come un push normale (CI3).
2. Build delle immagini nel runner integrato con BuildKit senza privilegi (CI2).
3. Hosting delle app attivabile dall'installer e da `gitstack upgrade`, con il dominio delle app (H2).
4. Immagini private del registry dello stesso repo usate dalle app senza credenziali a mano (H7, RG1).
5. Segreti delle app passati ai container, mai mostrati, nuova versione a ogni modifica (H8).
6. Login GitStack davanti alle app interne per chi ha `read` sul repo e per chi è nella lista di accesso dell'app, utente in intestazioni firmate (H6).

**Seconda parte (P2, subito dopo):** deploy automatico da branch o da regola sui tag e deploy di un tag preciso dalla CI (H7); variabili `GITSTACK_APP_VERSION`, `GITSTACK_COMMIT_SHA`, `GITSTACK_DEPLOYED_AT` (H14); nome interno stabile tra app dello stesso repo (H15); uscita verso internet e rete privata con guida (H16); log, stato e webhook `deployment` (H12); `gs app` (H13).

**Fuori dal pilota:** P3 (account bot, guida CA per i tester, Let's Encrypt della v3-c, volumi della v3-b) resta nei rispettivi obiettivi o si decide dopo.

## Open questions

- **Account bot** limitato ad aprire issue e caricare allegati (P3): bastano i grant (P4) o serve altro.
- Dove sta il PostgreSQL di test di retail.it (cloud o rete aziendale): decide se serve aprire una destinazione privata (H16).

## Source references

- Richiesta retail.it del 2026-10-26 (SRC-380ef9a9-8ee9-4334-bd62-3af9cb80cda0)
- [[knowledge/topics/hosting-app]], [[knowledge/topics/ci-registry-release]], [[knowledge/topics/mirror-in-pull]], [[knowledge/topics/rilascio-v1]]

