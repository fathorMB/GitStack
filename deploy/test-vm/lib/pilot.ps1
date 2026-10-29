# Pilot GIT-381: prepare the pull mirror and check the tag-triggered CI.
# Called after e12, which installs CI and the rootless build runner.
function Invoke-E2ePilotMirror {
    param(
        [Parameter(Mandatory)][string]$BaseUrl,
        [Parameter(Mandatory)][string]$Owner,
        [Parameter(Mandatory)][string]$Token,
        [Parameter(Mandatory)][string]$Suffix,
        [Parameter(Mandatory)][string]$AliasIp,
        [Parameter(Mandatory)][int]$OriginPort
    )
    $script:PilotContext = $null
    $repo = "pilot-$Suffix"
    $tag = 'v1.0.0'
    $originUser = 'pilot-source'
    $originToken = 'e2e-' + [guid]::NewGuid().ToString('N')
    $headers = @{ Authorization = "Bearer $Token" }
    $api = "$BaseUrl/api/v1/repos/$Owner/$repo"
    $stage = 'preparazione origine'
    $started = $false
    $wired = $false
    try {
        $mk = Invoke-VmSsh -Command 'mkdir -p /tmp/gitstack-e2e/pilot-fixture' -TimeoutSeconds 60
        if ($mk.ExitCode -ne 0) { throw 'cartella fixture sulla VM non creata' }
        foreach ($file in @('Dockerfile.api', 'Dockerfile.sito', 'Dockerfile.db', 'server.cjs', 'identity.cjs', 'workflow.yml')) {
            $copy = Copy-ToVm -LocalPath (Join-Path $PSScriptRoot "..\e2e\pilot\$file") -RemotePath "/tmp/gitstack-e2e/pilot-fixture/$file" -TimeoutSeconds 60
            if ($copy.ExitCode -ne 0) { throw "copia fixture ${file}: exit $($copy.ExitCode)" }
        }
        foreach ($file in @('mirror-dest.py', 'pilot-origin.sh')) {
            $copy = Copy-ToVm -LocalPath (Join-Path $PSScriptRoot "..\e2e\$file") -RemotePath "/tmp/gitstack-e2e/$file" -TimeoutSeconds 60
            if ($copy.ExitCode -ne 0) { throw "copia ${file}: exit $($copy.ExitCode)" }
        }
        $started = $true
        $start = Invoke-RemoteHelper -RemoteArgs @('mirror-dest-start', "$OriginPort", $originUser, $originToken, $AliasIp) -TimeoutSeconds 90
        if ($start.ExitCode -ne 0) { throw "avvio origine: exit $($start.ExitCode)" }
        $prepare = Invoke-VmSsh -Command 'bash /tmp/gitstack-e2e/pilot-origin.sh prepare /tmp/gitstack-e2e/mirror-dest/repos/mirror.git /tmp/gitstack-e2e/pilot-fixture /tmp/gitstack-e2e/pilot-source' -TimeoutSeconds 120
        if ($prepare.ExitCode -ne 0 -or $prepare.StdOut -notmatch 'STEP ok origin-prepare') { throw "preparazione origine: exit $($prepare.ExitCode) $($prepare.StdErr)" }
        $sha = (($prepare.StdOut -split "`r?`n") | Where-Object { $_ -match '^[0-9a-f]{40}$' } | Select-Object -First 1)
        if (-not $sha) { throw 'origine senza SHA' }
        Add-StepResult -Name 'pilot 1a. origine con workflow e immagini di prova' -Ok $true -Detail ''

        $stage = 'import mirror privato'
        $wired = $true
        $wire = Invoke-RemoteHelper -RemoteArgs @('mirror-enable', 'gitstack', 'default', $AliasIp) -TimeoutSeconds 600
        if ($wire.ExitCode -ne 0) { throw "mirror-enable: exit $($wire.ExitCode)" }
        $originUrl = "https://${AliasIp}:$OriginPort/mirror.git"
        $imp = Invoke-RepoImportWithRetry -Uri "$BaseUrl/api/v1/repos/import" -Headers $headers -MaxAttempts 4 -DelaySeconds 10 -Body (@{
            owner = $Owner; name = $repo; visibility = 'private'; url = $originUrl; mode = 'mirror'
            username = $originUser; token = $originToken; intervalSeconds = 3600
        } | ConvertTo-Json)
        if ($imp.StatusCode -ne 201 -or $imp.Body.Contains($originToken)) { throw "import: status $($imp.StatusCode) o credenziale esposta" }
        $ready = $false
        for ($i = 0; $i -lt 36; $i++) {
            $mirror = Invoke-HttpRaw -Uri "$api/pull-mirror" -Headers $headers
            if ($mirror.StatusCode -eq 200 -and ($mirror.Body | ConvertFrom-Json).state -eq 'in_sync') { $ready = $true; break }
            Start-Sleep -Seconds 5
        }
        if (-not $ready) { throw 'prima sincronizzazione non in_sync' }
        $repoInfo = Invoke-HttpRaw -Uri $api -Headers $headers
        if ($repoInfo.StatusCode -ne 200 -or ($repoInfo.Body | ConvertFrom-Json).visibility -ne 'private') { throw 'repo non privato o non leggibile' }
        Add-StepResult -Name 'pilot 1b. repo privato in mirror sincronizzato' -Ok $true -Detail ''

        $stage = 'accensione CI mirror'
        $perm = Invoke-HttpRaw -Uri "$api/actions/permissions" -Method 'PUT' -Headers $headers -Body (@{ contents = 'read'; packages = 'write'; actions = 'read' } | ConvertTo-Json)
        if ($perm.StatusCode -ne 200) { throw "permessi job: status $($perm.StatusCode)" }
        $enable = Invoke-HttpRaw -Uri "$api/actions/pull-mirror" -Method 'PUT' -Headers $headers -Body (@{ runOnPullMirror = $true } | ConvertTo-Json)
        if ($enable.StatusCode -ne 200 -or -not ($enable.Body | ConvertFrom-Json).runOnPullMirror) { throw "accensione CI: status $($enable.StatusCode)" }
        Add-StepResult -Name 'pilot 1c. CI accesa sul mirror' -Ok $true -Detail ''

        $stage = 'tag e sincronizzazione'
        $tagResult = Invoke-VmSsh -Command "bash /tmp/gitstack-e2e/pilot-origin.sh tag /tmp/gitstack-e2e/pilot-source $tag" -TimeoutSeconds 120
        if ($tagResult.ExitCode -ne 0 -or $tagResult.StdOut -notmatch 'STEP ok origin-tag' -or $tagResult.StdOut -notmatch [regex]::Escape($sha)) { throw 'creazione tag origine fallita o SHA diverso' }
        $sync = Invoke-HttpRaw -Uri "$api/pull-mirror/sync" -Method 'POST' -Headers $headers
        if ($sync.StatusCode -ne 202) { throw "sync: status $($sync.StatusCode)" }
        Add-StepResult -Name 'pilot 2a. tag nuovo e sincronizzazione richiesta' -Ok $true -Detail ''

        $stage = 'run del tag'
        $run = $null
        for ($i = 0; $i -lt 180; $i++) {
            $response = Invoke-HttpRaw -Uri "$api/actions/runs" -Headers $headers
            if ($response.StatusCode -eq 200) {
                $runs = @((($response.Body | ConvertFrom-Json).items) | Where-Object { $_.event -eq 'push' -and $_.ref -eq "refs/tags/$tag" -and $_.sha -eq $sha })
                $run = $runs | Select-Object -First 1
                if ($run -and ($run.status -eq 'completed' -or $run.status -eq 'cancelled')) { break }
            }
            Start-Sleep -Seconds 5
        }
        if (-not $run -or $run.status -ne 'completed' -or $run.conclusion -ne 'success' -or @($run.jobs | Where-Object { $_.runnerType -eq 'internal_pod' -and $_.status -eq 'completed' -and $_.conclusion -eq 'success' }).Count -eq 0) { throw 'run del tag non completato con successo sul runner integrato' }
        Add-StepResult -Name 'pilot 2b. run del tag riuscito sul runner integrato' -Ok $true -Detail "run $($run.id)"

        $stage = 'immagini firmate'
        $packages = Invoke-HttpRaw -Uri "$api/packages" -Headers $headers
        if ($packages.StatusCode -ne 200) { throw "packages: status $($packages.StatusCode)" }
        # GIT-507: la risposta grezza resta nella cartella del giro per la diagnosi.
        $outVar = Get-Variable -Name OutDir -Scope Script -ErrorAction SilentlyContinue
        if ($outVar -and $outVar.Value) {
            try { Set-Content -LiteralPath (Join-Path $outVar.Value 'pilot-packages.json') -Value $packages.Body -Encoding utf8 } catch { }
        }
        $items = @(($packages.Body | ConvertFrom-Json).items)
        foreach ($name in @('api', 'sito', 'db')) {
            # GIT-507: core dà name = owner/repo/immagine (packageToAPI), non il solo nome dell'immagine.
            $fullName = "$Owner/$repo/$name"
            $image = @($items | Where-Object { $_.name -eq $fullName })
            $signed = @($image | ForEach-Object { $_.tagDetails } | Where-Object { $_.name -eq $tag -and $_.signature -eq 'verified' -and $_.pushedBy.kind -eq 'job' -and $_.digest -match '^sha256:[0-9a-f]{64}$' })
            if ($signed.Count -lt 1) {
                if ($image.Count -eq 0) {
                    $present = (@($items | ForEach-Object { $_.name }) -join ', ')
                    throw "$name $tag non firmata dal job: immagine $fullName assente, name presenti: [$present]"
                }
                $rowsText = @($image | ForEach-Object { $_.tagDetails } | ForEach-Object { "name=$($_.name) digest=$($_.digest) signature=$($_.signature) pushedBy.kind=$($_.pushedBy.kind)" }) -join '; '
                throw "$name $tag non firmata dal job: tagDetails [$rowsText]"
            }
            Add-StepResult -Name "pilot 2c. immagine $name firmata dal job" -Ok $true -Detail $signed[0].digest
        }
        $script:PilotContext = [pscustomobject]@{ Owner = $Owner; Repo = $repo; Tag = $tag; Sha = $sha; Token = $Token }
    } catch {
        Add-StepResult -Name "pilot: $stage" -Ok $false -Detail $_.Exception.Message
    } finally {
        if ($wired) {
            $unwire = Invoke-RemoteHelper -RemoteArgs @('mirror-disable', 'gitstack', 'default') -TimeoutSeconds 600
            if ($unwire.ExitCode -ne 0) { Add-StepResult -Name 'pilot: rimozione configurazione mirror' -Ok $false -Detail "exit $($unwire.ExitCode)" }
        }
        if ($started) {
            $stop = Invoke-RemoteHelper -RemoteArgs @('mirror-dest-stop') -TimeoutSeconds 60
            if ($stop.ExitCode -ne 0) { Add-StepResult -Name 'pilot: arresto origine' -Ok $false -Detail "exit $($stop.ExitCode)" }
        }
    }
}

