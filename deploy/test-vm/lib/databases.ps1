# deploy/test-vm/lib/databases.ps1
#
# Database gestiti nel ciclo backup -> restore (GIT-558, v3-d, DB13-DB17). Dot-sourced da e2e.ps1 dopo
# phases.ps1 (usa Get-E2eBearer, Wait-E2eAppServing, Write-E2eText). Lo lancia il board con
# `e2e.ps1 -WithDatabases`, che accende i database gestiti nell'installer (--enable-databases).
#
#   Prima del backup (Test-E2eDatabases, cinque righe h-db):
#     1 crea il database e lo porta a running, con la password alla creazione
#     2 lo collega a un'app di prova (variabili DATABASE_URL e PG* fra i segreti, app di nuovo serving)
#     3 scrive un marcatore dal pod
#     4 accesso dalla LAN, dalla VM: verify-full con la CA accettato; senza CA, senza TLS e con un
#       nome host sbagliato respinti (e2e/gs-db.sh lan)
#     5 backup su richiesta completed
#   Subito dopo (Invoke-E2eDatabasesSpec, una riga): prepara un'app pronta dell'utente di prova e lancia
#     `pnpm exec playwright test e2e/databases.spec.ts` con E2E_BASE_URL, E2E_ADMIN_PASSWORD,
#     E2E_DATABASE_APP_REPO ed E2E_DATABASE_APP_NAME (nella CI k3d lo spec si salta: questa e' la sua
#     esecuzione vera).
#   Dopo `gitstack restore` su VM pulita (Test-E2eDatabasesAfterRestore, quattro righe i-db): il database
#     torna running, il marcatore e' ancora li, il collegamento e' stato ricreato, la LAN con verify-full.
#
# Ogni funzione ritorna oggetti {Name, Ok, Detail}, sempre; al primo errore la diagnostica del namespace del
# database finisce in diag.log. Solo ASCII: Windows PowerShell 5.1 legge in ANSI i .ps1 senza BOM.

$script:E2eDbName = 'e2edb'
$script:E2eDbAppName = 'e2edbapp'
$script:E2eDbSpecAppName = 'e2edbspec'
$script:E2eDbRemoteDir = '/tmp/gitstack-e2e'

# Accesso dell'utente del dataset con la sessione (i database non sono nello scope del token del dataset).
function Get-E2eUserCookie {
    param([Parameter(Mandatory)][string]$BaseUrl, [Parameter(Mandatory)]$Data)
    $l = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/auth/login" -Method 'POST' -Body (@{ username = $Data.User; password = $Data.Password } | ConvertTo-Json)
    if ($l.StatusCode -ne 200 -or -not $l.Cookie) { throw "login dell'utente $($Data.User): status $($l.StatusCode)" }
    return [string]$l.Cookie
}

# Aspetta che il database sia running. Ritorna l'oggetto Database. Lancia se failed o allo scadere.
function Wait-E2eDatabaseRunning {
    param([Parameter(Mandatory)][string]$DbUrl, [Parameter(Mandatory)][string]$Cookie, [int]$TimeoutSeconds = 600)
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $last = ''
    while ((Get-Date) -lt $deadline) {
        $r = Invoke-HttpRaw -Uri $DbUrl -Cookie $Cookie
        if ($r.StatusCode -eq 200) {
            $db = $r.Body | ConvertFrom-Json
            if ($db.status -eq 'running') { return $db }
            if ($db.status -eq 'failed') { throw "il database e in stato failed" }
            $last = "stato $($db.status)"
        } else {
            $last = "status $($r.StatusCode) $($r.Error)"
        }
        Start-Sleep -Seconds 5
    }
    throw "il database non e running dopo ${TimeoutSeconds}s (ultimo: $last)"
}

