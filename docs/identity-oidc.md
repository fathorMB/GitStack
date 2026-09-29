# Login esterno OIDC

GitStack può far accedere gli utenti con un provider OpenID Connect esterno (Entra ID, Google, Keycloak, o qualunque provider conforme) accanto al login con password. I provider li configura l'amministratore; non c'è nessuna interfaccia di configurazione. Il login è un *authorization code flow* con PKCE (S256): `state` e `nonce` sono verificati e il token ID è validato (firma, issuer, audience, scadenza) da `github.com/coreos/go-oidc/v3`.

Il servizio è **spento** finché non c'è un file di configurazione: `GET /v1/auth/oidc/providers` risponde `{"items": []}` e `GET /v1/auth/oidc/{slug}/start` risponde 404.

## Come si configura

Tre variabili d'ambiente di `identity` (più `GITSTACK_IDENTITY_OIDC_CONFIG_FILE`, il file dei provider):

| Variabile | Significato |
|---|---|
| `GITSTACK_IDENTITY_OIDC_CONFIG_FILE` | percorso del file JSON dei provider (nel deploy: un Secret Kubernetes montato). Vuota = OIDC spento |
| `GITSTACK_IDENTITY_OIDC_ENC_KEY` | chiave AES-256, **32 byte in base64**. Cifra i client secret nel database e il cookie di stato del login |
| `GITSTACK_IDENTITY_OIDC_ENC_KEY_ID` | identificativo della chiave (1-64 caratteri), salvato in `oidc_providers.enc_key_id` |
| `GITSTACK_IDENTITY_PUBLIC_URL` | URL pubblico di GitStack come lo vede il browser, senza slash finale (es. `https://git.example.com`) |

Le ultime tre sono obbligatorie quando c'è il file. Una chiave nuova si genera con `openssl rand -base64 32`.

### File dei provider

```json
{
  "providers": [
    {
      "slug": "keycloak",
      "displayName": "Accedi con Keycloak",
      "issuer": "https://keycloak.example.com/realms/gitstack",
      "clientId": "gitstack",
      "clientSecret": "…",
      "scopes": ["openid", "profile", "email"],
      "claims": {
        "email": "email",
        "emailVerified": "email_verified",
        "username": "preferred_username",
        "displayName": "name"
      },
      "linkByVerifiedEmail": true,
      "autoCreateUsers": false
    }
  ]
}
```

| Campo | Obbligatorio | Note |
|---|---|---|
| `slug` | sì | minuscole, cifre e trattini, 1-32 caratteri, univoco; entra nella redirect_uri |
| `displayName` | sì | testo del pulsante di login, al massimo 64 caratteri |
| `issuer` | sì | URL `https` senza query né frammento; deve coincidere **esattamente** con l'`issuer` del documento `/.well-known/openid-configuration` |
| `clientId`, `clientSecret` | sì | del client registrato presso il provider (tipo *confidential*) |
| `scopes` | no | default `openid profile email`; `openid` è sempre aggiunto |
| `claims.*` | no | nomi dei claim del token ID, con i default della tabella sopra. `email` è obbligatorio con `linkByVerifiedEmail` o `autoCreateUsers`; `emailVerified` con `linkByVerifiedEmail` |
| `linkByVerifiedEmail` | no, default `false` | collega l'identità all'utente locale con la stessa email, **solo** se il provider la dichiara verificata |
| `autoCreateUsers` | no, default `false` | crea un utente nuovo (senza password) al primo accesso |

Il file si valida all'avvio: un errore (slug duplicato, issuer `http`, campo sconosciuto…) ferma `identity` con un messaggio che non riporta mai i segreti. All'avvio i provider sono sincronizzati in `identity.oidc_providers` (upsert per `slug`, segreto cifrato AES-256-GCM); quelli tolti dal file diventano `enabled = false` (le identità collegate restano). Cambiare il file richiede il riavvio di `identity`.

### redirect_uri da registrare

```
<GITSTACK_IDENTITY_PUBLIC_URL>/api/v1/auth/oidc/<slug>/callback
```

Per esempio `https://git.example.com/api/v1/auth/oidc/keycloak/callback`. È l'unico URI di redirect da autorizzare presso il provider.