function Wait-E2ePilotServing {
    param([string]$AppsApi, [string]$Name, [hashtable]$Headers, [int]$TimeoutSeconds = 300)
    $until = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $until) {
        $r = Invoke-HttpRaw -Uri "$AppsApi/$Name/versions?per_page=1" -Headers $Headers
        if ($r.StatusCode -eq 200) {
            $v = @(($r.Body | ConvertFrom-Json).items) | Select-Object -First 1
            if ($v -and $v.state -eq 'serving') { return }
            if ($v -and ($v.state -eq 'failed' -or $v.state -eq 'rolled_back')) { throw "$Name stato $($v.state): $($v.stateReason)" }
        }
        Start-Sleep -Seconds 5
    }
    throw "$Name non e serving entro $TimeoutSeconds secondi"
}

function Initialize-E2ePilotDatabase {
    param([Parameter(Mandatory)][string]$BaseUrl, [Parameter(Mandatory)]$Context)
    $api = "$BaseUrl/api/v1/repos/$($Context.Owner)/$($Context.Repo)/apps"
    $headers = @{ Authorization = "Bearer $($Context.Token)" }
    $password = 'e2e-' + [guid]::NewGuid().ToString('N')
    $stage = 'app postgres'
    try {
        $db = Invoke-HttpRaw -Uri $api -Method 'POST' -Headers $headers -Body (@{ name = 'postgres'; image = "db:$($Context.Tag)"; port = 5432 } | ConvertTo-Json)
        if ($db.StatusCode -ne 202) { throw "creazione postgres: status $($db.StatusCode)" }
        $pgdata = Invoke-HttpRaw -Uri "$api/postgres/variables/PGDATA" -Method 'PUT' -Headers $headers -Body (@{ value = '/var/lib/postgresql/data/pgdata' } | ConvertTo-Json)
        if ($pgdata.StatusCode -ne 200) { throw "variabile PGDATA: status $($pgdata.StatusCode)" }
        $secret = Invoke-HttpRaw -Uri "$api/postgres/secrets/POSTGRES_PASSWORD" -Method 'PUT' -Headers $headers -Body (@{ value = $password } | ConvertTo-Json)
        if ($secret.StatusCode -ne 200) { throw "segreto postgres: status $($secret.StatusCode)" }
        Wait-E2ePilotServing -AppsApi $api -Name 'postgres' -Headers $headers
        Add-StepResult -Name 'pilot 3a. postgres in salute con password segreta' -Ok $true -Detail ''

        $stage = 'volume postgres'
        $volume = Invoke-HttpRaw -Uri "$api/postgres/volumes" -Method 'POST' -Headers $headers -Body (@{ name = 'data'; mountPath = '/var/lib/postgresql/data'; sizeBytes = 1073741824 } | ConvertTo-Json)
        if ($volume.StatusCode -ne 201 -and $volume.StatusCode -ne 202) { throw "volume: status $($volume.StatusCode)" }
        Wait-E2ePilotServing -AppsApi $api -Name 'postgres' -Headers $headers
        $volumes = Invoke-HttpRaw -Uri "$api/postgres/volumes" -Headers $headers
        if ($volumes.StatusCode -ne 200 -or @((($volumes.Body | ConvertFrom-Json).items) | Where-Object { $_.name -eq 'data' -and $_.includeInBackup -eq $true }).Count -ne 1) { throw 'volume data non incluso nel backup' }
        Add-StepResult -Name 'pilot 3b. postgres con volume incluso nel backup' -Ok $true -Detail ''

        $stage = 'app api'
        $created = Invoke-HttpRaw -Uri $api -Method 'POST' -Headers $headers -Body (@{ name = 'api'; image = "api:$($Context.Tag)"; port = 8080 } | ConvertTo-Json)
        if ($created.StatusCode -ne 202) { throw "creazione api: status $($created.StatusCode)" }
        $appId = ($created.Body | ConvertFrom-Json).app.id
        if ($appId -notmatch '^[0-9a-f-]{36}$') { throw 'app api senza id valido' }
        $secret = Invoke-HttpRaw -Uri "$api/api/secrets/PGPASSWORD" -Method 'PUT' -Headers $headers -Body (@{ value = $password } | ConvertTo-Json)
        if ($secret.StatusCode -ne 200) { throw "segreto api: status $($secret.StatusCode)" }
        Wait-E2ePilotServing -AppsApi $api -Name 'api' -Headers $headers
        Add-StepResult -Name 'pilot 3c. api in salute con immagine del repo e password segreta' -Ok $true -Detail ''

        $stage = 'scrittura PostgreSQL via api'
        $copy = Copy-ToVm -LocalPath (Join-Path $PSScriptRoot '..\e2e\pilot-db-probe.sh') -RemotePath '/tmp/gitstack-e2e/pilot-db-probe.sh' -TimeoutSeconds 60
        if ($copy.ExitCode -ne 0) { throw "copia probe: exit $($copy.ExitCode)" }
        $write = Invoke-VmSsh -Command "bash /tmp/gitstack-e2e/pilot-db-probe.sh $appId write" -TimeoutSeconds 120
        if ($write.ExitCode -ne 0 -or $write.StdOut -notmatch 'STEP ok database-write') { throw "scrittura via api: exit $($write.ExitCode)" }
        $Context | Add-Member -NotePropertyName ApiId -NotePropertyValue $appId -Force
        Add-StepResult -Name 'pilot 4. api scrive e rilegge PostgreSQL per nome postgres' -Ok $true -Detail ''
        return $true
    } catch {
        Add-StepResult -Name "pilot: $stage" -Ok $false -Detail $_.Exception.Message
        return $false
    }
}

