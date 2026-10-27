$ErrorActionPreference = 'Stop'
$script:rows = @()
$script:sha = 'a' * 40
$script:digest = 'b' * 64
$script:unsigned = $false
$script:probeFail = $false
$script:cleanup = @()
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
    $output = if ($Command -match 'pilot-origin.sh prepare') { "$script:sha`nSTEP ok origin-prepare" } elseif ($Command -match 'pilot-origin.sh tag') { "$script:sha`nSTEP ok origin-tag" } elseif ($Command -match 'pilot-db-probe.sh .* write') { 'STEP ok database-write' } elseif ($Command -match 'pilot-db-probe.sh .* data') { 'STEP ok database-data' } else { '' }
    $exit = if ($script:probeFail -and $Command -match 'pilot-db-probe.sh') { 1 } else { 0 }
    [pscustomobject]@{ ExitCode = $exit; StdOut = $output; StdErr = '' }
}
function Invoke-RepoImportWithRetry { param($Uri, $Headers, $MaxAttempts, $DelaySeconds, $Body) [pscustomobject]@{ StatusCode = 201; Body = '{}' } }
function Invoke-HttpRaw {
    param($Uri, $Method, $Headers, $Body)
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
        '{"items":[{"name":"api","tagDetails":[{"name":"v1.0.0","signature":"verified","pushedBy":{"kind":"job"},"digest":"sha256:' + $script:digest + '"}]},{"name":"sito","tagDetails":[{"name":"v1.0.0","signature":"' + $signature + '","pushedBy":{"kind":"job"},"digest":"sha256:' + $script:digest + '"}]}]}'
    } else { '{"visibility":"private"}' }
    [pscustomobject]@{ StatusCode = $status; Body = $response }
}
. (Join-Path $PSScriptRoot '..\lib\pilot.ps1')
Invoke-E2ePilotMirror -BaseUrl 'https://example.invalid' -Owner 'alice' -Token 'test' -Suffix '123' -AliasIp '192.168.250.10' -OriginPort 8443
if (-not $script:PilotContext -or @($script:rows | Where-Object { -not $_.Ok }).Count -ne 0 -or @($script:rows).Count -ne 7) { throw "pilot mirror success: $($script:rows | Out-String)" }
if ($script:cleanup -notcontains 'mirror-disable' -or $script:cleanup -notcontains 'mirror-dest-stop') { throw 'pilot cleanup missing' }
$script:rows = @(); $script:cleanup = @(); $script:unsigned = $true
Invoke-E2ePilotMirror -BaseUrl 'https://example.invalid' -Owner 'alice' -Token 'test' -Suffix '123' -AliasIp '192.168.250.10' -OriginPort 8443
if ($script:PilotContext -or @($script:rows | Where-Object { -not $_.Ok -and $_.Name -eq 'pilot: immagini firmate' }).Count -ne 1) { throw 'unsigned image accepted' }
if ($script:cleanup -notcontains 'mirror-disable' -or $script:cleanup -notcontains 'mirror-dest-stop') { throw 'pilot cleanup missing after failure' }
Write-Host '[PASS] pilot mirror: successo, immagine non firmata e pulizia'
$script:rows = @(); $script:probeFail = $false
$ctx = [pscustomobject]@{ Owner = 'alice'; Repo = 'pilot'; Tag = 'v1'; Token = 'test' }
if (-not (Initialize-E2ePilotDatabase -BaseUrl 'https://example.invalid' -Context $ctx)) { throw 'pilot database setup failed' }
if (-not $ctx.ApiId -or @($script:rows | Where-Object { -not $_.Ok }).Count -ne 0) { throw 'pilot database setup missing result' }
$script:rows = @()
Test-E2ePilotDatabaseAfterRestore -BaseUrl 'https://example.invalid' -Context $ctx
if (@($script:rows | Where-Object { $_.Ok }).Count -ne 1) { throw 'restore success missing' }
$script:rows = @(); $script:probeFail = $true
Test-E2ePilotDatabaseAfterRestore -BaseUrl 'https://example.invalid' -Context $ctx
if (@($script:rows | Where-Object { -not $_.Ok }).Count -ne 1) { throw 'restore accepted failed database probe' }
Write-Host '[PASS] pilot database: preparazione, restore e probe fallito'
