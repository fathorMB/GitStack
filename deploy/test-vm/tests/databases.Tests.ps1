# deploy/test-vm/tests/databases.Tests.ps1
#
# Prova, senza VM e senza istanza, il passo e2e dei database gestiti (GIT-558, lib/databases.ps1):
# API, VM (ssh/scp) e Playwright sono finti. Copre: comando di installazione con --enable-databases,
# esito completo prima del backup, errore a meta (le righe dopo non verificate, diagnostica), spec
# Playwright (variabili d'ambiente, "1 passed", spec saltato = ko, ambiente ripristinato) e i controlli
# dopo il restore. Test Pester (Describe/It, Pester 3.4+); da lanciare con Windows PowerShell 5.1:
#   powershell.exe -NoProfile -Command "Invoke-Pester -Path deploy	est-vm	estsdatabases.Tests.ps1 -EnableExit"
# Lo script shell e' provato a parte, con sudo/kubectl e psql finti: bash deploy/test-vm/e2e/gs-db.test.sh
# Esce con codice != 0 se un It fallisce. Solo ASCII.

$ErrorActionPreference = 'Stop'

# Dipendenze che in produzione arrivano da e2e.ps1: qui sono finte.
function Write-Log { param([string]$Message) }
function Add-StepResult { param($Name, $Ok, $Detail) }
function Start-Sleep { param([int]$Seconds) }
function Invoke-ExternalCommand {
    param([string]$FilePath, [string[]]$ArgumentList, [int]$TimeoutSeconds = 60)
    $global:Ext += [pscustomobject]@{ FilePath = $FilePath; Args = ($ArgumentList -join ' '); Env = @{
            Base = $env:E2E_BASE_URL; User = $env:E2E_ADMIN_USERNAME; Pass = $env:E2E_ADMIN_PASSWORD
            Repo = $env:E2E_DATABASE_APP_REPO; App = $env:E2E_DATABASE_APP_NAME; Chromium = $env:E2E_CHROMIUM_ARGS
        }; Cwd = (Get-Location).Path
    }
    $out = ''
    $code = 0
    if (($ArgumentList -join ' ') -like '*playwright test*') { $out = $global:PwOut; $code = $global:PwExit }
    return [pscustomobject]@{ ExitCode = $code; StdOut = $out; StdErr = ''; TimedOut = $false }
}
. (Join-Path $PSScriptRoot '..\lib\phases.ps1')
. (Join-Path $PSScriptRoot '..\lib\http.ps1')
. (Join-Path $PSScriptRoot '..\lib\databases.ps1')

$script:GitStackRepo = 'owner/GitStack'
$script:OutDir = Join-Path ([IO.Path]::GetTempPath()) 'gs-databases-tests'
$script:SshCommandTimeoutSeconds = 5
$script:VmIp = '10.0.0.9'
$data = [pscustomobject]@{ User = 'u1'; Password = 'pw-u1'; Token = 'tok'; Repo = 'r1' }
$caFile = Join-Path $script:OutDir 'ca.crt'
New-Item -ItemType Directory -Path $script:OutDir -Force | Out-Null
Set-Content -LiteralPath $caFile -Value 'CA' -Encoding ascii

Describe 'Installer con i database' {
    $cmd = Get-E2eInstallCommand -InstallRef 'abc123' -AppsDomain 'apps.e2e.test' -Databases
    It 'install: con -Databases e le app accende i database dopo --apps-domain' {
        ($cmd -like '*bash -s -- --enable-apps --apps-domain apps.e2e.test --enable-databases') | Should Be $true
    }
    $script:E2eDatabasesSkipPreflight = $true
    $cmd = Get-E2eInstallCommand -InstallRef 'abc123' -AppsDomain 'apps.e2e.test' -Databases
    It 'install: --skip-preflight solo se il board lo chiede' {
        ($cmd -like '*--enable-databases --skip-preflight') | Should Be $true
    }
    $script:E2eDatabasesSkipPreflight = $false
    $cmd = Get-E2eInstallCommand -InstallRef 'abc123' -AppsDomain 'apps.e2e.test'
    It 'install: senza -Databases il comando e quello di prima' {
        ($cmd -notlike '*databases*') | Should Be $true
    }
    $cmd = Get-E2eInstallCommand -InstallRef 'abc123' -Databases
    It 'install: senza le app i database non si accendono' {
        ($cmd -notlike '*databases*') | Should Be $true
    }

}

