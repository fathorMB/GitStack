#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Test end-to-end di GitStack (GIT-11, M-01/T-11): un solo comando che il
    board lancia da PowerShell come amministratore per provare l'installer
    (GIT-9) sulla VM di test (GIT-12) e verificare UI, API e JetStream.

.DESCRIPTION
    Passi, ciascuno stampato come "[PASS] <passo>" o
    "[FAIL] <passo>: <motivo>" (vedi il piano di consegna del CTO su GIT-11,
    commento del 28/09 16:00):
      a. reset-vm.ps1 (checkpoint 'clean') e IP della VM da Hyper-V.
      b. controlli di macchina pulita: niente k3s/kubectl, nessun servizio
         k3s, niente /etc/rancher ne /var/lib/rancher, porte 80/443/6443
         libere. Se falliscono, lo script esce senza installare.
      c. requisiti minimi (OS, vCPU, RAM, disco): li verifica gia il
         preflight di deploy/install.sh; qui si controlla solo che il log
         li mostri.
      d. verifica che le immagini gateway/core/web esistano su ghcr.io per
         il tag "sha-<Ref>", poi installer di GIT-9 dal commit -Ref di main,
         nello stesso modo in cui lo userebbe il cliente (curl | sudo bash).
      d3. HTTPS (N5, GIT-143): l'installer di default crea la CA interna.
         Scarica /downloads/ca.crt via HTTP (la 80 lo serve senza redirect),
         ne confronta l'impronta SHA-256 con `gitstack-tls fingerprint` sulla
         VM, controlla che la 80 reindirizzi a https (301 per GET, 308 per POST), che ca.key sia
         0600 e che il timer di rinnovo sia attivo. Da qui in poi tutte le
         chiamate sono HTTPS e si fidano SOLO di quella CA (callback del
         processo, lib/tls.ps1: niente modifiche allo store del PC), anche
         git (http.sslCAInfo).
      e. verifiche end-to-end: UI (dall'host Windows), /api/healthz,
         create+read della risorsa di prova via API, evento di prova
         pubblicato su JetStream (stream CORE, GIT-6). core pubblica
         l'evento in una goroutine dopo aver risposto 201 (timeout 5s):
         il conteggio dei messaggi si rilegge con un polling
         (-JetStreamPollAttempts tentativi ogni
         -JetStreamPollIntervalSeconds, default 6x5s), non con una lettura
         sola, per non dare un FAIL spurio per una corsa persa.
      e2. identity attraverso il gateway (GIT-36), senza credenziali valide:
         GET /api/v1/auth/session senza cookie -> 401 con code
         `unauthenticated`, POST /api/v1/auth/login con credenziali
         inventate -> 401 con code `invalid_credentials`: sono risposte di
         identity, non del gateway (che darebbe 404 o 503). Inoltre
         /api/v1/internal/verify deve dare 404 (interfaccia interna non
         esposto).
      e3. admin del primo avvio (GIT-35): login con la password letta dal
         Secret gitstack-identity-admin (mai stampata), 403
         password_change_required sulle altre chiamate, cambio password e
         chiamata autenticata attraverso il gateway.
      e5. git reale (GIT-77): utente di prova con token e chiave SSH, repo
         creato via API, push via HTTPS (ingress) e via SSH (porta 2222 della
         VM), clone/pull incrociati e clone anonimo rifiutato. Serve git
         nel PATH dell'host.
      e6. browser del codice (GIT-116, M-04): sul repo di e5 aggiunge
         page.html e image.svg (con <script>) e verifica via gateway tree,
         contents, raw (https.txt e page.html come text/plain, mai text/html;
         image.svg come octet-stream con attachment; nosniff e CSP
         sandbox), commits e dettaglio, e 401/404 senza credenziali.
      e7. prefisso API della UI (GIT-151): legge API_BASE_URL da
         web/src/lib/http.ts e, con quel prefisso, verifica sessione 401
         JSON, login admin e sessione 200 col cookie (la UI passa di li).
      e8. gs (GIT-173, M-07/L): sulla VM install-gs.sh scarica gs da
         /downloads (impronta della CA e checksum SHA256SUMS verificati), poi
         con GS_HOST/GS_TOKEN: repo create, clone, issue create, commit con
         `fixes #n` e push, issue chiusa, notifiche lette e `gs api` su un
         endpoint admin (e2e/gs-cycle.sh; log in gs-cycle.log).
      f. idempotenza: una seconda esecuzione dell'installer, senza reset,
         deve uscire con successo, non reinstallare k3s e non generare una
         nuova password di Postgres.
      f2. (GIT-35) dopo la seconda esecuzione il Secret dell'admin ha stessa
         resourceVersion e stessi dati, e l'admin non e stato ricreato: la
         password iniziale non vale piu, quella cambiata in e3 si.
      h. (GIT-148, DF/D) dati di prova (utente con token e chiave SSH,
         organizzazione, repo con un commit via HTTPS e uno via SSH, issue con
         allegato) e `sudo gitstack backup --key-file`: archivio cifrato
         0600 in cartella 0700, copiato sull'host con SHA-256 uguale,
         servizi di nuovo sani. Stampa la riga "Finestra di sola lettura".
      i. (GIT-148, DF/D) reset al checkpoint clean, reinstallazione dello
         stesso commit, `gitstack restore`: stessa CA, admin con la password
         cambiata, dati e allegato identici, clone HTTPS e SSH uguali con la
         chiave host verificata in modo rigido, `gitstack status` sano.
      j. (GIT-148, DF/F) reset, installazione del commit precedente
         (-PreviousRef, default: il primo genitore di -Ref), dati, restore
         di un archivio di un'altra versione rifiutato (exit 6), `gitstack
         upgrade --dry-run` e vero a -Ref, verifica di versione, backup
         preventivo, CA, admin, dati e clone.
      g. SEMPRE (anche dopo un fallimento): raccolta della diagnostica
         dalla VM e uno zip in -OutDir. Riepilogo finale PASS/FAIL. Exit
         code diverso da zero se un passo e fallito.

    La VM la resetta ed esegue solo il board (nessun agente ha accesso a
    Hyper-V o alla VM): questo script prepara tutto, il board lo lancia e
    incolla l'output nell'item.

.PARAMETER Ref
    Commit di GitStackRepo da provare (SHA o ref risolvibile). Default: lo
    SHA corrente di origin/main (risolto con 'git ls-remote' e stampato).

.PARAMETER SkipReset
    Salta il passo (a): riusa la VM nello stato in cui si trova. Solo per il
    debug di questo script: normalmente la VM viene sempre ripristinata al
    checkpoint 'clean' prima di ogni prova.

.PARAMETER SkipBackupRestore
    Salta i passi h e i (backup, reset, restore). Per il debug.

.PARAMETER SkipUpgrade
    Salta il passo j (upgrade dal commit precedente). Per il debug.

.PARAMETER PreviousRef
    Passo j: commit da installare prima dell'upgrade. Default: il primo
    genitore di -Ref (API GitHub). Deve avere gia il comando `gitstack
    upgrade` (GIT-147) e le immagini e il binario pubblicati dalla CI.

.PARAMETER GitSshPort
    Passo (e5): porta SSH del servizio git sulla VM. Default 2222.

.PARAMETER JetStreamPollAttempts
    Passo (e): numero di letture del conteggio JetStream dopo la create,
    finche' supera la baseline (core pubblica l'evento in una goroutine
    dopo il 201, non prima). Default 6.

.PARAMETER JetStreamPollIntervalSeconds
    Passo (e): secondi di attesa tra un tentativo e il successivo. Default
    5 (6x5s = fino a 30s in totale).

.EXAMPLE
    .\e2e.ps1
.EXAMPLE
    .\e2e.ps1 -Ref 07ab79d1234567890abcdef1234567890abcdef -OutDir C:\gitstack-e2e\run1
#>
[CmdletBinding()]
param(
    [string]$VmName = 'gitstack-test-vm',
    [string]$VmUser = 'gitstack',
    [string]$KeyPath = (Join-Path $env:USERPROFILE '.ssh\gitstack_vm'),
    [string]$GitStackRepo = 'fathorMB/GitStack',
    [string]$Ref,
    [string]$OutDir,
    [switch]$SkipReset,
    [switch]$SkipBackupRestore,
    [switch]$SkipUpgrade,
    [string]$PreviousRef,
    [int]$ResetBootTimeoutSeconds = 300,
    [int]$SshTimeoutSeconds = 180,
    [int]$SshConnectTimeoutSeconds = 15,
    [int]$SshCommandTimeoutSeconds = 60,
    [int]$InstallTimeoutSeconds = 900,
    [int]$JetStreamPollAttempts = 6,
    [int]$JetStreamPollIntervalSeconds = 5,
    [int]$GitSshPort = 2222
)

. (Join-Path $PSScriptRoot 'lib\common.ps1')
. (Join-Path $PSScriptRoot 'lib\http.ps1')
. (Join-Path $PSScriptRoot 'lib\tls.ps1')
. (Join-Path $PSScriptRoot 'lib\phases.ps1')

# --- Stato globale (risultati dei passi, file di log) ----------------------

$script:StepResults = New-Object System.Collections.Generic.List[object]
$script:LogFile = $null

function Write-Log {
    param([string]$Message)
    Write-Host $Message
    if ($script:LogFile) {
        Add-Content -LiteralPath $script:LogFile -Value $Message
    }
}

function Add-StepResult {
    <# Registra e stampa l'esito di un passo. Non interrompe l'esecuzione:
       il chiamante decide se un FAIL deve fermare i passi successivi. #>
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][bool]$Ok,
        [string]$Detail = ''
    )
    $line = if ($Ok) { "[PASS] $Name" } else { "[FAIL] ${Name}: $Detail" }
    Write-Log $line
    $script:StepResults.Add([pscustomobject]@{ Name = $Name; Ok = $Ok; Detail = $Detail }) | Out-Null
}

# --- Esecuzione di comandi esterni con timeout, senza far pendere lo script ---

function ConvertTo-ArgString {
    <# Costruisce una riga di comando singola, con virgolette solo dove
       serve: piu affidabile di -ArgumentList array su Windows PowerShell
       5.1 quando un valore (es. -KeyPath con spazi) le richiede. #>
    param([string[]]$ArgumentList)
    ($ArgumentList | ForEach-Object {
            if ($_ -match '[\s"]') { '"' + ($_ -replace '"', '\"') + '"' } else { $_ }
        }) -join ' '
}

function Invoke-ExternalCommand {
    <# Lancia un eseguibile catturando stdout/stderr ed esce in timeout
       (uccidendo il processo) invece di restare bloccata per sempre: una
       sessione ssh/scp che non risponde non deve mai impedire la raccolta
       della diagnostica e la stampa del riepilogo finale (passo g). #>
    param(
        [Parameter(Mandatory)][string]$FilePath,
        [Parameter(Mandatory)][string[]]$ArgumentList,
        [int]$TimeoutSeconds = 60
    )
    $stdOutFile = [IO.Path]::GetTempFileName()
    $stdErrFile = [IO.Path]::GetTempFileName()
    $stdInFile = [IO.Path]::GetTempFileName()
    try {
        $argString = ConvertTo-ArgString -ArgumentList $ArgumentList
        $proc = Start-Process -FilePath $FilePath -ArgumentList $argString -NoNewWindow -PassThru `
            -RedirectStandardOutput $stdOutFile -RedirectStandardError $stdErrFile -RedirectStandardInput $stdInFile
        # Toccare .Handle subito dopo Start-Process: senza questo, in Windows
        # PowerShell 5.1 l'handle del processo puo essere aperto con diritti
        # insufficienti e .ExitCode risulta $null/inaccessibile anche dopo
        # che WaitForExit ritorna true (bug noto di .NET/PowerShell 5.1).
        $null = $proc.Handle
        $finished = $proc.WaitForExit($TimeoutSeconds * 1000)
        if (-not $finished) {
            try { Stop-Process -Id $proc.Id -Force -ErrorAction Stop } catch {}
            return [pscustomobject]@{
                ExitCode = 124
                StdOut   = (Get-Content -Raw -LiteralPath $stdOutFile -ErrorAction SilentlyContinue)
                StdErr   = "timeout dopo ${TimeoutSeconds}s: '$FilePath $argString'"
                TimedOut = $true
            }
        }
        return [pscustomobject]@{
            ExitCode = $proc.ExitCode
            StdOut   = (Get-Content -Raw -LiteralPath $stdOutFile -ErrorAction SilentlyContinue)
            StdErr   = (Get-Content -Raw -LiteralPath $stdErrFile -ErrorAction SilentlyContinue)
            TimedOut = $false
        }
    } finally {
        Remove-Item -LiteralPath $stdOutFile, $stdErrFile, $stdInFile -Force -ErrorAction SilentlyContinue
    }
}

