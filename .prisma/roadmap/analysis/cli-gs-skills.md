---
{"depends_on":["TOP-74180f5f-ae9e-4bbf-b220-4b52950e5038","TOP-5dd4b056-b64a-4878-9266-6dc2e134dd00","TOP-062637bf-9acd-47cb-a089-3fb3bccee486","TOP-f4845d97-2a90-48c2-9a81-93e78e48c537"],"id":"TOP-5cc14fc3-52fa-4baa-ae1b-6cd27bf145e4","knowledge":["DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-7d7f0293-9b0f-4175-9bc5-b085d5db5f3f","DOC-d1718775-770d-4065-8b27-3dc7a400dad6","DOC-84811cef-6f8e-4028-97d6-d47381172907"],"schema_version":1,"state":"consolidated","title":"CLI gs e skills per agenti: regole di prodotto","updated":"2026-10-05T09:50:00+00:00"}
---

# CLI gs e skills per agenti: regole di prodotto

## Expected learning

Regole per M-07 oltre a D8 e D17: quanto `gs` somiglia a `gh`, come si autentica, com'è l'output per gli agenti, quali comandi entrano nella v1, che forma hanno le skills e come si distribuisce tutto on-prem.

## Già deciso (da altri temi)

- D8: REST + OpenAPI come fonte unica; `gs` in Go sul client generato, output JSON, stile `gh`; skills; server MCP in v1.1. D17: CLI e skills Apache-2.0.
- Agenti con utenti agent dedicati e token gestiti dall'admin (P4, P5); notifiche lette via `gs` (C4); ricerca con sintassi GitHub uguale in UI, API e `gs` (I10); blame, ricerca nel codice e storico disponibili via `gs` (B4, B5).
- Indirizzi `owner/repo` come nome breve (R1); SSH sulla porta 2222 (R7).

## Scelte confermate dall'operatore (2026-10-05)

- **G1 — Familiare come `gh`, non un clone:** stessi nomi di comandi e flag di `gh` dove i concetti coincidono (es. `gs issue list --label bug --assignee @me --json number,title`, `gs issue view 12`, `gs repo clone acme/api`, `gs api …`). Dove GitStack ha regole proprie (chiusura `duplicate` I2, archiviazione R10, repo eliminati R12, blame B4…) comandi e opzioni aggiuntivi nello stesso stile. Nessuna promessa di compatibilità piena con `gh`.
- **G2 — Autenticazione con token:** `gs auth login` chiede istanza e token; con `--web` apre la pagina "nuovo token" della UI con gli scope consigliati preselezionati. Per agenti e CI bastano `GS_TOKEN` e `GS_HOST`, senza comandi interattivi. Token salvato nel portachiavi del sistema operativo se disponibile, altrimenti in un file leggibile solo dall'utente. `gs auth setup-git` configura `git` per usare lo stesso token in HTTPS. `gs auth status` mostra istanza, utente e scope. Login automatico dal browser (flusso di autorizzazione lato server) escluso dalla v1.
- **G3 — Output: testo di default, JSON stabile su richiesta:** tabelle e testo per le persone; `--json` (con selezione dei campi, es. `--json number,title,labels`) e `--jq` per filtrare, come `gh`. I campi JSON sono un contratto: nella v1 si aggiungono ma non si tolgono né si rinominano senza una nuova versione maggiore di `gs`. Codici di uscita documentati: `0` ok, `1` errore generico, `2` uso errato, `4` non autenticato, `5` permesso negato, `6` non trovato. In modalità JSON gli errori escono come JSON su stderr.
- **G4 — Comandi della v1:** comandi dedicati per il lavoro quotidiano: `auth`, `repo` (create, list, view, clone, edit, archive, delete, restore), `issue` (create, list, view, edit, comment, close, reopen, lock), `label`, `milestone`, `search` (issues e codice), `notification`, `browse`, `blame`, `skills`. Amministrazione (organizzazioni, team, grant, webhook, utenti e token agent) tramite `gs api` su qualsiasi endpoint dell'API pubblica. Comandi admin dedicati esclusi dalla v1.
- **G5 — Skills nel formato aperto Agent Skills:** ogni skill è una cartella con `SKILL.md` (nome, descrizione, istruzioni). `gs skills install` riconosce l'agente presente e installa nel progetto o a livello utente; `gs skills update` allinea alla versione adatta all'istanza; `gs skills install --agents-md` aggiunge una sezione breve in `AGENTS.md` per gli agenti che non leggono le skills. Pacchetto v1: installazione e login, lavorare su una issue fino al commit, triage delle issues, gestione dei repo, ciclo notifiche → lavoro → risposta (C4). Licenza Apache-2.0 (D17), adattabili dai clienti.
- **G6 — Distribuzione dall'istanza, più copia pubblica:** ogni istanza serve da `https://<host>/downloads` i binari di `gs` (Linux, macOS, Windows; amd64 e arm64) e le skills della propria versione; installazione in un comando (`curl -fsSL https://<host>/install-gs.sh | sh`, script PowerShell su Windows); funziona anche air-gapped. Gli stessi binari sono pubblicati nelle release pubbliche del progetto. `gs` controlla la versione del server: avviso se non combaciano, rifiuto solo con versione maggiore diversa. Gestori di pacchetti (Homebrew, apt, winget) dopo la v1.
- **G7 — Più istanze, scelta dal contesto come `gh`:** dentro un repo clonato `gs` ricava istanza e `owner/repo` dal remote `origin` (HTTPS o SSH, anche con porta 2222) e usa le credenziali di quell'host; fuori da un repo usa `GS_HOST` o l'istanza predefinita (l'ultima configurata). `--hostname` e `--repo owner/repo` forzano la scelta. `gs auth status` elenca tutte le istanze configurate.
- **G8 — Conferma sulle operazioni distruttive:** eliminazioni, archiviazioni, blocchi e cancellazioni di etichette o milestone chiedono conferma interattiva (nome del repo da riscrivere per eliminare o archiviare un repo). Senza terminale interattivo l'operazione fallisce con codice `2` (uso errato, G3) salvo `--yes`. Le skills vietano agli agenti `--yes` su eliminazioni e archiviazioni senza istruzione esplicita di una persona. Gli scope del token restano il limite: senza permesso adeguato nemmeno `--yes` basta.

## Open questions

1. ~~Somiglianza con `gh`~~ (G1).
2. ~~Login~~ (G2).
3. ~~Output~~ (G3).
4. ~~Comandi della v1~~ (G4).
5. ~~Forma delle skills~~ (G5).
6. ~~Distribuzione~~ (G6).
7. ~~Più istanze~~ (G7).
8. ~~Operazioni distruttive~~ (G8).

## Consolidation summary

Consolidato il 2026-10-05 con conferma dell'operatore. Regole G1–G8: `gs` familiare come `gh` ma non un clone; autenticazione con token (incollato, `--web`, `GS_TOKEN`/`GS_HOST`) e `setup-git`; testo di default e JSON stabile su richiesta con codici di uscita documentati; comandi dedicati per il lavoro quotidiano e `gs api` per l'amministrazione; skills nel formato aperto Agent Skills con alternativa `AGENTS.md`; binari e skills serviti dall'istanza (anche air-gapped) più copia pubblica; istanza e repo ricavati dal remote `git`; conferma e `--yes` sulle operazioni distruttive. Riportato in [[knowledge/topics/cli-gs-skills]], nell'obiettivo M-07, in [[knowledge/topics/contratto-api]] e in [[knowledge/topics/installazione-e-deploy]]. Schermata Downloads da aggiungere e mockup 14 da aggiornare.
