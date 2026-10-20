---
name: gitstack-ci
description: Segue workflow ed esecuzioni CI in GitStack e aiuta a correggere gli errori dai log. Usala quando devi avviare un workflow o verificare i controlli di una modifica.
---

# GitStack: ciclo della CI

## Elenca e avvia

```sh
gs workflow list --json id,name,path,state
gs workflow run build.yml --ref feature/mia-modifica --input go-version=1.26
```

Usa il nome o il percorso del workflow restituito da `gs workflow list`. Fornisci il ref e gli input richiesti dal workflow.

## Aspetta la CI

```sh
gs run list --json id,workflowId,status,conclusion,ref,sha
gs run watch <id> --exit-status
```

`gs run watch` interroga l'esecuzione fino al completamento. Se fallisce, correggi dai log:

```sh
gs run view <id> --log-failed
```

Leggi i log dei job falliti, correggi la causa, crea un nuovo commit e controlla la nuova esecuzione con `gs run watch`. Per rieseguire lo stesso run usa `gs run rerun <id>`; per fermarlo usa `gs run cancel <id>`.

`gs run cancel` chiede conferma (G8), perché annullare un'esecuzione fa perdere il lavoro in corso. Senza terminale e senza `--yes` esce con 2 e non annulla nulla. Usa `--yes` solo se una persona te lo chiede esplicitamente per quella esecuzione in questa conversazione; altrimenti fermati e chiedi.

## Output per agenti

I comandi supportano `--json` e `--jq`. Gli errori API mantengono i codici G3: 4 per autenticazione, 5 per permessi e 6 per risorsa non trovata; uso errato restituisce 2. `gs run watch --exit-status` restituisce 1 quando la conclusione non è `success`.

Non usare `--yes` su operazioni di eliminazione o archiviazione senza un'istruzione esplicita di una persona (G8).
