# deploy/test-vm/lib/common.ps1
#
# Funzioni condivise da new-vm.ps1, reset-vm.ps1 e remove-vm.ps1.
# Non e' pensato per essere eseguito direttamente: viene incluso con dot-sourcing
# (". $PSScriptRoot\lib\common.ps1") dagli altri script di questa cartella.

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Assert-Administrator {
    <# Ferma lo script con un messaggio chiaro se non gira come amministratore. #>
    $current = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($current)
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw "Questo script va eseguito da PowerShell 'come amministratore': Hyper-V richiede diritti elevati. Apri PowerShell con 'Esegui come amministratore' e rilancia il comando."
    }
}

function Assert-HyperVAvailable {
    <# Ferma lo script con un messaggio chiaro se il modulo Hyper-V non e' disponibile. #>
    if (-not (Get-Command Get-VM -ErrorAction SilentlyContinue)) {
        throw "Il modulo PowerShell di Hyper-V non e' disponibile. Attiva la funzionalita' di Windows 'Hyper-V' (Pannello di controllo > Programmi > Attiva/disattiva funzionalita' di Windows > Hyper-V, oppure 'Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V -All'), riavvia e riprova."
    }
}

function Assert-SshClientAvailable {
    <# Ferma lo script con un messaggio chiaro se il client OpenSSH non e' disponibile. #>
    if (-not (Get-Command ssh -ErrorAction SilentlyContinue)) {
        throw "Il client OpenSSH ('ssh') non e' nel PATH. Su Windows 11 e' una funzionalita' opzionale: Impostazioni > App > Funzionalita' opzionali > Aggiungi una funzionalita' > 'Client OpenSSH'."
    }
}

function Resolve-SshPublicKey {
    <# Legge e valida la chiave pubblica SSH da parametro inline o da file. Non accetta mai chiavi private. #>
    param(
        [string]$SshPublicKey,
        [string]$SshPublicKeyPath
    )
    $key = $null
    if ($SshPublicKey) {
        $key = $SshPublicKey.Trim()
    } elseif ($SshPublicKeyPath) {
        if (-not (Test-Path -LiteralPath $SshPublicKeyPath)) {
            throw "File della chiave pubblica non trovato: $SshPublicKeyPath"
        }
        $key = (Get-Content -LiteralPath $SshPublicKeyPath -Raw).Trim()
    } else {
        throw "Specifica la chiave pubblica SSH con -SshPublicKeyPath <percorso file .pub> oppure -SshPublicKey '<contenuto>'. Nessuna chiave privata va mai passata a questo script."
    }
    if ($key -match 'PRIVATE KEY') {
        throw "Il valore passato sembra una chiave PRIVATA, non pubblica. Passa il file .pub (o il suo contenuto), mai la chiave privata."
    }
    if ($key -notmatch '^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp256|ecdsa-sha2-nistp384|ecdsa-sha2-nistp521)\s+\S+') {
        throw "La chiave pubblica non sembra in formato OpenSSH valido (deve iniziare con ssh-ed25519, ssh-rsa o ecdsa-sha2-...)."
    }
    if ($key -match "`r|`n") {
        throw "La chiave pubblica deve stare su una sola riga (niente ritorni a capo)."
    }
    return $key
}

function ConvertTo-SafeHostname {
    <# Normalizza un nome VM in un hostname valido: minuscolo, solo [a-z0-9-]. #>
    param([Parameter(Mandatory)][string]$Name)
    $safe = $Name.ToLowerInvariant() -replace '[^a-z0-9-]', '-'
    $safe = $safe.Trim('-')
    if (-not $safe) { $safe = 'gitstack-test-vm' }
    return $safe
}

# --- Creazione dell'ISO seed cloud-init (datasource NoCloud), senza strumenti esterni ---
# Usa la COM IMAPI2FS inclusa in Windows (la stessa usata per masterizzare CD/DVD).

