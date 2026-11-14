[CmdletBinding()]
param(
    [Parameter(Position=0)]
    [ValidateSet('acquire','renew','release','status')]
    [string]$Action = 'status',
    # deploy = exclusive (restart Go, swap the broker sidecar, migrate);
    # client = shared runtime, only the listed QA profiles are leased.
    [ValidateSet('deploy','client')]
    [string]$Kind = 'deploy',
    # Who holds it (thread title or branch); required to change it.
    [string]$Owner,
    [ValidateRange(5,240)][int]$Minutes = 45,
    [string]$Purpose = '',
    # client: the launcher profiles this owner drives (e.g. goqa20,goqa21,goqa90).
    [string[]]$Profiles = @(),
    # client: world:channel pairs this owner tests on (e.g. 0:5); overlap-checked.
    [string[]]$Channels = @(),
    # Wait for the current holder(s) instead of failing immediately.
    [ValidateRange(0,7200)][int]$WaitSeconds = 0
)
# One shared Go runtime and MCP broker serve every thread. A deploy holder has
# the runtime to itself; client holders share it, each driving only its own
# profiles. While client leases are active, .local/runtime-lock.json carries a
# synthetic 'client-leases' holder so older copies of this script wait too.
$ErrorActionPreference = 'Stop'
$source = Split-Path -Parent $PSScriptRoot
$common = & git -C $source rev-parse --path-format=absolute --git-common-dir
$runtime = Split-Path -Parent $common
$path = Join-Path $runtime '.local/runtime-lock.json'
$leaseDir = Join-Path $runtime '.local/runtime-leases'
$pendingPath = Join-Path $leaseDir '_deploy-pending.json'
$ClientsHolder = 'client-leases'

