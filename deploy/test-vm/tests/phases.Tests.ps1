# deploy/test-vm/tests/phases.Tests.ps1
#
# Prova, senza VM, i pezzi puri di lib/phases.ps1 (GIT-148): comando di
# installazione, riga ssh, URL del repo, ripristino dell'ambiente di
# Invoke-E2eGit e lettura del commit precedente da una risposta GitHub finta.
# Da lanciare con Windows PowerShell 5.1:
#   powershell.exe -NoProfile -File deploy\test-vm\tests\phases.Tests.ps1
# Esce con codice != 0 se una verifica fallisce. Solo ASCII.

$ErrorActionPreference = 'Stop'

# Dipendenze che in produzione arrivano da e2e.ps1: qui sono finte.
function Write-Log { param([string]$Message) }
function Add-StepResult { param($Name, $Ok, $Detail) }
function Invoke-ExternalCommand {
    param([string]$FilePath, [string[]]$ArgumentList, [int]$TimeoutSeconds = 60)
    $script:LastExternal = [pscustomobject]@{ FilePath = $FilePath; Args = $ArgumentList; Env = $env:GIT_SSH_COMMAND; Prompt = $env:GIT_TERMINAL_PROMPT }
    return [pscustomobject]@{ ExitCode = 0; StdOut = ''; StdErr = ''; TimedOut = $false }
}
. (Join-Path $PSScriptRoot '..\lib\phases.ps1')
. (Join-Path $PSScriptRoot '..\lib\http.ps1')

$failures = New-Object System.Collections.Generic.List[string]
function Assert-That {
    param([string]$Name, [bool]$Condition)
    if ($Condition) { Write-Host "[PASS] $Name" } else { Write-Host "[FAIL] $Name"; $failures.Add($Name) }
}

$script:GitStackRepo = 'owner/GitStack'
$script:GitSshPort = 2222

$cmd = Get-E2eInstallCommand -InstallRef 'abc123'
Assert-That 'install: punta a install.sh del commit' ($cmd -like '*raw.githubusercontent.com/owner/GitStack/abc123/deploy/install.sh*')
Assert-That 'install: GITSTACK_REF e admin obbligatorio' ($cmd -like '*GITSTACK_REF=abc123 GITSTACK_ADMIN_REQUIRED=1 bash')

$data = [pscustomobject]@{ User = 'u1'; Token = 'tok'; Repo = 'r1'; SshKey = 'C:\x\id'; KnownHosts = 'C:\x\kh' }
$open = Get-E2eSshCommand -Data $data
$strict = Get-E2eSshCommand -Data $data -Strict
Assert-That 'ssh: accept-new alla creazione' ($open -like '*StrictHostKeyChecking=accept-new*')
Assert-That 'ssh: yes in verifica (chiave host cambiata = errore)' ($strict -like '*StrictHostKeyChecking=yes*')
Assert-That 'ssh: percorsi con la barra normale' ($strict -like '*-i C:/x/id *' -and $strict -like '*UserKnownHostsFile=C:/x/kh*')

$urls = Get-E2eRepoUrls -Data $data -Ip '10.0.0.5'
Assert-That 'url https con token' ($urls.Https -eq 'https://u1:tok@10.0.0.5/u1/r1.git')
Assert-That 'url ssh sulla porta del servizio git' ($urls.Ssh -eq 'ssh://git@10.0.0.5:2222/u1/r1.git')

# Invoke-E2eGit: imposta l'ambiente per la chiamata e lo rimette com era.
$env:GIT_SSH_COMMAND = 'valore-originale'
$env:GIT_TERMINAL_PROMPT = $null
[void](Invoke-E2eGit -GitArgs @('ls-remote', 'x') -CaPath 'C:\ca\ca.crt' -SshCommand 'ssh-finto')
Assert-That 'git: ambiente della chiamata (SSH e niente prompt)' ($script:LastExternal.Env -eq 'ssh-finto' -and $script:LastExternal.Prompt -eq '0')
Assert-That 'git: ambiente ripristinato dopo la chiamata' ($env:GIT_SSH_COMMAND -eq 'valore-originale' -and -not $env:GIT_TERMINAL_PROMPT)
$joined = $script:LastExternal.Args -join ' '
Assert-That 'git: si fida solo della CA data, senza credential helper' ($joined -like '*http.sslCAInfo=C:/ca/ca.crt*' -and $joined -like '*credential.helper=*')
$env:GIT_SSH_COMMAND = $null

# Get-E2ePreviousCommit: primo genitore dalla risposta dell'API.
function Invoke-RestMethod {
    param([string]$Uri, [int]$TimeoutSec, [hashtable]$Headers)
    $script:LastUri = $Uri
    return [pscustomobject]@{ parents = @([pscustomobject]@{ sha = 'p1' }, [pscustomobject]@{ sha = 'p2' }) }
}
$prev = Get-E2ePreviousCommit -Repo 'owner/GitStack' -Sha 'abc123'
Assert-That 'precedente: primo genitore' ($prev -eq 'p1')
Assert-That 'precedente: URL dell''API del commit' ($script:LastUri -eq 'https://api.github.com/repos/owner/GitStack/commits/abc123')

# Install con hosting acceso (GIT-388): solo se c'e' un dominio.
$cmdApps = Get-E2eInstallCommand -InstallRef 'abc123' -AppsDomain 'apps.e2e.test'
Assert-That 'install: con dominio accende l''hosting' ($cmdApps -like '*bash -s -- --enable-apps --apps-domain apps.e2e.test')

