# Runner esterno della CI

GIT-278 (CI2, ADR 0001 §4). Un runner esterno è un processo su una macchina tua (Linux, macOS, Windows, una VM)
che prende i job che il pod interno non può fare: quelli con `runs-on:` diverso da `ubuntu-*`
(`self-hosted`, `windows-…`, `macos-…`, `gpu`, un'etichetta tua). Usa lo stesso protocollo `runner.v1` dei pod interni:
è **pull**, il runner apre sempre la connessione e non serve nessuna porta in ingresso sulla macchina.

## Cosa serve

- Il binario `gitstack-runner` (sotto) e [`act`](https://github.com/nektos/act) nel `PATH` (o `--act <percorso>`):
  il runner esegue il job con `act` in modalità host, come il pod interno. Sulla macchina girano i comandi dei workflow:
  usa una macchina (o un utente) dedicata alla CI.
- Raggiungere il **ci-gateway esterno** dell'istanza (porta dedicata di core, non quella dell'API): lo espone
  l'amministratore con l'opzione `ci.externalGateway` del chart, **spenta per default** (sezione sotto).

## 0. Esporre il ci-gateway (amministratore, GIT-329)

Il ci-gateway interno (`ci.gateway.port`, 8081) è raggiungibile solo dai pod dei job. Per i runner esterni il chart ha
un'opzione spenta per default, `ci.externalGateway` (dettagli in [`deploy/gitstack/README.md`](../deploy/gitstack/README.md)):

```sh
helm upgrade gitstack deploy/gitstack --reuse-values \
  --set ci.externalGateway.enabled=true --set ci.externalGateway.host=ci.gitstack.lan
```

Core apre una seconda porta (`GITSTACK_CORE_CI_EXTERNAL_ADDR`, 8082) e Traefik la pubblica con una `IngressRoute` sull'host
dedicato, solo HTTPS, con il certificato dell'istanza (la CA interna; deve coprire quell'host). Richiede
`ingress.tls.enabled`. La porta esterna accetta **solo token di runner registrati** (`gsr_…`): il biglietto di un pod
interno (`gsq_…`) riceve `Unauthenticated` e la NetworkPolicy dei job non la raggiunge. Un host dedicato, non un path, perché
il protocollo vive su percorsi suoi (`/runner.v1.RunnerService/…`, `/api/actions/…`, `/ci/v1/archive`) e il resto dell'host
risponde 404.

## 1. Scarica il runner

I binari sono sull'istanza, senza internet: `https://<host>/downloads/gitstack-runner_<os>_<arch>[.exe]`
(`linux`, `darwin`, `windows` × `amd64`, `arm64`). Il checksum è in `SHA256SUMS` e l'elenco in `index.json` (campo `runners`).

```sh
base=https://<host>/downloads
curl -fsSLO "$base/gitstack-runner_linux_amd64" -O "$base/SHA256SUMS"
grep ' gitstack-runner_linux_amd64$' SHA256SUMS | sha256sum -c -
chmod +x gitstack-runner_linux_amd64 && mv gitstack-runner_linux_amd64 /usr/local/bin/gitstack-runner
```

(Con la CA interna, vedi `deploy/gitstack/README.md`, «Download di gs e skills».) Gli stessi file sono negli asset della
release `sha-<commit>` / `vX.Y.Z`.

## 2. Registra il runner

Solo l'amministratore dell'installazione crea il token di registrazione: **monouso**, vale un'ora, si vede **una volta sola**
nella risposta alla creazione (nel database c'è solo l'hash e nessuna GET lo restituisce).

```sh
curl -fsS -X POST https://<host>/api/v1/admin/ci/runners/registration-token -H "Authorization: Bearer <token-admin>"
# {"token":"gsrt_…","expiresAt":"…"}
```

Sulla macchina del runner:

```sh
gitstack-runner register --url https://<ci-gateway> --token gsrt_… --name build-box --labels linux,gpu
```

