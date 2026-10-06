# deploy/test-vm/tests/tls.Tests.ps1
#
# Prova del callback di fiducia (lib/tls.ps1): un certificato firmato dalla CA
# data passa, uno firmato da un'altra CA o per un altro IP no. Da lanciare con
# Windows PowerShell 5.1:
#   powershell.exe -NoProfile -File deploy\test-vm\tests\tls.Tests.ps1
# Crea certificati di prova nello store dell'utente e li rimuove alla fine.
# Esce con codice != 0 se una verifica fallisce. Solo ASCII.

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '..\lib\tls.ps1')

$failures = New-Object System.Collections.Generic.List[string]
function Assert-That {
    param([string]$Name, [bool]$Condition)
    if ($Condition) { Write-Host "[PASS] $Name" } else { Write-Host "[FAIL] $Name"; $failures.Add($Name) }
}

$created = New-Object System.Collections.Generic.List[object]
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("gitstack-tls-test-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    function New-TestCa {
        param([string]$Name)
        $c = New-SelfSignedCertificate -Subject "CN=$Name" -CertStoreLocation Cert:\CurrentUser\My -KeyUsage CertSign, CRLSign `
            -TextExtension @('2.5.29.19={critical}{text}CA=true') -NotAfter (Get-Date).AddDays(30)
        $created.Add($c)
        return $c
    }
    function New-TestLeaf {
        param($Signer, [string]$Ip)
        $c = New-SelfSignedCertificate -Subject 'CN=leaf' -CertStoreLocation Cert:\CurrentUser\My -Signer $Signer `
            -TextExtension @("2.5.29.17={text}IPAddress=$Ip") -NotAfter (Get-Date).AddDays(20)
        $created.Add($c)
        return $c
    }

    $ca1 = New-TestCa 'GitStack test CA 1'
    $ca2 = New-TestCa 'GitStack test CA 2'
    $leaf1 = New-TestLeaf -Signer $ca1 -Ip '10.9.8.7'
    $leaf2 = New-TestLeaf -Signer $ca2 -Ip '10.9.8.7'

    $ca1Path = Join-Path $tmp 'ca1.cer'
    Export-Certificate -Cert $ca1 -FilePath $ca1Path | Out-Null
    [GitStackCaTrust]::Ca = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2 -ArgumentList $ca1Path
    $chainErr = [System.Net.Security.SslPolicyErrors]::RemoteCertificateChainErrors
    $nameErr = [System.Net.Security.SslPolicyErrors]::RemoteCertificateNameMismatch

    Assert-That 'catena nella CA data (radice non di sistema): accettata' ([GitStackCaTrust]::Validate($null, $leaf1, $null, $chainErr))
    Assert-That 'nessun errore: accettata' ([GitStackCaTrust]::Validate($null, $leaf2, $null, [System.Net.Security.SslPolicyErrors]::None))
    Assert-That 'firmato da un altra CA: rifiutata' (-not [GitStackCaTrust]::Validate($null, $leaf2, $null, $chainErr))
    Assert-That 'nome/IP non corrispondente: rifiutata' (-not [GitStackCaTrust]::Validate($null, $leaf1, $null, ($chainErr -bor $nameErr)))
    [GitStackCaTrust]::Ca = $null
    Assert-That 'senza CA configurata: rifiutata' (-not [GitStackCaTrust]::Validate($null, $leaf1, $null, $chainErr))

    $fp = Get-CertSha256Fingerprint -Path $ca1Path
    Assert-That 'impronta SHA-256 nel formato openssl' ($fp -match '^([0-9A-F]{2}:){31}[0-9A-F]{2}$')

    Set-GitStackCaTrust -CaPath $ca1Path
    Assert-That 'Set-GitStackCaTrust imposta il callback' ($null -ne [System.Net.ServicePointManager]::ServerCertificateValidationCallback)
    Clear-GitStackCaTrust
    Assert-That 'Clear-GitStackCaTrust lo toglie' ($null -eq [System.Net.ServicePointManager]::ServerCertificateValidationCallback)
} finally {
    foreach ($c in $created) { Remove-Item -LiteralPath ("Cert:\CurrentUser\My\" + $c.Thumbprint) -ErrorAction SilentlyContinue }
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

if ($failures.Count -gt 0) {
    Write-Host "$($failures.Count) verifiche fallite"
    exit 1
}
Write-Host 'Tutte le verifiche passate.'
exit 0
