# deploy/test-vm/lib/phases.ps1
#
# Fasi aggiuntive di e2e.ps1 (GIT-148, DF/G): backup, ripristino su macchina
# pulita e upgrade dal commit precedente. Dot-sourced da e2e.ps1, che
# fornisce Write-Log, Add-StepResult, Invoke-ExternalCommand, Invoke-VmSsh,
# Copy-ToVm, Copy-FromVm, Invoke-RemoteHelper, Initialize-RemoteHelper e le
# variabili $script:VmIp, $script:VmName, $script:OutDir, ... Solo ASCII:
# Windows PowerShell 5.1 legge in ANSI i .ps1 senza BOM.

# --- Piccoli aiuti ---------------------------------------------------------

# Server ACME di prova (Pebble, passo h-pub di GIT-468): il nome corto `pebble` e il Service in kube-system,
# nei SAN del suo certificato. L'email serve perche' core dia Let's Encrypt per acceso (letsEncrypt.enabled).
$script:E2eAcmeCaServer = 'https://pebble:14000/dir'
$script:E2eAcmeEmail = 'e2e@example.test'
# $true da quando il passo h-pub rilancia l'installer con Pebble: anche l'installer del restore deve
# averlo, altrimenti Traefik non ha il volume di acme.json e il backup dell'archivio ACME non si ripristina.
$script:E2eAcmeOn = $false
$script:E2ePubCtx = $null

function Get-E2eInstallCommand {
    # -AppsDomain: accende l'hosting delle app (GIT-375, --enable-apps) con quel dominio.
    # -AcmeCaServer: resolver ACME di Traefik verso quel server (GITSTACK_ACME_CA_SERVER, Pebble nelle prove).
    param([Parameter(Mandatory)][string]$InstallRef, [string]$AppsDomain, [string]$AcmeCaServer)
    $acmeEnv = ''
    if ($AcmeCaServer) { $acmeEnv = " GITSTACK_ACME_CA_SERVER=$AcmeCaServer GITSTACK_TLS_EMAIL=$script:E2eAcmeEmail" }
    $cmd = "curl -fsSL https://raw.githubusercontent.com/$script:GitStackRepo/$InstallRef/deploy/install.sh | sudo GITSTACK_REF=$InstallRef GITSTACK_ADMIN_REQUIRED=1$acmeEnv bash"
    if ($AppsDomain) { $cmd += " -s -- --enable-apps --apps-domain $AppsDomain" }
    return $cmd
}

function Write-E2eText {
    param([string]$Path, [string]$Stdout, [string]$Stderr)
    Set-Content -LiteralPath $Path -Value $Stdout -Encoding utf8
    Add-Content -LiteralPath $Path -Value $Stderr
}

# Ripristina il checkpoint 'clean' e aggiorna $script:VmIp. Ritorna $true/$false.
function Invoke-E2eVmReset {
    param([Parameter(Mandatory)][string]$Label)
    $resetLog = Join-Path $script:OutDir "reset-vm-$Label.log"
    $resetArgs = @(
        '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', (Join-Path (Split-Path -Parent $PSScriptRoot) 'reset-vm.ps1'),
        '-VmName', $script:VmName, '-VmUser', $script:VmUser, '-BootTimeoutSeconds', $script:ResetBootTimeoutSeconds, '-SshTimeoutSeconds', $script:SshTimeoutSeconds
    )
    $reset = Invoke-ExternalCommand -FilePath 'powershell.exe' -ArgumentList $resetArgs -TimeoutSeconds ($script:ResetBootTimeoutSeconds + $script:SshTimeoutSeconds + 60)
    Write-E2eText -Path $resetLog -Stdout $reset.StdOut -Stderr $reset.StdErr
    if ($reset.ExitCode -ne 0) { return "reset-vm.ps1 uscito con codice $($reset.ExitCode) (log: $resetLog)" }
    $m = [regex]::Match($reset.StdOut, 'IP:\s*(\d{1,3}(?:\.\d{1,3}){3})')
    if (-not $m.Success) { return "IP non trovato nell'output di reset-vm.ps1 (log: $resetLog)" }
    $script:VmIp = $m.Groups[1].Value
    $h = Initialize-RemoteHelper
    if ($h.ExitCode -ne 0) { return "copia di remote.sh sulla VM fallita (exit $($h.ExitCode)): $($h.StdErr)" }
    $clean = Invoke-RemoteHelper -RemoteArgs @('clean-check') -TimeoutSeconds $script:SshCommandTimeoutSeconds
    if ($clean.ExitCode -ne 0) { return "la VM dopo il reset non e pulita: $(($clean.StdOut, $clean.StdErr) -join ' ')" }
    return $null
}

# Installer di GIT-9 al commit indicato. Ritorna $null se ok, altrimenti il motivo.
function Invoke-E2eInstall {
    param([Parameter(Mandatory)][string]$InstallRef, [Parameter(Mandatory)][string]$LogName, [string]$AppsDomain, [string]$AcmeCaServer)
    $r = Invoke-VmSsh -Command (Get-E2eInstallCommand -InstallRef $InstallRef -AppsDomain $AppsDomain -AcmeCaServer $AcmeCaServer) -TimeoutSeconds $script:InstallTimeoutSeconds
    $log = Join-Path $script:OutDir $LogName
    Write-E2eText -Path $log -Stdout $r.StdOut -Stderr $r.StdErr
    if ($r.ExitCode -ne 0) { return "installer di ${InstallRef}: exit $($r.ExitCode) (log: $log)" }
    return $null
}

# Scarica ca.crt dalla VM (porta 80), lo confronta con `gitstack-tls
# fingerprint`, si fida di quella CA per il processo. Ritorna l'impronta.
function Set-E2eCaFromVm {
    param([Parameter(Mandatory)][string]$CaPath)
    $caResp = Invoke-HttpRaw -Uri "http://$script:VmIp/downloads/ca.crt"
    if ($caResp.StatusCode -ne 200 -or $caResp.Body -notmatch 'BEGIN CERTIFICATE') {
        throw "GET http://<vm>/downloads/ca.crt: status $($caResp.StatusCode) $($caResp.Error)"
    }
    Set-Content -LiteralPath $CaPath -Value $caResp.Body -Encoding ascii
    $fp = Get-CertSha256Fingerprint -Path $CaPath
    $fpVm = Invoke-VmSsh -Command 'sudo gitstack-tls fingerprint' -TimeoutSeconds $script:SshCommandTimeoutSeconds
    if ($fpVm.ExitCode -ne 0 -or $fpVm.StdOut.Trim() -ne $fp) {
        throw "l'impronta del ca.crt scaricato ($fp) non e quella della VM ('$($fpVm.StdOut.Trim())' $($fpVm.StdErr))"
    }
    Set-GitStackCaTrust -CaPath $CaPath
    return $fp
}

# Attende che /api/healthz risponda 'ok' (il gateway e fermo durante il restore).
function Wait-E2eHealthy {
    param([Parameter(Mandatory)][string]$BaseUrl, [int]$TimeoutSeconds = 300)
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $last = ''
    while ((Get-Date) -lt $deadline) {
        $h = Invoke-HttpRaw -Uri "$BaseUrl/api/healthz"
        if ($h.StatusCode -eq 200 -and $h.Body -match '"status"\s*:\s*"ok"') { return $null }
        $last = "status $($h.StatusCode) $($h.Error)"
        Start-Sleep -Seconds 5
    }
    return "/api/healthz non ok dopo ${TimeoutSeconds}s (ultimo: $last)"
}

# Dopo il restore (GIT-505): prende la CA ripristinata dalla VM, riprovando ogni $CaRetrySeconds fino a
# $CaTimeoutSeconds (subito dopo il restore /downloads/ca.crt puo dare 502, GIT-177, o avere ancora
# l'impronta vecchia), poi aspetta /api/healthz. Qualunque errore (CA o attesa) scrive la diagnosi TLS
# (log + file) e rilancia con il suo testo. Se la CA non si e potuta prendere, l'host verifica ancora
# con la CA della nuova installazione e il log lo dice. Ritorna l'impronta della CA ripristinata.
function Wait-E2eRestoredCaAndHealthy {
    param(
        [Parameter(Mandatory)][string]$BaseUrl, [Parameter(Mandatory)][string]$Ip,
        [Parameter(Mandatory)][string]$CaPath, [string]$DiagPath,
        [int]$CaTimeoutSeconds = 120, [int]$CaRetrySeconds = 5, [int]$HealthTimeoutSeconds = 300
    )
    $fp = $null
    $caTaken = $false
    try {
        $deadline = (Get-Date).AddSeconds($CaTimeoutSeconds)
        $lastErr = ''
        while ($true) {
            try { $fp = Set-E2eCaFromVm -CaPath $CaPath; $caTaken = $true; break } catch { $lastErr = $_.Exception.Message }
            if ((Get-Date) -ge $deadline) { break }
            Start-Sleep -Seconds $CaRetrySeconds
        }
        if (-not $caTaken) { throw "CA ripristinata non presa dalla VM dopo ${CaTimeoutSeconds}s (ultimo errore: $lastErr)" }
        $w = Wait-E2eHealthy -BaseUrl $BaseUrl -TimeoutSeconds $HealthTimeoutSeconds
        if ($w) { throw $w }
        return $fp
    } catch {
        $msg = $_.Exception.Message
        $diag = Get-E2eTlsDiagnosis -Ip $Ip -Path $DiagPath
        $note = if ($caTaken) { 'CA ripristinata impostata sull''host' } else { 'ATTENZIONE: la CA ripristinata non e stata presa, l''host verifica ancora con la CA della nuova installazione' }
        Write-Log "Diagnosi TLS dopo il restore ($msg; $note):`n$diag"
        $short = ($diag -replace '\s+', ' ')
        if ($short.Length -gt 1500) { $short = $short.Substring(0, 1500) + '...' }
        throw "dopo il restore: $msg (diagnosi TLS in diagnosi-tls-dopo-restore.txt: $short)"
    }
}

# Diagnosi della fiducia TLS (GIT-505): quando HTTPS dall'host non si fida, dice quale certificato
# serve davvero la 443 (issuer, subject, SAN, impronta; senza SNI come un client per IP e con SNI =
# IP della VM; l'e2e non ha un nome), quale CA ha in mano l'host per verificare, e quale certificato c'e nel Secret
# <release>-tls. Non lancia mai: ritorna il testo (anche per un errore di ssh).
function Get-E2eTlsDiagnosis {
    param([Parameter(Mandatory)][string]$Ip, [string]$Namespace = 'gitstack', [string]$SecretName = 'gitstack-tls', [string]$Path)
    $x509 = 'openssl x509 -noout -issuer -subject -ext subjectAltName -fingerprint -sha256 2>&1'
    $lines = New-Object System.Collections.Generic.List[string]
    $hostCa = 'nessuna CA impostata'
    try {
        $ca = [GitStackCaTrust]::Ca
        if ($ca) {
            $sha = [System.Security.Cryptography.SHA256]::Create()
            try { $h = $sha.ComputeHash($ca.RawData) } finally { $sha.Dispose() }
            $hostCa = "subject $($ca.Subject), impronta SHA-256 $((($h | ForEach-Object { $_.ToString('X2') }) -join ':'))"
        }
    } catch { $hostCa = "non leggibile: $($_.Exception.Message)" }
    $lines.Add("CA con cui l'host verifica: $hostCa")
    $probes = @(
        @{ Title = "certificato servito su ${Ip}:443 senza SNI (client per IP)"; Cmd = "echo | openssl s_client -connect ${Ip}:443 2>/dev/null | $x509" },
        @{ Title = "certificato servito su ${Ip}:443 con SNI $Ip"; Cmd = "echo | openssl s_client -connect ${Ip}:443 -servername $Ip 2>/dev/null | $x509" },
        @{ Title = "certificato nel Secret $Namespace/$SecretName"; Cmd = "sudo k3s kubectl -n $Namespace get secret $SecretName -o jsonpath='{.data.tls\.crt}' | base64 -d | $x509" },
        @{ Title = "certificato su disco /etc/gitstack/tls/server.crt"; Cmd = "sudo openssl x509 -in /etc/gitstack/tls/server.crt -noout -issuer -subject -ext subjectAltName -fingerprint -sha256 2>&1" },
        @{ Title = "CA su disco (gitstack-tls fingerprint)"; Cmd = 'sudo gitstack-tls fingerprint 2>&1' }
    )
    foreach ($p in $probes) {
        $lines.Add("--- $($p.Title)")
        try {
            $r = Invoke-VmSsh -Command $p.Cmd -TimeoutSeconds 60
            $out = (($r.StdOut, $r.StdErr) -join "`n").Trim()
            if ($r.ExitCode -ne 0) { $out = "exit $($r.ExitCode): $out" }
            $lines.Add($out)
        } catch {
            $lines.Add("ssh non riuscito: $($_.Exception.Message)")
        }
    }
    $text = $lines -join "`n"
    if ($Path) { try { Set-Content -LiteralPath $Path -Value $text -Encoding utf8 } catch { } }
    return $text
}

# Primo avvio dell'admin su un'installazione nuova: password dal Secret
# (mai stampata), cambio obbligatorio, poi login con la nuova. Ritorna il cookie.
function Initialize-E2eAdmin {
    param([Parameter(Mandatory)][string]$BaseUrl, [Parameter(Mandatory)][string]$NewPassword)
    $pw = Invoke-RemoteHelper -RemoteArgs @('admin-password', 'gitstack', 'default') -TimeoutSeconds 30
    if ($pw.ExitCode -ne 0 -or [string]::IsNullOrEmpty($pw.StdOut)) { throw "impossibile leggere la password iniziale dell'admin (exit $($pw.ExitCode))" }
    $initial = $pw.StdOut.TrimEnd("`r", "`n")
    $l = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/auth/login" -Method 'POST' -Body (@{ username = 'admin'; password = $initial } | ConvertTo-Json)
    if ($l.StatusCode -ne 200 -or -not $l.Cookie) { throw "login admin con la password del Secret: status $($l.StatusCode)" }
    $c = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/users/admin/password" -Method 'PUT' -Cookie $l.Cookie -Body (@{ currentPassword = $initial; newPassword = $NewPassword } | ConvertTo-Json)
    if ($c.StatusCode -ne 204) { throw "cambio password admin: status $($c.StatusCode)" }
    return (Get-E2eAdminCookie -BaseUrl $BaseUrl -Password $NewPassword)
}

function Get-E2eAdminCookie {
    param([Parameter(Mandatory)][string]$BaseUrl, [Parameter(Mandatory)][string]$Password)
    $l = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/auth/login" -Method 'POST' -Body (@{ username = 'admin'; password = $Password } | ConvertTo-Json)
    if ($l.StatusCode -ne 200 -or -not $l.Cookie -or $l.Body -notmatch '"mustChangePassword"\s*:\s*false') {
        throw "login admin con la password cambiata: status $($l.StatusCode) (atteso 200 con mustChangePassword=false)"
    }
    return $l.Cookie
}

