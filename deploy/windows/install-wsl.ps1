<#PSScriptInfo
.VERSION 1.0.0
.GUID 00000000-0000-0000-0000-000000000000
.AUTHOR GitStack
.COMPANYNAME GitStack
.PROJECTURI
.CALLOFTEXT Installa GitStack su WSL2 (prova/demo, W1/W2).
.PROJECTNAME GitStack-WSL-Deploy
.TAGS GitStack WSL2 Ubuntu
.LICENSEURI
.REQUIRESELEVATIONATIONS $false
.REQUIRES powershell 5.1
.DOCUMENTATION
# install-wsl.ps1 — Installazione di prova di GitStack su Windows via WSL2 (W1, W2)

## Scopo
Installa GitStack dentro WSL2 su Windows 10/11 per prove, demo e piccoli team.
NON è progettato per la produzione: WSL2 ha limitazioni note di rete (IP dinamico,
firewall di Windows non esposto a un cliente esterno), gestione dei servizi e
affidabilità (vedere D14 [c_cf5bc915298b97db]).

## Requisiti
- Windows 10 build 22621+ (21H2) o Windows 11 (per WSL2 e networkingMode=mirrored)
- Abilitazione Virtualizzazione nel BIOS/UEFI
- Per -ShareOnNetwork: Windows 11 22H2 (build ≥ 22621)

## Uso
# Installazione interattiva (default, solo accesso locale):
.\deploy\windows\install-wsl.ps1

# Con abilitazione della rete mirrored (Windows 11 22H2+):
.\deploy\windows\install-wsl.ps1 -ShareOnNetwork

# Disinstallazione:
.\deploy\windows\install-wsl.ps1 -Uninstall
#>

