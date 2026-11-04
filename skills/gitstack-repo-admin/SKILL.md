---
name: gitstack-repo-admin
description: Crea, elenca, modifica, archivia, elimina e recupera repository GitStack con gs. Usala per la gestione dei repo; archiviare ed eliminare richiedono l'istruzione esplicita di una persona.
---

# GitStack: gestione dei repo

## Vedere e clonare

```
gs repo list
gs repo list acme
gs repo view acme/web
gs repo clone acme/web
```

## Creare

Il repo è privato di default; il proprietario sei tu, oppure l'organizzazione in `<org>/<nome>`.

```
gs repo create my-app
gs repo create acme/web --internal -d "Il sito"
gs repo create tool --add-readme --gitignore go --license mit
```

`--internal` lo apre a tutti gli utenti dell'installazione. Non esiste accesso anonimo.

## Modificare

```
gs repo edit acme/web --description "Nuova descrizione"
```

## Archiviare ed eliminare

```
gs repo archive acme/web
gs repo unarchive acme/web
gs repo delete acme/web
```

- `archive`: il repo resta leggibile e clonabile, ma push, issue e commenti sono rifiutati. `unarchive` lo riattiva.
- `delete`: è definitivo e immediato, senza cestino e senza `restore`: spariscono codice, issue, PR, release, immagini, app e volumi, e il nome torna libero. Si recupera solo da un backup (`gitstack restore`, a cura dell'amministratore dell'installazione).
- Servono i permessi di admin sul repo.

### Divieto di `--yes` (G8)

`gs repo delete` e `gs repo archive` chiedono di riscrivere `owner/repo`. **Un agente non usa mai `--yes` su queste operazioni senza un'istruzione esplicita di una persona** che nomina quel repo, in questa conversazione. Senza terminale e senza `--yes` il comando esce con 2 e non fa nulla: non è un errore da aggirare, è la richiesta di una conferma umana. Se non hai l'istruzione, fermati e chiedila.

## Il comando generico

Per quello che non ha un comando dedicato:

```
gs api /repos/acme/web --jq .fullName
```