function Test-E2ePilotDatabaseAfterRestore {
    param([Parameter(Mandatory)][string]$BaseUrl, [Parameter(Mandatory)]$Context)
    $api = "$BaseUrl/api/v1/repos/$($Context.Owner)/$($Context.Repo)/apps"
    $headers = @{ Authorization = "Bearer $($Context.Token)" }
    try {
        Wait-E2ePilotServing -AppsApi $api -Name 'postgres' -Headers $headers
        Wait-E2ePilotServing -AppsApi $api -Name 'api' -Headers $headers
        $id = $Context.ApiId
        if ($id -notmatch '^[0-9a-f-]{36}$') { throw 'id api non valido' }
        $copy = Copy-ToVm -LocalPath (Join-Path $PSScriptRoot '..\e2e\pilot-db-probe.sh') -RemotePath '/tmp/gitstack-e2e/pilot-db-probe.sh' -TimeoutSeconds 60
        if ($copy.ExitCode -ne 0) { throw "copia probe: exit $($copy.ExitCode)" }
        $read = Invoke-VmSsh -Command "bash /tmp/gitstack-e2e/pilot-db-probe.sh $id data" -TimeoutSeconds 120
        if ($read.ExitCode -ne 0 -or $read.StdOut -notmatch 'STEP ok database-data') { throw "lettura dopo restore: exit $($read.ExitCode)" }
        Add-StepResult -Name 'pilot 7. dato PostgreSQL conservato dopo backup e restore' -Ok $true -Detail ''
    } catch {
        Add-StepResult -Name 'pilot 7. dato PostgreSQL conservato dopo backup e restore' -Ok $false -Detail $_.Exception.Message
    }
}

