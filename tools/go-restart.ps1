[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)]
    [ValidateSet('login','world0','world1','cashshop')]
    [string[]]$Service,
    # restart = stop then start; stop / start run one half (crash scenarios
    # observe survivors between them).
    [ValidateSet('restart','stop','start')]
    [string]$Action = 'restart',
    # graceful = stop file (players saved); crash = TerminateProcess, no cleanup.
    [ValidateSet('graceful','crash')]
    [string]$Mode = 'graceful',
    # Build the binary from this checkout and install it before starting.
    [switch]$Build,
    # Install an already-built binary (same kind for every -Service).
    [string]$Binary,
    # Run versioned migrations from the (built) migrate binary before start.
    [switch]$Migrate,
    # Live runtime owning .local (defaults to the main checkout, not a worktree).
    [string]$RuntimeRoot,
    [ValidateRange(5,300)][int]$StopTimeoutSeconds = 45,
    [ValidateRange(5,300)][int]$ReadyTimeoutSeconds = 90,
    # Refuse when anyone is online unless explicitly allowed.
    [switch]$AllowOnline,
    [string]$Label = 'restart'
)
$ErrorActionPreference = 'Stop'
$SourceRoot = Split-Path -Parent $PSScriptRoot
if (!$RuntimeRoot) {
    $common = & git -C $SourceRoot rev-parse --path-format=absolute --git-common-dir
    if ($LASTEXITCODE -ne 0) { throw 'Cannot resolve runtime root; pass -RuntimeRoot.' }
    $RuntimeRoot = Split-Path -Parent $common
}
$RuntimeRoot = (Resolve-Path -LiteralPath $RuntimeRoot).Path
$Local = Join-Path $RuntimeRoot '.local'
$Run = Join-Path $Local 'run'
$Logs = Join-Path $Local 'logs'
$Evidence = Join-Path $Local 'evidence'
if (!(Test-Path -LiteralPath (Join-Path $Local 'environment.json'))) { throw "No runtime environment at $Local" }
New-Item -ItemType Directory -Force -Path $Evidence | Out-Null
$Started = [DateTime]::UtcNow
$Stamp = $Started.ToString('yyyyMMdd-HHmmss')

