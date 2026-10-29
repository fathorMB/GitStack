# deploy/test-vm/tests/jsonfield.Tests.ps1
#
# Prova degli helper di lib/http.ps1 usati da e9/e10 (GIT-434): Get-JsonField,
# Invoke-RepoImportWithRetry e Wait-GitServing, sotto Set-StrictMode -Version Latest
# (come lib/common.ps1). Da lanciare con Windows PowerShell 5.1:
#   powershell.exe -NoProfile -File deploy\test-vm\tests\jsonfield.Tests.ps1
# Esce con codice != 0 se una verifica fallisce. Solo ASCII.

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '..\lib\http.ps1')

$failures = 0
function Assert-That([bool]$Cond, [string]$Msg) {
    if ($Cond) { Write-Host "PASS: $Msg" } else { Write-Host "FAIL: $Msg"; $script:failures++ }
}

Write-Host "PowerShell $($PSVersionTable.PSVersion)"

# JSON reale di e9 nel log di GIT-370: senza lastError (core lo omette se vuoto).
$e9Json = '{"attempts":0,"createdAt":"2026-10-27T08:42:29.121379Z","enabled":true,"hasToken":true,"id":"c70edd1a-3367-4e6f-82b7-50a13662d3fc","lastPushed":{},"nextAttemptAt":"2026-10-27T08:42:29.121172Z","state":"pending","updatedAt":"2026-10-27T08:42:29.121379Z","url":"https://192.168.250.10:8443/mirror.git","username":"mirror-bot"}'
$mj = $e9Json | ConvertFrom-Json

$threw = $false
try { $null = [string]$mj.lastError } catch { $threw = $true }
Assert-That $threw "accesso diretto a .lastError sotto StrictMode Latest lancia (riproduce e9)"
Assert-That ((Get-JsonField -Object $mj -Name 'lastError') -eq '') "helper: lastError assente -> ''"
Assert-That ((Get-JsonField -Object $mj -Name 'lastSyncedAt') -eq '') "helper: lastSyncedAt assente -> ''"
Assert-That ((Get-JsonField -Object $mj -Name 'state') -eq 'pending') "helper: state presente -> 'pending'"
$withErr = '{"state":"error","lastError":"boom","nextAttemptAt":null}' | ConvertFrom-Json
Assert-That ((Get-JsonField -Object $withErr -Name 'lastError') -eq 'boom') "helper: lastError presente -> 'boom'"
Assert-That ((Get-JsonField -Object $withErr -Name 'nextAttemptAt') -eq '') "helper: campo null -> ''"
Assert-That ((Get-JsonField -Object $null -Name 'state') -eq '') "helper: oggetto null -> ''"

# Invoke-HttpRaw finto: risposte in sequenza.
$script:Seq = @()
$script:Calls = 0
function Invoke-HttpRaw {
    param([string]$Uri, [string]$Method = 'GET', [string]$Body, [hashtable]$Headers, [string]$Cookie, [int]$TimeoutSec = 20)
    $i = $script:Calls
    $script:Calls++
    if ($i -ge $script:Seq.Count) { $i = $script:Seq.Count - 1 }
    return $script:Seq[$i]
}
function New-Resp([int]$Status, [string]$Body = '') { [pscustomobject]@{ StatusCode = $Status; Body = $Body; Error = $null } }
$gitDown = '{"error":{"code":"git_unavailable","message":"Servizio git non disponibile: il repo non e'' stato creato"}}'

# Retry: 503 git_unavailable x2 poi 201 -> 3 tentativi.
$script:Calls = 0; $script:Seq = @((New-Resp 503 $gitDown), (New-Resp 503 $gitDown), (New-Resp 201 '{}'))
$r = Invoke-RepoImportWithRetry -Uri 'http://x/import' -Body '{}' -MaxAttempts 4 -DelaySeconds 0
Assert-That ($r.StatusCode -eq 201 -and $r.Attempts -eq 3 -and $script:Calls -eq 3) "retry: 503 git_unavailable x2 poi 201 -> 3 tentativi"

# Sempre 503 git_unavailable: si ferma a N.
$script:Calls = 0; $script:Seq = @((New-Resp 503 $gitDown))
$r = Invoke-RepoImportWithRetry -Uri 'http://x/import' -Body '{}' -MaxAttempts 4 -DelaySeconds 0
Assert-That ($r.StatusCode -eq 503 -and $r.Attempts -eq 4 -and $script:Calls -eq 4) "retry: sempre 503 git_unavailable -> si ferma a 4 tentativi"

