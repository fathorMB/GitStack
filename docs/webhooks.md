# Webhook di GitStack: formato dei payload v1

Un webhook (M-06, regola C6) è un indirizzo HTTP(S) a cui GitStack manda un
`POST` ogni volta che succede un evento scelto. Si crea su un repo (serve
`admin` sul repo) o su un'organizzazione (serve il ruolo `owner`; riceve gli
eventi di tutti i repo dell'organizzazione). Le operazioni sono nel contratto
(`api/openapi.yaml`, tag `webhooks`): `…/hooks`, `…/hooks/{hookId}`,
`…/hooks/{hookId}/reactivate`, `…/hooks/{hookId}/deliveries`,
`…/deliveries/{deliveryId}` e `…/deliveries/{deliveryId}/redeliver`, sia sotto
`/repos/{owner}/{repo}` sia sotto `/orgs/{org}`.

Il formato è **GitStack**, ispirato a quello di GitHub ma non compatibile
(decisione C6): chi ha già un ricevitore per GitHub deve adattarlo. È
versionato: la versione corrente è la **1**. Un cambio incompatibile porta
alla 2 e alle intestazioni/payload della 1 non si tocca; le aggiunte di campi
sono compatibili, quindi un ricevitore ignora i campi che non conosce.

## Eventi selezionabili

| Evento (`X-GitStack-Event`) | Quando | Valori di `action` |
|---|---|---|
| `push` | dopo ogni push accettato, un messaggio **per ref** (branch o tag) | nessuna |
| `issues` | cambia una issue | `opened`, `edited`, `closed`, `reopened`, `assigned`, `unassigned`, `labeled`, `unlabeled`, `milestoned`, `demilestoned`, `locked`, `unlocked` |
| `issue_comment` | cambia un commento di una issue | `created`, `edited`, `deleted` |
| `repository` | cambia un repo | `created`, `deleted`, `restored`, `archived`, `unarchived`, `visibility_changed` |

Nessun webhook per una issue nascosta (I4) né per i suoi commenti. Nella v1
non c'è l'evento `ping`: si prova un webhook con una consegna vera e si
rinvia dal log (`redeliver`).

## Intestazioni

| Intestazione | Valore |
|---|---|
| `Content-Type` | `application/json` |
| `User-Agent` | `GitStack-Webhook/1` |
| `X-GitStack-Event` | l'evento: `push`, `issues`, `issue_comment` o `repository` |
| `X-GitStack-Delivery` | uuid della consegna: è l'`id` nel log (`…/deliveries`); una *Redeliver* ne ha uno nuovo |
| `X-GitStack-Payload-Version` | `1` |
| `X-GitStack-Signature` | `sha256=<hex>`, solo se il webhook ha un segreto |

### Firma

`X-GitStack-Signature` è `sha256=` seguito dall'HMAC-SHA256, in esadecimale
minuscolo, dei **byte esatti del corpo** della richiesta, con il segreto del
webhook come chiave. Chi riceve ricalcola l'HMAC sul corpo grezzo (prima di
ogni parsing JSON) e confronta in tempo costante.

```sh
printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex
```

Esempio: con segreto `s3cr3t-di-prova` e corpo
`{"version":1,"event":"issues","action":"closed"}` la firma è

```
X-GitStack-Signature: sha256=445f7173dfa8f83e67455aa9b74776b5eee2fc18442148d3de09fafad8bfce56
```

Senza segreto la consegna non è firmata (come GitHub) e l'intestazione manca:
chi espone un indirizzo raggiungibile da fuori dovrebbe sempre impostarlo. Il
segreto si imposta alla creazione o con `PATCH` (`secret`, stringa vuota per
toglierlo) e **non torna mai** nelle risposte, che hanno solo `hasSecret`. Lato
server è cifrato (README di `services/core`, sezione webhook).

## Consegna, tentativi e disattivazione (C7, C8)

- Risposta `2xx` entro **10 secondi** = consegnato. Ogni altra risposta,
  errore di rete o timeout è un fallimento; `5xx`, errore di rete e timeout si
  ritentano.
- Fino a **8 tentativi in circa 24 ore**, con attesa crescente fra l'uno e
  l'altro: subito, poi dopo 1 min, 5 min, 30 min, 2 h, 4 h, 8 h e 9 h (circa 23
  h 36 min dopo il primo). Un `4xx` diverso da `410` non si ritenta.
- Un webhook non attivo (disattivato dai fallimenti o in pausa con `active: false`)
  non riceve più niente: le consegne già in coda si chiudono come `failed` con
  `error` «webhook non attivo», senza richiesta e senza contare come fallimento.
  Una *Redeliver* parte comunque.
- **410 Gone** ferma la consegna: nessun altro tentativo (stato `gone`).
- Dopo **3 giorni di fallimenti consecutivi** il webhook si disattiva
  (`active: false`, `disabledReason: consecutive_failures`) e chi lo ha creato
  (per un webhook di organizzazione, gli owner) riceve una notifica con motivo
  `webhook`. Una consegna riuscita azzera il conto. Si riattiva con `POST
  …/hooks/{hookId}/reactivate`.