# Aspetta che un'app abbia una versione serving piu' recente di $After (dopo un collegamento l'app riparte).
function Wait-E2eAppServingAfter {
    param([Parameter(Mandatory)][string]$AppsApi, [Parameter(Mandatory)][hashtable]$Headers, [Parameter(Mandatory)][string]$AppName, [Parameter(Mandatory)][int]$After, [int]$TimeoutSeconds = 300)
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $r = Invoke-HttpRaw -Uri "$AppsApi/$AppName/versions?per_page=1" -Headers $Headers
        if ($r.StatusCode -eq 200) {
            $items = @(($r.Body | ConvertFrom-Json).items)
            if ($items.Count -gt 0) {
                $v = $items[0]
                if ($v.state -eq 'failed' -or $v.state -eq 'rolled_back') { throw "versione $($v.number) dell'app: stato $($v.state)" }
                if ($v.state -eq 'serving' -and [int]$v.number -gt $After) { return [int]$v.number }
            }
        }
        Start-Sleep -Seconds 5
    }
    throw "l'app $AppName non ha una versione serving piu recente della $After dopo ${TimeoutSeconds}s"
}

# Numero della versione piu' recente dell'app (0 se non c'e'): serve a sapere quando il collegamento ha fatto ripartire l'app.
function Get-E2eAppLatestVersion {
    param([Parameter(Mandatory)][string]$AppsApi, [Parameter(Mandatory)][hashtable]$Headers, [Parameter(Mandatory)][string]$AppName)
    $r = Invoke-HttpRaw -Uri "$AppsApi/$AppName/versions?per_page=1" -Headers $Headers
    if ($r.StatusCode -ne 200) { throw "GET versions di ${AppName}: status $($r.StatusCode)" }
    $items = @(($r.Body | ConvertFrom-Json).items)
    if ($items.Count -eq 0) { return 0 }
    return [int]$items[0].number
}

# Nomi dei segreti dell'app (mai i valori).
function Get-E2eAppSecretNames {
    param([Parameter(Mandatory)][string]$AppsApi, [Parameter(Mandatory)][hashtable]$Headers, [Parameter(Mandatory)][string]$AppName)
    $r = Invoke-HttpRaw -Uri "$AppsApi/$AppName/secrets" -Headers $Headers
    if ($r.StatusCode -ne 200) { throw "GET secrets di ${AppName}: status $($r.StatusCode)" }
    return @(($r.Body | ConvertFrom-Json).items | ForEach-Object { [string]$_.name })
}

# Crea un'app dal tag v1 dell'immagine di prova del passo h-app (e2eimg) e la porta a serving. Ritorna il numero di versione.
function New-E2eDatabaseTestApp {
    param([Parameter(Mandatory)][string]$AppsApi, [Parameter(Mandatory)][hashtable]$Headers, [Parameter(Mandatory)][string]$AppName)
    $c = Invoke-HttpRaw -Uri $AppsApi -Method 'POST' -Headers $Headers -Body (@{ name = $AppName; image = "$($script:E2eAppImageName):v1" } | ConvertTo-Json) -TimeoutSec 60
    if ($c.StatusCode -ne 202) { throw "POST apps $AppName`: status $($c.StatusCode) $($c.Body) $($c.Error)" }
    return Wait-E2eAppServing -AppsApi $AppsApi -Headers $Headers -AppName $AppName
}

# Esegue un comando di gs-db.sh sulla VM con le variabili d'ambiente date (mappa nome -> valore). Ritorna l'oggetto di Invoke-VmSsh.
function Invoke-E2eDbVm {
    param([Parameter(Mandatory)][string]$Command, [hashtable]$EnvVars = @{}, [int]$TimeoutSeconds = 300)
    $pairs = @()
    foreach ($k in ($EnvVars.Keys | Sort-Object)) { $pairs += "$k='$($EnvVars[$k])'" }
    $envText = if ($pairs.Count -gt 0) { ($pairs -join ' ') + ' ' } else { '' }
    return Invoke-VmSsh -Command "${envText}bash $script:E2eDbRemoteDir/gs-db.sh $Command" -TimeoutSeconds $TimeoutSeconds
}

