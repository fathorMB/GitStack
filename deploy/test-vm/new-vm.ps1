#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Crea la VM Linux di test riproducibile su Hyper-V per GitStack (GIT-12).

.DESCRIPTION
    Crea una VM Hyper-V Generazione 2 con Ubuntu Server 24.04 LTS (cloud
    image generica .img/qcow2, convertita in VHDX dinamico con un container
    Docker: vedi GIT-25), inizializzata via cloud-init (utente, chiave SSH,
    sudo senza password, nessun pacchetto extra), e al termine cattura uno
    checkpoint 'clean' a cui reset-vm.ps1 puo' tornare prima di ogni prova
    di GIT-9 (installer) e GIT-11 (end-to-end).

    Idempotente: se una VM con lo stesso nome esiste gia', lo script si ferma
    con un messaggio chiaro e non tocca nulla.

    Nessun segreto in questo script o nel repository: la chiave pubblica SSH
    si passa come parametro (-SshPublicKey o -SshPublicKeyPath); lo script non
    accetta ne' usa mai una chiave privata.

    L'IP della VM viene letto prima via KVP (servizi di integrazione
    Hyper-V); l'immagine cloud generica usata qui non ha hv_kvp_daemon,
    quindi lo script passa automaticamente al fallback MAC -> cache ARP
    dell'host (Wait-VmIPv4Address/Resolve-VmIPv4ViaArp in lib/common.ps1).

.EXAMPLE
    .\new-vm.ps1 -SshPublicKeyPath "$env:USERPROFILE\.ssh\id_ed25519.pub"

.EXAMPLE
    .\new-vm.ps1 -SshPublicKeyPath C:\keys\board.pub -SwitchName "GitStack-Lab" -VmName gitstack-e2e

.EXAMPLE
    # Comando del board: VM e cache immagine sul volume con piu' spazio libero.
    .\new-vm.ps1 -SshPublicKeyPath "$env:USERPROFILE\.ssh\id_ed25519.pub" -VmPath F:\HyperV-VMs\gitstack-test-vm -ImageCacheDir F:\HyperV-VMs\image-cache
#>
[CmdletBinding()]
param(
    # Nome della VM in Hyper-V.
    [string]$VmName = 'gitstack-test-vm',

    # Hostname impostato dentro la VM. Se omesso, deriva da -VmName.
    [string]$Hostname,

    # Utente creato da cloud-init: sudo senza password, accesso solo a chiave.
    [string]$VmUser = 'gitstack',

    # Chiave pubblica SSH come testo (alternativa a -SshPublicKeyPath). Mai una chiave privata.
    [string]$SshPublicKey,

    # Percorso del file .pub della chiave pubblica SSH (alternativa a -SshPublicKey).
    [string]$SshPublicKeyPath,

    # Switch virtuale Hyper-V a cui collegare la VM.
    [string]$SwitchName = 'Default Switch',

    [ValidateRange(1, 64)]
    [int]$CpuCount = 4,

    [ValidateRange(1, 1024)]
    [int]$MemoryGB = 8,

    [ValidateRange(10, 2048)]
    [int]$DiskGB = 60,

    # Cartella dove vivono VHDX/ISO della VM. Default: C:\HyperV-VMs\<VmName>.
    # Lo script verifica lo spazio libero su questo volume prima di
    # scaricare/convertire: su un host con poco spazio su C: passa un
    # percorso su un volume piu' capiente, es. F:\HyperV-VMs\<VmName>.
    [string]$VmPath,

    # URL dell'immagine cloud Ubuntu 24.04 LTS generica (.img, qcow2): la
    # cartella 'releases/noble/release/' e' il puntatore mobile mantenuto da
    # Canonical (24.04 = "noble"), aggiornato in-place a ogni point release.
    # Il vecchio formato .vhd.tar.gz Gen2 non e' piu' pubblicato per amd64
    # generico (solo la variante -azure, che ignora il seed NoCloud: vedi
    # GIT-25); l'immagine .img viene convertita in VHDX da questo script.
    # Verifica su https://cloud-images.ubuntu.com/releases/noble/release/ se
    # il nome file e' cambiato.
    [string]$UbuntuImageUrl = 'https://cloud-images.ubuntu.com/releases/noble/release/ubuntu-24.04-server-cloudimg-amd64.img',

    # Cache locale dell'immagine scaricata, fuori dal repository: le esecuzioni successive la riusano
    # se lo SHA256 (verificato a ogni corsa contro SHA256SUMS) combacia ancora.
    [string]$ImageCacheDir = (Join-Path $env:LOCALAPPDATA 'GitStack\test-vm\image-cache'),

    # Immagine Docker (pinnata per digest, non per tag mobile) usata per
    # convertire l'immagine .img (qcow2) in VHDX dinamico: alpine ufficiale,
    # con qemu-img installato al volo dal suo repository apk (vedi
    # Convert-QcowToVhdxDynamic in lib/common.ps1 e il README per i limiti).
    [string]$QemuImgDockerImage = 'alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc',

    # Nome del checkpoint "pulito" creato a fine esecuzione.
    [string]$CheckpointName = 'clean',

    [int]$BootTimeoutSeconds = 300,
    [int]$SshTimeoutSeconds = 180,
    [int]$CloudInitTimeoutSeconds = 600
)