# --- SSH/SCP verso la VM (chiave dedicata, known_hosts isolato in -OutDir) ---

function Get-SshOptionArgs {
    param([Parameter(Mandatory)][string]$KnownHostsPath, [int]$ConnectTimeoutSeconds = 15)
    return @(
        '-i', $script:KeyPath,
        '-o', 'IdentitiesOnly=yes',
        '-o', 'StrictHostKeyChecking=accept-new',
        '-o', "UserKnownHostsFile=$KnownHostsPath",
        '-o', "ConnectTimeout=$ConnectTimeoutSeconds",
        '-o', 'BatchMode=yes'
    )
}

function Invoke-VmSsh {
    <# Esegue un comando sulla VM via ssh, senza pseudo-terminale (BatchMode
       evita qualunque prompt interattivo: una richiesta di password o di
       conferma fa fallire subito, invece di restare bloccata). #>
    param(
        [Parameter(Mandatory)][string]$Command,
        [int]$TimeoutSeconds = 60
    )
    $sshArgs = (Get-SshOptionArgs -KnownHostsPath $script:KnownHostsPath -ConnectTimeoutSeconds $script:SshConnectTimeoutSeconds) + @("$script:VmUser@$script:VmIp", $Command)
    return Invoke-ExternalCommand -FilePath 'ssh' -ArgumentList $sshArgs -TimeoutSeconds $TimeoutSeconds
}

function Copy-ToVm {
    param([Parameter(Mandatory)][string]$LocalPath, [Parameter(Mandatory)][string]$RemotePath, [int]$TimeoutSeconds = 60)
    $sshArgs = (Get-SshOptionArgs -KnownHostsPath $script:KnownHostsPath -ConnectTimeoutSeconds $script:SshConnectTimeoutSeconds) + @($LocalPath, "$script:VmUser@${script:VmIp}:$RemotePath")
    return Invoke-ExternalCommand -FilePath 'scp' -ArgumentList $sshArgs -TimeoutSeconds $TimeoutSeconds
}

function Copy-FromVm {
    param([Parameter(Mandatory)][string]$RemotePath, [Parameter(Mandatory)][string]$LocalPath, [int]$TimeoutSeconds = 60)
    $sshArgs = (Get-SshOptionArgs -KnownHostsPath $script:KnownHostsPath -ConnectTimeoutSeconds $script:SshConnectTimeoutSeconds) + @("-r", "$script:VmUser@${script:VmIp}:$RemotePath", $LocalPath)
    return Invoke-ExternalCommand -FilePath 'scp' -ArgumentList $sshArgs -TimeoutSeconds $TimeoutSeconds
}

function Invoke-RemoteHelper {
    <# Esegue deploy/test-vm/e2e/remote.sh (gia copiato sulla VM, vedi
       Initialize-RemoteHelper) con i sottocomandi/argomenti indicati. #>
    param([Parameter(Mandatory)][string[]]$RemoteArgs, [int]$TimeoutSeconds = 60)
    $quoted = ($RemoteArgs | ForEach-Object { "'" + ($_ -replace "'", "'\''") + "'" }) -join ' '
    return Invoke-VmSsh -Command "bash /tmp/gitstack-e2e/remote.sh $quoted" -TimeoutSeconds $TimeoutSeconds
}

function Initialize-RemoteHelper {
    <# Crea /tmp/gitstack-e2e sulla VM e ci copia remote.sh. #>
    $mk = Invoke-VmSsh -Command 'mkdir -p /tmp/gitstack-e2e' -TimeoutSeconds $script:SshCommandTimeoutSeconds
    if ($mk.ExitCode -ne 0) {
        return $mk
    }
    $localHelper = Join-Path $PSScriptRoot 'e2e\remote.sh'
    return Copy-ToVm -LocalPath $localHelper -RemotePath '/tmp/gitstack-e2e/remote.sh' -TimeoutSeconds $script:SshCommandTimeoutSeconds
}

# --- Verifica delle immagini su ghcr.io (senza credenziali: repository pubblici) ---

function Test-GhcrImageExists {
    param([Parameter(Mandatory)][string]$Owner, [Parameter(Mandatory)][string]$Repo, [Parameter(Mandatory)][string]$Tag)
    $ownerLower = $Owner.ToLowerInvariant()
    try {
        $tokenResp = Invoke-RestMethod -Uri "https://ghcr.io/token?service=ghcr.io&scope=repository:${ownerLower}/${Repo}:pull" -TimeoutSec 20
        $token = $tokenResp.token
        if (-not $token) { return $false }
        $headers = @{
            Authorization = "Bearer $token"
            Accept        = 'application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.docker.distribution.manifest.v2+json'
        }
        $resp = Invoke-WebRequest -Uri "https://ghcr.io/v2/${ownerLower}/${Repo}/manifests/${Tag}" -Headers $headers -Method Head -TimeoutSec 20 -UseBasicParsing -ErrorAction Stop
        return ($resp.StatusCode -eq 200)
    } catch {
        return $false
    }
}

# --- main --------------------------------------------------------------

