# web/

Interfaccia web di GitStack: React + TypeScript, SPA (Vite). Parla solo con l'API pubblica esposta dal `gateway`, la stessa usata da CLI, skills e (in futuro) server MCP — principio API-first (vedi `.lmbrain-lite/knowledge/decisions.md`, D7).

Stato: scheletro (M-01 T-07). Shell dell'app (sidebar + topbar, `src/layout/`) coerente con `design/styleguide/` e `design/mockups-v1/` (token in `src/styles/tokens.css`, **generato** dalla styleguide con `scripts/sync-tokens.mjs`, mai ricopiato a mano). Una pagina (`src/pages/ResourcesPage.tsx`) elenca e crea la risorsa generica di prova (D15) usando solo il client TypeScript generato da T-03 (`@gitstack/api-client`, vedi `client/ts/`) e passando dal gateway, con base URL relativa (`/api/v1`, mai un host di servizio diverso). Le altre voci di navigazione (repository, issue...) sono ancora disattivate ("Soon"): non esistono ancora come funzionalità di prodotto.

## Sviluppo

```sh
pnpm install
pnpm dev        # http://localhost:5173, proxy /api/v1 -> gateway locale su :8080 (tolto solo /api) (vedi vite.config.ts)
pnpm lint
pnpm typecheck
pnpm test
pnpm build      # tsc --noEmit + vite build, output statico in dist/
```

## Token di design (generati, non a mano)

`src/styles/tokens.css` non si modifica a mano: è estratto meccanicamente (script Node senza dipendenze, `scripts/sync-tokens.mjs`) dal blocco `:root{...}` (tema chiaro/scuro) di `.lmbrain-lite/design/styleguide/index.html`, la styleguide di riferimento. Dopo una modifica alla styleguide:

```sh
pnpm run sync-tokens          # rigenera src/styles/tokens.css
pnpm run check-tokens         # rigenera e fallisce se il file committato non è allineato (usato in CI)
```

Il resto del CSS di `web/` (`src/styles/base.css`) usa sempre `var(--token)`, mai un colore o una misura fissa.

## Dipendenza da client/ts

`@gitstack/api-client` è dichiarata come `"link:../client/ts"` in `package.json`: non è pubblicata su un registry npm (vedi `client/README.md`), pnpm la collega direttamente alla cartella sorgente. Per questo `web/Dockerfile` costruisce con il contesto alla radice del monorepo, non `web/` da sola (serve anche `client/ts/`).

## Package manager

`pnpm` (vedi `package.json` → `packageManager`, e `pnpm-lock.yaml` per le versioni bloccate). Un solo package manager per tutto il frontend, così le build sono riproducibili.

## Container