### Nel chart Helm

```yaml
identity:
  oidc:
    enabled: true
    publicUrl: https://git.example.com
    providers:            # stesso formato del file; finisce in un Secret, non in un ConfigMap
      - slug: keycloak
        # …
    # oppure: existingSecret: <secret con la chiave oidc.json>
```

Il chart genera la chiave di cifratura una sola volta (Secret `<release>-identity-oidc-key`, `helm.sh/resource-policy: keep`) o usa `identity.oidc.encKey.existingSecret`. Vedi `deploy/gitstack/README.md`.

## Come si collega o si crea l'utente

Dopo la validazione del token ID, in quest'ordine:

1. **Identità già nota**: `(provider, subject)` è già in `identity.oidc_identities` → login. Il `subject` (`sub`) è l'identificativo stabile: l'email può cambiare.
2. **`linkByVerifiedEmail`** e `email_verified` uguale al booleano `true` o alla stringa `"true"` (niente altro: né `"True"`, né `1`, né claim assente) → l'identità è collegata all'utente locale con quell'email (senza distinguere le maiuscole) e si entra come lui.
3. **`autoCreateUsers`** → nuovo utente `human`, non amministratore, senza password. Lo username viene dal claim `username` (o dalla parte locale dell'email), normalizzato a minuscole, cifre e trattini; se è già preso si aggiunge un suffisso numerico (`-2`, `-3`…). L'email si salva **solo se il provider l'ha verificata**.
4. **Altrimenti** → `409 oidc_identity_unlinked`. È anche il comportamento con entrambe le opzioni a `false` (il default): nessun collegamento automatico.

Regole che non hanno eccezioni:

- Un'**email non verificata non si collega mai** a un utente esistente, nemmeno con entrambe le opzioni attive.
- Se l'email è già di un utente e non si può collegare (opzione spenta, o email non verificata), **non si crea** un utente nuovo: 409.
- Un utente disattivato non entra (401), né per identità nota né per email.
- La sessione è la stessa del login con password (cookie `gst_session`) con `auth_method = 'oidc'`; poi si torna a `redirectTo` (solo percorsi relativi).

Con `linkByVerifiedEmail` ci si fida del provider: chi controlla l'`issuer` configurato può dichiarare verificata un'email qualunque. Configura solo provider di cui ti fidi; per un tenant multi-organizzazione (Entra ID `common`, Google Workspace aperto) preferisci `autoCreateUsers` senza collegamento per email, e collega gli utenti a mano.

## Come funziona (per chi indaga)

- `GET /v1/auth/oidc/{slug}/start?redirectTo=/percorso`: genera `state`, `nonce` (32 byte casuali) e il `code_verifier` PKCE; li mette, insieme a slug, `redirectTo` e a una scadenza di 10 minuti, in un cookie cifrato con AES-256-GCM (`gst_oidc_state`, `HttpOnly`, `Secure`, `SameSite=Lax`, `Path` ristretto alla callback del provider). Risponde 302 al provider.
- `GET /v1/auth/oidc/{slug}/callback?code=…&state=…`: apre il cookie, confronta lo `state` in tempo costante, rifiuta il riuso (un `state` si consuma una sola volta per processo; il `code` è comunque monouso presso il provider), scambia il `code` con il `code_verifier`, valida il token ID, confronta il `nonce`, poi applica le regole sopra. Il cookie di stato è cancellato a ogni risposta.
- Errori: 400 `oidc_invalid_state` (state/cookie mancante, diverso, riusato, scaduto); 401 `oidc_login_failed` (scambio del code o token ID non valido, utente disattivato); 409 `oidc_identity_unlinked`; 404 provider sconosciuto o non abilitato.
- Il client secret non compare mai nei log né negli errori.
- Con più repliche di `identity` il riuso dello `state` è controllato per replica; il cookie di stato è comunque valido solo per il suo flusso e il `code` è monouso.

## Esempi

### Keycloak

1. Nel realm, crea un client *OpenID Connect* con *Client authentication* **On** e solo *Standard flow*.
2. *Valid redirect URIs*: `https://git.example.com/api/v1/auth/oidc/keycloak/callback`.
3. *Advanced → Proof Key for Code Exchange Code Challenge Method*: `S256` (facoltativo ma consigliato: GitStack lo usa sempre).
4. Copia il *Client secret* da *Credentials*.

```json
{
  "slug": "keycloak",
  "displayName": "Accedi con Keycloak",
  "issuer": "https://keycloak.example.com/realms/<realm>",
  "clientId": "gitstack",
  "clientSecret": "<client secret>",
  "linkByVerifiedEmail": true
}
```

Keycloak manda `email_verified` (vero solo se l'email dell'utente è verificata nel realm): il collegamento per email funziona. Con Keycloak ≥ 17 l'issuer non ha più il prefisso `/auth`.

### Google

1. In Google Cloud Console → *API e servizi → Credenziali*, crea un *ID client OAuth* di tipo *Applicazione web*.
2. *URI di reindirizzamento autorizzati*: `https://git.example.com/api/v1/auth/oidc/google/callback`.

```json
{
  "slug": "google",
  "displayName": "Accedi con Google",
  "issuer": "https://accounts.google.com",
  "clientId": "<id>.apps.googleusercontent.com",
  "clientSecret": "<client secret>",
  "scopes": ["openid", "profile", "email"],
  "linkByVerifiedEmail": true
}
```

Google manda `email_verified` come booleano. Google non manda `preferred_username`: con `autoCreateUsers` lo username deriva dalla parte locale dell'email (o imposta `"claims": {"username": "email"}` per usare l'indirizzo intero, normalizzato).

