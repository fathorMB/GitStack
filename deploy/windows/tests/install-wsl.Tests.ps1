# Test CI per install-wsl.ps1 (GIT-211, W1/W2): verifica statica
#
# I runner Windows di GitHub non hanno virtualizzazione annidata per WSL2,
# quindi non è possibile un test di installazione reale. Questo test:
# 1. Verifica che il parser PowerShell non sollevi errori (syntax check).
# 2. Verifica che lo script sia ASCII puro (senza BOM, compatibile con PS 5.1).
# 3. Verifica che le sezioni critiche siano presenti (controllo strutturale).
#
# Esecuzione:
#   powershell.exe -NoProfile -File deploy\windows\tests\install-wsl.Tests.ps1
#   pwsh -NoProfile -File deploy/windows/tests/install-wsl.Tests.ps1   (pwsh)

$ErrorActionPreference = 'Stop'
$scriptPath = Join-Path $PSScriptRoot '..\install-wsl.ps1'
$scriptPath = (Resolve-Path -LiteralPath $scriptPath).Path

$failed = 0
function Check([string]$name, [bool]$ok) {
    if ($ok) { Write-Host "ok   - $name" } else { Write-Host "FAIL - $name"; $script:failed++ }
}

# 1. Parser PowerShell: nessun errore di sintassi
$tokens = $null; $errors = $null
[void][System.Management.Automation.Language.Parser]::ParseFile(
    $scriptPath, [ref]$tokens, [ref]$errors
)
Check 'parser: nessun errore di sintassi' ($errors.Count -eq 0)

# 2. ASCII puro, senza BOM (compatibilità PowerShell 5.1)
$bytes = [IO.File]::ReadAllBytes($scriptPath)
$hasBom = ($bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF)
$hasNonAscii = ($bytes | Where-Object { $_ -gt 127 }).Count -gt 0
Check 'file: solo ASCII, senza BOM' (-not $hasBom -and -not $hasNonAscii)

# 3. Controllo strutturale: le sezioni critiche devono essere presenti
$content = [IO.File]::ReadAllText($scriptPath)

Check 'contiene -ShareOnNetwork'     ($content -match '\-ShareOnNetwork')
Check 'contiene -Uninstall'          ($content -match '\-Uninstall')
Check 'contiene Enable-WindowsOptionalFeature' ($content -match 'Enable-WindowsOptionalFeature')
Check 'contiene wsl --install'       ($content -match 'wsl\s+--install')
Check 'contiene networkingMode'      ($content -match 'networkingMode')
Check 'contiene New-NetFirewallRule' ($content -match 'New-NetFirewallRule')
Check 'contiene Register-ScheduledTask' ($content -match 'Register-ScheduledTask')
Check 'contiene systemd'             ($content -match 'systemd')
Check 'contiene Ubuntu-24.04'        ($content -match 'Ubuntu-24\.04')
Check 'contiene controllo build 22621' ($content -match '22621')
Check 'contiene porta 443'           ($content -match '443')
Check 'contiene porta 2222'          ($content -match '2222')
Check 'contiene install.sh'          ($content -match 'install\.sh')
Check 'contiene riferimento D14'     ($content -match 'D14')
Check 'contiene warning produzione'  ($content -match 'produzione|produzione|non per la produzione')

Write-Host ""
Write-Host "PSScriptAnalyzer: per una verifica completa (naming, best-practice), " `
    "eseguire Test-ScriptAnalyzer su un runner Windows con PSScriptInstaller." -ForegroundColor Yellow

exit $(if ($failed -eq 0) { 0 } else { 1 })