# Mutating the shared runtime needs tools/runtime-lock.ps1 held by $env:MAPLEGO_RUNTIME_OWNER.
function ConvertTo-UtcDateTime([object]$Value) {
    # PowerShell 7 JSON can produce DateTime values; stringifying them first
    # discards Kind/offset. Unspecified DateTimes retain .NET's local semantics.
    if ($Value -is [DateTimeOffset]) { return $Value.UtcDateTime }
    if ($Value -is [DateTime]) { return $Value.ToUniversalTime() }
    if ($Value -isnot [string] -or [string]::IsNullOrWhiteSpace($Value)) { throw 'Missing or invalid timestamp.' }
    return [DateTimeOffset]::Parse($Value, [Globalization.CultureInfo]::InvariantCulture, [Globalization.DateTimeStyles]::None).UtcDateTime
}
function Assert-RuntimeLock([string]$Root) {
    $lockPath = Join-Path $Root '.local/runtime-lock.json'
    $lock = if (Test-Path -LiteralPath $lockPath) { Get-Content -LiteralPath $lockPath -Raw | ConvertFrom-Json } else { $null }
    if (!$lock -or (ConvertTo-UtcDateTime $lock.expires_utc) -le [DateTime]::UtcNow -or $lock.owner -ne $env:MAPLEGO_RUNTIME_OWNER) {
        throw "Hold the runtime lock first: set `$env:MAPLEGO_RUNTIME_OWNER and run tools/runtime-lock.ps1 acquire -Owner <name>."
    }
}
function Write-Utf8([string]$Path, [string]$Content) {
    [IO.File]::WriteAllText($Path,$Content,[Text.UTF8Encoding]::new($false))
}
function Get-ServiceInfo([string]$Name) {
    $kind = $Name; $port = 19080; $worldID = 0
    if ($Name -eq 'world0') { $kind='world'; $port=19081 }
    if ($Name -eq 'world1') { $kind='world'; $port=19083; $worldID=1 }
    if ($Name -eq 'cashshop') { $port=19082 }
    [pscustomobject]@{Name=$Name;Kind=$kind;WorldID=$worldID;Exe=(Join-Path $Local "bin\$kind.exe");Health="http://127.0.0.1:$port";HealthAddr="127.0.0.1:$port";StopFile=(Join-Path $Run "$Name.stop");PidFile=(Join-Path $Run "$Name.json")}
}
function Get-OwnedProcess($Info) {
    if (!(Test-Path -LiteralPath $Info.PidFile)) { return $null }
    $record = Get-Content -LiteralPath $Info.PidFile -Raw | ConvertFrom-Json
    $p = Get-CimInstance Win32_Process -Filter "ProcessId=$($record.Pid)" -ErrorAction SilentlyContinue
    if ($p -and (!$p.ExecutablePath -or !$p.CreationDate)) {
        Start-Sleep -Milliseconds 200
        $p = Get-CimInstance Win32_Process -Filter "ProcessId=$($record.Pid)" -ErrorAction SilentlyContinue
    }
    if (!$p) { return $null }
    if ($p.ExecutablePath -ne $Info.Exe -or (ConvertTo-UtcDateTime $p.CreationDate).Ticks -ne (ConvertTo-UtcDateTime $record.CreatedUtc).Ticks) {
        throw "Recorded PID for $($Info.Name) belongs to another process; refusing to operate it."
    }
    return $p
}
function Get-Health($Info) {
    try { return Invoke-RestMethod -Uri ($Info.Health+'/health/ready') -TimeoutSec 3 } catch { return $null }
}
function Get-Identity([string]$Name) {
    $info = Get-ServiceInfo $Name
    $p = Get-OwnedProcess $info
    if (!$p) { return [ordered]@{name=$Name;running=$false} }
    $h = Get-Health $info
    [ordered]@{name=$Name;running=$true;pid=[int]$p.ProcessId;created_ticks=(ConvertTo-UtcDateTime $p.CreationDate).Ticks;sha256=(Get-FileHash -LiteralPath $p.ExecutablePath).Hash;instance=$h.instance;status=$h.status;epoch=$h.details.epoch;online=$h.details.online}
}
function Get-JavaIdentity {
    $path = Join-Path $Local 'java-baseline.json'
    if (!(Test-Path -LiteralPath $path)) { return @() }
    $baseline = Get-Content -LiteralPath $path -Raw | ConvertFrom-Json
    foreach ($old in $baseline.Processes) {
        $live = Get-CimInstance Win32_Process -Filter "ProcessId=$($old.ProcessId)" -ErrorAction SilentlyContinue
        $exe = if ($live) { [string]$live.ExecutablePath } else { $null }
        $ticks = if ($live -and $live.CreationDate) { (ConvertTo-UtcDateTime $live.CreationDate).Ticks } else { $null }
        [ordered]@{pid=[int]$old.ProcessId;alive=[bool]$live;exe=$exe;created_ticks=$ticks;same=([bool]$live -and $exe -eq $old.ExecutablePath -and $ticks -eq (ConvertTo-UtcDateTime $old.CreationDate).Ticks)}
    }
}
function Get-RuntimeEnvironment {
    $settings = Get-Content -LiteralPath (Join-Path $Local 'environment.json') -Raw | ConvertFrom-Json
    $map = @{}
    foreach ($p in $settings.PSObject.Properties) { $map[$p.Name] = [string]$p.Value }
    return $map
}
function Invoke-WithEnvironment([hashtable]$Vars, [scriptblock]$Body) {
    $saved = @{}
    try {
        foreach ($k in $Vars.Keys) { $saved[$k] = [Environment]::GetEnvironmentVariable($k,'Process'); [Environment]::SetEnvironmentVariable($k,$Vars[$k],'Process') }
        & $Body
    } finally {
        foreach ($k in $saved.Keys) { [Environment]::SetEnvironmentVariable($k,$saved[$k],'Process') }
    }
}
function Stop-Owned([string]$Name) {
    $info = Get-ServiceInfo $Name
    $p = Get-OwnedProcess $info
    if (!$p) { return [ordered]@{name=$Name;was_running=$false} }
    $t0 = [DateTime]::UtcNow
    if ($Mode -eq 'crash') {
        # TerminateProcess: no deferred cleanup, no lease release, no final save.
        Stop-Process -Id $p.ProcessId -Force
    } else {
        Write-Utf8 $info.StopFile 'stop'
    }
    $deadline = $t0.AddSeconds($StopTimeoutSeconds)
    while ((Get-OwnedProcess $info) -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 200 }
    if (Get-OwnedProcess $info) { throw "$Name did not exit within $StopTimeoutSeconds s; left running." }
    $archived = @()
    foreach ($suffix in @('stdout','stderr')) {
        $src = Join-Path $Logs "$Name.$suffix.log"
        if (Test-Path -LiteralPath $src) {
            $dst = Join-Path $Evidence "go-restart-$Stamp-$Name-pre.$suffix.log"
            Copy-Item -LiteralPath $src -Destination $dst
            $archived += $dst
        }
    }
    [ordered]@{name=$Name;was_running=$true;pid=[int]$p.ProcessId;mode=$Mode;stop_ms=[int]([DateTime]::UtcNow-$t0).TotalMilliseconds;logs=$archived}
}
function Start-Owned([string]$Name, [hashtable]$Env) {
    $info = Get-ServiceInfo $Name
    if (Get-OwnedProcess $info) { throw "$Name already running." }
    if (!(Test-Path -LiteralPath $info.Exe)) { throw "Missing binary: $($info.Exe)" }
    if (Test-Path -LiteralPath $info.StopFile) { Remove-Item -LiteralPath $info.StopFile }
    $vars = @{} + $Env
    $vars['MAPLEGO_WORLD_ID'] = [string]$info.WorldID
    $vars['MAPLEGO_HEALTH_ADDR'] = $info.HealthAddr
    $vars['MAPLEGO_STOP_FILE'] = $info.StopFile
    $t0 = [DateTime]::UtcNow
    $process = Invoke-WithEnvironment $vars {
        Start-Process -FilePath $info.Exe -WorkingDirectory $RuntimeRoot -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $Logs "$Name.stdout.log") -RedirectStandardError (Join-Path $Logs "$Name.stderr.log")
    }
    $identity = Get-CimInstance Win32_Process -Filter "ProcessId=$($process.Id)"
    Write-Utf8 $info.PidFile ([ordered]@{Pid=$process.Id;CreatedUtc=(ConvertTo-UtcDateTime $identity.CreationDate).ToString('o');Executable=$info.Exe;Health=$info.Health;Role=$Name} | ConvertTo-Json)
    $deadline = $t0.AddSeconds($ReadyTimeoutSeconds)
    $sawLeaseWait = $false
    while ([DateTime]::UtcNow -lt $deadline) {
        if (!(Get-OwnedProcess $info)) { throw "$Name exited during startup; see $Logs\$Name.stdout.log" }
        $h = Get-Health $info
        if ($h -and $h.status -eq 'ready') {
            if ($h.pid -ne $process.Id) { throw "$Name health endpoint owned by PID $($h.pid), not $($process.Id)." }
            $log = Join-Path $Logs "$Name.stdout.log"
            if ((Test-Path -LiteralPath $log) -and (Select-String -LiteralPath $log -Pattern '"lease_wait"' -Quiet)) { $sawLeaseWait = $true }
            return [ordered]@{name=$Name;pid=$process.Id;ready_ms=[int]([DateTime]::UtcNow-$t0).TotalMilliseconds;lease_wait=$sawLeaseWait;instance=$h.instance;epoch=$h.details.epoch}
        }
        Start-Sleep -Milliseconds 250
    }
    throw "$Name not ready within $ReadyTimeoutSeconds s (PID $($process.Id) left running)."
}