# --- Finti: API e VM -----------------------------------------------------------
function New-Mock { return @{ Calls = @(); Cmds = @(); Scp = @(); FailVm = ''; DbStatus = 'running'; BackupStatus = 'completed'; Secrets = @('DATABASE_URL', 'PGHOST', 'PGPORT', 'PGDATABASE', 'PGUSER', 'PGPASSWORD', 'PGSSLMODE', 'PGSSLROOTCERT'); LanEnabled = $false; LinkState = 'active'; Version = 1; LanFirstFails = 0; LanTries = 0 } }
$global:M = New-Mock
function Invoke-HttpRaw {
    param([string]$Uri, [string]$Method = 'GET', [string]$Body, [hashtable]$Headers, [string]$Cookie, [int]$TimeoutSec = 20)
    $global:M.Calls += "$Method $Uri"
    $json = { param($o, $code = 200) [pscustomobject]@{ StatusCode = $code; Body = ($o | ConvertTo-Json -Depth 6); Cookie = 'gst_session=x' } }
    if ($Uri -like '*/auth/login') { return (& $json @{ mustChangePassword = $false }) }
    if ($Method -eq 'POST' -and $Uri -like '*/apps') { return (& $json @{ app = @{ id = 'a1' }; version = @{ number = 1 } } 202) }
    if ($Uri -like '*/versions?per_page=1') { return (& $json @{ items = @(@{ number = $global:M.Version; state = 'serving' }) }) }
    if ($Uri -like '*/secrets') { return (& $json @{ items = @($global:M.Secrets | ForEach-Object { @{ name = $_ } }) }) }
    if ($Method -eq 'POST' -and $Uri -like '*/databases/u1') { return (& $json @{ database = @{ id = 'dbid1'; pgMajor = 16; status = 'creating' }; credentials = @{ username = 'e2edb_owner'; password = 'pw-created'; database = 'e2edb'; sslMode = 'verify-full' } } 202) }
    if ($Method -eq 'POST' -and $Uri -like '*/links') { $global:M.Version = 2; return (& $json @{ link = @{ id = 'link1'; state = 'active' }; credentials = @{ password = 'x' } } 201) }
    if ($Method -eq 'GET' -and $Uri -like '*/links') { return (& $json @{ items = @(@{ id = 'link1'; state = $global:M.LinkState; app = @{ name = 'e2edbapp' } }) }) }
    if ($Method -eq 'PUT' -and $Uri -like '*/lan') { $global:M.LanEnabled = $true; return (& $json @{ enabled = $true; port = 15432; host = '10.0.0.9' }) }
    if ($Method -eq 'GET' -and $Uri -like '*/lan') { return (& $json @{ enabled = $global:M.LanEnabled; port = 15432; host = '10.0.0.9' }) }
    if ($Method -eq 'POST' -and $Uri -like '*/backups') { return (& $json @{ id = 'bk1'; status = 'in_progress' } 202) }
    if ($Method -eq 'GET' -and $Uri -like '*/backups') { return (& $json @{ items = @(@{ id = 'bk1'; kind = 'on_demand'; status = $global:M.BackupStatus; error = 'boom' }) }) }
    if ($Method -eq 'POST' -and $Uri -like '*/credentials/rotate') { return (& $json @{ database = @{ id = 'dbid2' }; credentials = @{ username = 'e2edb_owner'; password = 'pw-rotated'; database = 'e2edb'; sslMode = 'verify-full' } }) }
    if ($Method -eq 'GET' -and $Uri -like '*/databases/u1/e2edb') { return (& $json @{ id = 'dbid2'; status = $global:M.DbStatus; pgMajor = 16 }) }
    return [pscustomobject]@{ StatusCode = 404; Body = ''; Cookie = $null }
}
function Copy-ToVm { param($LocalPath, $RemotePath, $TimeoutSeconds) $global:M.Scp += $RemotePath; return [pscustomobject]@{ ExitCode = 0; StdOut = ''; StdErr = '' } }
function Invoke-VmSsh {
    param($Command, $TimeoutSeconds)
    $global:M.Cmds += $Command
    if ($global:M.FailVm -and $Command -like "*$($global:M.FailVm)*") { return [pscustomobject]@{ ExitCode = 1; StdOut = 'STEP FAIL x: finto'; StdErr = '' } }
    $out = 'STEP ok x'
    if ($Command -like '*gs-db.sh write') { $out = 'STEP ok write' }
    elseif ($Command -like '*gs-db.sh read') { $out = 'STEP ok read' }
    elseif ($Command -like '*gs-db.sh lan') {
        $global:M.LanTries++
        if ($global:M.LanTries -le $global:M.LanFirstFails) { return [pscustomobject]@{ ExitCode = 1; StdOut = 'STEP FAIL lan-verify-full: connection refused'; StdErr = '' } }
        $out = "STEP ok lan-verify-full`nSTEP ok lan-no-ca`nSTEP ok lan-no-tls`nSTEP ok lan-wrong-host"
    }
    return [pscustomobject]@{ ExitCode = 0; StdOut = $out; StdErr = '' }
}

