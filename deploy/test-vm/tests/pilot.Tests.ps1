$ErrorActionPreference = 'Stop'
$script:rows = @()
$script:sha = 'a' * 40
$script:digest = 'b' * 64
$script:unsigned = $false
$script:pusherKind = 'job'
$script:imagePrefix = 'alice/pilot-123/'
$script:probeFail = $false
$script:siteMode = $false
$script:showIssues = $false
$script:issueMode = $false
$script:badAttachment = $false
$script:publicApps = $false
$script:cleanup = @()
$script:dbCreate = $null
$script:pgdataValue = $null
$script:diagMode = ''
$script:diagCommands = @()
$script:delMode = $false
$script:delCalls = @()
$script:delStatus = 204
$script:delGone = $true
function Write-Log { param($Message) }
function Add-StepResult { param($Name, $Ok, $Detail) $script:rows += [pscustomobject]@{ Name = $Name; Ok = $Ok; Detail = $Detail } }
function Start-Sleep { param($Seconds) }
function Copy-ToVm { param($LocalPath, $RemotePath, $TimeoutSeconds) [pscustomobject]@{ ExitCode = 0 } }
function Invoke-RemoteHelper {
    param($RemoteArgs, $TimeoutSeconds)
    $script:cleanup += $RemoteArgs[0]
    [pscustomobject]@{ ExitCode = 0; StdOut = ''; StdErr = '' }
}
function Invoke-VmSsh {
    param($Command, $TimeoutSeconds)
    if ($script:diagMode -and $Command -match 'kubectl') {
        $script:diagCommands += $Command
        if ($script:diagMode -eq 'error') { throw 'ssh diagnostic error' }
        return [pscustomobject]@{ ExitCode = 0; StdOut = 'container startup error'; StdErr = '' }
    }
    $output = if ($Command -match 'pilot-origin.sh prepare') { "$script:sha`nSTEP ok origin-prepare" } elseif ($Command -match 'pilot-origin.sh tag') { "$script:sha`nSTEP ok origin-tag" } elseif ($Command -match 'pilot-db-probe.sh .* write') { 'STEP ok database-write' } elseif ($Command -match 'pilot-db-probe.sh .* (data|pg)') { 'STEP ok database-data' } else { '' }
    $exit = if ($script:probeFail -and $Command -match 'pilot-db-probe.sh') { 1 } else { 0 }
    [pscustomobject]@{ ExitCode = $exit; StdOut = $output; StdErr = '' }
}
function Invoke-RepoImportWithRetry { param($Uri, $Headers, $MaxAttempts, $DelaySeconds, $Body) [pscustomobject]@{ StatusCode = 201; Body = '{}' } }
function Invoke-HttpRaw {
    param($Uri, $Method, $Headers, $Body, $Cookie)
    if ($script:delMode) {
        if ($Method -eq 'DELETE') { $script:delCalls += $Uri; return [pscustomobject]@{ StatusCode = $script:delStatus; Body = ''; Error = '' } }
        return [pscustomobject]@{ StatusCode = $(if ($script:delGone) { 404 } else { 200 }); Body = '{}'; Error = '' }
    }
    if ($script:diagMode) {
        if ($Uri -like '*/versions?per_page=1') { return [pscustomobject]@{ StatusCode = 200; Body = '{"items":[{"state":"failed","stateReason":"exit 1"}]}' } }
        if ($Uri -like '*/versions?per_page=3') { return [pscustomobject]@{ StatusCode = 200; Body = '{"items":[]}' } }
        if ($Uri -like '*/apps/api' -or $Uri -like '*/apps/postgres') { return [pscustomobject]@{ StatusCode = 200; Body = '{"id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}' } }
    }
    if ($Uri -like '*/apps' -and $Method -eq 'POST' -and $Body -match '"postgres"') { $script:dbCreate = $Body | ConvertFrom-Json }
    if ($Uri -like '*/postgres/variables/PGDATA') { $script:pgdataValue = ($Body | ConvertFrom-Json).value }
    if ($script:issueMode -and $Uri -like '*/issues/42') {
        return [pscustomobject]@{ StatusCode = 200; Body = '{"author":{"username":"e2epilotbot123","kind":"agent"},"body":"Tester: e2etester123","attachments":[{"id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","filename":"screenshot.png","contentType":"image/png"}]}' }
    }
    if ($script:siteMode) {
        $status = 200
        $response = if ($Uri -like '*/auth/login') { '{"ok":true}' }
        elseif ($Uri -like '*/user/tokens') { $status = 201; '{"token":"tester-token"}' }
        elseif ($Uri -like '*/users/*/tokens') { $status = 201; '{"token":"bot-token"}' }
        elseif ($Uri -like '*/users') { $status = 201; '{"id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}' }
        elseif ($Uri -like '*/resources/*/grants') { $status = 201; '{}' }
        elseif ($Uri -like '*/apps' -and $Method -eq 'POST') { $status = 202; '{}' }
        elseif ($Uri -like '*/versions?per_page=1') { '{"items":[{"state":"serving"}]}' }
        elseif ($Uri -like '*/admin/apps/settings') { if ($script:publicApps) { '{"publicAppsEnabled":true}' } else { '{"publicAppsEnabled":false}' } }
        elseif ($Uri -like '*/sito/access') { '{"mode":"login"}' }
        elseif ($Uri -like '*/access/members' -and $Method -eq 'POST') { $status = 201; '{}' }
        elseif ($Uri -like '*/access/members') { '{"items":[{"kind":"user","name":"e2etester123"}]}' }
        elseif ($Uri -like '*/access/invite') { '{"caMode":"internal","fingerprintSha256":"' + ('ab' * 32) + '","address":"https://sito.example.test","guideUrl":"https://example.test/help/ca"}' }
        elseif ($Uri -like '*/instance/ca') { '{"mode":"internal","fingerprintSha256":"' + ('ab' * 32) + '"}' }
        elseif ($Uri -like '*/issues') { $status = if ($script:showIssues) { 200 } else { 404 }; '{}' }
        elseif ($Headers.Authorization -eq 'Bearer tester-token') { $status = 404; '{}' }
        elseif ($Uri -like '*/repos/alice/pilot') { '{"id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}' }
        else { '{}' }
        return [pscustomobject]@{ StatusCode = $status; Body = $response; Cookie = 'session' }
    }
    $status = 200
    $response = if ($Uri -like '*/apps' -and $Method -eq 'POST') { $status = 202; '{"app":{"id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}}' }
    elseif ($Uri -like '*/versions?per_page=1') { '{"items":[{"state":"serving"}]}' }
    elseif ($Uri -like '*/postgres/volumes' -and $Method -eq 'POST') { $status = 202; '{}' }
    elseif ($Uri -like '*/postgres/volumes') { '{"items":[{"name":"data","includeInBackup":true}]}' }
    elseif ($Uri -like '*/secrets/*') { '{}' }
    elseif ($Uri -like '*/pull-mirror/sync') { $status = 202; '{}' }
    elseif ($Uri -like '*/actions/pull-mirror') { '{"runOnPullMirror":true}' }
    elseif ($Uri -like '*/pull-mirror') { '{"state":"in_sync"}' }
    elseif ($Uri -like '*/actions/permissions') { '{}' }
    elseif ($Uri -like '*/actions/runs') { '{"items":[{"id":"run1","event":"push","ref":"refs/tags/v1.0.0","sha":"' + $script:sha + '","status":"completed","conclusion":"success","jobs":[{"runnerType":"internal_pod","status":"completed","conclusion":"success"}]}]}' }
    elseif ($Uri -like '*/packages') {
        $signature = if ($script:unsigned) { 'unverified' } else { 'verified' }
        $p = $script:imagePrefix
        '{"items":[{"name":"' + $p + 'api","tagDetails":[{"name":"v1.0.0","signature":"verified","pushedBy":{"kind":"job"},"digest":"sha256:' + $script:digest + '"}]},{"name":"' + $p + 'sito","tagDetails":[{"name":"v1.0.0","signature":"verified","pushedBy":{"kind":"job"},"digest":"sha256:' + $script:digest + '"}]},{"name":"' + $p + 'db","tagDetails":[{"name":"v1.0.0","signature":"' + $signature + '","pushedBy":{"kind":"' + $script:pusherKind + '"},"digest":"sha256:' + $script:digest + '"}]}]}'
    } else { '{"visibility":"private"}' }
    [pscustomobject]@{ StatusCode = $status; Body = $response }
}
. (Join-Path $PSScriptRoot '..\lib\pilot.ps1')
$script:OutDir = Join-Path ([System.IO.Path]::GetTempPath()) ('pilot-diag-' + [guid]::NewGuid().ToString('N'))
$script:diagMode = 'ok'
try { Wait-E2ePilotServing -AppsApi 'https://example.invalid/apps' -Name 'api' -Headers @{}; throw 'failure not raised' } catch {
    if ($_.Exception.Message -ne 'api stato failed: exit 1' -or (Get-E2ePilotFailureDetail $_.Exception) -notmatch 'container startup error') { throw 'original failure or previous log missing' }
}
$diag = Join-Path $script:OutDir 'pilot\api-diag'
foreach ($file in @('app.json', 'versions.json', 'pods.txt', 'describe.txt', 'events.txt', 'previous.txt', 'current.txt')) {
    if (-not (Test-Path -LiteralPath (Join-Path $diag $file))) { throw "diagnostic file missing: $file" }
}
if ($script:diagCommands.Count -ne 5 -or @($script:diagCommands | Where-Object { $_ -notmatch 'kubectl -n gitstack-app-aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa ' }).Count -ne 0 -or @($script:diagCommands | Where-Object { $_ -match '^.* logs deploy/app -c app --previous --tail=200$' -and $_ -notmatch '--all-containers' }).Count -ne 1) { throw 'diagnostic commands incorrect' }
$script:rows = @()
Test-E2ePilotDatabaseAfterRestore -BaseUrl 'https://example.invalid' -Context ([pscustomobject]@{ Owner = 'alice'; Repo = 'pilot'; Token = 'test' })
if ($script:rows.Count -ne 1 -or $script:rows[0].Ok -or $script:rows[0].Detail -notmatch 'postgres stato failed: exit 1.*container startup error') { throw 'pilot FAIL detail missing previous log' }
$script:diagMode = 'error'
try { Wait-E2ePilotServing -AppsApi 'https://example.invalid/apps' -Name 'api' -Headers @{}; throw 'failure not raised' } catch { if ($_.Exception.Message -ne 'api stato failed: exit 1') { throw 'diagnostic error masked original' } }
$script:diagMode = 'ok'; $script:diagCommands = @()
try { Wait-E2ePilotServing -AppsApi 'https://example.invalid/apps' -Name 'api' -Headers @{} -TimeoutSeconds 0; throw 'timeout not raised' } catch { if ($_.Exception.Message -ne 'api non e serving entro 0 secondi' -or $script:diagCommands.Count -ne 5) { throw 'timeout diagnostics failed' } }
Remove-Item -LiteralPath $script:OutDir -Recurse -Force
Remove-Variable -Name OutDir -Scope Script
$script:diagMode = ''
$script:rows = @()
Write-Host '[PASS] pilot diagnostics: failed, timeout and collection error'
Invoke-E2ePilotMirror -BaseUrl 'https://example.invalid' -Owner 'alice' -Token 'test' -Suffix '123' -AliasIp '192.168.250.10' -OriginPort 8443
if (-not $script:PilotContext -or @($script:rows | Where-Object { -not $_.Ok }).Count -ne 0 -or @($script:rows).Count -ne 8) { throw "pilot mirror success: $($script:rows | Out-String)" }
if ($script:cleanup -notcontains 'mirror-disable' -or $script:cleanup -notcontains 'mirror-dest-stop') { throw 'pilot cleanup missing' }
$script:rows = @(); $script:cleanup = @(); $script:unsigned = $true
Invoke-E2ePilotMirror -BaseUrl 'https://example.invalid' -Owner 'alice' -Token 'test' -Suffix '123' -AliasIp '192.168.250.10' -OriginPort 8443
if ($script:PilotContext -or @($script:rows | Where-Object { -not $_.Ok -and $_.Name -eq 'pilot: immagini firmate' }).Count -ne 1) { throw 'unsigned image accepted' }
if ($script:cleanup -notcontains 'mirror-disable' -or $script:cleanup -notcontains 'mirror-dest-stop') { throw 'pilot cleanup missing after failure' }
$unsignedRow = @($script:rows | Where-Object { $_.Name -eq 'pilot: immagini firmate' })[0]
if ($unsignedRow.Detail -notmatch 'db v1.0.0' -or $unsignedRow.Detail -notmatch 'name=v1.0.0' -or $unsignedRow.Detail -notmatch 'signature=unverified' -or $unsignedRow.Detail -notmatch 'pushedBy.kind=job' -or $unsignedRow.Detail -notmatch ('digest=sha256:' + $script:digest)) { throw "unsigned detail: $($unsignedRow.Detail)" }
$script:rows = @(); $script:unsigned = $false; $script:pusherKind = 'user'
Invoke-E2ePilotMirror -BaseUrl 'https://example.invalid' -Owner 'alice' -Token 'test' -Suffix '123' -AliasIp '192.168.250.10' -OriginPort 8443
$userRow = @($script:rows | Where-Object { -not $_.Ok })[0]
if ($script:PilotContext -or $userRow.Detail -notmatch 'signature=verified' -or $userRow.Detail -notmatch 'pushedBy.kind=user') { throw "pushedBy user detail: $($userRow.Detail)" }
$script:rows = @(); $script:pusherKind = 'job'; $script:imagePrefix = 'altro/repo/'
Invoke-E2ePilotMirror -BaseUrl 'https://example.invalid' -Owner 'alice' -Token 'test' -Suffix '123' -AliasIp '192.168.250.10' -OriginPort 8443
$missingRow = @($script:rows | Where-Object { -not $_.Ok })[0]
if ($script:PilotContext -or $missingRow.Detail -notmatch 'assente' -or $missingRow.Detail -notmatch 'altro/repo/api') { throw "missing image detail: $($missingRow.Detail)" }
$script:imagePrefix = 'alice/pilot-123/'
$script:OutDir = Join-Path ([System.IO.Path]::GetTempPath()) ('pilot-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $script:OutDir | Out-Null
$script:rows = @()
Invoke-E2ePilotMirror -BaseUrl 'https://example.invalid' -Owner 'alice' -Token 'test' -Suffix '123' -AliasIp '192.168.250.10' -OriginPort 8443
$saved = Join-Path $script:OutDir 'pilot-packages.json'
if (-not $script:PilotContext -or -not (Test-Path -LiteralPath $saved) -or (Get-Content -LiteralPath $saved -Raw) -notmatch 'alice/pilot-123/api') { throw 'pilot-packages.json not saved' }
Remove-Item -LiteralPath $script:OutDir -Recurse -Force
Remove-Variable -Name OutDir -Scope Script
Write-Host '[PASS] pilot mirror: successo, immagine non firmata, pushedBy user, immagine assente, pilot-packages.json e pulizia'
$script:rows = @(); $script:probeFail = $false
$ctx = [pscustomobject]@{ Owner = 'alice'; Repo = 'pilot'; Tag = 'v1'; Token = 'test' }
if (-not (Initialize-E2ePilotDatabase -BaseUrl 'https://example.invalid' -Context $ctx)) { throw 'pilot database setup failed' }
if ($script:dbCreate.name -ne 'postgres' -or $script:dbCreate.image -ne 'db:v1' -or $script:pgdataValue -ne '/var/lib/postgresql/data/pgdata') { throw 'pilot database image or PGDATA incorrect' }
if (-not $ctx.ApiId -or $ctx.PostgresId -notmatch '^[0-9a-f-]{36}$' -or (@($ctx.CreatedApps) -join ',') -ne 'api' -or @($script:rows | Where-Object { -not $_.Ok }).Count -ne 0) { throw 'pilot database setup missing result' }
$script:rows = @()
Test-E2ePilotDatabaseAfterRestore -BaseUrl 'https://example.invalid' -Context $ctx
if (@($script:rows | Where-Object { $_.Ok }).Count -ne 1) { throw 'restore success missing' }
$script:rows = @(); $script:probeFail = $true
Test-E2ePilotDatabaseAfterRestore -BaseUrl 'https://example.invalid' -Context $ctx
if (@($script:rows | Where-Object { -not $_.Ok }).Count -ne 1) { throw 'restore accepted failed database probe' }
Write-Host '[PASS] pilot database: preparazione, restore e probe fallito'
$script:siteMode = $true; $script:rows = @(); $script:showIssues = $false
$site = Initialize-E2ePilotSite -BaseUrl 'https://example.invalid' -AdminCookie 'admin' -Suffix '123' -CaFingerprint ('ab' * 32) -Context $ctx
if (-not $site -or $site.Tester -ne 'e2etester123' -or @($script:rows | Where-Object { -not $_.Ok }).Count -ne 0) { throw 'pilot site setup failed' }
$script:rows = @(); $script:showIssues = $true
$site = Initialize-E2ePilotSite -BaseUrl 'https://example.invalid' -AdminCookie 'admin' -Suffix '123' -CaFingerprint ('ab' * 32) -Context $ctx
if ($site -or @($script:rows | Where-Object { -not $_.Ok }).Count -ne 1) { throw 'tester access to issues accepted' }
$script:rows = @(); $script:showIssues = $false
$site = Initialize-E2ePilotSite -BaseUrl 'https://example.invalid' -AdminCookie 'admin' -Suffix '123' -CaFingerprint ('cd' * 32) -Context $ctx
if ($site -or @($script:rows | Where-Object { -not $_.Ok }).Count -ne 1) { throw 'wrong CA fingerprint accepted' }
$script:rows = @(); $script:publicApps = $true
$site = Initialize-E2ePilotSite -BaseUrl 'https://example.invalid' -AdminCookie 'admin' -Suffix '123' -CaFingerprint ('ab' * 32) -Context $ctx
if ($site -or @($script:rows | Where-Object { -not $_.Ok }).Count -ne 1) { throw 'public apps enabled accepted' }
$script:publicApps = $false
Write-Host '[PASS] pilot site: lista, privacy e impronta CA'

function New-Object { param($TypeName) [pscustomobject]@{ Thumbprint = 'CA123' } }
function New-Item { param($ItemType, $Path, [switch]$Force) }
function Test-Path { param($LiteralPath) return $true }
function Invoke-ExternalCommand { param($FilePath, $ArgumentList, $TimeoutSeconds) [pscustomobject]@{ ExitCode = 0; StdOut = "STEP ok anonymous-blocked`nSTEP ok tester-https-data`nISSUE=42`nSTEP ok tester-report`n"; StdErr = '' } }
function Write-E2eText { param($Path, $Stdout, $Stderr) }
function Invoke-HttpDownloadFile { param($Uri, $OutFile, $Headers) [pscustomobject]@{ StatusCode = 200 } }
function Get-FileHash { param($LiteralPath, $Algorithm) [pscustomobject]@{ Hash = $(if ($script:badAttachment -and $LiteralPath -like '*allegato.png') { 'different' } else { 'same' }) } }
$script:VmIp = '192.0.2.1'; $script:siteMode = $false; $script:issueMode = $true; $script:rows = @()
$site = [pscustomobject]@{ Address = 'https://sito.example.test'; Tester = 'e2etester123'; TesterToken = 'test'; Bot = 'e2epilotbot123' }
Invoke-E2ePilotTester -BaseUrl 'https://example.invalid' -CaPath 'ca.crt' -WorkDir 'C:\pilot' -Context $ctx -Site $site
if (@($script:rows | Where-Object { $_.Ok }).Count -ne 2 -or @($script:rows | Where-Object { -not $_.Ok }).Count -ne 0) { throw "pilot tester verification failed: $($script:rows | Out-String)" }
$script:rows = @(); $script:badAttachment = $true
Invoke-E2ePilotTester -BaseUrl 'https://example.invalid' -CaPath 'ca.crt' -WorkDir 'C:\pilot' -Context $ctx -Site $site
if (@($script:rows | Where-Object { -not $_.Ok }).Count -ne 1) { throw 'different screenshot accepted' }
Write-Host '[PASS] pilot tester: issue e confronto dello screenshot'

$script:rows = @(); $script:delMode = $true
$ctx2 = [pscustomobject]@{ Owner = 'alice'; Repo = 'pilot'; Token = 'test'; CreatedApps = [System.Collections.ArrayList]@('api', 'sito') }
Remove-E2ePilotApps -BaseUrl 'https://example.invalid' -Context $ctx2
if ($script:delCalls.Count -ne 2 -or @($script:delCalls | Where-Object { $_ -like '*/apps/api' }).Count -ne 1 -or @($script:delCalls | Where-Object { $_ -like '*/apps/sito' }).Count -ne 1 -or @($script:delCalls | Where-Object { $_ -like '*postgres*' }).Count -ne 0) { throw "pilot cleanup deletes: $($script:delCalls -join ',')" }
if ($script:rows.Count -ne 1 -or -not $script:rows[0].Ok -or $script:rows[0].Name -ne 'pilot: eliminazione delle app api e sito') { throw "pilot cleanup row: $($script:rows | Out-String)" }
$script:rows = @(); $script:delCalls = @()
Remove-E2ePilotApps -BaseUrl 'https://example.invalid' -Context ([pscustomobject]@{ Owner = 'alice'; Repo = 'pilot'; Token = 'test'; CreatedApps = [System.Collections.ArrayList]@('sito') })
if ($script:delCalls.Count -ne 1 -or $script:delCalls[0] -notlike '*/apps/sito' -or -not $script:rows[0].Ok) { throw 'cleanup must delete only created apps' }
$script:rows = @(); $script:delCalls = @()
Remove-E2ePilotApps -BaseUrl 'https://example.invalid' -Context ([pscustomobject]@{ Owner = 'alice'; Repo = 'pilot'; Token = 'test' })
if ($script:delCalls.Count -ne 0 -or -not $script:rows[0].Ok -or $script:rows[0].Detail -notmatch 'nessuna app') { throw 'cleanup without created apps must not delete' }
$script:rows = @(); $script:delCalls = @(); $script:delStatus = 500
Remove-E2ePilotApps -BaseUrl 'https://example.invalid' -Context $ctx2
if ($script:delCalls.Count -ne 2 -or $script:rows[0].Ok -or $script:rows[0].Detail -notmatch 'api: DELETE: status 500' -or $script:rows[0].Detail -notmatch 'sito: DELETE: status 500') { throw "cleanup error detail: $($script:rows | Out-String)" }
$script:rows = @(); $script:delStatus = 204; $script:delGone = $false
Remove-E2ePilotApps -BaseUrl 'https://example.invalid' -Context $ctx2 -TimeoutSeconds 0
if ($script:rows[0].Ok -or $script:rows[0].Detail -notmatch 'ancora presente') { throw "cleanup must wait for 404: $($script:rows | Out-String)" }
$script:delMode = $false; $script:delGone = $true; $script:rows = @()
Write-Host '[PASS] pilot cleanup: DELETE di api e sito, mai postgres, attesa del 404 e dettaglio errori'
