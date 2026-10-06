# deploy/test-vm/lib/tls.ps1
#
# Fiducia nella CA interna di GitStack per e2e.ps1 (dot-sourced) e per
# tests/tls.Tests.ps1 (N5, GIT-143). Solo ASCII: Windows PowerShell 5.1 legge
# in ANSI i .ps1 senza BOM.
#
# Niente modifiche allo store dei certificati della macchina: la fiducia vale
# solo per questo processo PowerShell, tramite un callback di
# ServicePointManager che accetta SOLO una catena che finisce nella CA data
# (impronta confrontata) e solo se il nome/IP corrisponde. Un certificato
# qualunque, o firmato da un'altra CA, resta rifiutato.

if (-not ('GitStackCaTrust' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Net.Security;
using System.Security.Cryptography.X509Certificates;

public static class GitStackCaTrust
{
    public static X509Certificate2 Ca;

    // Vero se `cert` e' valido per il nome richiesto (nessun errore di nome)
    // e la catena finisce nella CA configurata.
    public static bool Validate(object sender, X509Certificate cert, X509Chain chain, SslPolicyErrors errors)
    {
        if (errors == SslPolicyErrors.None) return true;
        if ((errors & SslPolicyErrors.RemoteCertificateNameMismatch) != 0) return false;
        if ((errors & SslPolicyErrors.RemoteCertificateNotAvailable) != 0) return false;
        if (Ca == null || cert == null) return false;

        X509Chain own = new X509Chain();
        own.ChainPolicy.RevocationMode = X509RevocationMode.NoCheck;
        own.ChainPolicy.VerificationFlags = X509VerificationFlags.AllowUnknownCertificateAuthority;
        own.ChainPolicy.ExtraStore.Add(Ca);
        own.Build(new X509Certificate2(cert));
        if (own.ChainElements.Count < 2) return false;
        X509Certificate2 root = own.ChainElements[own.ChainElements.Count - 1].Certificate;
        if (!string.Equals(root.Thumbprint, Ca.Thumbprint, StringComparison.OrdinalIgnoreCase)) return false;
        foreach (X509ChainStatus s in own.ChainStatus)
        {
            // Unica anomalia ammessa: la radice non e' nello store di sistema.
            if (s.Status != X509ChainStatusFlags.UntrustedRoot && s.Status != X509ChainStatusFlags.NoError) return false;
        }
        return true;
    }

    public static RemoteCertificateValidationCallback Callback
    {
        get { return Validate; }
    }
}
'@
}

# Impronta SHA-256 di un certificato PEM/DER, nel formato di openssl
# (AA:BB:...), per confrontarla con `gitstack-tls fingerprint`.
function Get-CertSha256Fingerprint {
    param([Parameter(Mandatory)][string]$Path)
    $cert = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2 -ArgumentList (Resolve-Path -LiteralPath $Path).Path
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try { $hash = $sha.ComputeHash($cert.RawData) } finally { $sha.Dispose() }
    return (($hash | ForEach-Object { $_.ToString('X2') }) -join ':')
}

# Si fida della CA in $CaPath per le chiamate HTTPS di questo processo
# (Invoke-WebRequest, Invoke-RestMethod, Invoke-HttpRaw).
function Set-GitStackCaTrust {
    param([Parameter(Mandatory)][string]$CaPath)
    $ca = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2 -ArgumentList (Resolve-Path -LiteralPath $CaPath).Path
    [GitStackCaTrust]::Ca = $ca
    [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.SecurityProtocolType]::Tls12
    [System.Net.ServicePointManager]::ServerCertificateValidationCallback = [GitStackCaTrust]::Callback
}

function Clear-GitStackCaTrust {
    [System.Net.ServicePointManager]::ServerCertificateValidationCallback = $null
    [GitStackCaTrust]::Ca = $null
}