# Test-E2eAppAfterRestore: sette righe sempre, con HTTP e VM finti.
$script:OutDir = Join-Path ([IO.Path]::GetTempPath()) 'gs-phases-tests'
$script:SshCommandTimeoutSeconds = 5
$script:Mock = @{ Status = 'running'; V1 = 200; V2 = 200; V3 = 404; Inspect = "deploy=yes`nimage=10.0.0.9/u1/r1/e2eimg:v2`nvar=match`nsecret=match`nvolume=match`nscratch=empty`n" }
function Start-Sleep { param([int]$Seconds) }
function Copy-ToVm { param($LocalPath, $RemotePath, $TimeoutSeconds) return [pscustomobject]@{ ExitCode = 0; StdOut = ''; StdErr = '' } }
function Invoke-VmSsh { param($Command, $TimeoutSeconds) return [pscustomobject]@{ ExitCode = 0; StdOut = $script:Mock.Inspect; StdErr = '' } }
function Invoke-HttpRaw {
    param([string]$Uri, [string]$Method = 'GET', [string]$Body, [hashtable]$Headers, [string]$Cookie, [int]$TimeoutSec = 20)
    if ($Uri -like '*/manifests/*') { $script:LastAuth = $Headers.Authorization }
    if ($Uri -like '*/manifests/v1') { return [pscustomobject]@{ StatusCode = $script:Mock.V1; Body = '' } }
    if ($Uri -like '*/manifests/v2') { return [pscustomobject]@{ StatusCode = $script:Mock.V2; Body = '' } }
    if ($Uri -like '*/manifests/v3') { return [pscustomobject]@{ StatusCode = $script:Mock.V3; Body = '' } }
    return [pscustomobject]@{ StatusCode = 200; Body = (@{ status = $script:Mock.Status } | ConvertTo-Json) }
}
$ctx = [pscustomobject]@{ Namespace = 'gitstack-app-x'; ActiveVersion = 4; ActiveRef = '10.0.0.5/u1/r1/e2eimg:v2'; VarValue = 'a'; SecretValue = 'b'; ImagePath = 'u1/r1/e2eimg'; RegistryToken = 'regtok' }
$rows = Test-E2eAppAfterRestore -BaseUrl 'https://10.0.0.9' -Data $data -Ctx $ctx -WorkDir (Join-Path $script:OutDir 'a') -RestoreLog "ripristino dei volumi delle app (1) ripristino del volume data dell'app u1/r1/e2eapp"
Assert-That 'app dopo il restore: dieci righe' (@($rows).Count -eq 10)
Assert-That 'app dopo il restore: tutte ok (host del registry diverso ammesso)' (@($rows | Where-Object { -not $_.Ok }).Count -eq 0)
$rows = Test-E2eAppAfterRestore -BaseUrl 'https://10.0.0.9' -Data $data -Ctx $ctx -WorkDir (Join-Path $script:OutDir 'log') -RestoreLog 'ripristino dei volumi delle app (1)'
Assert-That 'volume: il log parziale non basta' (-not $rows[7].Ok -and $rows[8].Ok)
$script:Mock.Inspect = "deploy=yes`nimage=10.0.0.9/u1/r1/e2eimg:v2`nvar=match`nsecret=match`nvolume=mismatch`nscratch=empty`n"
$rows = Test-E2eAppAfterRestore -BaseUrl 'https://10.0.0.9' -Data $data -Ctx $ctx -WorkDir (Join-Path $script:OutDir 'volume') -RestoreLog "ripristino dei volumi delle app (1) ripristino del volume data dell'app u1/r1/e2eapp"
Assert-That 'volume: contenuto diverso fallisce anche con log corretto' ($rows[7].Ok -and -not $rows[8].Ok)
$script:Mock.Inspect = "deploy=yes`nimage=10.0.0.9/u1/r1/e2eimg:v2`nvar=match`nsecret=match`nvolume=match`nscratch=present`n"
$rows = Test-E2eAppAfterRestore -BaseUrl 'https://10.0.0.9' -Data $data -Ctx $ctx -WorkDir (Join-Path $script:OutDir 'scratch') -RestoreLog "ripristino dei volumi delle app (1) ripristino del volume data dell'app u1/r1/e2eapp"
Assert-That 'volume escluso: il file ancora presente dopo il restore fallisce, il resto ok' ($rows[8].Ok -and -not $rows[9].Ok -and $rows[9].Detail -like '*present*')
$script:Mock.Inspect = "deploy=yes`nimage=10.0.0.9/u1/r1/e2eimg:v2`nvar=match`nsecret=match`nvolume=match`nscratch=unmounted`n"
$rows = Test-E2eAppAfterRestore -BaseUrl 'https://10.0.0.9' -Data $data -Ctx $ctx -WorkDir (Join-Path $script:OutDir 'scratch2') -RestoreLog "ripristino dei volumi delle app (1) ripristino del volume data dell'app u1/r1/e2eapp"
Assert-That 'volume escluso: non montato non vale come vuoto' (-not $rows[9].Ok)
$script:Mock.V3 = 200; $script:Mock.V1 = 404; $script:Mock.Status = 'starting'
$script:Mock.Inspect = "deploy=yes`nimage=10.0.0.9/u1/r1/e2eimg:v1`nvar=missing`nsecret=mismatch`n"
$rows = Test-E2eAppAfterRestore -BaseUrl 'https://10.0.0.9' -Data $data -Ctx $ctx -WorkDir (Join-Path $script:OutDir 'b')
$ko = @($rows | Where-Object { -not $_.Ok } | ForEach-Object { $_.Name.Substring(0, 10) })
Assert-That 'app dopo il restore: ko su 2, 3, 4, 5, 6 e 7, ok su 1; volume e log ko' (@($rows).Count -eq 10 -and $rows[0].Ok -and $ko.Count -eq 9 -and -not $rows[7].Ok -and -not $rows[8].Ok -and -not $rows[9].Ok)
$script:Mock.Inspect = "deploy=no`nimage=`nvar=missing`nsecret=missing`n"
$rows = Test-E2eAppAfterRestore -BaseUrl 'https://10.0.0.9' -Data $data -Ctx $ctx -WorkDir (Join-Path $script:OutDir 'c')
Assert-That 'app dopo il restore: senza Deployment la riga 1 e ko' (-not $rows[0].Ok -and -not $rows[1].Ok)
$script:Mock.Inspect = "deploy=yes`nimage=10.0.0.9/u1/r1/e2eimg:v2`nvar=match`nsecret=match`n"
$script:Mock.V1 = 401; $script:Mock.V2 = 401; $script:Mock.V3 = 401
$rows = Test-E2eAppAfterRestore -BaseUrl 'https://10.0.0.9' -Data $data -Ctx $ctx -WorkDir (Join-Path $script:OutDir 'd')
Assert-That 'app dopo il restore: 401 sui manifest, 6 e 7 ko con lo status e i permessi' ((-not $rows[5].Ok) -and (-not $rows[6].Ok) -and $rows[5].Detail -like '*401*' -and $rows[6].Detail -like '*401*' -and $rows[6].Detail -like '*permessi*')
Assert-That 'app dopo il restore: il token del registry e quello del contesto' ($script:LastAuth -eq ('Basic ' + [Convert]::ToBase64String([Text.Encoding]::ASCII.GetBytes('token:regtok'))))

