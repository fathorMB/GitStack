#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Riporta la VM di test GitStack al checkpoint 'clean' e stampa il suo IP.

.DESCRIPTION
    Da lanciare prima di ogni prova di GIT-9 (installer) e GIT-11 (test
    end-to-end): scarta qualunque installazione fatta sulla VM, ripristina lo
    checkpoint 'clean' creato da new-vm.ps1, riavvia la VM e attende che SSH
    risponda prima di stampare IP e comando pronto.

    L'IP viene letto prima via KVP; se la VM usa la cloud image generica
    (senza hv_kvp_daemon, vedi GIT-25) lo script passa automaticamente al
    fallback MAC -> cache ARP dell'host (Wait-VmIPv4Address in
    lib/common.ps1), sulla stessa interfaccia dello switch passato a
    new-vm.ps1 (-SwitchName, default 'Default Switch').

.EXAMPLE
    .\reset-vm.ps1
.EXAMPLE
    .\reset-vm.ps1 -VmName gitstack-e2e -VmUser gitstack
.EXAMPLE
    .\reset-vm.ps1 -SshPrivateKeyPath "$env:USERPROFILE\.ssh\gitstack_vm"
#>
[CmdletBinding()]
param(
    [string]$VmName = 'gitstack-test-vm',
    [string]$VmUser = 'gitstack',
    # Switch virtuale Hyper-V della VM: deve combaciare con quello passato a new-vm.ps1 (serve al fallback MAC -> ARP per trovare l'interfaccia host giusta).
    [string]$SwitchName = 'Default Switch',
    [string]$CheckpointName = 'clean',
    # Facoltativo: percorso della chiave PRIVATA, usato solo per stampare "ssh -i <percorso> ...". Il contenuto non viene mai letto.
    [string]$SshPrivateKeyPath,
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

    $ip = Wait-VmIPv4Address -VmName $VmName -SwitchName $SwitchName -TimeoutSeconds $BootTimeoutSeconds
    Wait-TcpPort -IpAddress $ip -Port 22 -TimeoutSeconds $SshTimeoutSeconds

    Write-Host ""
    Write-Host "VM '$VmName' ripristinata allo stato 'clean' (sistema base, nessun'altra installazione)."
    Write-Host "IP:  $ip"
    if ($SshPrivateKeyPath) {
        Write-Host "SSH: ssh -i `"$SshPrivateKeyPath`" $VmUser@$ip"
    } else {
        Write-Host "SSH: ssh $VmUser@$ip"
    }
    Write-Host ""
    Write-Host "Da qui: esegui l'installer di GIT-9 via SSH, oppure lancia il test end-to-end di GIT-11 puntandolo a questo IP."
} catch {
    Write-Error $_.Exception.Message
    exit 1
}
