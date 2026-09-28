#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Riporta la VM di test GitStack al checkpoint 'clean' e stampa il suo IP.

.DESCRIPTION
    Da lanciare prima di ogni prova di GIT-9 (installer) e GIT-11 (test
    end-to-end): scarta qualunque installazione fatta sulla VM, ripristina lo
    checkpoint 'clean' creato da new-vm.ps1, riavvia la VM e attende che SSH
    risponda prima di stampare IP e comando pronto.

.EXAMPLE
    .\reset-vm.ps1
.EXAMPLE
    .\reset-vm.ps1 -VmName gitstack-e2e -VmUser gitstack
#>
[CmdletBinding()]
param(
    [string]$VmName = 'gitstack-test-vm',
    [string]$VmUser = 'gitstack',
    [string]$CheckpointName = 'clean',
    [int]$BootTimeoutSeconds = 300,
    [int]$SshTimeoutSeconds = 180
)

. (Join-Path $PSScriptRoot 'lib\common.ps1')

try {
    Assert-Administrator
    Assert-HyperVAvailable

    $vm = Get-VM -Name $VmName -ErrorAction SilentlyContinue
    if (-not $vm) {
        Write-Error "La VM '$VmName' non esiste in Hyper-V. Crea prima la VM con '.\new-vm.ps1' (vedi README.md)."
        exit 1
    }

    $checkpoint = Get-VMSnapshot -VMName $VmName -Name $CheckpointName -ErrorAction SilentlyContinue
    if (-not $checkpoint) {
        $available = (Get-VMSnapshot -VMName $VmName | Select-Object -ExpandProperty Name) -join ', '
        if (-not $available) { $available = '(nessuno)' }
        Write-Error "Nessun checkpoint '$CheckpointName' su '$VmName'. Checkpoint disponibili: $available. Se la VM non e' stata creata con new-vm.ps1 (o lo checkpoint e' stato cancellato), ricrea la VM con '.\remove-vm.ps1' seguito da '.\new-vm.ps1'."
        exit 1
    }

    Write-Host "==> Spengo '$VmName' prima del ripristino (lo stato corrente viene scartato)"
    if ($vm.State -ne 'Off') {
        Stop-VM -Name $VmName -TurnOff -Force
    }

    Write-Host "==> Ripristino il checkpoint '$CheckpointName'"
    Restore-VMSnapshot -Name $CheckpointName -VMName $VmName -Confirm:$false

    Write-Host "==> Riavvio la VM"
    Start-VM -Name $VmName

    $ip = Wait-VmIPv4Address -VmName $VmName -TimeoutSeconds $BootTimeoutSeconds
    Wait-TcpPort -IpAddress $ip -Port 22 -TimeoutSeconds $SshTimeoutSeconds

    Write-Host ""
    Write-Host "VM '$VmName' ripristinata allo stato 'clean' (sistema base, nessun'altra installazione)."
    Write-Host "IP:  $ip"
    Write-Host "SSH: ssh $VmUser@$ip"
    Write-Host ""
    Write-Host "Da qui: esegui l'installer di GIT-9 via SSH, oppure lancia il test end-to-end di GIT-11 puntandolo a questo IP."
} catch {
    Write-Error $_.Exception.Message
    exit 1
}