# Copia sulla VM lo script e la CA dell'istanza (file db-ca.crt).
function Copy-E2eDbFilesToVm {
    param([Parameter(Mandatory)][string]$CaPath)
    $mk = Invoke-VmSsh -Command "mkdir -p $script:E2eDbRemoteDir" -TimeoutSeconds $script:SshCommandTimeoutSeconds
    if ($mk.ExitCode -ne 0) { throw "mkdir sulla VM: exit $($mk.ExitCode) $($mk.StdErr)" }
    $cp = Copy-ToVm -LocalPath (Join-Path (Split-Path -Parent $PSScriptRoot) 'e2e\gs-db.sh') -RemotePath "$script:E2eDbRemoteDir/gs-db.sh" -TimeoutSeconds $script:SshCommandTimeoutSeconds
    if ($cp.ExitCode -ne 0) { throw "scp di gs-db.sh: exit $($cp.ExitCode) $($cp.StdErr)" }
    $cc = Copy-ToVm -LocalPath $CaPath -RemotePath "$script:E2eDbRemoteDir/db-ca.crt" -TimeoutSeconds $script:SshCommandTimeoutSeconds
    if ($cc.ExitCode -ne 0) { throw "scp della CA: exit $($cc.ExitCode) $($cc.StdErr)" }
}

# Accesso dalla LAN: verify-full accettato, respinti senza CA / senza TLS / con nome sbagliato. Ritorna $null se ok, altrimenti il motivo.
# Il primo passo si ritenta (la porta si apre qualche secondo dopo la risposta di ServiceLB).
function Test-E2eDatabaseLan {
    param([Parameter(Mandatory)]$Lan, [Parameter(Mandatory)][string]$DbNamespace, [Parameter(Mandatory)][string]$DbName, [Parameter(Mandatory)][string]$User, [Parameter(Mandatory)][string]$Password, [Parameter(Mandatory)][string]$Mark, [Parameter(Mandatory)][string]$WorkDir, [Parameter(Mandatory)][string]$LogName, [int]$Attempts = 8)
    $hostName = if ($Lan.host) { [string]$Lan.host } else { [string]$script:VmIp }
    $envVars = @{ GS_DB_HOST = $hostName; GS_DB_PORT = [string]$Lan.port; GS_DB_NAME = $DbName; GS_DB_USER = $User; GS_DB_PASSWORD = $Password; GS_DB_MARK = $Mark; GS_CA = "$script:E2eDbRemoteDir/db-ca.crt" }
    $last = $null
    for ($i = 1; $i -le $Attempts; $i++) {
        $last = Invoke-E2eDbVm -Command 'lan' -EnvVars $envVars -TimeoutSeconds 300
        Write-E2eText -Path (Join-Path $WorkDir $LogName) -Stdout $last.StdOut -Stderr $last.StdErr
        if ($last.ExitCode -eq 0) { break }
        if ($last.StdOut -match 'STEP ok lan-verify-full') { break }  # il primo passo e andato: un errore dopo non si ritenta
        if ($i -lt $Attempts) { Start-Sleep -Seconds 15 }
    }
    $out = "$($last.StdOut)"
    foreach ($s in 'lan-verify-full', 'lan-no-ca', 'lan-no-tls', 'lan-wrong-host') {
        if ($out -notmatch "STEP ok $s") { return "manca 'STEP ok $s' (exit $($last.ExitCode)): $out $($last.StdErr)" }
    }
    return $null
}

# Diagnostica del namespace del database sulla VM, in diag.log. Non lancia.
function Save-E2eDatabaseDiag {
    param([string]$DbNamespace, [Parameter(Mandatory)][string]$WorkDir)
    if (-not $DbNamespace) { return }
    try {
        $dg = Invoke-E2eDbVm -Command 'diag' -EnvVars @{ GS_DB_NS = $DbNamespace } -TimeoutSeconds 120
        Write-E2eText -Path (Join-Path $WorkDir 'diag.log') -Stdout $dg.StdOut -Stderr $dg.StdErr
    } catch {
        Write-Log "ATTENZIONE: diagnostica del database non raccolta: $($_.Exception.Message)"
    }
}

