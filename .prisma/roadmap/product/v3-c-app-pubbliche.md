---
{"horizon":"now","id":"OBJ-a9706e40-6490-451b-a667-475fd17886f5","knowledge":["DOC-3e900450-a39c-42b0-a7e1-d6108b0f4363","DOC-4a4d6270-cd1a-4748-a1ad-9488cee4ad9e"],"reopen_reason":"Orizzonte now dal 2026-10-26.","schema_version":1,"title":"v3-c — App pubbliche e domini propri","updated":"2026-10-25T23:32:19.377276400+00:00"}
---



# v3-c — App pubbliche e domini propri

## Outcome

Un'app può essere pubblicata su internet con il proprio dominio e un certificato automatico, solo con il doppio consenso dell'amministratore dell'installazione e di chi ha `admin` sull'app.

## Rationale

H1 e H2: completa la promessa «cloud hosting» per siti e API rivolti all'esterno, mantenendo «interno di default» come per i repo. Orizzonte now dal 2026-10-26; Let's Encrypt evita anche ai tester di retail.it di installare la CA interna.

## Scope

Interruttore di istanza per le app pubbliche e scelta per app (H1); domini propri con controllo del DNS; Let's Encrypt automatico via Traefik (porta 80 raggiungibile) o certificato del cliente (H2).

Terzo dei tre passi della v3, in parallelo agli altri.

## Source references

- Tema di analisi "v3 — Hosting di app e container: regole di prodotto" (H1, H2)

