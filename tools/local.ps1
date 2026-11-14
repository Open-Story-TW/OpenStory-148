[CmdletBinding()]
param(
    [Parameter(Position=0)]
    [ValidateSet('setup','build','start','stop','status','test')]
    [string]$Action = 'status',
    [ValidateSet('all','login','world0','world1','cashshop','admin')]
    [string]$Service = 'all',
    [switch]$SkipBuild,
    [switch]$StopInfrastructure
)
$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$Local = Join-Path $Root '.local'
$Run = Join-Path $Local 'run'
$Logs = Join-Path $Local 'logs'
Set-Location -LiteralPath $Root

function Write-Utf8([string]$Path, [string]$Content) {
    [IO.File]::WriteAllText($Path,$Content,[Text.UTF8Encoding]::new($false))
}
function Initialize-Settings {
    foreach ($dir in @($Local,$Run,$Logs,(Join-Path $Local 'secrets'),(Join-Path $Local 'bin'))) {
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
    }
    $envPath = Join-Path $Local 'environment.json'
    if (!(Test-Path -LiteralPath $envPath)) {
        $pgPassword = [Guid]::NewGuid().ToString('N')+[Guid]::NewGuid().ToString('N')
        $redisPassword = [Guid]::NewGuid().ToString('N')+[Guid]::NewGuid().ToString('N')
        $settings = [ordered]@{
            MAPLEGO_DATABASE_URL = 'postgres://maplego:'+$pgPassword+'@127.0.0.1:15432/maple_go_local?sslmode=disable&connect_timeout=5'
            MAPLEGO_REDIS_ADDR = '127.0.0.1:16379'
            MAPLEGO_REDIS_PASSWORD = $redisPassword
            MAPLEGO_RPC_TOKEN = [Guid]::NewGuid().ToString('N')+[Guid]::NewGuid().ToString('N')
            MAPLEGO_BIND = '127.0.0.1'
            MAPLEGO_LOGIN_PORT = '18484'
            MAPLEGO_CASHSHOP_PORT = '18600'
        }
        Write-Utf8 $envPath ($settings | ConvertTo-Json)
        Write-Utf8 (Join-Path $Local 'secrets\postgres-password') $pgPassword
        Write-Utf8 (Join-Path $Local 'secrets\redis-password') $redisPassword
        $redisConfig = @('bind 0.0.0.0','port 6379','protected-mode yes',('requirepass '+$redisPassword),'maxmemory 256mb','maxmemory-policy allkeys-lfu','appendonly no','save ""') -join "`n"
        Write-Utf8 (Join-Path $Local 'redis.conf') ($redisConfig+"`n")
    }
    $settings = Get-Content -LiteralPath $envPath -Raw | ConvertFrom-Json
    $worlds = @(
        [ordered]@{id=0;name='Go Scania';host='127.0.0.1';channel_base_port=18485;channel_count=11;rpc_addr='127.0.0.1:19485'},
        [ordered]@{id=1;name='Go Bera';host='127.0.0.1';channel_base_port=18505;channel_count=11;rpc_addr='127.0.0.1:19505'}
    ) | ConvertTo-Json -Compress
    $changed = $false
    if (!$settings.PSObject.Properties['MAPLEGO_WORLDS_JSON']) {
        $settings | Add-Member -NotePropertyName MAPLEGO_WORLDS_JSON -NotePropertyValue $worlds
        $changed = $true
    }
    # Web admin. It is the only service that listens beyond loopback; sign-in
    # needs an emailed code, so fill in the mail values before exposing it.
    $adminDefaults = [ordered]@{
        MAPLEGO_ADMIN_ADDR = '0.0.0.0:19090'
        MAPLEGO_ADMIN_EMAILS = 'Raymond@gmail.com'
        MAPLEGO_POSTMARK_TOKEN = ''
        MAPLEGO_ADMIN_MAIL_FROM = ''
    }
    foreach ($name in $adminDefaults.Keys) {
        if (!$settings.PSObject.Properties[$name]) {
            $settings | Add-Member -NotePropertyName $name -NotePropertyValue $adminDefaults[$name]
            $changed = $true
        }
    }
    if ($changed) { Write-Utf8 $envPath ($settings | ConvertTo-Json) }
    return $settings
}

function Use-LocalEnvironment([scriptblock]$Body) {
    $settings = Initialize-Settings
    $saved = @{}
    try {
        foreach ($p in $settings.PSObject.Properties) {
            $saved[$p.Name] = [Environment]::GetEnvironmentVariable($p.Name,'Process')
            [Environment]::SetEnvironmentVariable($p.Name,[string]$p.Value,'Process')
        }
        & $Body
    } finally {
        foreach ($key in $saved.Keys) { [Environment]::SetEnvironmentVariable($key,$saved[$key],'Process') }
    }
}