function Initialize-E2ePilotSite {
    param(
        [Parameter(Mandatory)][string]$BaseUrl,
        [Parameter(Mandatory)][string]$AdminCookie,
        [Parameter(Mandatory)][string]$Suffix,
        [Parameter(Mandatory)][string]$CaFingerprint,
        [Parameter(Mandatory)]$Context
    )
    $repoApi = "$BaseUrl/api/v1/repos/$($Context.Owner)/$($Context.Repo)"
    $apps = "$repoApi/apps"
    $headers = @{ Authorization = "Bearer $($Context.Token)" }
    $tester = "e2etester$Suffix"
    $bot = "e2epilotbot$Suffix"
    $password = 'E2e-tester-' + [guid]::NewGuid().ToString('N')
    $expires = (Get-Date).ToUniversalTime().AddHours(6).ToString('yyyy-MM-ddTHH:mm:ssZ')
    $stage = 'utente tester e bot'
    try {
        $u = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/users" -Method 'POST' -Cookie $AdminCookie -Body (@{ username = $tester; email = "$tester@example.invalid"; password = $password } | ConvertTo-Json)
        if ($u.StatusCode -ne 201) { throw "creazione tester: status $($u.StatusCode)" }
        $login = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/auth/login" -Method 'POST' -Body (@{ username = $tester; password = $password } | ConvertTo-Json)
        if ($login.StatusCode -ne 200 -or -not $login.Cookie) { throw "login tester: status $($login.StatusCode)" }
        $testerTokenResponse = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/user/tokens" -Method 'POST' -Cookie $login.Cookie -Body (@{ name = 'pilot-tester'; scopes = @('read:resource'); expiresAt = $expires } | ConvertTo-Json)
        if ($testerTokenResponse.StatusCode -ne 201) { throw "token tester: status $($testerTokenResponse.StatusCode)" }
        $testerToken = ($testerTokenResponse.Body | ConvertFrom-Json).token
        if (-not $testerToken) { throw 'token tester mancante' }
        $agent = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/users" -Method 'POST' -Cookie $AdminCookie -Body (@{ username = $bot; kind = 'agent'; email = "$bot@example.invalid" } | ConvertTo-Json)
        if ($agent.StatusCode -ne 201) { throw "creazione bot: status $($agent.StatusCode)" }
        $botId = ($agent.Body | ConvertFrom-Json).id
        if (-not $botId) { throw 'id bot mancante' }
        $agentTokenResponse = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/users/$bot/tokens" -Method 'POST' -Cookie $AdminCookie -Body (@{ name = 'pilot-bot'; scopes = @('read:resource', 'write:resource'); expiresAt = $expires } | ConvertTo-Json)
        if ($agentTokenResponse.StatusCode -ne 201) { throw "token bot: status $($agentTokenResponse.StatusCode)" }
        $botToken = ($agentTokenResponse.Body | ConvertFrom-Json).token
        if (-not $botToken) { throw 'token bot mancante' }
        $repo = Invoke-HttpRaw -Uri $repoApi -Headers $headers
        if ($repo.StatusCode -ne 200) { throw "repo: status $($repo.StatusCode)" }
        $repoId = ($repo.Body | ConvertFrom-Json).id
        if (-not $repoId) { throw 'id repo mancante' }
        $grant = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/resources/$repoId/grants" -Method 'POST' -Headers $headers -Body (@{ subjectType = 'user'; subjectId = $botId; role = 'write' } | ConvertTo-Json)
        if ($grant.StatusCode -ne 201) { throw "grant bot: status $($grant.StatusCode)" }
        Add-StepResult -Name 'pilot 5a. tester e bot dedicati, grant bot sul repo' -Ok $true -Detail ''

        $stage = 'deploy sito'
        $site = Invoke-HttpRaw -Uri $apps -Method 'POST' -Headers $headers -Body (@{ name = 'sito'; image = "sito:$($Context.Tag)"; port = 8080 } | ConvertTo-Json)
        if ($site.StatusCode -ne 202) { throw "creazione sito: status $($site.StatusCode)" }
        Wait-E2ePilotServing -AppsApi $apps -Name 'sito' -Headers $headers
        $var = Invoke-HttpRaw -Uri "$apps/sito/variables/PILOT_REPO" -Method 'PUT' -Headers $headers -Body (@{ value = "$($Context.Owner)/$($Context.Repo)" } | ConvertTo-Json)
        if ($var.StatusCode -ne 200) { throw "variabile repo: status $($var.StatusCode)" }
        Wait-E2ePilotServing -AppsApi $apps -Name 'sito' -Headers $headers
        $secret = Invoke-HttpRaw -Uri "$apps/sito/secrets/BOT_TOKEN" -Method 'PUT' -Headers $headers -Body (@{ value = $botToken } | ConvertTo-Json)
        if ($secret.StatusCode -ne 200) { throw "segreto bot: status $($secret.StatusCode)" }
        Wait-E2ePilotServing -AppsApi $apps -Name 'sito' -Headers $headers
        $settings = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/admin/apps/settings" -Cookie $AdminCookie
        if ($settings.StatusCode -ne 200 -or ($settings.Body | ConvertFrom-Json).publicAppsEnabled -ne $false) { throw 'interruttore app pubbliche non risulta spento' }
        $access = Invoke-HttpRaw -Uri "$apps/sito/access" -Headers $headers
        if ($access.StatusCode -ne 200 -or ($access.Body | ConvertFrom-Json).mode -ne 'login') { throw 'sito non protetto dal login' }
        Add-StepResult -Name 'pilot 5b. sito in salute con immagine del repo e token bot segreto' -Ok $true -Detail ''

        $stage = 'lista accesso tester'
        $member = Invoke-HttpRaw -Uri "$apps/sito/access/members" -Method 'POST' -Headers $headers -Body (@{ kind = 'user'; name = $tester } | ConvertTo-Json)
        if ($member.StatusCode -ne 201) { throw "aggiunta tester: status $($member.StatusCode)" }
        $list = Invoke-HttpRaw -Uri "$apps/sito/access/members" -Headers $headers
        if ($list.StatusCode -ne 200 -or @((($list.Body | ConvertFrom-Json).items) | Where-Object { $_.kind -eq 'user' -and $_.name -eq $tester }).Count -ne 1) { throw 'tester non presente nella lista' }
        $testerHeaders = @{ Authorization = "Bearer $testerToken" }
        foreach ($path in @('', '/contents?path=server.cjs', '/issues')) {
            $hidden = Invoke-HttpRaw -Uri "$repoApi$path" -Headers $testerHeaders
            if ($hidden.StatusCode -ne 404) { throw "tester vede repo o issues: status $($hidden.StatusCode)" }
        }
        Add-StepResult -Name 'pilot 5c. tester in lista, repo e issues nascosti' -Ok $true -Detail ''

        $stage = 'invito e impronta CA'
        $invite = Invoke-HttpRaw -Uri "$apps/sito/access/invite" -Headers $headers
        if ($invite.StatusCode -ne 200) { throw "invito: status $($invite.StatusCode)" }
        $info = $invite.Body | ConvertFrom-Json
        $actual = ([string]$info.fingerprintSha256 -replace '[^0-9a-fA-F]', '').ToLowerInvariant()
        $expected = ($CaFingerprint -replace '[^0-9a-fA-F]', '').ToLowerInvariant()
        if ($info.caMode -ne 'internal' -or $actual.Length -ne 64 -or $actual -ne $expected -or -not $info.address -or -not $info.guideUrl) { throw 'invito senza indirizzo o impronta CA diversa dalla CA scaricata' }
        $caInfo = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/instance/ca"
        if ($caInfo.StatusCode -ne 200) { throw "CA della guida: status $($caInfo.StatusCode)" }
        $guideFingerprint = ([string](($caInfo.Body | ConvertFrom-Json).fingerprintSha256) -replace '[^0-9a-fA-F]', '').ToLowerInvariant()
        if ($guideFingerprint -ne $expected) { throw 'impronta della guida diversa dalla CA scaricata' }
        Add-StepResult -Name 'pilot 5d. impronta CA dell invito corrisponde alla CA scaricata' -Ok $true -Detail ''
        return [pscustomobject]@{ Address = [string]$info.address; Tester = $tester; TesterToken = $testerToken; Bot = $bot }
    } catch {
        Add-StepResult -Name "pilot: $stage" -Ok $false -Detail $_.Exception.Message
        return $null
    }
}