# git con l'ambiente dell'e2e (nessun prompt, CA interna, chiave SSH del dataset).
# Ritorna l'oggetto di Invoke-ExternalCommand.
function Invoke-E2eGit {
    param(
        [Parameter(Mandatory)][string[]]$GitArgs,
        [Parameter(Mandatory)][string]$CaPath,
        [Parameter(Mandatory)][string]$SshCommand,
        [int]$TimeoutSeconds = 120
    )
    $names = 'GIT_TERMINAL_PROMPT', 'GCM_INTERACTIVE', 'GIT_SSH_COMMAND', 'GIT_AUTHOR_NAME', 'GIT_AUTHOR_EMAIL', 'GIT_COMMITTER_NAME', 'GIT_COMMITTER_EMAIL'
    $saved = @{}
    foreach ($n in $names) { $saved[$n] = [Environment]::GetEnvironmentVariable($n) }
    try {
        $env:GIT_TERMINAL_PROMPT = '0'
        $env:GCM_INTERACTIVE = 'never'
        $env:GIT_SSH_COMMAND = $SshCommand
        $env:GIT_AUTHOR_NAME = 'E2E'; $env:GIT_AUTHOR_EMAIL = 'e2e@example.com'
        $env:GIT_COMMITTER_NAME = 'E2E'; $env:GIT_COMMITTER_EMAIL = 'e2e@example.com'
        $base = @('-c', 'credential.helper=', '-c', 'core.autocrlf=false', '-c', 'http.sslBackend=openssl', '-c', "http.sslCAInfo=$($CaPath.Replace('\', '/'))")
        return Invoke-ExternalCommand -FilePath 'git' -ArgumentList ($base + $GitArgs) -TimeoutSeconds $TimeoutSeconds
    } finally {
        foreach ($n in $names) { [Environment]::SetEnvironmentVariable($n, $saved[$n]) }
    }
}

function Get-E2eSshCommand {
    <# StrictHostKeyChecking=accept-new alla creazione (registra la chiave
       host), yes in verifica: una chiave host cambiata fa fallire git. #>
    param([Parameter(Mandatory)]$Data, [switch]$Strict)
    $mode = if ($Strict) { 'yes' } else { 'accept-new' }
    return "ssh -i $($Data.SshKey -replace '\\', '/') -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=$mode -o UserKnownHostsFile=$($Data.KnownHosts -replace '\\', '/') -o LogLevel=ERROR"
}

function Get-E2eRepoUrls {
    param([Parameter(Mandatory)]$Data, [Parameter(Mandatory)][string]$Ip)
    return [pscustomobject]@{
        Https = "https://$($Data.User):$($Data.Token)@$Ip/$($Data.User)/$($Data.Repo).git"
        Ssh   = "ssh://git@${Ip}:$($script:GitSshPort)/$($Data.User)/$($Data.Repo).git"
    }
}

# --- Dati di prova ---------------------------------------------------------
# Utente con token e chiave SSH, organizzazione, repo con un commit via HTTPS
# e uno via SSH, issue con un allegato. Ritorna un oggetto con tutto quello
# che serve a verificarlo dopo (Test-E2eDataset). Lancia un'eccezione col
# passo che e fallito.

function New-E2eDataset {
    param(
        [Parameter(Mandatory)][string]$BaseUrl,
        [Parameter(Mandatory)][string]$AdminCookie,
        [Parameter(Mandatory)][string]$CaPath,
        [Parameter(Mandatory)][string]$WorkDir,
        [Parameter(Mandatory)][string]$Tag
    )
    New-Item -ItemType Directory -Path $WorkDir -Force | Out-Null
    $suffix = Get-Date -Format 'HHmmss'
    $d = [pscustomobject]@{
        User = "e2e$Tag$suffix"; Password = 'E2e-dati-password-lunga-42'; Token = $null
        Org = "org$Tag$suffix"; Repo = "dati-$suffix"; SshKey = (Join-Path $WorkDir 'id_e2e')
        KnownHosts = (Join-Path $WorkDir 'known_hosts'); HeadSha = $null; IssueNumber = 0
        AttachmentId = $null; AttachmentSha256 = $null; IssueTitle = "Issue di prova $suffix"
        Files = @{ 'https.txt' = 'da https'; 'ssh.txt' = 'da ssh' }
    }
    $stage = 'utente'
    try {
        $r = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/users" -Method 'POST' -Cookie $AdminCookie -Body (@{ username = $d.User; email = "$($d.User)@example.com"; password = $d.Password } | ConvertTo-Json)
        if ($r.StatusCode -ne 201) { throw "creazione utente: status $($r.StatusCode) $($r.Body)" }
        $l = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/auth/login" -Method 'POST' -Body (@{ username = $d.User; password = $d.Password } | ConvertTo-Json)
        if ($l.StatusCode -ne 200 -or -not $l.Cookie) { throw "login utente: status $($l.StatusCode)" }

        $stage = 'token e chiave SSH'
        $expires = (Get-Date).ToUniversalTime().AddHours(6).ToString('yyyy-MM-ddTHH:mm:ssZ')
        $t = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/user/tokens" -Method 'POST' -Cookie $l.Cookie -Body (@{ name = 'e2e'; scopes = @('read:resource', 'write:resource'); expiresAt = $expires } | ConvertTo-Json)
        if ($t.StatusCode -ne 201) { throw "creazione token: status $($t.StatusCode) $($t.Body)" }
        $d.Token = ($t.Body | ConvertFrom-Json).token
        if (-not $d.Token) { throw "creazione token: risposta senza 'token'" }
        & ssh-keygen -q -t ed25519 -N '""' -C 'e2e-dati' -f $d.SshKey | Out-Null
        if (-not (Test-Path -LiteralPath "$($d.SshKey).pub")) { throw 'ssh-keygen non ha creato la chiave' }
        $k = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/user/ssh-keys" -Method 'POST' -Cookie $l.Cookie -Body (@{ title = 'e2e'; publicKey = (Get-Content -Raw -LiteralPath "$($d.SshKey).pub").Trim() } | ConvertTo-Json)
        if ($k.StatusCode -ne 201) { throw "registrazione chiave SSH: status $($k.StatusCode) $($k.Body)" }
        $bearer = @{ Authorization = "Bearer $($d.Token)" }

        $stage = 'organizzazione e repo'
        $o = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/orgs" -Method 'POST' -Cookie $AdminCookie -Body (@{ name = $d.Org; displayName = 'Org di prova'; description = 'e2e' } | ConvertTo-Json)
        if ($o.StatusCode -ne 201) { throw "creazione organizzazione: status $($o.StatusCode) $($o.Body)" }
        $rp = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/repos" -Method 'POST' -Headers $bearer -Body (@{ owner = $d.User; name = $d.Repo; visibility = 'private' } | ConvertTo-Json)
        if ($rp.StatusCode -ne 201) { throw "POST /api/v1/repos: status $($rp.StatusCode) $($rp.Body)" }

        $ip = $script:VmIp
        $urls = Get-E2eRepoUrls -Data $d -Ip $ip
        $sshOpen = Get-E2eSshCommand -Data $d

        $stage = 'push via HTTPS'
        $w1 = Join-Path $WorkDir 'w1'
        New-Item -ItemType Directory -Path $w1 -Force | Out-Null
        foreach ($g in @(@('init', '-q', '-b', 'main', $w1), @('-C', $w1, 'remote', 'add', 'origin', $urls.Https))) {
            $x = Invoke-E2eGit -GitArgs $g -CaPath $CaPath -SshCommand $sshOpen -TimeoutSeconds 60
            if ($x.ExitCode -ne 0) { throw "git $($g[0]): exit $($x.ExitCode) $($x.StdErr)" }
        }
        Set-Content -LiteralPath (Join-Path $w1 'https.txt') -Value $d.Files['https.txt'] -Encoding ascii
        foreach ($g in @(@('-C', $w1, 'add', '-A'), @('-C', $w1, 'commit', '-q', '-m', 'dati: push https'), @('-C', $w1, 'push', '-q', '-u', 'origin', 'main'))) {
            $x = Invoke-E2eGit -GitArgs $g -CaPath $CaPath -SshCommand $sshOpen
            if ($x.ExitCode -ne 0) { throw "git $($g[2]) via HTTPS: exit $($x.ExitCode) $($x.StdOut) $($x.StdErr)" }
        }

        $stage = 'clone e push via SSH'
        $w2 = Join-Path $WorkDir 'w2'
        $x = Invoke-E2eGit -GitArgs @('clone', '-q', $urls.Ssh, $w2) -CaPath $CaPath -SshCommand $sshOpen
        if ($x.ExitCode -ne 0) { throw "git clone via SSH: exit $($x.ExitCode) $($x.StdOut) $($x.StdErr)" }
        Set-Content -LiteralPath (Join-Path $w2 'ssh.txt') -Value $d.Files['ssh.txt'] -Encoding ascii
        foreach ($g in @(@('-C', $w2, 'add', '-A'), @('-C', $w2, 'commit', '-q', '-m', 'dati: push ssh'), @('-C', $w2, 'push', '-q', 'origin', 'main'))) {
            $x = Invoke-E2eGit -GitArgs $g -CaPath $CaPath -SshCommand $sshOpen
            if ($x.ExitCode -ne 0) { throw "git $($g[2]) via SSH: exit $($x.ExitCode) $($x.StdOut) $($x.StdErr)" }
        }
        $x = Invoke-E2eGit -GitArgs @('ls-remote', $urls.Https, 'refs/heads/main') -CaPath $CaPath -SshCommand $sshOpen -TimeoutSeconds 60
        if ($x.ExitCode -ne 0 -or $x.StdOut -notmatch '^[0-9a-f]{40}') { throw "git ls-remote: exit $($x.ExitCode) $($x.StdErr)" }
        $d.HeadSha = ($x.StdOut -split '\s+')[0]

        $stage = 'allegato e issue'
        $attFile = Join-Path $WorkDir 'allegato.txt'
        Set-Content -LiteralPath $attFile -Value "allegato di prova $suffix" -Encoding ascii
        $d.AttachmentSha256 = (Get-FileHash -LiteralPath $attFile -Algorithm SHA256).Hash
        $up = Invoke-HttpUploadFile -Uri "$BaseUrl/api/v1/repos/$($d.User)/$($d.Repo)/issue-attachments" -FilePath $attFile -Headers $bearer
        if ($up.StatusCode -ne 201) { throw "upload allegato: status $($up.StatusCode), risposta '$($up.Body)' $($up.Error)" }
        $d.AttachmentId = ($up.Body | ConvertFrom-Json).id
        if (-not $d.AttachmentId) { throw "upload allegato: risposta senza 'id'" }
        $is = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/repos/$($d.User)/$($d.Repo)/issues" -Method 'POST' -Headers $bearer -Body (@{ title = $d.IssueTitle; body = 'corpo della issue di prova'; attachmentIds = @($d.AttachmentId) } | ConvertTo-Json)
        if ($is.StatusCode -ne 201) { throw "creazione issue: status $($is.StatusCode) $($is.Body)" }
        $d.IssueNumber = [int](($is.Body | ConvertFrom-Json).number)
        if ($d.IssueNumber -lt 1) { throw "creazione issue: risposta senza 'number'" }
    } catch {
        throw "[dati di prova: $stage] $($_.Exception.Message)"
    }
    return $d
}

# Verifica che il dataset sia ancora intero. Ritorna l'elenco dei problemi
# (vuoto = tutto a posto). $ChangedIp: se l'IP della VM e cambiato, il
# known_hosts si riscrive sul nuovo IP tenendo la stessa chiave host.
function Test-E2eDataset {
    param(
        [Parameter(Mandatory)]$Data,
        [Parameter(Mandatory)][string]$BaseUrl,
        [Parameter(Mandatory)][string]$CaPath,
        [Parameter(Mandatory)][string]$WorkDir,
        [string]$OldIp,
        [string]$AdminCookie
    )
    $problems = New-Object System.Collections.Generic.List[string]
    $ip = $script:VmIp
    New-Item -ItemType Directory -Path $WorkDir -Force | Out-Null
    $port = $script:GitSshPort
    if ($OldIp -and $OldIp -ne $ip -and (Test-Path -LiteralPath $Data.KnownHosts)) {
        $txt = (Get-Content -Raw -LiteralPath $Data.KnownHosts).Replace("[${OldIp}]:$port", "[${ip}]:$port")
        Set-Content -LiteralPath $Data.KnownHosts -Value $txt -Encoding ascii
    }
    $bearer = @{ Authorization = "Bearer $($Data.Token)" }
    $repoApi = "$BaseUrl/api/v1/repos/$($Data.User)/$($Data.Repo)"

    # utente e sessione: la password originale vale ancora (DB di identity)
    $l = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/auth/login" -Method 'POST' -Body (@{ username = $Data.User; password = $Data.Password } | ConvertTo-Json)
    if ($l.StatusCode -ne 200) { $problems.Add("login dell'utente $($Data.User): status $($l.StatusCode) (atteso 200)") }

    # il token emesso prima vale ancora
    $tr = Invoke-HttpRaw -Uri "$repoApi/tree" -Headers $bearer
    if ($tr.StatusCode -ne 200) {
        $problems.Add("tree con il token di prima: status $($tr.StatusCode) $($tr.Body)")
    } else {
        $names = @(($tr.Body | ConvertFrom-Json).entries | ForEach-Object { $_.name })
        foreach ($f in $Data.Files.Keys) { if ($names -notcontains $f) { $problems.Add("manca $f nella radice del repo (voci: $($names -join ', '))") } }
    }

    # organizzazione
    if ($AdminCookie) {
        $o = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/orgs/$($Data.Org)" -Cookie $AdminCookie
        if ($o.StatusCode -ne 200 -or $o.Body -notmatch [regex]::Escape($Data.Org)) { $problems.Add("GET /orgs/$($Data.Org) con la sessione dell admin: status $($o.StatusCode)") }
    }

    # issue e allegato (byte identici)
    $is = Invoke-HttpRaw -Uri "$repoApi/issues/$($Data.IssueNumber)" -Headers $bearer
    if ($is.StatusCode -ne 200 -or $is.Body -notmatch [regex]::Escape($Data.IssueTitle)) {
        $problems.Add("issue #$($Data.IssueNumber): status $($is.StatusCode), titolo atteso '$($Data.IssueTitle)'")
    }
    $dl = Join-Path $WorkDir 'allegato-scaricato.txt'
    $att = Invoke-HttpDownloadFile -Uri "$repoApi/issue-attachments/$($Data.AttachmentId)" -OutFile $dl -Headers $bearer
    if ($att.StatusCode -ne 200) {
        $problems.Add("download allegato: status $($att.StatusCode) $($att.Error)")
    } elseif ((Get-FileHash -LiteralPath $dl -Algorithm SHA256).Hash -ne $Data.AttachmentSha256) {
        $problems.Add('l''allegato scaricato ha un SHA-256 diverso da quello caricato')
    }

    # clone via HTTPS e SSH: stesso HEAD, stessi file, host key invariata (Strict)
    $urls = Get-E2eRepoUrls -Data $Data -Ip $ip
    $sshStrict = Get-E2eSshCommand -Data $Data -Strict
    foreach ($proto in 'Https', 'Ssh') {
        $dir = Join-Path $WorkDir "clone-$proto"
        if (Test-Path -LiteralPath $dir) { Remove-Item -LiteralPath $dir -Recurse -Force -ErrorAction SilentlyContinue }
        $x = Invoke-E2eGit -GitArgs @('clone', '-q', $($urls.$proto), $dir) -CaPath $CaPath -SshCommand $sshStrict
        if ($x.ExitCode -ne 0) { $problems.Add("git clone via ${proto}: exit $($x.ExitCode) $($x.StdErr)"); continue }
        $head = Invoke-E2eGit -GitArgs @('-C', $dir, 'rev-parse', 'HEAD') -CaPath $CaPath -SshCommand $sshStrict -TimeoutSeconds 30
        if ($head.StdOut.Trim() -ne $Data.HeadSha) { $problems.Add("clone via ${proto}: HEAD '$($head.StdOut.Trim())', atteso $($Data.HeadSha)") }
        foreach ($f in $Data.Files.Keys) {
            $p = Join-Path $dir $f
            if (-not (Test-Path -LiteralPath $p)) { $problems.Add("clone via ${proto}: manca $f"); continue }
            if ((Get-Content -Raw -LiteralPath $p).Trim() -ne $Data.Files[$f]) { $problems.Add("clone via ${proto}: contenuto di $f diverso") }
        }
        $fsck = Invoke-E2eGit -GitArgs @('-C', $dir, 'fsck', '--no-progress') -CaPath $CaPath -SshCommand $sshStrict -TimeoutSeconds 60
        if ($fsck.ExitCode -ne 0) { $problems.Add("git fsck del clone via ${proto}: $($fsck.StdErr)") }
    }
    return , $problems.ToArray()
}

