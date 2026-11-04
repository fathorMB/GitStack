# Solo ASCII per Windows PowerShell 5.1. La CA e' gia verificata dal passo d3.
function Invoke-E2eExternalCIGateway {
    param([string]$BaseUrl, [string]$CaPath, $AdminCookie, [string]$WorkDir)
    $ok = $false; $detail = ''; $store = $null; $added = $false
    $oldToken = $env:GITSTACK_RUNNER_REGISTRATION_TOKEN
    $oldProgress = $ProgressPreference
    try {
        if (-not $AdminCookie -or -not (Test-Path -LiteralPath $CaPath)) { throw 'manca la sessione admin o la CA verificata' }
        New-Item -ItemType Directory -Force -Path $WorkDir | Out-Null
        $on = Invoke-VmSsh -Command 'sudo gitstack config set ci-gateway on' -TimeoutSeconds 360
        if ($on.ExitCode -ne 0) { throw "accensione gateway: exit $($on.ExitCode) $($on.StdErr)" }
        $reg = Invoke-HttpRaw -Uri "$BaseUrl/api/v1/admin/ci/runners/registration-token" -Method 'POST' -Cookie $AdminCookie
        if ($reg.StatusCode -ne 201) { throw "token di registrazione: HTTP $($reg.StatusCode)" }
        $env:GITSTACK_RUNNER_REGISTRATION_TOKEN = ($reg.Body | ConvertFrom-Json).token
        if (-not $env:GITSTACK_RUNNER_REGISTRATION_TOKEN) { throw 'token di registrazione mancante' }
        $exe = Join-Path $WorkDir 'gitstack-runner_windows_amd64.exe'
        $sums = Join-Path $WorkDir 'SHA256SUMS'
        $ProgressPreference = 'SilentlyContinue'
        Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/downloads/gitstack-runner_windows_amd64.exe" -OutFile $exe -TimeoutSec 120
        Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/downloads/SHA256SUMS" -OutFile $sums -TimeoutSec 30
        $line = @(Get-Content -LiteralPath $sums | Where-Object { $_ -match '^([0-9a-fA-F]{64})\s+\*?gitstack-runner_windows_amd64\.exe$' })
        if ($line.Count -ne 1) { throw 'checksum del runner mancante o duplicato' }
        $expected = ($line[0] -split '\s+')[0]
        if ((Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash -ne $expected) { throw 'checksum del runner errato' }
        Write-Log 'ok ci-gateway: checksum SHA256 del runner verificato'
        # La VM e2e richiede amministratore: lo store macchina evita il prompt
        # interattivo di conferma dello store CurrentUser su Windows.
        $ca = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2 -ArgumentList (Resolve-Path -LiteralPath $CaPath).Path
        $store = New-Object System.Security.Cryptography.X509Certificates.X509Store('Root', 'LocalMachine')
        $store.Open([System.Security.Cryptography.X509Certificates.OpenFlags]::ReadWrite)
        if ($store.Certificates.Find('FindByThumbprint', $ca.Thumbprint, $false).Count -eq 0) { $store.Add($ca); $added = $true }
        $url = "https://$($script:VmIp):8443"
        $deadline = (Get-Date).AddSeconds(180)
        do {
            $probe = Invoke-HttpRaw -Uri "$url/runner.v1.RunnerService/Register" -Method 'GET'
            if ($probe.StatusCode -gt 0) { break }
            Start-Sleep -Seconds 3
        } while ((Get-Date) -lt $deadline)
        if ($probe.StatusCode -eq 0) { throw 'gateway TLS non raggiungibile sulla porta 8443' }
        $runner = Invoke-ExternalCommand -FilePath $exe -ArgumentList @('register', '--url', $url, '--name', "vm-e2e-$PID", '--labels', 'windows', '--config', (Join-Path $WorkDir 'runner.json')) -TimeoutSeconds 60
        if ($runner.ExitCode -ne 0) { throw "register: exit $($runner.ExitCode) $($runner.StdErr)" }
        Write-Log "ok ci-gateway: $($runner.StdOut.Trim())"
        $off = Invoke-VmSsh -Command 'sudo gitstack config set ci-gateway off' -TimeoutSeconds 360
        if ($off.ExitCode -ne 0) { throw "spegnimento gateway: exit $($off.ExitCode) $($off.StdErr)" }
        $deadline = (Get-Date).AddSeconds(180)
        do {
            $tcp = New-Object System.Net.Sockets.TcpClient
            try { $pending = $tcp.BeginConnect($script:VmIp, 8443, $null, $null); $connected = $pending.AsyncWaitHandle.WaitOne(2000) -and $tcp.Connected } finally { $tcp.Close() }
            if (-not $connected) { break }
            Start-Sleep -Seconds 3
        } while ((Get-Date) -lt $deadline)
        if ($connected) { throw 'porta 8443 ancora aperta dopo off' }
        Write-Log 'ok ci-gateway: porta 8443 chiusa dopo off'
        $ok = $true
    } catch { $detail = $_.Exception.Message } finally {
        if (-not $ok) { $null = Invoke-VmSsh -Command 'sudo gitstack config set ci-gateway off' -TimeoutSeconds 360 }
        if ($store) { if ($added) { $store.Remove($ca) }; $store.Close() }
        $env:GITSTACK_RUNNER_REGISTRATION_TOKEN = $oldToken
        $ProgressPreference = $oldProgress
        Remove-Item -LiteralPath (Join-Path $WorkDir 'runner.json') -Force -ErrorAction SilentlyContinue
    }
    Add-StepResult -Name 'e12-gateway. runner esterno TLS su 8443, register e spegnimento' -Ok $ok -Detail $detail
}
