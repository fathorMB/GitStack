---
{"depends_on":["TOP-4a11694d-e261-4934-bafb-f17ccc6d1726","TOP-74180f5f-ae9e-4bbf-b220-4b52950e5038"],"id":"TOP-5dd4b056-b64a-4878-9266-6dc2e134dd00","knowledge":["DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-d1718775-770d-4065-8b27-3dc7a400dad6","DOC-6878277b-230e-448b-a761-215e9d233c54","DOC-1565db4b-4108-490d-aca1-28d30c36ad99"],"schema_version":1,"state":"consolidated","title":"Issues: regole di prodotto","updated":"2026-10-05T08:42:00+00:00"}
---

# Issues: regole di prodotto

## Expected learning

Regole per M-05 (Issues) oltre a quanto fissato da D10: numerazione, stati, chi può fare cosa, etichette, assegnatari, milestone, allegati e ricerca. Molte toccano il modello dati e il lavoro degli agenti, quindi vanno decise prima dello sviluppo.

## Già deciso (da altri temi)

- D10: titolo, Markdown, commenti, stato, assegnatari, ricerca, etichette, milestone, collegamenti ai commit (`#12`, `fixes #12`), notifiche e webhook; Kanban dopo la v1.
- Le chiusure `fixes #12` valgono sul branch principale (R4); in un repo archiviato le issues sono di sola lettura (R10).
- Permessi: `read` / `write` / `admin` sulla risorsa repo (P1–P7).

## Scelte confermate dall'operatore (2026-10-05)

- **I1 — Numerazione condivisa con le Pull Request:** un solo contatore `#n` per repo, condiviso da issues e (dalla v1.1) Pull Request; un numero non si riusa mai, nemmeno dopo un'eliminazione. `#12`, `fixes #12` e `gs` indicano sempre un solo oggetto.
- **I2 — Due stati, chiusura con motivo:** aperta / chiusa; alla chiusura un motivo tra *completata*, *non pianificata*, *duplicata di #n*. `fixes #12` chiude come *completata*. Icone distinte e filtro `reason:` in lista, API e `gs`. Nell'avanzamento delle milestone le issue *non pianificate* e *duplicate* non contano. Riaprire azzera il motivo. Stati personalizzati esclusi (vicini al Kanban, rimandato da D10).
- **I3 — `read` apre e commenta, `write` gestisce:** chiunque veda il repo (anche via visibilità interna) apre issues e commenta; solo chi ha `write` assegna, mette etichette e milestone, chiude e riapre issues altrui. L'autore può sempre chiudere e riaprire la propria issue.
- **I4 — Modifiche tracciate, issues non eliminabili:** l'autore modifica il proprio testo (issue o commento) con indicazione "edited"; chi ha `admin` sul repo vede le versioni precedenti. Un commento può essere eliminato dal suo autore o da chi ha `admin`, lasciando la traccia "comment deleted" nella cronologia. Una issue non si elimina: si chiude come *non pianificata*; chi ha `admin` può **nasconderla** (es. dati sensibili incollati per errore): il numero resta, il contenuto è visibile solo a chi ha `admin`.
- **I5 — Etichette predefinite con convenzioni per agenti:** "Create default labels" è attiva di default alla creazione (mockup 04) e crea `bug`, `enhancement`, `documentation`, `question`, `duplicate`, `good first issue`, `agent-ready` (issue abbastanza chiara da affidare a un agente) e `needs-human` (l'agente si è fermato e serve una persona). Sono etichette per repo, rinominabili ed eliminabili, senza comportamenti automatici nella v1. Skills e `gs` possono contare su questa convenzione. Etichette di organizzazione escluse dalla v1.
- **I6 — Fino a 10 assegnatari, solo con `write`:** si possono assegnare persone o agenti che hanno `write` sul repo; gli agenti mostrano il badge "agent" (mockup 13). Chi ha `write` può assegnarsi da solo (è il gesto con cui un agente prende il lavoro). Il filtro `assignee:@agents` (mockup 12) seleziona le issues assegnate ad agenti.
- **I7 — Milestone per repo:** ogni repo ha le sue milestone (titolo, descrizione, data facoltativa), create e gestite da chi ha `write`, chiudibili e riapribili; una issue appartiene al massimo a una milestone. L'avanzamento segue I2. Milestone di organizzazione escluse (conflitto con la visibilità dei repo privati).
- **I8 — Menzioni rispettose della visibilità:** `@utente` / `@agente` notifica (M-06) solo se il destinatario può vedere il repo; altrimenti resta testo, senza notifica e senza concedere accesso. `@org/team` notifica i membri del team che vedono il repo. L'editor suggerisce solo nomi validi. Una menzione non assegna la issue.
- **I9 — Allegati protetti dai permessi del repo:** si allegano (anche incollando) immagini, PDF, testo/log e ZIP a issues e commenti; limite 10 MB per file, configurabile dall'amministratore. I file stanno sul volume dati di GitStack, sono inclusi nel backup coerente (D19) e si scaricano solo con una sessione o un token di chi vede il repo: nessun URL pubblico (P2).
- **I10 — Ricerca con sintassi GitHub, nel repo e su tutta l'installazione:** filtri `is:open|closed`, `reason:`, `label:`, `assignee:` (con `@me` e `@agents`), `author:`, `milestone:`, `no:assignee|label|milestone`, `repo:owner/nome`, `org:nome`, più testo libero su titolo, descrizione e commenti (ricerca testuale PostgreSQL, D6). Stessa sintassi in UI, API e `gs`. La ricerca globale mostra solo risultati di repo visibili all'utente; la Home (mockup 02) usa la stessa ricerca.
- **I11 — Extra v1: blocco e modelli sì, trasferimento no:** chi ha `admin` può bloccare la discussione di una issue (da quel momento commenta solo chi ha `write`). I modelli di issue sono file Markdown versionati nel repo in `.gitstack/ISSUE_TEMPLATE/`, proposti alla creazione di una issue da UI, API e `gs`. Nessun trasferimento di issues tra repo (cambierebbe il numero di I1 e richiederebbe redirect, come evitato in R3).

## Open questions

1. ~~Numerazione~~ (I1).
2. ~~Stati e chiusura~~ (I2).
3. ~~Chi apre e commenta~~ (I3).
4. ~~Modifica ed eliminazione~~ (I4).
5. ~~Etichette predefinite~~ (I5).
6. ~~Assegnatari~~ (I6).
7. ~~Milestone~~ (I7).
8. ~~Menzioni~~ (I8).
9. ~~Allegati~~ (I9).
10. ~~Ricerca~~ (I10).
11. ~~Extra v1~~ (I11).

## Consolidation summary

Consolidato il 2026-10-05 con conferma dell'operatore. Regole I1–I11: numerazione `#n` condivisa con le PR; chiusura con motivo (completata, non pianificata, duplicata); `read` apre e commenta, `write` gestisce; modifiche tracciate e issues non eliminabili ma nascondibili; etichette predefinite con `agent-ready` e `needs-human`; fino a 10 assegnatari con `write`; milestone per repo; menzioni che rispettano la visibilità; allegati protetti fino a 10 MB; ricerca con sintassi GitHub su repo e installazione; blocco della discussione e modelli di issue, nessun trasferimento. Riportato in [[knowledge/topics/issues]], nell'obiettivo M-05 e in D10 di [[knowledge/topics/decisioni]]. Mockup 12 e 13 da aggiornare e schermata "New issue" da aggiungere.
