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

if ($failures.Count -gt 0) {
    Write-Host "FALLITE: $($failures.Count)"
    exit 1
}
Write-Host 'Tutte le verifiche sono passate.'
exit 0
