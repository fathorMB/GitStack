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
    # Cartella "della VM" (-VmPath di new-vm.ps1): il padre comune di VHDX/ISO,
    # es. 'F:\HyperV-VMs\gitstack-test-vm' se il disco e' '...\gitstack-test-vm\gitstack-test-vm.vhdx'.
    # E' distinta da $vmConfigPath ('...\gitstack-test-vm\gitstack-test-vm', la
    # sottocartella che New-VM -Path crea per la configurazione).
    $diskParentPaths = @($hardDiskPaths + $dvdPaths) | Where-Object { $_ } | ForEach-Object { Split-Path -Parent $_ } | Select-Object -Unique
    # Cartella predefinita dell'host per le VM create senza -Path, condivisa da
    # TUTTE le VM: mai cancellarla (ne' un suo antenato) con -Recurse, vedi
    # Test-VmOwnedFolder in lib/common.ps1.
    $hostVmPath = (Get-VMHost).VirtualMachinePath

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
        if (Test-VmOwnedFolder -Path $vmConfigPath -VmName $VmName -HostVirtualMachinePath $hostVmPath) {
            Write-Host "==> Elimino $vmConfigPath"
            if (-not (Remove-ItemWithRetry -Path $vmConfigPath -Recurse -TimeoutSeconds 30 -RetryIntervalSeconds 3)) {
                $remainingPaths += $vmConfigPath
            }
        } else {
            # Non e' (in modo dimostrabile) una cartella dedicata solo a questa
            # VM: puo' essere la cartella condivisa dell'host per le VM create
            # senza -Path, o un suo antenato. Mai -Recurse alla cieca: si
            # rimuove solo se gia' vuota (comportamento precedente a GIT-27),
            # altrimenti si avvisa e basta.
            $remaining = Get-ChildItem -LiteralPath $vmConfigPath -Recurse -ErrorAction SilentlyContinue
            if (-not $remaining) {
                Write-Host "==> Elimino $vmConfigPath (vuota)"
                if (-not (Remove-ItemWithRetry -Path $vmConfigPath -TimeoutSeconds 30 -RetryIntervalSeconds 3)) {
                    $remainingPaths += $vmConfigPath
                }
            } else {
                Write-Warning "'$vmConfigPath' non ha il nome finale uguale a '$VmName', oppure coincide con la cartella condivisa dell'host (Get-VMHost).VirtualMachinePath ('$hostVmPath') o un suo antenato, e non e' vuota: non la rimuovo con -Recurse per non cancellare file di altre VM. Ripulisci a mano solo i file di '$VmName' al suo interno."
                $remainingPaths += $vmConfigPath
            }
        }
    }

    # Cartella "della VM" (-VmPath di new-vm.ps1, padre di VHDX/ISO): a questo
    # punto dovrebbe essere vuota (file e sottocartella di configurazione gia'
    # rimossi sopra). Stessa guardia di $vmConfigPath: rimossa solo se dedicata
    # a questa VM, e solo se davvero vuota (mai -Recurse su una cartella che
    # contiene altro).
    foreach ($diskParent in $diskParentPaths) {
        if (-not $diskParent -or -not (Test-Path -LiteralPath $diskParent)) { continue }
        if ($diskParent -ieq $vmConfigPath) { continue } # gia' gestita sopra
        if (-not (Test-VmOwnedFolder -Path $diskParent -VmName $VmName -HostVirtualMachinePath $hostVmPath)) {
            continue # non dimostrabilmente dedicata a questa VM: non la tocco
        }
        $leftovers = Get-ChildItem -LiteralPath $diskParent -ErrorAction SilentlyContinue
        if ($leftovers) {
            Write-Warning "La cartella della VM '$diskParent' contiene ancora altri file/cartelle (elencati sotto): non la rimuovo con -Recurse."
            $leftovers | ForEach-Object { Write-Warning "    $($_.FullName)" }
            $remainingPaths += $diskParent
            continue
        }
        Write-Host "==> Elimino $diskParent (vuota)"
        if (-not (Remove-ItemWithRetry -Path $diskParent -TimeoutSeconds 30 -RetryIntervalSeconds 3)) {
            $remainingPaths += $diskParent
        }
    }

    Write-Host ""
    if ($remainingPaths) {
        Write-Warning "VM '$VmName' rimossa da Hyper-V, ma alcuni percorsi sono rimasti su disco (bloccati da un processo Hyper-V come vmms/vmwp oltre i tentativi, o non rimossi per sicurezza: vedi gli avvisi sopra per il motivo di ciascuno):"
        foreach ($p in $remainingPaths) { Write-Warning "  - $p" }
        Write-Warning "Quando non sono piu' bloccati e ne hai verificato il contenuto, rimuovili a mano, per esempio:"
        foreach ($p in $remainingPaths) { Write-Warning "  Remove-Item -LiteralPath '$p' -Recurse -Force" }
        exit 1
    }
    Write-Host "VM '$VmName' rimossa, VHDX/ISO/cartella di configurazione eliminati. Per ricrearla: '.\new-vm.ps1' (vedi README.md)."
} catch {
    Write-Error $_.Exception.Message
    exit 1
}