function Setup-Infrastructure {
    [void](Initialize-Settings)
    $pinFile = Join-Path $Local 'compose.env'
    if (!(Test-Path -LiteralPath $pinFile)) {
        & docker pull postgres:18.6-bookworm
        if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL image download failed.' }
        & docker pull redis:8.2-alpine
        if ($LASTEXITCODE -ne 0) { throw 'Redis image download failed.' }
        $pgImage = & docker image inspect postgres:18.6-bookworm --format '{{index .RepoDigests 0}}'
        $redisImage = & docker image inspect redis:8.2-alpine --format '{{index .RepoDigests 0}}'
        Write-Utf8 $pinFile ((@(('MAPLEGO_POSTGRES_IMAGE='+$pgImage),('MAPLEGO_REDIS_IMAGE='+$redisImage)) -join "`n")+"`n")
    }
    & docker compose --project-name maple-go-local --env-file $pinFile -f (Join-Path $Root 'compose.yaml') up -d --wait --wait-timeout 60
    if ($LASTEXITCODE -ne 0) { throw 'Isolated infrastructure startup failed.' }
}

function Get-ServiceInfo([string]$Name) {
    $kind = $Name
    $healthPort = 19080
    $worldID = 0
    if ($Name -eq 'world0') { $kind='world';$healthPort=19081 }
    if ($Name -eq 'world1') { $kind='world';$healthPort=19083;$worldID=1 }
    if ($Name -eq 'cashshop') { $healthPort=19082 }
    if ($Name -eq 'admin') { $healthPort=19079 }
    return [pscustomobject]@{Name=$Name;Kind=$kind;WorldID=$worldID;Exe=(Join-Path $Local "bin\$kind.exe");Health="http://127.0.0.1:$healthPort";HealthAddr="127.0.0.1:$healthPort";StopFile=(Join-Path $Run "$Name.stop");PidFile=(Join-Path $Run "$Name.json")}
}

function Get-OwnedProcess($Info) {
    if (!(Test-Path -LiteralPath $Info.PidFile)) { return $null }
    $record = Get-Content -LiteralPath $Info.PidFile -Raw | ConvertFrom-Json
    $process = Get-CimInstance Win32_Process -Filter "ProcessId=$($record.Pid)" -ErrorAction SilentlyContinue
    if (!$process) { return $null }
    # Win32_Process can briefly return a terminating process without its path.
    if (!$process.ExecutablePath -or !$process.CreationDate) {
        Start-Sleep -Milliseconds 200
        $process = Get-CimInstance Win32_Process -Filter "ProcessId=$($record.Pid)" -ErrorAction SilentlyContinue
        if (!$process) { return $null }
    }
    if ($process.ExecutablePath -ne $Info.Exe -or ([DateTimeOffset]$process.CreationDate).UtcDateTime -ne ([DateTimeOffset]$record.CreatedUtc).UtcDateTime) {
        throw "Recorded PID for $($Info.Name) belongs to another process; refusing to operate it."
    }
    return $process
}

function Assert-JavaUnchanged {
    $baselinePath = Join-Path $Local 'java-baseline.json'
    if (!(Test-Path -LiteralPath $baselinePath)) { return }
    $baseline = Get-Content -LiteralPath $baselinePath -Raw | ConvertFrom-Json
    foreach ($oldProcess in $baseline.Processes) {
        $live = Get-CimInstance Win32_Process -Filter "ProcessId=$($oldProcess.ProcessId)" -ErrorAction SilentlyContinue
        if (!$live -or $live.ExecutablePath -ne $oldProcess.ExecutablePath -or ([DateTimeOffset]$live.CreationDate).UtcDateTime -ne ([DateTimeOffset]$oldProcess.CreationDate).UtcDateTime) {
            throw 'Java reference process identity changed; inspect .local/java-baseline.json.'
        }
    }
    foreach ($oldListener in $baseline.Listeners) {
        $live = Get-NetTCPConnection -State Listen -LocalPort $oldListener.LocalPort -ErrorAction SilentlyContinue | Where-Object { $_.OwningProcess -eq $oldListener.OwningProcess -and $_.LocalAddress -eq $oldListener.LocalAddress }
        if (!$live) { throw "Java reference listener changed on port $($oldListener.LocalPort)." }
    }
    Write-Host 'Java reference processes and listeners unchanged.'
}

function Build-Services {
    [void](Initialize-Settings)
    foreach ($kind in @('login','cashshop','world','admin','migrate','smoke')) {
        $target = Join-Path $Local "bin\$kind.exe"
        if ($kind -ne 'migrate' -and $kind -ne 'smoke') {
            foreach ($name in @('login','cashshop','world0','world1','admin')) {
                $info = Get-ServiceInfo $name
                if ($info.Kind -eq $kind -and (Get-OwnedProcess $info)) { throw "Stop Go $name before rebuilding its binary." }
            }
        }
        & go build -trimpath -o $target "./cmd/$kind"
        if ($LASTEXITCODE -ne 0) { throw "Go build failed: $kind" }
    }
    Write-Host 'Go binaries built.'
}

