<#PSScriptInfo
.VERSION 1.0.0
.AUTHOR GitStack
.DESCRIPTION On-login script for GitStack WSL2 (W1).
#>

# on-login.ps1 - Avvia WSL e GitStack all'accesso di Windows (W1)
# Lanciato da un attivita pianificata "All'accesso utente" creata da install-wsl.ps1.

param()

$ErrorActionPreference = 'SilentlyContinue'

# Avvia WSL se non e in esecuzione
try {
    $wslRunning = wsl --status 2>&1
    if ($LASTEXITCODE -ne 0) {
        wsl ~ 2>&1 | Out-Null
        Start-Sleep -Seconds 5
    }
} catch {}

# Controlla la distro GitStack e avvia i servizi se necessario
try {
    $distros = wsl -l -q 2>&1
    $gsDistro = $distros | Select-String -Pattern 'ubuntu|gitstack' -AllMatches | ForEach-Object { $_.Matches[0].Value } | Select-Object -First 1

    if ($gsDistro) {
        # Avvia la distro (systemctl start senza root fallisce inside WSL)
        wsl -d $gsDistro -- true 2>&1 | Out-Null
    }
} catch {}
