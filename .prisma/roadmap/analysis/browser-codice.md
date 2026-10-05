---
{"depends_on":["TOP-74180f5f-ae9e-4bbf-b220-4b52950e5038"],"id":"TOP-f4845d97-2a90-48c2-9a81-93e78e48c537","knowledge":["DOC-6878277b-230e-448b-a761-215e9d233c54","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-ef8572eb-11a9-4ab0-ac04-ff006b41bdc4","DOC-04fbc589-a144-403b-99e0-13e541fac16a"],"schema_version":1,"state":"consolidated","title":"Browser del codice: regole di prodotto","updated":"2026-10-05T09:30:00+00:00"}
---

# Browser del codice: regole di prodotto

## Expected learning

Regole per M-04 oltre a "come GitHub" (D9) e ai mockup 07–10: cosa si mostra e cosa no, come si rende il Markdown in sicurezza, download, storico per file e ricerca.

## Già deciso (da altri temi)

- D9: file, README, branch, tag, storico commit e diff; API di lettura per gli agenti (D7).
- Indirizzi `/<owner>/<repo>` (R1), branch principale mostrato di default (R4), file oltre 100 MB rifiutati al push (R6), nessun accesso anonimo (P2), Git LFS dopo la v1 (R8).

## Scelte confermate dall'operatore (2026-10-05)

- **B1 — Limiti di visualizzazione con alternativa Download:** testo fino a 1 MB con evidenziazione della sintassi; tra 1 e 5 MB testo semplice; oltre 5 MB solo "File troppo grande da mostrare" e Download. Immagini comuni (PNG, JPG, GIF, WebP, SVG) mostrate come immagini; gli SVG solo come immagine, mai come pagina (possono contenere script). Altri binari: dimensione e Download. Stessi limiti nell'API, che indica sempre se il contenuto è troncato.
- **B2 — Markdown in stile GitHub con protezione rigorosa (un solo motore per tutto il prodotto):** tabelle, liste di controllo, blocchi di codice colorati, link automatici, riferimenti `#n`, `owner/repo#n` e `@utente` (C1, I8). HTML solo in forma sicura (es. `<details>`, `<summary>`, `<sub>`, `<sup>`, `<img>`); script, iframe, stili e attributi `on…` sempre rimossi. Link e immagini relativi risolti dentro il repo (con login); link esterni in nuova scheda senza informazioni di provenienza. Vale per README, file `.md`, issues e commenti. Mermaid e formule matematiche dopo la v1.
- **B3 — Raw e archivi, sempre con login o token:** ogni file ha un indirizzo raw `/<owner>/<repo>/raw/<branch|tag|commit>/<percorso>`; ogni branch, tag o commit si scarica come ZIP o tar.gz. Serve sempre una sessione o un token con permesso `read` (P2), così funzionano anche CI e script. Il contenuto raw è servito come testo o file da scaricare, mai come pagina eseguibile. Link temporanei senza login esclusi dalla v1.
- **B4 — Storico per file e blame nella v1:** nella vista file (mockup 08) pulsanti History e Blame. History riusa la pagina dei commit (mockup 09) filtrata sul file; Blame mostra riga per riga commit, autore (con badge "agent") e data, per file fino a 1 MB (B1). Entrambi disponibili via API e `gs`.
- **B5 — "Go to file" e ricerca testuale nel singolo repo:** "Go to file" trova i file per nome con corrispondenza approssimata; "Search code" cerca un testo nei file del repo sul branch o tag scelto, al massimo 100 risultati con riga e link, eseguita al momento (nessun indice) con tempo massimo. Disponibile via API e `gs`. Ricerca indicizzata su tutta l'installazione esclusa dalla v1.
- **B6 — Diff con limiti progressivi:** file con più di 500 righe cambiate chiusi di default ("Load diff"); file generati e di lock (es. `package-lock.json`, `go.sum`, `*.min.js`) sempre chiusi di default; oltre 300 file o 20.000 righe per commit solo elenco dei file con righe aggiunte/tolte e diff completo scaricabile come `.diff` / `.patch`. Vista unificata o affiancata e opzione "ignora spazi" (base per le PR della v1.1). Stessi limiti nell'API, con indicazione di cosa è troncato.
- **B7 — Solo tag nella v1, release in v2:** pagina Tags con data, commit, messaggio dei tag annotati e download ZIP/tar.gz (B3). Le release (note di versione e file allegati) arrivano in v2 insieme alla CI che produce i file.

## Open questions

1. ~~Limiti di visualizzazione~~ (B1).
2. ~~Markdown~~ (B2).
3. ~~Download~~ (B3).
4. ~~Storico per file e blame~~ (B4).
5. ~~Ricerca~~ (B5).
6. ~~Diff grandi~~ (B6).
7. ~~Release~~ (B7, in v2).

## Consolidation summary

Consolidato il 2026-10-05 con conferma dell'operatore. Regole B1–B7: limiti di visualizzazione (1 MB colorato, 5 MB testo, poi Download; SVG mai come pagina); Markdown in stile GitHub protetto e unico per il prodotto; raw e archivi solo con login o token; storico per file e blame; "Go to file" e ricerca testuale nel singolo repo; diff con limiti progressivi, vista affiancata e "ignora spazi"; solo tag nella v1, release in v2. Riportato in [[knowledge/topics/browser-codice]], negli obiettivi M-04 e v2. Mockup 07, 08, 10 da aggiornare e schermate Blame e Tags da aggiungere.
