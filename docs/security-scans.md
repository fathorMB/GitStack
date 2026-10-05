# Scansioni di sicurezza in CI

Regola V2 del rilascio v1.0 (GIT-114): per la v1.0 servono scansioni in CI su
dipendenze vulnerabili, analisi del codice e immagini container, bloccanti per
il rilascio. Il board ha deciso il 2026-10-05 di introdurle subito, prima in
modalità «segnala», poi bloccanti sui tag.

Workflow: `.github/workflows/security.yml`. Logica di decisione:
`scripts/security/` (`gate.mjs`, `lib.mjs`, test in `gate.test.mjs`).
Eccezioni: `.github/security-exceptions.json`.

## Cosa gira

| Scansione | Strumento (versione pinnata nel workflow) | Cosa guarda |
|---|---|---|
| Dipendenze Go | `govulncheck` | tutti i moduli di `go.work` (da `go list -m`, mai elencati a mano), con analisi di raggiungibilità |
| Dipendenze web | `pnpm audit` | `web/pnpm-lock.yaml`, anche le dipendenze di sviluppo |
| Codice Go | `gosec` | tutti i moduli di `go.work` |
| Codice TypeScript | `eslint-plugin-security` (config `web/eslint.security.config.js`, script `pnpm run lint:security`) | `web/src`, test esclusi |
| Immagini | `Trivy` (vulnerabilità, `ignore-unfixed`) | gateway, identity, core, git, web, costruite con gli stessi Dockerfile e contesti del job `registry` di `ci.yml`, allo stesso sha (tag `sha-<sha completo>`) |

### Perché queste scelte

- **gosec ed eslint-plugin-security invece di CodeQL.** CodeQL con i risultati nella
  scheda «Code scanning» richiede un repository pubblico o GitHub Advanced
  Security: su un repository privato non è garantito, e un workflow che
  fallisce per un prerequisito d'account non è una scansione. gosec ed ESLint
  girano ovunque, non servono segreti né permessi in più (`contents: read`),
  producono un JSON che il gate sa leggere, e il file delle eccezioni vale
  per tutti allo stesso modo. Il limite è la profondità: sono analisi a
  pattern, senza analisi del flusso di dati come CodeQL. Se il repository
  diventa pubblico si può aggiungere CodeQL accanto, non al posto di questi.
- **pnpm audit invece di OSV-Scanner.** Il lockfile è già di pnpm, nessun
  binario da scaricare e pinnare; il report dà gravità e id GHSA.
- **govulncheck.** Conta solo le vulnerabilità raggiungibili dal codice
  (trace con funzione chiamata): sono `HIGH` nel gate. Quelle presenti nel
  modulo ma non chiamate sono `MEDIUM` e non bloccano mai. govulncheck non
  fornisce una gravità: «raggiungibile» è la soglia scelta qui.
- **Immagini costruite nel job di scansione.** Non si scarica da `ghcr.io`: sulle
  pull request l'immagine non viene pubblicata, e così non servono credenziali
  né un'attesa del job `registry`. Stessi Dockerfile, stesso sha; un'immagine di
  base che cambia fra le due build può dare differenze minime. Trivy usa
  `ignore-unfixed`: una vulnerabilità senza correzione disponibile non è
  azionabile e bloccherebbe un rilascio senza rimedio.

## Comportamento

- **pull request, ogni notte su main (schedule 03:17 UTC) e avvio a mano senza
  `release`**: modalità *segnala*. A ogni push su main non gira più, per il
  consumo di minuti e di job paralleli (decisione del board del 2026-10-05).
  Ogni
  scansione scrive un'annotazione `warning` per voce (le prime 50) e un
  riepilogo nel *Summary* del run; il report completo è un artefatto
  (`report-*`). Il job non fallisce per i finding. Fallisce solo se uno
  strumento non produce un report leggibile o se il file delle eccezioni non è
  valido: un'assenza di report non deve sembrare «nessun finding».