# Test-E2eAppLifecycle (GIT-355): sei righe sempre, API e VM finte.
$script:Life = @{ Serving = 1; Calls = @(); Cmds = @(); FailProbe = ''; Rollback = '' }
function Invoke-HttpRaw {
    param([string]$Uri, [string]$Method = 'GET', [string]$Body, [hashtable]$Headers, [string]$Cookie, [int]$TimeoutSec = 20)
    $script:Life.Calls += "$Method $Uri"
    if ($Uri -like '*/versions?per_page=1') { return [pscustomobject]@{ StatusCode = 200; Body = (@{ items = @(@{ number = $script:Life.Serving; state = 'serving' }) } | ConvertTo-Json -Depth 4) } }
    if ($Method -eq 'POST' -and $Uri -like '*/apps') { return [pscustomobject]@{ StatusCode = 202; Body = (@{ app = @{ id = 'a1' }; version = @{ number = 1 } } | ConvertTo-Json -Depth 4) } }
    if ($Method -eq 'POST' -and $Uri -like '*/deploys') { $script:Life.Serving = 2; return [pscustomobject]@{ StatusCode = 202; Body = '{}' } }
    if ($Method -eq 'POST' -and $Uri -like '*/rollback') { $script:Life.Serving = 3; $script:Life.Rollback = $Body; return [pscustomobject]@{ StatusCode = 202; Body = '{}' } }
    if ($Method -eq 'DELETE') { return [pscustomobject]@{ StatusCode = 204; Body = '' } }
    return [pscustomobject]@{ StatusCode = 200; Body = (@{ address = 'https://e2elife-r1-u1.apps.e2e.test' } | ConvertTo-Json) }
}
function Invoke-VmSsh {
    param($Command, $TimeoutSeconds)
    $script:Life.Cmds += $Command
    if ($script:Life.FailProbe -and $Command -like "*$($script:Life.FailProbe)*") { return [pscustomobject]@{ ExitCode = 1; StdOut = 'STEP FAIL x: finto'; StdErr = '' } }
    $out = 'STEP ok x'
    if ($Command -like '*push-web*') { $out = 'STEP ok push-web' }
    elseif ($Command -like '*probe-anon*') { $out = 'STEP ok login-anon' }
    elseif ($Command -like '*GS_EXPECT=e2e-web-v1*') { $out = 'STEP ok serve-e2e-web-v1' }
    elseif ($Command -like '*GS_EXPECT=e2e-web-v2*') { $out = 'STEP ok serve-e2e-web-v2' }
    return [pscustomobject]@{ ExitCode = 0; StdOut = $out; StdErr = '' }
}
$ctxLife = [pscustomobject]@{ RegistryToken = 'regtok' }
$rows = Test-E2eAppLifecycle -BaseUrl 'https://10.0.0.9' -CaPath 'C:\ca\ca.crt' -Data $data -Ctx $ctxLife -WorkDir (Join-Path $script:OutDir 'life')
Assert-That 'ciclo: sei righe, tutte ok' (@($rows).Count -eq 6 -and @($rows | Where-Object { -not $_.Ok }).Count -eq 0)
Assert-That 'ciclo: il rollback torna alla versione 1 e l''app si elimina' (($script:Life.Rollback | ConvertFrom-Json).version -eq 1 -and ($script:Life.Calls | Where-Object { $_ -like 'DELETE *e2elife' }).Count -eq 1)
Assert-That 'ciclo: l''indirizzo dell''app arriva alle sonde' (@($script:Life.Cmds | Where-Object { $_ -like "*GS_APP_HOST='e2elife-r1-u1.apps.e2e.test'*" }).Count -eq 4)
$script:Life = @{ Serving = 1; Calls = @(); Cmds = @(); FailProbe = 'GS_EXPECT=e2e-web-v2'; Rollback = '' }
$rows = Test-E2eAppLifecycle -BaseUrl 'https://10.0.0.9' -CaPath 'C:\ca\ca.crt' -Data $data -Ctx $ctxLife -WorkDir (Join-Path $script:OutDir 'life-ko')
Assert-That 'ciclo: v2 non servita, riga 4 ko, 5 e 6 non verificate, nessun rollback ne eliminazione' ($rows[2].Ok -and -not $rows[3].Ok -and -not $rows[4].Ok -and -not $rows[5].Ok -and $rows[3].Detail -like '*serve v2*' -and $rows[4].Detail -like 'non verificato*' -and $script:Life.Rollback -eq '' -and @($script:Life.Calls | Where-Object { $_ -like 'DELETE *' }).Count -eq 0)
Assert-That 'ciclo: al primo errore parte la diagnostica dei pod' (@($script:Life.Cmds | Where-Object { $_ -like "*GS_APP_NS='gitstack-app-a1'*bash*diag" }).Count -eq 1)

