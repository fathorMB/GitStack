---
{"area":"requirements","id":"DOC-6878277b-230e-448b-a761-215e9d233c54","related":["TOP-74180f5f-ae9e-4bbf-b220-4b52950e5038","TOP-4a11694d-e261-4934-bafb-f17ccc6d1726","DOC-d1718775-770d-4065-8b27-3dc7a400dad6","OBJ-bcb9bb89-a13a-4c0c-8a2f-e56f6dd76c38"],"schema_version":1,"sources":[],"tags":["git","repository","ssh","m-03"],"title":"Repository Git: regole di prodotto","updated":"2026-10-05T07:52:00+00:00"}
---

# Repository Git: regole di prodotto

## Context

Regole per l'hosting dei repo (M-03) confermate dall'operatore il 2026-10-05 nel tema di analisi "Hosting Git: ciclo di vita e regole dei repository". Si aggiungono alle regole su permessi e visibilità P1–P7 di [[knowledge/topics/identita-e-sicurezza]] (privato/interno, default privato, nessun accesso anonimo, repo personali ammessi).

## Confirmed decisions

| # | Regola |
|---|---|
| R1 | **Indirizzi come GitHub:** `https://<host>/<owner>/<repo>.git`; pagina web `/<owner>/<repo>`; `owner/repo` è il nome breve di `gs`. Utenti e organizzazioni condividono un unico spazio di nomi. |
| R2 | **Eliminazione recuperabile per 7 giorni:** il repo sparisce subito per tutti (UI, API, clone/push), resta ripristinabile per 7 giorni, poi è cancellato definitivamente. Nel frattempo il nome resta occupato e il disco non viene liberato. |
| R3 | **Nessuna rinomina né trasferimento nella v1:** `owner/repo` è fisso; per cambiarlo si crea un nuovo repo. Niente redirect. La scelta dell'owner va resa evidente alla creazione. |
| R4 | **Branch principale `main`, modificabile per repo** da chi ha `admin` (tra i branch esistenti). È il branch mostrato di default dal browser (M-04) e su cui valgono le chiusure `fixes #12` (M-06). |
| R5 | **Contenuto iniziale opzionale, tutto spento di default:** README, `.gitignore` e licenza (da piccoli elenchi di modelli). Senza opzioni il repo nasce vuoto; con almeno una, primo commit su `main`. Uguale da UI, API e `gs`. |
| R6 | **Limiti configurabili:** push rifiutato se contiene un file oltre 100 MB; avviso (non blocco) per repo oltre 5 GB; nessuna quota per organizzazione nella v1. Soglie impostabili dall'amministratore dell'installazione. |
| R7 | **SSH sulla porta 2222 di default, configurabile all'installazione:** l'installer non tocca mai l'SSH dell'host. Indirizzo `ssh://git@<host>:2222/<owner>/<repo>.git`; la forma corta `git@<host>:<owner>/<repo>.git` vale solo con porta 22. UI e `gs` mostrano sempre l'indirizzo completo dell'installazione. |
| R8 | **Git LFS dopo la v1.** I file grandi sono governati da R6; nulla in M-03 deve impedire di aggiungere LFS. |
| R9 | **Protezione minima del branch principale:** force-push ed eliminazione rifiutati, attiva di default, disattivabile da chi ha `admin`. Regole complete con le PR in v1.1. |
| R10 | **Archiviazione nella v1:** chi ha `admin` archivia e riattiva; il repo resta leggibile e clonabile ma rifiuta push e modifiche alle impostazioni; etichetta "Archived"; con M-05 le issues diventano di sola lettura. |
| R11 | **Nomi dei repo:** lettere maiuscole e minuscole, cifre, `-`, `_`, `.`; 1–100 caratteri; non inizia con `.`, non finisce con `.git`. Il nome conserva le maiuscole scritte alla creazione o alla modifica (es. `GitStack`) e così appare in UI, API, `gs` e indirizzi di clone mostrati. Unicità per owner **senza distinzione di maiuscole**: `GitStack` e `gitstack` non coesistono sotto lo stesso owner (il nome resta occupato anche nel cestino, R2). Ogni indirizzo risolve il repo in qualsiasi combinazione di maiuscole e minuscole: pagina web `/<owner>/<repo>`, API `/v1/repos/{owner}/{repo}/…`, git HTTPS `/<owner>/<repo>.git`, SSH `ssh://git@<host>:2222/<owner>/<repo>.git`, raw e archivi, `gs --repo`, riferimenti `owner/repo#n` nelle issues e nei commit (C1). Nomi di utenti e organizzazioni: restano in minuscolo (regola invariata). |
| R12 | **Eliminazione e ripristino a chi ha `admin` sul repo** (owner dell'organizzazione, grant `admin`, proprietario del repo personale) e sempre all'amministratore dell'installazione. Lista "Deleted repositories" nelle impostazioni dell'owner con ripristino e giorni rimasti. |

### Revisioni delle decisioni

- **2026-10-06, R11 (decisione del board, GIT-178):** la regola originale del 2026-10-05 rifiutava le maiuscole. Creando su homehub il repo per il codice di GitStack il nome `GitStack` è stato rifiutato: il rifiuto era corretto per la regola di allora, il board l'ha cambiata per comportarsi come GitHub (nome conservato come scritto, unicità e indirizzi senza distinzione di maiuscole). Il percorso su disco resta per id (R3), quindi i repo esistenti non si spostano; la migrazione `0013_repo_name_case` allarga il CHECK dei nomi e sostituisce l'unicità con un indice su `lower(name)`.

## Requisiti derivati

- **Spazio di nomi unico (R1):** oggi `identity.users.username` e `identity.organizations.name` sono unici solo nella propria tabella (`services/identity/internal/migrate/sql/0001`, `0002`); serve un controllo comune.
- **Nomi riservati (R1):** i percorsi web di primo livello (es. `login`, `settings`, `api`, `admin`) non possono diventare nomi di utente o organizzazione.
- **Porta 2222 (R7)** tra i requisiti di rete dell'installer: vedi [[knowledge/topics/installazione-e-deploy]].
- R6, R9 e R10 si applicano nello stesso punto: il controllo lato server alla ricezione del push.

## Fuori dalla v1

Rinomina e trasferimento (R3), Git LFS (R8), quote per organizzazione (R6), regole di protezione complete (R9).

## Related topics

- [[knowledge/topics/identita-e-sicurezza]]
- [[knowledge/topics/decisioni]]
- [[knowledge/topics/architettura]]
- [[knowledge/topics/installazione-e-deploy]]
