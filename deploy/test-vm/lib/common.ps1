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

function ConvertTo-NormalizedMacAddress {
    <# Normalizza un MAC in formato 'AA-BB-CC-DD-EE-FF' (maiuscolo, trattini), qualunque sia il formato in ingresso (con/senza trattini o due punti). #>
    param([Parameter(Mandatory)][string]$MacAddress)
    $hex = ($MacAddress -replace '[^0-9A-Fa-f]', '').ToUpperInvariant()
    if ($hex.Length -ne 12) {
        throw "MAC address '$MacAddress' non valido (attesi 12 esadecimali, trovati $($hex.Length))."
    }
    return ($hex -split '(?<=\G.{2})(?!$)') -join '-'
}

function Get-VmMacAddresses {
    <# Ritorna i MAC address (normalizzati) degli adattatori di rete Hyper-V di una VM. #>
    param([Parameter(Mandatory)][string]$VmName)
    $raw = (Get-VMNetworkAdapter -VMName $VmName | Select-Object -ExpandProperty MacAddress) | Where-Object { $_ }
    return @($raw | ForEach-Object { ConvertTo-NormalizedMacAddress $_ })
}

function Get-Ipv4SweepTargets {
    <#
      Calcola rete/broadcast della subnet configurata sull'host per
      -InterfaceAlias (es. 'vEthernet (Default Switch)') e ritorna la lista
      degli IP host da provare in uno sweep, esclusi rete e broadcast.
      La subnet del Default Switch cambia a ogni riavvio dell'host: va
      sempre calcolata da Get-NetIPAddress, mai codificata.
    #>
    param(
        [Parameter(Mandatory)][string]$InterfaceAlias,
        [int]$MaxHosts = 4094
    )
    $ipInfo = Get-NetIPAddress -InterfaceAlias $InterfaceAlias -AddressFamily IPv4 -ErrorAction SilentlyContinue |
        Where-Object { $_.IPAddress -notlike '169.254.*' } | Select-Object -First 1
    if (-not $ipInfo) {
        throw "Nessun indirizzo IPv4 configurato sull'interfaccia host '$InterfaceAlias'. Verifica che lo switch virtuale esista e che l'host abbia un IP su di esso ('Get-NetIPAddress -InterfaceAlias `"$InterfaceAlias`"')."
    }
    $bytes = ([System.Net.IPAddress]$ipInfo.IPAddress).GetAddressBytes()
    [int64]$ipUint = ([int64]$bytes[0] -shl 24) -bor ([int64]$bytes[1] -shl 16) -bor ([int64]$bytes[2] -shl 8) -bor [int64]$bytes[3]
    [int64]$fullMask = 0xFFFFFFFF
    $prefix = $ipInfo.PrefixLength
    [int64]$maskUint = if ($prefix -eq 0) { 0 } else { ($fullMask -shl (32 - $prefix)) -band $fullMask }
    [int64]$networkUint = $ipUint -band $maskUint
    [int64]$broadcastUint = ($networkUint -bor ((-bnot $maskUint) -band $fullMask)) -band $fullMask
    $hostCount = [Math]::Max(0, $broadcastUint - $networkUint - 1)
    $capped = $hostCount -gt $MaxHosts
    $take = [Math]::Min($hostCount, $MaxHosts)

    $targets = New-Object System.Collections.Generic.List[string]
    for ($i = 1; $i -le $take; $i++) {
        $u = $networkUint + $i
        $b0 = [byte](($u -shr 24) -band 0xFF)
        $b1 = [byte](($u -shr 16) -band 0xFF)
        $b2 = [byte](($u -shr 8) -band 0xFF)
        $b3 = [byte]($u -band 0xFF)
        $ip = "$b0.$b1.$b2.$b3"
        if ($ip -ne $ipInfo.IPAddress) { $targets.Add($ip) }
    }
    $b0n = [byte](($networkUint -shr 24) -band 0xFF); $b1n = [byte](($networkUint -shr 16) -band 0xFF); $b2n = [byte](($networkUint -shr 8) -band 0xFF); $b3n = [byte]($networkUint -band 0xFF)
    $b0b = [byte](($broadcastUint -shr 24) -band 0xFF); $b1b = [byte](($broadcastUint -shr 16) -band 0xFF); $b2b = [byte](($broadcastUint -shr 8) -band 0xFF); $b3b = [byte]($broadcastUint -band 0xFF)
    return [PSCustomObject]@{
        Targets   = $targets
        Network   = "$b0n.$b1n.$b2n.$b3n"
        Broadcast = "$b0b.$b1b.$b2b.$b3b"
        Prefix    = $prefix
        Capped    = $capped
    }
}

function Invoke-Ipv4ArpSweep {
    <#
      Popola la cache ARP/vicinato dell'host inviando un ping asincrono e a
      basso timeout verso ogni indirizzo della subnet di -InterfaceAlias.
      "Leggero": e' I/O asincrono (System.Net.NetworkInformation.Ping), non
      processi esterni; anche una /20 (~4094 host) impiega pochi secondi.
      Popola la cache anche per gli host che filtrano l'ICMP: la
      risoluzione ARP avviene a livello di driver di rete prima di
      qualunque firewall applicativo sulla VM di destinazione.
    #>
    param(
        [Parameter(Mandatory)][string]$InterfaceAlias,
        [int]$PingTimeoutMs = 300,
        [int]$MaxHosts = 4094
    )
    $sweep = Get-Ipv4SweepTargets -InterfaceAlias $InterfaceAlias -MaxHosts $MaxHosts
    if ($sweep.Capped) {
        Write-Host "    Subnet '$($sweep.Network)/$($sweep.Prefix)' piu' grande di $MaxHosts host: sweep limitato ai primi $MaxHosts."
    }
    $pingers = New-Object System.Collections.Generic.List[System.Net.NetworkInformation.Ping]
    $tasks = New-Object System.Collections.Generic.List[System.Threading.Tasks.Task]
    try {
        foreach ($target in $sweep.Targets) {
            $p = New-Object System.Net.NetworkInformation.Ping
            $pingers.Add($p)
            $tasks.Add($p.SendPingAsync($target, $PingTimeoutMs))
        }
        if ($tasks.Count -gt 0) {
            [System.Threading.Tasks.Task]::WaitAll($tasks.ToArray(), $PingTimeoutMs + 5000) | Out-Null
        }
    } finally {
        foreach ($p in $pingers) { $p.Dispose() }
    }
}

function Test-TcpPortOnce {
    <# Prova una singola connessione TCP, senza attesa/retry: true/false, mai un'eccezione. #>
    param(
        [Parameter(Mandatory)][string]$IpAddress,
        [int]$Port = 22,
        [int]$TimeoutMs = 2000
    )
    $client = New-Object System.Net.Sockets.TcpClient
    try {
        $connectTask = $client.ConnectAsync($IpAddress, $Port)
        if ($connectTask.Wait($TimeoutMs) -and $client.Connected) { return $true }
        return $false
    } catch {
        return $false
    } finally {
        $client.Dispose()
    }
}

function Resolve-VmIPv4ViaArp {
    <#
      Fallback quando Hyper-V non riporta l'IP via KVP (immagine cloud
      senza linux-cloud-tools/hv_kvp_daemon, vedi GIT-25): legge il MAC
      dell'adattatore di rete della VM e lo cerca nella cache ARP/vicinato
      dell'host sull'interfaccia dello switch virtuale, popolandola con uno
      sweep se serve. Conferma con una connessione TCP :22 prima di
      restituire l'IP, perche' dopo il ripristino di un checkpoint puo'
      restare in cache una voce ARP vecchia (VM precedente sullo stesso
      switch con lo stesso IP DHCP ma MAC diverso, o viceversa).
    #>
    param(
        [Parameter(Mandatory)][string]$VmName,
        [string]$SwitchName = 'Default Switch',
        [int]$TimeoutSeconds = 240,
        [int]$PollSeconds = 5
    )
    $interfaceAlias = "vEthernet ($SwitchName)"
    # -IncludeHidden: l'adattatore vEthernet del Default Switch puo' essere
    # nascosto (Get-NetAdapter senza questo switch non lo restituisce anche
    # se e' Up e ha un IP configurato, vedi GIT-26). Get-NetIPAddress e
    # Get-NetNeighbor piu' sotto interrogano per -InterfaceAlias e trovano
    # l'adattatore nascosto senza bisogno di un flag equivalente.
    if (-not (Get-NetAdapter -Name $interfaceAlias -IncludeHidden -ErrorAction SilentlyContinue)) {
        throw "L'interfaccia host '$interfaceAlias' non esiste: lo switch virtuale '$SwitchName' non ha un adattatore vEthernet sull'host (verifica con 'Get-NetAdapter -IncludeHidden' e 'Get-VMSwitch')."
    }
    $macs = Get-VmMacAddresses -VmName $VmName
    if (-not $macs) {
        throw "Nessun MAC address trovato per l'adattatore di rete di '$VmName' ('Get-VMNetworkAdapter -VMName $VmName')."
    }
    Write-Host "    Fallback MAC -> ARP: MAC $($macs -join ', ') su '$interfaceAlias' (timeout ${TimeoutSeconds}s)..."
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $sweepSubnet = $null
    while ((Get-Date) -lt $deadline) {
        $neighbors = Get-NetNeighbor -InterfaceAlias $interfaceAlias -AddressFamily IPv4 -ErrorAction SilentlyContinue |
            Where-Object { $_.State -in @('Reachable', 'Stale', 'Delay', 'Probe', 'Permanent') }
        $candidates = @($neighbors | Where-Object {
            try { $macs -contains (ConvertTo-NormalizedMacAddress $_.LinkLayerAddress) } catch { $false }
        })
        # Piu' voci ARP possono corrispondere allo stesso MAC (es. residuo di
        # un checkpoint precedente ancora in cache accanto a quella nuova):
        # provarle tutte su :22, non solo la prima, e restituire la prima
        # che risponde davvero.
        $confirmed = $null
        foreach ($candidate in $candidates) {
            if (Test-TcpPortOnce -IpAddress $candidate.IPAddress -Port 22) {
                $confirmed = $candidate
                break
            }
            Write-Host "    Voce ARP $($candidate.IPAddress) per MAC $($candidate.LinkLayerAddress) trovata ma la porta 22 non risponde ancora (boot in corso, o voce residua di un checkpoint precedente): provo le altre voci/continuo ad attendere."
        }
        if ($confirmed) {
            Write-Host "    IP trovato via ARP e confermato su :22: $($confirmed.IPAddress)"
            return $confirmed.IPAddress
        }
        try {
            Invoke-Ipv4ArpSweep -InterfaceAlias $interfaceAlias
            if (-not $sweepSubnet) { $sweepSubnet = (Get-Ipv4SweepTargets -InterfaceAlias $interfaceAlias).Network + '/' + (Get-Ipv4SweepTargets -InterfaceAlias $interfaceAlias).Prefix }
        } catch {
            Write-Warning "Sweep ARP su '$interfaceAlias' fallito: $($_.Exception.Message)"
        }
        Start-Sleep -Seconds $PollSeconds
    }
    throw "Timeout (${TimeoutSeconds}s) nel risolvere l'IPv4 di '$VmName' via MAC -> ARP. MAC cercato: $($macs -join ', '); interfaccia host: '$interfaceAlias'; subnet: $sweepSubnet. Verifica che la VM abbia finito il boot e ricevuto un IP via DHCP dallo switch '$SwitchName' (console della VM da Hyper-V Manager, 'ip a'), e che nessun firewall Windows blocchi ICMP in uscita verso quella subnet."
}

function Wait-VmIPv4Address {
    <#
      Attende l'IPv4 della VM. Primo tentativo via KVP/integration services
      Hyper-V (-KvpTimeoutSeconds, rapido se disponibile); se l'immagine non
      ha hv_kvp_daemon (cloud image generica Ubuntu 24.04, GIT-25) o KVP non
      risponde in tempo, fallback su MAC -> cache ARP dell'host
      (Resolve-VmIPv4ViaArp) per il tempo restante fino a -TimeoutSeconds.
    #>
    param(
        [Parameter(Mandatory)][string]$VmName,
        [string]$SwitchName = 'Default Switch',
        [int]$TimeoutSeconds = 300,
        [int]$PollSeconds = 5,
        [int]$KvpTimeoutSeconds = 30
    )
    Write-Host "    In attesa dell'indirizzo IPv4 di '$VmName' (timeout ${TimeoutSeconds}s): provo prima KVP (servizi di integrazione Hyper-V, fino a ${KvpTimeoutSeconds}s)..."
    $overallDeadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $kvpDeadline = (Get-Date).AddSeconds([Math]::Min($KvpTimeoutSeconds, $TimeoutSeconds))
    while ((Get-Date) -lt $kvpDeadline) {
        $addrs = (Get-VMNetworkAdapter -VMName $VmName).IPAddresses |
            Where-Object { $_ -match '^\d{1,3}(\.\d{1,3}){3}$' -and $_ -ne '0.0.0.0' }
        if ($addrs) {
            Write-Host "    IP trovato via KVP: $($addrs[0])"
            return $addrs[0]
        }
        Start-Sleep -Seconds $PollSeconds
    }
    $remaining = [int]([Math]::Max(1, ($overallDeadline - (Get-Date)).TotalSeconds))
    Write-Host "    KVP non ha riportato un IP entro ${KvpTimeoutSeconds}s (probabile immagine cloud generica, senza linux-cloud-tools/hv_kvp_daemon)."
    return Resolve-VmIPv4ViaArp -VmName $VmName -SwitchName $SwitchName -TimeoutSeconds $remaining -PollSeconds $PollSeconds
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

# --- Spazio libero (GIT-25: default su disco capiente) ---------------------

function Get-VolumeFreeBytes {
    <# Byte liberi sul volume che contiene -Path, anche se -Path non esiste ancora (guarda solo la lettera di unita'). #>
    param([Parameter(Mandatory)][string]$Path)
    $qualifier = Split-Path -Path $Path -Qualifier -ErrorAction SilentlyContinue
    if (-not $qualifier) {
        throw "Impossibile determinare il volume di '$Path': serve un percorso assoluto con lettera di unita' (es. 'F:\HyperV-VMs\...')."
    }
    try {
        $drive = [System.IO.DriveInfo]::new($qualifier)
        return $drive.AvailableFreeSpace
    } catch {
        throw "Impossibile leggere lo spazio libero del volume '$qualifier' per '$Path': $($_.Exception.Message)"
    }
}

function Assert-FreeSpace {
    <# Ferma lo script con un messaggio chiaro (GB liberi vs richiesti) se il volume di -Path non ha abbastanza spazio. #>
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][int64]$RequiredBytes,
        [Parameter(Mandatory)][string]$Purpose
    )
    $free = Get-VolumeFreeBytes -Path $Path
    $freeGB = [math]::Round($free / 1GB, 1)
    $requiredGB = [math]::Round($RequiredBytes / 1GB, 1)
    if ($free -lt $RequiredBytes) {
        $qualifier = Split-Path -Path $Path -Qualifier
        throw "Spazio insufficiente su '$qualifier' per $Purpose ($Path): ${freeGB} GB liberi, servono almeno ${requiredGB} GB. Passa un percorso su un volume con piu' spazio (es. '-VmPath F:\HyperV-VMs\gitstack-test-vm -ImageCacheDir F:\HyperV-VMs\image-cache')."
    }
    Write-Host "    Spazio libero su '$(Split-Path -Path $Path -Qualifier)': ${freeGB} GB (>= ${requiredGB} GB richiesti per $Purpose)."
}

# --- Download con verifica SHA256SUMS (GIT-25: immagine 24.04 non piu' pubblicata come .vhd.tar.gz) ---

function Get-RemoteContentLength {
    <# Content-Length via HEAD, per dimensionare il controllo di spazio libero prima di scaricare. $null se non disponibile. #>
    param([Parameter(Mandatory)][string]$Url)
    try {
        $resp = Invoke-WebRequest -Uri $Url -Method Head -UseBasicParsing
        $len = $resp.Headers['Content-Length']
        if ($len) { return [int64]($len | Select-Object -First 1) }
    } catch {
        Write-Warning "HEAD su '$Url' fallito, uso una stima fissa per il controllo di spazio libero: $($_.Exception.Message)"
    }
    return $null
}

function Get-Sha256SumsExpectedHash {
    <#
      Scarica SHA256SUMS dalla STESSA cartella di -ImageUrl (deriva l'URL
      sostituendo il nome file) e ne estrae l'hash atteso per il file. Il
      contenuto non si pinna: la cartella 'release' e' un puntatore mobile
      che Canonical aggiorna in-place a ogni point release (vedi commento
      del CTO su GIT-25), quindi SHA256SUMS va riscaricato a ogni corsa.
    #>
    param([Parameter(Mandatory)][string]$ImageUrl)
    $fileName = Split-Path -Leaf $ImageUrl
    $sumsUrl = $ImageUrl.Substring(0, $ImageUrl.Length - $fileName.Length) + 'SHA256SUMS'
    Write-Host "    Scarico $sumsUrl per verificare l'hash di $fileName"
    try {
        $rawContent = (Invoke-WebRequest -Uri $sumsUrl -UseBasicParsing).Content
    } catch {
        throw "Impossibile scaricare '$sumsUrl' per verificare lo SHA256: $($_.Exception.Message)"
    }
    # PowerShell 5.1 + -UseBasicParsing ritorna .Content come byte[] (non
    # string) quando il server risponde con un Content-Type non testuale
    # (SHA256SUMS arriva come 'application/octet-stream'): va decodificato
    # esplicitamente, altrimenti -split lavora sulla rappresentazione
    # dell'array invece che sul testo.
    $content = if ($rawContent -is [byte[]]) { [System.Text.Encoding]::UTF8.GetString($rawContent) } else { $rawContent }
    $line = ($content -split '\r?\n') | Where-Object { $_ -match [regex]::Escape($fileName) + '\s*$' } | Select-Object -First 1
    if (-not $line) {
        throw "Nessuna riga per '$fileName' in '$sumsUrl'. Il nome file dell'immagine potrebbe essere cambiato: verifica su $(Split-Path -Parent $sumsUrl)/ e passa -UbuntuImageUrl aggiornato."
    }
    $hash = ($line -split '\s+')[0].TrimStart('\')
    if ($hash -notmatch '^[0-9a-fA-F]{64}$') {
        throw "Riga SHA256SUMS per '$fileName' malformata: '$line'"
    }
    return $hash.ToLowerInvariant()
}

function Get-CloudImageWithHashVerification {
    <#
      Scarica -ImageUrl in -DestinationPath (cache), verificando sempre lo
      SHA256 contro SHA256SUMS della stessa cartella: se il file e' gia' in
      cache e l'hash combacia lo riusa senza riscaricare; se l'hash non
      combacia (cache di una release precedente, o file corrotto) lo
      riscarica. FAIL chiaro se il download finale non combacia.
      Usa curl.exe (presente su Windows 11, -fL --retry 3) se disponibile:
      Invoke-WebRequest con la barra di avanzamento di PowerShell 5.1 e'
      lentissima sui file grandi. Fallback: Invoke-WebRequest con
      $ProgressPreference = 'SilentlyContinue'.
    #>
    param(
        [Parameter(Mandatory)][string]$ImageUrl,
        [Parameter(Mandatory)][string]$DestinationPath
    )
    $expectedHash = Get-Sha256SumsExpectedHash -ImageUrl $ImageUrl

    if (Test-Path -LiteralPath $DestinationPath) {
        $cachedHash = (Get-FileHash -LiteralPath $DestinationPath -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($cachedHash -eq $expectedHash) {
            Write-Host "==> Uso l'immagine gia' in cache (SHA256 verificato): $DestinationPath"
            return
        }
        Write-Host "==> Cache presente ma SHA256 non combacia piu' (release aggiornata o file corrotto): la riscarico."
        Remove-Item -LiteralPath $DestinationPath -Force
    }

    $partialPath = "$DestinationPath.partial"
    if (Test-Path -LiteralPath $partialPath) { Remove-Item -LiteralPath $partialPath -Force }
    Write-Host "==> Scarico l'immagine cloud Ubuntu 24.04 LTS da $ImageUrl"
    Write-Host "    (cache: $DestinationPath; le esecuzioni successive riusano il file scaricato se l'hash combacia)"
    $curl = Get-Command curl.exe -ErrorAction SilentlyContinue
    if ($curl) {
        & $curl.Source -fL --retry 3 --retry-delay 2 -o $partialPath $ImageUrl
        if ($LASTEXITCODE -ne 0) {
            throw "curl.exe e' uscito con codice $LASTEXITCODE scaricando '$ImageUrl'. Verifica la connessione e riprova; se l'URL e' cambiato, controlla https://cloud-images.ubuntu.com/releases/noble/release/ e passa -UbuntuImageUrl aggiornato."
        }
    } else {
        Write-Host "    curl.exe non trovato nel PATH: uso Invoke-WebRequest (\$ProgressPreference='SilentlyContinue' per evitare la barra di avanzamento lentissima di PowerShell 5.1)."
        $previousProgressPreference = $ProgressPreference
        $ProgressPreference = 'SilentlyContinue'
        try {
            Invoke-WebRequest -Uri $ImageUrl -OutFile $partialPath -UseBasicParsing
        } finally {
            $ProgressPreference = $previousProgressPreference
        }
    }

    $downloadedHash = (Get-FileHash -LiteralPath $partialPath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($downloadedHash -ne $expectedHash) {
        Remove-Item -LiteralPath $partialPath -Force -ErrorAction SilentlyContinue
        throw "SHA256 non combacia per '$ImageUrl': atteso $expectedHash, ottenuto $downloadedHash. Download scartato (non salvato in cache). Riprova; se persiste, verifica che l'URL punti ancora al file giusto su https://cloud-images.ubuntu.com/releases/noble/release/."
    }
    Move-Item -LiteralPath $partialPath -Destination $DestinationPath -Force
    Write-Host "    SHA256 verificato: $downloadedHash"
}

# --- Conversione qcow2 -> VHDX dinamico via container Docker (GIT-25: qemu-img non installato sull'host) ---

function Assert-DockerAvailable {
    <#
      Ferma lo script con un messaggio chiaro se Docker non e' nel PATH, il
      daemon non risponde, o il motore e' in modalita' Windows containers
      (serve quello Linux per qemu-img). Nessuna installazione manuale
      richiesta: basta che Docker Desktop (motore Linux/WSL2) sia avviato.
    #>
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        throw "Il comando 'docker' non e' nel PATH. Installa/avvia Docker Desktop (motore Linux, incluso in Windows 11 con WSL2) e riprova. In alternativa converti l'immagine con WSL e 'qemu-img' installato li' dentro (vedi README)."
    }
    $osType = (& docker info --format '{{.OSType}}' 2>&1)
    if ($LASTEXITCODE -ne 0) {
        throw "Docker non risponde ('docker info' fallito): $osType. Avvia Docker Desktop e attendi che il daemon sia pronto (icona nella system tray), poi riprova."
    }
    if (($osType | Select-Object -Last 1).Trim() -ne 'linux') {
        throw "Docker Desktop e' in modalita' 'Windows containers' (OSType: $osType), serve il motore Linux per qemu-img. In Docker Desktop: tasto destro sull'icona nella tray -> 'Switch to Linux containers...', poi riprova."
    }
}

function Convert-QcowToVhdxDynamic {
    <#
      Converte un'immagine .img (qcow2) in VHDX dinamico con un container
      Docker usando un'immagine base pinnata per digest (non un tag
      mobile): alpine ha 'qemu-img' nei suoi repository ufficiali via apk,
      installato al volo nel container (nessuna immagine "qemu-img" di
      terzi non verificata su Docker Hub e' abbastanza affidabile da
      pinnare). -SourcePath e -DestinationPath vanno passati in formato
      Windows nativo (es. 'F:\...'): Docker Desktop (WSL2/Hyper-V backend)
      accetta bind mount con lettera di unita' Windows direttamente, NON
      percorsi POSIX tipo '/f/...' delle shell bash su Windows.
    #>
    param(
        [Parameter(Mandatory)][string]$SourcePath,
        [Parameter(Mandatory)][string]$DestinationPath,
        [string]$DockerImage = 'alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc'
    )
    Assert-DockerAvailable

    $srcDir = Split-Path -Parent $SourcePath
    $srcFile = Split-Path -Leaf $SourcePath
    $dstDir = Split-Path -Parent $DestinationPath
    $dstFile = Split-Path -Leaf $DestinationPath
    if (-not (Test-Path -LiteralPath $SourcePath)) {
        throw "File sorgente non trovato per la conversione: $SourcePath"
    }
    New-Item -ItemType Directory -Path $dstDir -Force | Out-Null
    if (Test-Path -LiteralPath $DestinationPath) { Remove-Item -LiteralPath $DestinationPath -Force }

    Write-Host "==> Converto $SourcePath in VHDX dinamico (container Docker, immagine $DockerImage)"
    $containerScript = "set -e; apk add --no-cache qemu-img >/tmp/apk-qemu-img.log 2>&1; qemu-img convert -f qcow2 -O vhdx -o subformat=dynamic '/src-in/$srcFile' '/dst-out/$dstFile'"
    & docker run --rm -v "${srcDir}:/src-in:ro" -v "${dstDir}:/dst-out" $DockerImage sh -c $containerScript
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0 -or -not (Test-Path -LiteralPath $DestinationPath)) {
        throw "Conversione qcow2 -> VHDX fallita (container Docker uscito con codice $exitCode) o file di destinazione mancante ($DestinationPath). Verifica che Docker Desktop condivida l'unita' sorgente/destinazione (Impostazioni -> Resources -> File sharing, se usi il backend Hyper-V; con WSL2 di norma non serve) e riprova."
    }
    Write-Host "    Conversione completata: $DestinationPath"
}

# --- Rimozione file con retry (GIT-27: vmms/vmwp puo' tenere il lock un attimo dopo Remove-VM) ---

function Remove-ItemWithRetry {
    <#
      Rimuove -Path (file o cartella, con -Recurse) ritentando per un tempo
      breve e limitato se il percorso e' ancora bloccato: un primo tentativo
      subito, poi uno ogni -RetryIntervalSeconds finche' non passano
      -TimeoutSeconds. Non lancia mai un'eccezione: ritorna $true se alla
      fine il percorso non esiste piu', $false se resta bloccato (il
      chiamante decide cosa fare, es. avvisare l'utente).
    #>
    param(
        [Parameter(Mandatory)][string]$Path,
        [switch]$Recurse,
        [int]$TimeoutSeconds = 30,
        [int]$RetryIntervalSeconds = 3
    )
    if (-not (Test-Path -LiteralPath $Path)) { return $true }
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $lastError = $null
    while ($true) {
        try {
            if ($Recurse) {
                Remove-Item -LiteralPath $Path -Recurse -Force -ErrorAction Stop
            } else {
                Remove-Item -LiteralPath $Path -Force -ErrorAction Stop
            }
        } catch {
            $lastError = $_
        }
        if (-not (Test-Path -LiteralPath $Path)) { return $true }
        if ((Get-Date) -ge $deadline) {
            if ($lastError) {
                Write-Warning "    '$Path' ancora bloccato dopo $TimeoutSeconds secondi di tentativi: $($lastError.Exception.Message)"
            }
            return $false
        }
        Start-Sleep -Seconds $RetryIntervalSeconds
    }
}

function Test-VmOwnedFolder {
    <#
      True se -Path e' una cartella dedicata solo a -VmName e quindi sicura
      da rimuovere con -Recurse: il suo nome finale (leaf) deve essere uguale
      a -VmName, E il percorso non deve coincidere ne' essere un antenato di
      -HostVirtualMachinePath.

      -HostVirtualMachinePath e' la cartella predefinita dell'host per le VM
      create senza -Path ((Get-VMHost).VirtualMachinePath), condivisa da
      TUTTE le VM: senza questo secondo controllo, una VM il cui nome
      coincidesse per caso con il leaf di quella cartella condivisa (o un suo
      antenato) farebbe cancellare con -Recurse -Force anche le
      configurazioni di altre VM (GIT-27, rework: verificato sulla macchina
      del board che ConfigurationLocation di una VM senza -Path e' proprio
      quella cartella condivisa).

      Confronto case-insensitive (percorsi Windows), ignora gli eventuali
      separatori finali.
    #>
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$VmName,
        [Parameter(Mandatory)][string]$HostVirtualMachinePath
    )
    $normalizedPath = $Path.TrimEnd('\', '/')
    $normalizedHostPath = $HostVirtualMachinePath.TrimEnd('\', '/')

    $leaf = Split-Path -Path $normalizedPath -Leaf
    if ($leaf -ne $VmName) { return $false }

    if ($normalizedPath -ieq $normalizedHostPath) { return $false }

    # $Path e' un antenato di $HostVirtualMachinePath se quest'ultimo comincia
    # con "$Path\": cancellarlo con -Recurse cancellerebbe anche la cartella
    # condivisa dell'host (e quindi le altre VM che ci vivono dentro).
    if ($normalizedHostPath.StartsWith($normalizedPath + '\', [StringComparison]::OrdinalIgnoreCase)) {
        return $false
    }

    return $true
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