### Microsoft Entra ID

1. In Entra ID → *Registrazioni app → Nuova registrazione*; tipo di account: *solo questa directory* (single tenant).
2. *Autenticazione → Aggiungi una piattaforma → Web*, URI di reindirizzamento: `https://git.example.com/api/v1/auth/oidc/entra/callback`.
3. *Certificati e segreti → Nuovo segreto client*: copia il **valore** (non l'ID del segreto).

```json
{
  "slug": "entra",
  "displayName": "Accedi con Microsoft",
  "issuer": "https://login.microsoftonline.com/<tenant-id>/v2.0",
  "clientId": "<application (client) id>",
  "clientSecret": "<valore del segreto>",
  "scopes": ["openid", "profile", "email"],
  "claims": { "username": "preferred_username" },
  "autoCreateUsers": true
}
```

**Attenzione: Entra ID non manda il claim `email_verified`.** Per la regola sopra, con Entra `linkByVerifiedEmail` **non collega mai** (claim assente = non verificata), anche se lo si attiva: gli utenti Entra o si creano con `autoCreateUsers` (l'email non viene salvata perché non verificata) o si collegano a mano all'utente locale. Non aggirarlo mappando `emailVerified` su un claim qualunque: in un tenant Entra l'email è un attributo modificabile e non prova il possesso della casella.

Usa l'endpoint per tenant (`/<tenant-id>/v2.0`) e non `common` o `organizations`: per quelli l'`issuer` del token contiene il tenant dell'utente e non coincide con quello del documento di discovery, quindi la validazione fallisce.

## Provare in locale con Keycloak

```
docker run -d --name kc -p 8080:8080 \
  -e KC_BOOTSTRAP_ADMIN_USERNAME=admin -e KC_BOOTSTRAP_ADMIN_PASSWORD=admin \
  -v "$PWD/services/identity/internal/httpapi/testdata/keycloak-realm.json:/opt/keycloak/data/import/gitstack-realm.json:ro" \
  quay.io/keycloak/keycloak:26.0.7 start-dev --import-realm
docker run -d --name pg -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=gitstack -p 55432:5432 postgres:16-alpine
cd services/identity
GITSTACK_TEST_DATABASE_URL="postgres://postgres:pw@localhost:55432/gitstack?sslmode=disable" \
GITSTACK_TEST_KEYCLOAK_URL=http://localhost:8080 \
  go test -tags=integration -count=1 -run Keycloak -v ./internal/httpapi/
```

Il realm di prova (`gitstack`) ha un client `gitstack` con PKCE S256 e tre utenti: `alice` (email verificata), `bob` (email **non** verificata), `dave` (email verificata). La CI esegue lo stesso test nel job `identity-oidc`.