[CmdletBinding()]
param(
    [switch]$ShareOnNetwork,
    [switch]$Uninstall
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# ============================================================
# Utilità di logging
# ============================================================

function Write-Status { param([string]$Msg) Write-Host "==> $Msg" }
function Write-Warn  { param([string]$Msg) Write-Warning $Msg }
function Write-Fail  { param([string]$Msg); Write-Error $Msg; exit 1 }

# ============================================================
# Disinstallazione (-Uninstall)
# ============================================================

function Uninstall-GitStackWSL {
    Write-Status "Disinstallazione di GitStack da WSL2 ..."

    # 1. Rimuovi l'attività pianificata
    $taskName = 'GitStack-WSL-Login'
    if (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue) {
        Write-Status "Rimozione attività pianificata '$taskName' ..."
        Unregister-ScheduledTask -TaskName $taskName -Confirm:$false
    }

    # 2. Rimuovi le regole del firewall
    if ($ShareOnNetwork) {
        foreach ($port in @(443, 2222)) {
            $ruleName = "GitStack-WSL-$port"
            if (Get-NetFirewallRule -Name $ruleName -ErrorAction SilentlyContinue) {
                Write-Status "Rimozione regola firewall '$ruleName' ..."
                Remove-NetFirewallRule -Name $ruleName
            }
        }
    }

    # 3. Rimuovi .wslconfig dell'utente se esiste
    $wslconf = Join-Path $env:USERPROFILE '.wslconfig'
    if (Test-Path $wslconf) {
        $content = Get-Content $wslconf -Raw
        if ($content -match 'GitStack|wsstack') {
            Write-Status "Rimozione configurazione WSL di GitStack da $wslconf ..."
            # Rimuovi solo le sezioni GitStack/WSStack
            $newContent = $content -replace '^\[(wsstack|gitstack)[\r\n]*.*?\]\r?\n' -replace '^\[wsl\].*?\]\r?\n' -replace 'networkingMode\s*=\s*mirrored.*?\r?\n', ''
            if ($newContent.Trim()) {
                $newContent | Set-Content $wslconf -Encoding UTF8
            } else {
                Remove-Item $wslconf -Force
            }
        }
    }

    # 4. Disinstalla la distro Ubuntu da WSL
    Write-Status "Rimozione della distro Ubuntu da WSL ..."
    $distros = wsl --list --quiet 2>$null
    foreach ($d in $distros) {
        if ($d -match 'ubuntu|GitStack') {
            Write-Status "Rimozione distro '$d' ..."
            wsl --unregister $d
        }
    }

    Write-Status "Disinstallazione completata."
}

if ($Uninstall) {
    Uninstall-GitStackWSL
    exit 0
}

# ============================================================
# Verifica dei prerequisiti di Windows
# ============================================================

function Test-Prerequisites {
    Write-Status "Verifica dei prerequisiti di Windows ..."

    # 1. Versione di Windows
    $os = Get-CimInstance Win32_OperatingSystem
    $build = [int]$os.BuildNumber

    if ($build -lt 19041) {
        Write-Fail "Windows 10 build 19041 (20H2) o superiore è richiesto. `
Trova: build $build. Scarica l'ultimo aggiornamento o passa a Windows 11."
    }
    Write-Status "  Windows build $build: OK"

    # 2. WSL2
    $wslInstalled = $false
    try {
        $wslCheck = wsl --status 2>&1
        $wslInstalled = ($LASTEXITCODE -eq 0)
    } catch {
        $wslInstalled = $false
    }

    if (-not $wslInstalled) {
        Write-Status "  WSL2 non installato: abilitazione in corso ..."
        try {
            Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Windows-Subsystem-Linux -NoRestart -ErrorAction Stop
        } catch {
            Write-Warn "Enable-WindowsOptionalFeature non ha funzionato: provo con dism.exe ..."
            dism /online /enable-feature /featurename:Microsoft-Windows-Subsystem-Linux /all /norestart
        }
    }

    # 3. Versione WSL (deve essere 2 di default)
    $wslDefaultVersion = wsl --set-default-version 2 2>&1
    $wslDefaultVersion = $wslDefaultVersion | Select-String "default version"

    # Verifica che il kernel sia aggiornato
    try {
        wsl --update 2>&1 | Out-Null
    } catch {
        Write-Warn "Impossibile aggiornare il kernel WSL (fuori dai prerequisiti minimi, ignorato)."
    }

    Write-Status "  WSL2: OK"

    # 4. Virtualizzazione
    $hv = Get-ComputerInfo | Select-Object -ExpandProperty HyperVisorPresent
    if (-not $hv) {
        Write-Warn "La virtualizzazione non risulta attiva nel BIOS/UEFI. `
WSL2 potrebbe non funzionare. Verifica nel BIOS di abilitare VT-x/AMD-V."
    } else {
        Write-Status "  Virtualizzazione: OK"
    }

    # 5. Abilita systemd (WSL2, richiede build ≥ 22621)
    $wslConfPath = Join-Path $env:USERPROFILE '.wslconfig'
    $needsSystemd = $false
    $wslConfContent = ''

    if (Test-Path $wslConfPath) {
        $wslConfContent = Get-Content $wslConfPath -Raw
    }

    if ($wslConfContent -notmatch '\[wsl\]|features') {
        $needsSystemd = $true
    } elseif ($wslConfContent -match '\[wsl\]' -and $wslConfContent -notmatch 'systemd\s*=\s*(true|1)') {
        $needsSystemd = $true
    }

    if ($needsSystemd) {
        Write-Status "  Abilitazione di systemd in .wslconfig ..."
        $wslConfBlock = "`n[wsl2]`n systemd=true`n"
        if ($wslConfContent.Trim()) {
            # Aggiungi prima della sezione [wsl] se esiste, altrimenti alla fine
            if ($wslConfContent -match '\[wsl\]') {
                $wslConfContent = $wslConfContent -replace '(\[wsl\].*)', "${wslConfBlock}\$1"
            } else {
                $wslConfContent = $wslConfContent.TrimEnd() + $wslConfBlock
            }
        } else {
            $wslConfContent = $wslConfBlock.Trim()
        }
        $wslConfContent | Set-Content $wslConfPath -Encoding UTF8
    } else {
        Write-Status "  systemd già configurato in .wslconfig."
    }

    return $build
}

# ============================================================
# Installazione di Ubuntu e lancio di install.sh
# ============================================================

function Install-UbuntuAndRun {
    param([int]$WindowsBuild)

    Write-Status "Installazione di Ubuntu 24.04 LTS in WSL2 ..."

    # Controlla se Ubuntu è già installato
    $existingDistros = wsl --list --quiet 2>$null
    $ubuntuInstalled = $false
    foreach ($d in $existingDistros) {
        if ($d -match 'ubuntu-24\.04|ubuntu-22\.04|GitStack') {
            $ubuntuInstalled = $true
            Write-Status "  Distro '$d' già installata, la uso."
            $distroName = $d
            break
        }
    }

    if (-not $ubuntuInstalled) {
        try {
            wsl --install -d Ubuntu-24.04
        } catch {
            # Tentativo alternativo con Ubuntu 22.04
            Write-Warn "Ubuntu 24.04 non disponibile, provo con Ubuntu 22.04 ..."
            wsl --install -d Ubuntu-22.04
            $distroName = 'Ubuntu-22.04'
        }
        $distroName = 'Ubuntu-24.04'

        # Attendi che l'installazione termini
        Write-Status "  Attendo il completamento dell'installazione di Ubuntu ..."
        $maxWait = 300  # 5 minuti
        $elapsed = 0
        while ($elapsed -lt $maxWait) {
            try {
                $check = wsl --list --quiet 2>&1
                if ($check -match 'ubuntu') {
                    break
                }
            } catch {}
            Start-Sleep -Seconds 5
            $elapsed += 5
        }

        if ($elapsed -ge $maxWait) {
            Write-Fail "Timeout: Ubuntu non è stato installato entro 5 minuti. `
Rilancia lo script dopo un riavvio del PC se richiesto."
        }
    }

    # Imposta come default
    wsl --set-default $distroName 2>$null | Out-Null

    # Restart WSL per applicare systemd
    Write-Status "  Riavvio di WSL per abilitare systemd ..."
    wsl --shutdown 2>$null | Out-Null
    Start-Sleep -Seconds 3

    # Lancia install.sh dentro WSL
    Write-Status "  Lancio deploy/install.sh dentro WSL2 ..."
    $installScript = Join-Path $PSScriptRoot '../install.sh'
    $deployDir = Join-Path $PSScriptRoot '..'

    # Copia il deploy directory dentro WSL se non esiste già
    $wslDeployPath = '/mnt/c/ProgramData/GitStack-deploy'
    $localDeployPath = $deployDir

    # Usa il percorso WSL assoluto del repo
    $repoPath = $PSScriptRoot -replace '^([A-Z]):', '/$1' -replace '\\', '/'

    try {
        # Esegui install.sh in WSL con systemd attivo
        $output = wsl -d $distroName -- bash -c "
            set -e
            echo '==> Install.sh avviato da install-wsl.ps1 (W1/W2)'
            cd '$repoPath'
            echo '==> Directory corrente: ' \$(pwd)
            echo '==> Script install.sh: ' \$(ls -la deploy/install.sh)
            sudo bash deploy/install.sh --skip-preflight
        " 2>&1

        if ($LASTEXITCODE -ne 0 -and $output) {
            Write-Host "`n$($output -join [Environment]::NewLine)" -ForegroundColor Yellow
        }
    } catch {
        Write-Fail "Errore durante l'esecuzione di install.sh in WSL2: $_"
    }

    Write-Status "  install.sh completato (o fallito con errore sopra)."
}

# ============================================================
# Configurazione -ShareOnNetwork (porte 443 e 2222)
# ============================================================

function Enable-ShareOnNetwork {
    param([int]$WindowsBuild)

    if ($WindowsBuild -lt 22621) {
        Write-Fail "-ShareOnNetwork richiede Windows 11 22H2 (build ≥ 22621). `
Trova: build $WindowsBuild. Usa senza -ShareOnNetwork per l'accesso solo dal PC."
    }

    Write-Status "  Configurazione della rete mirrored (ShareOnNetwork) ..."

    # Imposta networkingMode=mirrored in .wslconfig
    $wslConfPath = Join-Path $env:USERPROFILE '.wslconfig'
    $wslConfContent = ''
    if (Test-Path $wslConfPath) {
        $wslConfContent = Get-Content $wslConfPath -Raw
    }

    # Aggiungi la sezione networking se manca
    if ($wslConfContent -notmatch 'networkingMode') {
        $networkingBlock = "`n[experimental]`n networkingMode=mirrored`n"
        if ($wslConfContent.Trim()) {
            $wslConfContent = $wslConfContent.TrimEnd() + $networkingBlock
        } else {
            $wslConfContent = '[experimental]' + $networkingBlock
        }
        $wslConfContent | Set-Content $wslConfPath -Encoding UTF8
        Write-Status "    networkingMode=mirrored aggiunto a .wslconfig."
    }

    # Riavvia WSL per applicare il networking
    Write-Status "    Riavvio di WSL per applicare networkingMode=mirrored ..."
    wsl --shutdown 2>$null | Out-Null
    Start-Sleep -Seconds 3

    # Apre le porte nel firewall di Windows
    foreach ($port in @(443, 2222)) {
        $ruleName = "GitStack-WSL-$port"
        if (-not (Get-NetFirewallRule -Name $ruleName -ErrorAction SilentlyContinue)) {
            Write-Status "    Creazione regola firewall '$ruleName' (porta $port) ..."
            New-NetFirewallRule -Name $ruleName `
                -DisplayName $ruleName `
                -Direction Inbound `
                -Protocol TCP `
                -LocalPort $port `
                -Action Allow `
                -Profile Any
        } else {
            Write-Status "    Regola firewall '$ruleName' già esistente."
        }
    }

    Write-Status "  ShareOnNetwork: configurato."
}

# ============================================================
# Avvio al login (Scheduled Task)
# ============================================================

function Register-LoginTask {
    Write-Status "  Registrazione dell'attività di avvio al login ..."

    $taskName = 'GitStack-WSL-Login'
    $scriptPath = Join-Path $PSScriptRoot 'on-login.ps1'

    # Crea lo script di avvio al login
    $loginScript = @'
# on-login.ps1 — Avvia WSL e GitStack all'accesso di Windows (W1)
param()

$ErrorActionPreference = 'SilentlyContinue'

# Avvia WSL se non è in esecuzione
$wslRunning = wsl --status 2>$null
if ($LASTEXITCODE -ne 0) {
    wsl ~ &>/dev/null
    Start-Sleep -Seconds 5
}

# Controlla se GitStack è già in esecuzione (processo k3s)
$k3sRunning = wsl -l -q 2>$null | Select-String -Pattern 'ubuntu|gitstack'
if ($k3sRunning) {
    wsl -d $k3sRunning[0] -- pgrep -f k3s >$null 2>&1
    if ($LASTEXITCODE -ne 0) {
        # k3s non in esecuzione: avvia install.sh se necessario
        # (il primo avvio l'ha lanciato l'installatore, i successivi no)
        $k3sActive = wsl -d $k3sRunning[0] -- systemctl is-active k3s 2>$null
        if ($LASTEXITCODE -ne 0) {
            wsl -d $k3sRunning[0] -- systemctl start k3s 2>/dev/null
            wsl -d $k3sRunning[0] -- systemctl start gitstack 2>/dev/null
        }
    }
}
'@

    $loginScript | Set-Content $scriptPath -Encoding UTF8

    # Crea l'attività pianificata
    $action = New-ScheduledTaskAction -Execute 'powershell.exe' `
        -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$scriptPath`""

    $trigger = New-ScheduledTaskTrigger -AtLogOn

    $settings = New-ScheduledTaskSettingsSet `
        -StartWhenAvailable `
        -DontStopIfGoingOnBatteries `
        -AllowStartIfOnBatteries `
        -StartWhenAvailable `
        -ExecutionTimeLimit (New-TimeSpan -Minutes 0)

    try {
        Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue
    } catch {}

    Register-ScheduledTask -TaskName $taskName `
        -Action $action `
        -Trigger $trigger `
        -Settings $settings `
        -Description "Avvia WSL e GitStack all'accesso di Windows (W1)" `
        -Force

    Write-Status "  Attività pianificata '$taskName' registrata."
}

# ============================================================
# Main
# ============================================================

Write-Host ""
Write-Host "GitStack WSL2 Installer (W1/W2 — prova/demo)" -ForegroundColor Cyan
Write-Host "Non per la produzione: WSL2 ha limiti di rete e affidabilità (D14 [c_cf5bc915298b97db])." -ForegroundColor Yellow
Write-Host ""

# 1. Verifica prerequisiti
$windowsBuild = Test-Prerequisites

# 2. Avvia l'installazione e lancia install.sh
Install-UbuntuAndRun -WindowsBuild $windowsBuild

# 3. Configurazione ShareOnNetwork (se richiesta)
if ($ShareOnNetwork) {
    Enable-ShareOnNetwork -WindowsBuild $windowsBuild
}

# 4. Registrazione avvio al login
Register-LoginTask

Write-Host ""
Write-Host "Installazione completata." -ForegroundColor Green
Write-Host ""
Write-Host "Accesso:" -ForegroundColor Cyan
if ($ShareOnNetwork) {
    Write-Host "  - Dal PC: https://localhost:443 (oppure https://<IP-WSL>:443)" -ForegroundColor White
    Write-Host "  - SSH:    ssh -p 2222 utente@<IP-WSL>" -ForegroundColor White
    Write-Host "  - Porte 443 e 2222 aperte nel firewall." -ForegroundColor White
} else {
    Write-Host "  - Solo dal PC: https://localhost:443" -ForegroundColor White
}
Write-Host ""
Write-Host "Per disinstallare: .\deploy\windows\install-wsl.ps1 -Uninstall" -ForegroundColor Cyan
Write-Host ""