# 503 con un altro codice: subito.
$script:Calls = 0; $script:Seq = @((New-Resp 503 '{"error":{"code":"egress_blocked","message":"x"}}'))
$r = Invoke-RepoImportWithRetry -Uri 'http://x/import' -Body '{}' -MaxAttempts 4 -DelaySeconds 0
Assert-That ($r.StatusCode -eq 503 -and $r.Attempts -eq 1 -and $script:Calls -eq 1) "retry: 503 con altro codice -> nessun retry"

# 503 con corpo non JSON: subito.
$script:Calls = 0; $script:Seq = @((New-Resp 503 '<html>bad gateway</html>'))
$r = Invoke-RepoImportWithRetry -Uri 'http://x/import' -Body '{}' -MaxAttempts 4 -DelaySeconds 0
Assert-That ($r.Attempts -eq 1 -and $script:Calls -eq 1) "retry: 503 con corpo non JSON -> nessun retry"

# Altri status: subito.
foreach ($st in 400, 401, 404, 409, 500, 502) {
    $script:Calls = 0; $script:Seq = @((New-Resp $st $gitDown))
    $r = Invoke-RepoImportWithRetry -Uri 'http://x/import' -Body '{}' -MaxAttempts 4 -DelaySeconds 0
    Assert-That ($r.StatusCode -eq $st -and $r.Attempts -eq 1 -and $script:Calls -eq 1) "retry: status $st -> nessun retry"
}

# Wait-GitServing: 503, 503, 200 -> ok.
$script:Calls = 0; $script:Seq = @((New-Resp 503), (New-Resp 503), (New-Resp 200 '[]'))
$ok = $true
try { Wait-GitServing -BaseUrl 'http://x' -Owner 'o' -Repo 'r' -TimeoutSeconds 30 -IntervalSeconds 0 } catch { $ok = $false }
Assert-That ($ok -and $script:Calls -eq 3) "Wait-GitServing: 503,503,200 -> passa al terzo giro"

# Wait-GitServing: sempre 503 -> errore con timeout e ultimo status.
$script:Calls = 0; $script:Seq = @((New-Resp 503))
$msg = ''
try { Wait-GitServing -BaseUrl 'http://x' -Owner 'o' -Repo 'r' -TimeoutSeconds 1 -IntervalSeconds 0 } catch { $msg = $_.Exception.Message }
Assert-That ($msg -match 'dopo 1 secondi' -and $msg -match 'ultimo status 503') "Wait-GitServing: timeout nel messaggio con timeout e ultimo status: '$msg'"

# Get-TrimmedText (GIT-509): StdOut $null, poi vuoto, poi lo SHA, come nel ciclo di attesa di e9.
$sha = '5db811500000000000000000000000000000abcd'
$seq = @([pscustomobject]@{ ExitCode = 1; StdOut = $null }, [pscustomobject]@{ ExitCode = 0; StdOut = '' }, [pscustomobject]@{ ExitCode = 0; StdOut = "$sha`n" })
$got = @(); foreach ($r in $seq) { $got += (Get-TrimmedText -Result $r) }
Assert-That ($got[0] -eq '' -and $got[1] -eq '' -and $got[2] -eq $sha) "Get-TrimmedText: StdOut null -> '', vuoto -> '', SHA -> SHA senza a capo"
Assert-That ((Get-TrimmedText -Result $null) -eq '') "Get-TrimmedText: risultato null -> ''"
Assert-That ((Get-TrimmedText -Result ([pscustomobject]@{ A = 1 })) -eq '') "Get-TrimmedText: campo mancante -> ''"
Assert-That ((Get-TrimmedText -Result ([pscustomobject]@{ Body = $null }) -Property 'Body').Contains('x') -eq $false) "Get-TrimmedText: Body null -> '' e .Contains non lancia"
$loopOk = $false; $n = 0
foreach ($r in $seq) { $n++; if ((Get-TrimmedText -Result $r) -eq $sha) { $loopOk = $true; break } }
Assert-That ($loopOk -and $n -eq 3) "ciclo di attesa: continua su null e vuoto, esce allo SHA"

if ($failures -gt 0) { Write-Host "$failures verifiche fallite"; exit 1 }
Write-Host 'OK: tutte le verifiche passate'
exit 0