`web/Dockerfile`: build statica (stage Node) servita da `nginxinc/nginx-unprivileged` (stage runtime, utente non privilegiato `nginx`, porta 8080). Il template `deploy/nginx.conf.template` inoltra `/api/*` al gateway (`GATEWAY_UPSTREAM`, sovrascritto dal chart Helm di GIT-8) togliendo solo `/api` (`/api/v1/x` -> `/v1/x`, come il Middleware strip-api dell'Ingress), e fa da fallback SPA (`try_files ... /index.html`) per tutto il resto.

Licenza: AGPL-3.0, come il resto del server (vedi LICENSE in radice e la motivazione nel README principale).

## M-07: pagine per la CLI gs (percorsi e formato dell'indice)

Percorsi della SPA letti da `gs auth login --web` (GIT-164) e dall'immagine web (GIT-171):

- **Nuovo token precompilato**: `/settings/tokens/new` (richiede il login; dopo il login si torna allo stesso URL con la query intatta). Query string, tutte facoltative: `name` (testo), `scopes` (lista separata da virgole, dal catalogo TokenScope: read:user, write:user, read:org, write:org, admin:org, read:resource, write:resource), `expires` (giorni: 30, 90 o 365). Scope sconosciuti e scadenze non ammesse sono ignorati con un avviso che li nomina; l'utente conferma, il token non si crea da solo. Esempio: `/settings/tokens/new?name=gs&scopes=read:user,write:resource&expires=90`.
- **Downloads**: `/downloads` (rotta SPA pubblica, senza slash finale, raggiungibile prima del login). I file stanno sotto `/downloads/` (con slash) e li serve l'immagine web: nginx deve lasciare `/downloads` al fallback SPA (attenzione al redirect automatico verso `/downloads/` quando esiste la cartella).

Indice letto dalla pagina, `GET /downloads/index.json`:

```json
{"version":"sha-…","binaries":[{"os":"linux|darwin|windows","arch":"amd64|arm64","file":"gs_linux_amd64","url":"/downloads/gs_linux_amd64","sha256":"…","size":123}],"checksums":"/downloads/SHA256SUMS","skills":{"file":"gs-skills.zip","url":"/downloads/gs-skills.zip","sha256":"…"},"install":{"sh":"/install-gs.sh","ps1":"/install-gs.ps1"},"ca_cert":"/downloads/ca.crt"}
```

`ca_cert` è `null` senza CA propria; su windows il file è `gs_windows_amd64.exe`. I comandi mostrati usano `window.location.origin`: `curl -fsSL <origin>/install-gs.sh | sh` e `irm <origin>/install-gs.ps1 | iex`. Con l'indice assente o in errore la pagina mostra un messaggio e resta usabile.

## Markdown e evidenziazione della sintassi

- `src/components/Markdown.tsx`: react-markdown + remark-gfm (tabelle, liste di
  attività, autolink) + rehype-raw → rehype-sanitize (allowlist di default,
  stile GitHub) → rehype-slug (ancore sui titoli, dopo la sanificazione).
  Link e immagini relativi si risolvono con `basePath` (cartella del file) e le
  funzioni `resolveLink`/`resolveImage`: il componente non costruisce URL da
  solo. I link http(s) esterni hanno `rel="noopener noreferrer"`.
- **Evidenziazione: lowlight** (highlight.js, set `common`), unica libreria sia
  per il Markdown sia per la vista file (GIT-87): usare `highlightCode(code, lang)`
  da `src/lib/highlight.tsx`. Perché: produce un albero hast, convertito in
  elementi React (niente HTML da iniettare), è piccola da integrare e si carica
  con `import()` dinamico: `corepack pnpm build` con il componente importato
  mostra lowlight in un chunk separato dal bundle principale. I colori dei
  token usano `--tk-*` di tokens.css (classi `.hljs-*` in components.css).

Id dei titoli: rehype-slug gira **prima** di rehype-sanitize, che li prefissa con
`user-content-` (anti DOM clobbering: `# root` non collide con `<div id="root">`).
Le ancore interne `[x](#sez)` sono riscritte dal renderer di `a` in
`#user-content-sez`, quindi continuano a funzionare. Un `<pre>` HTML grezzo resta
un `<pre>` (spaziatura conservata); solo `<pre><code>` diventa un blocco evidenziato.

## Regola B2: riferimenti e HTML ammesso

Un solo motore (`Markdown.tsx`) per README, file `.md`, issues e commenti.

- Riferimenti: `#n` (repo corrente, prop `repo`) → `/<owner>/<repo>/issues/<n>`, `owner/repo#n` → stesso formato per l'altro repo, `@utente` → `/<utente>`, `@org/team` → `/orgs/<org>/teams/<team>`. Solo rendering: nessuna verifica di esistenza né di visibilità (C1 e notifiche sono di M-06). Mai dentro codice inline, blocchi, `<pre>`, link esistenti; né in email e URL.
- **Allowlist HTML** (rehype-sanitize, schema di default stile GitHub), tra gli altri: `details`, `summary`, `sub`, `sup`, `kbd`, `br`, `img`, `a`, `p`, `div`, `span`, `pre`, `code`, tabelle, liste, titoli, `blockquote`, `hr`, `em`, `strong`, `del`, `ins`, `picture`/`source`. Sempre rimossi: `script`, `iframe`, `style`, `form`, `svg`, attributi `on*` e `style`, URL `javascript:`/`data:`.
- Link esterni: `target="_blank"`, `rel="noopener noreferrer"`, `referrerpolicy="no-referrer"` (anche sulle immagini). Link e immagini relativi sono risolti nel repo da `resolveLink`/`resolveImage` (il raw di B3, con il login).
- Mermaid e formule non sono interpretati (v1): il blocco resta codice.
