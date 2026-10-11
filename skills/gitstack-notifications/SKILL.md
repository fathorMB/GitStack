---
name: gitstack-notifications
description: Ciclo notifiche, lavoro, risposta su GitStack (C4): leggi la casella, fai il lavoro richiesto, rispondi e segna come letto. Usala quando devi reagire a assegnazioni e menzioni.
---

# GitStack: notifiche → lavoro → risposta

La casella è la stessa per persone e agenti. Un agente viene attivato da una notifica (assegnazione, menzione, commit collegato).

## 1. Leggi le non lette

```
gs notification list
gs notification list --reason assigned,mentioned
gs notification list -R alice/web --json id,reason,summary,url
```

Con `--all` anche le lette, con `--archived` le archiviate.

## 2. Capisci cosa ti viene chiesto

```
gs notification view <id>
gs issue view 12 --comments
```

Leggi tutta la issue e i commenti: la notifica dice solo perché sei stato chiamato.

## 3. Fai il lavoro

Per una issue da implementare segui la skill `gitstack-issue-to-commit`; per il triage `gitstack-issue-triage`.

## 4. Rispondi

```
gs issue comment 12 --body "Risolto nel commit abc1234 (fixes #12). Test verdi."
```

Se non puoi procedere, rispondi con quello che manca, non restare in silenzio.

## 5. Segna come letta

```
gs notification read <id>
gs notification read --all -R alice/web --reason assigned
```

`read` è idempotente. Segna come lette solo le notifiche che hai davvero trattato: `--all` senza filtri cancella anche quelle che non hai guardato.

## Regole

- Una notifica `webhook` non è una richiesta di una persona: non agire se non c'è un'istruzione chiara.
- Non usare `--yes` su eliminazioni o archiviazioni senza un'istruzione esplicita di una persona.