- Il log delle consegne (richiesta, risposta, esito) si conserva **30 giorni**.
  La risposta è troncata a **4 KB** (`truncated: true`). *Redeliver* rimanda lo
  stesso payload con una nuova consegna e una firma ricalcolata.
- **Protezione SSRF (C8)**: la rete aziendale è ammessa; sono sempre bloccati
  loopback, indirizzi interni del cluster e link-local (`169.254.0.0/16`),
  controllati dopo la risoluzione DNS e a ogni redirect (al massimo 3, che si
  seguono). L'amministratore dell'installazione può aggiungere liste di
  destinazioni ammesse e vietate; le tre categorie fisse non si possono
  sbloccare. Un indirizzo bloccato dà 422 (`url_not_allowed`) alla creazione e,
  se lo diventa dopo, un fallimento con `error` nel log.
- L'ordine di arrivo non è garantito (i tentativi falliti si ritentano dopo
  eventi più recenti): ogni payload ha `time` e, per le issues, `updatedAt`.
  Un ricevitore idempotente usa `X-GitStack-Delivery` solo per le Redeliver;
  lo stesso evento non produce due consegne dello stesso webhook.

## Corpo comune

Ogni corpo è un oggetto JSON con questi campi (poi quelli dell'evento):

| Campo | Significato |
|---|---|
| `version` | `1` |
| `event` | come `X-GitStack-Event` |
| `action` | vedi tabella; assente per `push` |
| `time` | quando è nato l'evento (UTC, RFC 3339) |
| `repository` | `{ id, fullName, owner, name, defaultBranch, visibility, archived }` |
| `organization` | `{ id, name }` se il repo è di un'organizzazione; altrimenti assente |
| `sender` | `{ id, username, type }` chi ha causato l'evento (`type`: `human` o `agent`); `null` per eventi di sistema (es. chiusura da commit) |

I nomi sono in camelCase, le date in RFC 3339 UTC, gli id sono uuid; `number`
è il `#n` della issue.

## `push`

Parte dallo schema `git.push` v1 (`pkg/events/gitpush`, `docs/events.md`), ma
un webhook riceve **un messaggio per ref**: un `git push` di branch e tag
produce più consegne con lo stesso `X-GitStack-Event: push`. `sender` è il
`pusher` autenticato, non l'autore dei commit.

| Campo | Significato |
|---|---|
| `ref` | `refs/heads/<branch>` o `refs/tags/<tag>` |
| `before`, `after` | sha prima e dopo; `0000…0000` per ref creato (`before`) o eliminato (`after`) |
| `created`, `deleted`, `forced` | booleani derivati da `before`/`after` e dal force-push |
| `isDefaultBranch` | il ref è il branch principale |
| `commits` | commit nuovi, i più recenti per primi, al massimo 100; `[]` per un ref eliminato |
| `commitsTruncated` | i commit nuovi sono più di 100 (si ricostruiscono con `before..after`) |
| `commits[].sha`, `.message`, `.author`, `.committer` | come in `git.push` (`author` e `committer`: `name`, `email`, `date`); `message` completo |

```json
{
  "version": 1,
  "event": "push",
  "time": "2026-10-05T12:30:00Z",
  "repository": {
    "id": "3f1d2c4e-8a55-4b6e-9d0a-1c2b3d4e5f60",
    "fullName": "ada/demo",
    "owner": "ada",
    "name": "demo",
    "defaultBranch": "main",
    "visibility": "private",
    "archived": false
  },
  "sender": { "id": "7a9e1b20-5c3d-4e8f-a1b2-c3d4e5f60718", "username": "build-bot", "type": "agent" },
  "ref": "refs/heads/main",
  "before": "1111111111111111111111111111111111111111",
  "after": "2222222222222222222222222222222222222222",
  "created": false,
  "deleted": false,
  "forced": false,
  "isDefaultBranch": true,
  "commits": [
    {
      "sha": "2222222222222222222222222222222222222222",
      "author": { "name": "Ada Lovelace", "email": "ada@example.com", "date": "2026-10-05T12:20:00Z" },
      "committer": { "name": "Ada Lovelace", "email": "ada@example.com", "date": "2026-10-05T12:20:00Z" },
      "message": "Corregge il parser\n\nfixes #12\n"
    }
  ],
  "commitsTruncated": false
}
```

## `issues`

`issue` ha sempre lo stato completo **dopo** la modifica; i campi che cambiano
con certe azioni stanno a parte.

| Campo | Significato |
|---|---|
| `issue` | `{ id, number, title, body, state, closeReason, duplicateOf, author, assignees, labels, milestone, locked, createdAt, updatedAt, closedAt }`; `state` `open` o `closed`; `closeReason` `completed`, `not_planned` o `duplicate` (solo se chiusa); `author` e `assignees[]` come `sender`; `labels[]`: `{ id, name, color }`; `milestone`: `{ id, number, title }` o `null` |
| `assignee` | per `assigned` e `unassigned`: l'utente interessato |
| `label` | per `labeled` e `unlabeled` |
| `milestone` | per `milestoned` e `demilestoned` |
| `changes` | per `edited`: i valori **precedenti**, `{ "title": { "from": "…" } }` e/o `{ "body": { "from": "…" } }` |
| `commit` | per `closed` da commit (`fixes #n`): `{ sha, repository }`; `sender` è `null` |

```json
{
  "version": 1,
  "event": "issues",
  "action": "closed",
  "time": "2026-10-05T12:31:02Z",
  "repository": {
    "id": "3f1d2c4e-8a55-4b6e-9d0a-1c2b3d4e5f60", "fullName": "ada/demo", "owner": "ada", "name": "demo",
    "defaultBranch": "main", "visibility": "private", "archived": false
  },
  "sender": null,
  "issue": {
    "id": "c1d2e3f4-0a1b-4c2d-8e3f-a4b5c6d7e8f9",
    "number": 12,
    "title": "Il parser si blocca",
    "body": "Con un file vuoto non risponde.",
    "state": "closed",
    "closeReason": "completed",
    "author": { "id": "7a9e1b20-5c3d-4e8f-a1b2-c3d4e5f60718", "username": "ada", "type": "human" },
    "assignees": [],
    "labels": [ { "id": "9e8d7c6b-5a49-4382-b1a0-f9e8d7c6b5a4", "name": "bug", "color": "d73a4a" } ],
    "milestone": null,
    "locked": false,
    "createdAt": "2026-10-01T09:00:00Z",
    "updatedAt": "2026-10-05T12:31:02Z",
    "closedAt": "2026-10-05T12:31:02Z"
  },
  "commit": { "sha": "2222222222222222222222222222222222222222", "repository": "ada/demo" }
}
```

## `issue_comment`

| Campo | Significato |
|---|---|
| `issue` | `{ id, number, title, state, locked }` |
| `comment` | `{ id, body, author, createdAt, updatedAt }`; per `deleted` il `body` è vuoto (I4) |
| `changes` | per `edited`: `{ "body": { "from": "…" } }` |

```json
{
  "version": 1,
  "event": "issue_comment",
  "action": "created",
  "time": "2026-10-05T12:40:00Z",
  "repository": {
    "id": "3f1d2c4e-8a55-4b6e-9d0a-1c2b3d4e5f60", "fullName": "ada/demo", "owner": "ada", "name": "demo",
    "defaultBranch": "main", "visibility": "private", "archived": false
  },
  "sender": { "id": "5b4a3c2d-1e0f-4a9b-8c7d-6e5f4a3b2c1d", "username": "grace", "type": "human" },
  "issue": { "id": "c1d2e3f4-0a1b-4c2d-8e3f-a4b5c6d7e8f9", "number": 12, "title": "Il parser si blocca", "state": "open", "locked": false },
  "comment": {
    "id": "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d",
    "body": "Riprodotto, guardo io. @ada",
    "author": { "id": "5b4a3c2d-1e0f-4a9b-8c7d-6e5f4a3b2c1d", "username": "grace", "type": "human" },
    "createdAt": "2026-10-05T12:40:00Z",
    "updatedAt": "2026-10-05T12:40:00Z"
  }
}
```

## `repository`

Il corpo ha solo i campi comuni (`repository` è lo stato **dopo** l'evento) più:

| Campo | Significato |
|---|---|
| `changes` | per `visibility_changed`: `{ "visibility": { "from": "private" } }` |
| `deletedAt` | per `deleted`: il repo si può ripristinare per 7 giorni (R2) |

Nella v1 non c'è l'azione di rinomina né di trasferimento (R3).

```json
{
  "version": 1,
  "event": "repository",
  "action": "archived",
  "time": "2026-10-05T13:00:00Z",
  "repository": {
    "id": "3f1d2c4e-8a55-4b6e-9d0a-1c2b3d4e5f60", "fullName": "ada/demo", "owner": "ada", "name": "demo",
    "defaultBranch": "main", "visibility": "private", "archived": true
  },
  "sender": { "id": "7a9e1b20-5c3d-4e8f-a1b2-c3d4e5f60718", "username": "ada", "type": "human" }
}
```

## Da dove nascono

I webhook li produce core dagli eventi di dominio (`docs/events.md`):
`git.push` (letto da NATS con il consumer durevole `core-webhooks-git`) per
`push`; `issue.*`, `issue_comment.*` e `repository.*` dall'outbox
transazionale di core (`core.event_outbox`), lo stesso da cui li pubblica il
relay, per `issues`, `issue_comment` e `repository`. Le tabelle sono
`core.webhooks` e `core.webhook_deliveries` (migrazione
`0006_notifications_webhooks`; `0011_webhook_outbox` aggiunge il segno di
lettura dell'outbox). La coda delle consegne è la tabella stessa: le consegne
`pending` sopravvivono al riavvio di core. I campi di `issue` e `comment` sono
quelli di quando il motore elabora l'evento (di solito un secondo dopo): se la
issue cambia di nuovo nel frattempo, il payload mostra lo stato più recente.