function Invoke-E2ePilotTester {
    param(
        [Parameter(Mandatory)][string]$BaseUrl,
        [Parameter(Mandatory)][string]$CaPath,
        [Parameter(Mandatory)][string]$WorkDir,
        [Parameter(Mandatory)]$Context,
        [Parameter(Mandatory)]$Site
    )
    $stage = 'certificato CA e browser'
    $added = $false
    $certPath = $null
    $savedEnv = @{}
    foreach ($name in @('PILOT_SITE_URL', 'PILOT_VM_IP', 'PILOT_TESTER_TOKEN', 'PILOT_SCREENSHOT')) {
        $savedEnv[$name] = [Environment]::GetEnvironmentVariable($name)
    }
    try {
        New-Item -ItemType Directory -Path $WorkDir -Force | Out-Null
        $cert = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($CaPath)
        $certPath = 'Cert:\CurrentUser\Root\' + $cert.Thumbprint
        if (-not (Test-Path -LiteralPath $certPath)) {
            $null = Import-Certificate -FilePath $CaPath -CertStoreLocation 'Cert:\CurrentUser\Root'
            $added = $true
        }
        if (-not (Test-Path -LiteralPath $certPath)) { throw 'CA non presente nello store del tester' }
        $screen = Join-Path $WorkDir 'screenshot.png'
        $env:PILOT_SITE_URL = $Site.Address
        $env:PILOT_VM_IP = $script:VmIp
        $env:PILOT_TESTER_TOKEN = $Site.TesterToken
        $env:PILOT_SCREENSHOT = $screen
        $browser = Invoke-ExternalCommand -FilePath 'node' -ArgumentList @((Join-Path $PSScriptRoot '..\e2e\pilot-browser.cjs')) -TimeoutSeconds 240
        Write-E2eText -Path (Join-Path $WorkDir 'browser.log') -Stdout $browser.StdOut -Stderr $browser.StdErr
        if ($browser.ExitCode -ne 0 -or $browser.StdOut -notmatch 'STEP ok anonymous-blocked' -or $browser.StdOut -notmatch 'STEP ok tester-https-data' -or $browser.StdOut -notmatch 'STEP ok tester-report' -or $browser.StdOut -notmatch '(?m)^ISSUE=([0-9]+)\s*$' -or -not (Test-Path -LiteralPath $screen)) {
            throw "browser fallito: exit $($browser.ExitCode), log in $WorkDir"
        }
        $number = [int]$Matches[1]
        Add-StepResult -Name 'pilot 5e. tester apre HTTPS con CA verificata e legge il dato' -Ok $true -Detail ''

        $stage = 'issue e allegato'
        $repoApi = "$BaseUrl/api/v1/repos/$($Context.Owner)/$($Context.Repo)"
        $headers = @{ Authorization = "Bearer $($Context.Token)" }
        $issue = Invoke-HttpRaw -Uri "$repoApi/issues/$number" -Headers $headers
        if ($issue.StatusCode -ne 200) { throw "lettura issue: status $($issue.StatusCode)" }
        $item = $issue.Body | ConvertFrom-Json
        $attachments = @($item.attachments | Where-Object { $_.filename -eq 'screenshot.png' -and $_.contentType -eq 'image/png' })
        if ($item.author.username -ne $Site.Bot -or $item.author.kind -ne 'agent' -or $item.body -ne "Tester: $($Site.Tester)" -or $attachments.Count -ne 1) { throw 'autore, tester o allegato inattesi' }
        $id = $attachments[0].id
        if ($id -notmatch '^[0-9a-f-]{36}$') { throw 'id allegato non valido' }
        $download = Join-Path $WorkDir 'allegato.png'
        $get = Invoke-HttpDownloadFile -Uri "$repoApi/issue-attachments/$id" -OutFile $download -Headers $headers
        if ($get.StatusCode -ne 200 -or (Get-FileHash -LiteralPath $download -Algorithm SHA256).Hash -ne (Get-FileHash -LiteralPath $screen -Algorithm SHA256).Hash) { throw 'allegato diverso dallo screenshot del tester' }
        Add-StepResult -Name 'pilot 6. issue del bot con tester e screenshot identico' -Ok $true -Detail "issue #$number"
    } catch {
        Add-StepResult -Name "pilot: $stage" -Ok $false -Detail $_.Exception.Message
    } finally {
        foreach ($name in $savedEnv.Keys) { [Environment]::SetEnvironmentVariable($name, $savedEnv[$name]) }
        if ($added) {
            try { Remove-Item -LiteralPath $certPath -Force -ErrorAction Stop }
            catch { Add-StepResult -Name 'pilot: rimozione CA temporanea del tester' -Ok $false -Detail $_.Exception.Message }
        }
    }
}