`self-hosted` c'è sempre; le etichette sono in minuscolo. Il nome è unico (un nome già usato non consuma il token).
La registrazione scrive `config.json` (permessi `0600`) in `~/.config/gitstack-runner/` (`%AppData%\gitstack-runner\` su Windows)
con il **token del runner** (`gsr_…`): chi lo legge può prendere job al posto del runner, perciò il file e la cartella di lavoro
non vanno resi leggibili a chi scrive i workflow.

## 3. Avvia

```sh
gitstack-runner run            # --workdir, --act, --config, --action-mirror
```

Il runner esegue un job per volta, in ciclo, finché non lo fermi. Come servizio, esempio systemd:

```ini
[Service]
User=gitstack-runner
ExecStart=/usr/local/bin/gitstack-runner run
Restart=always
```

## Windows (GIT-526)

Provato su Windows 11 con `act` 0.2.89 e un'app Tauri 2 (installer NSIS firmato): prova e numeri in
[`docs/evidence/GIT-526-windows-runner.md`](evidence/GIT-526-windows-runner.md). Il runner è lo stesso binario
(`gitstack-runner_windows_amd64.exe`); cambia la preparazione del PC.

### Requisiti del PC

| Cosa | Perché |
|---|---|
| Windows 10/11 o Server 2019+ a 64 bit, una macchina (o almeno un utente) **dedicata alla CI** | i passi dei workflow girano come l'utente del runner |
| [Git per Windows](https://git-scm.com/download/win) (`git` e `bash` nel `PATH`) | `shell: bash`, il download delle action dal mirror (`--action-mirror`), `actions/setup-node` |
| [`act`](https://github.com/nektos/act/releases) 0.2.89 (`act_Windows_x86_64.zip`) | esegue il job in modalità host (`-P <etichetta>=-self-hosted`) |
| PowerShell 7 (`pwsh`) | `shell: pwsh`. Senza, il passo cade con `Cannot find: pwsh in PATH`; un passo **senza** `shell:` usa Windows PowerShell 5.1 (`powershell`), non `pwsh` come su GitHub |
| Per Tauri: Visual Studio Build Tools 2022 con «Sviluppo di applicazioni desktop con C++» (MSVC + Windows SDK), [WebView2](https://developer.microsoft.com/microsoft-edge/webview2/) (c'è già su Windows 11), Rust via `rustup` | `dtolnay/rust-toolchain` installa il toolchain ma non l'MSVC |
| Percorsi lunghi attivi: `Set-ItemProperty HKLM:\SYSTEM\CurrentControlSet\Control\FileSystem LongPathsEnabled 1` e `git config --system core.longpaths true` | `node_modules` e `target/` superano i 260 caratteri |
| Disco: 10 GB liberi per una build Tauri (target ~1,5 GB, cache di Cargo e npm, tool cache di Node) | |

Node non serve sul PC: `actions/setup-node` lo scarica nella tool cache del runner (`<workdir>\.cache\act\tool_cache`), che resta fra i job.
`dtolnay/rust-toolchain` invece **cambia il toolchain predefinito di rustup del PC** (`rustup default stable`): su un PC condiviso con altro
lavoro in Rust usa `rustup` già installato e salta l'action.

### Scarica e verifica (PowerShell)

```powershell
$base = 'https://<host>/downloads'
New-Item -ItemType Directory -Force C:\gsr\bin | Out-Null
Set-Location C:\gsr\bin
Invoke-WebRequest "$base/gitstack-runner_windows_amd64.exe" -OutFile gitstack-runner.exe
Invoke-WebRequest "$base/SHA256SUMS" -OutFile SHA256SUMS
$atteso = ((Get-Content SHA256SUMS | Where-Object { $_ -match ' \*?gitstack-runner_windows_amd64\.exe$' }) -split '\s+')[0]
$vero = (Get-FileHash gitstack-runner.exe -Algorithm SHA256).Hash.ToLower()
if ($atteso -ne $vero) { throw "SHA-256 diverso: atteso $atteso, trovato $vero" }
.\gitstack-runner.exe version
```

`act`: scarica `act_Windows_x86_64.zip` della release v0.2.89 e confronta con `checksums.txt` della stessa release
(`0f8cf866ad7e32421a219fc551a0a0ea5d12ead464de12ebfeafd334a5973b50`), poi `Expand-Archive act_Windows_x86_64.zip C:\gsr\bin`.

### Fiducia nella CA interna

Il runner (Go) usa l'archivio di Windows: importa la CA dell'istanza (`https://<host>/downloads/ca.crt`, vedi
`deploy/gitstack/README.md`, «Download di gs e skills»; **controlla l'impronta** con quella stampata dall'installer) nel PC, da amministratore:

