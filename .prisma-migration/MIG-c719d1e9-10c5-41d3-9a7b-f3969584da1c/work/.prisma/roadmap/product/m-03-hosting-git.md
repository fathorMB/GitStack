---
{"horizon":"next","id":"OBJ-bcb9bb89-a13a-4c0c-8a2f-e56f6dd76c38","knowledge":["DOC-8d539b83-ec08-41ca-a680-1529555fc42a","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd"],"schema_version":1,"title":"Hosting dei repository Git","updated":"2026-09-30T21:15:06.323554500+00:00"}
---

# Hosting dei repository Git

## Outcome

Si crea un repo da UI o API e ci si fa clone, push e pull via HTTPS (token) e SSH, con permessi e visibilità rispettati; ogni push pubblica un evento.

## Rationale

D9: è il cuore di un mini GitHub. Usa il binario `git` ufficiale lato server e un server SSH integrato; l'evento `git.push` è la base per collegamenti, notifiche e, più avanti, CI.

## Scope

Storage dei repo su volume persistente; repo con owner utente o organizzazione e visibilità privato/interno/pubblico; push/pull HTTPS con token e SSH con chiavi utente; controllo permessi su ogni operazione Git; hook post-receive → `git.push`; modello dati predisposto per le Pull Request (v1.1).

Priorità 3 di 9 nella v1. Mockup 03, 04, 06.

## Source references

- `.lmbrain-lite/milestones/M-03.md`

