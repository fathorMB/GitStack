---
{"depends_on":["TOP-4a11694d-e261-4934-bafb-f17ccc6d1726"],"id":"TOP-74180f5f-ae9e-4bbf-b220-4b52950e5038","knowledge":["DOC-d1718775-770d-4065-8b27-3dc7a400dad6","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-8d539b83-ec08-41ca-a680-1529555fc42a","DOC-6878277b-230e-448b-a761-215e9d233c54"],"schema_version":1,"state":"consolidated","title":"Hosting Git: ciclo di vita e regole dei repository","updated":"2026-10-05T07:52:00+00:00"}
---

# Hosting Git: ciclo di vita e regole dei repository

## Expected learning

Regole di prodotto per M-03 (Hosting dei repository Git) che i permessi P1–P7 non coprono: come si chiama e si raggiunge un repo, cosa succede quando lo si rinomina, sposta o elimina, quali limiti valgono. Servono prima di realizzare il servizio git.

## Già deciso (da altri temi)

- Visibilità privato/interno, default privato, nessun accesso anonimo, anche per clone e pull (P2, P3, P7).
- Owner utente o organizzazione; repo personali ammessi (P6).
- Binario `git` ufficiale lato server, server SSH integrato su porta dedicata, evento `git.push` (D9, scelte di default).

## Scelte confermate dall'operatore (2026-10-05)

- **R1 — Indirizzi come GitHub:** `https://<host>/<owner>/<repo>.git` e `git@<host>:<owner>/<repo>.git` (forma SSH precisata da R7); la pagina web del repo è `/<owner>/<repo>` e `owner/repo` è il nome breve usato da `gs`. Utenti e organizzazioni condividono un unico spazio di nomi.
- **R2 — Eliminazione recuperabile per 7 giorni:** un repo eliminato sparisce subito per tutti (UI, API, clone/push); per 7 giorni può essere ripristinato, poi viene cancellato definitivamente. Durante i 7 giorni il nome `owner/repo` resta occupato e lo spazio su disco non viene liberato. Chi elimina e ripristina: vedi R12.
- **R3 — Nessuna rinomina né trasferimento nella v1:** `owner/repo` è fisso per tutta la vita del repo. Per cambiarlo si crea un nuovo repo e si sposta il codice. Niente redirect da gestire. Rinomina e trasferimento restano candidati per una versione successiva.
- **R4 — Branch principale `main`, modificabile per repo:** ogni nuovo repo parte con `main`; chi ha `admin` sul repo può scegliere come principale un altro branch esistente. Il branch principale è quello mostrato di default dal browser del codice (M-04) e quello su cui valgono le chiusure `fixes #12` (M-06).
- **R5 — Contenuto iniziale opzionale, tutto spento di default:** alla creazione si può chiedere README, `.gitignore` (da un elenco di modelli) e licenza (da un elenco di modelli), come nel mockup 04. Senza opzioni il repo nasce vuoto (mockup 06); con almeno un'opzione GitStack crea un primo commit su `main`. Le stesse opzioni sono disponibili da API e `gs`. Per la v1 basta un piccolo elenco di modelli (circa una decina per tipo).
- **R6 — Limiti di dimensione configurabili:** un push che contiene un file oltre 100 MB viene rifiutato con un messaggio chiaro; sul repo intero nessun limite rigido, solo un avviso oltre 5 GB; niente quote per organizzazione nella v1. Entrambe le soglie sono impostazioni dell'amministratore dell'installazione.
- **R7 — SSH sulla porta 2222 di default, configurabile all'installazione:** l'installer non tocca mai l'SSH della macchina host. Con la porta 2222 l'indirizzo SSH è `ssh://git@<host>:2222/<owner>/<repo>.git`; la forma corta `git@<host>:<owner>/<repo>.git` di R1 vale solo se l'amministratore sceglie la porta 22 (dopo aver spostato lui l'SSH dell'host). UI e `gs` mostrano sempre l'indirizzo completo corretto per l'installazione. La porta 2222 si aggiunge ai requisiti di rete dell'installer.
- **R8 — Git LFS dopo la v1:** nella v1 i file grandi sono governati solo dal limite per file di R6. LFS resta candidato per una versione successiva, da valutare su richieste reali; nulla di ciò che si costruisce in M-03 deve impedirne l'aggiunta.
- **R9 — Protezione minima del branch principale:** sul branch principale (R4) il server rifiuta force-push ed eliminazione, con un messaggio chiaro; la regola è attiva di default e chi ha `admin` sul repo può disattivarla. Gli altri branch sono liberi. Regole complete (review obbligatoria, elenchi di chi può pushare, più branch) arrivano con le Pull Request in v1.1, partendo da questa.
- **R10 — Archiviazione nella v1:** chi ha `admin` sul repo può archiviarlo e riattivarlo. Un repo archiviato resta visibile e clonabile secondo le regole di visibilità, ma rifiuta push e modifiche alle impostazioni (tranne la riattivazione) e mostra l'etichetta "Archived"; con M-05 le sue issues diventano di sola lettura.
- **R11 — Regole sui nomi dei repo:** solo minuscole, cifre, `-`, `_` e `.`; da 1 a 100 caratteri; non può iniziare con `.` né finire con `.git`; le maiuscole sono rifiutate (non convertite). Il nome è unico per owner.
- **R12 — Eliminazione e ripristino a chi ha `admin` sul repo:** owner dell'organizzazione, grant `admin`, proprietario del repo personale e, sempre, l'amministratore dell'installazione (coerente con P1 e P6). I repo eliminati compaiono in "Deleted repositories" nelle impostazioni dell'owner, con ripristino e giorni rimasti.