# --- Prima del backup --------------------------------------------------------
# Ritorna {Rows, Ctx}; Ctx e' $null se il database non e stato creato (niente da verificare dopo il restore).
function Test-E2eDatabases {
    param(
        [Parameter(Mandatory)][string]$BaseUrl,
        [Parameter(Mandatory)][string]$CaPath,
        [Parameter(Mandatory)]$Data,
        [Parameter(Mandatory)][string]$WorkDir
    )
    New-Item -ItemType Directory -Path $WorkDir -Force | Out-Null
    $names = @(
        'h-db 1/5 crea un database PostgreSQL: parte (running) e la password si vede alla creazione',
        'h-db 2/5 collegato a un''app di prova: DATABASE_URL e PG* fra i segreti, l''app riparte (serving)',
        'h-db 3/5 scrive dati nel database (marcatore riletto dal pod)',
        'h-db 4/5 accesso LAN: verify-full con la CA accettato; senza CA, senza TLS e con nome sbagliato respinti',
        'h-db 5/5 backup su richiesta completato'
    )
    $rows = @()
    foreach ($n in $names) { $rows += [pscustomobject]@{ Name = $n; Ok = $false; Detail = 'non verificato' } }
    $ctx = $null
    $dbNs = ''
    $i = 0
    try {
        $ck = Get-E2eUserCookie -BaseUrl $BaseUrl -Data $Data
        $h = Get-E2eBearer -Data $Data
        $dbApi = "$BaseUrl/api/v1/databases/$($Data.User)"
        $dbUrl = "$dbApi/$script:E2eDbName"
        $appsApi = "$BaseUrl/api/v1/repos/$($Data.User)/$($Data.Repo)/apps"
        $mark = "e2e-db-$([guid]::NewGuid().ToString('N'))"
        Copy-E2eDbFilesToVm -CaPath $CaPath

        # 1. creazione
        $i = 0
        $c = Invoke-HttpRaw -Uri $dbApi -Method 'POST' -Cookie $ck -Body (@{ name = $script:E2eDbName; pgMajor = 16 } | ConvertTo-Json) -TimeoutSec 60
        if ($c.StatusCode -ne 202) { throw "POST databases: status $($c.StatusCode) $($c.Error) (corpo non stampato: ha la password)" }
        $created = $c.Body | ConvertFrom-Json
        $dbId = [string]$created.database.id
        $dbNs = "gitstack-db-$dbId"
        $cred = $created.credentials
        if (-not $cred.password -or $cred.username -ne "$($script:E2eDbName)_owner" -or $cred.sslMode -ne 'verify-full') { throw "credenziali alla creazione inattese (utente '$($cred.username)', sslMode '$($cred.sslMode)', password presente: $([bool]$cred.password))" }
        $db = Wait-E2eDatabaseRunning -DbUrl $dbUrl -Cookie $ck
        $rows[0].Ok = $true; $rows[0].Detail = "id $dbId, PostgreSQL $($db.pgMajor)"

        # 2. collegamento a un'app di prova
        $i = 1
        $v0 = New-E2eDatabaseTestApp -AppsApi $appsApi -Headers $h -AppName $script:E2eDbAppName
        $lk = Invoke-HttpRaw -Uri "$dbUrl/links" -Method 'POST' -Cookie $ck -Body (@{ app = @{ owner = $Data.User; repo = $Data.Repo; name = $script:E2eDbAppName }; mode = 'rw' } | ConvertTo-Json -Depth 4) -TimeoutSec 60
        if ($lk.StatusCode -ne 201) { throw "POST links: status $($lk.StatusCode) $($lk.Error)" }
        $linkId = [string](($lk.Body | ConvertFrom-Json).link.id)
        [void](Wait-E2eAppServingAfter -AppsApi $appsApi -Headers $h -AppName $script:E2eDbAppName -After $v0)
        $secrets = Get-E2eAppSecretNames -AppsApi $appsApi -Headers $h -AppName $script:E2eDbAppName
        $missing = @('DATABASE_URL', 'PGHOST', 'PGPORT', 'PGDATABASE', 'PGUSER', 'PGPASSWORD', 'PGSSLMODE', 'PGSSLROOTCERT' | Where-Object { $secrets -notcontains $_ })
        if ($missing.Count -gt 0) { throw "all'app mancano i segreti: $($missing -join ', ')" }
        $rows[1].Ok = $true; $rows[1].Detail = "collegamento $linkId"

        # 3. dati
        $i = 2
        $w = Invoke-E2eDbVm -Command 'write' -EnvVars @{ GS_DB_NS = $dbNs; GS_DB_NAME = $script:E2eDbName; GS_DB_MARK = $mark }
        Write-E2eText -Path (Join-Path $WorkDir 'write.log') -Stdout $w.StdOut -Stderr $w.StdErr
        if ($w.ExitCode -ne 0 -or $w.StdOut -notmatch 'STEP ok write') { throw "scrittura: exit $($w.ExitCode) $($w.StdOut) $($w.StdErr)" }
        $rows[2].Ok = $true; $rows[2].Detail = ''

        # 4. LAN
        $i = 3
        $lan = Invoke-HttpRaw -Uri "$dbUrl/lan" -Method 'PUT' -Cookie $ck -Body (@{ enabled = $true } | ConvertTo-Json) -TimeoutSec 120
        if ($lan.StatusCode -ne 200) { throw "PUT lan: status $($lan.StatusCode) $($lan.Body) $($lan.Error)" }
        $lanObj = $lan.Body | ConvertFrom-Json
        if (-not $lanObj.enabled -or -not $lanObj.port) { throw "PUT lan: risposta senza enabled/port: $($lan.Body)" }
        $why = Test-E2eDatabaseLan -Lan $lanObj -DbNamespace $dbNs -DbName $script:E2eDbName -User $cred.username -Password $cred.password -Mark $mark -WorkDir $WorkDir -LogName 'lan.log'
        if ($why) { throw $why }
        $rows[3].Ok = $true; $rows[3].Detail = "porta $($lanObj.port)"

        # 5. backup su richiesta
        $i = 4
        $b = Invoke-HttpRaw -Uri "$dbUrl/backups" -Method 'POST' -Cookie $ck -Body '{}' -TimeoutSec 60
        if ($b.StatusCode -ne 202) { throw "POST backups: status $($b.StatusCode) $($b.Body) $($b.Error)" }
        $backupId = [string](($b.Body | ConvertFrom-Json).id)
        $deadline = (Get-Date).AddSeconds(300)
        $state = ''
        while ((Get-Date) -lt $deadline) {
            $bl = Invoke-HttpRaw -Uri "$dbUrl/backups" -Cookie $ck
            if ($bl.StatusCode -eq 200) {
                $mine = @(($bl.Body | ConvertFrom-Json).items | Where-Object { $_.id -eq $backupId })
                if ($mine.Count -eq 1) {
                    $state = [string]$mine[0].status
                    if ($state -eq 'completed') { break }
                    if ($state -eq 'failed') { throw "backup $backupId fallito: $($mine[0].error)" }
                }
            }
            Start-Sleep -Seconds 5
        }
        if ($state -ne 'completed') { throw "backup $backupId non completato dopo 300s (stato '$state')" }
        $rows[4].Ok = $true; $rows[4].Detail = "backup $backupId"

        $ctx = [pscustomobject]@{
            DbId = $dbId; Namespace = $dbNs; Name = $script:E2eDbName; Owner = [string]$Data.User; Mark = $mark
            Username = [string]$cred.username; LinkedApp = $script:E2eDbAppName; AppsApi = $appsApi
        }
    } catch {
        $msg = $_.Exception.Message
        $rows[$i].Detail = $msg
        foreach ($r2 in $rows) { if (-not $r2.Ok -and $r2.Detail -eq 'non verificato') { $r2.Detail = "non verificato: un passo precedente e fallito ($msg)" } }
        Save-E2eDatabaseDiag -DbNamespace $dbNs -WorkDir $WorkDir
        # il database creato resta: se c'e e ha i dati, i controlli dopo il restore lo trovano comunque
        if ($dbNs -and $rows[2].Ok) {
            $ctx = [pscustomobject]@{ DbId = $dbId; Namespace = $dbNs; Name = $script:E2eDbName; Owner = [string]$Data.User; Mark = $mark; Username = "$($script:E2eDbName)_owner"; LinkedApp = $script:E2eDbAppName; AppsApi = "$BaseUrl/api/v1/repos/$($Data.User)/$($Data.Repo)/apps" }
        }
    }
    return [pscustomobject]@{ Rows = $rows; Ctx = $ctx }
}