```powershell
Import-Certificate -FilePath C:\gsr\ca.crt -CertStoreLocation Cert:\LocalMachine\Root
```

Con la CA solo nell'archivio dell'utente (`Cert:\CurrentUser\Root`) la vede solo quell'utente: per un utente dedicato importala in `LocalMachine`.
Anche gli strumenti dei passi hanno il loro archivio: `git config --system http.sslBackend schannel` (Git per Windows usa di default il proprio
`ca-bundle.crt`, e il mirror delle action su CA interna fallirebbe), `setx /M NODE_EXTRA_CA_CERTS C:\gsr\ca.crt` per Node e npm. Cargo e `rustup` usano
l'archivio di Windows.

### Registra

Con l'**utente dedicato** (sotto), così il `config.json` finisce nel suo profilo. Token di registrazione e ci-gateway come nella sezione 2:

```powershell
$env:GITSTACK_RUNNER_REGISTRATION_TOKEN = 'gsrt_…'
C:\gsr\bin\gitstack-runner.exe register --url https://<ci-gateway> --name pc-windows --labels windows
```

`self-hosted` c'è sempre. Un job con `runs-on: [self-hosted, windows]` lo prende questo runner. `register` scrive
`%AppData%\gitstack-runner\config.json` e ne restringe i permessi a quell'utente con `icacls` (il modo `0600` su Windows non limita nulla).

### Avvio: attività pianificata con un utente dedicato

`gitstack-runner` **non è un servizio di Windows** (non risponde al Service Control Manager): `sc.exe create` non funziona. Si usa un'attività
pianificata all'avvio, oppure un wrapper come NSSM. Utente senza privilegi di amministratore, cartella di lavoro **corta** (il percorso conta per il limite dei 260 caratteri):

```powershell
$pw = Read-Host -AsSecureString 'Password di gsrunner'
New-LocalUser gsrunner -Password $pw -PasswordNeverExpires -UserMayNotChangePassword | Out-Null
New-Item -ItemType Directory -Force C:\gsr\work, C:\gsr\target | Out-Null
icacls C:\gsr /inheritance:r /grant 'Administrators:(OI)(CI)F' 'SYSTEM:(OI)(CI)F' 'gsrunner:(OI)(CI)M' | Out-Null
icacls C:\gsr\bin /grant 'gsrunner:(OI)(CI)RX' | Out-Null   # l'utente non deve poter cambiare i binari

$az = New-ScheduledTaskAction -Execute C:\gsr\bin\gitstack-runner.exe -Argument 'run --workdir C:\gsr\work --act C:\gsr\bin\act.exe --action-mirror https://github.com'
$tr = New-ScheduledTaskTrigger -AtStartup
$se = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew -StartWhenAvailable
Register-ScheduledTask GitStackRunner -Action $az -Trigger $tr -Settings $se -User gsrunner -Password ([Net.NetworkCredential]::new('', $pw).Password) -RunLevel Limited
Start-ScheduledTask GitStackRunner
```

`-ExecutionTimeLimit 0` è indispensabile: il valore predefinito (72 ore) o uno più corto ferma il runner a metà job. Se il PC ha un
profilo utente da cui lanciare il runner a mano per provare, `gitstack-runner run --workdir C:\gsr\work --act C:\gsr\bin\act.exe` in una finestra va bene.

**Limite noto: i workflow girano come l'utente del runner.** `config.json` col token `gsr_…` è leggibile da quell'utente, e quindi da chi scrive i
workflow del repo (un passo `type %AppData%\gitstack-runner\config.json`). Il runner non separa i due: l'utente dedicato e `icacls` proteggono dagli
*altri* utenti del PC e dal resto del disco, non da chi scrive i workflow dei repo che il runner serve. Registra il runner solo per repo di cui ti fidi
(non per fork o PR di sconosciuti), e se il token trapela revoca il runner (`DELETE /api/v1/admin/ci/runners/{id}`, sezione «Stato e revoca»).

### Cosa è stato provato e come si comporta

