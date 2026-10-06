# deploy/test-vm/lib/phases.ps1
#
# Fasi aggiuntive di e2e.ps1 (GIT-148, DF/G): backup, ripristino su macchina
# pulita e upgrade dal commit precedente. Dot-sourced da e2e.ps1, che
# fornisce Write-Log, Add-StepResult, Invoke-ExternalCommand, Invoke-VmSsh,
# Copy-ToVm, Copy-FromVm, Invoke-RemoteHelper, Initialize-RemoteHelper e le
# variabili $script:VmIp, $script:VmName, $script:OutDir, ... Solo ASCII:
# Windows PowerShell 5.1 legge in ANSI i .ps1 senza BOM.

# --- Piccoli aiuti ---------------------------------------------------------

function Get-E2eInstallCommand {
    param([Parameter(Mandatory)][string]$InstallRef)
    return "curl -fsSL https://raw.githubusercontent.com/$script:GitStackRepo/$InstallRef/deploy/install.sh | sudo GITSTACK_REF=$InstallRef GITSTACK_ADMIN_REQUIRED=1 bash"
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
    param([Parameter(Mandatory)][string]$InstallRef, [Parameter(Mandatory)][string]$LogName)
    $r = Invoke-VmSsh -Command (Get-E2eInstallCommand -InstallRef $InstallRef) -TimeoutSeconds $script:InstallTimeoutSeconds
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
        $up = Invoke-ExternalCommand -FilePath 'curl.exe' -ArgumentList @('-sS', '--ssl-no-revoke', '--cacert', $CaPath, '-H', "Authorization: Bearer $($d.Token)", '-F', "file=@$attFile;type=text/plain", '-w', '\n%{http_code}', "$BaseUrl/api/v1/repos/$($d.User)/$($d.Repo)/issue-attachments") -TimeoutSeconds 60
        $upLines = @($up.StdOut -split "`n" | Where-Object { $_ -ne '' })
        if ($up.ExitCode -ne 0 -or $upLines.Count -lt 2 -or $upLines[-1].Trim() -ne '201') { throw "upload allegato: exit $($up.ExitCode), risposta '$($up.StdOut)' $($up.StdErr)" }
        $d.AttachmentId = ($upLines[0] | ConvertFrom-Json).id
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
    $att = Invoke-ExternalCommand -FilePath 'curl.exe' -ArgumentList @('-sS', '--ssl-no-revoke', '--cacert', $CaPath, '-H', "Authorization: Bearer $($Data.Token)", '-o', $dl, '-w', '%{http_code}', "$repoApi/issue-attachments/$($Data.AttachmentId)") -TimeoutSeconds 60
    if ($att.ExitCode -ne 0 -or $att.StdOut.Trim() -ne '200') {
        $problems.Add("download allegato: exit $($att.ExitCode), status '$($att.StdOut.Trim())' $($att.StdErr)")
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

    # --- h. dati di prova e backup (sullo stato dell'installazione appena provata) ---
    Write-Log "==> Passo h: dati di prova e gitstack backup cifrato ..."
    $hOk = $true; $hDetails = @()
    $data = $null; $fpBefore = $null; $oldIp = $script:VmIp
    try {
        $fpBefore = Get-CertSha256Fingerprint -Path $CaPath
        $adminCk = Get-E2eAdminCookie -BaseUrl $BaseUrl -Password $AdminPassword
        $data = New-E2eDataset -BaseUrl $BaseUrl -AdminCookie $adminCk -CaPath $CaPath -WorkDir (Join-Path $phaseDir 'dati') -Tag 'b'
        Write-Log "Dati di prova: utente $($data.User), org $($data.Org), repo $($data.Repo) (HEAD $($data.HeadSha)), issue #$($data.IssueNumber) con allegato"

        $gen = Invoke-VmSsh -Command 'umask 077; mkdir -p /tmp/gitstack-e2e/backup && openssl rand -base64 32 > /tmp/gitstack-e2e/backup/chiave' -TimeoutSeconds $script:SshCommandTimeoutSeconds
        if ($gen.ExitCode -ne 0) { throw "generazione della chiave di backup sulla VM: exit $($gen.ExitCode) $($gen.StdErr)" }
        $bk = Invoke-VmSsh -Command 'sudo gitstack backup --key-file /tmp/gitstack-e2e/backup/chiave' -TimeoutSeconds 900
        $bkLog = Join-Path $phaseDir 'backup.log'
        Write-E2eText -Path $bkLog -Stdout $bk.StdOut -Stderr $bk.StdErr
        if ($bk.ExitCode -ne 0) { throw "gitstack backup: exit $($bk.ExitCode) (log: $bkLog): $($bk.StdErr)" }
        $win = @(($bk.StdOut + "`n" + $bk.StdErr) -split "`r?`n" | Where-Object { $_ -match 'Finestra di sola lettura' })
        Write-Log "Finestra di sola lettura (da riportare nel README di admin): $(if ($win) { $win -join ' | ' } else { 'riga non trovata nell''output' })"

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
        $why = Invoke-E2eInstall -InstallRef $InstallRef -LogName 'installer-restore.log'
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

        # dopo il restore: la CA e quella del backup (la sua chiave e nel backup), HTTPS si fida ancora di lei
        $w = Wait-E2eHealthy -BaseUrl $baseUrl2 -TimeoutSeconds 300
        if ($w) { throw "dopo il restore: $w" }
        $caPath3 = Join-Path $phaseDir 'ca-dopo-restore.crt'
        $fpAfter = Set-E2eCaFromVm -CaPath $caPath3
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