. (Join-Path $PSScriptRoot 'lib\common.ps1')

try {
    Assert-Administrator
    Assert-HyperVAvailable
    Assert-SshClientAvailable

    if (-not $VmPath) { $VmPath = Join-Path 'C:\HyperV-VMs' $VmName }
    $resolvedHostname = if ($Hostname) { ConvertTo-SafeHostname $Hostname } else { ConvertTo-SafeHostname $VmName }
    $sshKey = Resolve-SshPublicKey -SshPublicKey $SshPublicKey -SshPublicKeyPath $SshPublicKeyPath

    Write-Host "==> Controllo idempotenza: '$VmName' non deve esistere gia'"
    if (Get-VM -Name $VmName -ErrorAction SilentlyContinue) {
        Write-Error "La VM '$VmName' esiste gia' in Hyper-V. new-vm.ps1 non la sovrascrive per non perdere lavoro. Usa '.\reset-vm.ps1 -VmName $VmName' per riportarla al checkpoint 'clean', oppure '.\remove-vm.ps1 -VmName $VmName' per eliminarla e poi rilancia new-vm.ps1."
        exit 1
    }
    if (Test-Path -LiteralPath $VmPath) {
        $leftovers = Get-ChildItem -LiteralPath $VmPath -ErrorAction SilentlyContinue
        if ($leftovers) {
            Write-Error "La cartella '$VmPath' esiste gia' e non e' vuota, ma nessuna VM '$VmName' e' registrata in Hyper-V: probabile residuo di un'esecuzione precedente interrotta. Rimuovi la cartella (dopo aver verificato che non serva) oppure passa -VmPath con un percorso diverso, e riprova."
            exit 1
        }
    }

    Write-Host "==> Controllo lo switch virtuale '$SwitchName'"
    if (-not (Get-VMSwitch -Name $SwitchName -ErrorAction SilentlyContinue)) {
        $available = (Get-VMSwitch | Select-Object -ExpandProperty Name) -join ', '
        Write-Error "Lo switch virtuale '$SwitchName' non esiste. Switch disponibili: $available. Passa -SwitchName con uno di questi, oppure creane uno nuovo da Hyper-V Manager (o 'New-VMSwitch') prima di riprovare."
        exit 1
    }

    Write-Host "==> Verifico che Docker sia pronto (conversione qcow2 -> VHDX, GIT-25: qemu-img non e' installato sull'host)"
    Assert-DockerAvailable

    New-Item -ItemType Directory -Path $ImageCacheDir -Force | Out-Null
    New-Item -ItemType Directory -Path $VmPath -Force | Out-Null

    Write-Host "==> Controllo lo spazio libero prima di scaricare/convertire"
    $imageFileName = Split-Path -Leaf $UbuntuImageUrl
    $remoteBytes = Get-RemoteContentLength -Url $UbuntuImageUrl
    # Margine sul solo download (la cache elimina il file vecchio prima di
    # riscaricare: non serve spazio per due copie contemporaneamente).
    $estimatedImageBytes = if ($remoteBytes) { [int64]($remoteBytes * 1.2) } else { 1.5GB }
    Assert-FreeSpace -Path $ImageCacheDir -RequiredBytes $estimatedImageBytes -Purpose 'la cache dell''immagine (-ImageCacheDir)'
    # Il VHDX e' dinamico (parte piccolo) ma il checkpoint 'clean' e le prove
    # dell'installer possono farlo crescere fino a -DiskGB: richiediamo
    # quello spazio con un margine per il seed ISO e i metadati Hyper-V.
    $requiredVmPathBytes = ([int64]$DiskGB + 5) * 1GB
    Assert-FreeSpace -Path $VmPath -RequiredBytes $requiredVmPathBytes -Purpose 'il VHDX e il checkpoint (-VmPath, fino a -DiskGB)'

    $qcow2Path = Join-Path $ImageCacheDir $imageFileName
    Get-CloudImageWithHashVerification -ImageUrl $UbuntuImageUrl -DestinationPath $qcow2Path

    $vhdxPath = Join-Path $VmPath "$VmName.vhdx"
    Write-Host "==> Converto il disco della cloud image in VHDX dinamico (Generazione 2, GPT/UEFI): $vhdxPath"
    Convert-QcowToVhdxDynamic -SourcePath $qcow2Path -DestinationPath $vhdxPath -DockerImage $QemuImgDockerImage

    $targetBytes = [int64]$DiskGB * 1GB
    $currentVhd = Get-VHD -Path $vhdxPath
    if ($targetBytes -lt $currentVhd.Size) {
        throw "-DiskGB $DiskGB GB e' piu' piccolo dell'immagine scaricata ($([math]::Round($currentVhd.Size / 1GB, 1)) GB). Usa un valore piu' alto."
    } elseif ($targetBytes -gt $currentVhd.Size) {
        Write-Host "==> Espando il disco a ${DiskGB} GB (growpart/resizefs di cloud-init useranno lo spazio al primo avvio)"
        Resize-VHD -Path $vhdxPath -SizeBytes $targetBytes
    }

    $templatesDir = Join-Path $PSScriptRoot 'cloud-init'
    $userDataTemplate = Get-Content -LiteralPath (Join-Path $templatesDir 'user-data.yaml.tmpl') -Raw
    $metaDataTemplate = Get-Content -LiteralPath (Join-Path $templatesDir 'meta-data.yaml.tmpl') -Raw
    $userData = $userDataTemplate.Replace('{{VM_HOSTNAME}}', $resolvedHostname).Replace('{{VM_USER}}', $VmUser).Replace('{{SSH_PUBLIC_KEY}}', $sshKey)
    $metaData = $metaDataTemplate.Replace('{{VM_HOSTNAME}}', $resolvedHostname).Replace('{{INSTANCE_ID}}', $VmName)

    $seedIsoPath = Join-Path $VmPath "$VmName-seed.iso"
    Write-Host "==> Genero il seed cloud-init (datasource NoCloud): $seedIsoPath"
    New-CloudInitSeedIso -UserDataContent $userData -MetaDataContent $metaData -DestinationIsoPath $seedIsoPath

    Write-Host "==> Creo la VM '$VmName' (Generazione 2, $CpuCount vCPU, ${MemoryGB} GB RAM, switch '$SwitchName')"
    New-VM -Name $VmName -Generation 2 -MemoryStartupBytes ([int64]$MemoryGB * 1GB) -VHDPath $vhdxPath -Path $VmPath -SwitchName $SwitchName | Out-Null
    Set-VMProcessor -VMName $VmName -Count $CpuCount
    Set-VMMemory -VMName $VmName -DynamicMemoryEnabled $false -StartupBytes ([int64]$MemoryGB * 1GB)
    # Ubuntu Gen2 non si avvia con il template Secure Boot predefinito (Windows-only): lo disattiviamo.
    Set-VMFirmware -VMName $VmName -EnableSecureBoot Off
    # Evita che i checkpoint automatici di Hyper-V interferiscano con lo checkpoint 'clean' nominato.
    Set-VM -VMName $VmName -AutomaticCheckpointsEnabled $false -CheckpointType Standard
    Add-VMDvdDrive -VMName $VmName -Path $seedIsoPath

    Write-Host "==> Avvio la VM per il primo boot e l'inizializzazione di cloud-init"
    Start-VM -Name $VmName

    $ip = Wait-VmIPv4Address -VmName $VmName -SwitchName $SwitchName -TimeoutSeconds $BootTimeoutSeconds
    Wait-TcpPort -IpAddress $ip -Port 22 -TimeoutSeconds $SshTimeoutSeconds
    Wait-CloudInitDone -IpAddress $ip -VmUser $VmUser -TimeoutSeconds $CloudInitTimeoutSeconds

    Write-Host "==> Spengo la VM per catturare uno checkpoint pulito e ripetibile"
    try {
        Stop-VM -Name $VmName -Force -ErrorAction Stop
    } catch {
        Write-Warning "Arresto controllato non riuscito ($($_.Exception.Message)): forzo lo spegnimento (-TurnOff)."
        Stop-VM -Name $VmName -TurnOff -Force
    }

    Write-Host "==> Creo il checkpoint '$CheckpointName'"
    Checkpoint-VM -Name $VmName -SnapshotName $CheckpointName

    Write-Host "==> Riavvio la VM per lasciarla pronta all'uso"
    Start-VM -Name $VmName
    $ip = Wait-VmIPv4Address -VmName $VmName -SwitchName $SwitchName -TimeoutSeconds $BootTimeoutSeconds
    Wait-TcpPort -IpAddress $ip -Port 22 -TimeoutSeconds $SshTimeoutSeconds

    Write-Host ""
    Write-Host "VM '$VmName' creata e pronta. Requisiti minimi (provvisori, da rivedere con M-08): Ubuntu Server 24.04 LTS, $CpuCount vCPU, ${MemoryGB} GB RAM, ${DiskGB} GB disco."
    Write-VmSummary -VmName $VmName -SwitchName $SwitchName -VmUser $VmUser -IpAddress $ip -CpuCount $CpuCount -MemoryGB $MemoryGB -DiskGB $DiskGB
    Write-Host "Checkpoint 'clean' creato: prima di ogni prova di GIT-9/GIT-11 lancia '.\reset-vm.ps1 -VmName $VmName'."
} catch {
    Write-Error $_.Exception.Message
    exit 1
}
