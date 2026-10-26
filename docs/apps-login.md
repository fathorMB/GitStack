# Login GitStack davanti alle app interne (H6)

Una app interna ha tre modi di accesso (`PUT /repos/{owner}/{repo}/apps/{app}/access`, serve `admin` sul repo):

| modo | chi passa |
| --- | --- |
| `login` (default) | chi ha `read` sul repo: repo interno → tutti gli utenti attivi, repo privato → chi ha un ruolo. Gli agenti passano con un token che ha `read:resource` e `read` sul repo. |
| `none` | tutti, senza login. Lo imposta solo un admin del repo, per scelta esplicita: una app nuova parte sempre da `login`. |
| `public` | su internet; solo con le app pubbliche abilitate dall'amministratore (H1). Oggi risponde 422 `public_apps_disabled`. |

Con `login` l'app riceve chi è l'utente in intestazioni firmate. Con `none` riceve **nessuna**
intestazione `X-GitStack-*`: quelle mandate dal client vengono comunque scartate.

## Come funziona

```
browser/agente ──> Traefik ──(1) gitstack-strip: svuota X-GitStack-* del client
                          ──(2) gitstack-auth: forwardAuth ──> gateway /_appauth/verify
                          ──> app   (solo se il gateway risponde 2xx)
```

- `core` crea nel namespace di ogni app due `Middleware` Traefik (`gitstack-strip` e
  `gitstack-auth`) e li mette davanti alla rotta. L'indirizzo del forward-auth porta `app`, `repo` e
  una firma HMAC calcolata da `core` con il segreto del gateway: chi chiama il gateway da fuori non può
  farsi firmare le intestazioni per un repo che sceglie lui.
- Il gateway risponde 2xx solo se la richiesta ha un'identità valida e `AccessPolicy` dice sì
  (`services/gateway/internal/appauth`: **un solo punto** dove si decide l'accesso; altre fonti,
  come una lista di accesso, si aggiungono lì: «`read` sul repo OPPURE membro della lista»).
- Il gateway restituisce a Traefik le intestazioni firmate **e** `Cookie`/`Authorization` ripuliti:
  `authResponseHeaders` li sostituisce a quelli del client. Il cookie di sessione GitStack
  (`gst_session`, `__Host-gst_session`), il cookie d'app (`gst_app_session`) e un token `gst_…` non
  arrivano mai all'app; i cookie e l'`Authorization` propri dell'app restano.
- Un browser senza credenziali è mandato a `https://<gitstack>/api/_appauth/login`: con la sessione
  GitStack riceve un codice di un minuto legato all'host dell'app e torna su
  `https://<app>/_gitstack/callback` (servito dal forward-auth, non dall'app), che rilascia il cookie
  d'app (otto ore, `HttpOnly`, `Secure`, valido solo su quell'host) e rimanda alla pagina richiesta.
  A ogni richiesta il gateway ricontrolla `read`: togliere il permesso vale subito. Senza una sessione
  GitStack il login web porta alla home: riaprire l'app completa il passaggio.

## Intestazioni ricevute dall'app

| intestazione | contenuto |
| --- | --- |
| `X-GitStack-User` | username (per un agente, il suo username) |
| `X-GitStack-Email` | email, vuota se l'utente non ne ha una |
| `X-GitStack-Teams` | team come `organizzazione/team`, separati da virgola, ordinati; vuoto senza team |
| `X-GitStack-Auth-Time` | secondi Unix della verifica |
| `X-GitStack-Key-Id` | identificativo della chiave che ha firmato (primi 8 byte di SHA-256 della chiave pubblica, in esadecimale) |
| `X-GitStack-Signature` | firma Ed25519, base64url senza padding |

### Cosa è firmato

Il testo, con `\n` fra i campi:

```
gitstack-app-identity-v1
<METODO in maiuscolo>
<host in minuscolo, come nell'intestazione Host>
<percorso della richiesta, senza query, come lo ha mandato il client>
<X-GitStack-Auth-Time>
<X-GitStack-User>
<X-GitStack-Email>
<X-GitStack-Teams>
```

La firma copre metodo, host e percorso: un'intestazione catturata non si può riusare su un'altra
pagina o su un'altra app. **Non** è un token di sessione: l'app deve controllare l'età (`Auth-Time` non
più vecchio di 60 secondi per richieste normali; per connessioni lunghe come WebSocket vale all'apertura).

### Chiave pubblica

`GET https://<gitstack>/api/.well-known/gitstack-app-keys` (senza autenticazione; dentro il cluster
`http://<gateway>:8080/.well-known/gitstack-app-keys`):

```json
{"keys":[{"kid":"9f3a1c2b7d4e5f60","alg":"Ed25519","publicKey":"<32 byte in base64>"}]}
```

