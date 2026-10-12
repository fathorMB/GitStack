<#PSScriptInfo
.VERSION 1.0.0
.AUTHOR GitStack
.DESCRIPTION Installa GitStack su WSL2 per prove, demo, piccoli team (W1, W2).
.CALLOFTEXT Installa GitStack su WSL2 (prova/demo, W1/W2).
.PROJECTNAME GitStack-WSL-Deploy
.TAGS GitStack WSL2 Ubuntu
.REQUIRES powershell 5.1
#>
#requires -RunAsAdministrator

[Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSAvoidUsingPlainTextForPassword', '')]
[Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSAvoidUsingComputerNameHardcoded', '')]
[Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSUseDeclaredVarsMoreThanAssignments', '')]
[CmdletBinding()]
param(
    [switch]$ShareOnNetwork,
    [switch]$Uninstall
)

# Funzioni pure per conversione percorsi e .wslconfig
. (Join-Path $PSScriptRoot 'wsl-utils.ps1')

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Imposta output UTF-8 per wsl --list (che produce UTF-16)
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$env:WSL_UTF8 = '1'

# Nome della distro WSL creata da questo script
$GS_DISTRO_NAME = 'GitStack'

# ============================================================
# Utilita' di logging
# ============================================================

function Write-Status { param([string]$Msg) Write-Host "==> $Msg" }
function Write-Warn  { param([string]$Msg) Write-Warning $Msg }
function Write-Fail  { param([string]$Msg); Write-Error $Msg; exit 1 }

# ============================================================
# Disinstallazione (-Uninstall)
# ============================================================

function Uninstall-GitStackWSL {
    Write-Status 'Disinstallazione di GitStack da WSL2 ...'

    # 1. Rimuovi l'attivita' pianificata
    $taskName = 'GitStack-WSL-Login'
    $task = Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
    if ($task) {
        Write-Status "Rimozione attivita' pianificata $taskName ..."
        Unregister-ScheduledTask -TaskName $taskName -Confirm:$false
    }

    # 2. Rimuovi le regole del firewall (sempre, non solo con -ShareOnNetwork)
    foreach ($port in @(443, 2222)) {
        $ruleName = "GitStack-WSL-$port"
        $rule = Get-NetFirewallRule -Name $ruleName -ErrorAction SilentlyContinue
        if ($rule) {
            Write-Status "Rimozione regola firewall $ruleName ..."
            Remove-NetFirewallRule -Name $ruleName
        }
    }

    # 3. Rimuovi le righe marcate # GitStack da .wslconfig
    $wslconf = Join-Path $env:USERProfile '.wslconfig'
    if (Test-Path $wslconf) {
        $content = Get-Content $wslconf -Raw
        if ($content -match '# GitStack') {
            Write-Status "Rimozione configurazione GitStack da $wslconf ..."
            $newContent = Remove-GitStackWslconfig -Content $content
            if (-not $newContent.Trim()) {
                Remove-Item $wslconf -Force
            } else {
                $newContent | Set-Content $wslconf -Encoding ASCII
            }
        }
    }

    # 4. Rimuovi SOLO la distro GitStack che abbiamo creato
    $gsDistro = wsl -l -q 2>$null | Where-Object { $_ -match $GS_DISTRO_NAME } | Select-Object -First 1
    if ($gsDistro) {
        Write-Status "Rimozione distro $gsDistro creata da GitStack ..."
        wsl --unregister $gsDistro
    }

    Write-Status 'Disinstallazione completata.'
}

if ($Uninstall) {
    Uninstall-GitStackWSL
    exit 0
}

# ============================================================
# Verifica dei prerequisiti di Windows
# ============================================================

function Test-Prerequisites {
    Write-Status 'Verifica dei prerequisiti di Windows ...'

    # 1. Versione di Windows (build >= 19041 per WSL2)
    $os = Get-CimInstance Win32_OperatingSystem
    $build = [int]$os.BuildNumber

    if ($build -lt 19041) {
        Write-Fail "Windows 10 build 19041 (2004) o superiore e' richiesto. Trovata: build ${build}."
    }
    Write-Host "  Windows build ${build}: OK"

    # 2. Verifica VirtualMachinePlatform
    $vmFeature = Get-WindowsOptionalFeature -Online -FeatureName VirtualMachinePlatform -ErrorAction SilentlyContinue
    if ($vmFeature.State -ne 'Enabled') {
        Write-Status "  Abilitazione VirtualMachinePlatform ..."
        Enable-WindowsOptionalFeature -Online -FeatureName VirtualMachinePlatform -NoRestart -ErrorAction Stop
        Write-Fail "VirtualMachinePlatform abilitato: devi riavviare il PC. Riavvia e rilancia questo script."
    }
    Write-Host '  VirtualMachinePlatform: OK'

    # 3. WSL
    $wslFeature = Get-WindowsOptionalFeature -Online -FeatureName Microsoft-Windows-Subsystem-Linux -ErrorAction SilentlyContinue
    if ($wslFeature.State -ne 'Enabled') {
        Write-Status "  WSL non installato: abilitazione ..."
        Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Windows-Subsystem-Linux -NoRestart -ErrorAction Stop
        Write-Fail "WSL abilitato: devi riavviare il PC. Riavvia e rilancia questo script."
    }
    Write-Host '  WSL: OK'

    # 4. Abilita WSL2 come default
    try {
        wsl --set-default-version 2 2>&1 | Out-Null
    } catch {
        Write-Warn "Impossibile impostare WSL2 come default."
    }

    # 5. Aggiorna il kernel WSL (controlla exit code, non try/catch)
    try {
        wsl --update 2>&1 | Out-Null
        if ($LASTEXITCODE -ne 0) {
            Write-Warn "Impossibile aggiornare il kernel WSL (ignorato)."
        }
    } catch {
        Write-Warn "Errore nell'aggiornamento del kernel WSL (ignorato)."
    }

    # 6. Virtualizzazione
    $hv = Get-CimInstance Win32_ComputerSystem | Select-Object -ExpandProperty HypervisorPresent
    if ($hv) {
        Write-Host '  Virtualizzazione: rilevata OK'
    } else {
        Write-Warn 'La virtualizzazione non risulta attiva nel BIOS/UEFI. WSL2 potrebbe non funzionare.'
    }

    return $build
}

# ============================================================
# Configurazione systemd (dentro la distro, /etc/wsl.conf)
# ============================================================

function Enable-SystemdInDistro {
    param([string]$DistroName)

    Write-Status '  Abilitazione systemd in /etc/wsl.conf ...'
    $result = wsl -d $DistroName -u root -- bash -c "
        if [ -f /etc/wsl.conf ] && grep -q '^systemd' /etc/wsl.conf; then
            echo 'systemd gia configurato'
        else
            printf '[boot]\nsystemd=true\n' > /etc/wsl.conf
            echo 'wsl.conf configurato'
        fi
    " 2>&1
    Write-Status "    $result"

    # Riavvia WSL per applicare
    Write-Status '  Riavvio di WSL per abilitare systemd ...'
    wsl --shutdown 2>$null | Out-Null
    Start-Sleep -Seconds 3
}

# ============================================================
# Configurazione -ShareOnNetwork (PRIMA di install.sh)
# ============================================================

function Enable-ShareOnNetwork {
    param([int]$WindowsBuild)

    if ($WindowsBuild -lt 22621) {
        Write-Fail "-ShareOnNetwork richiede Windows 11 22H2 (build >= 22621). Trovata: build ${WindowsBuild}."
    }

    Write-Status '  Configurazione della rete mirrored (ShareOnNetwork) ...'

    # networkingMode=mirrored va nella sezione [wsl2] di .wslconfig
    $wslConfPath = Join-Path $env:USERProfile '.wslconfig'
    $wslConfContent = ''
    if (Test-Path $wslConfPath) {
        $wslConfContent = Get-Content $wslConfPath -Raw
    }

    # Controlla se networkingMode e' gia' presente con un altro valore
    if ($wslConfContent -match 'networkingMode\s*=\s*(\S+)') {
        $currentValue = $matches[1]
        if ($currentValue -ne 'mirrored') {
            Write-Fail "networkingMode e' gia' impostato a $currentValue in .wslconfig. Cambia il valore in mirrored e rilancia."
        }
    } else {
        # Aggiungi networkingMode nella sezione [wsl2] o crea la sezione
        $nwLine = 'networkingMode=mirrored # GitStack'
        if ($wslConfContent -match '\[wsl2\]') {
            $wslConfContent = $wslConfContent -replace "(\[wsl2\].*?)([^\r\n])", "`$1`n# GitStack`n$nwLine`n`$2"
            if ($wslConfContent -notmatch [regex]::Escape($nwLine)) {
                $wslConfContent = $wslConfContent -replace "(\[wsl2\])", "`$1`n# GitStack`n$nwLine"
            }
        } else {
            $wslBlock = "`n[wsl2]`n# GitStack`n$nwLine"
            if ($wslConfContent.Trim()) {
                $wslConfContent = $wslConfContent.TrimEnd() + $wslBlock
            } else {
                $wslConfContent = '[wsl2]' + $wslBlock.Trim()
            }
        }
        $wslConfContent | Set-Content $wslConfPath -Encoding ASCII
        Write-Status '    networkingMode=mirrored aggiunto a .wslconfig.'
    }

    # Riavvia WSL per applicare il networking
    Write-Status '    Riavvio di WSL per applicare networkingMode=mirrored ...'
    wsl --shutdown 2>$null | Out-Null
    Start-Sleep -Seconds 3

    # Apre le porte nel firewall di Windows
    foreach ($port in @(443, 2222)) {
        $ruleName = "GitStack-WSL-$port"
        if (-not (Get-NetFirewallRule -Name $ruleName -ErrorAction SilentlyContinue)) {
            Write-Status "    Creazione regola firewall $ruleName (porta $port) ..."
            New-NetFirewallRule -Name $ruleName `
                -DisplayName $ruleName `
                -Direction Inbound `
                -Protocol TCP `
                -LocalPort $port `
                -Action Allow `
                -Profile Any
        } else {
            Write-Status "    Regola firewall $ruleName gia' esistente."
        }
    }

    Write-Status '  ShareOnNetwork: configurato.'
}

# ============================================================
# Installazione di Ubuntu e lancio di install.sh
# ============================================================

function Install-UbuntuAndRun {
    param([int]$WindowsBuild, [bool]$DoShareOnNetwork)

    Write-Status 'Installazione di Ubuntu in WSL2 ...'

    # Controlla se la distro GitStack e' gia' installata
    $existingDistro = wsl -l -q 2>$null | Where-Object { $_ -match $GS_DISTRO_NAME } | Select-Object -First 1
    $distroName = ''

    if ($existingDistro) {
        $distroName = $existingDistro
        Write-Host "  Distro ${distroName} gia' installata, la uso."
    }

    if (-not $distroName) {
        # Installa con --name GitStack --no-launch
        Write-Status "  Installazione Ubuntu 24.04 con nome $GS_DISTRO_NAME ..."

        $installOk = $false

        # Tentativo con Ubuntu 24.04
        try {
            wsl --install -d Ubuntu-24.04 --name $GS_DISTRO_NAME --no-launch 2>&1 | Out-Null
            if ($LASTEXITCODE -eq 0) {
                $distroName = $GS_DISTRO_NAME
                $installOk = $true
            }
        } catch {
            # Continua al fallback
        }

        if (-not $installOk) {
            # Fallback: Ubuntu 22.04
            Write-Warn "Ubuntu 24.04 non disponibile, provo con Ubuntu 22.04 ..."
            try {
                wsl --install -d Ubuntu-22.04 --name $GS_DISTRO_NAME --no-launch 2>&1 | Out-Null
                if ($LASTEXITCODE -eq 0) {
                    $distroName = $GS_DISTRO_NAME
                    $installOk = $true
                }
            } catch {
                # Continua
            }
        }

        if (-not $installOk) {
            Write-Fail "Impossibile installare la distro $GS_DISTRO_NAME. Verifica che wsl sia installato e riprova."
        }

        Write-Host "  Distro ${distroName} installata (senza lancio interattivo)."

        # Imposta come default
        wsl --set-default $distroName 2>$null | Out-Null
    }

    # Abilita systemd
    Enable-SystemdInDistro -DistroName $distroName

    # Se -ShareOnNetwork, applica PRIMA di install.sh
    if ($DoShareOnNetwork) {
        Enable-ShareOnNetwork -WindowsBuild $WindowsBuild
    }

    # Risolvi la radice del repo (.. da deploy\windows), poi converti in formato WSL
    $repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
    $wslRepoPath = Convert-WindowsToWSLPath -WinPath $repoRoot -DistroName $distroName

    # Lancia install.sh dentro WSL
    Write-Status "  Lancio deploy/install.sh dentro WSL2 (distro: $distroName) ..."
    try {
        $output = wsl -d $distroName -- bash -c "
            set -e
            echo '==> install.sh avviato da install-wsl.ps1 (W1/W2)'
            cd '$wslRepoPath'
            echo '==> Directory corrente: ' \$(pwd)
            echo '==> Script install.sh: ' \$(ls -la deploy/install.sh)
            sudo bash deploy/install.sh --skip-preflight
        " 2>&1

        if ($LASTEXITCODE -ne 0) {
            Write-Fail "install.sh e' uscito con codice ${LASTEXITCODE}. Output:`n$(($output -join [Environment]::NewLine))"
        }

        Write-Status '  install.sh completato con successo.'
    } catch {
        Write-Fail "Errore durante l'esecuzione di install.sh in WSL2: $_"
    }
}

# ============================================================
# Avvio al login (Scheduled Task)
# ============================================================

function Register-LoginTask {
    param([string]$DistroName)

    Write-Status '  Registrazione dell''attivita'' di avvio al login ...'

    $taskName = 'GitStack-WSL-Login'
    $scriptPath = Join-Path $PSScriptRoot 'on-login.ps1'

    # Usa il file committato on-login.ps1 (non lo riscriviamo qui)
    if (-not (Test-Path $scriptPath)) {
        Write-Warn "on-login.ps1 non trovato in $scriptPath."
        return
    }

    $action = New-ScheduledTaskAction -Execute 'powershell.exe' `
        -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$scriptPath`""

    $trigger = New-ScheduledTaskTrigger -AtLogOn

    $settings = New-ScheduledTaskSettingsSet `
        -DontStopIfGoingOnBatteries `
        -AllowStartIfOnBatteries `
        -ExecutionTimeLimit (New-TimeSpan -Minutes 0) `
        -StartWhenAvailable

    try {
        Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue
    } catch {}

    Register-ScheduledTask -TaskName $taskName `
        -Action $action `
        -Trigger $trigger `
        -Settings $settings `
        -Description 'Avvia WSL e GitStack all''accesso di Windows (W1)' `
        -Force

    Write-Status "  Attivita' pianificata $taskName registrata."
}

# ============================================================
# Main
# ============================================================

Write-Host ''
Write-Host 'GitStack WSL2 Installer (W1/W2 -- prova/demo)' -ForegroundColor Cyan
Write-Host 'NON per la produzione: WSL2 ha limiti di rete e affidabilita' -ForegroundColor Yellow
Write-Host '(riferimento D14 [c_cf5bc915298b97db]).' -ForegroundColor Yellow
Write-Host ''

# 1. Verifica prerequisiti
$windowsBuild = Test-Prerequisites

# 2. Installa Ubuntu, abilita systemd, configura ShareOnNetwork PRIMA, poi lancia install.sh
Install-UbuntuAndRun -WindowsBuild $windowsBuild -DoShareOnNetwork ($ShareOnNetwork.IsPresent)

# 3. Registrazione avvio al login
$foundDistro = wsl -l -q 2>$null | Where-Object { $_ -match $GS_DISTRO_NAME } | Select-Object -First 1
Register-LoginTask -DistroName $foundDistro

Write-Host ''
Write-Host 'Installazione completata.' -ForegroundColor Green
Write-Host ''
Write-Host 'Accesso:' -ForegroundColor Cyan
if ($ShareOnNetwork) {
    Write-Host '  - Dal PC: https://localhost:443 (oppure https://<IP-del-PC>:443)' -ForegroundColor White
    Write-Host '  - SSH:    ssh -p 2222 utente@<IP-del-PC>' -ForegroundColor White
    Write-Host '  - Porte 443 e 2222 aperte nel firewall.' -ForegroundColor White
} else {
    Write-Host '  - Solo dal PC: https://localhost:443' -ForegroundColor White
}
Write-Host ''
Write-Host 'Per disinstallare: .\deploy\windows\install-wsl.ps1 -Uninstall' -ForegroundColor Cyan
Write-Host ''
