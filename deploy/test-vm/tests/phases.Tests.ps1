# deploy/test-vm/tests/phases.Tests.ps1
#
# Prova, senza VM, i pezzi puri di lib/phases.ps1 (GIT-148): comando di
# installazione, riga ssh, URL del repo, ripristino dell'ambiente di
# Invoke-E2eGit e lettura del commit precedente da una risposta GitHub finta.
# Da lanciare con Windows PowerShell 5.1:
#   powershell.exe -NoProfile -File deploy\test-vm\tests\phases.Tests.ps1
# Esce con codice != 0 se una verifica fallisce. Solo ASCII.

$ErrorActionPreference = 'Stop'

# Dipendenze che in produzione arrivano da e2e.ps1: qui sono finte.
function Write-Log { param([string]$Message) }
function Add-StepResult { param($Name, $Ok, $Detail) }
function Invoke-ExternalCommand {
    param([string]$FilePath, [string[]]$ArgumentList, [int]$TimeoutSeconds = 60)
    $script:LastExternal = [pscustomobject]@{ FilePath = $FilePath; Args = $ArgumentList; Env = $env:GIT_SSH_COMMAND; Prompt = $env:GIT_TERMINAL_PROMPT }
    return [pscustomobject]@{ ExitCode = 0; StdOut = ''; StdErr = ''; TimedOut = $false }
}
. (Join-Path $PSScriptRoot '..\lib\phases.ps1')

$failures = New-Object System.Collections.Generic.List[string]
function Assert-That {
    param([string]$Name, [bool]$Condition)
    if ($Condition) { Write-Host "[PASS] $Name" } else { Write-Host "[FAIL] $Name"; $failures.Add($Name) }
}

$script:GitStackRepo = 'owner/GitStack'
$script:GitSshPort = 2222

$cmd = Get-E2eInstallCommand -InstallRef 'abc123'
Assert-That 'install: punta a install.sh del commit' ($cmd -like '*raw.githubusercontent.com/owner/GitStack/abc123/deploy/install.sh*')
Assert-That 'install: GITSTACK_REF e admin obbligatorio' ($cmd -like '*GITSTACK_REF=abc123 GITSTACK_ADMIN_REQUIRED=1 bash')

$data = [pscustomobject]@{ User = 'u1'; Token = 'tok'; Repo = 'r1'; SshKey = 'C:\x\id'; KnownHosts = 'C:\x\kh' }
$open = Get-E2eSshCommand -Data $data
$strict = Get-E2eSshCommand -Data $data -Strict
Assert-That 'ssh: accept-new alla creazione' ($open -like '*StrictHostKeyChecking=accept-new*')
Assert-That 'ssh: yes in verifica (chiave host cambiata = errore)' ($strict -like '*StrictHostKeyChecking=yes*')
Assert-That 'ssh: percorsi con la barra normale' ($strict -like '*-i C:/x/id *' -and $strict -like '*UserKnownHostsFile=C:/x/kh*')

$urls = Get-E2eRepoUrls -Data $data -Ip '10.0.0.5'
Assert-That 'url https con token' ($urls.Https -eq 'https://u1:tok@10.0.0.5/u1/r1.git')
Assert-That 'url ssh sulla porta del servizio git' ($urls.Ssh -eq 'ssh://git@10.0.0.5:2222/u1/r1.git')

# Invoke-E2eGit: imposta l'ambiente per la chiamata e lo rimette com era.
$env:GIT_SSH_COMMAND = 'valore-originale'
$env:GIT_TERMINAL_PROMPT = $null
[void](Invoke-E2eGit -GitArgs @('ls-remote', 'x') -CaPath 'C:\ca\ca.crt' -SshCommand 'ssh-finto')
Assert-That 'git: ambiente della chiamata (SSH e niente prompt)' ($script:LastExternal.Env -eq 'ssh-finto' -and $script:LastExternal.Prompt -eq '0')
Assert-That 'git: ambiente ripristinato dopo la chiamata' ($env:GIT_SSH_COMMAND -eq 'valore-originale' -and -not $env:GIT_TERMINAL_PROMPT)
$joined = $script:LastExternal.Args -join ' '
Assert-That 'git: si fida solo della CA data, senza credential helper' ($joined -like '*http.sslCAInfo=C:/ca/ca.crt*' -and $joined -like '*credential.helper=*')
$env:GIT_SSH_COMMAND = $null

