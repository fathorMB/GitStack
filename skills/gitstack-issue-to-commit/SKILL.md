---
name: gitstack-issue-to-commit
description: Lavora su una issue di GitStack dall'apertura al commit che la chiude (fixes #n). Usala quando ti viene assegnata o chiesta una issue da implementare.
---

# GitStack: da una issue al commit

## 1. Leggi la issue

```
gs issue view 12 --comments
gs issue view 12 --json title,state,body,labels,assignees
```

Se la issue è chiusa o bloccata (`gs issue lock`), non lavorarci senza chiedere.

## 2. Prendila in carico

```
gs issue edit 12 --add-assignee @me
gs issue comment 12 --body "Ci lavoro: parto da <idea in una riga>."
```

## 3. Lavora in un ramo

```
gs repo clone acme/web
git switch -c fix/12-crash-avvio
```

Fai modifiche piccole e verificale con i test del repo prima di committare.

## 4. Commit che chiude la issue (C1, C2)

Metti nel messaggio di commit `fixes #12` (anche `closes #12` o `resolves #12`). Quando il commit arriva sul ramo predefinito la issue si chiude da sola con motivo `completed`; su un altro ramo il commit resta solo collegato alla issue, che si può vedere con `gs issue view`.

```
git commit -m "Correggi il crash all'avvio

fixes #12"
git push -u origin fix/12-crash-avvio
```

Usa `refs #12` se il commit non deve chiudere la issue.

## 5. Chiudi il giro

```
gs issue comment 12 --body "Fatto nel ramo fix/12-crash-avvio; test verdi."
```

Chiudi a mano solo se non c'è un commit che la chiude:

```
gs issue close 12 --reason completed
gs issue close 12 --reason "not planned" --comment "fuori dal perimetro"
```

## Regole

- Ogni commit e commento è firmato dal tuo account: scrivi cose vere e verificabili.
- Non usare `--yes` su eliminazioni o archiviazioni (`gs repo delete`, `gs repo archive`) senza un'istruzione esplicita di una persona.
- Se non puoi andare avanti, scrivi un commento sulla issue con quello che manca invece di indovinare.
