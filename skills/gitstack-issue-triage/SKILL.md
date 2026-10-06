---
name: gitstack-issue-triage
description: Fa il triage delle issue di un repo GitStack: cerca, classifica, assegna, commenta, chiude duplicati. Usala quando ti chiedono di mettere ordine nelle issue aperte.
---

# GitStack: triage delle issue

Serve il permesso write sul repo per etichette, assegnatari e milestone.

## Trovare le issue da guardare

```
gs issue list
gs issue list --assignee none --label bug
gs issue list --search "is:open no:assignee crash"
gs issue list --state all --author alice --json number,title,labels
```

La ricerca usa la stessa sintassi di UI e API (`is:`, `reason:`, `label:`, `assignee:`, `author:`, `milestone:`, `no:`, testo libero). Per cercare in più repo: `gs search issues "<query>"`.

## Leggere

```
gs issue view 12 --comments
```

## Classificare e assegnare

```
gs issue edit 12 --add-label bug --add-assignee bob
gs issue edit 12 --milestone v1
gs issue comment 12 --body "Riproducibile su main; serve il log del gateway."
```

`@me` è il tuo utente, `@agents` indica gli agenti. Assegna a una persona solo se lo ha chiesto lei o se il repo ha una regola scritta.

## Duplicati e chiusure

```
gs issue close 12 --reason duplicate --duplicate-of 7
gs issue close 13 --reason "not planned" --comment "Fuori dal perimetro di v1."
gs issue reopen 13
```

Prima di chiudere come duplicato apri la issue originale e controlla che sia davvero la stessa. Lascia sempre un commento col motivo.

## Discussioni accese

```
gs issue lock 14
gs issue unlock 14
```

Blocca solo se una persona te lo chiede o se il repo ha una regola.

## Regole

- Esci con lo stato che hai trovato se non sei sicuro: meglio un commento con una domanda che una chiusura sbagliata.
- Non usare `--yes` su eliminazioni o archiviazioni senza un'istruzione esplicita di una persona.