function Main {
    Assert-Administrator
    Assert-HyperVAvailable
    Assert-SshClientAvailable

    if (-not (Test-Path -LiteralPath $KeyPath)) {
        throw "Chiave privata non trovata: $KeyPath. Deve esistere sulla macchina del board (mai nel repo): ed25519 senza passphrase, vedi il piano di consegna del CTO su GIT-11."
    }
    $script:KeyPath = $KeyPath
    $script:VmUser = $VmUser
    $script:VmName = $VmName
    $script:GitStackRepo = $GitStackRepo
    $script:GitSshPort = $GitSshPort
    $script:ResetBootTimeoutSeconds = $ResetBootTimeoutSeconds
    $script:SshTimeoutSeconds = $SshTimeoutSeconds
    $script:InstallTimeoutSeconds = $InstallTimeoutSeconds
    $script:SshConnectTimeoutSeconds = $SshConnectTimeoutSeconds
    $script:SshCommandTimeoutSeconds = $SshCommandTimeoutSeconds

    if (-not $OutDir) {
        $timestamp = Get-Date -Format 'yyyyMMdd-HHmmss'
        $OutDir = Join-Path $env:LOCALAPPDATA "GitStack\e2e-runs\$timestamp"
    }
    New-Item -ItemType Directory -Path $OutDir -Force | Out-Null
    $script:OutDir = $OutDir
    $script:LogFile = Join-Path $OutDir 'e2e.log'
    $script:KnownHostsPath = Join-Path $OutDir 'known_hosts'
    New-Item -ItemType File -Path $script:KnownHostsPath -Force | Out-Null

    Write-Log "=== GitStack -- test end-to-end (GIT-11) ==="
    Write-Log "VM: $VmName, utente: $VmUser, chiave: $KeyPath"
    Write-Log "Cartella di output: $OutDir"

    if (-not $Ref) {
        Write-Log "==> Risolvo lo SHA corrente di ${GitStackRepo}@main ..."
        $lsRemote = Invoke-ExternalCommand -FilePath 'git' -ArgumentList @('ls-remote', "https://github.com/$GitStackRepo.git", 'refs/heads/main') -TimeoutSeconds 30
        if ($lsRemote.ExitCode -ne 0 -or -not $lsRemote.StdOut) {
            throw "impossibile risolvere lo SHA di ${GitStackRepo}@main con 'git ls-remote' (exit $($lsRemote.ExitCode)): $($lsRemote.StdErr)"
        }
        $Ref = ($lsRemote.StdOut -split '\s+')[0].Trim()
        if ($Ref -notmatch '^[0-9a-f]{40}$') {
            throw "risposta inattesa da 'git ls-remote' per ${GitStackRepo}@main: '$($lsRemote.StdOut)'"
        }
    }
    Write-Log "Commit da provare (Ref): $Ref"
    $script:Ref = $Ref

    $exitCode = 1
    try {
        # --- a. reset della VM e IP -----------------------------------
        $script:VmIp = $null
        if (-not $SkipReset) {
            Write-Log "==> Passo a: ripristino il checkpoint 'clean' di '$VmName' ..."
            $resetLog = Join-Path $OutDir 'reset-vm.log'
            $resetArgs = @(
                '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', (Join-Path $PSScriptRoot 'reset-vm.ps1'),
                '-VmName', $VmName, '-VmUser', $VmUser, '-BootTimeoutSeconds', $ResetBootTimeoutSeconds, '-SshTimeoutSeconds', $SshTimeoutSeconds
            )
            # Lanciato come processo figlio (powershell.exe -File), non con
            # '&': reset-vm.ps1 chiama 'exit' nei suoi errori, e '&' lo
            # eseguirebbe nella STESSA sessione, terminando anche questo
            # script invece di limitarsi a segnalare il fallimento del passo.
            $reset = Invoke-ExternalCommand -FilePath 'powershell.exe' -ArgumentList $resetArgs -TimeoutSeconds ($ResetBootTimeoutSeconds + $SshTimeoutSeconds + 60)
            Set-Content -LiteralPath $resetLog -Value $reset.StdOut -Encoding utf8
            Add-Content -LiteralPath $resetLog -Value $reset.StdErr
            if ($reset.ExitCode -ne 0) {
                Add-StepResult -Name 'a. reset-vm.ps1 (checkpoint clean)' -Ok $false -Detail "reset-vm.ps1 uscito con codice $($reset.ExitCode) (log: $resetLog)"
                return
            }
            $ipMatch = [regex]::Match($reset.StdOut, 'IP:\s*(\d{1,3}(?:\.\d{1,3}){3})')
            if (-not $ipMatch.Success) {
                Add-StepResult -Name 'a. reset-vm.ps1 (checkpoint clean)' -Ok $false -Detail "IP non trovato nell'output di reset-vm.ps1 (log: $resetLog)"
                return
            }
            $script:VmIp = $ipMatch.Groups[1].Value
            Add-StepResult -Name 'a. reset-vm.ps1 (checkpoint clean)' -Ok $true
        } else {
            Write-Log "==> Passo a: -SkipReset attivo, riuso la VM '$VmName' nello stato attuale (solo debug)"
            $vm = Get-VM -Name $VmName -ErrorAction SilentlyContinue
            if (-not $vm) {
                Add-StepResult -Name 'a. VM esistente (-SkipReset)' -Ok $false -Detail "la VM '$VmName' non esiste in Hyper-V"
                return
            }
            if ($vm.State -ne 'Running') { Start-VM -Name $VmName }
            try {
                $script:VmIp = Wait-VmIPv4Address -VmName $VmName -TimeoutSeconds $ResetBootTimeoutSeconds
                Wait-TcpPort -IpAddress $script:VmIp -Port 22 -TimeoutSeconds $SshTimeoutSeconds
                Add-StepResult -Name 'a. VM esistente (-SkipReset)' -Ok $true
            } catch {
                Add-StepResult -Name 'a. VM esistente (-SkipReset)' -Ok $false -Detail $_.Exception.Message
                return
            }
        }
        Write-Log "IP della VM: $script:VmIp"

        # --- copia dell'aiutante remoto (serve dal passo b in poi) ------
        $helperCopy = Initialize-RemoteHelper
        if ($helperCopy.ExitCode -ne 0) {
            Add-StepResult -Name 'preparazione: copia di deploy/test-vm/e2e/remote.sh sulla VM' -Ok $false -Detail "ssh/scp exit $($helperCopy.ExitCode): $($helperCopy.StdErr)"
            return
        }

        # --- b. macchina pulita ----------------------------------------
        Write-Log "==> Passo b: verifico che la VM sia davvero pulita ..."
        $clean = Invoke-RemoteHelper -RemoteArgs @('clean-check') -TimeoutSeconds $SshCommandTimeoutSeconds
        Write-Log $clean.StdOut
        if ($clean.ExitCode -ne 0) {
            Add-StepResult -Name 'b. macchina pulita (nessun k3s/kubectl, /etc/rancher, /var/lib/rancher, porte libere)' -Ok $false -Detail (($clean.StdOut, $clean.StdErr) -join ' ').Trim()
            return
        }
        Add-StepResult -Name 'b. macchina pulita (nessun k3s/kubectl, /etc/rancher, /var/lib/rancher, porte libere)' -Ok $true

        # --- d.1 immagini pubblicate su ghcr.io per sha-<Ref> -----------
        Write-Log "==> Passo d (1/2): verifico che le immagini gateway/identity/core/web esistano su ghcr.io per sha-$Ref ..."
        $ownerRepo = $GitStackRepo -split '/'
        $owner = $ownerRepo[0]
        $missingImages = @()
        foreach ($svc in @('gitstack-gateway', 'gitstack-identity', 'gitstack-core', 'gitstack-web')) {
            if (-not (Test-GhcrImageExists -Owner $owner -Repo $svc -Tag "sha-$Ref")) {
                $missingImages += $svc
            }
        }
        if ($missingImages.Count -gt 0) {
            Add-StepResult -Name 'd.1 immagini su ghcr.io per sha-<Ref>' -Ok $false -Detail "mancanti: $($missingImages -join ', ') (tag sha-$Ref). Il job 'registry' della CI le pubblica solo dopo un push su main: verifica che sia verde per questo commit."
            return
        }
        Add-StepResult -Name 'd.1 immagini su ghcr.io per sha-<Ref>' -Ok $true

        # --- d.2 installer, prima esecuzione ----------------------------
        Write-Log "==> Passo d (2/2): eseguo l'installer di GIT-9 dal commit $Ref di main (prima esecuzione) ..."
        $installCmd = "curl -fsSL https://raw.githubusercontent.com/$GitStackRepo/$Ref/deploy/install.sh | sudo GITSTACK_REF=$Ref GITSTACK_ADMIN_REQUIRED=1 bash"
        Write-Log "Comando: $installCmd"
        $install1 = Invoke-VmSsh -Command $installCmd -TimeoutSeconds $InstallTimeoutSeconds
        $install1Log = Join-Path $OutDir 'installer-run1.log'
        Set-Content -LiteralPath $install1Log -Value $install1.StdOut -Encoding utf8
        Add-Content -LiteralPath $install1Log -Value $install1.StdErr
        if ($install1.ExitCode -ne 0) {
            Add-StepResult -Name 'd.2 installer (prima esecuzione)' -Ok $false -Detail "exit $($install1.ExitCode) (log: $install1Log)"
            return
        }
        Add-StepResult -Name 'd.2 installer (prima esecuzione)' -Ok $true

        # --- c. requisiti minimi nel log --------------------------------
        $preflightChecks = @(
            @{ Label = 'OS'; Pattern = 'OS: .*OK' },
            @{ Label = 'Architettura'; Pattern = 'Architettura: .*OK' },
            @{ Label = 'CPU'; Pattern = 'CPU: .*OK' },
            @{ Label = 'RAM'; Pattern = 'RAM: .*OK' },
            @{ Label = 'Disco'; Pattern = 'Disco su .*OK' }
        )
        $missingPreflight = $preflightChecks | Where-Object { $install1.StdOut -notmatch $_.Pattern } | ForEach-Object { $_.Label }
        if ($missingPreflight) {
            Add-StepResult -Name 'c. requisiti minimi registrati nel log' -Ok $false -Detail "righe di preflight non trovate nel log dell'installer: $($missingPreflight -join ', ') (log: $install1Log)"
        } else {
            Add-StepResult -Name 'c. requisiti minimi registrati nel log' -Ok $true
        }

        # --- d3. HTTPS con la CA interna (N5, GIT-143) ---------------------
        Write-Log "==> Passo d3: HTTPS con la CA interna (download di ca.crt, impronta, redirect 80->443) ..."
        $d3Ok = $true
        $d3Details = @()
        $caPath = Join-Path $OutDir 'ca.crt'
        $script:CaFingerprint = $null
        try {
            $caResp = Invoke-HttpRaw -Uri "http://$script:VmIp/downloads/ca.crt"
            if ($caResp.StatusCode -ne 200 -or $caResp.Body -notmatch 'BEGIN CERTIFICATE') {
                throw "GET http://<vm>/downloads/ca.crt: status $($caResp.StatusCode) (atteso 200 con un certificato PEM) $($caResp.Error)"
            }
            Set-Content -LiteralPath $caPath -Value $caResp.Body -Encoding ascii
            $script:CaFingerprint = Get-CertSha256Fingerprint -Path $caPath
            $fpVm = Invoke-VmSsh -Command 'sudo gitstack-tls fingerprint' -TimeoutSeconds $SshCommandTimeoutSeconds
            if ($fpVm.ExitCode -ne 0 -or $fpVm.StdOut.Trim() -ne $script:CaFingerprint) {
                throw "l'impronta del ca.crt scaricato ($script:CaFingerprint) non e quella della VM ('$($fpVm.StdOut.Trim())' $($fpVm.StdErr))"
            }
            Set-GitStackCaTrust -CaPath $caPath
        } catch {
            $d3Ok = $false; $d3Details += $_.Exception.Message
        }

        # 80 -> 443: redirect permanente (curl.exe, per non seguirlo).
        foreach ($p in @('/', '/api/healthz')) {
            $rd = Invoke-ExternalCommand -FilePath 'curl.exe' -ArgumentList @('-s', '-o', 'NUL', '-w', '%{http_code} %{redirect_url}', "http://$script:VmIp$p") -TimeoutSeconds 30
            $want = "https://$script:VmIp$p"
            if ($rd.ExitCode -ne 0 -or $rd.StdOut.Trim() -ne "301 $want") {
                $d3Ok = $false; $d3Details += "GET http://<vm>$p non reindirizza a $want con 301 (atteso 301, ricevuto): '$($rd.StdOut.Trim())' $($rd.StdErr)"
            }
        }
        # Gli altri metodi ricevono 308 (il metodo si conserva).
        $rdPost = Invoke-ExternalCommand -FilePath 'curl.exe' -ArgumentList @('-s', '-o', 'NUL', '-w', '%{http_code} %{redirect_url}', '-X', 'POST', "http://$script:VmIp/api/healthz") -TimeoutSeconds 30
        $wantPost = "https://$script:VmIp/api/healthz"
        if ($rdPost.ExitCode -ne 0 -or $rdPost.StdOut.Trim() -ne "308 $wantPost") {
            $d3Ok = $false; $d3Details += "POST http://<vm>/api/healthz non reindirizza a $wantPost con 308 (atteso 308, ricevuto): '$($rdPost.StdOut.Trim())' $($rdPost.StdErr)"
        }

        $keyMode = Invoke-VmSsh -Command 'sudo stat -c "%a %U" /etc/gitstack/tls/ca.key' -TimeoutSeconds $SshCommandTimeoutSeconds
        if ($keyMode.ExitCode -ne 0 -or $keyMode.StdOut.Trim() -ne '600 root') {
            $d3Ok = $false; $d3Details += "ca.key atteso '600 root': '$($keyMode.StdOut.Trim())' $($keyMode.StdErr)"
        }
        $timer = Invoke-VmSsh -Command 'systemctl is-active gitstack-tls-renew.timer' -TimeoutSeconds $SshCommandTimeoutSeconds
        if ($timer.ExitCode -ne 0 -or $timer.StdOut.Trim() -ne 'active') {
            $d3Ok = $false; $d3Details += "gitstack-tls-renew.timer non attivo: '$($timer.StdOut.Trim())'"
        }
        # La chiave della CA non deve essere in Kubernetes (solo il certificato).
        $caInK8s = Invoke-VmSsh -Command 'sudo k3s kubectl -n default get secret,configmap -o name' -TimeoutSeconds $SshCommandTimeoutSeconds
        if ($caInK8s.ExitCode -ne 0 -or $caInK8s.StdOut -notmatch 'secret/gitstack-tls' -or $caInK8s.StdOut -notmatch 'configmap/gitstack-ca') {
            $d3Ok = $false; $d3Details += "attesi secret/gitstack-tls e configmap/gitstack-ca: '$($caInK8s.StdOut.Trim())'"
        }
        Add-StepResult -Name 'd3. HTTPS: ca.crt scaricabile e con l impronta della VM, 80 reindirizzata a 443 (301 GET, 308 POST), ca.key 0600, timer di rinnovo attivo' -Ok $d3Ok -Detail ($d3Details -join '; ')
        if (-not $d3Ok) { return }

        # --- e. verifiche end-to-end -------------------------------------
        Write-Log "==> Passo e: verifiche UI e /api/healthz (HTTPS) ..."
        $baseUrl = "https://$script:VmIp"
        $eOk = $true
        $eDetails = @()

        try {
            $ui = Invoke-WebRequest -Uri "$baseUrl/" -TimeoutSec 20 -UseBasicParsing
            if ($ui.StatusCode -ne 200 -or $ui.Content -notmatch '(?i)<div id="root"') {
                $eOk = $false; $eDetails += "UI: status $($ui.StatusCode), corpo senza '<div id=""root"">'"
            }
        } catch {
            $eOk = $false; $eDetails += "UI non raggiungibile: $($_.Exception.Message)"
        }

        try {
            $health = Invoke-RestMethod -Uri "$baseUrl/api/healthz" -TimeoutSec 20
            if ($health.status -ne 'ok') {
                $eOk = $false; $eDetails += "/api/healthz: status='$($health.status)', atteso 'ok'"
            }
        } catch {
            $eOk = $false; $eDetails += "/api/healthz non raggiungibile: $($_.Exception.Message)"
        }

        Add-StepResult -Name 'e. UI e /api/healthz' -Ok $eOk -Detail ($eDetails -join '; ')

        # --- e2. identity attraverso il gateway (GIT-36) -----------------
        Write-Log "==> Passo e2: identity attraverso il gateway (sessione assente, login con credenziali inventate) ..."
        $e2Ok = $true
        $e2Details = @()

        $sess = Invoke-HttpRaw -Uri "$baseUrl/api/v1/auth/session"
        if ($sess.StatusCode -ne 401 -or $sess.Body -notmatch '"code"\s*:\s*"unauthenticated"') {
            $e2Ok = $false
            $e2Details += "GET /api/v1/auth/session senza cookie: status $($sess.StatusCode) (atteso 401 unauthenticated), corpo '$($sess.Body)' $($sess.Error)"
        }

        $loginBody = @{ username = 'e2e-nessun-utente'; password = 'e2e-password-inventata' } | ConvertTo-Json
        $login = Invoke-HttpRaw -Uri "$baseUrl/api/v1/auth/login" -Method 'POST' -Body $loginBody
        if ($login.StatusCode -ne 401 -or $login.Body -notmatch '"code"\s*:\s*"invalid_credentials"') {
            $e2Ok = $false
            $e2Details += "POST /api/v1/auth/login con credenziali inventate: status $($login.StatusCode) (atteso 401 invalid_credentials), corpo '$($login.Body)' $($login.Error)"
        }

        # /internal/* non deve essere raggiungibile dal gateway.
        $internal = Invoke-HttpRaw -Uri "$baseUrl/api/v1/internal/verify" -Method 'POST' -Body '{}'
        if ($internal.StatusCode -ne 404) {
            $e2Ok = $false
            $e2Details += "POST /api/v1/internal/verify: status $($internal.StatusCode), atteso 404 (interfaccia interna non esposta)"
        }

        Add-StepResult -Name 'e2. identity via gateway: sessione assente 401 unauthenticated, login inventato 401 invalid_credentials, /internal non esposto' -Ok $e2Ok -Detail ($e2Details -join '; ')

        # --- e3. admin del primo avvio via gateway (GIT-35) ---------------
        Write-Log "==> Passo e3: login admin con la password del Secret, cambio obbligatorio, chiamata autenticata (la password non viene mai stampata) ..."
        $e3Ok = $true
        $e3Details = @()
        $adminNewPassword = 'E2e-nuova-password-lunga-42'
        $adminInitialPassword = $null
        $ck = $null
        $adminPwRes = Invoke-RemoteHelper -RemoteArgs @('admin-password', 'gitstack', 'default') -TimeoutSeconds 30
        if ($adminPwRes.ExitCode -ne 0 -or [string]::IsNullOrEmpty($adminPwRes.StdOut)) {
            $e3Ok = $false
            $e3Details += "impossibile leggere la password iniziale dal Secret gitstack-identity-admin (exit $($adminPwRes.ExitCode))"
        } else {
            $adminInitialPassword = $adminPwRes.StdOut.TrimEnd("`r", "`n")
            $adminLoginBody = @{ username = 'admin'; password = $adminInitialPassword } | ConvertTo-Json
            $adminLogin = Invoke-HttpRaw -Uri "$baseUrl/api/v1/auth/login" -Method 'POST' -Body $adminLoginBody
            if ($adminLogin.StatusCode -ne 200 -or -not $adminLogin.Cookie -or $adminLogin.Body -notmatch '"mustChangePassword"\s*:\s*true') {
                $e3Ok = $false
                $e3Details += "login admin: status $($adminLogin.StatusCode) (atteso 200 con mustChangePassword=true e cookie di sessione) $($adminLogin.Error)"
            } else {
                $ck = $adminLogin.Cookie
                $blocked = Invoke-HttpRaw -Uri "$baseUrl/api/v1/users" -Cookie $ck
                if ($blocked.StatusCode -ne 403 -or $blocked.Body -notmatch '"code"\s*:\s*"password_change_required"') {
                    $e3Ok = $false
                    $e3Details += "GET /api/v1/users prima del cambio: status $($blocked.StatusCode) (atteso 403 password_change_required)"
                }
                $changeBody = @{ currentPassword = $adminInitialPassword; newPassword = $adminNewPassword } | ConvertTo-Json
                $change = Invoke-HttpRaw -Uri "$baseUrl/api/v1/users/admin/password" -Method 'PUT' -Body $changeBody -Cookie $ck
                if ($change.StatusCode -ne 204) {
                    $e3Ok = $false
                    $e3Details += "PUT /api/v1/users/admin/password: status $($change.StatusCode) (atteso 204)"
                } else {
                    $authed = Invoke-HttpRaw -Uri "$baseUrl/api/v1/users" -Cookie $ck
                    if ($authed.StatusCode -ne 200) {
                        $e3Ok = $false
                        $e3Details += "GET /api/v1/users dopo il cambio: status $($authed.StatusCode) (atteso 200)"
                    }
                }
            }
        }
        Add-StepResult -Name 'e3. admin via gateway: login con password del Secret, 403 password_change_required, cambio password, chiamata autenticata 200' -Ok $e3Ok -Detail ($e3Details -join '; ')

        # --- e4. risorsa di prova autenticata + evento JetStream (GIT-54) ---
        # Dal GIT-54 il gateway autentica ogni rotta tranne le pubbliche: la
        # risorsa di prova si crea e si legge con la sessione dell'admin
        # (password gia cambiata nel passo e3), e senza credenziali risponde
        # 401 unauthenticated.
        Write-Log "==> Passo e4: risorsa di prova senza credenziali (401) e con la sessione dell'admin, evento JetStream ..."
        $e4Ok = $true
        $e4Details = @()

        $anon = Invoke-HttpRaw -Uri "$baseUrl/api/v1/resources"
        if ($anon.StatusCode -ne 401 -or $anon.Body -notmatch '"code"\s*:\s*"unauthenticated"') {
            $e4Ok = $false
            $e4Details += "GET /api/v1/resources senza credenziali: status $($anon.StatusCode) (atteso 401 unauthenticated), corpo '$($anon.Body)' $($anon.Error)"
        }

        $baselineCount = $null
        if (-not $ck) {
            $e4Ok = $false
            $e4Details += "manca la sessione dell'admin (passo e3 fallito): impossibile creare la risorsa di prova"
        } else {
            $baseline = Invoke-RemoteHelper -RemoteArgs @('jetstream-count', 'CORE', 'gitstack', 'default') -TimeoutSeconds 60
            if ($baseline.ExitCode -ne 0 -or -not ($baseline.StdOut.Trim() -match '^\d+$')) {
                $e4Ok = $false; $e4Details += "conteggio JetStream (baseline) non riuscito: $($baseline.StdOut) $($baseline.StdErr)"
            } else {
                $baselineCount = [int]$baseline.StdOut.Trim()
            }
        }

        $resourceId = $null
        if ($e4Ok) {
            $createBody = @{ type = 'gitstack-e2e'; name = "e2e-$(Get-Date -Format 'yyyyMMddHHmmss')" } | ConvertTo-Json
            $created = Invoke-HttpRaw -Uri "$baseUrl/api/v1/resources" -Method 'POST' -Body $createBody -Cookie $ck
            if ($created.StatusCode -ne 201) {
                $e4Ok = $false; $e4Details += "create risorsa con la sessione: status $($created.StatusCode) (atteso 201), corpo '$($created.Body)' $($created.Error)"
            } else {
                $resourceId = ($created.Body | ConvertFrom-Json).id
                if (-not $resourceId) { $e4Ok = $false; $e4Details += "create risorsa: risposta senza 'id'" }
            }
        }

        if ($e4Ok -and $resourceId) {
            $read = Invoke-HttpRaw -Uri "$baseUrl/api/v1/resources/$resourceId" -Cookie $ck
            if ($read.StatusCode -ne 200 -or ($read.Body | ConvertFrom-Json).id -ne $resourceId) {
                $e4Ok = $false; $e4Details += "read risorsa ${resourceId}: status $($read.StatusCode), corpo '$($read.Body)' $($read.Error)"
            }
        }

        if ($e4Ok -and $null -ne $baselineCount) {
            # core pubblica l'evento di prova in una goroutine DOPO aver
            # risposto 201 (services/core/internal/httpserver/resources.go,
            # publishTestResourceCreated, timeout 5s): una lettura sola del
            # conteggio JetStream subito dopo la create rischia un FAIL
            # spurio per una corsa persa, non per un bug reale. Polling con
            # backoff fisso: si ferma al primo tentativo che supera la
            # baseline, o dopo -JetStreamPollAttempts tentativi.
            $jsOk = $false
            $jsLastDetail = ''
            for ($attempt = 1; $attempt -le $JetStreamPollAttempts; $attempt++) {
                $after = Invoke-RemoteHelper -RemoteArgs @('jetstream-count', 'CORE', 'gitstack', 'default') -TimeoutSeconds 60
                if ($after.ExitCode -ne 0 -or -not ($after.StdOut.Trim() -match '^\d+$')) {
                    $jsLastDetail = "conteggio JetStream (tentativo $attempt/$JetStreamPollAttempts) non riuscito: $($after.StdOut) $($after.StdErr)"
                    Write-Log $jsLastDetail
                } else {
                    $afterCount = [int]$after.StdOut.Trim()
                    Write-Log "Stream CORE (tentativo $attempt/$JetStreamPollAttempts): messaggi $baselineCount -> $afterCount"
                    if ($afterCount -gt $baselineCount) {
                        $jsOk = $true
                        break
                    }
                    $jsLastDetail = "stream CORE: messaggi non ancora aumentati dopo la create (prima: $baselineCount, ultimo: $afterCount)"
                }
                if ($attempt -lt $JetStreamPollAttempts) {
                    Start-Sleep -Seconds $JetStreamPollIntervalSeconds
                }
            }
            if (-not $jsOk) {
                $e4Ok = $false
                $e4Details += "$jsLastDetail dopo $JetStreamPollAttempts tentativi ogni ${JetStreamPollIntervalSeconds}s"
            }
        }

        Add-StepResult -Name 'e4. risorsa di prova: 401 senza credenziali, create+read con la sessione, evento JetStream' -Ok $e4Ok -Detail ($e4Details -join '; ')

        # --- e5. git reale: repo via API, clone e push via HTTPS e SSH (GIT-77) ---
        # Dall'host Windows, con il git e l'ssh del board: utente di prova con
        # token e chiave SSH (porta 2222 della VM), repo creato via API, push
        # via HTTPS (ingress, token in Basic auth) e via SSH, clone dell'altro
        # protocollo che vede il commit, e un clone anonimo che deve fallire.
        Write-Log "==> Passo e5: repo via API, clone e push via HTTPS e SSH (porta $GitSshPort) ..."
        $e5Ok = $true
        $e5Details = @()
        $e5Stage = 'preparazione'
        $savedEnv = @{}
        foreach ($n in 'GIT_TERMINAL_PROMPT', 'GCM_INTERACTIVE', 'GIT_SSH_COMMAND', 'GIT_AUTHOR_NAME', 'GIT_AUTHOR_EMAIL', 'GIT_COMMITTER_NAME', 'GIT_COMMITTER_EMAIL') {
            $savedEnv[$n] = [Environment]::GetEnvironmentVariable($n)
        }
        try {
            if (-not $ck) { throw "manca la sessione dell'admin (passo e3 fallito)" }
            if (-not (Get-Command git -ErrorAction SilentlyContinue)) { throw "git non e nel PATH dell'host" }
            $gitWork = Join-Path $OutDir 'git-e5'
            New-Item -ItemType Directory -Path $gitWork -Force | Out-Null
            $suffix = Get-Date -Format 'HHmmss'
            $gitUser = "e2egit$suffix"
            $gitPassword = 'E2e-git-password-lunga-42'
            $gitRepo = "prova-$suffix"

            $e5Stage = 'utente di prova'
            $mkUser = Invoke-HttpRaw -Uri "$baseUrl/api/v1/users" -Method 'POST' -Cookie $ck -Body (@{ username = $gitUser; email = "$gitUser@example.com"; password = $gitPassword } | ConvertTo-Json)
            if ($mkUser.StatusCode -ne 201) { throw "creazione utente: status $($mkUser.StatusCode), corpo '$($mkUser.Body)' $($mkUser.Error)" }
            $uLogin = Invoke-HttpRaw -Uri "$baseUrl/api/v1/auth/login" -Method 'POST' -Body (@{ username = $gitUser; password = $gitPassword } | ConvertTo-Json)
            if ($uLogin.StatusCode -ne 200 -or -not $uLogin.Cookie) { throw "login utente di prova: status $($uLogin.StatusCode) $($uLogin.Body)" }
            $uck = $uLogin.Cookie
            $expires = (Get-Date).ToUniversalTime().AddHours(2).ToString('yyyy-MM-ddTHH:mm:ssZ')
            $mkTok = Invoke-HttpRaw -Uri "$baseUrl/api/v1/user/tokens" -Method 'POST' -Cookie $uck -Body (@{ name = 'e2e'; scopes = @('read:resource', 'write:resource'); expiresAt = $expires } | ConvertTo-Json)
            if ($mkTok.StatusCode -ne 201) { throw "creazione token: status $($mkTok.StatusCode) $($mkTok.Body)" }
            $gitToken = ($mkTok.Body | ConvertFrom-Json).token
            if (-not $gitToken) { throw "creazione token: risposta senza 'token'" }

            $sshKey = Join-Path $gitWork 'id_e2e'
            & ssh-keygen -q -t ed25519 -N '""' -C 'e2e-git' -f $sshKey | Out-Null
            if (-not (Test-Path -LiteralPath "$sshKey.pub")) { throw "ssh-keygen non ha creato la chiave" }
            $pub = (Get-Content -Raw -LiteralPath "$sshKey.pub").Trim()
            $mkKey = Invoke-HttpRaw -Uri "$baseUrl/api/v1/user/ssh-keys" -Method 'POST' -Cookie $uck -Body (@{ title = 'e2e'; publicKey = $pub } | ConvertTo-Json)
            if ($mkKey.StatusCode -ne 201) { throw "registrazione chiave SSH: status $($mkKey.StatusCode) $($mkKey.Body)" }

            $e5Stage = 'creazione del repo'
            $bearer = @{ Authorization = "Bearer $gitToken" }
            $mkRepo = Invoke-HttpRaw -Uri "$baseUrl/api/v1/repos" -Method 'POST' -Headers $bearer -Body (@{ owner = $gitUser; name = $gitRepo; visibility = 'private' } | ConvertTo-Json)
            if ($mkRepo.StatusCode -ne 201) { throw "POST /api/v1/repos: status $($mkRepo.StatusCode), corpo '$($mkRepo.Body)' $($mkRepo.Error)" }

            $env:GIT_TERMINAL_PROMPT = '0'
            $env:GCM_INTERACTIVE = 'never'
            $env:GIT_AUTHOR_NAME = 'E2E'; $env:GIT_AUTHOR_EMAIL = 'e2e@example.com'
            $env:GIT_COMMITTER_NAME = 'E2E'; $env:GIT_COMMITTER_EMAIL = 'e2e@example.com'
            $knownHosts = (Join-Path $gitWork 'known_hosts') -replace '\\', '/'
            $env:GIT_SSH_COMMAND = "ssh -i $($sshKey -replace '\\', '/') -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=$knownHosts -o LogLevel=ERROR"
            # HTTPS: git si fida solo della CA interna scaricata in d3 (backend OpenSSL).
            $caForGit = $caPath.Replace('\', '/')
            $gitBase = @('-c', 'credential.helper=', '-c', 'core.autocrlf=false', '-c', 'http.sslBackend=openssl', '-c', "http.sslCAInfo=$caForGit")
            $httpsUrl = "https://${gitUser}:$gitToken@$($script:VmIp)/$gitUser/$gitRepo.git"
            $sshUrl = "ssh://git@$($script:VmIp):$GitSshPort/$gitUser/$gitRepo.git"

            $e5Stage = 'push via HTTPS'
            $w1 = Join-Path $gitWork 'w1'
            New-Item -ItemType Directory -Path $w1 -Force | Out-Null
            foreach ($g in @(
                    @('init', '-q', '-b', 'main', $w1),
                    @('-C', $w1, 'remote', 'add', 'origin', $httpsUrl))) {
                $r = Invoke-ExternalCommand -FilePath 'git' -ArgumentList ($gitBase + $g) -TimeoutSeconds 60
                if ($r.ExitCode -ne 0) { throw "git $($g[0]): exit $($r.ExitCode) $($r.StdErr)" }
            }
            Set-Content -LiteralPath (Join-Path $w1 'https.txt') -Value 'da https' -Encoding ascii
            foreach ($g in @(@('-C', $w1, 'add', '-A'), @('-C', $w1, 'commit', '-q', '-m', 'push https'), @('-C', $w1, 'push', '-q', '-u', 'origin', 'main'))) {
                $r = Invoke-ExternalCommand -FilePath 'git' -ArgumentList ($gitBase + $g) -TimeoutSeconds 120
                if ($r.ExitCode -ne 0) { throw "git $($g[2]) via HTTPS: exit $($r.ExitCode) $($r.StdOut) $($r.StdErr)" }
            }

            $e5Stage = 'clone e push via SSH'
            $w2 = Join-Path $gitWork 'w2'
            $r = Invoke-ExternalCommand -FilePath 'git' -ArgumentList ($gitBase + @('clone', '-q', $sshUrl, $w2)) -TimeoutSeconds 120
            if ($r.ExitCode -ne 0) { throw "git clone via SSH: exit $($r.ExitCode) $($r.StdOut) $($r.StdErr)" }
            if (-not (Test-Path -LiteralPath (Join-Path $w2 'https.txt'))) { throw "il clone SSH non ha il file spinto via HTTPS" }
            Set-Content -LiteralPath (Join-Path $w2 'ssh.txt') -Value 'da ssh' -Encoding ascii
            foreach ($g in @(@('-C', $w2, 'add', '-A'), @('-C', $w2, 'commit', '-q', '-m', 'push ssh'), @('-C', $w2, 'push', '-q', 'origin', 'main'))) {
                $r = Invoke-ExternalCommand -FilePath 'git' -ArgumentList ($gitBase + $g) -TimeoutSeconds 120
                if ($r.ExitCode -ne 0) { throw "git $($g[2]) via SSH: exit $($r.ExitCode) $($r.StdOut) $($r.StdErr)" }
            }

            $e5Stage = 'pull via HTTPS del commit spinto via SSH'
            $r = Invoke-ExternalCommand -FilePath 'git' -ArgumentList ($gitBase + @('-C', $w1, 'pull', '-q', '--ff-only', 'origin', 'main')) -TimeoutSeconds 120
            if ($r.ExitCode -ne 0) { throw "git pull via HTTPS: exit $($r.ExitCode) $($r.StdOut) $($r.StdErr)" }
            if (-not (Test-Path -LiteralPath (Join-Path $w1 'ssh.txt'))) { throw "il pull HTTPS non ha portato il commit spinto via SSH" }

            $e5Stage = 'accesso negato senza credenziali'
            $anonUrl = "https://$($script:VmIp)/$gitUser/$gitRepo.git"
            $r = Invoke-ExternalCommand -FilePath 'git' -ArgumentList ($gitBase + @('clone', '-q', $anonUrl, (Join-Path $gitWork 'anon'))) -TimeoutSeconds 60
            if ($r.ExitCode -eq 0) { throw "il clone senza credenziali e riuscito" }
            if (Test-Path -LiteralPath (Join-Path $gitWork 'anon\https.txt')) { throw "il clone senza credenziali ha portato dati del repo" }
        } catch {
            $e5Ok = $false
            $e5Details += "[$e5Stage] $($_.Exception.Message)"
        } finally {
            foreach ($n in $savedEnv.Keys) { [Environment]::SetEnvironmentVariable($n, $savedEnv[$n]) }
        }
        Add-StepResult -Name 'e5. git reale: repo via API, clone/push/pull via HTTPS (token) e via SSH (porta 2222), clone anonimo negato' -Ok $e5Ok -Detail ($e5Details -join '; ')

        # --- e6. browser del codice: tree, contents, raw, commits via gateway (GIT-116, M-04) ---
        # Sul repo privato del passo e5, con il suo token (read:resource): aggiunge
        # page.html e image.svg (con <script>) via HTTPS e verifica le API di lettura.
        # Il raw non deve mai uscire come pagina eseguibile (regola B3).
        Write-Log "==> Passo e6: browser del codice (tree, contents, raw, commits) ..."
        $e6Ok = $true
        $e6Details = @()
        $e6Stage = 'preparazione'
        $savedEnv6 = @{}
        foreach ($n in 'GIT_TERMINAL_PROMPT', 'GCM_INTERACTIVE', 'GIT_AUTHOR_NAME', 'GIT_AUTHOR_EMAIL', 'GIT_COMMITTER_NAME', 'GIT_COMMITTER_EMAIL') {
            $savedEnv6[$n] = [Environment]::GetEnvironmentVariable($n)
        }
        try {
            if (-not $e5Ok) { throw 'e5 non riuscito' }
            $repoApi = "$baseUrl/api/v1/repos/$gitUser/$gitRepo"

            $e6Stage = 'push di page.html e image.svg via HTTPS'
            $env:GIT_TERMINAL_PROMPT = '0'
            $env:GCM_INTERACTIVE = 'never'
            $env:GIT_AUTHOR_NAME = 'E2E'; $env:GIT_AUTHOR_EMAIL = 'e2e@example.com'
            $env:GIT_COMMITTER_NAME = 'E2E'; $env:GIT_COMMITTER_EMAIL = 'e2e@example.com'
            Set-Content -LiteralPath (Join-Path $w1 'page.html') -Value '<script>alert(1)</script>' -Encoding ascii
            Set-Content -LiteralPath (Join-Path $w1 'image.svg') -Value '<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><script>alert(1)</script><rect width="10" height="10"/></svg>' -Encoding ascii
            foreach ($g in @(@('-C', $w1, 'add', '-A'), @('-C', $w1, 'commit', '-q', '-m', 'aggiunge page.html e image.svg'), @('-C', $w1, 'push', '-q', 'origin', 'main'))) {
                $r = Invoke-ExternalCommand -FilePath 'git' -ArgumentList ($gitBase + $g) -TimeoutSeconds 120
                if ($r.ExitCode -ne 0) { throw "git $($g[2]) via HTTPS: exit $($r.ExitCode) $($r.StdOut) $($r.StdErr)" }
            }

            $e6Stage = '2. tree della radice'
            $tr = Invoke-HttpRaw -Uri "$repoApi/tree" -Headers $bearer
            if ($tr.StatusCode -ne 200) { throw "status $($tr.StatusCode), corpo '$($tr.Body)' $($tr.Error)" }
            $names = @(($tr.Body | ConvertFrom-Json).entries | ForEach-Object { $_.name })
            foreach ($want in 'https.txt', 'ssh.txt', 'page.html', 'image.svg') {
                if ($names -notcontains $want) { throw "manca $want nella radice (voci: $($names -join ', '))" }
            }

            $e6Stage = '3. contents di https.txt'
            $ct = Invoke-HttpRaw -Uri "$repoApi/contents?path=https.txt" -Headers $bearer
            if ($ct.StatusCode -ne 200) { throw "status $($ct.StatusCode), corpo '$($ct.Body)' $($ct.Error)" }
            $fileContent = [string](($ct.Body | ConvertFrom-Json).content)
            if ($fileContent.Trim() -ne 'da https') { throw "content '$fileContent' diverso da 'da https'" }

            $e6Stage = '4. raw di https.txt'
            $rw = Invoke-HttpRaw -Uri "$repoApi/raw?path=https.txt" -Headers $bearer
            if ($rw.StatusCode -ne 200) { throw "status $($rw.StatusCode), corpo '$($rw.Body)' $($rw.Error)" }
            if ($rw.Headers['Content-Type'] -notmatch '^text/plain') { throw "Content-Type '$($rw.Headers['Content-Type'])', atteso text/plain" }
            if ($rw.Headers['X-Content-Type-Options'] -ne 'nosniff') { throw "X-Content-Type-Options '$($rw.Headers['X-Content-Type-Options'])', atteso nosniff" }
            if ($rw.Headers['Content-Security-Policy'] -notmatch 'sandbox') { throw "Content-Security-Policy '$($rw.Headers['Content-Security-Policy'])' senza sandbox" }

            $e6Stage = '5a. raw di page.html (testo: text/plain, mai eseguibile)'
            $rw = Invoke-HttpRaw -Uri "$repoApi/raw?path=page.html" -Headers $bearer
            if ($rw.StatusCode -ne 200) { throw "page.html status $($rw.StatusCode), corpo '$($rw.Body)' $($rw.Error)" }
            $ctype = [string]$rw.Headers['Content-Type']
            if ($ctype -match 'text/html|image/svg+xml') { throw "page.html servito come '$ctype'" }
            if ($ctype -notmatch '^text/plain') { throw "page.html Content-Type '$ctype', atteso text/plain" }
            if ($rw.Headers['X-Content-Type-Options'] -ne 'nosniff') { throw "page.html X-Content-Type-Options '$($rw.Headers['X-Content-Type-Options'])', atteso nosniff" }
            if ($rw.Headers['Content-Security-Policy'] -notmatch 'sandbox') { throw "page.html Content-Security-Policy '$($rw.Headers['Content-Security-Policy'])' senza sandbox" }

            $e6Stage = '5b. raw di image.svg (octet-stream, attachment)'
            $rw = Invoke-HttpRaw -Uri "$repoApi/raw?path=image.svg" -Headers $bearer
            if ($rw.StatusCode -ne 200) { throw "image.svg status $($rw.StatusCode), corpo '$($rw.Body)' $($rw.Error)" }
            $ctype = [string]$rw.Headers['Content-Type']
            if ($ctype -match 'text/html|image/svg+xml') { throw "image.svg servito come '$ctype'" }
            if ($ctype -notmatch '^application/octet-stream') { throw "image.svg Content-Type '$ctype', atteso application/octet-stream" }
            if ([string]$rw.Headers['Content-Disposition'] -notmatch '^attachment') { throw "image.svg Content-Disposition '$($rw.Headers['Content-Disposition'])', atteso attachment" }
            if ($rw.Headers['X-Content-Type-Options'] -ne 'nosniff') { throw "image.svg X-Content-Type-Options '$($rw.Headers['X-Content-Type-Options'])', atteso nosniff" }
            if ($rw.Headers['Content-Security-Policy'] -notmatch 'sandbox') { throw "image.svg Content-Security-Policy '$($rw.Headers['Content-Security-Policy'])' senza sandbox" }

            $e6Stage = '6. commits e dettaglio del commit'
            $cm = Invoke-HttpRaw -Uri "$repoApi/commits" -Headers $bearer
            if ($cm.StatusCode -ne 200) { throw "status $($cm.StatusCode), corpo '$($cm.Body)' $($cm.Error)" }
            $commits = @(($cm.Body | ConvertFrom-Json).items)
            if ($commits.Count -lt 3) { throw "attesi almeno 3 commit, trovati $($commits.Count)" }
            $headSha = [string]$commits[0].sha
            $headFiles = @()
            $cd = Invoke-HttpRaw -Uri "$repoApi/commits/$headSha" -Headers $bearer
            if ($cd.StatusCode -ne 200) { throw "dettaglio $headSha status $($cd.StatusCode), corpo '$($cd.Body)' $($cd.Error)" }
            $detail = $cd.Body | ConvertFrom-Json
            if ([string]$detail.commit.sha -ne $headSha) { throw "dettaglio con sha '$($detail.commit.sha)', atteso $headSha" }
            $headFiles = @($detail.files | ForEach-Object { $_.path })
            if ($headFiles -notcontains 'page.html') { throw "il commit piu recente non contiene page.html (file: $($headFiles -join ', '))" }
            if ($headFiles -notcontains 'image.svg') { throw "il commit piu recente non contiene image.svg (file: $($headFiles -join ', '))" }

            $e6Stage = '7. senza credenziali'
            foreach ($u in @("$repoApi/tree", "$repoApi/contents?path=https.txt", "$repoApi/raw?path=https.txt")) {
                $an = Invoke-HttpRaw -Uri $u
                if ($an.StatusCode -ne 401 -and $an.StatusCode -ne 404) { throw "$u senza credenziali: status $($an.StatusCode), atteso 401 o 404" }
                foreach ($leak in 'da https', 'https.txt', 'ssh.txt', 'page.html', 'image.svg') {
                    if ($an.Body -match [regex]::Escape($leak)) { throw "$u senza credenziali: il corpo contiene '$leak'" }
                }
            }
        } catch {
            $e6Ok = $false
            $e6Details += "[$e6Stage] $($_.Exception.Message)"
        } finally {
            foreach ($n in $savedEnv6.Keys) { [Environment]::SetEnvironmentVariable($n, $savedEnv6[$n]) }
        }
        Add-StepResult -Name 'e6. browser del codice: tree, contents, raw (https.txt e page.html text/plain; image.svg octet-stream+attachment; nosniff, sandbox), commits, nessun dato senza credenziali' -Ok $e6Ok -Detail ($e6Details -join '; ')

        # --- e7. prefisso API della UI, con login (GIT-151) ---------------
        # La UI chiama <API_BASE_URL>/<percorso> (web/src/lib/http.ts), non
        # /api/v1 scritto a mano: il prefisso si legge dal sorgente. Con '/api'
        # l'Ingress toglie /api e il gateway riceve /auth/session (404). Il
        # passo e3 ha gia cambiato la password dell'admin: si prova prima
        # quella nuova, poi la iniziale del Secret (mai stampate).
        Write-Log "==> Passo e7: prefisso API della UI (sessione 401, login admin, sessione 200 col cookie) ..."
        $e7Ok = $true
        $e7Details = @()
        $uiHttpTs = Join-Path $PSScriptRoot '..\..\web\src\lib\http.ts'
        $uiPrefix = $null
        if (Test-Path -LiteralPath $uiHttpTs) {
            $m7 = Select-String -LiteralPath $uiHttpTs -Pattern "^export const API_BASE_URL = '([^']*)';" | Select-Object -First 1
            if ($m7) { $uiPrefix = $m7.Matches[0].Groups[1].Value }
        }
        if (-not $uiPrefix) {
            $e7Ok = $false
            $e7Details += "API_BASE_URL non trovato in web/src/lib/http.ts"
        } else {
            $uiBase = "$baseUrl$uiPrefix"
            $uiSess = Invoke-HttpRaw -Uri "$uiBase/auth/session"
            $uiCt = if ($uiSess.Headers -and $uiSess.Headers['content-type']) { [string]$uiSess.Headers['content-type'] } else { '' }
            if ($uiSess.StatusCode -ne 401 -or $uiSess.Body -notmatch 'unauthenticated' -or $uiCt -notmatch 'application/json') {
                $e7Ok = $false
                $e7Details += "GET $uiPrefix/auth/session senza cookie: status $($uiSess.StatusCode) content-type '$uiCt' (atteso 401 JSON unauthenticated), corpo '$($uiSess.Body)' $($uiSess.Error)"
            }
            $uiCookie = $null
            $uiLastStatus = $null
            foreach ($candidate in @($adminNewPassword, $adminInitialPassword)) {
                if ([string]::IsNullOrEmpty($candidate)) { continue }
                $uiLogin = Invoke-HttpRaw -Uri "$uiBase/auth/login" -Method 'POST' -Body (@{ username = 'admin'; password = $candidate } | ConvertTo-Json)
                $uiLastStatus = $uiLogin.StatusCode
                if ($uiLogin.StatusCode -eq 200 -and $uiLogin.Cookie) { $uiCookie = $uiLogin.Cookie; break }
            }
            if (-not $uiCookie) {
                $e7Ok = $false
                $e7Details += "POST $uiPrefix/auth/login dell'admin: ultimo status $uiLastStatus (atteso 200 con cookie di sessione)"
            } else {
                $uiSess2 = Invoke-HttpRaw -Uri "$uiBase/auth/session" -Cookie $uiCookie
                if ($uiSess2.StatusCode -ne 200 -or $uiSess2.Body -notmatch '"username"') {
                    $e7Ok = $false
                    $e7Details += "GET $uiPrefix/auth/session col cookie: status $($uiSess2.StatusCode) (atteso 200), corpo '$($uiSess2.Body)'"
                }
            }
        }
        Add-StepResult -Name 'e7. prefisso API della UI (letto da web/src/lib/http.ts): sessione 401 JSON, login admin, sessione 200 col cookie' -Ok $e7Ok -Detail ($e7Details -join '; ')

        # --- e8. gs da /downloads: installazione, checksum e ciclo (GIT-173, M-07/L) ---
        # Sulla VM (Linux): install-gs.sh scarica gs da /downloads (impronta
        # della CA verificata, checksum di SHA256SUMS), poi con GS_HOST/GS_TOKEN
        # fa repo create, clone, issue create, commit con `fixes #n` e push,
        # issue chiusa, notifiche lette e `gs api` su un endpoint admin
        # (e2e/gs-cycle.sh). L'utente di prova e il suo token sono quelli di e5.
        Write-Log "==> Passo e8: gs da /downloads (install-gs.sh, checksum) e ciclo repo/issue/push/notifiche/api ..."
        $e8Ok = $true
        $e8Details = @()
        $e8Stage = 'preparazione'
        try {
            if (-not $e5Ok -or -not $gitToken -or -not $gitUser) { throw 'e5 non riuscito: manca l''utente di prova con il token' }
            if (-not $script:CaFingerprint) { throw 'manca l''impronta della CA (passo d3 fallito)' }
            if (-not $ck) { throw "manca la sessione dell'admin (passo e3 fallito)" }

            $e8Stage = 'utente agente e token admin'
            $agentName = "e2eagent$suffix"
            $mkAgent = Invoke-HttpRaw -Uri "$baseUrl/api/v1/users" -Method 'POST' -Cookie $ck -Body (@{ username = $agentName; kind = 'agent'; email = "$agentName@agents.example.com" } | ConvertTo-Json)
            if ($mkAgent.StatusCode -ne 201) { throw "creazione utente agente: status $($mkAgent.StatusCode), corpo '$($mkAgent.Body)' $($mkAgent.Error)" }
            $e8Expires = (Get-Date).ToUniversalTime().AddHours(2).ToString('yyyy-MM-ddTHH:mm:ssZ')
            $mkAdminTok = Invoke-HttpRaw -Uri "$baseUrl/api/v1/user/tokens" -Method 'POST' -Cookie $ck -Body (@{ name = 'e2e-gs-admin'; scopes = @('read:user', 'write:user'); expiresAt = $e8Expires } | ConvertTo-Json)
            if ($mkAdminTok.StatusCode -ne 201) { throw "token dell'admin: status $($mkAdminTok.StatusCode) $($mkAdminTok.Body)" }
            $adminGsToken = ($mkAdminTok.Body | ConvertFrom-Json).token
            if (-not $adminGsToken) { throw "token dell'admin: risposta senza 'token'" }

            $e8Stage = 'copia di gs-cycle.sh sulla VM'
            $mk8 = Invoke-VmSsh -Command 'mkdir -p /tmp/gitstack-e2e' -TimeoutSeconds $SshCommandTimeoutSeconds
            if ($mk8.ExitCode -ne 0) { throw "mkdir sulla VM: exit $($mk8.ExitCode) $($mk8.StdErr)" }
            $cp8 = Copy-ToVm -LocalPath (Join-Path $PSScriptRoot 'e2e\gs-cycle.sh') -RemotePath '/tmp/gitstack-e2e/gs-cycle.sh' -TimeoutSeconds $SshCommandTimeoutSeconds
            if ($cp8.ExitCode -ne 0) { throw "scp di gs-cycle.sh: exit $($cp8.ExitCode) $($cp8.StdErr)" }

            $e8Stage = 'ciclo di gs sulla VM'
            # I segreti viaggiano nell'ambiente del comando remoto, mai negli argomenti stampati nel log.
            $e8Env = @(
                "GS_VM_IP='$($script:VmIp)'", "GS_CA_SHA256='$($script:CaFingerprint)'",
                "GS_USER_TOKEN='$gitToken'", "GS_ADMIN_TOKEN='$adminGsToken'",
                "GS_USER_NAME='$gitUser'", "GS_REPO_NAME='gs-ciclo-$suffix'", "GS_AGENT_NAME='$agentName'"
            ) -join ' '
            $run8 = Invoke-VmSsh -Command "$e8Env bash /tmp/gitstack-e2e/gs-cycle.sh" -TimeoutSeconds 600
            Set-Content -LiteralPath (Join-Path $OutDir 'gs-cycle.log') -Value ($run8.StdOut + "`n" + $run8.StdErr) -Encoding utf8
            if ($run8.ExitCode -ne 0 -or $run8.StdOut -notmatch 'GS-CYCLE-DONE') {
                $failLine = ($run8.StdOut -split "`n" | Where-Object { $_ -match '^STEP FAIL' } | Select-Object -First 1)
                throw "gs-cycle.sh: exit $($run8.ExitCode) $failLine $($run8.StdErr)"
            }
            foreach ($stepName in 'install', 'checksum', 'version', 'auth_status', 'repo_create', 'clone', 'issue_create', 'push', 'issue_chiusa', 'notification', 'gs_api_admin') {
                if ($run8.StdOut -notmatch "STEP ok $stepName\b") { throw "manca il passo '$stepName' nell'output di gs-cycle.sh" }
            }
        } catch {
            $e8Ok = $false
            $e8Details += "[$e8Stage] $($_.Exception.Message)"
        }
        Add-StepResult -Name 'e8. gs da /downloads (install-gs.sh, checksum) e ciclo con GS_HOST/GS_TOKEN: repo, clone, issue, fixes #n, notifiche, gs api admin' -Ok $e8Ok -Detail ($e8Details -join '; ')

        # --- f. idempotenza -----------------------------------------------
        Write-Log "==> Passo f: idempotenza (seconda esecuzione dell'installer, senza reset) ..."
        $adminStateBefore = Invoke-RemoteHelper -RemoteArgs @('admin-secret-state', 'gitstack', 'default') -TimeoutSeconds 30
        $hashBefore = Invoke-RemoteHelper -RemoteArgs @('postgres-secret-hash', 'gitstack', 'default') -TimeoutSeconds 30
        $activeBefore = Invoke-RemoteHelper -RemoteArgs @('k3s-active-since') -TimeoutSeconds 30

        $install2 = Invoke-VmSsh -Command $installCmd -TimeoutSeconds $InstallTimeoutSeconds
        $install2Log = Join-Path $OutDir 'installer-run2.log'
        Set-Content -LiteralPath $install2Log -Value $install2.StdOut -Encoding utf8
        Add-Content -LiteralPath $install2Log -Value $install2.StdErr

        $fOk = $true
        $fDetails = @()
        if ($install2.ExitCode -ne 0) {
            $fOk = $false; $fDetails += "seconda esecuzione dell'installer: exit $($install2.ExitCode) (log: $install2Log)"
        }
        if ($hashBefore.ExitCode -ne 0 -or $activeBefore.ExitCode -ne 0) {
            $fOk = $false; $fDetails += "impossibile leggere lo stato 'prima' (hash secret/k3s-active-since)"
        } else {
            $hashAfter = Invoke-RemoteHelper -RemoteArgs @('postgres-secret-hash', 'gitstack', 'default') -TimeoutSeconds 30
            $activeAfter = Invoke-RemoteHelper -RemoteArgs @('k3s-active-since') -TimeoutSeconds 30
            if ($hashAfter.ExitCode -ne 0 -or $activeAfter.ExitCode -ne 0) {
                $fOk = $false; $fDetails += "impossibile leggere lo stato 'dopo' (hash secret/k3s-active-since)"
            } else {
                if ($hashAfter.StdOut.Trim() -ne $hashBefore.StdOut.Trim()) {
                    $fOk = $false; $fDetails += "la password di Postgres e cambiata dopo la seconda esecuzione (hash diverso)"
                }
                if ($activeAfter.StdOut.Trim() -ne $activeBefore.StdOut.Trim()) {
                    $fOk = $false; $fDetails += "il servizio k3s e stato riavviato/reinstallato (ActiveEnterTimestamp diverso: '$($activeBefore.StdOut.Trim())' -> '$($activeAfter.StdOut.Trim())')"
                }
            }
        }
        if ($fOk -and $script:CaFingerprint) {
            # La CA e il suo certificato non cambiano a una riesecuzione: i client
            # che si fidano gia della CA non devono rifare niente.
            $fpAfter = Invoke-VmSsh -Command 'sudo gitstack-tls fingerprint' -TimeoutSeconds $SshCommandTimeoutSeconds
            if ($fpAfter.ExitCode -ne 0 -or $fpAfter.StdOut.Trim() -ne $script:CaFingerprint) {
                $fOk = $false; $fDetails += "l'impronta della CA e cambiata dopo la seconda esecuzione: '$($fpAfter.StdOut.Trim())' (prima $script:CaFingerprint)"
            }
        }
        if ($fOk) {
            try {
                $health2 = Invoke-RestMethod -Uri "$baseUrl/api/healthz" -TimeoutSec 20
                if ($health2.status -ne 'ok') { $fOk = $false; $fDetails += "/api/healthz dopo la seconda esecuzione: status='$($health2.status)'" }
            } catch {
                $fOk = $false; $fDetails += "/api/healthz dopo la seconda esecuzione non raggiungibile: $($_.Exception.Message)"
            }
        }
        Add-StepResult -Name 'f. idempotenza (seconda esecuzione, k3s non reinstallato, password invariata, healthz OK)' -Ok $fOk -Detail ($fDetails -join '; ')

        # --- f2. admin e Secret invariati dopo la seconda esecuzione (GIT-35) ---
        Write-Log "==> Passo f2: dopo la seconda esecuzione l'admin e il Secret sono invariati ..."
        $f2Ok = $true
        $f2Details = @()
        $adminStateAfter = Invoke-RemoteHelper -RemoteArgs @('admin-secret-state', 'gitstack', 'default') -TimeoutSeconds 30
        if ($adminStateBefore.ExitCode -ne 0 -or $adminStateAfter.ExitCode -ne 0) {
            $f2Ok = $false; $f2Details += "impossibile leggere lo stato del Secret dell'admin"
        } elseif ($adminStateBefore.StdOut.Trim() -ne $adminStateAfter.StdOut.Trim()) {
            $f2Ok = $false; $f2Details += "il Secret dell'admin e cambiato (resourceVersion/dati: '$($adminStateBefore.StdOut.Trim())' -> '$($adminStateAfter.StdOut.Trim())')"
        }
        if (-not $adminInitialPassword) {
            $f2Ok = $false; $f2Details += "manca la password iniziale (passo e3 fallito): impossibile provare l'admin"
        } else {
            # Admin non ricreato: la password iniziale non vale piu, quella nuova si.
            $oldTry = Invoke-HttpRaw -Uri "$baseUrl/api/v1/auth/login" -Method 'POST' -Body (@{ username = 'admin'; password = $adminInitialPassword } | ConvertTo-Json)
            if ($oldTry.StatusCode -ne 401) {
                $f2Ok = $false; $f2Details += "login con la password iniziale dopo la seconda esecuzione: status $($oldTry.StatusCode) (atteso 401: l'admin non va ricreato)"
            }
            $newTry = Invoke-HttpRaw -Uri "$baseUrl/api/v1/auth/login" -Method 'POST' -Body (@{ username = 'admin'; password = $adminNewPassword } | ConvertTo-Json)
            if ($newTry.StatusCode -ne 200 -or $newTry.Body -notmatch '"mustChangePassword"\s*:\s*false') {
                $f2Ok = $false; $f2Details += "login con la password cambiata dopo la seconda esecuzione: status $($newTry.StatusCode) (atteso 200 con mustChangePassword=false)"
            }
        }
        Add-StepResult -Name 'f2. seconda esecuzione: Secret admin invariato (resourceVersion e dati), admin non ricreato (password cambiata ancora valida)' -Ok $f2Ok -Detail ($f2Details -join '; ')

        # --- f3. comando di amministrazione gitstack (GIT-142) -----------
        # Il binario lo pubblica il job admin-binary della CI come asset della
        # release sha-<Ref>; l'installer (GITSTACK_ADMIN_REQUIRED=1) fallisce se
        # manca. Qui si prova che sia installato e che dica il vero.
        Write-Log "==> Passo f3: gitstack status sulla VM (con e senza root) ..."
        $f3Ok = $true
        $f3Details = @()
        $gsRoot = Invoke-VmSsh -Command 'sudo gitstack status' -TimeoutSeconds 60
        if ($gsRoot.ExitCode -ne 0) {
            $f3Ok = $false; $f3Details += "sudo gitstack status: exit $($gsRoot.ExitCode) (atteso 0): $($gsRoot.StdOut) $($gsRoot.StdErr)"
        }
        foreach ($expected in @("Versione server: sha-$Ref", "Host: $script:VmIp", 'Stato: sano')) {
            if ($gsRoot.StdOut -notmatch [regex]::Escape($expected)) {
                $f3Ok = $false; $f3Details += "nell'output di gitstack status manca '$expected'"
            }
        }
        # Senza root il config (0600) non e leggibile: exit 5.
        $gsUser = Invoke-VmSsh -Command 'gitstack status' -TimeoutSeconds 60
        if ($gsUser.ExitCode -ne 5) {
            $f3Ok = $false; $f3Details += "gitstack status senza sudo: exit $($gsUser.ExitCode) (atteso 5, config root-only)"
        }
        Add-StepResult -Name 'f3. gitstack status con sudo: exit 0, Versione server sha-<Ref>, Host, Stato sano; senza sudo: exit 5' -Ok $f3Ok -Detail ($f3Details -join '; ')

        # --- h, i. backup, restore su macchina pulita (GIT-148, DF/D) -----
        if ($SkipBackupRestore) {
            Write-Log "==> Passi h e i: saltati (-SkipBackupRestore)"
        } elseif (-not $ck) {
            Add-StepResult -Name 'h. backup e restore' -Ok $false -Detail "manca la sessione dell'admin (passo e3 fallito): impossibile creare i dati di prova"
        } else {
            Invoke-E2eBackupRestorePhase -BaseUrl $baseUrl -CaPath $caPath -AdminPassword $adminNewPassword -InstallRef $Ref
        }

        # --- j. upgrade dal commit precedente (GIT-148, DF/F) -------------
        if ($SkipUpgrade) {
            Write-Log "==> Passo j: saltato (-SkipUpgrade)"
        } else {
            $prev = $PreviousRef
            try {
                if (-not $prev) { $prev = Get-E2ePreviousCommit -Repo $GitStackRepo -Sha $Ref }
                Write-Log "Commit precedente (per l'upgrade): $prev"
                $missingPrev = @()
                foreach ($svc in @('gitstack-gateway', 'gitstack-identity', 'gitstack-core', 'gitstack-web')) {
                    if (-not (Test-GhcrImageExists -Owner $owner -Repo $svc -Tag "sha-$prev")) { $missingPrev += $svc }
                }
                if ($missingPrev.Count -gt 0) { throw "immagini mancanti su ghcr.io per sha-${prev}:$($missingPrev -join ', '). Indica un altro commit con -PreviousRef." }
            } catch {
                Add-StepResult -Name 'j. upgrade: commit precedente con le immagini pubblicate' -Ok $false -Detail $_.Exception.Message
                $prev = $null
            }
            if ($prev) { Invoke-E2eUpgradePhase -InstallRef $Ref -PreviousRef $prev }
        }

        $exitCode = 0
    } catch {
        Write-Log "ERRORE inatteso: $($_.Exception.Message)"
        Add-StepResult -Name 'esecuzione' -Ok $false -Detail $_.Exception.Message
    } finally {
        # --- g. diagnostica, SEMPRE -------------------------------------
        Write-Log "==> Passo g: raccolgo la diagnostica dalla VM (anche in caso di fallimento) ..."
        $diagLocalDir = Join-Path $OutDir 'vm-diagnostics'
        New-Item -ItemType Directory -Path $diagLocalDir -Force | Out-Null
        $diagOk = $false
        $diagDetail = ''
        if ($script:VmIp) {
            $helperCopy2 = Initialize-RemoteHelper
            if ($helperCopy2.ExitCode -eq 0) {
                $collect = Invoke-RemoteHelper -RemoteArgs @('collect-diagnostics', '/tmp/gitstack-e2e/diag') -TimeoutSeconds 120
                if ($collect.ExitCode -eq 0) {
                    $pull = Copy-FromVm -RemotePath '/tmp/gitstack-e2e/diag/*' -LocalPath $diagLocalDir -TimeoutSeconds 120
                    if ($pull.ExitCode -eq 0) {
                        $diagOk = $true
                    } else {
                        $diagDetail = "raccolta riuscita sulla VM ma scp di ritorno fallito (exit $($pull.ExitCode)): $($pull.StdErr)"
                    }
                } else {
                    $diagDetail = "remote.sh collect-diagnostics fallito (exit $($collect.ExitCode)): $($collect.StdOut) $($collect.StdErr)"
                }
            } else {
                $diagDetail = "impossibile copiare remote.sh sulla VM per la diagnostica (exit $($helperCopy2.ExitCode)): $($helperCopy2.StdErr)"
            }
        } else {
            $diagDetail = "nessun IP della VM disponibile (fallito prima del passo a): nessuna diagnostica da raccogliere sulla VM."
        }
        if (-not $diagOk) {
            Set-Content -LiteralPath (Join-Path $diagLocalDir 'DIAGNOSTICA-NON-RACCOLTA.txt') -Value $diagDetail -Encoding utf8
        }
        Write-Log $(if ($diagOk) { "Diagnostica della VM raccolta in $diagLocalDir" } else { "Diagnostica della VM NON raccolta: $diagDetail" })

        # --- riepilogo e zip ---------------------------------------------
        Write-Log ''
        Write-Log '=== Riepilogo ==='
        foreach ($s in $script:StepResults) {
            Write-Log $(if ($s.Ok) { "[PASS] $($s.Name)" } else { "[FAIL] $($s.Name): $($s.Detail)" })
        }
        $anyFail = @($script:StepResults | Where-Object { -not $_.Ok }).Count -gt 0
        if ($anyFail -or $script:StepResults.Count -eq 0) { $exitCode = 1 }

        $zipPath = "$OutDir.zip"
        try {
            if (Test-Path -LiteralPath $zipPath) { Remove-Item -LiteralPath $zipPath -Force }
            Compress-Archive -Path (Join-Path $OutDir '*') -DestinationPath $zipPath -Force
            Write-Log "Zip con log e diagnostica: $zipPath"
        } catch {
            Write-Log "ATTENZIONE: creazione dello zip fallita: $($_.Exception.Message) (i file restano in $OutDir)"
        }

        Write-Log ''
        Write-Log $(if ($exitCode -eq 0) { "RISULTATO: VERDE" } else { "RISULTATO: ROSSO" })
        Write-Log "Log completo: $script:LogFile"

        # 'exit' va chiamato qui, dentro il 'finally': i passi falliti escono
        # dal blocco 'try' con un 'return' anticipato (per non proseguire con
        # i passi successivi), e 'return' fa terminare la funzione non
        # appena 'finally' ha finito, saltando qualunque istruzione dopo il
        # blocco try/catch/finally (compreso un 'exit' messo li).
        exit $exitCode
    }
}

Main
