---
name: gitstack-issue-to-commit
description: Lavora su una issue di GitStack dall'apertura alla Pull Request (bozza, poi pronta per la review) con il commit che la chiude (fixes #n). Usala quando ti viene assegnata o chiesta una issue da implementare.
---

# GitStack: da una issue alla Pull Request

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

## 5. Apri la Pull Request in bozza

Dal tuo ramo apri una PR in bozza verso il ramo predefinito (`--head` è il ramo corrente, `--base` il branch principale). Serve il permesso write. Il testo porta `fixes #12`: la issue si chiude quando la PR viene unita, e la unisce una persona, non tu. In bozza la PR non si unisce e non chiede review.

```
gs pr create --draft --title "Correggi il crash all'avvio" --body-file pr.txt
```

Dove `pr.txt` (scritto con lo strumento di scrittura file) è, per esempio:

```
Il crash all'avvio era un puntatore non controllato in cmd/main.go.

fixes #12
```

Annota il numero della PR (`#7`) che `gs` stampa: serve per i passi seguenti.

## 6. Passa a «Ready for review» con gli stati verdi

Prima controlla che la PR sia davvero pronta: test del repo verdi, nessun conflitto, e lo stato dei controlli obbligatori (`statusState`) non in `failure`, `error` o `pending`.

```
gs pr view 7 --json draft,statusState,approvals,requiredApprovals,reviewDecision
gs api /repos/acme/web/pulls/7/mergeability
```

Quando è pronta, chiedi la review a chi ti ha affidato la issue (per esempio chi l'ha assegnata a te). Se non sai chi è, chiedilo con un commento sulla issue e lascia la PR in bozza: non passarla a pronta da solo.

```
gs pr ready 7 --reviewer alice
gs pr comment 7 --body "Pronta per la review: <cosa cambia in una riga>. Test: <quali>."
```

Se la review chiede modifiche, correggi sullo stesso ramo, committa con `fixes #12` e pusha: la PR si aggiorna da sola.

## 7. Se sei bloccato: `needs-human`

Se non puoi andare avanti (manca un'informazione, un permesso, un ambiente per i test), non indovinare. Aggiungi l'etichetta `needs-human` alla issue e scrivi cosa manca. La PR resta in bozza.

```
gs issue edit 12 --add-label needs-human
gs issue comment 12 --body-file blocco.txt
```

Con `blocco.txt` (con lo strumento di scrittura file) scrivi: cosa hai fatto, cosa ti manca, a chi lo chiedi.

## Senza permesso di scrittura

Se non puoi aprire la PR, scrivi sulla issue il nome del ramo, un riassunto e il comando per unirlo, e chiedi a una persona di aprirla:

```
gs issue comment 12 --body-file comment.txt
```

Con `comment.txt`:

```
Fatto nel ramo agent/12-crash-avvio; test verdi.
Riassunto: <cosa hai cambiato, in una o due righe>.

Per unire, dal ramo predefinito:
git fetch && git merge --ff-only origin/agent/12-crash-avvio && git push
```

Chiudi a mano una issue solo se non c'è un commit che la chiude (per esempio la issue non richiede codice):

```
gs issue close 12 --reason completed
gs issue close 12 --reason "not planned" --comment "fuori dal perimetro"
```

## Regole

- Mai push sul ramo predefinito: lavori su `agent/<n>-<slug>` e a unire è una persona.
- Non passare a pronta una PR con test rossi o senza un revisore, e non approvare mai la tua PR.
- Non unire né chiudere una PR (`gs pr merge`, `gs pr close`) senza un'istruzione esplicita di una persona, e non usare `--yes` per farlo.
- Ogni commit, commento e PR è firmato dal tuo account: scrivi cose vere e verificabili.
- Non usare `--yes` su eliminazioni o archiviazioni (`gs repo delete`, `gs repo archive`) senza un'istruzione esplicita di una persona.
- Se non puoi andare avanti, metti `needs-human` e scrivi cosa manca invece di indovinare.
