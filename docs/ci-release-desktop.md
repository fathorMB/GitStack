# Release di un'app desktop (Tauri, Windows) da GitStack (GIT-528)

Workflow di esempio per un'app **Tauri 2** su Windows che, su un tag `v*`, costruisce l'installer firmato, scrive `latest.json`
dell'updater, crea la **release GitStack** con gli allegati e ne fa una **copia su un repo GitHub di sole release**, così le copie
già installate continuano a leggere `https://github.com/<utente>/<repo-release>/releases/latest/download/latest.json`.

Non è un file di GitStack da attivare: è un modello da copiare in `.github/workflows/release.yml` del repo dell'app (o del suo mirror
in pull). Il repo dell'app, nel caso tipico di Odin, è un **mirror in pull** di GitHub: i tag arrivano con la sincronizzazione
([ci-mirror-in-pull.md](ci-mirror-in-pull.md)).

## Cosa serve

| Cosa | Dove |
|---|---|
| Un runner esterno su Windows con etichetta `windows` (`--labels windows`) e il ci-gateway esposto | [ci-runner-esterno.md](ci-runner-esterno.md), sezioni 0 e «Windows» |
| La CI accesa sul repo (sul mirror in pull: «Accendi la CI», fa un admin o chi ha `write`) | [ci-mirror-in-pull.md](ci-mirror-in-pull.md) |
| PC del runner: Node (`actions/setup-node` lo scarica), Rust MSVC, Build Tools 2022, `pwsh` 7, `git`/`bash` | tabella «Requisiti del PC» di ci-runner-esterno.md |
| Il PC del runner si fida della CA dell'istanza (serve a `Invoke-RestMethod` verso `GITSTACK_API_URL`) | «Fiducia nella CA interna» di ci-runner-esterno.md |
| Un repo GitHub **di sole release** (pubblico), con almeno un commit sul ramo predefinito | GitHub |

### Segreti del repo (Impostazioni → Segreti)

| Segreto | Contenuto |
|---|---|
| `TAURI_SIGNING_PRIVATE_KEY` | la chiave privata dell'updater, **il contenuto** del file prodotto da `npx tauri signer generate` (non il percorso). Se ha una password, aggiungere `TAURI_SIGNING_PRIVATE_KEY_PASSWORD` e passarla al passo di build |
| `GH_RELEASES_TOKEN` | token GitHub (fine-grained) limitato al solo repo di release, permesso **Contents: Read and write**. Nient'altro |

### Variabili del repo (Impostazioni → Variabili)

| Variabile | Esempio | Perché |
|---|---|---|
| `GITSTACK_API_URL` | `https://homehub.local/api/v1` | l'API di GitStack raggiunta dal PC del runner (il job non la riceve da solo: il runner parla con il ci-gateway, non con l'API) |
| `GH_RELEASES_REPO` | `fathorMB/odin-releases` | repo GitHub di sole release |
| `CARGO_TARGET_DIR` | `C:\gsr-target` | `target/` fuori dal workspace, che si cancella a ogni job (build a caldo: circa 4 min 37 s invece di 6 min 39 s, GIT-526) |

`GITHUB_TOKEN` non va creato: è il token del job (`gsj_`) che il runner passa da solo.

## Il workflow