Describe 'h-db prima del backup' {
    $global:Ext = @()
    $res = Test-E2eDatabases -BaseUrl 'https://10.0.0.9' -CaPath $caFile -Data $data -WorkDir (Join-Path $script:OutDir 'db')
    It 'prima del backup: cinque righe, tutte ok' {
        (@($res.Rows).Count -eq 5 -and @($res.Rows | Where-Object { -not $_.Ok }).Count -eq 0) | Should Be $true
    }
    It 'prima del backup: contesto con namespace del database e utente proprietario' {
        ($res.Ctx.Namespace -eq 'gitstack-db-dbid1' -and $res.Ctx.Username -eq 'e2edb_owner' -and $res.Ctx.Mark -like 'e2e-db-*') | Should Be $true
    }
    It 'prima del backup: gli script e la CA vanno sulla VM' {
        (($global:M.Scp -contains '/tmp/gitstack-e2e/gs-db.sh') -and ($global:M.Scp -contains '/tmp/gitstack-e2e/db-ca.crt')) | Should Be $true
    }
    It 'prima del backup: la LAN riceve host, porta e la password della creazione, mai nei log delle righe' {
        (@($global:M.Cmds | Where-Object { $_ -like "*GS_DB_HOST='10.0.0.9'*GS_DB_PASSWORD='pw-created'*GS_DB_PORT='15432'*bash*gs-db.sh lan" }).Count -eq 1 -and @($res.Rows | Where-Object { "$($_.Detail)" -like '*pw-created*' }).Count -eq 0) | Should Be $true
    }
    It 'prima del backup: il database si crea con major 16 e il collegamento e rw' {
        (@($global:M.Calls | Where-Object { $_ -eq 'POST https://10.0.0.9/api/v1/databases/u1' }).Count -eq 1 -and @($global:M.Calls | Where-Object { $_ -like 'POST *e2edb/links' }).Count -eq 1) | Should Be $true
    }

    # LAN con la porta che si apre al secondo tentativo
    $global:M = New-Mock; $global:M.LanFirstFails = 2
    $res = Test-E2eDatabases -BaseUrl 'https://10.0.0.9' -CaPath $caFile -Data $data -WorkDir (Join-Path $script:OutDir 'db-lan-retry')
    It 'LAN: la porta che si apre tardi si ritenta e il passo e ok' {
        ($res.Rows[3].Ok -and $global:M.LanTries -eq 3) | Should Be $true
    }

    # Errore alla scrittura dei dati: righe 4 e 5 non verificate, diagnostica del namespace, contesto non creato
    $global:M = New-Mock; $global:M.FailVm = 'gs-db.sh write'
    $res = Test-E2eDatabases -BaseUrl 'https://10.0.0.9' -CaPath $caFile -Data $data -WorkDir (Join-Path $script:OutDir 'db-ko')
    It 'errore alla scrittura: righe 1 e 2 ok, 3 ko con il motivo, 4 e 5 non verificate' {
        ($res.Rows[0].Ok -and $res.Rows[1].Ok -and -not $res.Rows[2].Ok -and $res.Rows[2].Detail -like '*scrittura*' -and $res.Rows[3].Detail -like 'non verificato*' -and $res.Rows[4].Detail -like 'non verificato*') | Should Be $true
    }
    It 'errore alla scrittura: nessuna LAN aperta e niente backup' {
        (@($global:M.Calls | Where-Object { $_ -like 'PUT *lan' -or $_ -like 'POST *backups' }).Count -eq 0) | Should Be $true
    }
    It 'errore alla scrittura: parte la diagnostica del namespace e il contesto manca' {
        (@($global:M.Cmds | Where-Object { $_ -like "*GS_DB_NS='gitstack-db-dbid1'*gs-db.sh diag" }).Count -eq 1 -and $null -eq $res.Ctx) | Should Be $true
    }

    # Segreti mancanti sull'app collegata
    $global:M = New-Mock; $global:M.Secrets = @('DATABASE_URL')
    $res = Test-E2eDatabases -BaseUrl 'https://10.0.0.9' -CaPath $caFile -Data $data -WorkDir (Join-Path $script:OutDir 'db-sec')
    It 'collegamento: segreti mancanti, riga 2 ko e li nomina' {
        (-not $res.Rows[1].Ok -and $res.Rows[1].Detail -like '*PGPASSWORD*') | Should Be $true
    }

    # Backup fallito
    $global:M = New-Mock; $global:M.BackupStatus = 'failed'
    $res = Test-E2eDatabases -BaseUrl 'https://10.0.0.9' -CaPath $caFile -Data $data -WorkDir (Join-Path $script:OutDir 'db-bk')
    It 'backup fallito: riga 5 ko con l''errore, le altre ok, il contesto c''e' {
        ($res.Rows[3].Ok -and -not $res.Rows[4].Ok -and $res.Rows[4].Detail -like '*boom*' -and $null -ne $res.Ctx) | Should Be $true
    }

}