if (-not ([System.Management.Automation.PSTypeName]'GitStackTestVm.IsoFileWriter').Type) {
    Add-Type -TypeDefinition @"
using System;
using System.IO;
using System.Runtime.InteropServices;
using System.Runtime.InteropServices.ComTypes;
namespace GitStackTestVm {
    public class IsoFileWriter {
        // Copia lo stream COM (IStream) prodotto da IMAPI2FS su un file .iso.
        // Marshal.AllocHGlobal invece di puntatori "unsafe": nessuna opzione
        // di compilazione speciale richiesta.
        public static void Create(string path, object streamObj, int blockSize, long totalBlocks) {
            byte[] buf = new byte[blockSize];
            IntPtr bytesReadPtr = Marshal.AllocHGlobal(4);
            try {
                using (FileStream o = File.Create(path)) {
                    IStream i = streamObj as IStream;
                    if (i == null) {
                        throw new InvalidOperationException("Lo stream immagine ISO non e' un IStream COM valido.");
                    }
                    while (totalBlocks-- > 0) {
                        i.Read(buf, blockSize, bytesReadPtr);
                        int bytesRead = Marshal.ReadInt32(bytesReadPtr);
                        if (bytesRead <= 0) { break; }
                        o.Write(buf, 0, bytesRead);
                    }
                    o.Flush();
                }
            } finally {
                Marshal.FreeHGlobal(bytesReadPtr);
            }
        }
    }
}
"@
}

