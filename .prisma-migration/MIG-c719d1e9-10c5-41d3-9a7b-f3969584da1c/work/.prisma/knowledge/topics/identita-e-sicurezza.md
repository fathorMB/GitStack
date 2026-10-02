---
{"area":"requirements","id":"DOC-d1718775-770d-4065-8b27-3dc7a400dad6","related":[],"schema_version":1,"sources":[{"origin_path":".lmbrain-lite/milestones/M-02.md","source_id":"SRC-1acfaa85-6fd1-4f3e-8902-7631d5324e73"},{"origin_path":".lmbrain-lite/knowledge/decisions.md","source_id":"SRC-bfac41b1-d1b2-48e7-9691-90df6c7dcdc5"}],"tags":["identity","sicurezza","token","oidc","permessi"],"title":"Identità, autenticazione e permessi","updated":"2026-09-30T21:13:45.438588700+00:00"}
---

# Identità, autenticazione e permessi

## Context

Requisiti di M-02 (D5) e comportamento realizzato. Fonti nel repo: `services/identity/README.md`, `services/gateway/README.md`, `docs/identity-oidc.md`, `deploy/gitstack/README.md`.

## Confirmed decisions

**Confine gateway / identity (D4).** Identity fa l'autenticazione (chi sei) e conosce utenti, credenziali, appartenenze e grant; non è esposta fuori dal cluster. Il gateway verifica la credenziale chiedendo a identity e applica gli **scope** (autorizzazione grossolana). L'autorizzazione fine (ruolo su una risorsa o in un'organizzazione) spetta al servizio proprietario del dato, che chiede a identity `POST /internal/permissions/check`.

**Credenziali.**
- Password: argon2id (parametri OWASP), 12–1024 caratteri, diversa da username/email. Errore identico per utente inesistente, password errata o utente disattivato. Limite ai tentativi falliti per utente (5) e per IP (20) in 15 minuti → 429.
- Sessioni web: cookie opaco `gst_session` (HttpOnly, Secure, SameSite=Lax), durata assoluta 7 giorni; nel database solo l'hash.
- Token personali `gst_...`: opachi e revocabili, nome, scope, **scadenza obbligatoria** (massimo 365 giorni, configurabile), mostrati una sola volta; nel database solo lo SHA-256.
- Chiavi SSH per utente, fingerprint unico in tutto il sistema.
- Catalogo scope (deciso dal CTO, non si allarga senza di lui): `read:user`, `write:user`, `read:org`, `write:org`, `admin:org`, `read:resource`, `write:resource`. `write:*` include `read:*`; `admin:org` include `write:org`. Le sessioni non hanno scope: valgono i permessi dell'utente.

**Verifica nel gateway.** Chiamata a identity con cache breve (30 s per gli esiti positivi, mai oltre la scadenza; 5 s per i negativi); con identity irraggiungibile risponde 503, mai "fail open". Verso i servizi a valle l'identità viaggia in header firmati HMAC-SHA256 con il segreto di servizio (finestra 60 s); chi raggiunge core direttamente non può spacciarsi per un utente. Credenziali oltre 512 caratteri → 401.

**Compromesso accettato:** dopo una revoca il gateway può accettare la vecchia credenziale fino a 30 s; logout e cambio password svuotano subito la cache.

**Primo avvio.** Se non esiste un amministratore, identity crea `admin` con una password generata dal chart in un Secret (mai stampata). Finché non la cambia, ogni chiamata risponde 403 `password_change_required`, tranne sessione, logout e cambio della propria password.

**Login OIDC.** Provider configurati dall'amministratore via file (Secret), nessuna UI di configurazione. Authorization code flow con PKCE S256, state e nonce verificati. Collegamento all'utente: identità già nota → login; `linkByVerifiedEmail` solo con email dichiarata verificata; `autoCreateUsers` crea un utente senza password; altrimenti 409. Un'email non verificata non collega mai un utente esistente. Entra ID non manda `email_verified`: con Entra si usa `autoCreateUsers` o il collegamento manuale. Guide per Entra ID, Google e Keycloak in `docs/identity-oidc.md`.

**Organizzazioni e team.** Membri di team sempre membri dell'organizzazione; grant su risorse generiche a un utente **o** a un team (read/write/admin), senza vincolo di database verso lo schema `core`.

## Open questions

- Modello dei permessi fini sulle risorse (GIT-38) non ancora realizzato: `/internal/permissions/check` risponde 501. Vedi il topic di analisi sui permessi.
- Revoca immediata: possibile evento `identity.credential.revoked` sul bus per svuotare la cache del gateway; non pianificato.
- Nessuna API di amministrazione dei provider OIDC; cambiare i provider richiede il riavvio di identity.
- Limite ai login e riuso dello `state` OIDC valgono per replica, non per tutto il cluster.
- LDAP rimandato dopo la v1 (D5).

## Related topics

- [[knowledge/topics/contratto-api]]
- [[knowledge/topics/architettura]]

