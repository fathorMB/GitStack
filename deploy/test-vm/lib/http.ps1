# deploy/test-vm/lib/http.ps1
#
# Invoke-HttpRaw: chiamata HTTP per e2e.ps1 (dot-sourced) e per tests/http.Tests.ps1.
# Solo ASCII: Windows PowerShell 5.1 legge in ANSI i .ps1 senza BOM.

# Header di risposta (dizionario o WebHeaderCollection) in una hashtable di stringhe.
function ConvertTo-HeaderTable {
    param($Source)
    $t = @{}
    if ($null -eq $Source) { return $t }
    foreach ($k in @($Source.Keys)) {
        $t[[string]$k] = (@($Source[$k]) | ForEach-Object { [string]$_ }) -join ','
    }
    return $t
}

# Chiamata HTTP che non solleva eccezioni sugli status 4xx/5xx (Windows
# PowerShell 5.1 lancia una WebException): ritorna StatusCode, Body (testo),
# Cookie (gst_session=<valore> dal Set-Cookie, se presente), Headers (hashtable
# degli header di risposta, nomi senza distinzione di maiuscole, valori stringa)
# oppure StatusCode 0 con Error se la connessione fallisce.
#
# -Cookie 'nome=valore': su Windows PowerShell 5.1 Invoke-WebRequest ignora un
# header Cookie passato in -Headers (usa il proprio CookieContainer), quindi il
# cookie va in una WebRequestSession, NON Secure (un cookie Secure non parte su
# http), con dominio = host dell'URI.
function Invoke-HttpRaw {
    param(
        [Parameter(Mandatory)][string]$Uri,
        [string]$Method = 'GET',
        [string]$Body,
        [hashtable]$Headers,
        [string]$Cookie,
        [int]$TimeoutSec = 20
    )
    $params = @{ Uri = $Uri; Method = $Method; TimeoutSec = $TimeoutSec; UseBasicParsing = $true }
    if ($Body) { $params.Body = $Body; $params.ContentType = 'application/json' }
    if ($Headers) { $params.Headers = $Headers }
    if ($Cookie) {
        $idx = $Cookie.IndexOf('=')
        if ($idx -lt 1) { throw "Invoke-HttpRaw: -Cookie deve essere nome=valore (ricevuto '$Cookie')" }
        $session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
        $ck = New-Object System.Net.Cookie($Cookie.Substring(0, $idx), $Cookie.Substring($idx + 1), '/', ([Uri]$Uri).Host)
        $ck.Secure = $false
        $session.Cookies.Add($ck)
        $params.WebSession = $session
    }
    try {
        $r = Invoke-WebRequest @params
        $text = if ($r.Content -is [byte[]]) { [Text.Encoding]::UTF8.GetString($r.Content) } else { [string]$r.Content }
        # Il cookie di sessione e' Secure: il cookie jar non lo rimanda su HTTP,
        # quindi lo si estrae dal Set-Cookie e lo si rimanda con -Cookie.
        $sessionCookie = $null
        $setCookie = @($r.Headers['Set-Cookie']) -join ','
        if ($setCookie -match 'gst_session=([^;,\s]+)') { $sessionCookie = "gst_session=$($Matches[1])" }
        return [pscustomobject]@{ StatusCode = [int]$r.StatusCode; Body = $text; Error = $null; Cookie = $sessionCookie; Headers = (ConvertTo-HeaderTable $r.Headers) }
    } catch {
        $resp = $_.Exception.Response
        if ($null -ne $resp) {
            $text = ''
            # 1) su alcune build 5.1 il corpo e' in ErrorDetails.Message e lo
            #    stream e' gia' consumato; 2) altrimenti si rilegge lo stream
            #    da Position 0 se CanSeek.
            if ($_.ErrorDetails -and -not [string]::IsNullOrEmpty($_.ErrorDetails.Message)) {
                $text = $_.ErrorDetails.Message
            } else {
                try {
                    $stream = $resp.GetResponseStream()
                    if ($stream.CanSeek) { $stream.Position = 0 }
                    $reader = New-Object System.IO.StreamReader($stream)
                    $text = $reader.ReadToEnd()
                    $reader.Close()
                } catch { $text = '' }
            }
            $setCookie = ''
            try { $setCookie = [string]$resp.Headers['Set-Cookie'] } catch { $setCookie = '' }
            $sessionCookie = $null
            if ($setCookie -match 'gst_session=([^;,\s]+)') { $sessionCookie = "gst_session=$($Matches[1])" }
            return [pscustomobject]@{ StatusCode = [int]$resp.StatusCode; Body = $text; Error = $null; Cookie = $sessionCookie; Headers = (ConvertTo-HeaderTable $resp.Headers) }
        }
        return [pscustomobject]@{ StatusCode = 0; Body = ''; Error = $_.Exception.Message; Cookie = $null; Headers = @{} }
    }
}