- **tag di release** (`v*`, comprese le `-rc.N`): `ci.yml` richiama
  `security.yml` con `release: true` (job `security`) e il job `registry`
  dipende da quel risultato. Un finding `CRITICAL` o `HIGH` senza eccezione
  valida fa fallire il job (annotazione `error`) e il tag non pubblica le
  immagini.
- **dry-run senza tag**: Actions → *security* → *Run workflow* con `release`
  spuntato, su qualunque ramo. Stessa soglia, nessun tag da creare. La logica
  di soglia è comunque provata dai test del gate (`node --test
  "scripts/security/*.test.mjs"`, job `gate-selftest`): HIGH senza eccezione →
  codice 1 in release e 0 in segnalazione; HIGH con eccezione valida → 0;
  eccezione scaduta → 1.

Gravità: `CRITICAL`, `HIGH`, `MEDIUM`, `LOW`, `UNKNOWN`. Bloccano solo `CRITICAL`
e `HIGH`. Per gosec la gravità è quella dello strumento; per ESLint un errore
è `HIGH` e un warning `MEDIUM` (le regole ad alto segnale sono `error`, il
resto di `eslint-plugin-security` è `warn`); per pnpm audit `moderate` è
`MEDIUM`.

## File delle eccezioni

`.github/security-exceptions.json`, un oggetto con l'array `exceptions`:

```json
{
  "exceptions": [
    {
      "id": "CVE-2099-0001",
      "tool": "trivy",
      "reason": "Il pacchetto non è raggiungibile: il binario non carica la libreria.",
      "expires": "2027-01-31",
      "path": "gitstack-core"
    }
  ]
}
```

| Campo | Obbligatorio | Significato |
|---|---|---|
| `id` | sì | `GO-…` (govulncheck), `GHSA-…` (pnpm-audit), `CVE-…`/id del distro (trivy), regola come `G304` (gosec) o `security/detect-unsafe-regex` (eslint-security) |
| `tool` | sì | `govulncheck`, `gosec`, `eslint-security`, `pnpm-audit`, `trivy` |
| `reason` | sì | perché è un falso positivo o un rischio accettato |
| `expires` | sì | `AAAA-MM-GG`, il primo giorno in cui la voce **non** vale più |
| `path` | no | sottostringa del luogo del finding (modulo, `file:riga`, nome immagine): senza, l'eccezione copre l'id ovunque |

Regole:

- Tutti i campi obbligatori devono essere presenti e `expires` ben formata,
  altrimenti il gate fallisce, anche in modalità segnala.
- Una voce scaduta non copre più nulla: il finding torna attivo e il gate
  scrive un `warning` «Eccezione scaduta». Rinnovala con un motivo aggiornato
  o toglila.
- Le eccezioni si chiedono con una pull request che modifica il file: il motivo
  e la data restano nella storia di git e passano dalla revisione.
- Lo stesso file serve tutti gli strumenti (non si traduce in `.trivyignore` o
  `osv-scanner.toml`): lo legge solo `gate.mjs`, dopo che lo strumento ha
  prodotto il suo report completo.

## Eseguirle in locale

```bash
go install golang.org/x/vuln/cmd/govulncheck@v1.1.4
(cd services/core && govulncheck -format json ./... > /tmp/vuln-services_core.json)
node scripts/security/gate.mjs govulncheck --name govulncheck /tmp/vuln-*.json

(cd web && corepack pnpm audit --json > /tmp/pnpm-audit.json)
node scripts/security/gate.mjs pnpm-audit /tmp/pnpm-audit.json
```

Con `--release` il gate esce 1 sui CRITICAL/HIGH non coperti.

## Rischio noto

Su rami `item/*` la CI non parte, e `workflow_call`, `workflow_dispatch`,
`aquasecurity/trivy-action` e `actions/upload-artifact@v6` non si possono
provare prima del merge. Il comportamento reale dei workflow si vede solo dopo
l'integrazione su `main`: il primo run verde (o il primo errore di
configurazione) arriva da lì. Quello che è stato provato in locale: la logica
del gate (test), govulncheck, gosec, ESLint e pnpm audit con gli stessi
comandi del workflow, e la build più la scansione Trivy (v0.70.0) delle
cinque immagini.