- **Shell**: `pwsh` 7, `powershell` 5.1 e `bash` (Git Bash) con `GITHUB_PATH`, `GITHUB_ENV` e `GITHUB_OUTPUT` funzionano e si propagano ai passi
  successivi; un comando nativo che fallisce a metà script in `pwsh`/`powershell` non ferma lo script ma fa fallire il passo (come su GitHub, per
  `$LASTEXITCODE`), `bash` si ferma subito. In `bash`, `$GITHUB_PATH` vuole percorsi in stile Windows (`$(pwd -W)`) o `/e/...` convertibili.
- **Segreti** (`secrets.X`) e **variabili** (`vars.X`) del repo arrivano ai passi: il runner li scrive in `<workdir>\actenv\secrets.env` e `vars.env`
  (letti da act con `--secret-file` e `--var-file`, non dall'ambiente, così i passi non ereditano segreti che non citano) e li toglie a fine job.
  Un segreto su più righe (chiave minisign `TAURI_SIGNING_PRIVATE_KEY`, certificato) arriva intero. Il gateway li **maschera riga per riga** nei log.
  Un segreto messo **in linea** nel testo dello script (`echo ${{ secrets.X }}`) lo trasforma la shell prima che il masker lo veda, come su GitHub:
  passalo con `env:`. Prima di GIT-526 il runner passava a act solo `GITHUB_TOKEN`: `secrets.X` e `vars.X` erano vuoti.
- **`.env`, `.secrets`, `.vars` del repo non sono letti**: act li caricava dalla cartella corrente (il codice del repo) come ambiente e segreti del job.
- **Cartella di lavoro**: `workspace/`, `workflow/`, `actenv/` e `outputs/` si cancellano prima di ogni job, con qualche tentativo (su Windows un file può
  restare aperto un attimo); se la pulizia non riesce il runner **non prende job** e ritenta. La cartella predefinita sta in `%LocalAppData%\gitstack-runner\work`
  (non nel profilo Roaming); preferisci `--workdir C:\gsr\work`.
- **Cache fra job**: la tool cache di Node (`<workdir>\.cache\act\tool_cache`), la cache di Cargo (`%USERPROFILE%\.cargo`) e quella di npm
  (`%LocalAppData%\npm-cache`) restano fra i job. `src-tauri\target` **no**: sta nel workspace, che si cancella a ogni job, e ogni build ricompila tutto.
  Per averlo incrementale e fuori dal limite dei 260 caratteri metti nel workflow `env: CARGO_TARGET_DIR: C:\gsr\target` (un job per volta: nessuna collisione).
  `actions/cache` funziona (cache del ci-gateway).
- **Percorsi lunghi**: con `LongPathsEnabled` e un `--workdir` corto la build non cade; il runner usa le API di Go, che prefissano `\\?\` oltre i 248 caratteri.
- **Job lunghi**: il runner non ha un timeout suo e la connessione verso il gateway non scade durante il job; vale `timeout-minutes:` del job (act) e, per
  l'attività pianificata, `-ExecutionTimeLimit 0`.
- **Artefatti**: `actions/upload-artifact@v4` e successive **non sono supportate** dal ci-gateway (risponde 501 con un messaggio chiaro, vedi `docs/operations.md`):
  nei workflow si impacchetta in uno ZIP e si carica con `PUT /ci/v1/artifacts/<nome>` del ci-gateway (corpo = lo ZIP, token del job):
  `Compress-Archive installer.exe installer.zip; curl.exe -fsS -X PUT -T installer.zip -H "Authorization: Bearer $env:GITHUB_TOKEN" "<ci-gateway>/ci/v1/artifacts/installer"`
  (non provato in questa prova: il gateway della prova è finto), oppure si pubblica il file con `gs release`.
- **Mirror delle action**: `actions/checkout` non scarica nulla (il codice è già nel workspace); le altre action le clona il runner con `git` da `--action-mirror`
  (o da `https://github.com` se omesso): serve rete verso quell'indirizzo e una CA fidata da `git`.

## Come si scelgono i job

Un job con `runs-on:` che contiene anche una sola etichetta **diversa da `ubuntu-*`** va ai runner esterni
(`runs-on: [self-hosted, linux]`, `runs-on: gpu`, `runs-on: linux`): così decide `runnerTypeOf` (GIT-278, confermato da GIT-329).
Lo prende un runner le cui etichette le **contengono tutte** (`self-hosted` è implicita). Un job senza nessun runner
compatibile resta in coda.

**Attenzione a `runs-on: linux`.** `linux` non è `ubuntu-*`: il job non va a un pod interno, va ai runner esterni, e se
nessun runner ha registrato l'etichetta `linux` resta `queued` senza errori. Per usare il runner integrato scrivi
`runs-on: ubuntu-latest` (o un altro `ubuntu-*`); per un tuo runner registralo con quell'etichetta
(`--labels linux`). La UI mostra solo che il job è in coda su un «external runner» (`runnerType: external` nel contratto):
non c'è ancora un campo col motivo dell'attesa, perciò il motivo è questo. I job `ubuntu-*` restano ai pod interni e non
contano nella capacità del cluster (CI7); un runner esterno esegue un solo job alla volta.

## Compatibilità con il runner di Gitea (act_runner)

Il ci-gateway parla `runner.v1` di `actions-proto` come il runner di Gitea/Forgejo (oggi `gitea.com/gitea/runner`, già
`act_runner`). Il test `TestActRunner_Compat` (`services/core/internal/cigateway/actrunner_integration_test.go`, passo CI
«Compatibilità con il runner di Gitea») registra il binario **pinnato** (v5.2.2, sha256
`5a2639fd2a57feeba29a9b1fb0e941489f092a4fa681c7c333c0372731248b63` per `linux-amd64`) col token monouso, esegue un job
`runs-on: self-hosted` e verifica log ed esito `success`:

```sh
gitea-runner register --no-interactive --instance https://<ci-gateway> --token gsrt_… --name box --labels self-hosted:host
gitea-runner daemon
```

Per arrivarci il gateway ha dovuto fare tre cose in più rispetto al nostro `gitstack-runner` (le incompatibilità trovate dalla
prova, con il messaggio d'errore originale):

1. **Prefisso `/api/actions`**: il runner di Gitea chiama `<istanza>/api/actions/<servizio>/<metodo>`. Prima il gateway
   rispondeva 404 e la registrazione falliva con `cannot ping the Gitea instance server: unimplemented: 404 Not Found`.
   Ora toglie il prefisso.
2. **`PingService`**: il runner lo chiama prima di registrarsi (stesso errore sopra). Ora c'è, senza credenziali, come in Gitea.
3. **Token del runner su `UpdateLog`/`UpdateTask`**: il runner di Gitea riusa il proprio token (`gsr_…`) su ogni chiamata e
   non usa il token del job; il log arrivava con `unauthenticated: token del job non valido` (il job restava `in_progress`).
   Ora `gsr_…` vale anche per queste due chiamate, ma solo per il job che quel runner sta eseguendo.

Limiti noti: il runner di Gitea esegue il job con il suo `act`, non con quello di `gitstack-runner`, quindi `uses:`,
mirror delle action, cache e artefatti dipendono dalla sua configurazione e non sono coperti dalla prova (che usa passi
`run:`). Con il runner di Gitea il token del job (`GITHUB_TOKEN`) è mascherato nei log solo se il gateway non si è riavviato
a metà job (il masker è in memoria). Provato su Linux; Windows e macOS non sono coperti dalla prova.

## Stato e revoca

`GET /api/v1/admin/ci/runners` (solo amministratore) elenca i runner con etichette e stato: `online` (si è fatto vivo da meno
di 90 s), `busy` (ha un job in corso), `offline`. `DELETE /api/v1/admin/ci/runners/{runnerId}` revoca il runner: il suo
token non vale più, non prende altri job e il job che stava eseguendo è chiuso come `cancelled`. Per registrarlo di nuovo serve un
nuovo token di registrazione.

## Come per il runner integrato

Il runner esterno riceve il token del job (`GITHUB_TOKEN`, `gsj_…`, stessi permessi e stessa scadenza) e i segreti del repo,
e pubblica log e stati dei passi con `UpdateLog` / `UpdateTask`. Il gateway maschera i segreti e il token del job nelle righe di log
prima di scriverle, identico al pod interno. Se il runner sparisce senza chiudere il job, il job resta `in_progress` finché
l'amministratore revoca il runner (non c'è ancora un timeout automatico).
