---
{"area":"requirements","id":"DOC-04fbc589-a144-403b-99e0-13e541fac16a","related":["TOP-f4845d97-2a90-48c2-9a81-93e78e48c537","DOC-6878277b-230e-448b-a761-215e9d233c54","DOC-1565db4b-4108-490d-aca1-28d30c36ad99","OBJ-affcf73c-61dd-4152-b847-5ca7d47d0db2"],"schema_version":1,"sources":[],"tags":["browser","markdown","diff","sicurezza","m-04"],"title":"Browser del codice: regole di prodotto","updated":"2026-10-05T09:30:00+00:00"}
---

# Browser del codice: regole di prodotto

## Context

Regole per M-04 confermate dall'operatore il 2026-10-05 nel tema di analisi "Browser del codice: regole di prodotto". Completano D9 ([[knowledge/topics/decisioni]]) e le regole dei repo di [[knowledge/topics/repository-git]] (R1, R4, R6, R8). Il motore Markdown (B2) vale anche per [[knowledge/topics/issues]].

## Confirmed decisions

| # | Regola |
|---|---|
| B1 | **Limiti di visualizzazione:** testo fino a 1 MB con evidenziazione, 1–5 MB testo semplice, oltre 5 MB solo Download. Immagini comuni mostrate come immagini; SVG solo come immagine, mai come pagina. Altri binari: dimensione e Download. Stessi limiti nell'API, con indicazione di troncamento. |
| B2 | **Markdown in stile GitHub, protetto, unico per il prodotto:** tabelle, liste di controllo, codice colorato, link automatici, riferimenti `#n`, `owner/repo#n`, `@utente`. HTML solo sicuro (`<details>`, `<summary>`, `<sub>`, `<sup>`, `<img>`…); script, iframe, stili e attributi `on…` rimossi. Link e immagini relativi risolti nel repo; link esterni in nuova scheda senza provenienza. Mermaid e formule dopo la v1. |
| B3 | **Raw e archivi con login o token:** `/<owner>/<repo>/raw/<ref>/<percorso>`; ZIP e tar.gz di branch, tag e commit; sessione o token con `read`. Raw servito come testo o file da scaricare, mai come pagina eseguibile. Nessun link temporaneo anonimo. |
| B4 | **Storico per file e blame:** pulsanti History e Blame nella vista file; blame riga per riga con commit, autore (badge "agent") e data, fino a 1 MB; disponibili via API e `gs`. |
| B5 | **Ricerca:** "Go to file" con corrispondenza approssimata; "Search code" testuale nel singolo repo, sul ref scelto, massimo 100 risultati, senza indice e con tempo massimo; via API e `gs`. |
| B6 | **Diff con limiti progressivi:** file oltre 500 righe cambiate chiusi ("Load diff"); file generati e di lock sempre chiusi; oltre 300 file o 20.000 righe solo elenco e download `.diff`/`.patch`. Vista unificata o affiancata, "ignora spazi". Stessi limiti nell'API. |
| B7 | **Solo tag nella v1:** pagina Tags con data, commit, messaggio dei tag annotati e download. Release (note e file allegati) in v2 con la CI. |

## Fuori dalla v1

Mermaid e formule (B2), link temporanei senza login (B3), ricerca indicizzata su tutta l'installazione (B5), release (B7, previste in v2).

## Related topics

- [[knowledge/topics/repository-git]]
- [[knowledge/topics/issues]]
- [[knowledge/topics/decisioni]]
