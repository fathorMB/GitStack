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
      f. idempotenza: una seconda esecuzione dell'installer, senza reset,
         deve uscire con successo, non reinstallare k3s e non generare una
         nuova password di Postgres.
      f2. (GIT-35) dopo la seconda esecuzione il Secret dell'admin ha stessa
         resourceVersion e stessi dati, e l'admin non e stato ricreato: la
         password iniziale non vale piu, quella cambiata in e3 si.
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
    [int]$ResetBootTimeoutSeconds = 300,
    [int]$SshTimeoutSeconds = 180,
    [int]$SshConnectTimeoutSeconds = 15,
    [int]$SshCommandTimeoutSeconds = 60,
    [int]$InstallTimeoutSeconds = 900,
    [int]$JetStreamPollAttempts = 6,
    [int]$JetStreamPollIntervalSeconds = 5
)

. (Join-Path $PSScriptRoot 'lib\common.ps1')
. (Join-Path $PSScriptRoot 'lib\http.ps1')

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
    $script:SshConnectTimeoutSeconds = $SshConnectTimeoutSeconds
    $script:SshCommandTimeoutSeconds = $SshCommandTimeoutSeconds

    if (-not $OutDir) {
        $timestamp = Get-Date -Format 'yyyyMMdd-HHmmss'
        $OutDir = Join-Path $env:LOCALAPPDATA "GitStack\e2e-runs\$timestamp"
    }
    New-Item -ItemType Directory -Path $OutDir -Force | Out-Null
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
        $installCmd = "curl -fsSL https://raw.githubusercontent.com/$GitStackRepo/$Ref/deploy/install.sh | sudo GITSTACK_REF=$Ref bash"
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

        # --- e. verifiche end-to-end -------------------------------------
        Write-Log "==> Passo e: verifiche UI e /api/healthz ..."
        $baseUrl = "http://$script:VmIp"
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
            $created = Invoke-HttpRaw -Uri "$baseUrl/api/v1/resources" -Method 'POST' -Body $createBody -Headers $ck
            if ($created.StatusCode -ne 201) {
                $e4Ok = $false; $e4Details += "create risorsa con la sessione: status $($created.StatusCode) (atteso 201), corpo '$($created.Body)' $($created.Error)"
            } else {
                $resourceId = ($created.Body | ConvertFrom-Json).id
                if (-not $resourceId) { $e4Ok = $false; $e4Details += "create risorsa: risposta senza 'id'" }
            }
        }

        if ($e4Ok -and $resourceId) {
            $read = Invoke-HttpRaw -Uri "$baseUrl/api/v1/resources/$resourceId" -Headers $ck
            if ($read.StatusCode -ne 200 -or ($read.Body | ConvertFrom-Json).id -ne $resourceId) {
                $e4Ok = $false; $e4Details += "read risorsa $resourceId: status $($read.StatusCode), corpo '$($read.Body)' $($read.Error)"
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