Assert-RuntimeLock $RuntimeRoot
$kinds = @($Service | ForEach-Object { (Get-ServiceInfo $_).Kind } | Sort-Object -Unique)
if (($Build -or $Binary) -and $Action -eq 'stop') { throw '-Build/-Binary need start or restart.' }
if ($Binary -and $kinds.Count -ne 1) { throw '-Binary installs one kind; pass services of a single kind.' }
$receipt = [ordered]@{
    tool='go-restart.ps1'; label=$Label; action=$Action; mode=$Mode; services=$Service
    runtime_root=$RuntimeRoot; source_root=$SourceRoot
    started_utc=$Started.ToString('o')
}
$all = @('login','world0','world1','cashshop')
$receipt.before = @($all | ForEach-Object { Get-Identity $_ })
$receipt.java_before = @(Get-JavaIdentity)
$online = @($receipt.before | Where-Object { $_.running -and $Service -contains $_.name -and $_.online -gt 0 })
if ($online.Count -gt 0 -and !$AllowOnline -and $Action -ne 'start') { throw "Players online on $($online.name -join ','); pass -AllowOnline to proceed." }
$receipt.online_at_stop = @($online | ForEach-Object { [ordered]@{name=$_.name;online=$_.online} })

# Build before stopping so a compile failure never leaves services down.
$installs = @()
if ($Build) {
    $commit = (& git -C $SourceRoot rev-parse HEAD).Trim()
    $dirty = [bool](& git -C $SourceRoot status --porcelain)
    $stage = Join-Path $Local "staged\go-restart-$Stamp"
    New-Item -ItemType Directory -Force -Path $stage | Out-Null
    $buildKinds = @($kinds)
    if ($Migrate) { $buildKinds += 'migrate' }
    Push-Location -LiteralPath $SourceRoot
    try {
        foreach ($k in $buildKinds) {
            & go build -trimpath -o (Join-Path $stage "$k.exe") "./cmd/$k"
            if ($LASTEXITCODE -ne 0) { throw "go build failed: $k" }
        }
    } finally { Pop-Location }
    $receipt.build = [ordered]@{commit=$commit;dirty=$dirty;stage=$stage}
    foreach ($k in $buildKinds) { $installs += [pscustomobject]@{Kind=$k;Path=(Join-Path $stage "$k.exe")} }
} elseif ($Binary) {
    $installs += [pscustomobject]@{Kind=$kinds[0];Path=(Resolve-Path -LiteralPath $Binary).Path}
}