# Get-E2ePreviousCommit: primo genitore dalla risposta dell'API.
function Invoke-RestMethod {
    param([string]$Uri, [int]$TimeoutSec, [hashtable]$Headers)
    $script:LastUri = $Uri
    return [pscustomobject]@{ parents = @([pscustomobject]@{ sha = 'p1' }, [pscustomobject]@{ sha = 'p2' }) }
}
$prev = Get-E2ePreviousCommit -Repo 'owner/GitStack' -Sha 'abc123'
Assert-That 'precedente: primo genitore' ($prev -eq 'p1')
Assert-That 'precedente: URL dell''API del commit' ($script:LastUri -eq 'https://api.github.com/repos/owner/GitStack/commits/abc123')

# Install con hosting acceso (GIT-388): solo se c'e' un dominio.
$cmdApps = Get-E2eInstallCommand -InstallRef 'abc123' -AppsDomain 'apps.e2e.test'
Assert-That 'install: con dominio accende l''hosting' ($cmdApps -like '*bash -s -- --enable-apps --apps-domain apps.e2e.test')

# Test-E2eAppAfterRestore: sette righe sempre, con HTTP e VM finti.
$script:OutDir = Join-Path ([IO.Path]::GetTempPath()) 'gs-phases-tests'
$script:SshCommandTimeoutSeconds = 5
$script:Mock = @{ Status = 'running'; V1 = 200; V2 = 200; V3 = 404; Inspect = "deploy=yes`nimage=10.0.0.9/u1/r1/e2eimg:v2`nvar=match`nsecret=match`n" }
function Start-Sleep { param([int]$Seconds) }
function Copy-ToVm { param($LocalPath, $RemotePath, $TimeoutSeconds) return [pscustomobject]@{ ExitCode = 0; StdOut = ''; StdErr = '' } }
function Invoke-VmSsh { param($Command, $TimeoutSeconds) return [pscustomobject]@{ ExitCode = 0; StdOut = $script:Mock.Inspect; StdErr = '' } }
function Invoke-HttpRaw {
    param([string]$Uri, [string]$Method = 'GET', [string]$Body, [hashtable]$Headers, [string]$Cookie, [int]$TimeoutSec = 20)
    if ($Uri -like '*/manifests/v1') { return [pscustomobject]@{ StatusCode = $script:Mock.V1; Body = '' } }
    if ($Uri -like '*/manifests/v2') { return [pscustomobject]@{ StatusCode = $script:Mock.V2; Body = '' } }
    if ($Uri -like '*/manifests/v3') { return [pscustomobject]@{ StatusCode = $script:Mock.V3; Body = '' } }
    return [pscustomobject]@{ StatusCode = 200; Body = (@{ status = $script:Mock.Status } | ConvertTo-Json) }
}
$ctx = [pscustomobject]@{ Namespace = 'gitstack-app-x'; ActiveVersion = 4; ActiveRef = '10.0.0.5/u1/r1/e2eimg:v2'; VarValue = 'a'; SecretValue = 'b'; ImagePath = 'u1/r1/e2eimg' }
$rows = Test-E2eAppAfterRestore -BaseUrl 'https://10.0.0.9' -Data $data -Ctx $ctx -WorkDir (Join-Path $script:OutDir 'a')
Assert-That 'app dopo il restore: sette righe' (@($rows).Count -eq 7)
Assert-That 'app dopo il restore: tutte ok (host del registry diverso ammesso)' (@($rows | Where-Object { -not $_.Ok }).Count -eq 0)
$script:Mock.V3 = 200; $script:Mock.V1 = 404; $script:Mock.Status = 'starting'
$script:Mock.Inspect = "deploy=yes`nimage=10.0.0.9/u1/r1/e2eimg:v1`nvar=missing`nsecret=mismatch`n"
$rows = Test-E2eAppAfterRestore -BaseUrl 'https://10.0.0.9' -Data $data -Ctx $ctx -WorkDir (Join-Path $script:OutDir 'b')
$ko = @($rows | Where-Object { -not $_.Ok } | ForEach-Object { $_.Name.Substring(0, 10) })
Assert-That 'app dopo il restore: ko su 2, 3, 4, 5, 6 e 7, ok su 1' (@($rows).Count -eq 7 -and $rows[0].Ok -and $ko.Count -eq 6)
$script:Mock.Inspect = "deploy=no`nimage=`nvar=missing`nsecret=missing`n"
$rows = Test-E2eAppAfterRestore -BaseUrl 'https://10.0.0.9' -Data $data -Ctx $ctx -WorkDir (Join-Path $script:OutDir 'c')
Assert-That 'app dopo il restore: senza Deployment la riga 1 e ko' (-not $rows[0].Ok -and -not $rows[1].Ok)
Remove-Item -LiteralPath $script:OutDir -Recurse -Force -ErrorAction SilentlyContinue

if ($failures.Count -gt 0) {
    Write-Host "FALLITE: $($failures.Count)"
    exit 1
}
Write-Host 'Tutte le verifiche sono passate.'
exit 0