# Installer con Pebble (GIT-468): server ACME e email passano all'installer come variabili d'ambiente.
$cmdAcme = Get-E2eInstallCommand -InstallRef 'abc123' -AppsDomain 'apps.e2e.test' -AcmeCaServer 'https://pebble:14000/dir'
Assert-That 'install: con Pebble passa GITSTACK_ACME_CA_SERVER e l''email prima di bash' ($cmdAcme -like '*GITSTACK_ADMIN_REQUIRED=1 GITSTACK_ACME_CA_SERVER=https://pebble:14000/dir GITSTACK_TLS_EMAIL=e2e@example.test bash -s -- --enable-apps --apps-domain apps.e2e.test')
Assert-That 'install: senza Pebble il comando e quello di prima' ($cmdApps -notlike '*ACME*')

# Test-E2eAppPublic e Test-E2eAppPublicAfterRestore (GIT-468): API e VM finte.
$script:AppsDomain = 'apps.e2e.test'
$script:VmIp = '10.0.0.9'
$script:Pub = @{ On = $false; Calls = @(); Cmds = @(); DomainStatus = 'verified'; CertState = 'valid'; Issuer = 'Pebble Intermediate CA 1a2b'; Fp = 'AA:BB:CC'; Running = 'running' }
function Copy-FromVm {
    param($RemotePath, $LocalPath, $TimeoutSeconds)
    Set-Content -LiteralPath (Join-Path $LocalPath 'pebble-root.pem') -Value '-----BEGIN CERTIFICATE-----' -Encoding ascii
    return [pscustomobject]@{ ExitCode = 0; StdOut = ''; StdErr = '' }
}
function Invoke-HttpRaw {
    param([string]$Uri, [string]$Method = 'GET', [string]$Body, [hashtable]$Headers, [string]$Cookie, [int]$TimeoutSec = 20)
    $script:Pub.Calls += "$Method $Uri"
    $json = { param($o) [pscustomobject]@{ StatusCode = 200; Body = ($o | ConvertTo-Json -Depth 6) } }
    $settings = { @{ publicAppsEnabled = $script:Pub.On; signedImagesOnly = $false; restricted = $false; maxPerApp = @{ cpuMillis = 2000; memoryBytes = 1073741824 }; logRetention = @{ days = 7; maxBytes = 1073741824 }; letsEncrypt = @{ enabled = $script:Pub.On; acmeEmail = 'e2e@example.test' } } }
    if ($Uri -like '*/api/healthz') { return [pscustomobject]@{ StatusCode = 200; Body = '{"status":"ok"}' } }
    if ($Uri -like '*/auth/login') { return [pscustomobject]@{ StatusCode = 200; Cookie = 'gst_session=x'; Body = '{"mustChangePassword":false}' } }
    if ($Uri -like '*/admin/apps/settings') {
        if ($Method -eq 'PUT') { $script:Pub.On = [bool](($Body | ConvertFrom-Json).publicAppsEnabled) }
        return (& $json (& $settings))
    }
    if ($Uri -like '*/versions?per_page=1') { return (& $json @{ items = @(@{ number = 1; state = 'serving' }) }) }
    if ($Method -eq 'POST' -and $Uri -like '*/apps') { return [pscustomobject]@{ StatusCode = 202; Body = '{"app":{"id":"p1"},"version":{"number":1}}' } }
    if ($Method -eq 'PUT' -and $Uri -like '*/access') {
        if (-not $script:Pub.On) { return [pscustomobject]@{ StatusCode = 422; Body = '{"code":"public_apps_disabled"}' } }
        return (& $json @{ mode = 'public'; publicAppsEnabled = $true })
    }
    if ($Method -eq 'GET' -and $Uri -like '*/access') { return (& $json @{ mode = 'public'; publicAppsEnabled = $script:Pub.On }) }
    if ($Method -eq 'POST' -and $Uri -like '*/domains') { return [pscustomobject]@{ StatusCode = 201; Body = (@{ domain = 'shop-pub.e2e.test'; status = 'pending_dns'; dns = @{ type = 'CNAME'; value = 'e2epub-r1-u1.apps.e2e.test' } } | ConvertTo-Json -Depth 4) } }
    if ($Method -eq 'POST' -and $Uri -like '*/recheck') { return (& $json @{ status = 'verified' }) }
    if ($Uri -like '*/domains/shop-pub.e2e.test') { return (& $json @{ domain = 'shop-pub.e2e.test'; status = $script:Pub.DomainStatus; certificate = @{ mode = 'letsencrypt'; state = $script:Pub.CertState; issuer = $script:Pub.Issuer; validUntil = '2031-01-01T00:00:00Z' } }) }
    if ($Uri -like '*/domains') { return (& $json @{ items = @(@{ domain = 'shop-pub.e2e.test'; status = 'verified' }) }) }
    if ($Uri -like '*/apps/e2epub') { return (& $json @{ address = 'https://e2epub-r1-u1.apps.e2e.test'; status = $script:Pub.Running }) }
    return [pscustomobject]@{ StatusCode = 404; Body = '' }
}
function Invoke-VmSsh {
    param($Command, $TimeoutSeconds)
    $script:Pub.Cmds += $Command
    if ($Command -like '*cert-info*') { return [pscustomobject]@{ ExitCode = 0; StdOut = "fp=$($script:Pub.Fp)`nissuer=CN=Pebble Intermediate CA 1a2b`nsan=DNS:shop-pub.e2e.test`n"; StdErr = '' } }
    $out = 'STEP ok x'
    foreach ($s in 'pebble-up', 'traefik-acme', 'dns-set', 'pebble-root', 'domain-open', 'domain-closed', 'address-open') { if ($Command -like "*gs-app-public.sh $s") { $out = "STEP ok $s" } }
    if ($Command -like '*push-web*') { $out = 'STEP ok push-web' }
    elseif ($Command -like '*probe-anon*') { $out = 'STEP ok login-anon' }
    elseif ($Command -like '*probe-auth*') { $out = 'STEP ok serve-e2e-web-v1' }
    return [pscustomobject]@{ ExitCode = 0; StdOut = $out; StdErr = '' }
}
$ctxPub = [pscustomobject]@{ RegistryToken = 'regtok' }
$dataPub = [pscustomobject]@{ User = 'u1'; Repo = 'r1'; Token = 'tok' }
$script:E2eAcmeOn = $false
$rows = Test-E2eAppPublic -BaseUrl 'https://10.0.0.9' -Data $dataPub -Ctx $ctxPub -AdminPassword 'pw' -InstallRef 'abc123' -WorkDir (Join-Path $script:OutDir 'pub')
Assert-That 'pubblica: tredici righe, tutte ok' (@($rows).Count -eq 13 -and @($rows | Where-Object { -not $_.Ok }).Count -eq 0)
Assert-That 'pubblica: l''installer rilanciato ha Pebble e il restore lo rifara' ($script:E2eAcmeOn -and $script:E2ePubCtx.Fingerprint -eq 'AA:BB:CC' -and $script:E2ePubCtx.Domain -eq 'shop-pub.e2e.test')
Assert-That 'pubblica: public rifiutato a interruttore spento e poi accettato' (@($script:Pub.Calls | Where-Object { $_ -like 'PUT *e2epub/access' }).Count -eq 2)
Assert-That 'pubblica: l''interruttore va spento, acceso, spento, acceso' ((@($script:Pub.Calls | Where-Object { $_ -like 'PUT *admin/apps/settings' }).Count) -eq 4 -and $script:Pub.On)
Assert-That 'pubblica: la zona DNS di prova riceve etichetta, dominio delle app e dominio proprio' (@($script:Pub.Cmds | Where-Object { $_ -like "*GS_APPS_DOMAIN='apps.e2e.test' GS_APP_LABEL='e2epub-r1-u1' GS_PUB_DOMAIN='shop-pub.e2e.test'*dns-set" }).Count -eq 1)