## Conseguenze da realizzare (evidenze dal codice)

- Oggi `identity.users.username` e `identity.organizations.name` sono unici ciascuno nella propria tabella (`services/identity/internal/migrate/sql/0001`, `0002`): un utente e un'organizzazione possono avere lo stesso nome. Con R1 serve un controllo comune.
- I percorsi web di primo livello (es. `login`, `settings`, `api`, `admin`) non devono poter diventare nomi di utente o organizzazione: serve un elenco di nomi riservati.

## Open questions

1. ~~Indirizzi~~ (R1); ~~nomi dei repo~~ (R11).
2. ~~Eliminazione~~ (R2); ~~chi ripristina~~ (R12).
3. ~~Rinomina e trasferimento~~ (R3, non nella v1).
4. ~~Branch principale~~ (R4).
5. ~~Contenuto iniziale~~ (R5).
6. ~~Limiti~~ (R6).
7. ~~Porta SSH~~ (R7).
8. ~~Git LFS~~ (R8, dopo la v1).
9. ~~Protezione dei branch~~ (R9).
10. ~~Archiviazione~~ (R10).

## Consolidation summary

Consolidato il 2026-10-05 con conferma dell'operatore. Regole R1–R12 per l'hosting dei repo: indirizzi `owner/repo` come GitHub con spazio di nomi unico; eliminazione recuperabile 7 giorni, gestita da chi ha `admin`; nessuna rinomina o trasferimento nella v1; branch principale `main` modificabile e protetto da force-push ed eliminazione; contenuto iniziale opzionale spento di default; limite 100 MB per file e avviso oltre 5 GB; SSH sulla porta 2222 configurabile; archiviazione in sola lettura; nomi minuscoli con `-` `_` `.`; LFS dopo la v1. Riportato in [[knowledge/topics/repository-git]], nell'obiettivo M-03, nelle scelte di default di [[knowledge/topics/decisioni]] e in [[knowledge/topics/installazione-e-deploy]]. Mockup 04, 06, 07 da aggiornare e schermata "Settings · General" del repo da aggiungere.
