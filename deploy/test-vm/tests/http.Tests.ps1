# deploy/test-vm/tests/http.Tests.ps1
#
# Prova di Invoke-HttpRaw (lib/http.ps1) contro un HttpListener su 127.0.0.1.
# Da lanciare con Windows PowerShell 5.1:
#   powershell.exe -NoProfile -File deploy\test-vm\tests\http.Tests.ps1
# Esce con codice != 0 se una verifica fallisce. Solo ASCII.

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '..\lib\http.ps1')

$port = Get-Random -Minimum 20000 -Maximum 40000
$prefix = "http://127.0.0.1:$port/"
$listener = New-Object System.Net.HttpListener
$listener.Prefixes.Add($prefix)
$listener.Start()

# Server in un runspace: risponde 401 con corpo JSON e rimanda il Cookie ricevuto
# nel corpo e nell'header X-Seen-Cookie.
$ps = [PowerShell]::Create()
$null = $ps.AddScript({
    param($l, $n)
    for ($i = 0; $i -lt $n; $i++) {
        $ctx = $l.GetContext()
        $seen = [string]$ctx.Request.Headers['Cookie']
        $json = '{"code":"unauthorized","seen":"' + $seen + '"}'
        $bytes = [Text.Encoding]::UTF8.GetBytes($json)
        $ctx.Response.StatusCode = 401
        $ctx.Response.ContentType = 'application/json'
        $ctx.Response.Headers['X-Seen-Cookie'] = $seen
        $ctx.Response.ContentLength64 = $bytes.Length
        $ctx.Response.OutputStream.Write($bytes, 0, $bytes.Length)
        $ctx.Response.OutputStream.Close()
    }
}).AddArgument($listener).AddArgument(3)
$handle = $ps.BeginInvoke()

$failures = 0
function Assert-That([bool]$Cond, [string]$Msg) {
    if ($Cond) { Write-Host "PASS: $Msg" } else { Write-Host "FAIL: $Msg"; $script:failures++ }
}

try {
    Write-Host "PowerShell $($PSVersionTable.PSVersion)"

    $r1 = Invoke-HttpRaw -Uri "${prefix}api/x" -TimeoutSec 10
    Assert-That ($r1.StatusCode -eq 401) "senza cookie: status 401 (ottenuto $($r1.StatusCode))"
    Assert-That ($r1.Body -match '"code":"unauthorized"') "corpo 4xx non vuoto: '$($r1.Body)'"

    $r2 = Invoke-HttpRaw -Uri "${prefix}api/x" -Cookie 'gst_session=abc' -TimeoutSec 10
    Assert-That ($r2.StatusCode -eq 401) "con cookie: status 401 (ottenuto $($r2.StatusCode))"
    Assert-That ($r2.Body -match 'gst_session=abc') "il server ha ricevuto il Cookie gst_session=abc: '$($r2.Body)'"

    # Un header Cookie in -Headers NON deve essere l'unico meccanismo: su 5.1 si perde.
    $r3 = Invoke-HttpRaw -Uri "${prefix}api/x" -Cookie 'gst_session=xyz=1' -TimeoutSec 10
    Assert-That ($r3.Body -match 'gst_session=xyz=1') "cookie con '=' nel valore preservato: '$($r3.Body)'"

    $r4 = Invoke-HttpRaw -Uri 'http://127.0.0.1:1/' -TimeoutSec 3
    Assert-That ($r4.StatusCode -eq 0 -and $r4.Error) "connessione rifiutata: StatusCode 0 con Error"
} finally {
    try { $listener.Stop() } catch { }
    $ps.Dispose()
}

if ($failures -gt 0) { Write-Host "$failures verifiche fallite"; exit 1 }
Write-Host 'OK: tutte le verifiche passate'
exit 0