function Start-ServiceProcess([string]$Name) {
    $info = Get-ServiceInfo $Name
    $owned = Get-OwnedProcess $info
    if ($owned) { Write-Host "$Name already running (PID $($owned.ProcessId))."; return }
    if (!(Test-Path -LiteralPath $info.Exe)) { throw "Missing binary: $($info.Exe)" }
    if (Test-Path -LiteralPath $info.StopFile) { Remove-Item -LiteralPath $info.StopFile }
    $savedWorld = $env:MAPLEGO_WORLD_ID
    $savedHealth = $env:MAPLEGO_HEALTH_ADDR
    $savedStop = $env:MAPLEGO_STOP_FILE
    try {
        $env:MAPLEGO_WORLD_ID = [string]$info.WorldID
        $env:MAPLEGO_HEALTH_ADDR = $info.HealthAddr
        $env:MAPLEGO_STOP_FILE = $info.StopFile
        $process = Start-Process -FilePath $info.Exe -WorkingDirectory $Root -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $Logs "$Name.stdout.log") -RedirectStandardError (Join-Path $Logs "$Name.stderr.log")
        $identity = Get-CimInstance Win32_Process -Filter "ProcessId=$($process.Id)"
        Write-Utf8 $info.PidFile ([ordered]@{Pid=$process.Id;CreatedUtc=$identity.CreationDate.ToUniversalTime().ToString('o');Executable=$info.Exe;Health=$info.Health;Role=$Name} | ConvertTo-Json)
    } finally {
        $env:MAPLEGO_WORLD_ID=$savedWorld;$env:MAPLEGO_HEALTH_ADDR=$savedHealth;$env:MAPLEGO_STOP_FILE=$savedStop
    }
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    do {
        if (!(Get-OwnedProcess $info)) { throw "$Name exited during startup. See .local/logs/$Name.stderr.log" }
        try {
            $ready = Invoke-RestMethod -Uri ($info.Health+'/health/ready') -TimeoutSec 2
            if ($ready.pid -ne $process.Id) { throw 'Health endpoint is owned by a different process.' }
            Write-Host "$Name ready (PID $($process.Id))."
            return
        } catch { Start-Sleep -Milliseconds 250 }
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "$Name readiness timed out."
}

function Stop-ServiceProcess([string]$Name) {
    $info = Get-ServiceInfo $Name
    if (!(Get-OwnedProcess $info)) { Write-Host "$Name stopped."; return }
    Write-Utf8 $info.StopFile 'stop'
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    while ((Get-OwnedProcess $info) -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 250 }
    if (Get-OwnedProcess $info) { throw "$Name graceful stop timed out; left process running." }
    Write-Host "$Name stopped gracefully."
}

function Show-Status {
    foreach ($name in @('login','world0','world1','cashshop','admin')) {
        $info = Get-ServiceInfo $name
        $process = Get-OwnedProcess $info
        if ($process) { Write-Host "$name RUNNING (PID $($process.ProcessId)) $($info.Health)" } else { Write-Host "$name STOPPED" }
    }
    Assert-JavaUnchanged
}

try {
    switch ($Action) {
        'setup' { Setup-Infrastructure }
        'build' { Build-Services }
        'start' {
            Setup-Infrastructure
            if (!$SkipBuild) { Build-Services }
            Use-LocalEnvironment {
                & (Join-Path $Local 'bin\migrate.exe')
                if ($LASTEXITCODE -ne 0) { throw 'Versioned database migration failed.' }
                $names = if ($Service -eq 'all') { @('world0','world1','cashshop','login','admin') } else { @($Service) }
                foreach ($name in $names) { Start-ServiceProcess $name }
            }
            Assert-JavaUnchanged
        }
        'stop' {
            $names = if ($Service -eq 'all') { @('admin','login','cashshop','world1','world0') } else { @($Service) }
            foreach ($name in $names) { Stop-ServiceProcess $name }
            if ($StopInfrastructure) {
                foreach ($name in @('admin','login','cashshop','world1','world0')) { if (Get-OwnedProcess (Get-ServiceInfo $name)) { throw 'Go services still running; infrastructure left up.' } }
                & docker compose --project-name maple-go-local --env-file .local/compose.env down
                if ($LASTEXITCODE -ne 0) { throw 'Infrastructure stop failed.' }
            }
            Assert-JavaUnchanged
        }
        'status' { Show-Status }
        'test' {
            Use-LocalEnvironment {
                & (Join-Path $Local 'bin\smoke.exe')
                if ($LASTEXITCODE -ne 0) { throw 'Protocol integration test failed.' }
            }
            Assert-JavaUnchanged
        }
    }
} catch {
    Write-Error $_
    exit 1
}