$envVars = Get-RuntimeEnvironment
$stopBegin = $null
$receipt.stopped = @()
$receipt.started = @()
try {
    if ($Action -ne 'start') {
        $stopBegin = [DateTime]::UtcNow
        $stopOrder = @('login','cashshop','world1','world0') | Where-Object { $Service -contains $_ }
        foreach ($name in $stopOrder) { $receipt.stopped += (Stop-Owned $name) }
        $receipt.down_utc = [DateTime]::UtcNow.ToString('o')
    }
    if ($Action -ne 'stop') {
        $receipt.installed = @()
        foreach ($i in $installs) {
            $target = Join-Path $Local "bin\$($i.Kind).exe"
            foreach ($name in $all) {
                $info = Get-ServiceInfo $name
                if ($info.Kind -eq $i.Kind -and (Get-OwnedProcess $info)) { throw "$name still runs $($i.Kind).exe; stop every service of that kind first." }
            }
            $old = if (Test-Path -LiteralPath $target) { (Get-FileHash -LiteralPath $target).Hash } else { $null }
            if ($old) { Copy-Item -LiteralPath $target -Destination (Join-Path $Local "bin\$($i.Kind)-prev-$Stamp.exe") }
            # A just-exited process (or a scanner) can hold the image briefly.
            for ($try = 1; ; $try++) {
                try { Copy-Item -LiteralPath $i.Path -Destination $target -Force -ErrorAction Stop; break }
                catch { if ($try -ge 20) { throw }; Start-Sleep -Milliseconds 500 }
            }
            $receipt.installed += [ordered]@{kind=$i.Kind;previous_sha256=$old;sha256=(Get-FileHash -LiteralPath $target).Hash}
        }
        if ($Migrate) {
            # migrate logs to stderr; with Stop that would throw before the exit code is seen.
            $out = Invoke-WithEnvironment $envVars { $ErrorActionPreference = 'Continue'; & (Join-Path $Local 'bin\migrate.exe') 2>&1 | ForEach-Object { "$_" } }
            if ($LASTEXITCODE -ne 0) { throw "migrate failed: $out" }
            $receipt.migrate = 'ok'
        }
        $startOrder = @('world0','world1','cashshop','login') | Where-Object { $Service -contains $_ }
        foreach ($name in $startOrder) { $receipt.started += (Start-Owned $name $envVars) }
        if ($stopBegin) { $receipt.rto_ms = [int]([DateTime]::UtcNow - $stopBegin).TotalMilliseconds }
    }
    $receipt.status = 'ok'
} catch {
    $receipt.status = 'failed'
    $receipt.error = $_.Exception.Message
}
$receipt.after = @($all | ForEach-Object { Get-Identity $_ })
$receipt.java_after = @(Get-JavaIdentity)
# Judge only what changed during this run: java-baseline.json can be older
# than the current Java reference (it is restarted outside this tool), and a
# baseline that was already stale before we touched anything is not our fault.
$receipt.java_baseline_stale = [bool]($receipt.java_before | Where-Object { -not $_.same })
$javaChanged = @()
foreach ($b in $receipt.java_before) {
    $a = $receipt.java_after | Where-Object { $_.pid -eq $b.pid }
    if (!$a -or $a.alive -ne $b.alive -or $a.exe -ne $b.exe -or $a.created_ticks -ne $b.created_ticks) { $javaChanged += $b.pid }
}
$receipt.java_unchanged = $javaChanged.Count -eq 0
# Services outside -Service must keep their exact process identity.
$untouched = @()
foreach ($b in $receipt.before) {
    if ($Service -contains $b.name) { continue }
    $a = $receipt.after | Where-Object { $_.name -eq $b.name }
    $untouched += [ordered]@{name=$b.name;same=($b.running -eq $a.running -and $b.pid -eq $a.pid -and $b.created_ticks -eq $a.created_ticks)}
}
$receipt.untouched = $untouched
if ($receipt.status -eq 'ok' -and ($untouched | Where-Object { -not $_.same })) { $receipt.status = 'failed'; $receipt.error = 'A service outside -Service changed identity.' }
if ($receipt.status -eq 'ok' -and -not $receipt.java_unchanged) { $receipt.status = 'failed'; $receipt.error = "Java reference process identity changed during this run (PID $($javaChanged -join ','))." }
foreach ($s in $receipt.started) {
    $b = $receipt.before | Where-Object { $_.name -eq $s.name }
    $s.epoch_advanced = if ($b.epoch -ne $null -and $s.epoch -ne $null) { [int64]$s.epoch -gt [int64]$b.epoch } else { $null }
}
$receipt.finished_utc = [DateTime]::UtcNow.ToString('o')
$path = Join-Path $Evidence "go-restart-$Stamp-$Label.json"
Write-Utf8 $path ($receipt | ConvertTo-Json -Depth 8)
Write-Host "receipt: $path"
Write-Host ("status={0} stopped={1} started={2}" -f $receipt.status, (($receipt.stopped | ForEach-Object { "$($_.name):$($_.pid)" }) -join ','), (($receipt.started | ForEach-Object { "$($_.name):$($_.pid)/epoch$($_.epoch)/$($_.ready_ms)ms" }) -join ','))
if ($receipt.java_baseline_stale) { Write-Warning '.local/java-baseline.json no longer matches the running Java reference; refresh it. Not counted as a failure.' }
if ($receipt.status -ne 'ok') { Write-Error $receipt.error; exit 1 }