$script:Pub = @{ On = $false; Calls = @(); Cmds = @(); DomainStatus = 'verified'; CertState = 'error'; Issuer = 'Pebble'; Fp = 'AA:BB:CC'; Running = 'running' }
$rows = Test-E2eAppPublic -BaseUrl 'https://10.0.0.9' -Data $dataPub -Ctx $ctxPub -AdminPassword 'pw' -InstallRef 'abc123' -WorkDir (Join-Path $script:OutDir 'pub-ko')
Assert-That 'pubblica: certificato in error, riga 9 ko, 1-8 ok, 10-13 non verificate e nessuno spegnimento' ($rows[7].Ok -and -not $rows[8].Ok -and $rows[8].Detail -like '*certificato in error*' -and -not $rows[9].Ok -and $rows[12].Detail -like 'non verificato*' -and @($script:Pub.Calls | Where-Object { $_ -like 'PUT *admin/apps/settings' }).Count -eq 2)
Assert-That 'pubblica: al primo errore parte la diagnostica di Pebble, Traefik e CoreDNS' (@($script:Pub.Cmds | Where-Object { $_ -like '*gs-app-public.sh diag' }).Count -eq 1)

$script:Pub = @{ On = $true; Calls = @(); Cmds = @(); DomainStatus = 'verified'; CertState = 'valid'; Issuer = 'Pebble Intermediate CA 1a2b'; Fp = 'AA:BB:CC'; Running = 'running' }
$ctxAfter = [pscustomobject]@{ AppName = 'e2epub'; Domain = 'shop-pub.e2e.test'; Label = 'e2epub-r1-u1'; Fingerprint = 'AA:BB:CC'; RootLocal = (Join-Path $script:OutDir 'pub\pebble-root.pem'); Expect = 'e2e-web-v1' }
$rows = Test-E2eAppPublicAfterRestore -BaseUrl 'https://10.0.0.9' -Data $dataPub -Ctx $ctxAfter -WorkDir (Join-Path $script:OutDir 'pub-dopo')
Assert-That 'dopo il restore: quattro righe, tutte ok' (@($rows).Count -eq 4 -and @($rows | Where-Object { -not $_.Ok }).Count -eq 0)
$script:Pub.Fp = 'DD:EE:FF'
$rows = Test-E2eAppPublicAfterRestore -BaseUrl 'https://10.0.0.9' -Data $dataPub -Ctx $ctxAfter -WorkDir (Join-Path $script:OutDir 'pub-dopo-ko')
Assert-That 'dopo il restore: certificato servito diverso, riga 3 ko e la 4 non verificata' ($rows[1].Ok -and -not $rows[2].Ok -and $rows[2].Detail -like '*diversa*' -and -not $rows[3].Ok)