```yaml
name: release-desktop
on:
  push:
    tags: ['v*']

# Il token del job (secrets.GITHUB_TOKEN) può creare la release e caricare gli allegati
# solo con contents: write, e solo su questo repo.
permissions:
  contents: write

jobs:
  release:
    runs-on: [self-hosted, windows]
    timeout-minutes: 90
    env:
      CARGO_TARGET_DIR: ${{ vars.CARGO_TARGET_DIR }}
      TAG: ${{ github.ref_name }}
      GS_API: ${{ vars.GITSTACK_API_URL }}
      GS_REPO: ${{ github.repository }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '22'
      - uses: dtolnay/rust-toolchain@stable
      - run: npm ci

      # 1. Installer firmato. La chiave sta solo nell'ambiente di QUESTO passo: Tauri legge il
      #    contenuto della variabile, non serve alcun file sul disco.
      - name: Build Tauri (NSIS firmato)
        shell: pwsh
        env:
          TAURI_SIGNING_PRIVATE_KEY: ${{ secrets.TAURI_SIGNING_PRIVATE_KEY }}
          TAURI_SIGNING_PRIVATE_KEY_PASSWORD: ''
        run: |
          npx tauri build --bundles nsis
          if ($LASTEXITCODE -ne 0) { throw "tauri build: $LASTEXITCODE" }

      # 2. latest.json e raccolta degli allegati in una cartella.
      - name: latest.json
        shell: pwsh
        env:
          GH_RELEASES_REPO: ${{ vars.GH_RELEASES_REPO }}
        run: |
          $ErrorActionPreference = 'Stop'
          $b = Join-Path $env:CARGO_TARGET_DIR 'release\bundle\nsis'
          $exe = Get-ChildItem "$b\*-setup.exe" | Select-Object -First 1
          if (-not $exe) { throw 'installer NSIS assente' }
          $sigFile = "$($exe.FullName).sig"
          if (-not (Test-Path $sigFile)) { throw "firma dell'updater assente" }
          $out = New-Item -ItemType Directory -Force 'release-assets'
          Copy-Item $exe.FullName, $sigFile $out
          $version = $env:TAG.TrimStart('v')
          # URL dei download della copia su GitHub: è quello che l'updater delle copie installate segue.
          $base = "https://github.com/$env:GH_RELEASES_REPO/releases/download/$env:TAG"
          $latest = [ordered]@{
            version  = $version
            notes    = "Versione $version"
            pub_date = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
            platforms = @{
              'windows-x86_64' = [ordered]@{
                signature = (Get-Content $sigFile -Raw).Trim()
                url       = "$base/$([uri]::EscapeDataString($exe.Name))"
              }
            }
          }
          # UTF-8 senza BOM: l'updater non lo accetta con il BOM.
          [IO.File]::WriteAllText((Join-Path $out.FullName 'latest.json'), ($latest | ConvertTo-Json -Depth 5), (New-Object Text.UTF8Encoding $false))
          Get-ChildItem $out | Get-FileHash -Algorithm SHA256 | Format-Table Hash, @{n='File';e={Split-Path $_.Path -Leaf}}

      # 3. Release GitStack sul tag: bozza, allegati, poi pubblicazione (nessuno vede una release a metà).
      - name: Release GitStack
        shell: pwsh
        env:
          GS_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          $ErrorActionPreference = 'Stop'
          $h = @{ Authorization = "Bearer $env:GS_TOKEN" }
          $rel = Invoke-RestMethod -Method Post -Headers $h -ContentType 'application/json' `
            -Uri "$env:GS_API/repos/$env:GS_REPO/releases" `
            -Body (@{ tagName = $env:TAG; name = $env:TAG; draft = $true } | ConvertTo-Json)
          foreach ($f in Get-ChildItem 'release-assets') {
            $a = Invoke-RestMethod -Method Post -Headers $h -ContentType 'application/octet-stream' `
              -Uri "$env:GS_API/repos/$env:GS_REPO/releases/$($rel.id)/assets?name=$([uri]::EscapeDataString($f.Name))" `
              -InFile $f.FullName
            $local = (Get-FileHash $f.FullName -Algorithm SHA256).Hash.ToLower()
            if ($a.sha256.ToLower() -ne $local) { throw "SHA-256 diverso per $($f.Name): server $($a.sha256), locale $local" }
            "{0}  {1}" -f $a.sha256, $f.Name
          }
          Invoke-RestMethod -Method Patch -Headers $h -ContentType 'application/json' `
            -Uri "$env:GS_API/repos/$env:GS_REPO/releases/$($rel.id)" -Body (@{ draft = $false } | ConvertTo-Json) | Out-Null

      # 4. Stessa release sul repo GitHub di sole release. latest.json si carica per ultimo e la
      #    release si pubblica alla fine: latest/download/latest.json cambia in un colpo solo.
      - name: Release su GitHub
        shell: pwsh
        env:
          GH_TOKEN: ${{ secrets.GH_RELEASES_TOKEN }}
          GH_RELEASES_REPO: ${{ vars.GH_RELEASES_REPO }}
        run: |
          $ErrorActionPreference = 'Stop'
          $h = @{ Authorization = "Bearer $env:GH_TOKEN"; Accept = 'application/vnd.github+json'; 'X-GitHub-Api-Version' = '2022-11-28' }
          $rel = Invoke-RestMethod -Method Post -Headers $h -ContentType 'application/json' `
            -Uri "https://api.github.com/repos/$env:GH_RELEASES_REPO/releases" `
            -Body (@{ tag_name = $env:TAG; name = $env:TAG; draft = $true } | ConvertTo-Json)
          $files = Get-ChildItem 'release-assets' | Sort-Object { $_.Name -eq 'latest.json' }
          foreach ($f in $files) {
            Invoke-RestMethod -Method Post -Headers $h -ContentType 'application/octet-stream' `
              -Uri "https://uploads.github.com/repos/$env:GH_RELEASES_REPO/releases/$($rel.id)/assets?name=$([uri]::EscapeDataString($f.Name))" `
              -InFile $f.FullName | Out-Null
            "caricato su GitHub: $($f.Name)"
          }
          Invoke-RestMethod -Method Patch -Headers $h -ContentType 'application/json' `
            -Uri "https://api.github.com/repos/$env:GH_RELEASES_REPO/releases/$($rel.id)" `
            -Body (@{ draft = $false; make_latest = 'true' } | ConvertTo-Json) | Out-Null
