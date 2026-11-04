# GIT-526 — Runner esterno su Windows: prova reale con un'app Tauri

Data: 2026-11-04. PC: Windows 11 Pro 10.0.22631, Node 24 (di sistema; il job usa Node 22 scaricato da `actions/setup-node`),
Rust 1.99.1 `x86_64-pc-windows-msvc` (installato dall'action `dtolnay/rust-toolchain@stable`), Visual Studio Build Tools 2022,
WebView2, PowerShell 5.1 di sistema e PowerShell 7.6.9 (zip portatile nel `PATH`), Git per Windows (Git Bash 5.2), `act` 0.2.89
(`act_Windows_x86_64.zip`, sha256 `0f8cf866ad7e32421a219fc551a0a0ea5d12ead464de12ebfeafd334a5973b50` = quello di `checksums.txt` della release).

## Come è stata fatta la prova (e dove si discosta dal testo dell'item)

**Il job non è girato su un'istanza GitStack vera**: non c'era un cluster né la VM e2e raggiungibile da questo PC, e l'opzione
`ci.externalGateway` è di GIT-527. La prova usa:

- il **binario vero** `gitstack-runner.exe` (compilato da `services/core/cmd/gitstack-runner`), con `config.json` e `run`, etichette `self-hosted,windows`;
- **`act` vero** 0.2.89 in modalità host (`-P self-hosted=-self-hosted`, `-P windows=-self-hosted`), le action vere da `https://github.com`
  (`--action-mirror`), la rete vera, Node/Rust/MSVC/NSIS veri;
- un **ci-gateway finto** (`fakeGateway` di `runner_test.go`, protocollo `runner.v1` vero) che consegna il task, l'archivio del codice, i
  segreti e le variabili, e registra log, passi ed esito. Il gateway finto applica ai log lo **stesso `ci.Masker`** del gateway vero (`UpdateLog`).

Quindi non c'è un link a un run di GitStack: la prova è riproducibile con

```powershell
$env:PATH += ';<cartella di pwsh 7>'
$env:GITSTACK_WINDOWS_E2E_DIR  = '<repo>\deploy\ci\windows-tauri-prova'
$env:GITSTACK_WINDOWS_E2E_ACT  = '<percorso>\act.exe'
$env:GITSTACK_WINDOWS_E2E_KEY  = '<chiave>'            # tauri signer generate -w <chiave> -p "" --ci
$env:GITSTACK_WINDOWS_E2E_WORK = 'C:\gsr'; $env:GITSTACK_WINDOWS_E2E_TARGET = 'C:\gsr-target'
$env:GITSTACK_WINDOWS_E2E_LOG  = '<file di log>'
go test -count=1 -timeout 100m -run TestWindowsE2E -v ./internal/cirunner/    # da services/core
```

(`services/core/internal/cirunner/windows_e2e_test.go`, solo Windows e solo con `GITSTACK_WINDOWS_E2E_DIR`). Resta da fare, a cura del board, lo
stesso job contro l'istanza vera quando c'è il gateway esterno (GIT-527): stesso workflow, `deploy/ci/windows-tauri-prova/.github/workflows/windows.yml`.

## Esito

Job `tauri` di `runs-on: [self-hosted, windows]`, app `deploy/ci/windows-tauri-prova` (Tauri 2, `bundle.targets: ["nsis"]`,
`createUpdaterArtifacts: true`), log completo in [`GIT-526-windows-runner.log`](GIT-526-windows-runner.log):

| Giro | Target di Cargo | Durata del job | Passo `Build Tauri` | Esito |
|---|---|---|---|---|
| a freddo (`C:\gsr-target-freddo` nuovo, 223 righe `Compiling`) | vuoto | **6 min 39 s** | 4 min 9,8 s | `RESULT_SUCCESS` |
| a caldo (stesso `CARGO_TARGET_DIR`) | pieno | 4 min 37 s | 1 min 45 s | `RESULT_SUCCESS` (log non allegato) |

Nel passo di build: `npx tauri build --bundles nsis` → `GsTauriProva_0.1.0_x64-setup.exe` (1,56 MiB) e
`GsTauriProva_0.1.0_x64-setup.exe.sig` (firma dell'updater, 444 byte, fatta con `TAURI_SIGNING_PRIVATE_KEY` passata come segreto del repo).
Per arrivarci i primi quattro giri hanno trovato: disco pieno sul drive `E:` del PC (target spostato su `C:`), `plugins.updater.pubkey` mancante in
`tauri.conf.json` e `serde_json` mancante (e poi messo nella sezione sbagliata di `Cargo.toml`) nell'app di prova: difetti dell'app/dell'ambiente di prova, non del runner.

## Criteri

1. **Job Windows → success**: sopra. Checkout (`actions/checkout@v4`: il codice è già nel workspace, il passo è saltato come previsto), `actions/setup-node@v4`
   (Node 22, 53 s la prima volta, poi dalla tool cache), `dtolnay/rust-toolchain@stable` (rustup già presente: 3 s a caldo, 2 min la prima volta che scarica un nuovo `stable`).
2. **Shell e file speciali** (righe 83–103 del log): `pwsh 7.6.9`, `powershell 5.1.22621.6133` e `bash 5.2.26` scrivono `GITHUB_ENV`, `GITHUB_OUTPUT`
   e `GITHUB_PATH`; il passo dopo vede le tre variabili, i tre `outputs` (`da-pwsh da-powershell da-bash`) e lancia i tre `.cmd` messi nel `PATH`.
   Il passo di verifica lancia un'eccezione se qualcosa non c'è (`Success`). In più, fuori dal job lungo: un comando nativo che esce con 3 fa fallire il passo `pwsh`,
   `powershell` e `bash`, in coda o a metà dello script (pwsh/powershell proseguono, come su GitHub; bash si ferma).
3. **Segreto su più righe mascherato**: il segreto `MULTI_SECRET` ha 3 righe (una con CRLF, `$HOME` e virgolette: `untrusted comment: …\nRWQ-riga-due-…\r\nriga-tre-$HOME-"q"`).
   Nel log, `echo "$MULTI"` (bash) e `Write-Output "pwsh: $env:MULTI"` (pwsh) stampano `***` per ogni riga (righe 104–118 del log). Il test fallisce se una riga
   del segreto, o della chiave di Tauri, compare in chiaro nel log. La variabile `vars.CARGO_TARGET_DIR` arriva ai passi (`variabile: C:\gsr-target-freddo`).
4. **Difetti trovati** (sotto): ognuno con test automatico, oppure scritto come limite noto.
5. **Documentazione**: sezione «Windows» di `docs/ci-runner-esterno.md`.
6. **CI verde su main**: a cura del board dopo il reintegro; i controlli del repo sono stati lanciati sul ramo (vedi la richiesta di revisione).

## Difetti trovati e correzioni

| # | Difetto | Correzione | Test |
|---|---|---|---|
| 1 | Il runner passava ad act solo `-s GITHUB_TOKEN`: `secrets.X` e `vars.X` erano **vuoti** (vale anche per il runner interno). `TAURI_SIGNING_PRIVATE_KEY` non arrivava. | Segreti e variabili del task in file dotenv `0600` (`<workdir>\actenv\`, letti con `--secret-file`/`--var-file`, tolti a fine job), non nell'ambiente di act (lo ereditano tutti i passi) né sulla riga di comando. Codifica senza perdite (a-capo, CRLF, `\`, `"`, `$`, UTF-8). | `TestRun_SegretiEVariabiAdAct`, `TestDotenvQuote`, `TestWriteDotenv`, `TestDotenvQuote_ConActVero` (act vero, se nel `PATH`) |
| 2 | act legge `.env`, `.secrets` e `.vars` **dalla cartella corrente**, cioè dal codice del repo: un `.env` di Vite/Tauri diventava l'ambiente del job, un `.secrets` committato i suoi segreti. | `--env-file` esplicito su un file vuoto del runner, più i due file sopra. | `TestRun_SegretiEVariabiAdAct` (archivio con `.env`, `.secrets`, `.vars`) |
| 3 | La pulizia fra i job ignorava gli errori di `RemoveAll`: su Windows un file ancora aperto (antivirus, processo figlio) lasciava un workspace sporco al job dopo. `outputs/` non si puliva mai. | `cleanJobDirs`: 4 tentativi con attesa crescente; se fallisce, il runner **non prende job** e ritenta dopo 5 s. | `TestCleanJobDirs_Ritenta` |
| 4 | `withoutCIEnv` toglieva `GITSTACK_CI_*` solo se maiuscole: su Windows i nomi non distinguono. | Confronto senza maiuscole/minuscole. | `TestWithoutCIEnv_MaiuscoleEMinuscole` |
| 5 | La cartella di lavoro predefinita stava in `%AppData%` (Roaming, sincronizzato dai profili di dominio). | `%LocalAppData%\gitstack-runner\work`. | `TestDefaultWorkDir` |
| 6 | `config.json` con il token `gsr_…` scritto con modo `0600`, che su Windows non limita nulla: fuori da `%AppData%` lo leggono gli altri utenti del PC. | `register` lo restringe all'utente con `icacls` (toglie ereditarietà, Everyone, Users, Authenticated Users). | `TestProtectFile` (solo Windows) |

## Limiti noti (non corretti, con il motivo)

- **I workflow girano come l'utente del runner** e leggono il suo `config.json`: il runner non separa l'utente del processo da quello dei passi. L'utente dedicato protegge dagli altri utenti, non da chi scrive i workflow dei repo serviti. Rimedio: registrare il runner solo per repo fidati e revocarlo se il token trapela (documentato).
- **`actions/upload-artifact@v4` e successive**: il ci-gateway risponde 501 (decisione già presa, GIT-370/GIT-393). Si usa `PUT /ci/v1/artifacts/<nome>` con uno ZIP. Il PUT non è stato provato qui perché il gateway della prova è finto.
- **`src-tauri\target` non si conserva fra i job** (sta nel workspace, cancellato a ogni job): senza `CARGO_TARGET_DIR` fuori dal workspace ogni build è «a freddo» (qui 6 min 39 s contro 4 min 37 s di un giro a caldo). Si risolve nel workflow, non nel runner. Cache di Cargo (`%USERPROFILE%\.cargo`), di npm e la tool cache di Node restano.
- **`dtolnay/rust-toolchain` cambia il toolchain predefinito di rustup del PC** (`rustup default stable`): lo fa l'action, non il runner. Su un PC condiviso, usare il `rustup` già installato.
- **Shell predefinita**: un passo senza `shell:` usa Windows PowerShell 5.1 (act), non `pwsh` come GitHub; `shell: pwsh` senza PowerShell 7 installato cade con `Cannot find: pwsh in PATH`.
- **Nessun timeout del job nel runner**: vale `timeout-minutes:` (act) e, per l'attività pianificata, `-ExecutionTimeLimit 0`. Un job lungo (qui 6 min; 15–30 min attesi per Odin) non ha problemi di connessione: il client HTTP del runner non ha timeout globale. Non provato sulla durata di 30 minuti.
- **Servizio di Windows**: `gitstack-runner` non lo è; l'avvio è con attività pianificata (procedura nella documentazione, **scritta ma non eseguita** qui: non ho creato l'utente `gsrunner` né l'attività sul PC).
- **Non provato**: macOS; `windows/arm64`; fiducia nella CA interna su Windows (nessuna CA interna in questa prova: i comandi sono quelli standard di Windows/Git/Node, da verificare sul PC del board).