Remove-Item -LiteralPath $script:OutDir -Recurse -Force -ErrorAction SilentlyContinue

# GIT-498: il marcatore che phases.ps1 si aspetta dal push deve essere quello che
# gs-app-restore.sh stampa davvero (il mock di Invoke-VmSsh se lo inventa).
$shSrc = Get-Content -LiteralPath (Join-Path $PSScriptRoot '..\e2e\gs-app-restore.sh') -Raw
$psLines = @(Get-Content -LiteralPath (Join-Path $PSScriptRoot '..\lib\phases.ps1'))
# passi: funzioni che chiamano publish <passo> (es. push() { publish push sleep v1 v2 v3; })
$pubSteps = @{}
foreach ($m in [regex]::Matches($shSrc, '(?m)^(?<fn>[a-z_]+)\(\)\s*\{\s*publish\s+(?<step>[a-z-]+)\s')) { $pubSteps[$m.Groups['fn'].Value.Replace('_', '-')] = $m.Groups['step'].Value }
# riga echo "STEP ok ..." dentro publish()
$pubBody = [regex]::Match($shSrc, '(?s)\npublish\(\)\s*\{.*?\n\}\n').Value
$okLine = [regex]::Match($pubBody, '(?m)^\s*echo\s+"(?<m>STEP ok [^"]*)"\s*$').Groups['m'].Value
Assert-That 'gs-app-restore.sh: push e push-web chiamano publish, con una riga STEP ok in publish' ($pubSteps.ContainsKey('push') -and $pubSteps.ContainsKey('push-web') -and $okLine -ne '')
$pending = $null; $checked = 0; $mismatch = New-Object System.Collections.Generic.List[string]
foreach ($ln in $psLines) {
    $c = [regex]::Match($ln, '(?:gs-app-restore\.sh|\$remoteSh) (?<cmd>push(?:-web)?)"')
    if ($c.Success) { $pending = $c.Groups['cmd'].Value; continue }
    $e = [regex]::Match($ln, "notmatch '(?<exp>STEP ok [^']+)'")
    if ($pending -and $e.Success) {
        $printed = $okLine.Replace('${step}', $pubSteps[$pending]).Replace('$step', $pubSteps[$pending])
        $checked++
        if ($printed -notmatch [regex]::Escape($e.Groups['exp'].Value)) { $mismatch.Add("$pending stampa '$printed', phases.ps1 cerca '$($e.Groups['exp'].Value)'") }
        $pending = $null
    }
}
if ($mismatch.Count -gt 0) { Write-Host ($mismatch -join '; ') }
Assert-That 'marcatore del push: quello atteso da phases.ps1 e quello stampato da gs-app-restore.sh combaciano (almeno 3 controlli)' ($checked -ge 3 -and $mismatch.Count -eq 0)

# GIT-505: diagnosi TLS dopo il restore. Mock di Invoke-VmSsh che risponde in base al comando.
. (Join-Path $PSScriptRoot '..\lib\tls.ps1')
$rsa = [System.Security.Cryptography.RSA]::Create(2048)
$req = New-Object System.Security.Cryptography.X509Certificates.CertificateRequest -ArgumentList 'CN=CA di prova GIT-505', $rsa, ([System.Security.Cryptography.HashAlgorithmName]::SHA256), ([System.Security.Cryptography.RSASignaturePadding]::Pkcs1)
$testCert = $req.CreateSelfSigned([DateTimeOffset]::UtcNow.AddDays(-1), [DateTimeOffset]::UtcNow.AddDays(30))
[GitStackCaTrust]::Ca = $testCert
$script:DiagCmds = New-Object System.Collections.Generic.List[string]
function Invoke-VmSsh {
    param($Command, $TimeoutSeconds)
    $script:DiagCmds.Add($Command)
    if ($Command -like '*s_client*' -and $Command -notlike '*-servername*') { return [pscustomobject]@{ ExitCode = 0; StdOut = "issuer=CN = TRAEFIK DEFAULT CERT`nsubject=CN = TRAEFIK DEFAULT CERT`nsha256 Fingerprint=AA:11"; StdErr = '' } }
    if ($Command -like '*s_client*') { return [pscustomobject]@{ ExitCode = 0; StdOut = "issuer=O = GitStack, CN = GitStack CA`nsubject=CN = 10.0.0.9`nX509v3 Subject Alternative Name:`n    IP Address:10.0.0.9`nsha256 Fingerprint=BB:22"; StdErr = '' } }
    if ($Command -like '*kubectl*') { return [pscustomobject]@{ ExitCode = 0; StdOut = "issuer=O = GitStack, CN = GitStack CA`nsubject=CN = 10.0.0.9`nsha256 Fingerprint=CC:33"; StdErr = '' } }
    if ($Command -like '*fingerprint*') { return [pscustomobject]@{ ExitCode = 0; StdOut = 'DD:44'; StdErr = '' } }
    return [pscustomobject]@{ ExitCode = 1; StdOut = ''; StdErr = 'no' }
}
$diagFile = Join-Path ([IO.Path]::GetTempPath()) ("diag-" + [guid]::NewGuid().ToString('N') + '.txt')
$diag = Get-E2eTlsDiagnosis -Ip '10.0.0.9' -Path $diagFile
Write-Host $diag
Assert-That 'diagnosi TLS: certificato servito senza SNI (issuer, subject, impronta)' ($diag -like '*TRAEFIK DEFAULT CERT*' -and $diag -like '*Fingerprint=AA:11*')
Assert-That 'diagnosi TLS: certificato servito con SNI, con i SAN' ($diag -like '*IP Address:10.0.0.9*' -and $diag -like '*Fingerprint=BB:22*')
Assert-That 'diagnosi TLS: certificato del Secret gitstack-tls e CA su disco' ($diag -like '*Secret gitstack/gitstack-tls*' -and $diag -like '*Fingerprint=CC:33*' -and $diag -like '*DD:44*')
Assert-That 'diagnosi TLS: CA con cui l''host verifica (subject e impronta)' ($diag -like '*CA di prova GIT-505*' -and $diag -match 'impronta SHA-256 ([0-9A-F]{2}:){31}[0-9A-F]{2}')
Assert-That 'diagnosi TLS: scritta anche su file' ((Test-Path -LiteralPath $diagFile) -and ((Get-Content -LiteralPath $diagFile -Raw) -like '*TRAEFIK DEFAULT CERT*'))
Assert-That 'diagnosi TLS: cinque comandi sulla VM' ($script:DiagCmds.Count -eq 5)
Remove-Item -LiteralPath $diagFile -ErrorAction SilentlyContinue
function Invoke-VmSsh { param($Command, $TimeoutSeconds) throw 'ssh giu' }
$diag2 = Get-E2eTlsDiagnosis -Ip '10.0.0.9'
Assert-That 'diagnosi TLS: con ssh in errore non lancia e lo dice' ($diag2 -like '*ssh non riuscito*ssh giu*')
Clear-GitStackCaTrust

