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
        foreach ($file in @('Dockerfile.api', 'Dockerfile.sito', 'server.cjs', 'identity.cjs', 'workflow.yml')) {
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
        $items = @(($packages.Body | ConvertFrom-Json).items)
        foreach ($name in @('api', 'sito')) {
            $image = @($items | Where-Object { $_.name -eq $name })
            $signed = @($image | ForEach-Object { $_.tagDetails } | Where-Object { $_.name -eq $tag -and $_.signature -eq 'verified' -and $_.pushedBy.kind -eq 'job' -and $_.digest -match '^sha256:[0-9a-f]{64}$' })
            if ($signed.Count -lt 1) { throw "$name $tag non firmata dal job" }
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
        $db = Invoke-HttpRaw -Uri $api -Method 'POST' -Headers $headers -Body (@{ name = 'postgres'; image = 'docker.io/library/postgres:16'; port = 5432 } | ConvertTo-Json)
        if ($db.StatusCode -ne 202) { throw "creazione postgres: status $($db.StatusCode)" }
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
