<#PSScriptInfo
.VERSION 1.0.0
.AUTHOR GitStack
.DESCRIPTION On-login script for GitStack WSL2 (W1).
#>

# on-login.ps1 - Avvia WSL e GitStack all'accesso di Windows (W1)
# Lanciato da un attivita pianificata "All'accesso utente" creata da install-wsl.ps1.

param()

$ErrorActionPreference = 'SilentlyContinue'

# Nome della distro WSL creata da install-wsl.ps1
$GS_DISTRO_NAME = 'GitStack'

# Tieni viva la distro GitStack con un processo in background,
# altrimenti WSL la ferma quando non ci sono piu' processi aperti.
try {
    $wslRunning = wsl --status 2>&1
    if ($LASTEXITCODE -ne 0) {
        Start-Sleep -Seconds 3
    }
} catch {}

# Controlla la distro GitStack e avvia i servizi se necessario
try {
    $distros = wsl -l -q 2>&1
    $gsDistro = $distros | Where-Object { $_ -match $GS_DISTRO_NAME } | Select-Object -First 1

    if ($gsDistro) {
        # Avvia un processo sleep infinity in background (utente root)
        # WSL mantiene la distro viva finche' il processo e' attivo.
        wsl -d $gsDistro -u root -- bash -c "nohup sleep infinity </dev/null >/dev/null 2>&1 &" 2>&1 | Out-Null
    }
} catch {}