# l'ordine nel passo i: la CA ripristinata si scarica PRIMA dell'attesa su HTTPS, e la diagnosi parte se l'attesa fallisce
$phSrc = Get-Content -LiteralPath (Join-Path $PSScriptRoot '..\lib\phases.ps1') -Raw
$iBlock = [regex]::Match($phSrc, '(?s)gitstack restore --key-file.*?\$fpAfter -ne \$fpBefore').Value
Assert-That 'passo i: dopo il restore tutto (CA + attesa) passa da Wait-E2eRestoredCaAndHealthy' ($iBlock -like '*$fpAfter = Wait-E2eRestoredCaAndHealthy*' -and $iBlock -notlike '*Set-E2eCaFromVm*' -and $iBlock -notlike '*Wait-E2eHealthy -BaseUrl*')
$fnBody = [regex]::Match($phSrc, '(?s)function Wait-E2eRestoredCaAndHealthy \{.*?\r?\n\}\r?\n').Value
$posTry = $fnBody.IndexOf('try {'); $posCa = $fnBody.IndexOf('Set-E2eCaFromVm'); $posWait2 = $fnBody.IndexOf('Wait-E2eHealthy'); $posCatch = $fnBody.IndexOf('} catch {', $posWait2); $posDiag = $fnBody.IndexOf('Get-E2eTlsDiagnosis', $posCatch)
Assert-That 'funzione: Set-E2eCaFromVm e Wait-E2eHealthy stanno dentro il try, la diagnosi nel catch' ($posTry -gt 0 -and $posCa -gt $posTry -and $posWait2 -gt $posCa -and $posCatch -gt $posWait2 -and $posDiag -gt $posCatch)

# GIT-505 r1: ripresa della CA e diagnosi su qualunque errore del blocco «dopo il restore»
$pem = "-----BEGIN CERTIFICATE-----`n" + [Convert]::ToBase64String($testCert.RawData, 'InsertLineBreaks') + "`n-----END CERTIFICATE-----`n"
$tmpPem = Join-Path ([IO.Path]::GetTempPath()) ("ca-" + [guid]::NewGuid().ToString('N') + '.crt')
Set-Content -LiteralPath $tmpPem -Value $pem -Encoding ascii
$pemFp = Get-CertSha256Fingerprint -Path $tmpPem
$script:VmIp = '10.0.0.9'
$script:LogLines = New-Object System.Collections.Generic.List[string]
function Write-Log { param([string]$Message) $script:LogLines.Add($Message) }
$caOut = Join-Path ([IO.Path]::GetTempPath()) ("ca-out-" + [guid]::NewGuid().ToString('N') + '.crt')
$diagOut = Join-Path ([IO.Path]::GetTempPath()) ("diag-out-" + [guid]::NewGuid().ToString('N') + '.txt')

# (a) ca.crt risponde 502 per due chiamate e poi 200: il passo prosegue e ritorna l'impronta
$script:CaCalls = 0
function Invoke-HttpRaw {
    param([string]$Uri, [string]$Method = 'GET', [string]$Body, $Headers, [string]$Cookie, [int]$TimeoutSec = 20)
    if ($Uri -like '*/downloads/ca.crt') {
        $script:CaCalls++
        if ($script:CaCalls -le 2) { return [pscustomobject]@{ StatusCode = 502; Body = ''; Error = 'bad gateway' } }
        return [pscustomobject]@{ StatusCode = 200; Body = $pem; Error = $null }
    }
    return [pscustomobject]@{ StatusCode = 200; Body = '{"status":"ok"}'; Error = $null }
}
function Invoke-VmSsh {
    param($Command, $TimeoutSeconds)
    if ($Command -eq 'sudo gitstack-tls fingerprint') { return [pscustomobject]@{ ExitCode = 0; StdOut = $script:VmCaFp; StdErr = '' } }
    return [pscustomobject]@{ ExitCode = 0; StdOut = 'issuer=O = GitStack'; StdErr = '' }
}
$script:VmCaFp = $pemFp
$fpRet = Wait-E2eRestoredCaAndHealthy -BaseUrl 'https://10.0.0.9' -Ip '10.0.0.9' -CaPath $caOut -DiagPath $diagOut -CaTimeoutSeconds 30 -CaRetrySeconds 0
Assert-That 'dopo il restore (a): ca.crt 502 per due chiamate poi 200, il passo prosegue con l''impronta' ($fpRet -eq $pemFp -and $script:CaCalls -eq 3 -and -not (Test-Path -LiteralPath $diagOut))

