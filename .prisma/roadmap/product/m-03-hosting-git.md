---
{"horizon":"next","id":"OBJ-bcb9bb89-a13a-4c0c-8a2f-e56f6dd76c38","knowledge":["DOC-8d539b83-ec08-41ca-a680-1529555fc42a","DOC-f8d2e48c-74fd-4b35-8551-80a2f345eacd","DOC-d1718775-770d-4065-8b27-3dc7a400dad6","DOC-6878277b-230e-448b-a761-215e9d233c54","DOC-a5cf81fd-1f8e-4080-9c73-24511fb29cf5"],"reopen_reason":"M-03 completata secondo il codice di main (GIT-65…GIT-77, GIT-99, GIT-100).","schema_version":1,"title":"Hosting dei repository Git (completata)","updated":"2026-10-05T19:53:06.225602900+00:00"}
---



# Hosting dei repository Git (completata)

## Outcome

Si crea un repo da UI o API e ci si fa clone, push e pull via HTTPS (token) e SSH, con permessi e visibilità rispettati; ogni push pubblica un evento.

**Stato: completata** secondo il codice di `main` al commit `1587133` (2026-10-05): API pubblica dei repo, push/pull HTTPS e SSH sulla 2222, regole al push R6/R9/R10, eliminazione recuperabile, evento `git.push`, test end-to-end con client git reale in CI e su VM. Dettagli in [[knowledge/topics/stato-di-realizzazione]].

## Rationale

D9: è il cuore di un mini GitHub. Usa il binario `git` ufficiale lato server e un server SSH integrato; l'evento `git.push` è la base per collegamenti, notifiche e, più avanti, CI.

## Scope

Storage dei repo su volume persistente; repo con owner utente o organizzazione e visibilità **privato/interno** (default privato, nessun accesso anonimo, anche per clone e pull; P2, P3, P7); push/pull HTTPS con token e SSH con chiavi utente; controllo permessi su ogni operazione Git secondo le regole di [[knowledge/topics/identita-e-sicurezza]]; hook post-receive → `git.push`; modello dati predisposto per le Pull Request (v1.1).

Regole di ciclo di vita in [[knowledge/topics/repository-git]] (R1–R12). Restano parziali solo le parti che dipendono da altre milestone: `gs repo create` (M-07), apertura della porta 2222 da parte dell'installer (M-08), issues in sola lettura sui repo archiviati (M-05).

Fuori dalla v1: rinomina e trasferimento, Git LFS, quote per organizzazione, regole di protezione complete.

Priorità 3 di 9 nella v1. Mockup 03, 04, 06, 07, 18, 19.

## Source references

- `.lmbrain-lite/milestones/M-03.md`
- `docs/rules-coverage.md` (famiglia R)
- Tema di analisi "Permessi fini sulle risorse e visibilità dei repo" (P2, P3, P7)
- Tema di analisi "Hosting Git: ciclo di vita e regole dei repository" (R1–R12)

