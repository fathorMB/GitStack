---
name: gitstack-pr-review
description: Rivede una Pull Request di GitStack: legge descrizione, commit e diff, controlla che si possa unire, scrive una review con commenti sulle righe. Usala quando ti chiedono di rivedere una PR.
---

# GitStack: review di una Pull Request

Serve almeno il permesso read sul repo per leggere la PR e commentare; `--approve` e `--request-changes` richiedono write. Le PR si leggono e si scrivono con `gs pr` (`gs pr --help`). Per i commenti sulle singole righe e per le API che `gs pr` non copre si usa `gs api` (percorsi relativi a `/api/v1`). Se hai il server MCP di GitStack (`gs mcp`) gli strumenti `pr_view`, `pr_files`, `review_create` fanno le stesse chiamate.

## 1. Leggi la PR

```
gs pr view 7
gs pr view 7 --json title,state,draft,sourceBranch,targetBranch,closingIssues
gs pr diff 7 --name-only
```

Titolo, descrizione, branch di origine e destinazione, stato (bozza, aperta, chiusa, unita) e issue collegate (`fixes #n`) stanno nella prima risposta. Una PR chiusa o in bozza: chiedi prima se rivederla.

## 2. Controlla lo stato di unione

```
gs api /repos/acme/web/pulls/7/mergeability
gs api /repos/acme/web/pulls/7/reviews
gs api /repos/acme/web/pulls/7/threads
```

Annota conflitti, regole del branch non soddisfatte, review già inviate e conversazioni ancora aperte: non ripetere quello che c'è già.

## 3. Rivedi il diff

Leggi il diff della PR (`gs pr diff 7`, oppure `gs pr diff 7 --name-only` per l'elenco dei file) e, dove serve il contesto, il file intero al branch di origine:

```
gs api "/repos/acme/web/contents?path=cmd/main.go&ref=agent/12-crash-avvio"
```

Per provare i test senza toccare il branch puoi passare al ramo della PR in locale con `gs pr checkout 7` e lanciare i test del repo: non fare commit su quel ramo.

Cerca errori di logica, casi limite, test mancanti, cambi di comportamento non dichiarati nella descrizione. Verifica con i test del repo quando puoi, senza modificare il branch della PR.

## 4. Scrivi la review

Una sola review. Per una review con solo un testo generale usa `gs pr review` con l'esito (`--comment`, `--approve` o `--request-changes`) e il testo in un file, scritto con lo strumento di scrittura file, non con un heredoc:

```
gs pr review 7 --request-changes -F review.md
```

Per i commenti sulle righe la review va in JSON con `gs api`. `side` è `new` per le righe nuove e `old` per quelle tolte; `line` è il numero della riga in quel lato:

```
gs api /repos/acme/web/pulls/7/reviews --input review.json
```

Dove `review.json` è, per esempio:

```
{
  "event": "request_changes",
  "body": "Due problemi da sistemare prima di unire.",
  "comments": [
    {"path": "cmd/main.go", "side": "new", "line": 42, "body": "Qui `cfg` può essere nil."}
  ]
}
```

`event` è `comment`, `approve` o `request_changes`. Usa `comment` quando hai solo osservazioni e `request_changes` per un problema che blocca; `request_changes` vuole un testo generale o almeno un commento. Non inviare `approve` senza una richiesta esplicita di una persona: un'approvazione conta solo se viene da chi ha il permesso write e se ne assume la responsabilità. Un agente può approvare, ma la sua approvazione non conta per il merge.

Per un commento generale fuori da una review: `gs pr comment 7 --body-file commento.md`.

## Regole

- Non unire la PR e non cancellarne il branch: a unire è una persona con permesso di scrittura (`gs pr merge` non è per te).
- Non fare push sul branch della PR e non modificare il codice che stai rivedendo: scrivi cosa cambiare in un commento.
- Ogni commento è firmato dal tuo account: scrivi cose vere e verificabili, con il file e la riga.
- Non usare `--yes` su eliminazioni o archiviazioni (`gs repo delete`, `gs repo archive`) senza un'istruzione esplicita di una persona.
- Se non puoi andare avanti (permessi, PR illeggibile), scrivi un commento sulla PR con quello che manca invece di indovinare.