# (b) impronta del ca.crt servito diversa da `gitstack-tls fingerprint` per tutta la scadenza: diagnosi nel log, errore con l'impronta
$script:CaCalls = 100
$script:VmCaFp = 'FF:EE:DD'
$script:LogLines.Clear()
$err = $null
try { [void](Wait-E2eRestoredCaAndHealthy -BaseUrl 'https://10.0.0.9' -Ip '10.0.0.9' -CaPath $caOut -DiagPath $diagOut -CaTimeoutSeconds 1 -CaRetrySeconds 0) } catch { $err = $_.Exception.Message }
$logAll = $script:LogLines -join "`n"
Assert-That 'dopo il restore (b): l''errore cita le due impronte e la scadenza' ($err -like 'dopo il restore: CA ripristinata non presa*' -and $err -like '*non e quella della VM*' -and $err -like '*FF:EE:DD*' -and $err -like "*$pemFp*")
Assert-That 'dopo il restore (b): nel log c''e la diagnosi TLS e l''avviso che l''host verifica con la CA vecchia' ($logAll -like '*Diagnosi TLS dopo il restore*' -and $logAll -like '*CA con cui l''host verifica*' -and $logAll -like '*ancora con la CA della nuova installazione*')
Assert-That 'dopo il restore (b): file di diagnosi scritto' ((Test-Path -LiteralPath $diagOut) -and ((Get-Content -LiteralPath $diagOut -Raw) -like '*issuer=O = GitStack*'))

# (c) CA presa, ma /api/healthz mai ok: diagnosi e errore con l'ultimo status
function Invoke-HttpRaw {
    param([string]$Uri, [string]$Method = 'GET', [string]$Body, $Headers, [string]$Cookie, [int]$TimeoutSec = 20)
    if ($Uri -like '*/downloads/ca.crt') { return [pscustomobject]@{ StatusCode = 200; Body = $pem; Error = $null } }
    return [pscustomobject]@{ StatusCode = 0; Body = ''; Error = 'Could not establish trust relationship' }
}
$script:VmCaFp = $pemFp
$script:LogLines.Clear()
$err = $null
try { [void](Wait-E2eRestoredCaAndHealthy -BaseUrl 'https://10.0.0.9' -Ip '10.0.0.9' -CaPath $caOut -DiagPath $diagOut -CaTimeoutSeconds 5 -CaRetrySeconds 0 -HealthTimeoutSeconds 1) } catch { $err = $_.Exception.Message }
Assert-That 'dopo il restore (c): attesa fallita, errore con ultimo status e diagnosi nel log' ($err -like '*Could not establish trust*' -and (($script:LogLines -join "`n") -like '*CA ripristinata impostata sull''host*'))
Remove-Item -LiteralPath $tmpPem, $caOut, $diagOut -ErrorAction SilentlyContinue
Clear-GitStackCaTrust

# GIT-506: Wait-E2eAppDomain legge i campi facoltativi di certificate (issuer, error, ...) anche sotto StrictMode.
function Invoke-HttpRaw {
    param([string]$Uri, [string]$Method = 'GET', [string]$Body, $Headers, [string]$Cookie, [int]$TimeoutSec = 20)
    $script:DomCalls++
    $b = $script:DomBodies[[Math]::Min($script:DomCalls, $script:DomBodies.Count) - 1]
    return [pscustomobject]@{ StatusCode = 200; Body = $b; Error = $null }
}
function Start-Sleep { param([int]$Seconds) [System.Threading.Thread]::Sleep(300) }
$issuing = '{"status":"verified","certificate":{"mode":"letsencrypt","state":"issuing"}}'
$valid = '{"status":"verified","certificate":{"mode":"letsencrypt","state":"valid","issuer":"Pebble Intermediate CA 1","validUntil":"2027-01-01T00:00:00Z"}}'
Set-StrictMode -Version Latest
$script:DomCalls = 0; $script:DomBodies = @($issuing, $valid)
$err = $null; $dom = $null
try { $dom = Wait-E2eAppDomain -DomainUrl 'https://x/d' -Headers @{} -CertState 'valid' -TimeoutSeconds 30 } catch { $err = $_.Exception.Message }
Assert-That 'Wait-E2eAppDomain: senza issuer poi con issuer ritorna il dominio, senza eccezioni' ($null -eq $err -and $script:DomCalls -eq 2 -and $dom.certificate.issuer -like 'Pebble*')
$script:DomCalls = 0; $script:DomBodies = @($issuing)
$err = $null
try { [void](Wait-E2eAppDomain -DomainUrl 'https://x/d' -Headers @{} -CertState 'valid' -TimeoutSeconds 1) } catch { $err = $_.Exception.Message }
Assert-That 'Wait-E2eAppDomain: scadenza senza issuer, errore leggibile con ultimo stato' ($err -like '*ultimo: stato verified, certificato issuing ()*')
$script:DomCalls = 0; $script:DomBodies = @('{"status":"verified","certificate":{"mode":"letsencrypt","state":"error"}}')
$err = $null
try { [void](Wait-E2eAppDomain -DomainUrl 'https://x/d' -Headers @{} -CertState 'valid' -TimeoutSeconds 5) } catch { $err = $_.Exception.Message }
Assert-That 'Wait-E2eAppDomain: certificato in error senza campo error, errore leggibile' ($err -eq 'certificato in error: ')
Set-StrictMode -Off

if ($failures.Count -gt 0) {
    Write-Host "FALLITE: $($failures.Count)"
    exit 1
}
Write-Host 'Tutte le verifiche sono passate.'
exit 0
