---
{"area":"requirements","id":"DOC-3d5a5862-5bff-4e92-821c-c5caa78ca358","related":["TOP-4538888e-9b10-4aed-937a-f1d3bd849a67","DOC-1565db4b-4108-490d-aca1-28d30c36ad99","DOC-6878277b-230e-448b-a761-215e9d233c54","DOC-04fbc589-a144-403b-99e0-13e541fac16a","DOC-6db23bbe-fbf2-4913-a207-711865af66f6","DOC-84811cef-6f8e-4028-97d6-d47381172907","OBJ-33ee21a1-e5f1-4a4e-8668-4dfb162d8446"],"reopen_reason":"PR12: dettagli emersi dai mockup 25–28, confermati dall'operatore il 2026-10-05.","schema_version":1,"sources":[],"tags":["pull-request","v1.1","review","merge","agenti","mockup"],"title":"Pull Request (v1.1): regole di prodotto","updated":"2026-10-05T20:36:52.766359800+00:00"}
---


# Pull Request (v1.1): regole di prodotto

## Context

Regole per le Pull Request della v1.1 confermate dall'operatore il 2026-10-05 nel tema di analisi "v1.1 — Pull Request: regole di prodotto". Si appoggiano a I1 e I2 ([[knowledge/topics/issues]]), R4 e R9 ([[knowledge/topics/repository-git]]), B6 ([[knowledge/topics/browser-codice]]), C1–C8 ([[knowledge/topics/collegamenti-notifiche-webhook]]) e G3 ([[knowledge/topics/cli-gs-skills]]). Mockup: schermate 25–28 di `design/mockups-v1/index.html` (gruppo "Pull Request (v1.1)").

## Confirmed decisions

| # | Regola |
|---|---|
| PR1 | **Solo branch dello stesso repo:** una PR unisce due branch dello stesso repo; per aprirla serve `write`. Chi ha `read` propone con una issue. Fork esclusi dalla v1.1, senza impedirli in futuro. |
| PR2 | **Approvano le persone:** chi ha `write` approva o chiede modifiche, non sulla propria PR. Gli agenti fanno review e possono "approvare", ma la loro approvazione si vede e **non conta**: serve almeno una persona. |
| PR3 | **Merge commit e squash:** scelta al merge, squash proposto; `admin` può disattivarne uno. Niente rebase. Merge da chi ha `write`, agenti compresi, nel rispetto di PR4 e PR9. "Delete branch" dopo il merge; eliminazione automatica facoltativa, spenta di default. |
| PR4 | **"Richiedi una Pull Request" sul branch principale:** impostazione di `admin`, spenta di default. Se accesa: nessun push diretto, **admin compresi**; merge con almeno 1 approvazione di una persona (impostabile 1–5); nuovi commit annullano le approvazioni. |
| PR5 | **Commenti sulle righe:** su una riga o un gruppo di righe del diff, raccolti in review *Comment* / *Approve* / *Request changes*; conversazioni "risolte" da autore o `write`; blocchi `suggestion` mostrati come diff ma senza "Applica"; commenti su righe cambiate marcati "outdated". |
| PR6 | **Bozze e tre stati:** aperta (bozza o pronta), chiusa, unita. Le bozze non si uniscono e non chiedono review fino a "Ready for review". Chiusa si riapre se il branch esiste; unita è definitiva; chiudere non elimina il branch. |
| PR7 | **Conflitti:** "This branch has conflicts" con l'elenco dei file, merge disattivato, istruzioni `git` / `gs pr checkout`; stesso stato in API. Una PR solo "indietro" si unisce. Niente editor dei conflitti né "Update branch". |
| PR8 | **Collegamento alle issues:** parole chiave di C2 anche nella descrizione della PR; la issue mostra "PR #n will close this issue"; al merge sul branch principale si chiude come *completata*, con qualunque metodo di merge. Nessun collegamento manuale. |
| PR9 | **Stati dei commit:** API per sistemi esterni con `write`: *pending* / *success* / *failure* / *error*, contesto, descrizione, link; mostrati in PR, vista commit, API e `gs`. In PR4 `admin` può rendere obbligatori dei contesti. La CI di v2 userà la stessa API. |
| PR10 | **Agenti:** nessuna regola speciale nel server. Le skills insegnano: autoassegnazione, branch, PR in bozza con `fixes #n`, "Ready for review" a stati verdi con review chiesta a chi ha affidato la issue, `needs-human` se bloccati. Comandi `gs pr` create, list, view, checkout, diff, review, comment, ready, merge, close, status, con `--json` e ricerca `is:pr`. |
| PR11 | **Notifiche e webhook:** come le issues (C3–C8), con i revisori richiesti tra chi segue. Notifiche per richiesta e esito di review, commenti, pronta, merge/chiusura, conflitti e stati obbligatori rossi sulle proprie PR. Email di default per richieste di review e menzioni. Webhook `pull_request`, `pull_request_review`, `commit_status`. |
| PR12 | **Dettagli emersi dai mockup (confermati il 2026-10-05):** (a) la ricerca I10 si estende alle PR con `is:pr`, `is:draft`, `review:required` / `review:approved` / `review:changes-requested` e `author:@agents` (oltre a `assignee:@agents`), in UI, API e `gs`; (b) nella scheda *Files changed* ogni file ha una casella **Viewed**, personale per chi guarda, che lo richiude; (c) chiedono o richiedono di nuovo una review l'autore della PR e chi ha `write`; i revisori richiesti devono avere almeno `read` sul repo. |

## Fuori dalla v1.1

Fork (idea futura nel Dream Journal), rebase, applicazione dei suggerimenti e risoluzione dei conflitti dal browser, "Update branch", collegamento manuale delle issues, peso configurabile delle approvazioni degli agenti, protezioni su altri branch.

## Related topics

- [[knowledge/topics/issues]]
- [[knowledge/topics/repository-git]]
- [[knowledge/topics/browser-codice]]
- [[knowledge/topics/collegamenti-notifiche-webhook]]
- [[knowledge/topics/cli-gs-skills]]

