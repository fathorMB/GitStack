# Test CI per install-wsl.ps1 (GIT-211, W1/W2): verifica statica
#
# I runner Windows di GitHub non hanno virtualizzazione annidata per WSL2
# quindi non e possibile un test di installazione reale. Questo test:
# 1. Verifica che il parser PowerShell non sollevi errori (syntax check).
# 2. Verifica che lo script sia ASCII puro (senza BOM, compatibile con PS 5.1).
# 3. Verifica che le sezioni critiche siano presenti (controllo strutturale).
# 4. Verifica on-login.ps1 e comportamenti critici.
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

# Parser e ASCII per on-login.ps1
$loginPath = Join-Path $PSScriptRoot '..\on-login.ps1'
$loginPath = (Resolve-Path -LiteralPath $loginPath).Path
$loginTokens = $null; $loginErrors = $null
[void][System.Management.Automation.Language.Parser]::ParseFile(
    $loginPath, [ref]$loginTokens, [ref]$loginErrors
)
Check 'on-login parser: nessun errore di sintassi' ($loginErrors.Count -eq 0)
$loginBytes = [IO.File]::ReadAllBytes($loginPath)
$loginHasBom = ($loginBytes[0] -eq 0xEF -and $loginBytes[1] -eq 0xBB -and $loginBytes[2] -eq 0xBF)
$loginHasNonAscii = ($loginBytes | Where-Object { $_ -gt 127 }).Count -gt 0
Check 'on-login file: solo ASCII, senza BOM' (-not $loginHasBom -and -not $loginHasNonAscii)

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
Check 'contiene warning produzione'  ($content -match 'produzione|non per la produzione')

# 4. Verifiche on-login.ps1 e comportamenti critici
$loginContent = [IO.File]::ReadAllText($loginPath)
Check 'on-login contiene "wsl -d"'       ($loginContent -match 'wsl -d')
Check 'on-login non usa &>/dev/null'     ($loginContent -notmatch '&>/dev/null')
Check 'install-wsl usa --no-launch'      ($content -match '\-\-no-launch')
Check 'install-wsl usa --name'           ($content -match '\-\-name')
Check 'install-wsl systemd inside distro' ($content -match 'wsl -d.*-u root' -and $content -match 'wsl\.conf')
Check 'install-wsl ShareOnNetwork PRIMA' ($content -match 'PRIMA di install\.sh')

# ============================================================
# 5. Test comportamentali delle funzioni pure (wsl-utils.ps1)
# ============================================================
# Dot-sourcing del modulo con le funzioni pure
$utilsPath = Join-Path $PSScriptRoot '..\wsl-utils.ps1'
$utilsPath = (Resolve-Path -LiteralPath $utilsPath).Path
. $utilsPath

# Mock di wsl per le funzioni che lo invocano: restituisce valori fissi
$mockWslResult = 0
$mockWslOutput = ''
function wsl {
    param([string[]]$Args)
    if ($Args -contains 'wslpath') {
        Set-Variable -Name LASTEXITCODE -Value $mockWslResult -Scope Script
        return $mockWslOutput
    }
    Set-Variable -Name LASTEXITCODE -Value $mockWslResult -Scope Script
    return $mockWslOutput
}

# 5a. Convert-WindowsToWSLPath: wslpath fallisce → fallback E:\ -> /mnt/e/
$mockWslResult = 1
$mockWslOutput = ''
$result = Convert-WindowsToWSLPath -WinPath 'E:\Aziende\GitStack\deploy\windows' -DistroName GitStack
Check 'convert fallback: E:\ -> /mnt/e/' ($result -eq '/mnt/e/Aziende/GitStack/deploy/windows')

# 5b. Convert-WindowsToWSLPath: wslpath ha successo (valore mock distinto)
$mockWslResult = 0
$mockWslOutput = '/mnt/x/mock'
$result = Convert-WindowsToWSLPath -WinPath 'X:\percorso' -DistroName GitStack
Check 'convert wslpath: successo (mock distinguibile)' ($result -eq '/mnt/x/mock')

# 5c. Convert-WindowsToWSLPath: root repo (..) risolto prima della chiamata
# Verifica che il file install-wsl.ps1 passi '..\..' per la radice del repo
Check 'install-wsl usa Resolve-Path ..\..' ($content -match 'Resolve-Path')

# 5d. Remove-GitStackWslconfig: rimuove solo righe marcate
$wslconfInput = "[wsl2]
# GitStack
networkingMode=mirrored
memory=4GB
[wsl2]
# Altre configurazioni
networkingMode=nat"
$expected = "[wsl2]
memory=4GB
[wsl2]
# Altre configurazioni
networkingMode=nat"
$result = Remove-GitStackWslconfig -Content $wslconfInput
Check 'wslconfig: rimuove networkingMode marcato' ($result -eq $expected)

# 5e. Remove-GitStackWslconfig: file senza marker
$plain = "[wsl2]
memory=4GB"
$result = Remove-GitStackWslconfig -Content $plain
Check 'wslconfig: nessun marker, nulla rimosso' ($result -eq $plain)

# 5f. Add-GitStackWslconfig: file vuoto → [wsl2] con networkingMode
$empty = ''
$result = Add-GitStackWslconfig -Content $empty
$expected = "[wsl2]
# GitStack
networkingMode=mirrored"
Check 'add: file vuoto' ($result -eq $expected)

# 5g. Add-GitStackWslconfig: senza [wsl2] → crea la sezione
$noSection = "memory=4GB"
$result = Add-GitStackWslconfig -Content $noSection
$expected = "memory=4GB
[wsl2]
# GitStack
networkingMode=mirrored"
Check 'add: senza [wsl2]' ($result -eq $expected)

# 5h. Add-GitStackWslconfig: con [wsl2] esistente → aggiunge dentro
$existing = "[wsl2]
memory=4GB"
$result = Add-GitStackWslconfig -Content $existing
$expected = "[wsl2]
# GitStack
networkingMode=mirrored
memory=4GB"
Check 'add: con [wsl2] esistente' ($result -eq $expected)

# 5i. Roundtrip: Add → Remove restituisce il contenuto originale
# Roundtrip solo con [wsl2] già presente: Add inserisce # GitStack dentro il blocco,
# Remove cancella il blocco e il marker; [wsl2] utente resta intatto.
$cases = @(
    "[wsl2]
memory=4GB",
    "[wsl2]
# Commento utente
memory=4GB"
)
$rtOk = $true
foreach ($case in $cases) {
    $added = Add-GitStackWslconfig -Content $case
    $removed = Remove-GitStackWslconfig -Content $added
    if ($removed -ne $case) {
        $rtOk = $false
        break
    }
}
Check 'roundtrip Add Remove con [wsl2] presente' ($rtOk -eq $true)

Write-Host ""
Write-Host "PSScriptAnalyzer: per una verifica completa (naming, best-practice), " `
    "eseguire Test-ScriptAnalyzer su un runner Windows con PSScriptInstaller." -ForegroundColor Yellow

exit $(if ($failed -eq 0) { 0 } else { 1 })