# --- App nel ciclo backup -> restore (GIT-388, criterio 2 di GIT-351, H4) --
# Prima del backup: immagine di prova con tre tag nel registry, app da API,
# variabile e segreto, v1 poi v2 (v2 attiva, v1 precedente). Dopo il restore:
# sette controlli, una riga ok/ko ciascuno (Test-E2eAppAfterRestore).

$script:E2eAppName = 'e2eapp'
$script:E2eAppImageName = 'e2eimg'
$script:E2eAppVarName = 'E2E_VAR'
$script:E2eAppSecretName = 'E2E_SECRET'

function Get-E2eBearer {
    param([Parameter(Mandatory)]$Data)
    return @{ Authorization = "Bearer $($Data.Token)" }
}

# Attende che la versione piu' recente dell'app sia `serving`. Ritorna il suo numero.
function Wait-E2eAppServing {
    param([Parameter(Mandatory)][string]$AppsApi, [Parameter(Mandatory)][hashtable]$Headers, [int]$TimeoutSeconds = 300, [string]$AppName = $script:E2eAppName)
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $last = ''
    while ((Get-Date) -lt $deadline) {
        $r = Invoke-HttpRaw -Uri "$AppsApi/$AppName/versions?per_page=1" -Headers $Headers
        if ($r.StatusCode -eq 200) {
            $items = @(($r.Body | ConvertFrom-Json).items)
            if ($items.Count -gt 0) {
                $v = $items[0]
                if ($v.state -eq 'serving') { return [int]$v.number }
                if ($v.state -eq 'failed' -or $v.state -eq 'rolled_back') { throw "versione $($v.number) dell'app: stato $($v.state) ($($v.stateReason))" }
                $last = "versione $($v.number) stato $($v.state)"
            }
        } else {
            $last = "status $($r.StatusCode) $($r.Error)"
        }
        Start-Sleep -Seconds 5
    }
    throw "la versione piu' recente dell'app non e' serving dopo ${TimeoutSeconds}s (ultimo: $last)"
}

# Il token del dataset ha solo read/write:resource: il registry vuole read:registry e
# write:registry (services/git/internal/registry, pkg/scopes: *:resource non li implica).
# Token dedicato, con il login dell'utente del dataset; sta nel DB e torna col restore.
function New-E2eRegistryToken {
    param([Parameter(Mandatory)][string]$BaseUrl, [Parameter(Mandatory)]$Data)
    $l = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/auth/login" -Method 'POST' -Body (@{ username = $Data.User; password = $Data.Password } | ConvertTo-Json)
    if ($l.StatusCode -ne 200 -or -not $l.Cookie) { throw "login dell'utente $($Data.User) per il token del registry: status $($l.StatusCode)" }
    $expires = (Get-Date).ToUniversalTime().AddHours(6).ToString('yyyy-MM-ddTHH:mm:ssZ')
    $t = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/user/tokens" -Method 'POST' -Cookie $l.Cookie -Body (@{ name = 'e2e-registry'; scopes = @('read:registry', 'write:registry'); expiresAt = $expires } | ConvertTo-Json)
    if ($t.StatusCode -ne 201) { throw "creazione del token del registry: status $($t.StatusCode) $($t.Body)" }
    $tok = ($t.Body | ConvertFrom-Json).token
    if (-not $tok) { throw "creazione del token del registry: risposta senza 'token'" }
    return [string]$tok
}

# Prepara l'app prima del backup. Ritorna il contesto per i controlli dopo il restore.
function New-E2eAppFixture {
    param(
        [Parameter(Mandatory)][string]$BaseUrl,
        [Parameter(Mandatory)][string]$CaPath,
        [Parameter(Mandatory)]$Data,
        [Parameter(Mandatory)][string]$WorkDir
    )
    New-Item -ItemType Directory -Path $WorkDir -Force | Out-Null
    $stage = 'copia dello script sulla VM'
    try {
        $h = Get-E2eBearer -Data $Data
        $appsApi = "$BaseUrl/api/v1/repos/$($Data.User)/$($Data.Repo)/apps"
        $imagePath = "$($Data.User)/$($Data.Repo)/$script:E2eAppImageName"
        $mk = Invoke-VmSsh -Command 'mkdir -p /tmp/gitstack-e2e' -TimeoutSeconds $script:SshCommandTimeoutSeconds
        if ($mk.ExitCode -ne 0) { throw "mkdir sulla VM: exit $($mk.ExitCode) $($mk.StdErr)" }
        $cp = Copy-ToVm -LocalPath (Join-Path (Split-Path -Parent $PSScriptRoot) 'e2e\gs-app-restore.sh') -RemotePath '/tmp/gitstack-e2e/gs-app-restore.sh' -TimeoutSeconds $script:SshCommandTimeoutSeconds
        if ($cp.ExitCode -ne 0) { throw "scp di gs-app-restore.sh: exit $($cp.ExitCode) $($cp.StdErr)" }
        $cc = Copy-ToVm -LocalPath $CaPath -RemotePath '/tmp/gitstack-e2e/app-ca.crt' -TimeoutSeconds $script:SshCommandTimeoutSeconds
        if ($cc.ExitCode -ne 0) { throw "scp della CA: exit $($cc.ExitCode) $($cc.StdErr)" }

        $stage = 'token del registry'
        $regToken = New-E2eRegistryToken -BaseUrl $BaseUrl -Data $Data

        $stage = 'immagine di prova con tre tag nel registry'
        $push = Invoke-VmSsh -Command "GS_VM_IP='$script:VmIp' GS_CA=/tmp/gitstack-e2e/app-ca.crt GS_USER_TOKEN='$regToken' GS_IMAGE='$imagePath' bash /tmp/gitstack-e2e/gs-app-restore.sh push" -TimeoutSeconds 600
        Write-E2eText -Path (Join-Path $WorkDir 'push.log') -Stdout $push.StdOut -Stderr $push.StdErr
        if ($push.ExitCode -ne 0 -or $push.StdOut -notmatch 'STEP ok push') { throw "push dell'immagine: exit $($push.ExitCode) $($push.StdOut) $($push.StdErr)" }

        $stage = 'creazione dell''app con v1'
        $c = Invoke-HttpRaw -Uri $appsApi -Method 'POST' -Headers $h -Body (@{ name = $script:E2eAppName; image = "$($script:E2eAppImageName):v1" } | ConvertTo-Json) -TimeoutSec 60
        if ($c.StatusCode -ne 202) { throw "POST apps: status $($c.StatusCode) $($c.Body) $($c.Error)" }
        $appId = ($c.Body | ConvertFrom-Json).app.id
        if (-not $appId) { throw 'POST apps: risposta senza app.id' }
        [void](Wait-E2eAppServing -AppsApi $appsApi -Headers $h)

        $stage = 'volume data'
        $vol = Invoke-HttpRaw -Uri "$appsApi/$script:E2eAppName/volumes" -Method 'POST' -Headers $h -Body (@{ name = 'data'; mountPath = '/data'; sizeBytes = 1048576 } | ConvertTo-Json)
        if ($vol.StatusCode -ne 201 -and $vol.StatusCode -ne 202) { throw "POST volume: status $($vol.StatusCode) $($vol.Body)" }
        [void](Wait-E2eAppServing -AppsApi $appsApi -Headers $h)
        $write = Invoke-VmSsh -Command "GS_APP_NS='gitstack-app-$appId' bash /tmp/gitstack-e2e/gs-app-restore.sh write-volume" -TimeoutSeconds 300
        if ($write.ExitCode -ne 0 -or $write.StdOut -notmatch 'STEP ok volume') { throw "scrittura volume: $($write.StdErr)" }

        # secondo volume, escluso dal backup (GIT-360): con un file dentro, dopo il restore deve tornare vuoto
        $stage = 'volume scratch escluso dal backup'
        $vol2 = Invoke-HttpRaw -Uri "$appsApi/$script:E2eAppName/volumes" -Method 'POST' -Headers $h -Body (@{ name = 'scratch'; mountPath = '/scratch'; sizeBytes = 1048576 } | ConvertTo-Json)
        if ($vol2.StatusCode -ne 201 -and $vol2.StatusCode -ne 202) { throw "POST volume scratch: status $($vol2.StatusCode) $($vol2.Body)" }
        [void](Wait-E2eAppServing -AppsApi $appsApi -Headers $h)
        $ex = Invoke-HttpRaw -Uri "$appsApi/$script:E2eAppName/volumes/scratch" -Method 'PATCH' -Headers $h -Body (@{ includeInBackup = $false } | ConvertTo-Json)
        if ($ex.StatusCode -ne 200) { throw "PATCH volume scratch: status $($ex.StatusCode) $($ex.Body)" }
        $write2 = Invoke-VmSsh -Command "GS_APP_NS='gitstack-app-$appId' bash /tmp/gitstack-e2e/gs-app-restore.sh write-scratch" -TimeoutSeconds 300
        if ($write2.ExitCode -ne 0 -or $write2.StdOut -notmatch 'STEP ok scratch') { throw "scrittura volume scratch: $($write2.StdErr)" }

        $stage = 'variabile e segreto'
        $varValue = "valore-var-$([guid]::NewGuid().ToString('N').Substring(0, 8))"
        $secValue = "segreto-$([guid]::NewGuid().ToString('N'))"
        $pv = Invoke-HttpRaw -Uri "$appsApi/$script:E2eAppName/variables/$script:E2eAppVarName" -Method 'PUT' -Headers $h -Body (@{ value = $varValue } | ConvertTo-Json)
        if ($pv.StatusCode -ne 200) { throw "PUT variabile: status $($pv.StatusCode) $($pv.Body)" }
        [void](Wait-E2eAppServing -AppsApi $appsApi -Headers $h)
        $ps = Invoke-HttpRaw -Uri "$appsApi/$script:E2eAppName/secrets/$script:E2eAppSecretName" -Method 'PUT' -Headers $h -Body (@{ value = $secValue } | ConvertTo-Json)
        if ($ps.StatusCode -ne 200) { throw "PUT segreto: status $($ps.StatusCode) (corpo non stampato)" }
        [void](Wait-E2eAppServing -AppsApi $appsApi -Headers $h)

        $stage = 'deploy di v2'
        $d = Invoke-HttpRaw -Uri "$appsApi/$script:E2eAppName/deploys" -Method 'POST' -Headers $h -Body (@{ image = "$($script:E2eAppImageName):v2" } | ConvertTo-Json) -TimeoutSec 60
        if ($d.StatusCode -ne 202) { throw "POST deploys: status $($d.StatusCode) $($d.Body)" }
        $activeNumber = Wait-E2eAppServing -AppsApi $appsApi -Headers $h

        $stage = 'lettura dello stato prima del backup'
        $g = Invoke-HttpRaw -Uri "$appsApi/$script:E2eAppName" -Headers $h
        if ($g.StatusCode -ne 200) { throw "GET app: status $($g.StatusCode)" }
        $app = $g.Body | ConvertFrom-Json
        if ($app.image.reference -notmatch ':v2(@sha256:[0-9a-f]{64})?$') { throw "l'immagine attiva non e v2: $($app.image.reference)" }
        $vl = Invoke-HttpRaw -Uri "$appsApi/$script:E2eAppName/versions" -Headers $h
        $prev = @(($vl.Body | ConvertFrom-Json).items | Where-Object { $_.image.reference -match ':v1(@sha256:[0-9a-f]{64})?$' -and $_.inBackup })
        if ($prev.Count -lt 1) { throw 'nessuna versione precedente con v1 segnata inBackup' }
        Write-Log "App $script:E2eAppName ($appId): versione attiva $($app.activeVersion) ($($app.image.reference)), precedente $($prev[0].number) ($($prev[0].image.reference)), stato $($app.status)"
        return [pscustomobject]@{
            AppId = [string]$appId; Namespace = "gitstack-app-$appId"; ActiveVersion = [int]$app.activeVersion
            ActiveRef = [string]$app.image.reference; VarValue = $varValue; SecretValue = $secValue
            ImagePath = $imagePath; Number = $activeNumber; RegistryToken = $regToken
        }
    } catch {
        throw "[app di prova: $stage] $($_.Exception.Message)"
    }
}