Describe 'Spec Playwright del portale' {
    $webDir = Join-Path $script:OutDir 'web'
    New-Item -ItemType Directory -Path (Join-Path $webDir 'node_modules') -Force | Out-Null
    Set-Content -LiteralPath (Join-Path $webDir 'package.json') -Value '{}' -Encoding ascii
    $env:E2E_BASE_URL = 'valore-originale'; $env:E2E_CHROMIUM_ARGS = $null
    $global:M = New-Mock; $global:Ext = @(); $global:PwOut = "Running 1 test using 1 worker`n  1 passed (40s)"; $global:PwExit = 0
    $row = Invoke-E2eDatabasesSpec -BaseUrl 'https://10.0.0.9' -Data $data -WorkDir (Join-Path $script:OutDir 'spec') -WebDir $webDir
    $pwCall = @($global:Ext | Where-Object { $_.Args -like '*playwright test*' })
    It 'spec: 1 passed, riga ok' {
        ($row.Ok) | Should Be $true
    }
    It 'spec: il comando e pnpm exec playwright test e2e/databases.spec.ts dalla cartella web' {
        ($pwCall.Count -eq 1 -and $pwCall[0].Args -like '*pnpm exec playwright test e2e/databases.spec.ts' -and $pwCall[0].Cwd -eq (Resolve-Path -LiteralPath $webDir).Path) | Should Be $true
    }
    It 'spec: le quattro variabili del corpo dell''item e l''utente di prova' {
        ($pwCall[0].Env.Base -eq 'https://10.0.0.9' -and $pwCall[0].Env.Pass -eq 'pw-u1' -and $pwCall[0].Env.Repo -eq 'r1' -and $pwCall[0].Env.App -eq 'e2edbspec' -and $pwCall[0].Env.User -eq 'u1') | Should Be $true
    }
    It 'spec: l''app pronta e creata e porta il certificato privato accettato da Chromium' {
        (@($global:M.Calls | Where-Object { $_ -like 'POST *apps' }).Count -eq 1 -and $pwCall[0].Env.Chromium -like '*ignore-certificate-errors*') | Should Be $true
    }
    It 'spec: l''ambiente del processo e ripristinato' {
        ($env:E2E_BASE_URL -eq 'valore-originale' -and -not $env:E2E_CHROMIUM_ARGS -and -not $env:E2E_DATABASE_APP_NAME) | Should Be $true
    }

    $global:PwOut = "Running 1 test`n  1 skipped"; $global:PwExit = 0
    $row = Invoke-E2eDatabasesSpec -BaseUrl 'https://10.0.0.9' -Data $data -WorkDir (Join-Path $script:OutDir 'spec-skip') -WebDir $webDir
    It 'spec: saltato (exit 0 senza 1 passed) e ko' {
        (-not $row.Ok -and $row.Detail -like '*saltato*') | Should Be $true
    }
    $global:PwOut = "1 failed`n  gestione database dal portale"; $global:PwExit = 1
    $row = Invoke-E2eDatabasesSpec -BaseUrl 'https://10.0.0.9' -Data $data -WorkDir (Join-Path $script:OutDir 'spec-ko') -WebDir $webDir
    It 'spec: exit 1 e ko, con le ultime righe' {
        (-not $row.Ok -and $row.Detail -like '*exit 1*' -and $row.Detail -like '*gestione database*') | Should Be $true
    }
    $row = Invoke-E2eDatabasesSpec -BaseUrl 'https://10.0.0.9' -Data $data -WorkDir (Join-Path $script:OutDir 'spec-noweb') -WebDir (Join-Path $script:OutDir 'non-esiste')
    It 'spec: cartella web mancante, ko senza lanciare niente' {
        (-not $row.Ok -and $row.Detail -like '*cartella web*') | Should Be $true
    }
    $env:E2E_BASE_URL = $null

}