La prima chiave è quella che firma; le altre, in rotazione, servono a verificare le firme già emesse.
Scegli la chiave dal `X-GitStack-Key-Id`; una chiave sconosciuta vuol dire «rileggi l'elenco».

### Esempio (Go)

```go
type key struct{ KID, Alg, PublicKey string }

func verify(r *http.Request, keys map[string]ed25519.PublicKey) (user string, err error) {
	h := r.Header
	pub, ok := keys[h.Get("X-GitStack-Key-Id")]
	if !ok {
		return "", errors.New("chiave sconosciuta")
	}
	sig, err := base64.RawURLEncoding.DecodeString(h.Get("X-GitStack-Signature"))
	if err != nil {
		return "", err
	}
	msg := "gitstack-app-identity-v1\n" + strings.ToUpper(r.Method) + "\n" + strings.ToLower(r.Host) + "\n" +
		r.URL.EscapedPath() + "\n" + h.Get("X-GitStack-Auth-Time") + "\n" + h.Get("X-GitStack-User") + "\n" +
		h.Get("X-GitStack-Email") + "\n" + h.Get("X-GitStack-Teams")
	if !ed25519.Verify(pub, []byte(msg), sig) {
		return "", errors.New("firma non valida")
	}
	t, _ := strconv.ParseInt(h.Get("X-GitStack-Auth-Time"), 10, 64)
	if d := time.Since(time.Unix(t, 0)); d > time.Minute || d < -time.Minute {
		return "", errors.New("firma scaduta")
	}
	return h.Get("X-GitStack-User"), nil
}
```

### Esempio (Node)

```js
import { createPublicKey, verify } from 'node:crypto';

// publicKey: 32 byte in base64 dall'elenco delle chiavi; il prefisso DER racchiude una chiave Ed25519.
const der = (b64) => Buffer.concat([Buffer.from('302a300506032b6570032100', 'hex'), Buffer.from(b64, 'base64')]);
const keyOf = (b64) => createPublicKey({ key: der(b64), format: 'der', type: 'spki' });

export function whoIs(req, keys /* kid → KeyObject */) {
  const h = req.headers;
  const key = keys.get(h['x-gitstack-key-id']);
  if (!key) throw new Error('chiave sconosciuta');
  const path = req.url.split('?')[0];
  const msg = ['gitstack-app-identity-v1', req.method.toUpperCase(), req.headers.host.toLowerCase(), path,
    h['x-gitstack-auth-time'], h['x-gitstack-user'], h['x-gitstack-email'] ?? '', h['x-gitstack-teams'] ?? ''].join('\n');
  const ok = verify(null, Buffer.from(msg), key, Buffer.from(h['x-gitstack-signature'], 'base64url'));
  if (!ok || Math.abs(Date.now() / 1000 - Number(h['x-gitstack-auth-time'])) > 60) throw new Error('intestazioni non valide');
  return h['x-gitstack-user'];
}
```

Un'app che si fida delle intestazioni **senza** verificarle è sicura solo finché nessuno raggiunge il
suo Service saltando Traefik: la verifica della firma toglie questa ipotesi.

## Configurazione

Gateway:

| variabile | significato |
| --- | --- |
| `GITSTACK_GATEWAY_APPAUTH_SIGNING_KEYS` | semi Ed25519 (32 byte in base64, `openssl rand -base64 32`), elenco «nuova,vecchia». Senza, `/_appauth/verify` risponde 503: nessuna app `login` passa. Va in un Secret. |
| `GITSTACK_GATEWAY_PUBLIC_URL` | indirizzo pubblico di GitStack (`https://git.acme.it`), per il passaggio dal login web |
| `GITSTACK_GATEWAY_APPS_DOMAIN` | dominio delle app (`apps.acme.it`): il gateway rifiuta host che non sono `<etichetta>.<dominio>` |
| `GITSTACK_SERVICE_SECRET_GATEWAY` | già esistente: verifica la firma dell'indirizzo del forward-auth e firma il codice e il cookie d'app |

Core: `GITSTACK_CORE_APPS_AUTH_URL` è l'indirizzo interno del forward-auth
(`http://<gateway>:8080/_appauth/verify`). Senza, le app `login` non ricevono una rotta (mai esposte
senza il login); il segreto è `GITSTACK_SERVICE_SECRET_GATEWAY`, che core già conosce.

**Rotazione.** Per cambiare la chiave di firma: metti la nuova in testa («nuova,vecchia»), aspetta
che le app rileggano l'elenco, poi togli la vecchia. Per il segreto di servizio vale la rotazione
documentata in `docs/operations.md` (elenco «nuova,vecchia»; gli indirizzi dei forward-auth si
riallineano al giro di sync successivo).
