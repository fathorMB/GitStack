<#
.SYNOPSIS
  Rimuove i container di test orfani (label org.testcontainers=true e gitstack.test).
.DESCRIPTION
  Da lanciare a mano sull'host (Windows PowerShell 5.1 o pwsh) dopo test
  d'integrazione uccisi. Di default toglie i container fermi e quelli accesi da
  piu' di -MaxAgeMinutes (60); con -All toglie anche quelli accesi da meno
  tempo (solo se nessun test e' in corso). -DryRun elenca senza rimuovere.
.EXAMPLE
  powershell -File scripts/cleanup-test-containers.ps1 -DryRun
#>
param(
  [int]$MaxAgeMinutes = 60,
  [switch]$All,
  [switch]$DryRun
)
$ErrorActionPreference = 'Stop'
$ids = @()
foreach ($label in 'org.testcontainers=true', 'gitstack.test') {
  $ids += @(docker ps -a --filter "label=$label" --format '{{.ID}}')
}
$ids = $ids | Where-Object { $_ } | Sort-Object -Unique
$removed = 0
foreach ($id in $ids) {
  $info = docker inspect --format '{{.State.Running}}|{{.Created}}|{{.Name}}' $id
  $parts = $info -split '\|', 3
  $running = $parts[0] -eq 'true'
  $age = ((Get-Date).ToUniversalTime() - ([datetime]::Parse($parts[1]).ToUniversalTime())).TotalMinutes
  if ($running -and -not $All -and $age -lt $MaxAgeMinutes) {
    Write-Host "salto $id ($($parts[2])): acceso da $([int]$age) min"
    continue
  }
  if ($DryRun) { Write-Host "rimuoverei $id ($($parts[2]))"; continue }
  docker rm -f $id | Out-Null
  Write-Host "rimosso $id ($($parts[2]))"
  $removed++
}
Write-Host "container rimossi: $removed (su $(@($ids).Count) con etichetta di test)"