Describe 'i-db dopo il restore' {
    $ctx = [pscustomobject]@{ DbId = 'dbid1'; Namespace = 'gitstack-db-dbid1'; Name = 'e2edb'; Owner = 'u1'; Mark = 'e2e-db-abc'; Username = 'e2edb_owner'; LinkedApp = 'e2edbapp'; AppsApi = 'https://10.0.0.9/api/v1/repos/u1/r1/apps' }
    $global:M = New-Mock
    $rows = Test-E2eDatabasesAfterRestore -BaseUrl 'https://10.0.0.9' -CaPath $caFile -Data $data -Ctx $ctx -WorkDir (Join-Path $script:OutDir 'dopo')
    It 'dopo il restore: quattro righe, tutte ok' {
        (@($rows).Count -eq 4 -and @($rows | Where-Object { -not $_.Ok }).Count -eq 0) | Should Be $true
    }
    It 'dopo il restore: il namespace e quello del database ripristinato (id nuovo) e la LAN usa la password ruotata' {
        (@($global:M.Cmds | Where-Object { $_ -like "*GS_DB_NS='gitstack-db-dbid2'*gs-db.sh read" }).Count -eq 1 -and @($global:M.Cmds | Where-Object { $_ -like "*GS_DB_PASSWORD='pw-rotated'*gs-db.sh lan" }).Count -eq 1) | Should Be $true
    }
    It 'dopo il restore: LAN spenta dal restore, riaccesa dal passo e il Detail lo dice' {
        ($rows[3].Detail -like '*riaccesa*' -and @($global:M.Calls | Where-Object { $_ -like 'PUT *lan' }).Count -eq 1) | Should Be $true
    }

    $global:M = New-Mock; $global:M.LanEnabled = $true
    $rows = Test-E2eDatabasesAfterRestore -BaseUrl 'https://10.0.0.9' -CaPath $caFile -Data $data -Ctx $ctx -WorkDir (Join-Path $script:OutDir 'dopo-lan')
    It 'dopo il restore: LAN gia accesa, nessuna PUT' {
        ($rows[3].Ok -and @($global:M.Calls | Where-Object { $_ -like 'PUT *lan' }).Count -eq 0) | Should Be $true
    }

    $global:M = New-Mock; $global:M.FailVm = 'gs-db.sh read'
    $rows = Test-E2eDatabasesAfterRestore -BaseUrl 'https://10.0.0.9' -CaPath $caFile -Data $data -Ctx $ctx -WorkDir (Join-Path $script:OutDir 'dopo-ko')
    It 'dopo il restore: dati mancanti, riga 2 ko, 3 e 4 non verificate, diagnostica' {
        ($rows[0].Ok -and -not $rows[1].Ok -and $rows[1].Detail -like '*rilettura*' -and $rows[2].Detail -like 'non verificato*' -and $rows[3].Detail -like 'non verificato*' -and @($global:M.Cmds | Where-Object { $_ -like '*gs-db.sh diag' }).Count -eq 1) | Should Be $true
    }

    $global:M = New-Mock; $global:M.LinkState = 'unlinking'
    $rows = Test-E2eDatabasesAfterRestore -BaseUrl 'https://10.0.0.9' -CaPath $caFile -Data $data -Ctx $ctx -WorkDir (Join-Path $script:OutDir 'dopo-link') -LinkTimeoutSeconds 1
    It 'dopo il restore: collegamento non active e riga 3 ko' {
        ($rows[1].Ok -and -not $rows[2].Ok -and $rows[2].Detail -like '*nessun collegamento active*') | Should Be $true
    }


}