function New-CloudInitSeedIso {
    <#
      Crea un'immagine ISO9660 con etichetta 'cidata' contenente user-data e
      meta-data, per il datasource NoCloud di cloud-init. Nessuno strumento
      esterno richiesto (ne' oscdimg ne' genisoimage).
    #>
    param(
        [Parameter(Mandatory)][string]$UserDataContent,
        [Parameter(Mandatory)][string]$MetaDataContent,
        [Parameter(Mandatory)][string]$DestinationIsoPath
    )

    $staging = Join-Path ([System.IO.Path]::GetTempPath()) ("gitstack-cidata-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $staging -Force | Out-Null
    try {
        # UTF-8 senza BOM: un BOM in testa allo YAML manda in errore il parser di cloud-init.
        $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
        [System.IO.File]::WriteAllText((Join-Path $staging 'user-data'), $UserDataContent, $utf8NoBom)
        [System.IO.File]::WriteAllText((Join-Path $staging 'meta-data'), $MetaDataContent, $utf8NoBom)

        $fsi = New-Object -ComObject IMAPI2FS.MsftFileSystemImage
        $fsi.VolumeName = 'cidata'
        $fsi.FileSystemsToCreate = 3  # ISO9660 + Joliet
        $fsi.Root.AddTree($staging, $false)
        $result = $fsi.CreateResultImage()

        $destDir = Split-Path -Parent $DestinationIsoPath
        if ($destDir -and -not (Test-Path -LiteralPath $destDir)) {
            New-Item -ItemType Directory -Path $destDir -Force | Out-Null
        }
        if (Test-Path -LiteralPath $DestinationIsoPath) {
            Remove-Item -LiteralPath $DestinationIsoPath -Force
        }
        [GitStackTestVm.IsoFileWriter]::Create($DestinationIsoPath, $result.ImageStream, $result.BlockSize, $result.TotalBlocks)
    } finally {
        Remove-Item -LiteralPath $staging -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Wait-VmIPv4Address {
    <# Attende che Hyper-V riporti un IPv4 per la VM (via KVP/integration services) ed esce in timeout. #>
    param(
        [Parameter(Mandatory)][string]$VmName,
        [int]$TimeoutSeconds = 300,
        [int]$PollSeconds = 5
    )
    Write-Host "    In attesa dell'indirizzo IPv4 di '$VmName' (timeout ${TimeoutSeconds}s)..."
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $addrs = (Get-VMNetworkAdapter -VMName $VmName).IPAddresses |
            Where-Object { $_ -match '^\d{1,3}(\.\d{1,3}){3}$' -and $_ -ne '0.0.0.0' }
        if ($addrs) { return $addrs[0] }
        Start-Sleep -Seconds $PollSeconds
    }
    throw "Timeout in attesa dell'IP di '$VmName'. L'IP viene letto dai servizi di integrazione Hyper-V (scambio dati/KVP): verifica in Hyper-V Manager che la VM sia avviata, che riceva un IP via DHCP dallo switch collegato, e che 'Servizio di scambio dati' sia abilitato ('Get-VMIntegrationService -VMName $VmName'). Se il problema persiste, apri la console della VM da Hyper-V Manager per leggere l'IP con 'ip a'."
}

function Wait-TcpPort {
    <# Attende che una porta TCP risponda (tipicamente 22/SSH) ed esce in timeout. #>
    param(
        [Parameter(Mandatory)][string]$IpAddress,
        [int]$Port = 22,
        [int]$TimeoutSeconds = 180,
        [int]$PollSeconds = 3
    )
    Write-Host "    In attesa che ${IpAddress}:${Port} (SSH) risponda (timeout ${TimeoutSeconds}s)..."
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $test = Test-NetConnection -ComputerName $IpAddress -Port $Port -WarningAction SilentlyContinue
        if ($test.TcpTestSucceeded) { return $true }
        Start-Sleep -Seconds $PollSeconds
    }
    throw "Timeout in attesa che ${IpAddress}:${Port} risponda. Verifica che la VM sia arrivata al prompt di login (console Hyper-V Manager) e che nessun firewall della VM blocchi la porta 22."
}

function Wait-CloudInitDone {
    <#
      Aspetta che cloud-init finisca sulla VM via SSH ('cloud-init status --wait').
      Richiede che la chiave privata corrispondente sia disponibile al client ssh
      del chiamante (ssh-agent o file di identita' predefiniti): questo script
      non riceve mai e non usa mai chiavi private.
    #>
    param(
        [Parameter(Mandatory)][string]$IpAddress,
        [Parameter(Mandatory)][string]$VmUser,
        [int]$TimeoutSeconds = 600
    )
    Write-Host "    In attesa che cloud-init finisca su ${VmUser}@${IpAddress} (timeout ${TimeoutSeconds}s)..."
    $sshArgs = @(
        '-o', 'StrictHostKeyChecking=no',
        '-o', 'UserKnownHostsFile=NUL',
        '-o', 'ConnectTimeout=10',
        "$VmUser@$IpAddress",
        'cloud-init status --wait'
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $lastDetail = ''
    while ((Get-Date) -lt $deadline) {
        $stdoutFile = [System.IO.Path]::GetTempFileName()
        $stderrFile = [System.IO.Path]::GetTempFileName()
        try {
            $proc = Start-Process -FilePath 'ssh' -ArgumentList $sshArgs -NoNewWindow -PassThru -Wait `
                -RedirectStandardOutput $stdoutFile -RedirectStandardError $stderrFile
            if ($proc.ExitCode -eq 0) { return }
            if ($proc.ExitCode -eq 2) {
                Write-Warning "cloud-init ha finito con avvisi (exit 2, 'done with recoverable errors'): continuo, ma controlla /var/log/cloud-init.log sulla VM."
                return
            }
            $lastDetail = "ssh 'cloud-init status --wait' e' uscito con codice $($proc.ExitCode): $(Get-Content -LiteralPath $stderrFile -Raw)"
        } finally {
            Remove-Item -LiteralPath $stdoutFile, $stderrFile -Force -ErrorAction SilentlyContinue
        }
        Start-Sleep -Seconds 5
    }
    throw "Timeout in attesa che cloud-init finisse su ${VmUser}@${IpAddress}. Ultimo tentativo: $lastDetail. Se SSH rifiuta la connessione con la tua chiave, verifica che la chiave privata corrispondente a quella passata a new-vm.ps1 sia caricata nel tuo ssh-agent o nei percorsi predefiniti (~/.ssh/id_ed25519, ~/.ssh/id_rsa)."
}

function Write-VmSummary {
    <# Stampa il riepilogo "Cosa comunicare al team": VM, switch, utente, IP, specifiche, comando ssh. #>
    param(
        [Parameter(Mandatory)][string]$VmName,
        [Parameter(Mandatory)][string]$SwitchName,
        [Parameter(Mandatory)][string]$VmUser,
        [Parameter(Mandatory)][string]$IpAddress,
        [int]$CpuCount,
        [int]$MemoryGB,
        [int]$DiskGB
    )
    Write-Host ""
    Write-Host "=== Cosa comunicare al team ===" -ForegroundColor Cyan
    Write-Host "VM:         $VmName"
    Write-Host "Switch:     $SwitchName"
    Write-Host "Utente:     $VmUser"
    Write-Host "IP:         $IpAddress"
    Write-Host "Specifiche: Ubuntu Server 24.04 LTS, $CpuCount vCPU, ${MemoryGB} GB RAM, ${DiskGB} GB disco"
    Write-Host "Comando SSH pronto da copiare:"
    Write-Host "  ssh $VmUser@$IpAddress"
    Write-Host "================================"
    Write-Host ""
}