```

Se l'app ha già `scripts/make-latest-json.mjs` (Odin), il passo «latest.json» può chiamarlo con l'URL base dei download invece di
costruire il JSON in PowerShell: il file prodotto deve avere la forma sopra (`version`, `pub_date`, `platforms.windows-x86_64.signature`
e `.url`). Gli argomenti dello script sono quelli del repo dell'app, non di GitStack.

## Cosa fa ogni passo

1. **Avvio.** Parte dall'evento `push` del tag `v*`. Su un mirror in pull l'evento nasce quando la sincronizzazione porta il tag (fino a 10 minuti, o «Sincronizza ora»).
2. **Build.** `tauri build --bundles nsis` con `createUpdaterArtifacts: true` in `tauri.conf.json` produce `<App>_<versione>_x64-setup.exe` e il suo `.sig`.
3. **latest.json.** Il file dell'updater di Tauri 2, con la firma letta dal `.sig` e l'URL del download **sul repo GitHub di release** (non su GitStack: le copie installate non hanno accesso all'istanza).
4. **Release GitStack.** `POST /repos/{owner}/{repo}/releases` (`createRelease`) con il token del job, poi `POST …/releases/{id}/assets?name=…` (`uploadReleaseAsset`) per ogni file. Il server calcola l'SHA-256 dell'allegato; il passo lo confronta con quello locale, lo stampa e fallisce se differiscono. La release resta bozza fino all'ultimo allegato. Non esiste ancora `gs release`: si usa l'API.
5. **Release su GitHub.** Stessa sequenza (bozza, allegati con `latest.json` per ultimo, pubblicazione con `make_latest`) sul repo `GH_RELEASES_REPO`. Da quel momento `releases/latest/download/latest.json` punta al nuovo file.

## Limiti

- **Il tag deve già esserci in GitStack.** Creare la release su un tag che la sincronizzazione non ha portato dà `422 tag_not_found`. Per questo il workflow parte dall'evento `push` del tag sincronizzato: non lanciarlo a mano (dispatch) subito dopo il push su GitHub.
- **Nessun rerun idempotente.** Rilanciare il job su un tag già rilasciato fallisce (`409` su GitStack, `422` su GitHub). Se il job cade a metà resta una **bozza**: va cancellata (UI di GitStack o `DELETE …/releases/{id}`, e la bozza su GitHub) prima di rilanciare. La release GitHub non è pubblicata finché tutti gli allegati non ci sono.
- **Le release GitStack non tornano all'origine** e gli allegati restano fuori dal backup di default (vedi ci-mirror-in-pull.md). La copia su GitHub è quella che serve agli updater.
- **Nomi degli allegati senza spazi.** GitHub sostituisce gli spazi con punti nel nome del file, e l'URL scritto in `latest.json` non troverebbe più il file. Usare `productName` senza spazi.
- **Il token GitHub è un segreto del repo**: chi può scrivere workflow sul repo lo legge. Limitarlo al repo di release e a `Contents: write`, e registrare il runner solo per repo fidati.
- **La chiave di firma**: è mascherata nei log (anche su più righe) e il runner passa i segreti ad `act` con un file dotenv `0600` tolto a fine job (GIT-526); questo workflow non scrive la chiave in nessun file e la mette nell'ambiente solo del passo di build. Il runner non separa l'utente del processo da quello dei passi: chi scrive il workflow può comunque leggerla (limite di GIT-526). La build lascia sul PC la cache di Cargo/npm e `CARGO_TARGET_DIR`, mai la chiave.
- **`GITSTACK_API_URL` va indicata a mano** e il PC deve fidarsi della sua CA. Il token del job vale solo sul repo del run e al più quanto i permessi di chi ha acceso la CI (CI4).
- **`actions/upload-artifact@v4` non c'è** (501 del ci-gateway): qui non serve, gli allegati sono quelli della release.
- **Build a freddo** circa 6 min 40 s su un PC di prova; `timeout-minutes: 90` lascia margine.

## Stato della prova

Questa pagina e il workflow sono scritti, **ma non ancora provati sull'istanza vera** con un repo di prova in mirror in pull, un repo GitHub di prova e un runner Windows
registrato contro la 8443 (criteri dell'item GIT-528). Le correzioni emerse dal run (soprattutto: l'URL dell'API raggiunto dal runner, la CA, il formato di `id` e `sha256`) vanno riportate qui.
