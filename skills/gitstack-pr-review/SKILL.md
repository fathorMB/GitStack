---
name: gitstack-pr-review
description: Rivede una Pull Request di GitStack: legge descrizione, commit e diff, controlla che si possa unire, scrive una review con commenti sulle righe. Usala quando ti chiedono di rivedere una PR.
---

# GitStack: review di una Pull Request

Serve almeno il permesso read sul repo per leggere la PR e commentare. Le Pull Request si leggono e si scrivono con `gs api` (percorsi relativi a `/api/v1`); se hai il server MCP di GitStack (`gs mcp`) usa gli strumenti `pr_view`, `pr_files`, `review_create` e simili, che fanno le stesse chiamate.

## 1. Leggi la PR

```
gs api /repos/acme/web/pulls/7
gs api /repos/acme/web/pulls/7/commits --jq '.[].message'
gs api /repos/acme/web/pulls/7/files --paginate
```

Titolo, descrizione, branch di origine e destinazione, stato (bozza, aperta, chiusa) e issue collegate (`fixes #n`) stanno nella prima risposta. Una PR chiusa o in bozza: chiedi prima se rivederla.

## 2. Controlla lo stato di unione

```
gs api /repos/acme/web/pulls/7/mergeability
gs api /repos/acme/web/pulls/7/reviews
gs api /repos/acme/web/pulls/7/threads
```

Annota conflitti, regole del branch non soddisfatte, review già inviate e conversazioni ancora aperte: non ripetere quello che c'è già.

## 3. Rivedi il diff

Leggi ogni file modificato (`/files`) e, dove serve il contesto, il file intero al branch di origine:

```
gs api "/repos/acme/web/contents?path=cmd/main.go&ref=agent/12-crash-avvio"
```

Cerca errori di logica, casi limite, test mancanti, cambi di comportamento non dichiarati nella descrizione. Verifica con i test del repo quando puoi, senza modificare il branch della PR.

## 4. Scrivi la review

Una sola review con i commenti sulle righe (`side` è `right` per le righe nuove, `left` per quelle tolte), scritta in un file JSON con lo strumento di scrittura file, non con un heredoc:

```
gs api /repos/acme/web/pulls/7/reviews --input review.json
```

Dove `review.json` è, per esempio:

```
{
  "event": "request_changes",
  "body": "Due problemi da sistemare prima di unire.",
  "comments": [
    {"path": "cmd/main.go", "side": "right", "line": 42, "body": "Qui `cfg` può essere nil."}
  ]
}
```

`event` è `comment`, `approve` o `request_changes`. Usa `comment` quando hai solo osservazioni e `request_changes` per un problema che blocca. Non inviare `approve` senza una richiesta esplicita di una persona: un'approvazione conta solo se viene da chi ha il permesso write e se ne assume la responsabilità. I commenti fuori da una review stanno in `/pulls/7/comments`.

## Regole

- Non unire la PR e non cancellarne il branch: a unire è una persona con permesso di scrittura.
- Non fare push sul branch della PR e non modificare il codice che stai rivedendo: scrivi cosa cambiare in un commento.
- Ogni commento è firmato dal tuo account: scrivi cose vere e verificabili, con il file e la riga.
- Non usare `--yes` su eliminazioni o archiviazioni (`gs repo delete`, `gs repo archive`) senza un'istruzione esplicita di una persona.
- Se non puoi andare avanti (permessi, PR illeggibile), scrivi un commento sulla PR con quello che manca invece di indovinare.
