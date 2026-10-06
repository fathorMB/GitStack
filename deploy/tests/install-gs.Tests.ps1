# Prova di install-gs.ps1 (GIT-171): parser senza errori e verifica del
# checksum contro un server HTTP locale (HttpListener in un runspace), senza
# istanza. Gira con Windows PowerShell 5.1 e con pwsh (CI Linux).
#   powershell.exe -NoProfile -File deploy\tests\install-gs.Tests.ps1
$ErrorActionPreference = 'Stop'
$script = Join-Path $PSScriptRoot '..\..\web\deploy\install-gs.ps1'
$script = (Resolve-Path -LiteralPath $script).Path
$failed = 0
function Check([string]$name, [bool]$ok) {
    if ($ok) { Write-Host "ok   - $name" } else { Write-Host "FAIL - $name"; $script:failed++ }
}

# 1. Parser.
$tokens = $null; $errors = $null
[void][System.Management.Automation.Language.Parser]::ParseFile($script, [ref]$tokens, [ref]$errors)
Check 'il parser non da errori' ($errors.Count -eq 0)
$bytes = [IO.File]::ReadAllBytes($script)
Check 'solo ASCII, senza BOM (PowerShell 5.1)' (-not ($bytes | Where-Object { $_ -gt 127 }) )

# 2. Server finto.
$root = Join-Path ([IO.Path]::GetTempPath()) ("igs-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path "$root\site\downloads" | Out-Null
$payload = [Text.Encoding]::ASCII.GetBytes('gs finto')
[IO.File]::WriteAllBytes("$root\site\downloads\gs_windows_amd64.exe", $payload)
$good = (Get-FileHash -Algorithm SHA256 -LiteralPath "$root\site\downloads\gs_windows_amd64.exe").Hash.ToLower()

$port = Get-Random -Minimum 20000 -Maximum 40000
$listener = New-Object System.Net.HttpListener
$listener.Prefixes.Add("http://127.0.0.1:$port/")
$listener.Start()
$ps = [PowerShell]::Create()
[void]$ps.AddScript({
    param($l, $dir)
    while ($l.IsListening) {
        try { $ctx = $l.GetContext() } catch { break }
        $p = Join-Path $dir ($ctx.Request.Url.AbsolutePath.TrimStart('/').Replace('/', [IO.Path]::DirectorySeparatorChar))
        if (Test-Path -LiteralPath $p -PathType Leaf) {
            $b = [IO.File]::ReadAllBytes($p)
            $ctx.Response.OutputStream.Write($b, 0, $b.Length)
        } else { $ctx.Response.StatusCode = 404 }
        $ctx.Response.Close()
    }
}).AddArgument($listener).AddArgument("$root\site")
$handle = $ps.BeginInvoke()

$psExe = (Get-Process -Id $PID).Path
function Run-Installer([string]$dir) {
    $ErrorActionPreference = 'Continue'
    $out = & $psExe -NoProfile -File $script -BaseUrl "http://127.0.0.1:$port" -InstallDir $dir -Arch amd64 -NoPath 2>&1
    return @{ Code = $LASTEXITCODE; Out = ($out | Out-String) }
}

try {
    "$good  gs_windows_amd64.exe" | Set-Content -Encoding ASCII "$root\site\downloads\SHA256SUMS"
    $r = Run-Installer "$root\ok"
    Check 'checksum corretto: installa' (($r.Code -eq 0) -and (Test-Path "$root\ok\gs.exe"))

    ("0" * 64 + "  gs_windows_amd64.exe") | Set-Content -Encoding ASCII "$root\site\downloads\SHA256SUMS"
    $r = Run-Installer "$root\bad"
    Check 'checksum sbagliato: fallisce' ($r.Code -ne 0)
    Check 'il messaggio parla del checksum' ($r.Out -match 'checksum SHA-256')
    Check 'con checksum sbagliato non installa nulla' (-not (Test-Path "$root\bad\gs.exe"))
}
finally {
    $listener.Stop(); $listener.Close()
    $ps.Dispose()
    Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue
}
exit $(if ($failed -eq 0) { 0 } else { 1 })
