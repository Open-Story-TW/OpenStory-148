# Abruptly terminate only an empty, tracked Go world 1 and prove peers survive.
[CmdletBinding()]
param()
$ErrorActionPreference='Stop'
$Root=Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $Root
$run=Join-Path $Root '.local\run'
$before=@{}
foreach($name in @('login','cashshop','world0','world1')) {
    $record=Get-Content -LiteralPath (Join-Path $run "$name.json") -Raw | ConvertFrom-Json
    $live=Get-CimInstance Win32_Process -Filter "ProcessId=$($record.Pid)"
    if (!$live -or $live.ExecutablePath -ne $record.Executable -or ([DateTimeOffset]$live.CreationDate).UtcDateTime -ne ([DateTimeOffset]$record.CreatedUtc).UtcDateTime) { throw "Cannot verify Go process identity: $name" }
    $ready=Invoke-RestMethod -Uri ($record.Health+'/health/ready') -TimeoutSec 3
    if ($ready.pid -ne $record.Pid) { throw "Health identity mismatch: $name" }
    $before[$name]=$record
}
$occupied=& docker compose --project-name maple-go-local --env-file .local/compose.env exec -T postgres psql -U maplego -d maple_go_local -t -A -c "SELECT COUNT(*) FROM ops_account_sessions WHERE world_id=1 AND state <> 'offline';"
if ($LASTEXITCODE -ne 0 -or [int]$occupied -ne 0) { throw 'World 1 has sessions or its state is unknown; refusing crash test.' }
$target=$before['world1']
$expected=[IO.Path]::GetFullPath((Join-Path $Root '.local\bin\world.exe'))
if ($target.Executable -ne $expected) { throw 'Crash target is outside the Go workspace.' }
Stop-Process -Id $target.Pid -Force
Start-Sleep -Milliseconds 300
foreach($name in @('login','cashshop','world0')) {
    $record=$before[$name]
    $ready=Invoke-RestMethod -Uri ($record.Health+'/health/ready') -TimeoutSec 3
    if ($ready.pid -ne $record.Pid) { throw "Peer process restarted or failed: $name" }
}
Write-Host 'PASS abrupt world 1 termination leaves login, cashshop and world 0 ready with unchanged PIDs.'
# A crashed owner deliberately retains its lease until expiration; never bypass it.
Write-Host 'Waiting for the crashed world lease to expire before normal restart...'
Start-Sleep -Seconds 21
& (Join-Path $PSScriptRoot 'local.ps1') start -Service world1 -SkipBuild
if ($LASTEXITCODE -ne 0) { throw 'World 1 normal recovery failed.' }
$after=Get-Content -LiteralPath (Join-Path $run 'world1.json') -Raw | ConvertFrom-Json
if ($after.Pid -eq $target.Pid) { throw 'World 1 recovery did not create a fresh process.' }
$peers=@{}
foreach($name in @('login','cashshop','world0')) {
    $ready=Invoke-RestMethod -Uri ($before[$name].Health+'/health/ready') -TimeoutSec 3
    if ($ready.pid -ne $before[$name].Pid) { throw "Peer changed during world recovery: $name" }
    $peers[$name]=$ready.pid
}
[ordered]@{VerifiedUtc=[DateTime]::UtcNow.ToString('o');Result='PASS';Scenario='abrupt empty world 1 process termination and fenced restart';TerminatedPid=$target.Pid;RecoveredPid=$after.Pid;UnaffectedPeers=$peers} | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath '.local\isolation-verification.json' -Encoding UTF8
Write-Host 'PASS world 1 recovered through the normal lease mechanism.'