# --- Ciclo dell'app (GIT-355, H1/H6/H7/H12) --------------------------------
# App web di prova (immagine e2eweb: busybox httpd che serve "e2e-web-<tag>"):
# deploy di v1, login GitStack davanti all'app (forward-auth del gateway: senza
# credenziale 401, con il token l'app risponde), deploy di v2, rollback a v1,
# eliminazione. Le righe sono STEP ok/ko; al primo errore i pod del namespace
# dell'app finiscono in diag.log. L'app di prova si elimina solo a passo riuscito.
# Ritorna sei oggetti {Name, Ok, Detail}, sempre.
function Test-E2eAppLifecycle {
    param(
        [Parameter(Mandatory)][string]$BaseUrl,
        [Parameter(Mandatory)][string]$CaPath,
        [Parameter(Mandatory)]$Data,
        [Parameter(Mandatory)]$Ctx,
        [Parameter(Mandatory)][string]$WorkDir
    )
    New-Item -ItemType Directory -Path $WorkDir -Force | Out-Null
    $appName = 'e2elife'
    $webImage = 'e2eweb'
    $names = @(
        'h-app ciclo 1/6 deploy di v1: la versione e serving e l''app ha un indirizzo',
        'h-app ciclo 2/6 login GitStack davanti all''app: senza credenziale 401, il corpo dell''app non arriva',
        'h-app ciclo 3/6 con il token dell''utente (read sul repo) l''app risponde e serve v1',
        'h-app ciclo 4/6 deploy di v2: la versione e serving e l''app serve v2',
        'h-app ciclo 5/6 rollback alla versione di v1: nuova versione serving e l''app serve v1',
        'h-app ciclo 6/6 l''app di prova e eliminata (204)'
    )
    $rows = @()
    foreach ($n in $names) { $rows += [pscustomobject]@{ Name = $n; Ok = $false; Detail = 'non verificato' } }
    $ns = ''
    $i = 0
    try {
        $h = Get-E2eBearer -Data $Data
        $appsApi = "$BaseUrl/api/v1/repos/$($Data.User)/$($Data.Repo)/apps"
        $appUrl = "$appsApi/$appName"
        $sshEnv = "GS_VM_IP='$script:VmIp' GS_CA=/tmp/gitstack-e2e/app-ca.crt"
        $remoteSh = '/tmp/gitstack-e2e/gs-app-restore.sh'

        # immagine web con due tag, nello stesso registry del repo di prova
        $push = Invoke-VmSsh -Command "$sshEnv GS_USER_TOKEN='$($Ctx.RegistryToken)' GS_IMAGE='$($Data.User)/$($Data.Repo)/$webImage' bash $remoteSh push-web" -TimeoutSeconds 600
        Write-E2eText -Path (Join-Path $WorkDir 'push-web.log') -Stdout $push.StdOut -Stderr $push.StdErr
        if ($push.ExitCode -ne 0 -or $push.StdOut -notmatch 'STEP ok push-web') { throw "push dell'immagine web: exit $($push.ExitCode) $($push.StdOut) $($push.StdErr)" }

        # 1. deploy di v1
        $i = 0
        $c = Invoke-HttpRaw -Uri $appsApi -Method 'POST' -Headers $h -Body (@{ name = $appName; image = "${webImage}:v1"; port = 8080 } | ConvertTo-Json) -TimeoutSec 60
        if ($c.StatusCode -ne 202) { throw "POST apps: status $($c.StatusCode) $($c.Body) $($c.Error)" }
        $created = $c.Body | ConvertFrom-Json
        $ns = "gitstack-app-$($created.app.id)"
        $v1 = [int]$created.version.number
        [void](Wait-E2eAppServing -AppsApi $appsApi -Headers $h -AppName $appName)
        $g = Invoke-HttpRaw -Uri $appUrl -Headers $h
        if ($g.StatusCode -ne 200) { throw "GET app: status $($g.StatusCode)" }
        $addr = [string](($g.Body | ConvertFrom-Json).address)
        if (-not $addr) { throw 'l''app non ha un indirizzo (address assente)' }
        $appHost = ([Uri]$addr).Host
        $rows[0].Ok = $true; $rows[0].Detail = "versione $v1, indirizzo $appHost"

        $probeEnv = "$sshEnv GS_APP_HOST='$appHost' GS_APP_NS='$ns'"
        # 2. senza credenziale: 401
        $i = 1
        $r = Invoke-VmSsh -Command "$probeEnv bash $remoteSh probe-anon" -TimeoutSeconds 120
        Write-E2eText -Path (Join-Path $WorkDir 'probe-anon.log') -Stdout $r.StdOut -Stderr $r.StdErr
        if ($r.ExitCode -ne 0 -or $r.StdOut -notmatch 'STEP ok login-anon') { throw "login-anon: $($r.StdOut) $($r.StdErr)" }
        $rows[1].Ok = $true; $rows[1].Detail = ''
        # 3. con il token: v1
        $i = 2
        $r = Invoke-VmSsh -Command "$probeEnv GS_PROBE_TOKEN='$($Data.Token)' GS_EXPECT=e2e-web-v1 bash $remoteSh probe-auth" -TimeoutSeconds 300
        Write-E2eText -Path (Join-Path $WorkDir 'probe-v1.log') -Stdout $r.StdOut -Stderr $r.StdErr
        if ($r.ExitCode -ne 0 -or $r.StdOut -notmatch 'STEP ok serve-e2e-web-v1') { throw "serve v1: $($r.StdOut) $($r.StdErr)" }
        $rows[2].Ok = $true; $rows[2].Detail = ''

        # 4. deploy di v2
        $i = 3
        $d = Invoke-HttpRaw -Uri "$appUrl/deploys" -Method 'POST' -Headers $h -Body (@{ image = "${webImage}:v2" } | ConvertTo-Json) -TimeoutSec 60
        if ($d.StatusCode -ne 202) { throw "POST deploys v2: status $($d.StatusCode) $($d.Body) $($d.Error)" }
        $v2 = Wait-E2eAppServing -AppsApi $appsApi -Headers $h -AppName $appName
        $r = Invoke-VmSsh -Command "$probeEnv GS_PROBE_TOKEN='$($Data.Token)' GS_EXPECT=e2e-web-v2 bash $remoteSh probe-auth" -TimeoutSeconds 300
        Write-E2eText -Path (Join-Path $WorkDir 'probe-v2.log') -Stdout $r.StdOut -Stderr $r.StdErr
        if ($r.ExitCode -ne 0 -or $r.StdOut -notmatch 'STEP ok serve-e2e-web-v2') { throw "serve v2: $($r.StdOut) $($r.StdErr)" }
        $rows[3].Ok = $true; $rows[3].Detail = "versione $v2"

        # 5. rollback alla versione di v1 (crea una versione nuova con l'immagine di v1)
        $i = 4
        $rb = Invoke-HttpRaw -Uri "$appUrl/rollback" -Method 'POST' -Headers $h -Body (@{ version = $v1 } | ConvertTo-Json) -TimeoutSec 60
        if ($rb.StatusCode -ne 202) { throw "POST rollback alla versione ${v1}: status $($rb.StatusCode) $($rb.Body) $($rb.Error)" }
        $v3 = Wait-E2eAppServing -AppsApi $appsApi -Headers $h -AppName $appName
        if ($v3 -le $v2) { throw "dopo il rollback la versione serving e $v3, attesa una piu recente di $v2" }
        $r = Invoke-VmSsh -Command "$probeEnv GS_PROBE_TOKEN='$($Data.Token)' GS_EXPECT=e2e-web-v1 bash $remoteSh probe-auth" -TimeoutSeconds 300
        Write-E2eText -Path (Join-Path $WorkDir 'probe-rollback.log') -Stdout $r.StdOut -Stderr $r.StdErr
        if ($r.ExitCode -ne 0 -or $r.StdOut -notmatch 'STEP ok serve-e2e-web-v1') { throw "serve v1 dopo il rollback: $($r.StdOut) $($r.StdErr)" }
        $rows[4].Ok = $true; $rows[4].Detail = "versione $v3 con l'immagine di v1"

        # 6. pulizia: l'app senza volumi si elimina senza conferma
        $i = 5
        $del = Invoke-HttpRaw -Uri $appUrl -Method 'DELETE' -Headers $h -TimeoutSec 60
        if ($del.StatusCode -ne 204) { throw "DELETE app: status $($del.StatusCode) $($del.Body) $($del.Error)" }
        $rows[5].Ok = $true; $rows[5].Detail = ''
    } catch {
        $msg = $_.Exception.Message
        $rows[$i].Detail = $msg
        foreach ($r2 in $rows) { if (-not $r2.Ok -and $r2.Detail -eq 'non verificato') { $r2.Detail = "non verificato: un passo precedente e fallito ($msg)" } }
        if ($ns) {
            try {
                $dg = Invoke-VmSsh -Command "GS_APP_NS='$ns' bash /tmp/gitstack-e2e/gs-app-restore.sh diag" -TimeoutSeconds 120
                Write-E2eText -Path (Join-Path $WorkDir 'diag.log') -Stdout $dg.StdOut -Stderr $dg.StdErr
            } catch {
                Write-Log "ATTENZIONE: diagnostica dei pod dell'app di prova non raccolta: $($_.Exception.Message)"
            }
        }
    }
    return , $rows
}

# --- App pubbliche e domini propri con Pebble (GIT-468, H1/H2) -------------
# Pebble come server ACME e un resolver DNS di prova (zona nel CoreDNS del cluster) sulla VM, script in
# e2e/gs-app-public.sh. Sequenza: interruttore delle app pubbliche acceso, un'app resa pubblica, un
# dominio aggiunto e verificato, certificato da Pebble, app raggiunta senza login; interruttore spento e
# app non piu esposta senza login (resta raggiungibile col login, ADR 0002); riacceso. Dopo il restore
# (Test-E2eAppPublicAfterRestore) dominio e certificato sono ancora li.

# Esegue un comando di gs-app-public.sh sulla VM; lancia se manca il marcatore "STEP ok ...".
function Invoke-E2ePubVm {
    param(
        [string]$VmEnv = '',
        [Parameter(Mandatory)][string]$Sub,
        [Parameter(Mandatory)][string]$Marker,
        [Parameter(Mandatory)][string]$LogPath,
        [int]$TimeoutSeconds = 300
    )
    $r = Invoke-VmSsh -Command "$VmEnv bash /tmp/gitstack-e2e/gs-app-public.sh $Sub" -TimeoutSeconds $TimeoutSeconds
    Write-E2eText -Path $LogPath -Stdout $r.StdOut -Stderr $r.StdErr
    if ($r.ExitCode -ne 0 -or $r.StdOut -notmatch $Marker) { throw "${Sub}: exit $($r.ExitCode) $($r.StdOut) $($r.StdErr)" }
    return $r
}

# Interruttore d'istanza delle app pubbliche (PUT /admin/apps/settings, solo admin): rilegge le impostazioni e
# le rimanda con il solo publicAppsEnabled cambiato. Ritorna la risposta di Invoke-HttpRaw.
function Set-E2ePublicAppsSwitch {
    param([Parameter(Mandatory)][string]$BaseUrl, [Parameter(Mandatory)][string]$AdminCookie, [Parameter(Mandatory)][bool]$On)
    $uri = "$BaseUrl/api/v1/admin/apps/settings"
    $g = Invoke-HttpRaw -Uri $uri -Cookie $AdminCookie
    if ($g.StatusCode -ne 200) { throw "GET admin/apps/settings: status $($g.StatusCode) $($g.Body) $($g.Error)" }
    $s = $g.Body | ConvertFrom-Json
    $body = @{
        publicAppsEnabled = $On
        signedImagesOnly  = [bool]$s.signedImagesOnly
        restricted        = [bool]$s.restricted
        maxPerApp         = @{ cpuMillis = [long]$s.maxPerApp.cpuMillis; memoryBytes = [long]$s.maxPerApp.memoryBytes }
        logRetention      = @{ days = [int]$s.logRetention.days; maxBytes = [long]$s.logRetention.maxBytes }
    }
    return (Invoke-HttpRaw -Uri $uri -Method 'PUT' -Cookie $AdminCookie -Body ($body | ConvertTo-Json -Depth 5) -TimeoutSec 60)
}

