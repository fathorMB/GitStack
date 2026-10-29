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
$script:Mock = @{ Status = 'running'; V1 = 200; V2 = 200; V3 = 404; Inspect = "deploy=yes`nimage=10.0.0.9/u1/r1/e2eimg:v2`nvar=match`nsecret=match`nvolume=match`n" }
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
Assert-That 'app dopo il restore: nove righe' (@($rows).Count -eq 9)
Assert-That 'app dopo il restore: tutte ok (host del registry diverso ammesso)' (@($rows | Where-Object { -not $_.Ok }).Count -eq 0)
$rows = Test-E2eAppAfterRestore -BaseUrl 'https://10.0.0.9' -Data $data -Ctx $ctx -WorkDir (Join-Path $script:OutDir 'log') -RestoreLog 'ripristino dei volumi delle app (1)'
Assert-That 'volume: il log parziale non basta' (-not $rows[7].Ok -and $rows[8].Ok)
$script:Mock.Inspect = "deploy=yes`nimage=10.0.0.9/u1/r1/e2eimg:v2`nvar=match`nsecret=match`nvolume=mismatch`n"
$rows = Test-E2eAppAfterRestore -BaseUrl 'https://10.0.0.9' -Data $data -Ctx $ctx -WorkDir (Join-Path $script:OutDir 'volume') -RestoreLog "ripristino dei volumi delle app (1) ripristino del volume data dell'app u1/r1/e2eapp"
Assert-That 'volume: contenuto diverso fallisce anche con log corretto' ($rows[7].Ok -and -not $rows[8].Ok)
$script:Mock.V3 = 200; $script:Mock.V1 = 404; $script:Mock.Status = 'starting'
$script:Mock.Inspect = "deploy=yes`nimage=10.0.0.9/u1/r1/e2eimg:v1`nvar=missing`nsecret=mismatch`n"
$rows = Test-E2eAppAfterRestore -BaseUrl 'https://10.0.0.9' -Data $data -Ctx $ctx -WorkDir (Join-Path $script:OutDir 'b')
$ko = @($rows | Where-Object { -not $_.Ok } | ForEach-Object { $_.Name.Substring(0, 10) })
Assert-That 'app dopo il restore: ko su 2, 3, 4, 5, 6 e 7, ok su 1; volume e log ko' (@($rows).Count -eq 9 -and $rows[0].Ok -and $ko.Count -eq 8 -and -not $rows[7].Ok -and -not $rows[8].Ok)
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

if ($failures.Count -gt 0) {
    Write-Host "FALLITE: $($failures.Count)"
    exit 1
}
Write-Host 'Tutte le verifiche sono passate.'
exit 0