# --- Spec Playwright del portale (GIT-555) --------------------------------------
# Prepara un'app pronta dell'utente di prova (stesso owner del database che lo spec crea) e lancia lo spec dalla
# cartella web/ dell'host. Una riga: ok solo se Playwright esce con 0 e scrive "1 passed" (uno spec saltato esce
# con 0 e scrive "skipped": non e' una prova).
function Invoke-E2eDatabasesSpec {
    param(
        [Parameter(Mandatory)][string]$BaseUrl,
        [Parameter(Mandatory)]$Data,
        [Parameter(Mandatory)][string]$WorkDir,
        [string]$WebDir = ''
    )
    New-Item -ItemType Directory -Path $WorkDir -Force | Out-Null
    $row = [pscustomobject]@{ Name = 'h-db spec. pnpm exec playwright test e2e/databases.spec.ts: gestione database dal portale (1 passed)'; Ok = $false; Detail = 'non verificato' }
    if (-not $WebDir) { $WebDir = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..\..\web')) }
    $saved = @{}
    $varNames = 'E2E_BASE_URL', 'E2E_ADMIN_USERNAME', 'E2E_ADMIN_PASSWORD', 'E2E_DATABASE_APP_REPO', 'E2E_DATABASE_APP_NAME', 'E2E_CHROMIUM_ARGS'
    foreach ($n in $varNames) { $saved[$n] = [Environment]::GetEnvironmentVariable($n) }
    try {
        if (-not (Test-Path -LiteralPath (Join-Path $WebDir 'package.json'))) { throw "cartella web non trovata: $WebDir" }
        $h = Get-E2eBearer -Data $Data
        $appsApi = "$BaseUrl/api/v1/repos/$($Data.User)/$($Data.Repo)/apps"
        [void](New-E2eDatabaseTestApp -AppsApi $appsApi -Headers $h -AppName $script:E2eDbSpecAppName)

        $env:E2E_BASE_URL = $BaseUrl
        $env:E2E_ADMIN_USERNAME = [string]$Data.User
        $env:E2E_ADMIN_PASSWORD = [string]$Data.Password
        $env:E2E_DATABASE_APP_REPO = [string]$Data.Repo
        $env:E2E_DATABASE_APP_NAME = $script:E2eDbSpecAppName
        # il certificato dell'istanza e di una CA privata: Chromium di prova non la conosce
        $env:E2E_CHROMIUM_ARGS = '--ignore-certificate-errors'

        Push-Location -LiteralPath $WebDir
        try {
            if (-not (Test-Path -LiteralPath (Join-Path $WebDir 'node_modules'))) {
                $inst = Invoke-ExternalCommand -FilePath 'cmd.exe' -ArgumentList @('/c', 'corepack', 'pnpm', 'install', '--frozen-lockfile') -TimeoutSeconds 900
                Write-E2eText -Path (Join-Path $WorkDir 'pnpm-install.log') -Stdout $inst.StdOut -Stderr $inst.StdErr
                if ($inst.ExitCode -ne 0) { throw "pnpm install: exit $($inst.ExitCode) (log: pnpm-install.log)" }
            }
            $br = Invoke-ExternalCommand -FilePath 'cmd.exe' -ArgumentList @('/c', 'corepack', 'pnpm', 'exec', 'playwright', 'install', 'chromium') -TimeoutSeconds 900
            Write-E2eText -Path (Join-Path $WorkDir 'playwright-install.log') -Stdout $br.StdOut -Stderr $br.StdErr
            if ($br.ExitCode -ne 0) { throw "playwright install chromium: exit $($br.ExitCode) (log: playwright-install.log)" }
            $pw = Invoke-ExternalCommand -FilePath 'cmd.exe' -ArgumentList @('/c', 'corepack', 'pnpm', 'exec', 'playwright', 'test', 'e2e/databases.spec.ts') -TimeoutSeconds 1500
        } finally {
            Pop-Location
        }
        $log = Join-Path $WorkDir 'playwright.log'
        Write-E2eText -Path $log -Stdout $pw.StdOut -Stderr $pw.StdErr
        $text = "$($pw.StdOut)`n$($pw.StdErr)"
        if ($pw.ExitCode -ne 0) { throw "playwright: exit $($pw.ExitCode) (log: $log): $(($text -split "`r?`n" | Select-Object -Last 8) -join ' | ')" }
        if ($text -notmatch '\b1 passed\b') { throw "playwright e uscito con 0 ma senza '1 passed' (spec saltato?) (log: $log)" }
        $row.Ok = $true; $row.Detail = 'app e2edbspec, utente ' + $Data.User
    } catch {
        $row.Detail = $_.Exception.Message
    } finally {
        foreach ($n in $varNames) { [Environment]::SetEnvironmentVariable($n, $saved[$n]) }
    }
    return $row
}

# --- Dopo `gitstack restore` su VM pulita ---------------------------------------
function Test-E2eDatabasesAfterRestore {
    param(
        [Parameter(Mandatory)][string]$BaseUrl,
        [Parameter(Mandatory)][string]$CaPath,
        [Parameter(Mandatory)]$Data,
        [Parameter(Mandatory)]$Ctx,
        [Parameter(Mandatory)][string]$WorkDir,
        [int]$LinkTimeoutSeconds = 300
    )
    New-Item -ItemType Directory -Path $WorkDir -Force | Out-Null
    $names = @(
        'i-db 1/4 il database e tornato running dopo il restore',
        'i-db 2/4 i dati ci sono (marcatore riletto dal pod) e la password si rilegge solo ruotandola',
        'i-db 3/4 il collegamento all''app e stato ricreato: link attivo e variabili al loro posto',
        'i-db 4/4 accesso LAN dopo il restore: verify-full con la CA accettato; senza CA, senza TLS e con nome sbagliato respinti'
    )
    $rows = @()
    foreach ($n in $names) { $rows += [pscustomobject]@{ Name = $n; Ok = $false; Detail = 'non verificato' } }
    $i = 0
    $dbNs = [string]$Ctx.Namespace
    try {
        $ck = Get-E2eUserCookie -BaseUrl $BaseUrl -Data $Data
        $h = Get-E2eBearer -Data $Data
        $dbUrl = "$BaseUrl/api/v1/databases/$($Ctx.Owner)/$($Ctx.Name)"
        Copy-E2eDbFilesToVm -CaPath $CaPath

        # 1. il controller ricrea il database vuoto e gitstack restore lo riempie: il namespace puo cambiare id
        $db = Wait-E2eDatabaseRunning -DbUrl $dbUrl -Cookie $ck
        $dbNs = "gitstack-db-$($db.id)"
        $rows[0].Ok = $true; $rows[0].Detail = "id $($db.id)"

        # 2. dati: la password nuova si ottiene ruotando (nessun segreto di database viaggia nel backup)
        $i = 1
        $rot = Invoke-HttpRaw -Uri "$dbUrl/credentials/rotate" -Method 'POST' -Cookie $ck -Body '{}' -TimeoutSec 120
        if ($rot.StatusCode -ne 200) { throw "POST credentials/rotate: status $($rot.StatusCode) $($rot.Error)" }
        $cred = ($rot.Body | ConvertFrom-Json).credentials
        if (-not $cred.password) { throw 'la rotazione non ha restituito la password' }
        $rd = Invoke-E2eDbVm -Command 'read' -EnvVars @{ GS_DB_NS = $dbNs; GS_DB_NAME = $Ctx.Name; GS_DB_MARK = $Ctx.Mark }
        Write-E2eText -Path (Join-Path $WorkDir 'read.log') -Stdout $rd.StdOut -Stderr $rd.StdErr
        if ($rd.ExitCode -ne 0 -or $rd.StdOut -notmatch 'STEP ok read') { throw "rilettura: exit $($rd.ExitCode) $($rd.StdOut) $($rd.StdErr)" }
        $rows[1].Ok = $true; $rows[1].Detail = ''

        # 3. collegamento ricreato (password nuova, app ridistribuita)
        $i = 2
        $deadline = (Get-Date).AddSeconds($LinkTimeoutSeconds)
        $active = @()
        while ((Get-Date) -lt $deadline) {
            $ll = Invoke-HttpRaw -Uri "$dbUrl/links" -Cookie $ck
            if ($ll.StatusCode -eq 200) {
                $active = @(($ll.Body | ConvertFrom-Json).items | Where-Object { $_.state -eq 'active' -and $_.app.name -eq $Ctx.LinkedApp })
                if ($active.Count -ge 1) { break }
            }
            Start-Sleep -Seconds 5
        }
        if ($active.Count -lt 1) { throw "nessun collegamento active per l'app $($Ctx.LinkedApp) entro ${LinkTimeoutSeconds}s" }
        $secrets = Get-E2eAppSecretNames -AppsApi $Ctx.AppsApi -Headers $h -AppName $Ctx.LinkedApp
        $missing = @('DATABASE_URL', 'PGPASSWORD', 'PGSSLROOTCERT' | Where-Object { $secrets -notcontains $_ })
        if ($missing.Count -gt 0) { throw "all'app mancano i segreti: $($missing -join ', ')" }
        $rows[2].Ok = $true; $rows[2].Detail = "collegamento $($active[0].id)"

        # 4. LAN: se il restore non l'ha riaperta si accende ora e il Detail lo dice
        $i = 3
        $lan = Invoke-HttpRaw -Uri "$dbUrl/lan" -Cookie $ck
        if ($lan.StatusCode -ne 200) { throw "GET lan: status $($lan.StatusCode) $($lan.Error)" }
        $lanObj = $lan.Body | ConvertFrom-Json
        $note = 'LAN gia accesa dal restore'
        if (-not $lanObj.enabled) {
            $on = Invoke-HttpRaw -Uri "$dbUrl/lan" -Method 'PUT' -Cookie $ck -Body (@{ enabled = $true } | ConvertTo-Json) -TimeoutSec 120
            if ($on.StatusCode -ne 200) { throw "PUT lan: status $($on.StatusCode) $($on.Body) $($on.Error)" }
            $lanObj = $on.Body | ConvertFrom-Json
            $note = 'LAN spenta dopo il restore, riaccesa dal passo'
        }
        $why = Test-E2eDatabaseLan -Lan $lanObj -DbNamespace $dbNs -DbName $Ctx.Name -User $cred.username -Password $cred.password -Mark $Ctx.Mark -WorkDir $WorkDir -LogName 'lan.log'
        if ($why) { throw $why }
        $rows[3].Ok = $true; $rows[3].Detail = "$note, porta $($lanObj.port)"
    } catch {
        $msg = $_.Exception.Message
        $rows[$i].Detail = $msg
        foreach ($r2 in $rows) { if (-not $r2.Ok -and $r2.Detail -eq 'non verificato') { $r2.Detail = "non verificato: un passo precedente e fallito ($msg)" } }
        Save-E2eDatabaseDiag -DbNamespace $dbNs -WorkDir $WorkDir
    }
    return , $rows
}