# Aspetta che il dominio abbia lo stato (e, se indicato, il certificato) voluto. Ritorna l'oggetto del dominio.
# Lancia se il dominio va in error o se il tempo scade.
function Wait-E2eAppDomain {
    param(
        [Parameter(Mandatory)][string]$DomainUrl,
        [Parameter(Mandatory)][hashtable]$Headers,
        [string]$Status = 'verified',
        [string]$CertState = '',
        [int]$TimeoutSeconds = 120,
        [string]$Recheck = ''
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $last = ''
    while ((Get-Date) -lt $deadline) {
        if ($Recheck) { [void](Invoke-HttpRaw -Uri $Recheck -Method 'POST' -Headers $Headers -TimeoutSec 60) }
        $r = Invoke-HttpRaw -Uri $DomainUrl -Headers $Headers
        if ($r.StatusCode -eq 200) {
            $d = $r.Body | ConvertFrom-Json
            if ($d.status -eq 'error') { throw "dominio in error: $($d.statusReason)" }
            if ($CertState -and $d.certificate.state -eq 'error') { throw "certificato in error: $($d.certificate.error)" }
            $last = "stato $($d.status), certificato $($d.certificate.state) ($($d.certificate.issuer))"
            if ($d.status -eq $Status -and (-not $CertState -or $d.certificate.state -eq $CertState)) { return $d }
        } else {
            $last = "status $($r.StatusCode) $($r.Error)"
        }
        Start-Sleep -Seconds 10
    }
    throw "dopo ${TimeoutSeconds}s il dominio non e arrivato a $Status$(if ($CertState) { "/$CertState" }) (ultimo: $last)"
}

# Tredici controlli prima del backup. Ritorna tredici oggetti {Name, Ok, Detail}, sempre. Al primo errore il
# passo si ferma e i log di Pebble, Traefik e CoreDNS finiscono in diag.log. Lascia l'app, il dominio e il
# certificato dov'e' (devono entrare nel backup) e imposta $script:E2ePubCtx per i controlli dopo il restore.
function Test-E2eAppPublic {
    param(
        [Parameter(Mandatory)][string]$BaseUrl,
        [Parameter(Mandatory)]$Data,
        [Parameter(Mandatory)]$Ctx,
        [Parameter(Mandatory)][string]$AdminPassword,
        [Parameter(Mandatory)][string]$InstallRef,
        [Parameter(Mandatory)][string]$WorkDir
    )
    New-Item -ItemType Directory -Path $WorkDir -Force | Out-Null
    $appName = 'e2epub'
    $webImage = 'e2eweb'
    $names = @(
        'h-pub 1/13 Pebble (ACME di prova) pronto e Traefik col resolver su Pebble: installer rilanciato con GITSTACK_ACME_CA_SERVER, CA di Pebble data a Traefik',
        'h-pub 2/13 app e2epub creata; con l''interruttore spento la scelta public e rifiutata (422 public_apps_disabled)',
        'h-pub 3/13 interruttore delle app pubbliche acceso dall''amministratore e Let''s Encrypt acceso nelle impostazioni',
        'h-pub 4/13 chi ha admin sull''app sceglie public (doppio consenso)',
        'h-pub 5/13 app pubblica: l''indirizzo automatico risponde senza login',
        'h-pub 6/13 resolver DNS di prova: il dominio proprio risolve dal cluster verso Traefik (CNAME sull''indirizzo automatico)',
        'h-pub 7/13 dominio proprio aggiunto: 201 con il record CNAME atteso',
        'h-pub 8/13 dominio verificato (DNS verified)',
        'h-pub 9/13 certificato ottenuto da Pebble (valid, emittente Pebble)',
        'h-pub 10/13 app raggiunta senza login sul dominio, catena fino alla radice di Pebble',
        'h-pub 11/13 interruttore spento: il dominio non espone piu l''app',
        'h-pub 12/13 interruttore spento: l''indirizzo automatico chiede il login (401) e col token l''app risponde',
        'h-pub 13/13 interruttore riacceso: dominio e certificato conservati, app di nuovo senza login sul dominio'
    )
    $rows = @()
    foreach ($n in $names) { $rows += [pscustomobject]@{ Name = $n; Ok = $false; Detail = 'non verificato' } }
    $script:E2ePubCtx = $null
    $i = 0
    try {
        $apps = [string]$script:AppsDomain
        if (-not $apps -or $apps.IndexOf('.') -lt 1) { throw "dominio delle app '$apps' non utilizzabile per la zona di prova" }
        $apex = $apps.Substring($apps.IndexOf('.') + 1)
        $pubDomain = "shop-pub.$apex"
        $h = Get-E2eBearer -Data $Data
        $appsApi = "$BaseUrl/api/v1/repos/$($Data.User)/$($Data.Repo)/apps"
        $appUrl = "$appsApi/$appName"
        $domainUrl = "$appUrl/domains/$pubDomain"
        $rootRemote = '/tmp/gitstack-e2e/pebble-root.pem'
        $caRemote = '/tmp/gitstack-e2e/app-ca.crt'
        $expectBody = 'e2e-web-v1'

        # 1. Pebble, installer con il resolver su Pebble, Traefik si fida della CA di prova
        $i = 0
        $cp = Copy-ToVm -LocalPath (Join-Path (Split-Path -Parent $PSScriptRoot) 'e2e\gs-app-public.sh') -RemotePath '/tmp/gitstack-e2e/gs-app-public.sh' -TimeoutSeconds $script:SshCommandTimeoutSeconds
        if ($cp.ExitCode -ne 0) { throw "scp di gs-app-public.sh: exit $($cp.ExitCode) $($cp.StdErr)" }
        [void](Invoke-E2ePubVm -Sub 'pebble-up' -Marker 'STEP ok pebble-up' -LogPath (Join-Path $WorkDir 'pebble-up.log') -TimeoutSeconds 600)
        $script:E2eAcmeOn = $true
        $why = Invoke-E2eInstall -InstallRef $InstallRef -LogName 'installer-acme.log' -AppsDomain $apps -AcmeCaServer $script:E2eAcmeCaServer
        if ($why) { throw $why }
        $w = Wait-E2eHealthy -BaseUrl $BaseUrl -TimeoutSeconds 300
        if ($w) { throw "dopo il rilancio dell'installer: $w" }
        [void](Invoke-E2ePubVm -Sub 'traefik-acme' -Marker 'STEP ok traefik-acme' -LogPath (Join-Path $WorkDir 'traefik-acme.log') -TimeoutSeconds 900)
        $adminCk = Get-E2eAdminCookie -BaseUrl $BaseUrl -Password $AdminPassword
        $rows[0].Ok = $true; $rows[0].Detail = ''

        # 2. l'app, e con l'interruttore spento public e rifiutato
        $i = 1
        $off = Set-E2ePublicAppsSwitch -BaseUrl $BaseUrl -AdminCookie $adminCk -On $false
        if ($off.StatusCode -ne 200) { throw "PUT admin/apps/settings (spento): status $($off.StatusCode) $($off.Body)" }
        $push = Invoke-VmSsh -Command "GS_VM_IP='$script:VmIp' GS_CA=$caRemote GS_USER_TOKEN='$($Ctx.RegistryToken)' GS_IMAGE='$($Data.User)/$($Data.Repo)/$webImage' bash /tmp/gitstack-e2e/gs-app-restore.sh push-web" -TimeoutSeconds 600
        Write-E2eText -Path (Join-Path $WorkDir 'push-web.log') -Stdout $push.StdOut -Stderr $push.StdErr
        if ($push.ExitCode -ne 0 -or $push.StdOut -notmatch 'STEP ok push-web') { throw "push dell'immagine web: exit $($push.ExitCode) $($push.StdOut) $($push.StdErr)" }
        $c = Invoke-HttpRaw -Uri $appsApi -Method 'POST' -Headers $h -Body (@{ name = $appName; image = "${webImage}:v1"; port = 8080 } | ConvertTo-Json) -TimeoutSec 60
        if ($c.StatusCode -ne 202) { throw "POST apps: status $($c.StatusCode) $($c.Body) $($c.Error)" }
        [void](Wait-E2eAppServing -AppsApi $appsApi -Headers $h -AppName $appName)
        $g = Invoke-HttpRaw -Uri $appUrl -Headers $h
        if ($g.StatusCode -ne 200) { throw "GET app: status $($g.StatusCode)" }
        $addr = [string](($g.Body | ConvertFrom-Json).address)
        if (-not $addr) { throw 'l''app non ha un indirizzo (address assente)' }
        $appHost = ([Uri]$addr).Host
        if (-not $appHost.EndsWith(".$apps")) { throw "l'indirizzo $appHost non sta sotto il dominio delle app $apps" }
        $label = $appHost.Substring(0, $appHost.Length - $apps.Length - 1)
        $deny = Invoke-HttpRaw -Uri "$appUrl/access" -Method 'PUT' -Headers $h -Body (@{ mode = 'public' } | ConvertTo-Json) -TimeoutSec 60
        if ($deny.StatusCode -ne 422 -or $deny.Body -notmatch 'public_apps_disabled') { throw "PUT access public a interruttore spento: status $($deny.StatusCode) $($deny.Body) (atteso 422 public_apps_disabled)" }
        $rows[1].Ok = $true; $rows[1].Detail = "indirizzo $appHost"

        # 3. interruttore acceso
        $i = 2
        $on = Set-E2ePublicAppsSwitch -BaseUrl $BaseUrl -AdminCookie $adminCk -On $true
        if ($on.StatusCode -ne 200) { throw "PUT admin/apps/settings (acceso): status $($on.StatusCode) $($on.Body)" }
        $set = $on.Body | ConvertFrom-Json
        if (-not $set.publicAppsEnabled) { throw 'la risposta non ha publicAppsEnabled' }
        if (-not $set.letsEncrypt -or -not $set.letsEncrypt.enabled) { throw "Let's Encrypt non acceso nelle impostazioni (letsEncrypt: $($set.letsEncrypt | ConvertTo-Json -Compress)): core non ha il resolver o l'email ACME" }
        $rows[2].Ok = $true; $rows[2].Detail = "email ACME $($set.letsEncrypt.acmeEmail)"

        # 4. public
        $i = 3
        $pub = Invoke-HttpRaw -Uri "$appUrl/access" -Method 'PUT' -Headers $h -Body (@{ mode = 'public' } | ConvertTo-Json) -TimeoutSec 60
        if ($pub.StatusCode -ne 200) { throw "PUT access public: status $($pub.StatusCode) $($pub.Body)" }
        $acc = $pub.Body | ConvertFrom-Json
        if ($acc.mode -ne 'public' -or -not $acc.publicAppsEnabled) { throw "accesso dopo la scelta: mode '$($acc.mode)', publicAppsEnabled '$($acc.publicAppsEnabled)'" }
        $rows[3].Ok = $true; $rows[3].Detail = ''

        # 5. indirizzo automatico senza login
        $i = 4
        [void](Invoke-E2ePubVm -VmEnv "GS_APP_HOST='$appHost' GS_CA=$caRemote GS_EXPECT=$expectBody" -Sub 'address-open' -Marker 'STEP ok address-open' -LogPath (Join-Path $WorkDir 'address-open.log') -TimeoutSeconds 300)
        $rows[4].Ok = $true; $rows[4].Detail = ''

        # 6. il resolver DNS di prova
        $i = 5
        [void](Invoke-E2ePubVm -VmEnv "GS_APPS_DOMAIN='$apps' GS_APP_LABEL='$label' GS_PUB_DOMAIN='$pubDomain'" -Sub 'dns-set' -Marker 'STEP ok dns-set' -LogPath (Join-Path $WorkDir 'dns-set.log') -TimeoutSeconds 600)
        $rows[5].Ok = $true; $rows[5].Detail = "$pubDomain -> $label.$apps"

        # 7. il dominio proprio
        $i = 6
        $ad = Invoke-HttpRaw -Uri "$appUrl/domains" -Method 'POST' -Headers $h -Body (@{ domain = $pubDomain } | ConvertTo-Json) -TimeoutSec 60
        if ($ad.StatusCode -ne 201) { throw "POST domains: status $($ad.StatusCode) $($ad.Body) $($ad.Error)" }
        $dom = $ad.Body | ConvertFrom-Json
        if ($dom.domain -ne $pubDomain -or $dom.dns.type -ne 'CNAME' -or $dom.dns.value -ne $appHost) {
            throw "dominio aggiunto: dominio '$($dom.domain)', record atteso $($dom.dns.type) '$($dom.dns.value)' (atteso CNAME verso $appHost)"
        }
        $rows[6].Ok = $true; $rows[6].Detail = "stato $($dom.status)"

        # 8. DNS verified
        $i = 7
        $dom = Wait-E2eAppDomain -DomainUrl $domainUrl -Headers $h -Status 'verified' -TimeoutSeconds 180 -Recheck "$domainUrl/recheck"
        $rows[7].Ok = $true; $rows[7].Detail = ''

        # 9. certificato da Pebble
        $i = 8
        $dom = Wait-E2eAppDomain -DomainUrl $domainUrl -Headers $h -Status 'verified' -CertState 'valid' -TimeoutSeconds 540
        if ([string]$dom.certificate.issuer -notmatch 'Pebble') { throw "emittente '$($dom.certificate.issuer)': non e Pebble" }
        if ($dom.certificate.mode -ne 'letsencrypt') { throw "modalita del certificato '$($dom.certificate.mode)' (attesa letsencrypt)" }
        $rows[8].Ok = $true; $rows[8].Detail = "emittente $($dom.certificate.issuer), scade $($dom.certificate.validUntil)"

        # 10. app sul dominio senza login, catena fino alla radice di Pebble
        $i = 9
        [void](Invoke-E2ePubVm -VmEnv "GS_ROOT_OUT=$rootRemote" -Sub 'pebble-root' -Marker 'STEP ok pebble-root' -LogPath (Join-Path $WorkDir 'pebble-root.log'))
        $pull = Copy-FromVm -RemotePath $rootRemote -LocalPath $WorkDir -TimeoutSeconds $script:SshCommandTimeoutSeconds
        if ($pull.ExitCode -ne 0) { throw "scp della radice di Pebble: exit $($pull.ExitCode) $($pull.StdErr)" }
        $rootLocal = Join-Path $WorkDir 'pebble-root.pem'
        if (-not (Test-Path -LiteralPath $rootLocal)) { throw 'la radice di Pebble non e arrivata sull''host' }
        [void](Invoke-E2ePubVm -VmEnv "GS_DOMAIN='$pubDomain' GS_ROOT=$rootRemote GS_EXPECT=$expectBody" -Sub 'domain-open' -Marker 'STEP ok domain-open' -LogPath (Join-Path $WorkDir 'domain-open.log') -TimeoutSeconds 300)
        $ci = Invoke-E2ePubVm -VmEnv "GS_DOMAIN='$pubDomain'" -Sub 'cert-info' -Marker 'fp=' -LogPath (Join-Path $WorkDir 'cert-info.log')
        $fpm = [regex]::Match($ci.StdOut, '(?m)^fp=(\S+)')
        if (-not $fpm.Success) { throw 'cert-info senza impronta' }
        if ($ci.StdOut -notmatch 'Pebble') { throw "il certificato servito non e di Pebble: $($ci.StdOut)" }
        if ($ci.StdOut -notmatch [regex]::Escape($pubDomain)) { throw "il certificato servito non ha $pubDomain nei SAN: $($ci.StdOut)" }
        $script:E2ePubCtx = [pscustomobject]@{ AppName = $appName; Domain = $pubDomain; Label = $label; Fingerprint = $fpm.Groups[1].Value; RootLocal = $rootLocal; Expect = $expectBody }
        $rows[9].Ok = $true; $rows[9].Detail = "impronta $($fpm.Groups[1].Value)"

        # 11. interruttore spento: dominio non esposto
        $i = 10
        $offAgain = Set-E2ePublicAppsSwitch -BaseUrl $BaseUrl -AdminCookie $adminCk -On $false
        if ($offAgain.StatusCode -ne 200 -or ($offAgain.Body | ConvertFrom-Json).publicAppsEnabled) { throw "PUT admin/apps/settings (spento): status $($offAgain.StatusCode) $($offAgain.Body)" }
        [void](Invoke-E2ePubVm -VmEnv "GS_DOMAIN='$pubDomain' GS_EXPECT=$expectBody" -Sub 'domain-closed' -Marker 'STEP ok domain-closed' -LogPath (Join-Path $WorkDir 'domain-closed.log') -TimeoutSeconds 300)
        $rows[10].Ok = $true; $rows[10].Detail = ''

        # 12. spento: l'indirizzo automatico resta raggiungibile col login (ADR 0002)
        $i = 11
        $probeEnv = "GS_VM_IP='$script:VmIp' GS_CA=$caRemote GS_APP_HOST='$appHost'"
        $anon = $null
        for ($try = 0; $try -lt 6; $try++) {
            $anon = Invoke-VmSsh -Command "$probeEnv bash /tmp/gitstack-e2e/gs-app-restore.sh probe-anon" -TimeoutSeconds 120
            if ($anon.ExitCode -eq 0 -and $anon.StdOut -match 'STEP ok login-anon') { break }
            Start-Sleep -Seconds 10
        }
        Write-E2eText -Path (Join-Path $WorkDir 'probe-anon-spento.log') -Stdout $anon.StdOut -Stderr $anon.StdErr
        if ($anon.ExitCode -ne 0 -or $anon.StdOut -notmatch 'STEP ok login-anon') { throw "senza credenziale, a interruttore spento: $($anon.StdOut) $($anon.StdErr)" }
        $auth = Invoke-VmSsh -Command "$probeEnv GS_PROBE_TOKEN='$($Data.Token)' GS_EXPECT=$expectBody bash /tmp/gitstack-e2e/gs-app-restore.sh probe-auth" -TimeoutSeconds 300
        Write-E2eText -Path (Join-Path $WorkDir 'probe-auth-spento.log') -Stdout $auth.StdOut -Stderr $auth.StdErr
        if ($auth.ExitCode -ne 0 -or $auth.StdOut -notmatch "STEP ok serve-$expectBody") { throw "col token, a interruttore spento: $($auth.StdOut) $($auth.StdErr)" }
        $rows[11].Ok = $true; $rows[11].Detail = ''

        # 13. riacceso: tutto conservato
        $i = 12
        $reOn = Set-E2ePublicAppsSwitch -BaseUrl $BaseUrl -AdminCookie $adminCk -On $true
        if ($reOn.StatusCode -ne 200 -or -not ($reOn.Body | ConvertFrom-Json).publicAppsEnabled) { throw "PUT admin/apps/settings (riacceso): status $($reOn.StatusCode) $($reOn.Body)" }
        $dom = Wait-E2eAppDomain -DomainUrl $domainUrl -Headers $h -Status 'verified' -CertState 'valid' -TimeoutSeconds 180
        [void](Invoke-E2ePubVm -VmEnv "GS_DOMAIN='$pubDomain' GS_ROOT=$rootRemote GS_EXPECT=$expectBody" -Sub 'domain-open' -Marker 'STEP ok domain-open' -LogPath (Join-Path $WorkDir 'domain-open-riacceso.log') -TimeoutSeconds 300)
        $ci2 = Invoke-E2ePubVm -VmEnv "GS_DOMAIN='$pubDomain'" -Sub 'cert-info' -Marker 'fp=' -LogPath (Join-Path $WorkDir 'cert-info-riacceso.log')
        if ($ci2.StdOut -notmatch [regex]::Escape($script:E2ePubCtx.Fingerprint)) { throw "il certificato servito e cambiato dopo lo spegnimento: $($ci2.StdOut)" }
        $rows[12].Ok = $true; $rows[12].Detail = ''
    } catch {
        $msg = $_.Exception.Message
        $rows[$i].Detail = $msg
        foreach ($r2 in $rows) { if (-not $r2.Ok -and $r2.Detail -eq 'non verificato') { $r2.Detail = "non verificato: un passo precedente e fallito ($msg)" } }
        try {
            $dg = Invoke-VmSsh -Command 'bash /tmp/gitstack-e2e/gs-app-public.sh diag' -TimeoutSeconds 120
            Write-E2eText -Path (Join-Path $WorkDir 'diag.log') -Stdout $dg.StdOut -Stderr $dg.StdErr
        } catch {
            Write-Log "ATTENZIONE: diagnostica di Pebble/Traefik/CoreDNS non raccolta: $($_.Exception.Message)"
        }
    }
    return , $rows
}

# Quattro controlli dopo il restore: dominio e certificato ci sono ancora e l'app risponde sul dominio.
# Ritorna quattro oggetti {Name, Ok, Detail}, sempre. Pebble non c'e piu (la VM e stata ripristinata): il
# certificato e quello di prima perche' l'archivio ACME di Traefik e tornato col backup.
function Test-E2eAppPublicAfterRestore {
    param(
        [Parameter(Mandatory)][string]$BaseUrl,
        [Parameter(Mandatory)]$Data,
        [Parameter(Mandatory)]$Ctx,
        [Parameter(Mandatory)][string]$WorkDir
    )
    New-Item -ItemType Directory -Path $WorkDir -Force | Out-Null
    $names = @(
        'i-pub 1/4 dopo il restore il dominio proprio e ancora dell''app e2epub (modo public)',
        'i-pub 2/4 il certificato del dominio e ancora valid (emittente Pebble)',
        'i-pub 3/4 Traefik serve lo stesso certificato di prima (impronta SHA-256 uguale: archivio ACME ripristinato)',
        'i-pub 4/4 l''app risponde senza login sul dominio, catena fino alla radice di Pebble di prima'
    )
    $rows = @()
    foreach ($n in $names) { $rows += [pscustomobject]@{ Name = $n; Ok = $false; Detail = 'non verificato' } }
    $i = 0
    try {
        $h = Get-E2eBearer -Data $Data
        $appUrl = "$BaseUrl/api/v1/repos/$($Data.User)/$($Data.Repo)/apps/$($Ctx.AppName)"
        $domainUrl = "$appUrl/domains/$($Ctx.Domain)"
        $rootRemote = '/tmp/gitstack-e2e/pebble-root.pem'

        # 1. dominio e modo
        $i = 0
        $acc = Invoke-HttpRaw -Uri "$appUrl/access" -Headers $h
        if ($acc.StatusCode -ne 200) { throw "GET access: status $($acc.StatusCode) $($acc.Body) $($acc.Error)" }
        $access = $acc.Body | ConvertFrom-Json
        if ($access.mode -ne 'public' -or -not $access.publicAppsEnabled) { throw "accesso dopo il restore: mode '$($access.mode)', publicAppsEnabled '$($access.publicAppsEnabled)'" }
        $list = Invoke-HttpRaw -Uri "$appUrl/domains" -Headers $h
        if ($list.StatusCode -ne 200) { throw "GET domains: status $($list.StatusCode) $($list.Body)" }
        $found = @(($list.Body | ConvertFrom-Json).items | Where-Object { $_.domain -eq $Ctx.Domain })
        if ($found.Count -ne 1) { throw "il dominio $($Ctx.Domain) non e nell'elenco dopo il restore" }
        $rows[0].Ok = $true; $rows[0].Detail = "stato $($found[0].status)"

        # 2. certificato valid: core lo rilegge da Traefik (SNI), puo servire un minuto
        $i = 1
        $d = Wait-E2eAppDomain -DomainUrl $domainUrl -Headers $h -Status $found[0].status -CertState 'valid' -TimeoutSeconds 420
        if ([string]$d.certificate.issuer -notmatch 'Pebble') { throw "emittente '$($d.certificate.issuer)': non e Pebble" }
        $rows[1].Ok = $true; $rows[1].Detail = "scade $($d.certificate.validUntil)"

        # 3. stesso certificato
        $i = 2
        $cp = Copy-ToVm -LocalPath (Join-Path (Split-Path -Parent $PSScriptRoot) 'e2e\gs-app-public.sh') -RemotePath '/tmp/gitstack-e2e/gs-app-public.sh' -TimeoutSeconds $script:SshCommandTimeoutSeconds
        if ($cp.ExitCode -ne 0) { throw "scp di gs-app-public.sh sulla VM nuova: exit $($cp.ExitCode) $($cp.StdErr)" }
        $ci = Invoke-E2ePubVm -VmEnv "GS_DOMAIN='$($Ctx.Domain)'" -Sub 'cert-info' -Marker 'fp=' -LogPath (Join-Path $WorkDir 'cert-info.log')
        if ($ci.StdOut -notmatch [regex]::Escape($Ctx.Fingerprint)) { throw "impronta del certificato servito diversa da quella di prima ($($Ctx.Fingerprint)): $($ci.StdOut)" }
        $rows[2].Ok = $true; $rows[2].Detail = $Ctx.Fingerprint

        # 4. l'app sul dominio (le app ripartono dopo il restore: si aspetta running)
        $i = 3
        $status = ''
        for ($attempt = 0; $attempt -lt 42; $attempt++) {
            $a = Invoke-HttpRaw -Uri $appUrl -Headers $h
            if ($a.StatusCode -eq 200) {
                $status = [string](($a.Body | ConvertFrom-Json).status)
                if ($status -eq 'running') { break }
            }
            Start-Sleep -Seconds 10
        }
        if ($status -ne 'running') { throw "l'app e2epub non e running dopo il restore (stato '$status')" }
        $cr = Copy-ToVm -LocalPath $Ctx.RootLocal -RemotePath $rootRemote -TimeoutSeconds $script:SshCommandTimeoutSeconds
        if ($cr.ExitCode -ne 0) { throw "scp della radice di Pebble sulla VM nuova: exit $($cr.ExitCode) $($cr.StdErr)" }
        [void](Invoke-E2ePubVm -VmEnv "GS_DOMAIN='$($Ctx.Domain)' GS_ROOT=$rootRemote GS_EXPECT=$($Ctx.Expect)" -Sub 'domain-open' -Marker 'STEP ok domain-open' -LogPath (Join-Path $WorkDir 'domain-open.log') -TimeoutSeconds 300)
        $rows[3].Ok = $true; $rows[3].Detail = ''
    } catch {
        $msg = $_.Exception.Message
        $rows[$i].Detail = $msg
        foreach ($r2 in $rows) { if (-not $r2.Ok -and $r2.Detail -eq 'non verificato') { $r2.Detail = "non verificato: un passo precedente e fallito ($msg)" } }
        try {
            $dg = Invoke-VmSsh -Command 'bash /tmp/gitstack-e2e/gs-app-public.sh diag' -TimeoutSeconds 120
            Write-E2eText -Path (Join-Path $WorkDir 'diag.log') -Stdout $dg.StdOut -Stderr $dg.StdErr
        } catch {
            Write-Log "ATTENZIONE: diagnostica dopo il restore non raccolta: $($_.Exception.Message)"
        }
    }
    return , $rows
}

# Dieci controlli dopo il restore (sette dell'app, tre sui volumi). Ritorna dieci oggetti {Name, Ok, Detail}, sempre.
function Test-E2eAppAfterRestore {
    param(
        [Parameter(Mandatory)][string]$BaseUrl,
        [Parameter(Mandatory)]$Data,
        [Parameter(Mandatory)]$Ctx,
        [Parameter(Mandatory)][string]$WorkDir,
        [string]$RestoreLog = ''
    )
    New-Item -ItemType Directory -Path $WorkDir -Force | Out-Null
    $names = @(
        "i-app 1/7 il Deployment 'app' esiste nel namespace dell'app",
        'i-app 2/7 la sua immagine e il riferimento della versione attiva (v2)',
        'i-app 3/7 lo stato dell''app via API e running',
        'i-app 4/7 la variabile e nell''env del container',
        'i-app 5/7 il segreto si decifra (valore uguale a quello impostato)',
        'i-app 6/7 i tag v1 e v2 si scaricano dal registry ripristinato (manifest 200)',
        'i-app 7/7 il terzo tag non c''e (RG2: il resto del registry resta fuori)',
        'i-app volume: log del restore dei volumi',
        'i-app volume: file data presente con contenuto invariato',
        'i-app volume escluso: scratch torna vuoto, il file scritto prima del backup non c''e (controllo negativo)'
    )
    $rows = @()
    foreach ($n in $names) { $rows += [pscustomobject]@{ Name = $n; Ok = $false; Detail = 'non verificato' } }
    $expectedVolume = "ripristino del volume data dell'app $($Data.User)/$($Data.Repo)/$script:E2eAppName"
    $rows[7].Ok = $RestoreLog.Contains('ripristino dei volumi delle app (1)') -and $RestoreLog.Contains($expectedVolume)
    $rows[7].Detail = if ($rows[7].Ok) { '' } else { 'mancano le righe del restore del volume nel log' }
    try {
        $h = Get-E2eBearer -Data $Data
        $appUrl = "$BaseUrl/api/v1/repos/$($Data.User)/$($Data.Repo)/apps/$script:E2eAppName"

        # le app ripartono dopo il restore: si attende running (check 3), senza fermarsi se non arriva
        $status = ''; $http = 0
        for ($attempt = 0; $attempt -lt 42; $attempt++) {
            $a = Invoke-HttpRaw -Uri $appUrl -Headers $h
            $http = $a.StatusCode
            if ($a.StatusCode -eq 200) {
                $status = [string](($a.Body | ConvertFrom-Json).status)
                if ($status -eq 'running') { break }
            }
            Start-Sleep -Seconds 10
        }
        $rows[2].Ok = ($status -eq 'running')
        $rows[2].Detail = if ($status -eq 'running') { '' } else { "status HTTP $http, stato '$status' (atteso running entro 420s)" }

        # Deployment, immagine, variabile e segreto: dentro la VM
        $cp = Copy-ToVm -LocalPath (Join-Path (Split-Path -Parent $PSScriptRoot) 'e2e\gs-app-restore.sh') -RemotePath '/tmp/gitstack-e2e/gs-app-restore.sh' -TimeoutSeconds $script:SshCommandTimeoutSeconds
        if ($cp.ExitCode -ne 0) { throw "scp di gs-app-restore.sh sulla VM nuova: exit $($cp.ExitCode) $($cp.StdErr)" }
        $env1 = "GS_APP_NS='$($Ctx.Namespace)' GS_VAR_NAME='$script:E2eAppVarName' GS_VAR_VALUE='$($Ctx.VarValue)' GS_SECRET_NAME='$script:E2eAppSecretName' GS_SECRET_VALUE='$($Ctx.SecretValue)'"
        $ins = Invoke-VmSsh -Command "$env1 bash /tmp/gitstack-e2e/gs-app-restore.sh inspect" -TimeoutSeconds 300
        Write-E2eText -Path (Join-Path $WorkDir 'inspect.log') -Stdout $ins.StdOut -Stderr $ins.StdErr
        $kv = @{}
        foreach ($line in ($ins.StdOut -split "`r?`n")) {
            $i = $line.IndexOf('=')
            if ($i -gt 0) { $kv[$line.Substring(0, $i)] = $line.Substring($i + 1) }
        }
        if ($ins.ExitCode -ne 0 -or -not $kv.ContainsKey('deploy')) {
            $why = "inspect: exit $($ins.ExitCode) $($ins.StdErr)"
            foreach ($i in 0, 1, 3, 4) { $rows[$i].Detail = $why }
        } else {
            $rows[0].Ok = ($kv['deploy'] -eq 'yes')
            $rows[0].Detail = if ($rows[0].Ok) { '' } else { "nessun Deployment 'app' in $($Ctx.Namespace)" }
            # l'host del registry puo' cambiare con l'IP della VM: si confronta dal primo '/'
            $expPath = $Ctx.ActiveRef -replace '^[^/]+/', ''
            $gotPath = $kv['image'] -replace '^[^/]+/', ''
            $rows[1].Ok = ($kv['deploy'] -eq 'yes' -and $gotPath -eq $expPath)
            $rows[1].Detail = if ($rows[1].Ok) { '' } else { "immagine '$($kv['image'])', attesa '$($Ctx.ActiveRef)' (versione attiva $($Ctx.ActiveVersion))" }
            $rows[3].Ok = ($kv['var'] -eq 'match')
            $rows[3].Detail = if ($rows[3].Ok) { '' } else { "$script:E2eAppVarName nel container: $($kv['var'])" }
            $rows[4].Ok = ($kv['secret'] -eq 'match')
            $rows[4].Detail = if ($rows[4].Ok) { '' } else { "$script:E2eAppSecretName decifrato: $($kv['secret'])" }
            $rows[8].Ok = ($kv['volume'] -eq 'match')
            $rows[8].Detail = if ($rows[8].Ok) { '' } else { "file nel volume: $($kv['volume'])" }
            $rows[9].Ok = ($kv['scratch'] -eq 'empty')
            $rows[9].Detail = if ($rows[9].Ok) { '' } else { "volume scratch dopo il restore: '$($kv['scratch'])' (atteso empty: montato e senza il file)" }
        }

        # registry ripristinato: v1 e v2 si scaricano, v3 no
        $basic = [Convert]::ToBase64String([Text.Encoding]::ASCII.GetBytes("token:$($Ctx.RegistryToken)"))
        $rh = @{ Authorization = "Basic $basic"; Accept = 'application/vnd.oci.image.manifest.v1+json' }
        $st = @{}
        foreach ($t in 'v1', 'v2', 'v3') {
            $m = Invoke-HttpRaw -Uri "$BaseUrl/v2/$($Ctx.ImagePath)/manifests/$t" -Headers $rh
            $st[$t] = $m.StatusCode
        }
        $rows[5].Ok = ($st['v1'] -eq 200 -and $st['v2'] -eq 200)
        $rows[5].Detail = if ($rows[5].Ok) { '' } else { "manifest v1: $($st['v1']), v2: $($st['v2']) (attesi 200)$(if ($st.Values -contains 401 -or $st.Values -contains 403) { ': permessi, il token del registry non e accettato' })" }
        # solo 404 vale come «tag assente»: 401/403 sono un problema di permessi, non un esito di RG2
        $rows[6].Ok = ($st['v3'] -eq 404)
        $rows[6].Detail = if ($rows[6].Ok) { '' } elseif ($st['v3'] -eq 401 -or $st['v3'] -eq 403) { "manifest v3: status $($st['v3']), permessi: il token del registry non e accettato, l'assenza del tag non e verificata" } elseif ($st['v3'] -eq 200) { 'manifest v3: status 200, il tag non usato da nessuna app e presente (atteso 404, RG2)' } else { "manifest v3: status $($st['v3']) (atteso 404)" }
    } catch {
        $msg = $_.Exception.Message
        foreach ($r in $rows) { if (-not $r.Ok -and $r.Detail -eq 'non verificato') { $r.Detail = "non verificato: $msg" } }
    }
    if (@($rows | Where-Object { -not $_.Ok }).Count -gt 0) {
        try {
            $api = Invoke-HttpRaw -Uri $appUrl -Headers $h
            Write-E2eText -Path (Join-Path $WorkDir 'app-api.log') -Stdout "HTTP $($api.StatusCode)`n$($api.Body)" -Stderr ''
        } catch { Set-Content -LiteralPath (Join-Path $WorkDir 'app-api.log') -Value $_.Exception.Message -Encoding utf8 }
        try {
            $diag = Invoke-VmSsh -Command "GS_APP_NS='$($Ctx.Namespace)' bash /tmp/gitstack-e2e/gs-app-restore.sh diagnose /tmp/gitstack-e2e/app-diagnostics" -TimeoutSeconds 120
            $pull = Copy-FromVm -RemotePath '/tmp/gitstack-e2e/app-diagnostics/*' -LocalPath $WorkDir -TimeoutSeconds 120
            if ($diag.ExitCode -ne 0 -or $pull.ExitCode -ne 0) { throw "diagnose exit $($diag.ExitCode); scp exit $($pull.ExitCode): $($diag.StdErr) $($pull.StdErr)" }
        } catch { Set-Content -LiteralPath (Join-Path $WorkDir 'diagnostica-errore.txt') -Value $_.Exception.Message -Encoding utf8 }
    }
    return , $rows
}

# --- Fase h/i: backup, reset, reinstallazione, restore (DF/D) -------------

function Invoke-E2eBackupRestorePhase {
    param(
        [Parameter(Mandatory)][string]$BaseUrl,
        [Parameter(Mandatory)][string]$CaPath,
        [Parameter(Mandatory)][string]$AdminPassword,
        [Parameter(Mandatory)][string]$InstallRef
    )
    $phaseDir = Join-Path $script:OutDir 'backup-restore'
    New-Item -ItemType Directory -Path $phaseDir -Force | Out-Null
    $script:BackupArchiveLocal = $null
    $script:BackupKeyLocal = $null
    $script:RestoredBaseUrl = $null

    # --- h. dati di prova e backup (sullo stato dell'installazione appena provata) ---
    Write-Log "==> Passo h: dati di prova e gitstack backup cifrato ..."
    $hOk = $true; $hDetails = @()
    $data = $null; $fpBefore = $null; $oldIp = $script:VmIp; $appCtx = $null
    try {
        $fpBefore = Get-CertSha256Fingerprint -Path $CaPath
        $adminCk = Get-E2eAdminCookie -BaseUrl $BaseUrl -Password $AdminPassword
        $data = New-E2eDataset -BaseUrl $BaseUrl -AdminCookie $adminCk -CaPath $CaPath -WorkDir (Join-Path $phaseDir 'dati') -Tag 'b'
        Write-Log "Dati di prova: utente $($data.User), org $($data.Org), repo $($data.Repo) (HEAD $($data.HeadSha)), issue #$($data.IssueNumber) con allegato"

        # app di prova (GIT-388): immagine con tre tag, app con v1 poi v2, variabile e segreto
        if ($script:AppsDomain) {
            try {
                $appCtx = New-E2eAppFixture -BaseUrl $BaseUrl -CaPath $CaPath -Data $data -WorkDir (Join-Path $phaseDir 'app')
            } catch {
                $hOk = $false; $hDetails += "app di prova: $($_.Exception.Message)"
            }
            # ciclo dell'app (GIT-355): deploy v1, login davanti all'app, deploy v2, rollback a v1
            if ($appCtx) {
                $lifeRows = Test-E2eAppLifecycle -BaseUrl $BaseUrl -CaPath $CaPath -Data $data -Ctx $appCtx -WorkDir (Join-Path $phaseDir 'app-ciclo')
                foreach ($lr in $lifeRows) { Add-StepResult -Name $lr.Name -Ok $lr.Ok -Detail $lr.Detail }
                $lifeOk = @($lifeRows | Where-Object { $_.Ok }).Count
                Add-StepResult -Name "h-app ciclo. esito complessivo: $lifeOk/$(@($lifeRows).Count) controlli ok" -Ok ($lifeOk -eq @($lifeRows).Count) -Detail "$(@($lifeRows).Count - $lifeOk) controlli ko (vedi le righe h-app ciclo sopra; pod in app-ciclo/diag.log)"
            } else {
                Add-StepResult -Name 'h-app ciclo. esito complessivo' -Ok $false -Detail 'l''app di prova non e stata preparata: ciclo non eseguito'
            }

            # app pubblica e dominio proprio con Pebble (GIT-468): prima del backup, cosi' entrano nell'archivio
            if ($appCtx) {
                $pubRows = Test-E2eAppPublic -BaseUrl $BaseUrl -Data $data -Ctx $appCtx -AdminPassword $AdminPassword -InstallRef $InstallRef -WorkDir (Join-Path $phaseDir 'app-pubblica')
                foreach ($pr in $pubRows) { Add-StepResult -Name $pr.Name -Ok $pr.Ok -Detail $pr.Detail }
                $pubOk = @($pubRows | Where-Object { $_.Ok }).Count
                Add-StepResult -Name "h-pub. esito complessivo: $pubOk/$(@($pubRows).Count) controlli ok" -Ok ($pubOk -eq @($pubRows).Count) -Detail "$(@($pubRows).Count - $pubOk) controlli ko (vedi le righe h-pub sopra; log di Pebble, Traefik e CoreDNS in app-pubblica\diag.log)"
            } else {
                Add-StepResult -Name 'h-pub. esito complessivo' -Ok $false -Detail 'l''app di prova non e stata preparata: passo non eseguito'
            }
        }

        $gen = Invoke-VmSsh -Command 'umask 077; mkdir -p /tmp/gitstack-e2e/backup && openssl rand -base64 32 > /tmp/gitstack-e2e/backup/chiave' -TimeoutSeconds $script:SshCommandTimeoutSeconds
        if ($gen.ExitCode -ne 0) { throw "generazione della chiave di backup sulla VM: exit $($gen.ExitCode) $($gen.StdErr)" }
        $bk = Invoke-VmSsh -Command 'sudo gitstack backup --key-file /tmp/gitstack-e2e/backup/chiave' -TimeoutSeconds 900
        $bkLog = Join-Path $phaseDir 'backup.log'
        Write-E2eText -Path $bkLog -Stdout $bk.StdOut -Stderr $bk.StdErr
        if ($bk.ExitCode -ne 0) { throw "gitstack backup: exit $($bk.ExitCode) (log: $bkLog): $($bk.StdErr)" }
        $win = @(($bk.StdOut + "`n" + $bk.StdErr) -split "`r?`n" | Where-Object { $_ -match 'Finestra di sola lettura' })
        Write-Log "Finestra di sola lettura (da riportare nel README di admin): $(if ($win) { $win -join ' | ' } else { 'riga non trovata nell''output' })"

        # il backup ha copiato il volume data e non lo scratch escluso (GIT-360)
        if ($appCtx) {
            $bkText = "$($bk.StdOut)`n$($bk.StdErr)"
            $volCopy = "copia del volume data dell'app $($data.User)/$($data.Repo)/$script:E2eAppName"
            $volOk = $bkText.Contains($volCopy) -and -not $bkText.Contains('copia del volume scratch')
            Add-StepResult -Name 'h-app volume: il backup copia il volume data e non lo scratch escluso' -Ok $volOk -Detail $(if ($volOk) { '' } else { "nel log del backup manca '$volCopy' oppure compare lo scratch (log: $bkLog)" })
        }

        # archivio cifrato 0600, cartella 0700, .sha256 accanto
        $ls = Invoke-VmSsh -Command "sudo find /var/backups/gitstack -maxdepth 1 -printf '%m %u %p\n'"-TimeoutSeconds $script:SshCommandTimeoutSeconds
        Write-E2eText -Path (Join-Path $phaseDir 'backup-ls.txt') -Stdout $ls.StdOut -Stderr $ls.StdErr
        $lines = @($ls.StdOut -split "`r?`n" | Where-Object { $_ })
        $dirLine = $lines | Where-Object { $_ -match ' /var/backups/gitstack$' } | Select-Object -First 1
        if (-not $dirLine -or $dirLine -notmatch '^700 root ') { $hOk = $false; $hDetails += "cartella dei backup: '$dirLine' (atteso 700 root)" }
        $enc = @($lines | Where-Object { $_ -match '\.tar\.gz\.enc$' })
        if ($enc.Count -ne 1) {
            throw "attesi esattamente un archivio .tar.gz.enc in /var/backups/gitstack, trovati $($enc.Count): $($lines -join ' ; ')"
        }
        if ($enc[0] -notmatch '^600 root ') { $hOk = $false; $hDetails += "archivio: '$($enc[0])' (atteso 600 root)" }
        $remoteArchive = ($enc[0] -split ' ', 3)[2]
        if (-not ($lines | Where-Object { $_ -match '\.sha256$' })) { $hOk = $false; $hDetails += 'manca il file .sha256 accanto all''archivio' }

        # l'archivio esce dalla VM (il reset la cancella): copia leggibile dall'utente, scp, controllo SHA-256
        $cp = Invoke-VmSsh -Command "sudo cp '$remoteArchive' '$remoteArchive.sha256' /tmp/gitstack-e2e/backup/ && sudo chown -R `$(id -u):`$(id -g) /tmp/gitstack-e2e/backup && sha256sum /tmp/gitstack-e2e/backup/*.enc" -TimeoutSeconds $script:SshCommandTimeoutSeconds
        if ($cp.ExitCode -ne 0) { throw "copia dell'archivio in /tmp sulla VM: exit $($cp.ExitCode) $($cp.StdErr)" }
        $pull = Copy-FromVm -RemotePath '/tmp/gitstack-e2e/backup/*' -LocalPath $phaseDir -TimeoutSeconds 600
        if ($pull.ExitCode -ne 0) { throw "scp dell'archivio verso l'host: exit $($pull.ExitCode) $($pull.StdErr)" }
        $localArchive = Get-ChildItem -LiteralPath $phaseDir -Filter '*.tar.gz.enc' | Select-Object -First 1
        $localKey = Join-Path $phaseDir 'chiave'
        if (-not $localArchive -or -not (Test-Path -LiteralPath $localKey)) { throw 'archivio o chiave non arrivati sull''host' }
        $remoteSum = ($cp.StdOut.Trim() -split '\s+')[0]
        $localSum = (Get-FileHash -LiteralPath $localArchive.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($remoteSum -ne $localSum) { throw "SHA-256 dell'archivio sull'host ($localSum) diverso da quello sulla VM ($remoteSum)" }
        $script:BackupArchiveLocal = $localArchive.FullName
        $script:BackupKeyLocal = $localKey
        Write-Log "Archivio sull'host: $($localArchive.FullName) ($($localArchive.Length) byte)"

        # i servizi sono tornati su dopo la finestra di sola lettura, e i dati ci sono ancora
        $w = Wait-E2eHealthy -BaseUrl $BaseUrl -TimeoutSeconds 180
        if ($w) { throw "dopo il backup: $w" }
        $pb = Test-E2eDataset -Data $data -BaseUrl $BaseUrl -CaPath $CaPath -WorkDir (Join-Path $phaseDir 'verifica-dopo-backup') -AdminCookie $adminCk
        if ($pb.Count -gt 0) { $hOk = $false; $hDetails += "dopo il backup: $($pb -join '; ')" }
    } catch {
        $hOk = $false; $hDetails += $_.Exception.Message
    }
    Add-StepResult -Name 'h. dati di prova (utente, org, repo HTTPS+SSH, issue con allegato) e gitstack backup cifrato: archivio 0600 in cartella 0700, copiato sull host con SHA-256 uguale, servizi di nuovo sani' -Ok $hOk -Detail ($hDetails -join '; ')
    if (-not $script:BackupArchiveLocal -or -not $data) { return }

    # --- i. reset, reinstallazione della stessa versione, restore, verifica ---
    Write-Log "==> Passo i: reset al checkpoint clean, reinstallazione di $InstallRef, gitstack restore, verifica ..."
    $iOk = $true; $iDetails = @()
    try {
        $why = Invoke-E2eVmReset -Label 'restore'
        if ($why) { throw "reset: $why" }
        Write-Log "IP della VM dopo il reset: $script:VmIp (prima: $oldIp)"
        # con Pebble nel passo h-pub: il resolver ACME (e il volume di acme.json) deve esserci anche qui,
        # altrimenti l'archivio ACME del backup non ha dove tornare
        $acmeServer = if ($script:E2eAcmeOn) { $script:E2eAcmeCaServer } else { '' }
        $why = Invoke-E2eInstall -InstallRef $InstallRef -LogName 'installer-restore.log' -AppsDomain $script:AppsDomain -AcmeCaServer $acmeServer
        if ($why) { throw $why }
        $baseUrl2 = "https://$script:VmIp"
        $caPath2 = Join-Path $phaseDir 'ca-nuova-installazione.crt'
        $fpNew = Set-E2eCaFromVm -CaPath $caPath2
        Write-Log "CA della nuova installazione: $fpNew (originale: $fpBefore)"
        $w = Wait-E2eHealthy -BaseUrl $baseUrl2 -TimeoutSeconds 300
        if ($w) { throw "nuova installazione: $w" }

        # restore: archivio e chiave sulla VM, poi gitstack restore
        $mk = Invoke-VmSsh -Command 'umask 077; mkdir -p /tmp/gitstack-e2e/restore' -TimeoutSeconds $script:SshCommandTimeoutSeconds
        if ($mk.ExitCode -ne 0) { throw "cartella /tmp/gitstack-e2e/restore: exit $($mk.ExitCode) $($mk.StdErr)" }
        $archiveName = Split-Path -Leaf $script:BackupArchiveLocal
        foreach ($f in @($script:BackupArchiveLocal, $script:BackupKeyLocal)) {
            $c = Copy-ToVm -LocalPath $f -RemotePath "/tmp/gitstack-e2e/restore/$(Split-Path -Leaf $f)" -TimeoutSeconds 600
            if ($c.ExitCode -ne 0) { throw "scp di $(Split-Path -Leaf $f) sulla VM: exit $($c.ExitCode) $($c.StdErr)" }
        }
        $rs = Invoke-VmSsh -Command "sudo gitstack restore --key-file /tmp/gitstack-e2e/restore/chiave '/tmp/gitstack-e2e/restore/$archiveName'" -TimeoutSeconds 900
        $rsLog = Join-Path $phaseDir 'restore.log'
        Write-E2eText -Path $rsLog -Stdout $rs.StdOut -Stderr $rs.StdErr
        if ($rs.ExitCode -ne 0) { throw "gitstack restore: exit $($rs.ExitCode) (log: $rsLog): $($rs.StdErr)" }

        # dopo il restore: la CA e quella del backup (la sua chiave e nel backup). Il certificato del
        # server ora e firmato da lei, non da quella della nuova installazione di cui l'host si fidava
        # (GIT-505, ipotesi c): la CA si scarica dalla VM (porta 80) PRIMA dell'attesa su HTTPS.
        $caPath3 = Join-Path $phaseDir 'ca-dopo-restore.crt'
        $fpAfter = Wait-E2eRestoredCaAndHealthy -BaseUrl $baseUrl2 -Ip $script:VmIp -CaPath $caPath3 -DiagPath (Join-Path $phaseDir 'diagnosi-tls-dopo-restore.txt')
        if ($fpAfter -ne $fpBefore) {
            $iOk = $false; $iDetails += "la CA dopo il restore ($fpAfter) non e quella del backup ($fpBefore): i client che si fidavano della CA dovrebbero rifare la fiducia"
        }

        # admin: la password cambiata prima del backup vale, quella della nuova installazione no
        $adminCk2 = $null
        try { $adminCk2 = Get-E2eAdminCookie -BaseUrl $baseUrl2 -Password $AdminPassword } catch { $iOk = $false; $iDetails += "admin dopo il restore: $($_.Exception.Message)" }

        # dati e clone (la chiave host SSH ripristinata evita l'avviso: la verifica e Strict)
        $pr = Test-E2eDataset -Data $data -BaseUrl $baseUrl2 -CaPath $caPath3 -WorkDir (Join-Path $phaseDir 'verifica-dopo-restore') -OldIp $oldIp -AdminCookie $adminCk2
        if ($pr.Count -gt 0) { $iOk = $false; $iDetails += "dati dopo il restore: $($pr -join '; ')" }

        $gs = Invoke-VmSsh -Command 'sudo gitstack status' -TimeoutSeconds 60
        foreach ($expected in @("Versione server: sha-$InstallRef", 'Stato: sano')) {
            if ($gs.StdOut -notmatch [regex]::Escape($expected)) { $iOk = $false; $iDetails += "gitstack status dopo il restore: manca '$expected'" }
        }
        $script:RestoredBaseUrl = $baseUrl2
    } catch {
        $iOk = $false; $iDetails += $_.Exception.Message
    }
    Add-StepResult -Name 'i. reset, reinstallazione della stessa versione, gitstack restore: CA e admin come prima, utente/token/org/repo/issue/allegato intatti, clone HTTPS e SSH identici (host key invariata), status sano' -Ok $iOk -Detail ($iDetails -join '; ')

    # --- i-pub. dominio e certificato dopo il restore (GIT-468): quattro righe ok/ko e l'esito ---
    if ($script:E2ePubCtx) {
        Write-Log "==> Passo i-pub: dominio proprio e certificato dopo il restore (quattro controlli) ..."
        if (-not $script:RestoredBaseUrl -or $script:RestoredBaseUrl -ne "https://$script:VmIp") {
            Add-StepResult -Name 'i-pub. dominio e certificato dopo il restore' -Ok $false -Detail 'il restore non e arrivato in fondo (passo i fallito): controlli non eseguiti'
        } else {
            $pubAfter = Test-E2eAppPublicAfterRestore -BaseUrl $script:RestoredBaseUrl -Data $data -Ctx $script:E2ePubCtx -WorkDir (Join-Path $phaseDir 'app-pubblica-dopo-restore')
            foreach ($pr in $pubAfter) { Add-StepResult -Name $pr.Name -Ok $pr.Ok -Detail $pr.Detail }
            $pubAfterOk = @($pubAfter | Where-Object { $_.Ok }).Count
            Add-StepResult -Name "i-pub. esito complessivo: $pubAfterOk/$(@($pubAfter).Count) controlli ok" -Ok ($pubAfterOk -eq @($pubAfter).Count) -Detail "$(@($pubAfter).Count - $pubAfterOk) controlli ko (vedi le righe i-pub sopra)"
        }
    } elseif ($script:E2eAcmeOn) {
        Add-StepResult -Name 'i-pub. dominio e certificato dopo il restore' -Ok $false -Detail 'il passo h-pub non e arrivato al certificato: niente da verificare dopo il restore'
    }

    # --- i-app. l'app col suo stato dopo il restore (GIT-388): sette righe ok/ko e l'esito ---
    if ($appCtx) {
        Write-Log "==> Passo i-app: app e registry dopo il restore (sette controlli) ..."
        if (-not $script:RestoredBaseUrl -or $script:RestoredBaseUrl -ne "https://$script:VmIp") {
            Add-StepResult -Name 'i-app. app dopo il restore' -Ok $false -Detail 'il restore non e arrivato in fondo (passo i fallito): controlli dell''app non eseguiti'
            return
        }
        $rows = Test-E2eAppAfterRestore -BaseUrl $script:RestoredBaseUrl -Data $data -Ctx $appCtx -WorkDir (Join-Path $phaseDir 'app-dopo-restore') -RestoreLog "$($rs.StdOut)`n$($rs.StdErr)"
        foreach ($r in $rows) { Add-StepResult -Name $r.Name -Ok $r.Ok -Detail $r.Detail }
        $okCount = @($rows | Where-Object { $_.Ok }).Count
        Add-StepResult -Name "i-app. esito complessivo: $okCount/$(@($rows).Count) controlli dell'app ok" -Ok ($okCount -eq @($rows).Count) -Detail "$(@($rows).Count - $okCount) controlli ko (vedi le righe i-app sopra)"
    }
}

# --- Fase j: upgrade dal commit precedente (DF/F) --------------------------

function Invoke-E2eUpgradePhase {
    param(
        [Parameter(Mandatory)][string]$InstallRef,
        [Parameter(Mandatory)][string]$PreviousRef
    )
    $phaseDir = Join-Path $script:OutDir 'upgrade'
    New-Item -ItemType Directory -Path $phaseDir -Force | Out-Null
    Write-Log "==> Passo j: installazione del commit precedente ($PreviousRef), dati, gitstack upgrade a $InstallRef, verifica ..."
    $jOk = $true; $jDetails = @()
    $adminPw = 'E2e-upgrade-password-lunga-42'
    try {
        $why = Invoke-E2eVmReset -Label 'upgrade'
        if ($why) { throw "reset: $why" }
        $why = Invoke-E2eInstall -InstallRef $PreviousRef -LogName 'installer-precedente.log'
        if ($why) { throw $why }
        $baseUrl = "https://$script:VmIp"
        $caPath = Join-Path $phaseDir 'ca.crt'
        $fpBefore = Set-E2eCaFromVm -CaPath $caPath
        $w = Wait-E2eHealthy -BaseUrl $baseUrl -TimeoutSeconds 300
        if ($w) { throw "installazione precedente: $w" }

        $gs0 = Invoke-VmSsh -Command 'sudo gitstack status' -TimeoutSeconds 60
        if ($gs0.StdOut -notmatch [regex]::Escape("Versione server: sha-$PreviousRef")) { throw "prima dell'upgrade la versione non e sha-${PreviousRef}: $($gs0.StdOut)" }

        $ck = Initialize-E2eAdmin -BaseUrl $baseUrl -NewPassword $adminPw
        $data = New-E2eDataset -BaseUrl $baseUrl -AdminCookie $ck -CaPath $caPath -WorkDir (Join-Path $phaseDir 'dati') -Tag 'u'
        Write-Log "Dati di prova sulla versione precedente: utente $($data.User), repo $($data.Repo) (HEAD $($data.HeadSha))"

        # un restore di un archivio di un'altra versione si rifiuta (exit 6) senza fermare niente
        if ($script:BackupArchiveLocal -and (Test-Path -LiteralPath $script:BackupArchiveLocal)) {
            $mk = Invoke-VmSsh -Command 'umask 077; mkdir -p /tmp/gitstack-e2e/restore' -TimeoutSeconds $script:SshCommandTimeoutSeconds
            $cpA = Copy-ToVm -LocalPath $script:BackupArchiveLocal -RemotePath "/tmp/gitstack-e2e/restore/$(Split-Path -Leaf $script:BackupArchiveLocal)" -TimeoutSeconds 600
            $cpK = Copy-ToVm -LocalPath $script:BackupKeyLocal -RemotePath '/tmp/gitstack-e2e/restore/chiave' -TimeoutSeconds 120
            if ($mk.ExitCode -eq 0 -and $cpA.ExitCode -eq 0 -and $cpK.ExitCode -eq 0) {
                $bad = Invoke-VmSsh -Command "sudo gitstack restore --key-file /tmp/gitstack-e2e/restore/chiave '/tmp/gitstack-e2e/restore/$(Split-Path -Leaf $script:BackupArchiveLocal)'" -TimeoutSeconds 300
                Write-E2eText -Path (Join-Path $phaseDir 'restore-versione-diversa.log') -Stdout $bad.StdOut -Stderr $bad.StdErr
                if ($bad.ExitCode -ne 6 -or (($bad.StdOut + $bad.StdErr) -notmatch '(?i)versione diversa|versione')) {
                    $jOk = $false; $jDetails += "restore di un archivio di un'altra versione: exit $($bad.ExitCode) (atteso 6 con il motivo): $($bad.StdErr)"
                }
                $w = Wait-E2eHealthy -BaseUrl $baseUrl -TimeoutSeconds 60
                if ($w) { $jOk = $false; $jDetails += "dopo il restore rifiutato i servizi non sono sani: $w" }
            } else {
                $jOk = $false; $jDetails += 'impossibile copiare l''archivio sulla VM per la prova del restore rifiutato'
            }
        } else {
            Write-Log 'Nessun archivio dalla fase h: salto la prova del restore con versione diversa.'
        }

        # dry-run: solo controlli, non cambia niente
        $dry = Invoke-VmSsh -Command "sudo env GITSTACK_REPO=$script:GitStackRepo gitstack upgrade --to $InstallRef --dry-run" -TimeoutSeconds 300
        Write-E2eText -Path (Join-Path $phaseDir 'upgrade-dry-run.log') -Stdout $dry.StdOut -Stderr $dry.StdErr
        if ($dry.ExitCode -ne 0) { throw "gitstack upgrade --dry-run: exit $($dry.ExitCode): $($dry.StdOut) $($dry.StdErr). Il commit precedente deve gia avere il comando upgrade (GIT-147): usa -PreviousRef con un commit piu recente." }

        # upgrade vero, con backup preventivo
        $up = Invoke-VmSsh -Command "sudo env GITSTACK_REPO=$script:GitStackRepo gitstack upgrade --to $InstallRef" -TimeoutSeconds 1500
        $upLog = Join-Path $phaseDir 'upgrade.log'
        Write-E2eText -Path $upLog -Stdout $up.StdOut -Stderr $up.StdErr
        if ($up.ExitCode -ne 0) { throw "gitstack upgrade: exit $($up.ExitCode) (7 = tornato com'era, 8 = rollback fallito; log: $upLog)" }

        # verifica
        $w = Wait-E2eHealthy -BaseUrl $baseUrl -TimeoutSeconds 300
        if ($w) { $jOk = $false; $jDetails += "dopo l'upgrade: $w" }
        $gs = Invoke-VmSsh -Command 'sudo gitstack status' -TimeoutSeconds 60
        foreach ($expected in @("Versione server: sha-$InstallRef", 'Stato: sano')) {
            if ($gs.StdOut -notmatch [regex]::Escape($expected)) { $jOk = $false; $jDetails += "gitstack status dopo l'upgrade: manca '$expected'" }
        }
        $bin = Invoke-VmSsh -Command 'gitstack version' -TimeoutSeconds 30
        Write-Log "gitstack version dopo l'upgrade: $($bin.StdOut.Trim())"
        $bk = Invoke-VmSsh -Command "sudo find /var/backups/gitstack -maxdepth 1 -name '*.tar.gz*' -not -name '*.sha256' | wc -l" -TimeoutSeconds 30
        if ($bk.ExitCode -ne 0 -or [int]($bk.StdOut.Trim()) -lt 1) { $jOk = $false; $jDetails += "nessun backup preventivo in /var/backups/gitstack dopo l'upgrade" }
        $fpAfter = Get-CertSha256Fingerprint -Path $caPath
        $fpVm = Invoke-VmSsh -Command 'sudo gitstack-tls fingerprint' -TimeoutSeconds 30
        if ($fpVm.StdOut.Trim() -ne $fpBefore) { $jOk = $false; $jDetails += "la CA e cambiata con l'upgrade: '$($fpVm.StdOut.Trim())' (prima $fpBefore)" }
        $ck2 = $null
        try { $ck2 = Get-E2eAdminCookie -BaseUrl $baseUrl -Password $adminPw } catch { $jOk = $false; $jDetails += "admin dopo l'upgrade: $($_.Exception.Message)" }
        $pu = Test-E2eDataset -Data $data -BaseUrl $baseUrl -CaPath $caPath -WorkDir (Join-Path $phaseDir 'verifica-dopo-upgrade') -AdminCookie $ck2
        if ($pu.Count -gt 0) { $jOk = $false; $jDetails += "dati dopo l'upgrade: $($pu -join '; ')" }
        $ui = Invoke-HttpRaw -Uri "$baseUrl/"
        if ($ui.StatusCode -ne 200 -or $ui.Body -notmatch '(?i)<div id="root"') { $jOk = $false; $jDetails += "UI dopo l'upgrade: status $($ui.StatusCode)" }
    } catch {
        $jOk = $false; $jDetails += $_.Exception.Message
    }
    Add-StepResult -Name "j. upgrade: installazione del commit precedente, dati, restore di un altra versione rifiutato (exit 6), gitstack upgrade --dry-run e vero, versione nuova, backup preventivo, CA e admin invariati, dati e clone intatti" -Ok $jOk -Detail ($jDetails -join '; ')
}

# Commit precedente a $Sha su main (primo genitore), via API GitHub.
function Get-E2ePreviousCommit {
    param([Parameter(Mandatory)][string]$Repo, [Parameter(Mandatory)][string]$Sha)
    $c = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/commits/$Sha" -TimeoutSec 30 -Headers @{ 'User-Agent' = 'gitstack-e2e'; Accept = 'application/vnd.github+json' }
    $p = @($c.parents)
    if ($p.Count -lt 1) { throw "il commit $Sha non ha genitori" }
    return [string]$p[0].sha
}
