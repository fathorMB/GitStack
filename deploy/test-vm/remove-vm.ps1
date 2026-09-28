#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Distrugge la VM di test GitStack e i suoi file (VHDX, ISO seed, checkpoint).

.DESCRIPTION
    Rimuove la VM da Hyper-V con tutti i suoi checkpoint, poi elimina la
    cartella con VHDX e ISO seed. Se la VM non esiste, lo script lo segnala e
    esce senza errore (comodo da rilanciare). Chiede conferma a meno di
    passare -Force.

.EXAMPLE
    .\remove-vm.ps1
.EXAMPLE
    .\remove-vm.ps1 -VmName gitstack-e2e -Force
#>
[CmdletBinding()]
param(
    [string]$VmName = 'gitstack-test-vm',
    [switch]$Force
)

. (Join-Path $PSScriptRoot 'lib\common.ps1')

try {
    Assert-Administrator
    Assert-HyperVAvailable

    $vm = Get-VM -Name $VmName -ErrorAction SilentlyContinue
    if (-not $vm) {
        Write-Host "La VM '$VmName' non esiste in Hyper-V: niente da rimuovere."
        exit 0
    }

    # Raccogli i percorsi dei file prima di eliminare l'oggetto VM.
    $vmConfigPath = $vm.ConfigurationLocation
    $hardDiskPaths = (Get-VMHardDiskDrive -VMName $VmName | Select-Object -ExpandProperty Path) 2> $null
    $dvdPaths = (Get-VMDvdDrive -VMName $VmName | Where-Object { $_.Path } | Select-Object -ExpandProperty Path) 2> $null

    if (-not $Force) {
        $answer = Read-Host "Rimuovere definitivamente la VM '$VmName' (dischi, ISO seed e checkpoint inclusi)? Digita 'si' per confermare"
        if ($answer -notin @('si', 'sì', 's', 'y', 'yes')) {
            Write-Host "Annullato: nessuna modifica effettuata."
            exit 0
        }
    }

    if ($vm.State -ne 'Off') {
        Write-Host "==> Spengo '$VmName'"
        Stop-VM -Name $VmName -TurnOff -Force
    }

    $snapshots = Get-VMSnapshot -VMName $VmName -ErrorAction SilentlyContinue
    if ($snapshots) {
        Write-Host "==> Rimuovo $($snapshots.Count) checkpoint"
        $snapshots | Remove-VMSnapshot -ErrorAction SilentlyContinue
        # Attende che Hyper-V finisca la fusione dei dischi differenziali dei checkpoint.
        $deadline = (Get-Date).AddMinutes(5)
        while ((Get-VM -Name $VmName).Status -eq 'Merging disks' -and (Get-Date) -lt $deadline) {
            Start-Sleep -Seconds 5
        }
    }

    Write-Host "==> Rimuovo la VM '$VmName' da Hyper-V"
    Remove-VM -Name $VmName -Force

    # Subito dopo Remove-VM, vmms/vmwp possono tenere il lock su VHDX/ISO/cartella
    # ancora per qualche secondo: Remove-ItemWithRetry ritenta fino a 30s (ogni 3s)
    # prima di arrendersi, invece di ignorare l'errore in silenzio.
    $remainingPaths = @()

    $filesToRemove = @($hardDiskPaths + $dvdPaths) | Where-Object { $_ -and (Test-Path -LiteralPath $_) }
    foreach ($file in $filesToRemove) {
        Write-Host "==> Elimino $file"
        if (-not (Remove-ItemWithRetry -Path $file -TimeoutSeconds 30 -RetryIntervalSeconds 3)) {
            $remainingPaths += $file
        }
    }
    if ($vmConfigPath -and (Test-Path -LiteralPath $vmConfigPath)) {
        Write-Host "==> Elimino $vmConfigPath"
        if (-not (Remove-ItemWithRetry -Path $vmConfigPath -Recurse -TimeoutSeconds 30 -RetryIntervalSeconds 3)) {
            $remainingPaths += $vmConfigPath
        }
    }

    Write-Host ""
    if ($remainingPaths) {
        Write-Warning "VM '$VmName' rimossa da Hyper-V, ma alcuni percorsi sono rimasti su disco (probabilmente ancora bloccati da un processo Hyper-V, es. vmms/vmwp):"
        foreach ($p in $remainingPaths) { Write-Warning "  - $p" }
        Write-Warning "Riprova tra qualche secondo a mano, per esempio:"
        foreach ($p in $remainingPaths) { Write-Warning "  Remove-Item -LiteralPath '$p' -Recurse -Force" }
        exit 1
    }
    Write-Host "VM '$VmName' rimossa, VHDX/ISO/cartella di configurazione eliminati. Per ricrearla: '.\new-vm.ps1' (vedi README.md)."
} catch {
    Write-Error $_.Exception.Message
    exit 1
}
