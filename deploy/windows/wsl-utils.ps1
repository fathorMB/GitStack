# wsl-utils.ps1 — Funzioni pure per WSL2 (testabili senza wsl reale).
# Dot-sourced da install-wsl.ps1 e dai test.

function Convert-WindowsToWSLPath {
    param([string]$WinPath, [string]$DistroName)

    # Usa wslpath nella distro specificata per una conversione corretta
    $wslPath = wsl -d $DistroName -- wslpath -a "$WinPath" 2>&1
    if ($LASTEXITCODE -eq 0) {
        return $wslPath.Trim()
    }

    # Fallback: E:\ -> /mnt/e/
    $dummy = $WinPath -match '^([A-Z]):\\'
    if ($dummy) {
        $letter = $Matches[1].ToLower()
        return "/mnt/$letter" + $WinPath.Substring(2).Replace('\', '/')
    }

    return $WinPath
}

function Add-GitStackWslconfig {
    param([string]$Content)

    $nwLine = 'networkingMode=mirrored'
    if ($Content -match '\[wsl2\]') {
        $Content = $Content -replace "(\[wsl2\].*?)([^\r\n])", "`$1`n# GitStack`n$nwLine`n`$2"
        if ($Content -notmatch [regex]::Escape($nwLine)) {
            $Content = $Content -replace "(\[wsl2\])", "`$1`n# GitStack`n$nwLine"
        }
    } else {
        $wslBlock = "`n[wsl2]`n# GitStack`n$nwLine"
        if ($Content.Trim()) {
            $Content = $Content.TrimEnd() + $wslBlock
        } else {
            $Content = $wslBlock.Trim()
        }
    }

    return $Content
}

function Remove-GitStackWslconfig {
    param([string]$Content)

    $lines = $Content -split "`r?`n"
    $newLines = @()
    $skip = $false

    foreach ($line in $lines) {
        if ($line -match '^# GitStack') { $skip = $true }
        if ($skip) {
            if ($line -match '^[^\s#]') { $skip = $false }
            continue
        }
        $newLines += $line
    }

    return ($newLines -join "`n").TrimEnd()
}