## Primo giro (2026-10-05, su `main` a `c081b35`)

Nessuna correzione in questo item: ogni punto qui sotto diventa un item
separato. Tutto ciò che è `HIGH`/`CRITICAL` e non coperto da eccezione
bloccherebbe oggi un tag di release.

**govulncheck** — 15 vulnerabilità raggiungibili (HIGH) e 24 non chiamate
(MEDIUM, non bloccanti). 14 delle 15 raggiungibili sono nella libreria
standard di Go 1.26.2 (`go.work` e i `go.mod` fissano 1.26.2, che è anche la
versione che usa la CI): `GO-2026-4918`, `4971`, `4976`, `4977`, `4986`,
`5026`, `5037`, `5039`, `5856`, `5972`, `6088`, `6089`, `6090`, `6218`; si
risolvono con Go ≥ 1.26.6 (la più alta delle correzioni indicate). L'altra è
`GO-2026-5004` (SQL injection in `github.com/jackc/pgx/v5` v5.7.6,
raggiungibile da core e identity, corretta in v5.9.2). Moduli coinvolti:
core, gateway, git, client/go, identity (più pkg/events per x509/asn1);
api, cli, names hanno solo voci non chiamate.

**gosec** — 22 occorrenze, 7 voci distinte: 4 `HIGH` `G115` (conversione
intera che può traboccare: `services/core/internal/config/config.go:215`,
`services/identity/internal/config/config.go:270`,
`services/git/internal/sshd/server.go:361`,
`services/identity/internal/password/password.go:156`); 18 `MEDIUM`: `G204`
(subprocess con argomenti variabili, in `services/git`, atteso per un server
git), `G301` (permessi directory in `repostore.go`), `G304` (apertura file da
percorso variabile: `hostkey.go`, generatori), `G306` (permessi WriteFile).
Da valutare caso per caso: molti sono falsi positivi da annotare con
un'eccezione motivata.

**eslint-plugin-security (web/src)** — 7 occorrenze: 3 `HIGH`
`security/detect-unsafe-regex` (`web/src/components/Markdown.tsx:38` e `:58`,
`web/src/lib/codeLines.ts:37`: da verificare per ReDoS su input utente, dato
che il Markdown rende contenuto dei repository) e 4 `MEDIUM`
`detect-object-injection` (`StatusBadge.tsx:15`, `codeApi.ts:113`,
`format.ts:55`, `RepoSettingsPage.tsx:52`).

**pnpm audit (web)** — 1 advisory `moderate`: `GHSA-82fw-gwwq-j7x9` in
vitest 3.2.7 / @vitest/mocker (path traversal in un mock, solo
dipendenza di sviluppo, corretto in vitest ≥ 4.1.11). Non bloccante.

**Trivy (immagini, solo vulnerabilità con correzione disponibile)**:

| Immagine | Risultato |
|---|---|
| gateway | 1 `UNKNOWN` (`DLA-4792-1`, tzdata) |
| identity | 2 `CRITICAL` (`CVE-2026-33815`, `CVE-2026-33816`, pgx v5.7.6, correzione in 5.9.0), 1 `LOW`, 1 `UNKNOWN` (tzdata) |
| core | come identity: 2 `CRITICAL` pgx, 1 `LOW`, 1 `UNKNOWN` |
| git | nessun finding |
| web | 2 `CRITICAL` (`CVE-2026-31789`, openssl 3.3.3-r0 → 3.3.7-r0), 40 `HIGH` (openssl, libexpat, libpng, libxml2, zlib, musl, c-ares, nghttp2), 72 `MEDIUM`, 42 `LOW`: tutti pacchetti Alpine dell'immagine di base di runtime (nginx), da aggiornare |

Nota: per core e identity govulncheck dà `GO-2026-4771`/`4772` (le stesse
CVE-2026-33815/33816 di pgx) come *non chiamate*, mentre Trivy le segna
`CRITICAL` perché guarda solo la versione del modulo nel binario. La
correzione vera è l'aggiornamento di pgx a ≥ 5.9.2 (che copre anche
`GO-2026-5004`), non un'eccezione.
