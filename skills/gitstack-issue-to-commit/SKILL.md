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

## 3. Lavora in un ramo `agent/<n>-<slug>`

Lavori sempre su un ramo `agent/<n>-<slug>` (`<n>` è il numero della issue, `<slug>` due o tre parole). Non fare mai push sul ramo predefinito: a unire il ramo è una persona con permesso di scrittura.

```
gs repo clone acme/web
cd web
git switch -c agent/12-crash-avvio
```

Prima del primo commit imposta l'identità git **di questo repo** (config locale, mai `--global`): il commit deve risultare del tuo account, non della persona il cui PC stai usando.

```
git config user.name "<il tuo username GitStack>"
git config user.email "<l'email del tuo account GitStack>"
git log -1 --format='%an <%ae>'     # dopo il primo commit: deve essere la tua identità
```

L'email è quella dell'account agente: GitStack collega ai commit l'utente che ha quell'email (la dà la persona che ha creato l'account, `scripts/agent-trial/setup.sh` la stampa). Se non la conosci, chiedila con un commento sulla issue: non usare l'identità globale del computer.

Fai modifiche piccole e verificale con i test del repo prima di committare.

## 4. Commit che chiude la issue (C1, C2)

Metti nel messaggio di commit `fixes #12` (anche `closes #12` o `resolves #12`). Scrivi il messaggio in un file e usa `-F`: funziona uguale in bash e in PowerShell, mentre `git commit -m "..."` con virgolette e righe multiple si rompe in PowerShell.

```
git commit -F msg.txt
git push -u origin agent/12-crash-avvio
```

Dove `msg.txt` (scritto con lo strumento di scrittura file, non con un heredoc) è, per esempio:

```
Correggi il crash all'avvio

fixes #12
```

Non committare `msg.txt` (`git add` solo dei file della modifica, o lascialo fuori dal repo). Usa `refs #12` se il commit non deve chiudere la issue.

## 5. Chiudi il giro: commento con ramo e comando di unione

Sul ramo il commit resta solo collegato alla issue. Non chiudere la issue e non unire tu: la issue si chiude quando `fixes #12` arriva sul ramo predefinito, e questo lo fa la persona. Scrivi sulla issue il nome del ramo, un riassunto e il comando di unione:

```
gs issue comment 12 --body-file comment.txt
```

con `comment.txt` (scrivilo con un file, come per il commit):

```
Fatto nel ramo agent/12-crash-avvio; test verdi.
Riassunto: <cosa hai cambiato, in una o due righe>.

Per unire, dal ramo predefinito:
git fetch && git merge --ff-only origin/agent/12-crash-avvio && git push
```

Chiudi a mano solo se non c'è un commit che la chiude (per esempio la issue non richiede codice):

```
gs issue close 12 --reason completed
gs issue close 12 --reason "not planned" --comment "fuori dal perimetro"
```

## Regole

- Mai push sul ramo predefinito: lavori su `agent/<n>-<slug>` e a unire è una persona.
- Ogni commit e commento è firmato dal tuo account: scrivi cose vere e verificabili.
- Non usare `--yes` su eliminazioni o archiviazioni (`gs repo delete`, `gs repo archive`) senza un'istruzione esplicita di una persona.
- Se non puoi andare avanti, scrivi un commento sulla issue con quello che manca invece di indovinare.