function ConvertTo-UtcDateTime([object]$Value) {
    # PowerShell 7 JSON can produce DateTime values; stringifying them first
    # discards Kind/offset. Unspecified DateTimes retain .NET's local semantics.
    if ($Value -is [DateTimeOffset]) { return $Value.UtcDateTime }
    if ($Value -is [DateTime]) { return $Value.ToUniversalTime() }
    if ($Value -isnot [string] -or [string]::IsNullOrWhiteSpace($Value)) { throw 'Missing or invalid timestamp.' }
    return [DateTimeOffset]::Parse($Value, [Globalization.CultureInfo]::InvariantCulture, [Globalization.DateTimeStyles]::None).UtcDateTime
}
function Get-Expiry($Record) { ConvertTo-UtcDateTime $Record.expires_utc }
function Read-Json([string]$File) {
    if (!(Test-Path -LiteralPath $File)) { return $null }
    $record = Get-Content -LiteralPath $File -Raw | ConvertFrom-Json
    if ((Get-Expiry $record) -le [DateTime]::UtcNow) { return $null }
    return $record
}
function Write-Json([string]$File, $Body) {
    $tmp = "$File.$PID.tmp"
    [IO.File]::WriteAllText($tmp, ($Body | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
    Move-Item -LiteralPath $tmp -Destination $File -Force
}
function Read-Deploy {
    $lock = Read-Json $path
    if ($lock -and $lock.owner -eq $ClientsHolder) { return $null }
    return $lock
}
function Read-Leases {
    if (!(Test-Path -LiteralPath $leaseDir)) { return @() }
    @(Get-ChildItem -LiteralPath $leaseDir -Filter 'client-*.json' | ForEach-Object { Read-Json $_.FullName } | Where-Object { $_ })
}
function Get-LeasePath([string]$Name) { Join-Path $leaseDir ('client-' + ($Name -replace '[^A-Za-z0-9_.-]','_') + '.json') }
function New-Record([string]$KindName) {
    $now = [DateTime]::UtcNow
    [ordered]@{kind=$KindName;owner=$Owner;purpose=$Purpose;acquired_utc=$now.ToString('o');expires_utc=$now.AddMinutes($Minutes).ToString('o');branch=((& git -C $source rev-parse --abbrev-ref HEAD) -join '');commit=((& git -C $source rev-parse --short HEAD) -join '')}
}
# Mirror active client leases into runtime-lock.json for scripts that only know it.
function Sync-ClientsHolder {
    if (Read-Deploy) { return }
    $leases = @(Read-Leases)
    if ($leases.Count -eq 0) {
        $lock = if (Test-Path -LiteralPath $path) { Get-Content -LiteralPath $path -Raw | ConvertFrom-Json } else { $null }
        if ($lock -and ($lock.owner -eq $ClientsHolder -or (Get-Expiry $lock) -le [DateTime]::UtcNow)) { Remove-Item -LiteralPath $path -Force }
        return
    }
    $latest = $leases | ForEach-Object { Get-Expiry $_ } | Sort-Object | Select-Object -Last 1
    Write-Json $path ([ordered]@{kind='clients';owner=$ClientsHolder;purpose=(($leases | ForEach-Object { 'client:' + $_.owner }) -join ',');acquired_utc=[DateTime]::UtcNow.ToString('o');expires_utc=$latest.ToString('o')})
}
# Every read-check-write runs under one mutex shared by all threads on this machine.
function Invoke-Locked([scriptblock]$Body) {
    $mutex = New-Object System.Threading.Mutex($false, 'Local\maplego-runtime-lock')
    try {
        try { [void]$mutex.WaitOne() } catch [System.Threading.AbandonedMutexException] { }
        try { & $Body } finally { $mutex.ReleaseMutex() }
    } finally { $mutex.Dispose() }
}

if ($Action -ne 'status' -and !$Owner) { throw '-Owner is required.' }
if ($Owner -eq $ClientsHolder) { throw "'$ClientsHolder' is reserved." }
New-Item -ItemType Directory -Force -Path $leaseDir | Out-Null
if ($Kind -eq 'client' -and $Action -eq 'acquire') {
    $Profiles = @($Profiles | ForEach-Object { $_ -split ',' } | ForEach-Object { $_.Trim().ToLowerInvariant() } | Where-Object { $_ })
    $Channels = @($Channels | ForEach-Object { $_ -split ',' } | ForEach-Object { $_.Trim() } | Where-Object { $_ })
    if ($Profiles.Count -eq 0) { throw '-Profiles is required for a client lease.' }
    foreach ($p in $Profiles) { if ($p -notmatch '^[a-z0-9][a-z0-9_-]{0,31}$') { throw "Invalid profile '$p'." } }
    foreach ($c in $Channels) { if ($c -notmatch '^\d{1,3}:\d{1,3}$') { throw "Invalid channel '$c'; use world:channel." } }
}

switch ($Action) {
    'status' {
        Invoke-Locked { [ordered]@{deploy=(Read-Deploy);deploy_pending=(Read-Json $pendingPath);clients=@(Read-Leases)} } | ConvertTo-Json -Depth 5
    }
    'acquire' {
        $deadline = [DateTime]::UtcNow.AddSeconds($WaitSeconds)
        while ($true) {
            # Returns $null when granted, otherwise why not.
            $blocked = Invoke-Locked {
                $deploy = Read-Deploy
                $leases = @(Read-Leases)
                $pending = Read-Json $pendingPath
                if ($deploy -and $deploy.owner -ne $Owner) { return "Runtime deploy-locked by '$($deploy.owner)' ($($deploy.purpose)) until $($deploy.expires_utc)." }
                if ($Kind -eq 'deploy') {
                    $others = @($leases | Where-Object { $_.owner -ne $Owner })
                    if ($others.Count -gt 0) {
                        # Hold back new client leases until this deploy gets its turn.
                        if (!$pending -or $pending.owner -eq $Owner) {
                            Write-Json $pendingPath ([ordered]@{owner=$Owner;purpose=$Purpose;expires_utc=$deadline.AddSeconds(30).ToString('o')})
                        }
                        return "Client leases active: $(($others | ForEach-Object { 'client:' + $_.owner + ' [' + (@($_.profiles) -join ',') + '] until ' + $_.expires_utc }) -join '; '). Ask them to close their clients and release."
                    }
                    $own = Get-LeasePath $Owner
                    if (Test-Path -LiteralPath $own) { Remove-Item -LiteralPath $own -Force }
                    Write-Json $path (New-Record 'deploy')
                    if ($pending -and $pending.owner -eq $Owner) { Remove-Item -LiteralPath $pendingPath -Force }
                    return $null
                }
                if ($deploy) { return 'You hold the deploy lock; release it before taking a client lease.' }
                $mine = @($leases | Where-Object { $_.owner -eq $Owner })
                if ($pending -and $pending.owner -ne $Owner -and $mine.Count -eq 0) { return "Deploy by '$($pending.owner)' is waiting ($($pending.purpose)); retry after it releases." }
                foreach ($other in @($leases | Where-Object { $_.owner -ne $Owner })) {
                    $clash = @($Profiles | Where-Object { @($other.profiles) -contains $_ }) + @($Channels | Where-Object { @($other.channels) -contains $_ })
                    if ($clash.Count -gt 0) { return "client:$($other.owner) already leases $($clash -join ',') until $($other.expires_utc)." }
                }
                $record = New-Record 'client'
                $record.profiles = @($Profiles)
                $record.channels = @($Channels)
                Write-Json (Get-LeasePath $Owner) $record
                Sync-ClientsHolder
                return $null
            }
            if (!$blocked) {
                if ($Kind -eq 'deploy') { "deploy acquired by $Owner" } else { "client:$Owner acquired [$($Profiles -join ',')]" }
                break
            }
            if ([DateTime]::UtcNow -ge $deadline) {
                if ($Kind -eq 'deploy') { Invoke-Locked { $p = Read-Json $pendingPath; if ($p -and $p.owner -eq $Owner) { Remove-Item -LiteralPath $pendingPath -Force } } }
                throw $blocked
            }
            Start-Sleep -Seconds 10
        }
    }
    'renew' {
        Invoke-Locked {
            $expires = [DateTime]::UtcNow.AddMinutes($Minutes).ToString('o')
            if ($Kind -eq 'deploy') {
                $lock = Read-Deploy
                if (!$lock -or $lock.owner -ne $Owner) { throw "Deploy lock not held by '$Owner'." }
                $lock.expires_utc = $expires
                Write-Json $path $lock
            } else {
                $file = Get-LeasePath $Owner
                $lease = Read-Json $file
                if (!$lease) { throw "No active client lease for '$Owner'." }
                $lease.expires_utc = $expires
                Write-Json $file $lease
                Sync-ClientsHolder
            }
        }
        "renewed until $([DateTime]::UtcNow.AddMinutes($Minutes).ToString('o'))"
    }
    'release' {
        Invoke-Locked {
            if ($Kind -eq 'deploy') {
                $lock = Read-Deploy
                if ($lock -and $lock.owner -ne $Owner) { throw "Deploy lock held by '$($lock.owner)', not '$Owner'." }
                if ($lock) { Remove-Item -LiteralPath $path -Force }
                $p = Read-Json $pendingPath
                if ($p -and $p.owner -eq $Owner) { Remove-Item -LiteralPath $pendingPath -Force }
            } else {
                $file = Get-LeasePath $Owner
                if (Test-Path -LiteralPath $file) { Remove-Item -LiteralPath $file -Force }
            }
            Sync-ClientsHolder
        }
        'released'
    }
}
