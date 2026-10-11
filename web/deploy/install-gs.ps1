# Installa `gs`, la CLI di GitStack, da questa istanza (GIT-171, G6).
#
#   irm https://<host>/install-gs.ps1 | iex
#
# Lo script e' servito dall'nginx dell'immagine web, che al momento del
# servizio sostituisce il segnaposto dell'indirizzo con quello con cui lo hai
# raggiunto. Fuori dall'istanza (o in prova) passa -BaseUrl o GS_BASE_URL.
#
# Parametri (con `irm | iex` usa le variabili d'ambiente equivalenti):
#   -BaseUrl      indirizzo dell'istanza            (GS_BASE_URL)
#   -InstallDir   dove installare, default %LOCALAPPDATA%\Programs\gs (GS_INSTALL_DIR)
#   -Arch         amd64|arm64, forza il rilevamento (GS_ARCH)
#   -CaSha256     impronta SHA-256 della CA interna (GS_CA_SHA256)
#   -NoPath       non aggiungere la cartella al PATH dell'utente
#
# Il checksum viene da SHA256SUMS dell'istanza: protegge da download corrotti,
# non da un'istanza ostile. Niente amministratore: installa per l'utente
# corrente (la CA va in Cert:\CurrentUser\Root, Windows chiede conferma).
# File ASCII, senza BOM: PowerShell 5.1 lo legge come ANSI.
[CmdletBinding()]
param(
    [string]$BaseUrl = $env:GS_BASE_URL,
    [string]$InstallDir = $env:GS_INSTALL_DIR,
    [string]$Arch = $env:GS_ARCH,
    [string]$CaSha256 = $env:GS_CA_SHA256,
    [switch]$NoPath
)

$ErrorActionPreference = 'Stop'

function Fail([string]$msg) {
    throw ("install-gs: " + $msg)
}

if ([string]::IsNullOrEmpty($BaseUrl)) { $BaseUrl = '__GS_BASE_URL__' }
# Il confronto usa un prefisso: il segnaposto intero verrebbe riscritto dal servizio.
if ($BaseUrl.StartsWith('__GS_BASE_')) {
    Fail "indirizzo dell'istanza sconosciuto: passa -BaseUrl (es. -BaseUrl https://git.example.com)"
}
$BaseUrl = $BaseUrl.TrimEnd('/')

if ([string]::IsNullOrEmpty($InstallDir)) {
    $InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\gs'
}

if ([string]::IsNullOrEmpty($Arch)) {
    $procArch = $env:PROCESSOR_ARCHITEW6432
    if ([string]::IsNullOrEmpty($procArch)) { $procArch = $env:PROCESSOR_ARCHITECTURE }
    switch ($procArch) {
        'AMD64' { $Arch = 'amd64' }
        'ARM64' { $Arch = 'arm64' }
        default { Fail "architettura non supportata: $procArch" }
    }
}
if ($Arch -ne 'amd64' -and $Arch -ne 'arm64') { Fail "architettura non supportata: $Arch" }
$file = "gs_windows_$Arch.exe"

# PowerShell 5.1 vuole TLS 1.2 esplicito su alcune macchine.
try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch { }

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("install-gs-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null

function Get-Url([string]$url, [string]$dest) {
    Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $dest
}

try {
    $sums = Join-Path $tmp 'SHA256SUMS'
    try {
        Get-Url "$BaseUrl/downloads/SHA256SUMS" $sums
    } catch {
        if (-not $BaseUrl.StartsWith('https://')) { Fail "download di SHA256SUMS fallito da ${BaseUrl}: $($_.Exception.Message)" }
        # Certificato non fidato: CA interna (N5). La CA si scarica anche in HTTP.
        $caTmp = Join-Path $tmp 'ca.crt'
        $httpUrl = 'http://' + $BaseUrl.Substring('https://'.Length) + '/downloads/ca.crt'
        try { Get-Url $httpUrl $caTmp } catch { Fail "non riesco a scaricare da $BaseUrl (certificato non fidato e CA non disponibile)" }
        $cert = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2 $caTmp
        $got = $cert.GetCertHashString('SHA256').ToLower()
        $want = ([string]$CaSha256).Replace(':', '').ToLower()
        if ([string]::IsNullOrEmpty($want)) {
            Fail "CA interna non ancora fidata: passa -CaSha256 con l'impronta stampata dall'installer (sudo gitstack-tls fingerprint). Impronta del file scaricato: $got"
        }
        if ($got -ne $want) { Fail "impronta della CA diversa da -CaSha256 (attesa $want, trovata $got): non mi fido" }
        Import-Certificate -FilePath $caTmp -CertStoreLocation Cert:\CurrentUser\Root | Out-Null
        Write-Host "CA verificata e installata in Cert:\CurrentUser\Root"
        Get-Url "$BaseUrl/downloads/SHA256SUMS" $sums
    }

    Write-Host "Scarico $file da $BaseUrl ..."
    $bin = Join-Path $tmp $file
    Get-Url "$BaseUrl/downloads/$file" $bin

    $wantSum = $null
    foreach ($line in Get-Content -LiteralPath $sums) {
        $parts = $line -split '\s+', 2
        if ($parts.Count -eq 2 -and $parts[1].TrimStart('*') -eq $file) { $wantSum = $parts[0].ToLower() }
    }
    if (-not $wantSum) { Fail "$file non e' in SHA256SUMS" }
    $gotSum = (Get-FileHash -Algorithm SHA256 -LiteralPath $bin).Hash.ToLower()
    if ($wantSum -ne $gotSum) {
        Fail "checksum SHA-256 di $file diverso (atteso $wantSum, calcolato $gotSum): non installo"
    }

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Copy-Item -LiteralPath $bin -Destination (Join-Path $InstallDir 'gs.exe') -Force
    Write-Host "gs installato in $(Join-Path $InstallDir 'gs.exe')"

    if (-not $NoPath) {
        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        $entries = @()
        if ($userPath) { $entries = $userPath -split ';' }
        if ($entries -notcontains $InstallDir) {
            $newPath = (@($entries | Where-Object { $_ -ne '' }) + $InstallDir) -join ';'
            [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
            Write-Host "Aggiunto $InstallDir al PATH dell'utente: apri un nuovo terminale."
        }
    }
}
finally {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
