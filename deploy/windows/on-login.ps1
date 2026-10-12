<#PSScriptInfo
.VERSION 1.0.0
.AUTHOR GitStack
.DESCRIPTION On-login script for GitStack WSL2 (W1).
#>

# on-login.ps1 — Avvia WSL e GitStack all'accesso di Windows (W1)
# Lanciato da un'attività pianificata "All'accesso utente" creata da install-wsl.ps1.

param()

$ErrorActionPreference = 'SilentlyContinue'

# Avvia WSL se non è in esecuzione
try {
    $wslRunning = wsl --status 2>&1
    if ($LASTEXITCODE -ne 0) {
        wsl ~ &>/dev/null
        Start-Sleep -Seconds 5
    }
} catch {}

# Controlla la distro GitStack e avvia i servizi se necessario
try {
    $distros = wsl -l -q 2>&1
    $gsDistro = $distros | Select-String -Pattern 'ubuntu|gitstack' -AllMatches | ForEach-Object { $_.Matches[0].Value } | Select-Object -First 1

    if ($gsDistro) {
        $k3sActive = wsl -d $gsDistro -- systemctl is-active k3s 2>&1
        if ($LASTEXITCODE -ne 0 -or $k3sActive -notmatch 'active') {
            wsl -d $gsDistro -- systemctl start k3s 2>/dev/null
        }
        $gsActive = wsl -d $gsDistro -- systemctl is-active gitstack 2>&1
        if ($LASTEXITCODE -ne 0 -or $gsActive -notmatch 'active') {
            wsl -d $gsDistro -- systemctl start gitstack 2>/dev/null
        }
    }
} catch {}
