---
{"horizon":"next","id":"OBJ-bcb9bb89-a13a-4c0c-8a2f-e56f6dd76c38","knowledge":["DOC-8d539b83-ec08-41ca-a680-1529555fc42a","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-d1718775-770d-4065-8b27-3dc7a400dad6","DOC-6878277b-230e-448b-a761-215e9d233c54"],"reopen_reason":"Regole R1–R12 su ciclo di vita e regole dei repo (tema consolidato il 2026-10-05).","schema_version":1,"title":"Hosting dei repository Git","updated":"2026-10-05T07:52:00+00:00"}
---


# Hosting dei repository Git

## Outcome

Si crea un repo da UI o API e ci si fa clone, push e pull via HTTPS (token) e SSH, con permessi e visibilità rispettati; ogni push pubblica un evento.

## Rationale

D9: è il cuore di un mini GitHub. Usa il binario `git` ufficiale lato server e un server SSH integrato; l'evento `git.push` è la base per collegamenti, notifiche e, più avanti, CI.

## Scope

Storage dei repo su volume persistente; repo con owner utente o organizzazione e visibilità **privato/interno** (default privato, nessun accesso anonimo, anche per clone e pull; P2, P3, P7); push/pull HTTPS con token e SSH con chiavi utente; controllo permessi su ogni operazione Git secondo le regole di [[knowledge/topics/identita-e-sicurezza]]; hook post-receive → `git.push`; modello dati predisposto per le Pull Request (v1.1).

Regole di ciclo di vita in [[knowledge/topics/repository-git]] (R1–R12): indirizzi `owner/repo` con spazio di nomi unico e nomi riservati; nomi dei repo minuscoli; contenuto iniziale opzionale; branch principale `main` modificabile e protetto; limite 100 MB per file; SSH sulla porta 2222 configurabile; eliminazione recuperabile 7 giorni; archiviazione in sola lettura.

Fuori dalla v1: rinomina e trasferimento, Git LFS, quote per organizzazione, regole di protezione complete.

Priorità 3 di 9 nella v1. Mockup 03, 04, 06, 07 e una nuova schermata "Settings · General" del repo.

## Source references

- `.lmbrain-lite/milestones/M-03.md`
- Tema di analisi "Permessi fini sulle risorse e visibilità dei repo" (P2, P3, P7)
- Tema di analisi "Hosting Git: ciclo di vita e regole dei repository" (R1–R12)

