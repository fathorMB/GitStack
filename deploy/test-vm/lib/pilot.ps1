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
