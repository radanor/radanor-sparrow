#Requires -Version 5.1
<#
    Radanor Sparrow CE-7A - native Windows 11 validation harness.

    Runs the real compiled binaries on a dedicated Windows 11 x64 test machine
    and records one of five statuses per requirement:

        PASS      - natively executed and verified
        FAIL      - executed and incorrect
        BLOCKED   - cannot execute safely, or the required access is missing
        NOT RUN   - no native environment available for this run
        SIMULATED - fixture-based evidence only

    Safety contract (the private engineering record):
      * read-only with respect to the machine: no Defender change, no firewall
        policy change, no BitLocker change, no service change, no scheduled
        task, no production Event Log clear, no registry write.
      * all state lives under -WorkRoot; the monitored folders are redirected
        into that scratch area so the real Startup folder and ~/.ssh are not
        written to unless -AllowRealMonitoredRoots is passed.
      * no credential or private key is embedded anywhere; the per-install IPC
        secret is read at run time from the isolated runtime directory and is
        never printed or persisted.

    Example:
        powershell -ExecutionPolicy Bypass -File .\Invoke-RadanorValidation.ps1
        powershell -File .\Invoke-RadanorValidation.ps1 -Stage environment,build,ipc,auth
#>
[CmdletBinding()]
param(
    # Resolved in the body below, NOT here: under Windows PowerShell 5.1 a
    # param-block default sees $PSScriptRoot as empty (reported by a native 5.1
    # run), so the old default
    #     (Split-Path -Parent (Split-Path -Parent $PSScriptRoot))
    # crashed at parameter binding. An explicit -RepoRoot is unchanged.
    [string]$RepoRoot = '',
    [string]$BinDir,
    [string]$WorkRoot = (Join-Path ([System.IO.Path]::GetTempPath()) 'radanor-sparrow-ce7a'),
    [string[]]$Stage = @('all'),
    [string]$ResultJson = '',
    [string]$ResultMarkdown = '',
    [switch]$SkipBuild,
    [switch]$UseRealBrowserLaunch,
    [switch]$AllowRealMonitoredRoots,
    [switch]$ContinueOnError
)

$ErrorActionPreference = 'Stop'

# --- locate the module and the repository root after parameter binding ------
# $PSCommandPath and $MyInvocation.MyCommand.Path are file-based references to
# this script and are populated by the time the body runs, on Windows PowerShell
# 5.1 and on PowerShell 7+, whatever the invocation style. $PSScriptRoot is
# consulted only as a last resort, because it is the one that read as empty on
# 5.1. This block runs before Set-StrictMode so that a missing optional
# reference falls through the chain instead of terminating the script.
$scriptRoot = ''
if ($PSCommandPath) { $scriptRoot = Split-Path -Parent $PSCommandPath }
if (-not $scriptRoot -and $MyInvocation.MyCommand.Path) { $scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path }
if (-not $scriptRoot) { $scriptRoot = $PSScriptRoot }
if (-not $scriptRoot) {
    throw 'Cannot determine where this script lives, so RadanorValidation.psm1 cannot be located. Run it from a copy of validation\windows, or from a standalone bundle, sitting on a real filesystem path.'
}
$scriptFile = Join-Path $scriptRoot 'Invoke-RadanorValidation.ps1'
$moduleFile = Join-Path $scriptRoot 'RadanorValidation.psm1'
if (-not (Test-Path -LiteralPath $moduleFile)) {
    throw "RadanorValidation.psm1 was not found at '$moduleFile'. Invoke-RadanorValidation.ps1 and RadanorValidation.psm1 must sit in the same folder - the repository's validation\windows, or a standalone bundle."
}
Import-Module (Resolve-Path -LiteralPath $moduleFile).Path -Force

$script:Layout = Resolve-ShieldHarnessLayout -ScriptPath $scriptFile -RequestedRepoRoot $RepoRoot
$RepoRoot = $script:Layout.RepoRoot

Set-StrictMode -Version Latest

if (-not $ResultJson) { $ResultJson = Join-Path $WorkRoot 'results.json' }
if (-not $ResultMarkdown) { $ResultMarkdown = Join-Path $WorkRoot 'results.md' }

Reset-ValidationResults
$script:Stage = @(Resolve-ShieldStages -Stages $Stage)

function Test-Stage {
    param([Parameter(Mandatory)][string]$Name)
    return ($script:Stage -contains 'all' -or $script:Stage -contains $Name)
}

function Get-Status {
    param([Parameter(Mandatory)][bool]$Ok)
    if ($Ok) { return 'PASS' }
    return 'FAIL'
}

# ---------------------------------------------------------------------------
# Shared run state
# ---------------------------------------------------------------------------

$script:Paths = $null
$script:Core = $null
$script:Gateway = $null
$script:Origin = $null
$script:Session = $null

# Row caps for cursor-walked reads. The API serves at most 50 rows per page, so
# these caps bound how many pages the harness will walk, not how many rows a
# single request returns. A read that stops at the cap is reported as truncated
# and is never treated as a complete inventory.
$script:EventRowCap = 200
$script:InventoryRowCap = 500
$script:EndpointRowCap = 500
$script:Csrf = $null
$script:Facts = $null
$script:Binaries = @{}
$script:Native = $null
$script:PostureNative = $null

function Resolve-Executable {
    param([Parameter(Mandatory)][string]$Name)
    if ($BinDir) {
        $candidate = Join-Path $BinDir $Name
        if (Test-Path $candidate) { return (Resolve-Path $candidate).Path }
    }
    $built = Join-Path $WorkRoot 'bin'
    $candidate = Join-Path $built $Name
    if (Test-Path $candidate) { return (Resolve-Path $candidate).Path }
    throw "$Name not found; pass -BinDir or run the build stage"
}

function Start-Shield {
    param(
        [switch]$RedirectRoots,
        [hashtable]$ExtraEnvironment = @{}
    )
    if ($script:Core) { Stop-ShieldProcess -Handle $script:Core }
    if ($script:Gateway) { Stop-ShieldProcess -Handle $script:Gateway }
    $script:Core = Start-ShieldProcess -Name 'core' -ExePath $script:Binaries['core'] `
        -Paths $script:Paths -ExtraEnvironment $ExtraEnvironment -RedirectMonitoredRoots:$RedirectRoots
    $coreLine = Wait-ShieldLogMatch -Handle $script:Core -Pattern '"msg":"core started"' -TimeoutSeconds 30
    if (-not $coreLine) { throw 'core did not log "core started"' }
    $script:Gateway = Start-ShieldProcess -Name 'gateway' -ExePath $script:Binaries['gateway'] `
        -Paths $script:Paths -ArgumentList @('-no-browser') -ExtraEnvironment $ExtraEnvironment `
        -RedirectMonitoredRoots:$RedirectRoots
    $script:Origin = Wait-ShieldGatewayOrigin -Handle $script:Gateway -TimeoutSeconds 30
    $script:Session = $null
    $script:Csrf = $null
    return $script:Origin
}

function Stop-Shield {
    if ($script:Gateway) { Stop-ShieldProcess -Handle $script:Gateway; $script:Gateway = $null }
    if ($script:Core) { Stop-ShieldProcess -Handle $script:Core; $script:Core = $null }
    $script:Origin = $null
    $script:Session = $null
    $script:Csrf = $null
}

function Connect-ShieldSession {
    $auth = New-ShieldSession -Origin $script:Origin -RuntimeDir $script:Paths.RuntimeDir -OpenBrowser:$UseRealBrowserLaunch
    if ($auth.StatusCode -ne 200) { throw "session exchange failed with $($auth.StatusCode): $($auth.Body)" }
    $script:Session = $auth.Session
    $script:Csrf = $auth.CsrfToken
    return $auth
}

function Invoke-Api {
    param(
        [Parameter(Mandatory)][string]$Path,
        [ValidateSet('GET', 'POST')][string]$Method = 'GET',
        [string]$Body,
        [switch]$WithCsrf,
        [switch]$WithOrigin,
        [string]$OverrideOrigin
    )
    $headers = @{}
    $origin = if ($OverrideOrigin) { $OverrideOrigin } else { $script:Origin }
    if ($WithOrigin -or $WithCsrf) { $headers['Origin'] = $origin }
    if ($WithCsrf -and $script:Csrf) { $headers['X-CSRF-Token'] = $script:Csrf }
    $params = @{ Uri = ($script:Origin + $Path); Method = $Method; Headers = $headers }
    if ($PSBoundParameters.ContainsKey('Body')) { $params['Body'] = $Body }
    if ($script:Session) { $params['WebSession'] = $script:Session }
    return (Invoke-ShieldHttp @params)
}

function Get-Json {
    param([Parameter(Mandatory)][object]$Response)
    if (-not $Response.Body) { return $null }
    try { return ($Response.Body | ConvertFrom-Json) } catch { return $null }
}

# The API rejects a page larger than 50 rows with HTTP 400 and caps a single
# page at 50 rows, so a multi-row read must walk the cursor. These wrappers bind
# the module's cursor walk to this script's authenticated session.
function Get-EventRecords {
    param([int]$MaxRows = 200, [string]$Source, [string]$Type)
    return (Get-PagedEvents -MaxRows $MaxRows -Source $Source -Type $Type `
        -Request { param($p) Get-Json (Invoke-Api -Path $p) })
}

function Get-InventoryRows {
    param(
        [Parameter(Mandatory)][ValidateSet('software', 'endpoints', 'services')][string]$Kind,
        [int]$MaxRows = 500,
        [string]$Search
    )
    return (Get-PagedInventory -Kind $Kind -MaxRows $MaxRows -Search $Search `
        -Request { param($p) Get-Json (Invoke-Api -Path $p) })
}

function Wait-For {
    param(
        [Parameter(Mandatory)][scriptblock]$Condition,
        [int]$TimeoutSeconds = 30,
        [int]$IntervalMs = 500
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        try { if (& $Condition) { return $true } } catch { }
        Start-Sleep -Milliseconds $IntervalMs
    }
    return $false
}

# ===========================================================================
# Stage: environment
# ===========================================================================
function Invoke-StageEnvironment {
    $script:Facts = Get-WindowsEnvironmentFacts
    # The supported-platform decision is made by the module, from the OS build
    # number and the CIM Caption. It is never made from the registry
    # ProductName, which still says "Windows 10 ..." on some genuine Windows 11
    # images. See Get-WindowsPlatformClassification and
    # validation/windows/Test-PlatformDetection.ps1.
    $platform = $script:Facts.Platform
    $supported = $platform.IsX64OperatingSystem
    $family = $platform.VersionFamily
    $buildText = if ($platform.BuildKnown) { [string]$platform.BuildNumber } else { 'unknown' }
    $brandingConflicts = @($platform.BrandingConflicts)

    Add-ValidationResult -Id 'W-ENV-01' -Area 'environment' -Requirement 'Windows build and architecture recorded' `
        -Status (Get-Status ($platform.BuildKnown -or $null -ne $script:Facts.BuildString)) `
        -Observation ('{0} {1}; build {2} (registry {3}, CIM {4}) from {5}; version family {6}; {7}' -f $script:Facts.WindowsProductName, $script:Facts.DisplayVersion, $buildText, $script:Facts.BuildString, $script:Facts.CIMBuildNumber, $platform.BuildSource, $family, $script:Facts.Architecture) `
        -Evidence ($script:Facts | ConvertTo-Json -Depth 8 -Compress)
    Add-ValidationResult -Id 'W-ENV-02' -Area 'environment' -Requirement '64-bit Windows and 64-bit process' `
        -Status (Get-Status ($script:Facts.Is64BitOS -and $script:Facts.Is64BitProcess)) `
        -Observation ('Is64BitOS={0} Is64BitProcess={1}' -f $script:Facts.Is64BitOS, $script:Facts.Is64BitProcess)
    Add-ValidationResult -Id 'W-ENV-03' -Area 'environment' -Requirement 'Current user and privilege level recorded' `
        -Status 'PASS' `
        -Observation ('user={0} elevated={1} culture={2} uiCulture={3} ps={4}' -f $script:Facts.User, $script:Facts.Elevated, $script:Facts.Culture, $script:Facts.UICulture, $script:Facts.PSVersion)
    # W-ENV-04 is decided by the authoritative OS build number, corroborated by
    # the CIM Caption. Conflicting branding is recorded, not treated as a
    # failure: a Windows 11 host whose registry ProductName still reads
    # "Windows 10 ..." is a supported platform with a recorded diagnostic. An
    # unknown or invalid build number, or a non-x64 host, is NOT supported.
    $targetObservation = 'versionFamily={0} build={1} (source: {2}) x64OS={3} x64Process={4} processArchitecture={5} osArchitecture={6} caption="{7}" productName="{8}" legacyProductNameBranding={9}' -f $family, $buildText, $platform.BuildSource, $platform.IsX64OperatingSystem, $platform.IsX64Process, $platform.ProcessArchitecture, $platform.OsArchitecture, $platform.Caption, $platform.ProductName, $platform.LegacyProductNameBranding
    if ($brandingConflicts.Count -gt 0) { $targetObservation += ' | branding: ' + ($brandingConflicts -join ' ') }
    if (-not $platform.IsSupported) { $targetObservation += ' | NOT SUPPORTED: ' + $platform.UnsupportedReason }
    Add-ValidationResult -Id 'W-ENV-04' -Area 'environment' -Requirement 'Target is Windows 11 x64' `
        -Status (Get-Status $platform.IsSupported) `
        -Observation $targetObservation `
        -Evidence ($platform | ConvertTo-Json -Depth 6 -Compress)
    if ($platform.LegacyProductNameBranding) {
        Write-Warning ('W-ENV-04: legacy Windows 10 product branding recorded on this {0} host (build {1}) and ignored; the OS build number is authoritative.' -f $family, $buildText)
    }
    foreach ($note in @($platform.Diagnostics)) { Write-Host ('            note: {0}' -f $note) -ForegroundColor DarkGray }
    if (-not $supported) {
        Add-ValidationResult -Id 'W-ENV-05' -Area 'environment' -Status 'FAIL' `
            -Requirement 'Host architecture is x64 as required by the Windows plan' `
            -Observation ("PROCESSOR_ARCHITECTURE='{0}' PROCESSOR_ARCHITEW6432='{1}' OSArchitecture='{2}'" -f $script:Facts.Architecture, $script:Facts.Architecture6432, $script:Facts.OSArchitecture)
    } else {
        Add-ValidationResult -Id 'W-ENV-05' -Area 'environment' -Status 'PASS' `
            -Requirement 'Host architecture is x64 as required by the Windows plan' `
            -Observation ("PROCESSOR_ARCHITECTURE='{0}' OSArchitecture='{1}'" -f $script:Facts.Architecture, $script:Facts.OSArchitecture)
    }
}

# ===========================================================================
# Stage: build
# ===========================================================================
function Invoke-StageBuild {
    $outDir = Join-Path $WorkRoot 'bin'
    if (-not (Test-Path $outDir)) { New-Item -ItemType Directory -Path $outDir -Force | Out-Null }
    $go = Get-Command go -ErrorAction SilentlyContinue
    if ($SkipBuild) {
        $script:Binaries['core'] = Resolve-Executable -Name 'radanor-sparrow-core.exe'
        $script:Binaries['gateway'] = Resolve-Executable -Name 'radanor-sparrow-gateway.exe'
        Add-ValidationResult -Id 'W-BLD-01' -Area 'build' -Status 'PASS' `
            -Requirement 'Prebuilt Windows binaries supplied and located' `
            -Observation ("core={0}`ngateway={1}" -f $script:Binaries['core'], $script:Binaries['gateway'])
        return
    }
    if (-not $go) {
        Add-ValidationResult -Id 'W-BLD-01' -Area 'build' -Status 'BLOCKED' `
            -Requirement 'go build ./cmd/... succeeds natively on Windows amd64' `
            -Observation 'Go toolchain is not installed on this machine and no -BinDir was supplied. Install Go 1.27.x or pass -BinDir with prebuilt .exe files.'
        return
    }
    if (-not $RepoRoot -or -not (Test-Path -LiteralPath (Join-Path $RepoRoot 'go.mod'))) {
        # A standalone binary-only package has no source tree to build from.
        # Recording BLOCKED rather than letting Push-Location or go build fail
        # keeps the row honest: nothing was executed and observed to be wrong.
        Add-ValidationResult -Id 'W-BLD-01' -Area 'build' -Status 'BLOCKED' `
            -Requirement 'go build ./cmd/... succeeds natively on Windows amd64' `
            -Observation ("No Go module found at the repository root '{0}' ({1}). This is a standalone package: run it with -SkipBuild and -BinDir, or pass -RepoRoot pointing at a Radanor Sparrow checkout." -f $RepoRoot, $script:Layout.RepoRootSource)
        return
    }
    $buildOut = @()
    Push-Location $RepoRoot
    try {
        & $go.Source build -o $outDir ./cmd/... 2>&1 | Tee-Object -Variable buildOut | Out-Null
        $buildExit = $LASTEXITCODE
    } finally { Pop-Location }
    $core = Join-Path $outDir 'radanor-sparrow-core.exe'
    $gateway = Join-Path $outDir 'radanor-sparrow-gateway.exe'
    $ok = ($buildExit -eq 0 -and (Test-Path $core) -and (Test-Path $gateway))
    Add-ValidationResult -Id 'W-BLD-01' -Area 'build' -Status (Get-Status $ok) `
        -Requirement 'go build ./cmd/... succeeds natively on Windows amd64' `
        -Observation ("exit={0}`n{1}" -f $buildExit, ($buildOut -join "`n"))
    if ($ok) {
        $script:Binaries['core'] = (Resolve-Path $core).Path
        $script:Binaries['gateway'] = (Resolve-Path $gateway).Path
        $f1 = Get-Item $script:Binaries['core']
        $f2 = Get-Item $script:Binaries['gateway']
        Add-ValidationResult -Id 'W-BLD-02' -Area 'build' -Status 'PASS' `
            -Requirement 'Both Windows executables produced' `
            -Observation ("core {0} bytes; gateway {1} bytes" -f $f1.Length, $f2.Length)
    }
}

# ===========================================================================
# Stage: startup
# ===========================================================================
function Invoke-StageStartup {
    if (-not $script:Binaries.ContainsKey('core')) {
        Add-ValidationResult -Id 'W-STA-01' -Area 'startup' -Status 'NOT RUN' `
            -Requirement 'Core and gateway startup' -Observation 'No binaries resolved (build stage not run or blocked).'
        return
    }
    try {
        Start-Shield -RedirectRoots:(-not $AllowRealMonitoredRoots) | Out-Null
        Add-ValidationResult -Id 'W-STA-01' -Area 'startup' -Status 'PASS' `
            -Requirement 'radanor-sparrow-core.exe starts as a standard user, no elevation prompt, logs "core started"' `
            -Observation ('elevated session={0}; core logged "core started"' -f $script:Facts.Elevated)
        Add-ValidationResult -Id 'W-STA-02' -Area 'startup' -Status 'PASS' `
            -Requirement 'radanor-sparrow-gateway.exe starts, logs "gateway started" with a loopback origin' `
            -Observation $script:Origin
    } catch {
        Add-ValidationResult -Id 'W-STA-01' -Area 'startup' -Status 'FAIL' `
            -Requirement 'radanor-sparrow-core.exe starts as a standard user, no elevation prompt, logs "core started"' `
            -Observation ("$($_.Exception.Message)")
        return
    }

    # Second concurrent start must be refused by the instance lock.
    $second = Start-ShieldProcess -Name 'core-second' -ExePath $script:Binaries['core'] -Paths $script:Paths `
        -RedirectMonitoredRoots:(-not $AllowRealMonitoredRoots)
    Start-Sleep -Seconds 2
    $exited = $second.Process.HasExited
    $secondLog = if (Test-Path $second.LogPath) { (Get-Content $second.LogPath -Raw -ErrorAction SilentlyContinue) } else { '' }
    Stop-ShieldProcess -Handle $second
    $refused = ($exited -and $secondLog -match 'already running')
    Add-ValidationResult -Id 'W-STA-03' -Area 'startup' -Status (Get-Status $refused) `
        -Requirement 'A second concurrent core start is refused by the instance lock, not a second listener' `
        -Observation ("exited={0} log={1}" -f $exited, (($secondLog -replace "`r?`n", ' ').Trim()))

    # Elevation refusal. The gateway must refuse to run from an elevated token.
    if ($script:Facts.Elevated) {
        $el = Start-ShieldProcess -Name 'gateway-elevated' -ExePath $script:Binaries['gateway'] -Paths $script:Paths `
            -ArgumentList @('-no-browser') -RedirectMonitoredRoots:(-not $AllowRealMonitoredRoots)
        Start-Sleep -Seconds 2
        $elExited = $el.Process.HasExited
        $elLog = if (Test-Path $el.LogPath) { (Get-Content $el.LogPath -Raw -ErrorAction SilentlyContinue) } else { '' }
        Stop-ShieldProcess -Handle $el
        $refusedElev = ($elExited -and $elLog -match 'refused')
        Add-ValidationResult -Id 'W-STA-04' -Area 'startup' -Status (Get-Status $refusedElev) `
            -Requirement 'Starting the gateway from an elevated prompt is refused with an explicit error' `
            -Observation ("exited={0} log={1}" -f $elExited, (($elLog -replace "`r?`n", ' ').Trim()))
    } else {
        Add-ValidationResult -Id 'W-STA-04' -Area 'startup' -Status 'BLOCKED' `
            -Requirement 'Starting the gateway from an elevated prompt is refused with an explicit error' `
            -Observation 'This harness run is not elevated, and UAC elevation cannot be automated safely. Manual step: from an elevated prompt run radanor-sparrow-gateway.exe -no-browser and confirm "gateway start refused".'
    }

    # No elevation prompt anywhere in the workflow (plan item 24).
    Add-ValidationResult -Id 'W-STA-05' -Area 'startup' -Status 'PASS' `
        -Requirement 'No elevation prompt appears anywhere in the automated workflow (plan item 24)' `
        -Observation 'Every process in this run was started with UseShellExecute=false and no verb; a UAC prompt would have aborted the run.'
}

# ===========================================================================
# Stage: ipc (named pipe, HMAC, ticket)
# ===========================================================================
function Invoke-StageIpc {
    if (-not $script:Core) {
        Add-ValidationResult -Id 'W-IPC-01' -Area 'ipc' -Status 'NOT RUN' -Requirement 'Named-pipe IPC' `
            -Observation 'Core is not running.'
        return
    }
    $secretPath = Join-Path $script:Paths.RuntimeDir 'ipc.key'
    $secretOk = Test-Path $secretPath
    Add-ValidationResult -Id 'W-IPC-01' -Area 'ipc' -Status (Get-Status $secretOk) `
        -Requirement 'Per-install IPC secret exists in the isolated runtime directory' `
        -Observation ("path={0} (value never read into the report)" -f $secretPath)

    $acl = $null
    $acl = (Get-Acl -LiteralPath $secretPath).Sddl
    $aclOk = (Test-ShieldPrivateAcl -Path $secretPath)
    Add-ValidationResult -Id 'W-IPC-02' -Area 'ipc' -Status (Get-Status $aclOk) `
        -Requirement 'IPC secret DACL grants only the current user' `
        -Observation $acl

    try {
        $ticket = Get-ShieldLaunchTicket -RuntimeDir $script:Paths.RuntimeDir
        $ticketOk = ($ticket -and $ticket.Length -ge 16)
        Add-ValidationResult -Id 'W-IPC-03' -Area 'ipc' -Status (Get-Status $ticketOk) `
            -Requirement 'Named-pipe IPC accepts a correctly signed request (issue_ticket) and returns a ticket' `
            -Observation ("ticket length={0}; value not recorded" -f $(if ($ticket) { $ticket.Length } else { 0 }))
    } catch {
        Add-ValidationResult -Id 'W-IPC-03' -Area 'ipc' -Status 'FAIL' `
            -Requirement 'Named-pipe IPC accepts a correctly signed request (issue_ticket) and returns a ticket' `
            -Observation "$($_.Exception.Message)"
        return
    }

    # A request signed with the wrong key must be dropped without a response.
    $bogus = New-Object byte[] 32
    for ($i = 0; $i -lt 32; $i++) { $bogus[$i] = [byte](($i * 7 + 3) % 256) }
    $rejected = $false
    $reason = ''
    try {
        $r = Invoke-ShieldIpc -Secret $bogus -Operation 'issue_ticket' -TimeoutSeconds 3
        if ($null -eq $r) { $rejected = $true; $reason = 'no response' } else { $reason = "unexpected response: $($r | ConvertTo-Json -Compress)" }
    } catch { $rejected = $true; $reason = "connection dropped: $($_.Exception.Message)" }
    Add-ValidationResult -Id 'W-IPC-04' -Area 'ipc' -Status (Get-Status $rejected) `
        -Requirement 'A request signed with a wrong secret is refused, not served' `
        -Observation $reason

    # The pipe DACL must scope access to the current user SID.
    $sid = $script:Facts.UserId
    $pipePath = '\\.\pipe\RadanorShieldCore'
    $sd = $null
    try {
        # Get-Acl does not read named pipes; use the raw security descriptor via
        # [System.IO.Pipes] ownership is not exposed, so fall back to a live
        # connect test as the observable proof that the pipe exists and answers.
        $probe = New-Object System.IO.Pipes.NamedPipeClientStream('.', 'RadanorShieldCore', [System.IO.Pipes.PipeDirection]::InOut)
        $probe.Connect(3000)
        $probe.Dispose()
        $sd = 'pipe reachable by the current user'
    } catch { $sd = "pipe not reachable: $($_.Exception.Message)" }
    Add-ValidationResult -Id 'W-IPC-05' -Area 'ipc' -Status (Get-Status ($sd -match 'reachable by')) `
        -Requirement 'The Windows named pipe is reachable by the current user' `
        -Observation ("{0}; SID={1}; a cross-user DACL denial is covered by W-IPC-02 on the secret and by the kernel pipe DACL in internal/ipc/platform_windows.go (no second account available here)" -f $sd, $sid)
}

# ===========================================================================
# Stage: auth (session + CSRF)
# ===========================================================================
function Invoke-StageAuth {
    if (-not $script:Origin) {
        Add-ValidationResult -Id 'W-AUT-01' -Area 'auth' -Status 'NOT RUN' -Requirement 'Browser authentication and session/CSRF enforcement' `
            -Observation 'Gateway origin unavailable.'
        return
    }

    # Unauthenticated reads must be 401.
    $reads = @('/api/v1/foundation', '/api/v1/monitoring/status', '/api/v1/assessment/status',
        '/api/v1/inventory/status', '/api/v1/settings')
    $bad = @()
    foreach ($p in $reads) {
        $r = Invoke-ShieldHttp -Uri ($script:Origin + $p) -Method GET
        if ($r.StatusCode -ne 401) { $bad += ('{0}={1}' -f $p, $r.StatusCode) }
    }
    Add-ValidationResult -Id 'W-AUT-01' -Area 'auth' -Status (Get-Status ($bad.Count -eq 0)) `
        -Requirement 'Every read endpoint returns 401 without a session' `
        -Observation $(if ($bad.Count -eq 0) { "$($reads.Count) endpoints returned 401" } else { 'unexpected: ' + ($bad -join ', ') })

    $postUnauth = Invoke-ShieldHttp -Uri ($script:Origin + '/api/v1/inventory/refresh') -Method POST `
        -Headers @{ Origin = $script:Origin } -Body '{}'
    Add-ValidationResult -Id 'W-AUT-02' -Area 'auth' -Status (Get-Status ($postUnauth.StatusCode -eq 401)) `
        -Requirement 'Every state-changing endpoint returns 401 without a session, even with a same-origin header' `
        -Observation ("POST /api/v1/inventory/refresh -> {0}" -f $postUnauth.StatusCode)

    # Browser authentication: ticket -> session cookie + CSRF token.
    try {
        $auth = Connect-ShieldSession
        $ok = ($auth.StatusCode -eq 200 -and $auth.CsrfToken)
        Add-ValidationResult -Id 'W-AUT-03' -Area 'auth' -Status (Get-Status ([bool]$ok)) `
            -Requirement 'Browser launch URL authenticates: ticket exchanged for a session cookie and a CSRF token' `
            -Observation ("exchange={0}; csrf token length={1}; launch url host={2}" -f $auth.StatusCode, $auth.CsrfToken.Length, ($auth.LaunchUrl -replace '#ticket=.*', '#ticket=<redacted>'))
        Add-ValidationResult -Id 'W-AUT-04' -Area 'auth' -Status 'NOT RUN' `
            -Requirement 'The default browser actually opened with the ticket fragment (plan item: browser authentication)' `
            -Observation 'The ticket URL was constructed and (optionally) handed to Start-Process. Whether a visible browser window appeared must be confirmed by the operator; fragments are never sent in HTTP requests or logs.'
    } catch {
        Add-ValidationResult -Id 'W-AUT-03' -Area 'auth' -Status 'FAIL' `
            -Requirement 'Browser launch URL authenticates: ticket exchanged for a session cookie and a CSRF token' `
            -Observation "$($_.Exception.Message)"
        return
    }

    # Ticket replay must fail (single use).
    $replay = Get-ShieldLaunchTicket -RuntimeDir $script:Paths.RuntimeDir
    $first = Invoke-ShieldHttp -Uri ($script:Origin + '/api/v1/session/exchange') -Method POST `
        -Headers @{ Origin = $script:Origin } -Body ('{{"ticket":"{0}"}}' -f $replay)
    $second = Invoke-ShieldHttp -Uri ($script:Origin + '/api/v1/session/exchange') -Method POST `
        -Headers @{ Origin = $script:Origin } -Body ('{{"ticket":"{0}"}}' -f $replay)
    Add-ValidationResult -Id 'W-AUT-05' -Area 'auth' -Status (Get-Status ($first.StatusCode -eq 200 -and $second.StatusCode -eq 401)) `
        -Requirement 'A launch ticket is single use: the replay is refused' `
        -Observation ("first={0} replay={1}" -f $first.StatusCode, $second.StatusCode)

    # CSRF token required on state-changing calls.
    $noCsrf = Invoke-ShieldHttp -Uri ($script:Origin + '/api/v1/settings') -Method POST `
        -Headers @{ Origin = $script:Origin } -Body '{"theme":"dark"}' -WebSession $script:Session
    Add-ValidationResult -Id 'W-AUT-06' -Area 'auth' -Status (Get-Status ($noCsrf.StatusCode -eq 401)) `
        -Requirement 'A signed-in POST without the X-CSRF-Token header is refused' `
        -Observation ("POST /api/v1/settings without CSRF -> {0}" -f $noCsrf.StatusCode)

    $okCsrf = Invoke-Api -Path '/api/v1/settings' -Method POST -WithCsrf -Body '{"theme":"system"}'
    Add-ValidationResult -Id 'W-AUT-07' -Area 'auth' -Status (Get-Status ($okCsrf.StatusCode -eq 200)) `
        -Requirement 'A signed-in POST with Origin and X-CSRF-Token is accepted' `
        -Observation ("POST /api/v1/settings with CSRF -> {0}" -f $okCsrf.StatusCode)

    # Foreign Origin while signed in must be refused.
    $foreign = Invoke-Api -Path '/api/v1/inventory/refresh' -Method POST -WithCsrf -WithOrigin `
        -OverrideOrigin 'http://evil.test' -Body '{}'
    Add-ValidationResult -Id 'W-AUT-08' -Area 'auth' -Status (Get-Status ($foreign.StatusCode -eq 401)) `
        -Requirement 'A signed-in request carrying a foreign Origin is refused' `
        -Observation ("POST /api/v1/inventory/refresh with Origin http://evil.test -> {0}" -f $foreign.StatusCode)

    # Session protection: session cookie is not readable from JavaScript-free
    # unauthenticated callers, and logout invalidates it.
    $logout = Invoke-Api -Path '/api/v1/session/logout' -Method POST -WithCsrf -Body '{}'
    $afterLogout = Invoke-ShieldHttp -Uri ($script:Origin + '/api/v1/settings') -Method GET -WebSession $script:Session
    Add-ValidationResult -Id 'W-AUT-09' -Area 'auth' -Status (Get-Status ($logout.StatusCode -eq 204 -and $afterLogout.StatusCode -eq 401)) `
        -Requirement 'Logout invalidates the session' `
        -Observation ("logout={0}; subsequent read={1}" -f $logout.StatusCode, $afterLogout.StatusCode)
    # Re-establish a session for the remaining stages.
    try { Connect-ShieldSession | Out-Null } catch { Write-Warning "re-authentication failed: $_" }
}

# ===========================================================================
# Stage: storage (SQLite + permissions)
# ===========================================================================
function Invoke-StageStorage {
    $dbPath = Join-Path $script:Paths.StateDir 'monitoring.db'
    $exists = Test-Path $dbPath
    $size = if ($exists) { (Get-Item $dbPath).Length } else { 0 }
    Add-ValidationResult -Id 'W-STO-01' -Area 'storage' -Status (Get-Status $exists) `
        -Requirement 'SQLite database is created inside the isolated state directory' `
        -Observation ("{0} bytes at {1}" -f $size, $dbPath)

    $aclText = ''
    $aclText = (Get-Acl -LiteralPath $dbPath).Sddl
    $inheritanceRemoved = (Test-ShieldPrivateAcl -Path $dbPath)
    Add-ValidationResult -Id 'W-STO-02' -Area 'storage' -Status (Get-Status $inheritanceRemoved) `
        -Requirement 'Database file permissions are restricted to the current user' `
        -Observation $aclText

    $stateAcl = ''
    $stateAcl = (Get-Acl -LiteralPath $script:Paths.StateDir).Sddl
    $dirOk = (Test-ShieldPrivateAcl -Path $script:Paths.StateDir)
    Add-ValidationResult -Id 'W-STO-03' -Area 'storage' -Status (Get-Status $dirOk) `
        -Requirement 'State directory grants are not broader than the current user' `
        -Observation $stateAcl

    if ($script:Session) {
        $storageResponse = Invoke-Api -Path '/api/v1/settings'
        $s = Get-Json $storageResponse
        if ($storageResponse.StatusCode -eq 200 -and $s -and $s.PSObject.Properties['storage'] -and $s.storage) {
            $reported = [int64]$s.storage.database_bytes
            $diff = [math]::Abs($reported - $size)
            $ok = ($exists -and $diff -le 1048576)
            Add-ValidationResult -Id 'W-STO-04' -Area 'storage' -Status (Get-Status $ok) `
                -Requirement 'Storage panel agrees with the database file size on disk (plan item 20)' `
                -Observation ("reported={0} on-disk={1} delta={2}" -f $reported, $size, $diff)
        } else {
            Add-ValidationResult -Id 'W-STO-04' -Area 'storage' -Status 'FAIL' `
                -Requirement 'Storage panel agrees with the database file size on disk (plan item 20)' `
                -Observation ('settings read failed or lacks storage: HTTP={0} transport={1}' -f $storageResponse.StatusCode, $storageResponse.TransportError)
        }
    }
}

# ===========================================================================
# Stage: monitoring (CE-7A section 3)
# ===========================================================================
function Test-MonitoredFileOperations {
    param(
        [Parameter(Mandatory)][string]$Label,
        [Parameter(Mandatory)][string]$Root
    )
    $file = Join-Path $Root ('ce7a-probe-{0}.txt' -f ([guid]::NewGuid().ToString('N').Substring(0, 8)))
    $renamed = $file -replace '\.txt$', '.renamed'
    $names = @((Split-Path -Leaf $file), (Split-Path -Leaf $renamed))
    $required = @('file.created','file.modified','file.renamed_from','file.renamed_to','file.deleted')
    try {
        Set-Content -LiteralPath $file -Value 'ce-7a probe create' -Encoding UTF8
        Start-Sleep -Milliseconds 400
        Add-Content -LiteralPath $file -Value 'ce-7a probe modify append' -Encoding UTF8
        Start-Sleep -Milliseconds 400
        Rename-Item -LiteralPath $file -NewName (Split-Path -Leaf $renamed)
        Start-Sleep -Milliseconds 400
        Remove-Item -LiteralPath $renamed
    } catch {
        return [pscustomobject]@{ Ok = $false; Detail = "file operation failed: $_"; Count = 0 }
    }
    $saw = Wait-For -TimeoutSeconds 30 -Condition {
        $page = Get-EventRecords -MaxRows $script:EventRowCap
        if ($page.Failed) { return $false }
        $rows = @($page.Rows | Where-Object { $_.metadata.PSObject.Properties['name'] -and $names -contains $_.metadata.name })
        $types = @($rows | ForEach-Object { $_.type } | Sort-Object -Unique)
        return (@($required | Where-Object { $types -notcontains $_ }).Count -eq 0)
    }
    $page = Get-EventRecords -MaxRows $script:EventRowCap
    $mine = @($page.Rows | Where-Object { $_.metadata.PSObject.Properties['name'] -and $names -contains $_.metadata.name })
    $types = @($mine | ForEach-Object { $_.type } | Sort-Object -Unique)
    [pscustomobject]@{
        Ok = ($saw -and -not $page.Failed)
        Detail = ("{0}: observed={1} event types=[{2}] missing=[{3}]" -f $Label, $mine.Count, ($types -join ', '), (@($required | Where-Object { $types -notcontains $_ }) -join ', '))
        Count = $mine.Count
        Types = $types
    }
}

function Invoke-StageMonitoring {
    if (-not $script:Session) {
        Add-ValidationResult -Id 'W-MON-01' -Area 'monitoring' -Status 'NOT RUN' -Requirement 'Event monitoring' `
            -Observation 'No authenticated session.'
        return
    }
    $roots = @()
    if ($AllowRealMonitoredRoots) {
        $appData = $env:APPDATA
        if ($appData) { $roots += @{ Label = 'startup'; Path = (Join-Path $appData 'Microsoft\Windows\Start Menu\Programs\Startup') } }
        $ssh = Join-Path $HOME '.ssh'
        if (Test-Path $ssh) { $roots += @{ Label = 'ssh'; Path = $ssh } }
        Add-ValidationResult -Id 'W-MON-00' -Area 'monitoring' -Status 'BLOCKED' `
            -Requirement 'Monitored folders are the real user Startup folder and ~/.ssh' `
            -Observation '-AllowRealMonitoredRoots was passed: this run writes probe files into real user folders and removes them again. Record the operator approval for this in the run notes.'
    } else {
        $roots += @{ Label = 'startup'; Path = $script:Paths.MonitoredStartup }
        $roots += @{ Label = 'ssh'; Path = $script:Paths.MonitoredSSH }
        Add-ValidationResult -Id 'W-MON-00' -Area 'monitoring' -Status 'PASS' `
            -Requirement 'Monitored folders are isolated from the real user Startup folder and ~/.ssh' `
            -Observation ("APPDATA and USERPROFILE were redirected: startup={0} ssh={1}" -f $script:Paths.MonitoredStartup, $script:Paths.MonitoredSSH)
    }

    foreach ($r in $roots) {
        $res = Test-MonitoredFileOperations -Label $r.Label -Root $r.Path
        $status = if ($res.Ok) { 'PASS' } else { 'FAIL' }
        if (-not (Test-Path $r.Path)) { $status = 'BLOCKED' }
        Add-ValidationResult -Id ("W-MON-{0}" -f ($r.Label.ToUpper())) -Area 'monitoring' -Status $status `
            -Requirement ("Create, modify, rename and delete observations in the {0} folder" -f $r.Label) `
            -Observation $res.Detail
    }

    # A real multi-page event read, independent of offline cursor fixtures.
    $prefix = 'ce7a-page-' + [guid]::NewGuid().ToString('N')
    $probePaths = @(1..26 | ForEach-Object { Join-Path $script:Paths.MonitoredStartup ($prefix + '-' + $_ + '.txt') })
    try {
        foreach ($path in $probePaths) { Set-Content -LiteralPath $path -Value 'cursor probe'; Start-Sleep -Milliseconds 25 }
        foreach ($path in $probePaths) { Remove-Item -LiteralPath $path; Start-Sleep -Milliseconds 25 }
        $paged = Wait-For -TimeoutSeconds 30 -Condition {
            $page = Get-EventRecords -MaxRows $script:EventRowCap
            $mine = @($page.Rows | Where-Object { $_.metadata.PSObject.Properties['name'] -and $_.metadata.name -like ($prefix + '*') })
            return (-not $page.Failed -and $page.Pages -ge 2 -and $mine.Count -ge 52)
        }
        $page = Get-EventRecords -MaxRows $script:EventRowCap
        $ids = @($page.Rows | ForEach-Object { $_.event_id })
        $unique = @($ids | Sort-Object -Unique).Count
        Add-ValidationResult -Id 'W-MON-PAGINATION' -Area 'monitoring' -Status (Get-Status ($paged -and $unique -eq $ids.Count)) `
            -Requirement 'Native event cursor pagination returns multiple pages without duplicate event identities' `
            -Observation ('pages={0} rows={1} distinct={2} failed={3} clipped={4}' -f $page.Pages, $ids.Count, $unique, $page.Failed, $page.Truncated)
    } finally {
        foreach ($path in $probePaths) { if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path } }
    }

    # Source health.
    $ms = Get-Json (Invoke-Api -Path '/api/v1/monitoring/status')
    if ($ms) {
        $fileSrc = @($ms.sources | Where-Object { $_.id -eq 'native.file_integrity' })
        $healthy = @($fileSrc | Where-Object { $_.state -eq 'healthy' }).Count -gt 0
        Add-ValidationResult -Id 'W-MON-HEALTH' -Area 'monitoring' -Status (Get-Status $healthy) `
            -Requirement 'Native file-integrity source reports healthy after the watches start' `
            -Observation (($fileSrc | ForEach-Object { '{0}={1} last_error={2}' -f $_.id, $_.state, $(if ($_.PSObject.Properties['last_error']) { $_.last_error } else { '' }) }) -join '; ')
        $wevt = @($ms.sources | Where-Object { $_.id -like '*eventlog*' -or $_.id -like '*wevt*' -or $_.id -like '*defender*' })
        if ($wevt.Count -gt 0) {
            Add-ValidationResult -Id 'W-MON-WEVT' -Area 'monitoring' -Status $(if (@($wevt | Where-Object { $_.state -eq 'healthy' }).Count -gt 0) { 'PASS' } else { 'BLOCKED' }) `
                -Requirement 'Windows Event Log source is present with its descriptor and limitations' `
                -Observation (($wevt | ForEach-Object { '{0}={1} recovery={2} error={3}' -f $_.id, $_.state, $_.recovery, $(if ($_.PSObject.Properties['last_error']) { $_.last_error } else { '' }) }) -join '; ')
        } else {
            Add-ValidationResult -Id 'W-MON-WEVT' -Area 'monitoring' -Status 'FAIL' `
                -Requirement 'Windows Event Log source is present with its descriptor and limitations' `
                -Observation 'no Event Log source reported by monitoring_status'
        }
        Add-ValidationResult -Id 'W-MON-DEFENDER' -Area 'monitoring' -Status 'NOT RUN' `
            -Requirement 'Microsoft Defender Operational events are observed natively' `
            -Observation 'Defender only writes to its Operational channel when it acts (a detection, a quarantine action or a configuration change). This harness never triggers one. Confirmation requires a real detection on the test machine or an operator-supplied event; the subscription itself is checked by W-MON-WEVT.'
        if ($ms.PSObject.Properties['ingest']) {
            Add-ValidationResult -Id 'W-MON-INGEST' -Area 'monitoring' -Status 'PASS' `
                -Requirement 'Genuine replay versus duplicate observations are distinguishable in the reported counters' `
                -Observation (($ms.ingest | ForEach-Object { '{0}: inserted={1} duplicates={2} collisions={3} rejected={4} failed={5}' -f $_.source, $_.inserted, $_.duplicates, $_.collisions, $_.rejected, $_.failed }) -join '; ')
        }
        $gapResponse = Invoke-Api -Path '/api/v1/monitoring/gaps?limit=50'
        $gaps = Get-Json $gapResponse
        Add-ValidationResult -Id 'W-MON-GAPS' -Area 'monitoring' -Status (Get-Status ($gapResponse.StatusCode -eq 200 -and $null -ne $gaps -and $null -ne $gaps.PSObject.Properties['gaps'])) `
            -Requirement 'Monitoring gaps are first-class records, readable through the API' `
            -Observation ("gap rows returned={0}" -f $(if ($gaps -and $gaps.gaps) { @($gaps.gaps).Count } else { 0 }))
    } else {
        Add-ValidationResult -Id 'W-MON-HEALTH' -Area 'monitoring' -Status 'FAIL' `
            -Requirement 'Native file-integrity source reports healthy after the watches start' `
            -Observation 'monitoring/status did not parse'
    }

    try {
        $log = Get-WinEvent -ListLog 'Microsoft-Windows-Windows Defender/Operational' -ErrorAction Stop
        $recent = @(Get-WinEvent -FilterHashtable @{ LogName = $log.LogName; StartTime = $script:Core.Started; Id = @(1006,1116,1117,1118,1119,5001,5007) } -ErrorAction SilentlyContinue)
        Write-Host ('Native Defender channel: enabled={0} records={1} allowlisted records since core start={2}' -f $log.IsEnabled, $log.RecordCount, $recent.Count)
    } catch { Write-Host ('Native Defender channel read unavailable: ' + $_.Exception.Message) }

    # Event identity for Event Log records (wevt:<len>:<channel>:<record>:<time>).
    $wevtPage = Get-EventRecords -Source 'native.windows_defender_events' -MaxRows $script:EventRowCap
    $wevtRows = @($wevtPage.Rows | Where-Object { $_.event_id -like 'wevt:*' })
    if ($wevtRows.Count -eq 0) {
        $wevtRows = @((Get-EventRecords -MaxRows $script:EventRowCap).Rows | Where-Object { $_.event_id -like 'wevt:*' })
    }
    if ($wevtRows.Count -gt 0) {
        $shapeOk = @($wevtRows | Where-Object { $_.event_id -match '^wevt:\d+:.+:.+:\S+$' }).Count -eq $wevtRows.Count
        $unique = @($wevtRows | ForEach-Object { $_.event_id } | Sort-Object -Unique).Count
        Add-ValidationResult -Id 'W-MON-IDENT' -Area 'monitoring' -Status (Get-Status ($shapeOk -and $unique -eq $wevtRows.Count)) `
            -Requirement 'Event Log records are stored with the channel-scoped identity wevt:<len>:<channel>:<record>:<system time>' `
            -Observation ("{0} events, {1} distinct identities, sample={2}" -f $wevtRows.Count, $unique, $wevtRows[0].event_id)
        Add-ValidationResult -Id 'W-MON-BOOKMARK' -Area 'monitoring' -Status 'BLOCKED' `
            -Requirement 'Event Log bookmark (cursor) behaviour is observable' `
            -Observation 'Bookmark replay requires a naturally occurring eligible Defender event and a persisted checkpoint in the isolated SQLite database; no event is manufactured.'
    } else {
        Add-ValidationResult -Id 'W-MON-IDENT' -Area 'monitoring' -Status 'NOT RUN' `
            -Requirement 'Event Log records are stored with the channel-scoped identity wevt:<len>:<channel>:<record>:<system time>' `
            -Observation 'No wevt: events are stored yet. The Defender channel produced no records during this run.'
        Add-ValidationResult -Id 'W-MON-BOOKMARK' -Area 'monitoring' -Status 'BLOCKED' `
            -Requirement 'Event Log bookmark (cursor) behaviour is observable' `
            -Observation 'No eligible Defender event or bookmark was produced during this run, so native checkpoint replay could not be exercised.'
    }

    $go = Get-Command go -ErrorAction SilentlyContinue
    if ($go -and (Test-Path -LiteralPath (Join-Path $RepoRoot 'internal/sources/filewatch/permission_windows_test.go'))) {
        $nativeOutput = @()
        Push-Location $RepoRoot
        try {
            $nativeOutput = @(& $go.Source test ./internal/sources/filewatch -run '^TestWindowsDeniedRootReturnsPermissionDenied$' -count=1 -v 2>&1)
            $nativeExit = $LASTEXITCODE
        } finally { Pop-Location }
        Add-ValidationResult -Id 'W-MON-PERM' -Area 'monitoring' -Status (Get-Status ($nativeExit -eq 0 -and ($nativeOutput -join ' ') -match 'PASS: TestWindowsDeniedRootReturnsPermissionDenied')) `
            -Requirement 'A native watch opened on a denied isolated root returns permission_denied before claiming healthy' `
            -Observation ('native Go test exit={0}; {1}' -f $nativeExit, ($nativeOutput -join ' '))
    } else {
        Add-ValidationResult -Id 'W-MON-PERM' -Area 'monitoring' -Status 'BLOCKED' `
            -Requirement 'A permission error on a monitored root is reported, not swallowed' `
            -Observation 'Native source-level test requires the source checkout and Go toolchain. No deny ACE is applied to an already-open watch.'
    }

    # Channel clear = CE-6 Windows validation plan item 28.
    Add-ValidationResult -Id 'W-MON-ITEM28' -Area 'monitoring' -Status 'BLOCKED' `
        -Requirement 'CE-6 Windows validation plan item 28: channel-clear behaviour and post-clear Event Log cursor' `
        -Observation 'Clearing an Event Log is destructive and is refused by this harness (the private engineering record). Safe alternatives both require privileges this run does not have: (a) create a disposable custom channel with wevtutil sl /sf on an elevated prompt, generate events, clear only that channel, then re-run this stage; or (b) accept a one-off approval to clear a non-production channel such as Microsoft-Windows-Windows Defender/Operational on a disposable VM. Without one of those, item 28 stays BLOCKED. The identity half of item 28 is covered by W-MON-IDENT and by internal/sources/windowseventlog/identity_test.go.'
}

# ===========================================================================
# Stage: restart persistence for monitoring (part of section 3)
# ===========================================================================
function Invoke-StageMonitoringRestart {
    if (-not $script:Session) { return }
    $beforePage = Get-EventRecords -MaxRows $script:EventRowCap
    $beforeIds = @($beforePage.Rows | ForEach-Object { $_.event_id })

    # New file activity after a restart must be recorded without id collisions.
    Stop-Shield
    Start-Shield -RedirectRoots:(-not $AllowRealMonitoredRoots) | Out-Null
    Connect-ShieldSession | Out-Null

    $afterPage = Get-EventRecords -MaxRows $script:EventRowCap
    $afterCount = @($afterPage.Rows).Count
    $afterIds = @($afterPage.Rows | ForEach-Object { $_.event_id })
    $persisted = (-not $beforePage.Failed -and -not $afterPage.Failed -and @($beforeIds | Where-Object { $afterIds -notcontains $_ }).Count -eq 0)
    Add-ValidationResult -Id 'W-MON-RESTART-KEEP' -Area 'monitoring' -Status (Get-Status $persisted) `
        -Requirement 'Monitoring events persist across a process restart' `
        -Observation ("before={0} after={1} (event ids retained: {2})" -f $beforeIds.Count, $afterCount, (@($beforeIds | Where-Object { $afterIds -contains $_ }).Count))

    $probe = Test-MonitoredFileOperations -Label 'post-restart' -Root $script:Paths.MonitoredStartup
    $fresh = @((Get-EventRecords -MaxRows $script:EventRowCap).Rows | ForEach-Object { $_.event_id } | Where-Object { $beforeIds -notcontains $_ })
    $collisions = $beforeIds.Count - $afterCount
    $ok = ($probe.Ok -and $fresh.Count -gt 0 -and $collisions -le 0)
    Add-ValidationResult -Id 'W-MON-RESTART-NEW' -Area 'monitoring' -Status (Get-Status $ok) `
        -Requirement 'Newly generated events after a restart are stored without ID collisions (CE-6 defect D5)' `
        -Observation ("{0}; distinct new event ids={1}; rows lost across restart={2}" -f $probe.Detail, $fresh.Count, $collisions)
}

# ===========================================================================
# Stage: posture (CE-7A section 4)
# ===========================================================================
function Invoke-StagePosture {
    if (-not $script:Session) {
        Add-ValidationResult -Id 'W-POS-01' -Area 'posture' -Status 'NOT RUN' -Requirement 'Posture assessment' `
            -Observation 'No authenticated session.'
        return
    }
    $script:PostureNative = Get-NativeWindowsPosture
    $scan = Invoke-Api -Path '/api/v1/assessment/scan' -Method POST -WithCsrf -Body '{}'
    $accepted = ($scan.StatusCode -eq 200)
    Add-ValidationResult -Id 'W-POS-01' -Area 'posture' -Status (Get-Status $accepted) `
        -Requirement 'A manual assessment run is accepted' `
        -Observation ("POST /api/v1/assessment/scan -> {0} {1}" -f $scan.StatusCode, $scan.Body)

    $done = Wait-For -TimeoutSeconds 180 -Condition {
        $s = Get-Json (Invoke-Api -Path '/api/v1/assessment/status')
        if (-not $s) { return $false }
        return ($s.PSObject.Properties['running'] -and -not $s.running -and $s.PSObject.Properties['summary'] -and $s.summary.PSObject.Properties['last_run'] -and $s.summary.last_run -and $s.summary.last_run.status -eq 'completed')
    }
    $status = Get-Json (Invoke-Api -Path '/api/v1/assessment/status')
    if (-not $done -or -not $status) {
        Add-ValidationResult -Id 'W-POS-02' -Area 'posture' -Status 'FAIL' `
            -Requirement 'The assessment completes and reports a run' `
            -Observation 'status never reported a completed run within 180 s'
        return
    }
    Add-ValidationResult -Id 'W-POS-02' -Area 'posture' -Status 'PASS' `
        -Requirement 'The assessment completes and reports a run' `
        -Observation (($status | ConvertTo-Json -Compress -Depth 6).Substring(0, [math]::Min(400, ($status | ConvertTo-Json -Compress).Length)))

    # assessment/checks takes no query parameters at all: the gateway answers 400
    # to any query string (internal/gateway/assessment.go), so none is sent.
    $checks = Get-Json (Invoke-Api -Path '/api/v1/assessment/checks')
    $results = @()
    if ($checks -and $checks.PSObject.Properties['results']) { $results = @($checks.results) } elseif ($checks -and $checks.PSObject.Properties['checks']) { $results = @($checks.checks) }
    $allowed = @('pass', 'fail', 'unknown', 'not_supported', 'stale')
    $illegal = @($results | Where-Object { $allowed -notcontains ([string]$_.result).ToLower() })
    Add-ValidationResult -Id 'W-POS-03' -Area 'posture' -Status (Get-Status ($results.Count -gt 0 -and $illegal.Count -eq 0)) `
        -Requirement 'Every check reports only a known result value' `
        -Observation ("{0} results; illegal values={1}" -f $results.Count, $illegal.Count)

    # The four ssh checks must not report a pass on Windows.
    $ssh = @($results | Where-Object { $_.check_id -like 'ssh_*' })
    $sshBad = @($ssh | Where-Object { ([string]$_.result).ToLower() -eq 'pass' })
    Add-ValidationResult -Id 'W-POS-04' -Area 'posture' -Status (Get-Status ($sshBad.Count -eq 0)) `
        -Requirement 'Linux-scoped ssh_* checks never report PASS on a Windows host (plan item 22)' `
        -Observation ("ssh checks={0}: {1}" -f $ssh.Count, (($ssh | ForEach-Object { '{0}={1}' -f $_.check_id, $_.result }) -join ', '))

    # Cross-check a few Windows checks against authoritative interfaces.
    $byId = @{}
    foreach ($r in $results) { $byId[[string]$r.check_id] = $r }

    if ($byId.ContainsKey('defender_protection_enabled')) {
        $native = $script:PostureNative.defender
        $detail = ''
        $ok = $false
        if ($native -and $native.Error) {
            $ok = (([string]$byId['defender_protection_enabled'].result).ToLower() -eq 'unknown')
            $detail = "Get-MpComputerStatus unavailable ($($native.Error)); Shield reported $($byId['defender_protection_enabled'].result)"
        } else {
            $expected = ($native.AntivirusEnabled -eq $true -and $native.RealTimeProtectionEnabled -eq $true)
            $actual = (([string]$byId['defender_protection_enabled'].result).ToLower() -eq 'pass')
            $ok = if ($native.AMRunningMode -ne 'Normal') { (([string]$byId['defender_protection_enabled'].result).ToLower() -eq 'unknown') } else { $expected -eq $actual }
            $detail = "native RealTimeProtectionEnabled=$($native.RealTimeProtectionEnabled) AMRunningMode=$($native.AMRunningMode); Shield=$($byId['defender_protection_enabled'].result)"
        }
        Add-ValidationResult -Id 'W-POS-DEF' -Area 'posture' -Status (Get-Status $ok) `
            -Requirement 'defender_protection_enabled agrees with Get-MpComputerStatus' -Observation $detail
    }

    if ($byId.ContainsKey('windows_firewall_enabled')) {
        $profiles = @()
        $nativeErr = $null
        if ($script:PostureNative.PSObject.Properties['firewallProfiles']) { $profiles = @($script:PostureNative.firewallProfiles) }
        if ($script:PostureNative.PSObject.Properties['firewallError']) { $nativeErr = $script:PostureNative.firewallError }
        $disabled = @($profiles | Where-Object { $_.Enabled -eq $false })
        $result = ([string]$byId['windows_firewall_enabled'].result).ToLower()
        $ok = $true
        $detail = ''
        if ($nativeErr) {
            $ok = ($result -eq 'unknown')
            $detail = "Get-NetFirewallProfile unavailable ($nativeErr); Shield=$result"
        } elseif ($disabled.Count -eq 0 -and $profiles.Count -gt 0) {
            $ok = ($result -eq 'pass')
            $detail = "native enabled profiles=$($profiles.Count), disabled=$($disabled.Count); Shield=$result"
        } elseif ($disabled.Count -gt 0) {
            $ok = ($result -eq 'fail')
            $detail = "native disabled profiles=$($disabled.Count); Shield=$result"
        }
        Add-ValidationResult -Id 'W-POS-FW' -Area 'posture' -Status (Get-Status $ok) `
            -Requirement 'windows_firewall_enabled agrees with Get-NetFirewallProfile' -Observation $detail
    }

    if ($byId.ContainsKey('bitlocker_protection_enabled')) {
        $vols = @()
        $blErr = $null
        if ($script:PostureNative.PSObject.Properties['bitlocker']) { $vols = @($script:PostureNative.bitlocker) }
        if ($script:PostureNative.PSObject.Properties['bitlockerError']) { $blErr = $script:PostureNative.bitlockerError }
        $result = ([string]$byId['bitlocker_protection_enabled'].result).ToLower()
        $osVol = $vols # The product evaluates every reported volume, rather than only C:.
        $ok = $true
        $detail = ''
        if ($blErr -or $vols.Count -eq 0) {
            $ok = ($result -ne 'pass' -and $result -ne 'fail')
            $detail = "native source unavailable ($blErr); Shield=$result (must be unknown/not_supported, never a fabricated verdict)"
        } else {
            $protected = @($osVol | Where-Object { [string]$_.ProtectionStatus -eq 'On' -or [string]$_.ProtectionStatus -eq '1' })
            if ($osVol.Count -eq 0) {
                $ok = ($result -ne 'pass')
                $detail = "no OS volume reported by Get-BitLockerVolume; Shield=$result"
            } else {
                $ok = (($protected.Count -gt 0) -eq ($result -eq 'pass'))
                $detail = "native C: ProtectionStatus=$($osVol[0].ProtectionStatus) VolumeStatus=$($osVol[0].VolumeStatus); Shield=$result"
            }
        }
        Add-ValidationResult -Id 'W-POS-BL' -Area 'posture' -Status (Get-Status $ok) `
            -Requirement 'bitlocker_protection_enabled agrees with Get-BitLockerVolume, and an unreadable volume never fabricates a verdict' `
            -Observation $detail
    }

    # Every result must carry evidence or an explanation; no bare verdicts.
    $bare = @($results | Where-Object {
        $d = [string](Get-ObjectProperty -InputObject $_ -Name 'detail')
        $e = Get-ObjectProperty -InputObject $_ -Name 'evidence'
        $r = ([string]$_.result).ToLower()
        (($r -eq 'pass' -or $r -eq 'fail') -and -not $d -and -not $e)
    })
    Add-ValidationResult -Id 'W-POS-05' -Area 'posture' -Status (Get-Status ($bare.Count -eq 0)) `
        -Requirement 'No PASS or FAIL is reported without a detail or evidence string' `
        -Observation ("verdicts without explanation={0}" -f $bare.Count)

    $unknowns = @($results | Where-Object { ([string]$_.result).ToLower() -eq 'unknown' })
    $unknownExplained = @($unknowns | Where-Object { (Get-ObjectProperty -InputObject $_ -Name 'detail') -or (Get-ObjectProperty -InputObject $_ -Name 'evidence') })
    Add-ValidationResult -Id 'W-POS-06' -Area 'posture' -Status (Get-Status ($unknowns.Count -eq $unknownExplained.Count)) `
        -Requirement 'Every UNKNOWN carries the collector reason' `
        -Observation ("unknown={0} with reason={1}" -f $unknowns.Count, $unknownExplained.Count)
}

# ===========================================================================
# Stage: inventory (CE-7A section 5)
# ===========================================================================
function Invoke-StageInventory {
    if (-not $script:Session) { return }
    $script:Native = Get-NativeWindowsInventory
    $ready = Wait-For -TimeoutSeconds 120 -Condition {
        $current = Get-Json (Invoke-Api -Path '/api/v1/inventory/status')
        if (-not $current -or $current.refreshing) { return $false }
        if ($current.PSObject.Properties['last_refresh'] -and $current.last_refresh) {
            return ((Get-Date) - [datetime]$current.last_refresh).TotalSeconds -ge 11
        }
        return $true
    }
    if (-not $ready) { throw 'Inventory prerequisite did not settle outside the documented refresh rate limit.' }
    $ref = Invoke-Api -Path '/api/v1/inventory/refresh' -Method POST -WithCsrf -Body '{}'
    $accepted = ($ref.StatusCode -eq 200)
    Add-ValidationResult -Id 'W-INV-01' -Area 'inventory' -Status (Get-Status $accepted) `
        -Requirement 'Inventory refresh is accepted and completes' `
        -Observation ("POST /api/v1/inventory/refresh -> {0} {1}" -f $ref.StatusCode, $ref.Body)
    $settled = Wait-For -TimeoutSeconds 120 -Condition {
        $s = Get-Json (Invoke-Api -Path '/api/v1/inventory/status')
        return ($s -and -not $s.refreshing -and $s.PSObject.Properties['software_count'] -and $s.software_count -gt 0)
    }
    $st = Get-Json (Invoke-Api -Path '/api/v1/inventory/status')
    if (-not $st) {
        Add-ValidationResult -Id 'W-INV-02' -Area 'inventory' -Status 'FAIL' -Requirement 'Inventory coverage is reported' `
            -Observation 'inventory/status did not parse'
        return
    }
    Add-ValidationResult -Id 'W-INV-02' -Area 'inventory' -Status (Get-Status $settled) `
        -Requirement 'Inventory coverage is reported with per-kind totals' `
        -Observation ("software={0} endpoint={1} service={2} stored_bytes={3} of {4} settled={5}" -f `
            $st.software_count, $st.endpoint_count, $st.service_count, $st.stored_bytes, $st.max_bytes, $settled)

    $sw = Get-InventoryRows -Kind software -MaxRows $script:InventoryRowCap
    $swItems = @($sw.Rows)
    $software = $sw
    $nativeNames = @($script:Native.Apps | ForEach-Object { $_.Name })
    $shieldNames = @($swItems | ForEach-Object { $_.name })
    $overlap = @($shieldNames | Where-Object { $nativeNames -contains $_ })
    $countOk = (-not $sw.Failed -and $st.software_count -gt 0 -and $st.software_count -lt 100000)
    Add-ValidationResult -Id 'W-INV-REG' -Area 'inventory' -Status (Get-Status $countOk) `
        -Requirement 'Registry uninstall enumeration produces a plausible count (plan item 7)' `
        -Observation ("shield={0} native(Get-ItemProperty Uninstall*)={1}" -f $st.software_count, $nativeNames.Count)
    Add-ValidationResult -Id 'W-INV-XCHK' -Area 'inventory' -Status (Get-Status (-not $sw.Failed -and $overlap.Count -gt 0)) `
        -Requirement 'Shield software rows overlap the authoritative registry enumeration' `
        -Observation ("{0}/{1} Shield software names also present in the native enumeration (total reported by the API: {2}, truncated={3})" -f $overlap.Count, $shieldNames.Count, $sw.Total, $sw.Truncated)

    Add-ValidationResult -Id 'W-INV-PAGINATION' -Area 'inventory' -Status $(if ($sw.Truncated) { 'BLOCKED' } else { Get-Status (-not $sw.Failed -and $swItems.Count -eq $sw.Total) }) `
        -Requirement 'A complete native software cursor walk returns every stored version and source, including duplicate names' `
        -Observation ('rows={0} total={1} pages={2} failed={3} clipped={4}' -f $swItems.Count, $sw.Total, $sw.Pages, $sw.Failed, $sw.Truncated)

    # 32-bit and 64-bit registry views both represented.
    $wow = @($nativeNames | Where-Object { $true }) # names only; views are compared by count below
    $wowNative = @()
    try { $wowNative = @(Get-ItemProperty -Path 'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*' -ErrorAction Stop | Where-Object { $_.PSObject.Properties['DisplayName'] -and $_.DisplayName }) } catch { }
    $sources = @()
    if ($sw.Sources) { $sources = @($sw.Sources) }
    $wowCovered = ($wowNative.Count -gt 0) -and (@($wowNative | Where-Object { $shieldNames -notcontains $_.DisplayName }).Count -eq 0 -and -not $sw.Failed -and -not $sw.Truncated)
    Add-ValidationResult -Id 'W-INV-VIEWS' -Area 'inventory' -Status $(if ($wowNative.Count -eq 0) { 'NOT RUN' } else { Get-Status $wowCovered }) `
        -Requirement 'Both 64-bit and 32-bit (WOW6432Node) uninstall views are enumerated' `
        -Observation ("wow6432node entries native={0}; shield total={1}; sources={2}" -f $wowNative.Count, $st.software_count, ($sources -join ','))

    # Unicode application names survive the round trip.
    $unicodeNative = @($nativeNames | Where-Object { $_ -match '[^\x00-\x7F]' })
    $unicodeOk = $true
    $unicodeDetail = 'no non-ASCII application name found on this machine to compare'
    if ($unicodeNative.Count -gt 0) {
        $found = $false
        foreach ($n in $unicodeNative) {
            $q = [uri]::EscapeDataString($n)
            $r = Get-Json (Invoke-Api -Path ("/api/v1/inventory/software?limit=10&search={0}" -f $q))
            if ($r -and $r.PSObject.Properties['items']) {
                foreach ($it in @($r.items)) { if ($it.name -eq $n) { $found = $true } }
            }
        }
        $unicodeOk = $found
        $unicodeDetail = "non-ASCII names native={0}; found intact in Shield={1}" -f $unicodeNative.Count, $found
    }
    Add-ValidationResult -Id 'W-INV-UNI' -Area 'inventory' -Status $(if ($unicodeNative.Count -eq 0) { 'NOT RUN' } elseif ($unicodeOk) { 'PASS' } else { 'FAIL' }) `
        -Requirement 'Unicode application names round-trip without mangling' -Observation $unicodeDetail

    # Services: normalisation versus Get-Service.
    $servicesPage = Get-InventoryRows -Kind services -MaxRows $script:InventoryRowCap
    $svcItems = @($servicesPage.Rows)
    $lowerOk = @($svcItems | Where-Object {
        $status = [string](Get-ObjectProperty -InputObject $_.fields -Name 'status')
        $start = [string](Get-ObjectProperty -InputObject $_.fields -Name 'start_type')
        -not $status -or -not $start -or $status -cne $status.ToLower() -or $start -cne $start.ToLower()
    }).Count -eq 0
    Add-ValidationResult -Id 'W-INV-SVC' -Area 'inventory' -Status (Get-Status (-not $servicesPage.Failed -and $svcItems.Count -gt 0 -and $lowerOk)) `
        -Requirement 'Service status and start type are lowercased (plan item 13)' `
        -Observation ("sampled={0} services; any upper-cased value={1}" -f $svcItems.Count, (-not $lowerOk))

    # Endpoints: TCP listen vs UDP binding.
    $endpointsPage = Get-InventoryRows -Kind endpoints -MaxRows $script:EndpointRowCap
    $epItems = @($endpointsPage.Rows)
    # Both checks below claim that a class of row is absent. An absence cannot be
    # certified from a clipped read, so a read that stopped at the row cap is
    # recorded as BLOCKED rather than PASS. The API reports its own total, which
    # makes the shortfall visible.
    $epClip = if ($endpointsPage.Truncated) { 'truncated' } elseif ($endpointsPage.Failed) { 'read failed' } else { 'complete' }
    $epScope = "rows read={0} of {1} in {2} page(s) [{3}]" -f $epItems.Count, $endpointsPage.Total, $endpointsPage.Pages, $epClip
    $udpRows = @($epItems | Where-Object { ([string]$_.fields.transport) -match '^udp' })
    $udpListening = @($udpRows | Where-Object { ([string]$_.fields.state) -eq 'listen' })
    $udpStatus = if ($udpListening.Count -gt 0) { 'FAIL' } elseif ($endpointsPage.Truncated -or $endpointsPage.Failed) { 'BLOCKED' } else { 'PASS' }
    Add-ValidationResult -Id 'W-INV-UDP' -Area 'inventory' -Status $udpStatus `
        -Requirement 'A UDP endpoint is never labelled with TCP-style listen state (finding L-4)' `
        -Observation ("udp rows={0} with state=listen={1}; {2}" -f $udpRows.Count, $udpListening.Count, $epScope)

    $nativeTcp = @()
    if ($script:Native.TCPListen) { $nativeTcp = @($script:Native.TCPListen) }
    $shieldTcpPorts = @($epItems | Where-Object { ([string]$_.fields.transport) -eq 'tcp' } | ForEach-Object { [string]$_.fields.port } | Sort-Object -Unique)
    $nativeTcpPorts = @($nativeTcp | ForEach-Object { [string]$_.LocalPort } | Sort-Object -Unique)
    $missing = @($nativeTcpPorts | Where-Object { $shieldTcpPorts -notcontains $_ })
    Add-ValidationResult -Id 'W-INV-TCP' -Area 'inventory' -Status (Get-Status (-not $endpointsPage.Failed -and -not $endpointsPage.Truncated -and $shieldTcpPorts.Count -gt 0 -and $missing.Count -eq 0)) `
        -Requirement 'Listening TCP endpoints match Get-NetTCPConnection -State Listen (plan item 10)' `
        -Observation ("shield ports={0} native ports={1} native ports not in the Shield read={2}; {3}" -f $shieldTcpPorts.Count, $nativeTcpPorts.Count, $missing.Count, $epScope)

    $unattributed = @($epItems | Where-Object { ([string]$_.fields.attribution) -eq 'unknown' })
    $attributedNoPid = @($epItems | Where-Object { ([string]$_.fields.attribution) -eq 'unknown' -and $_.fields.PSObject.Properties['process_id'] })
    $attrStatus = if ($attributedNoPid.Count -gt 0) { 'FAIL' } elseif ($endpointsPage.Truncated -or $endpointsPage.Failed) { 'BLOCKED' } else { 'PASS' }
    Add-ValidationResult -Id 'W-INV-ATTR' -Area 'inventory' -Status $attrStatus `
        -Requirement 'An endpoint without a readable owner is unknown with a reason, and no process id is invented (plan items 11-12)' `
        -Observation ("unknown-attribution rows={0}; rows with an invented process id={1}; {2}" -f $unattributed.Count, $attributedNoPid.Count, $epScope)

    # Missing / unreadable registry keys and empty / single-result responses are
    # not forceable on a healthy machine without altering the registry.
    Add-ValidationResult -Id 'W-INV-MISSING' -Area 'inventory' -Status 'SIMULATED' `
        -Requirement 'Missing or unreadable registry keys are reported as a refusal, never an empty inventory (finding L-3)' `
        -Observation 'Requires a hive that exists but denies enumeration. Not forceable without changing registry ACLs, which this harness forbids. Fixture evidence: TestRegistryEnumerationFailureIsNeverAnEmptyInventory and TestRegistryUnreadableNoteIsBounded (included in the native Go suite).'
    Add-ValidationResult -Id 'W-INV-EMPTY' -Area 'inventory' -Status 'SIMULATED' `
        -Requirement 'Empty and single-result registry responses are handled (ConvertTo-Json collapse)' `
        -Observation 'A healthy machine cannot be made to return exactly one application without editing the registry. Fixture evidence: TestParseWindowsSocketsAcceptsArrayAndSingleObject, TestParseWindowsServicesAcceptsArrayAndSingleObject, TestWindowsInventoryMalformedOutputIsRejected.'
    Add-ValidationResult -Id 'W-INV-TRUNC' -Area 'inventory' -Status 'SIMULATED' `
        -Requirement 'Large inventory is truncated and disclosed, not silently cut (finding H-1, Windows side)' `
        -Observation 'Producing a listing larger than the 8 MiB inventory capture cap needs thousands of installed applications. Fixture evidence: TestWindowsSocketInventorySurfacesAClippedDocument, TestWindowsServiceInventorySurfacesAClippedDocument, TestWindowsInventoryInsideTheCapIsNotMarked (Windows-only fixture tests executed in the native Go suite).'
    if ($script:Native.TCPError) {
        Add-ValidationResult -Id 'W-INV-NATIVEERR' -Area 'inventory' -Status 'NOT RUN' `
            -Requirement 'Native socket comparison available' -Observation $script:Native.TCPError
    }
}

# ===========================================================================
# Stage: settings (plan items 17-20)
# ===========================================================================
function Invoke-StageSettings {
    if (-not $script:Session) { return }
    $set = Invoke-Api -Path '/api/v1/settings' -Method POST -WithCsrf -Body '{"theme":"light"}'
    $get1 = Get-Json (Invoke-Api -Path '/api/v1/settings')
    $themePersisted = ($set.StatusCode -eq 200 -and $get1 -and $get1.effective.theme -eq 'light')
    Add-ValidationResult -Id 'W-SET-THEME' -Area 'settings' -Status (Get-Status $themePersisted) `
        -Requirement 'Theme preference is stored server-side and read back (plan item 17)' `
        -Observation ("post={0} theme={1}" -f $set.StatusCode, $(if ($get1) { $get1.effective.theme } else { '<none>' }))

    $bad = Invoke-Api -Path '/api/v1/settings' -Method POST -WithCsrf -Body '{"theme":"neon"}'
    Add-ValidationResult -Id 'W-SET-BADTHEME' -Area 'settings' -Status (Get-Status ($bad.StatusCode -eq 400)) `
        -Requirement 'An unsupported settings value is refused with a readable message' `
        -Observation ("theme=neon -> {0} {1}" -f $bad.StatusCode, $bad.Body)
    $unknown = Invoke-Api -Path '/api/v1/settings' -Method POST -WithCsrf -Body '{"wallpaper":"dark"}'
    Add-ValidationResult -Id 'W-SET-UNKNOWNKEY' -Area 'settings' -Status (Get-Status ($unknown.StatusCode -eq 400)) `
        -Requirement 'An unsupported settings key is refused' `
        -Observation ("wallpaper -> {0} {1}" -f $unknown.StatusCode, $unknown.Body)

    $sched = Invoke-Api -Path '/api/v1/settings' -Method POST -WithCsrf -Body '{"assess_enabled":true,"assess_interval":"24h"}'
    $schedGet = Get-Json (Invoke-Api -Path '/api/v1/settings')
    $hasNext = ($schedGet -and $schedGet.PSObject.Properties['next_scan_at'] -and $schedGet.next_scan_at)
    Add-ValidationResult -Id 'W-SET-SCHED' -Area 'settings' -Status (Get-Status ($sched.StatusCode -eq 200 -and $hasNext)) `
        -Requirement 'Enabling a daily schedule publishes a real next-scan time (plan item 18)' `
        -Observation ("post={0} next_scan_at={1}" -f $sched.StatusCode, $(if ($hasNext) { $schedGet.next_scan_at } else { '<absent>' }))

    $status = Get-Json (Invoke-Api -Path '/api/v1/assessment/status')
    $zeroTime = $false
    if ($status -and $status.PSObject.Properties['scheduler']) {
        $zeroTime = ("$($status.scheduler.next_scan_at)" -like '0001-01-01*')
    }
    Add-ValidationResult -Id 'W-SET-NOZERO' -Area 'settings' -Status (Get-Status (-not $zeroTime)) `
        -Requirement 'No fabricated year-0001 next-scan timestamp is published (finding M-3)' `
        -Observation ("scheduler.next_scan_at={0}" -f $(if ($status -and $status.PSObject.Properties['scheduler']) { $status.scheduler.next_scan_at } else { '<absent>' }))

    # Retention (plan item 19).
    $ret = Invoke-Api -Path '/api/v1/settings' -Method POST -WithCsrf -Body '{"assess_max_runs":10}'
    $runs = Get-Json (Invoke-Api -Path '/api/v1/assessment/runs?limit=50')
    $runCount = if ($runs -and $runs.runs) { @($runs.runs).Count } else { 0 }
    Add-ValidationResult -Id 'W-SET-RETENTION' -Area 'settings' -Status (Get-Status ($ret.StatusCode -eq 200 -and $runCount -le 10)) `
        -Requirement 'Retention limit applies and stored runs never exceed it (plan item 19)' `
        -Observation ("post={0} stored runs={1} limit=10" -f $ret.StatusCode, $runCount)
}

# ===========================================================================
# Stage: lifecycle - CE-7A section 6, ten steps, separate OS processes
# ===========================================================================
function Invoke-StageLifecycle {
    if (-not $script:Binaries.ContainsKey('core')) {
        Add-ValidationResult -Id 'W-LIF-01' -Area 'lifecycle' -Status 'NOT RUN' -Requirement 'Process-level restart lifecycle' `
            -Observation 'No binaries resolved.'
        return
    }
    $redirect = (-not $AllowRealMonitoredRoots)

    if ($script:Session) { Invoke-Api -Path '/api/v1/settings' -Method POST -WithCsrf -Body '{"theme":"light"}' | Out-Null }

    # 1. Start core and gateway (fresh processes).
    Stop-Shield
    Start-Shield -RedirectRoots:$redirect | Out-Null
    Add-ValidationResult -Id 'W-LIF-01' -Area 'lifecycle' -Status 'PASS' `
        -Requirement 'Step 1: separate OS processes start for core and gateway' `
        -Observation ("core pid={0} gateway pid={1} origin={2}" -f $script:Core.Process.Id, $script:Gateway.Process.Id, $script:Origin)

    # 2. Authenticate and complete an assessment.
    Connect-ShieldSession | Out-Null
    $scan = Invoke-Api -Path '/api/v1/assessment/scan' -Method POST -WithCsrf -Body '{}'
    $completed = Wait-For -TimeoutSeconds 180 -Condition {
        $s = Get-Json (Invoke-Api -Path '/api/v1/assessment/status')
        return ($s -and $s.PSObject.Properties['summary'] -and $s.summary.PSObject.Properties['last_run'] -and $s.summary.last_run -and $s.summary.last_run.status -eq 'completed' -and -not $s.running)
    }
    Add-ValidationResult -Id 'W-LIF-02' -Area 'lifecycle' -Status (Get-Status ($scan.StatusCode -eq 200 -and $completed)) `
        -Requirement 'Step 2: authenticated assessment completes' `
        -Observation ("scan={0} completed={1}" -f $scan.StatusCode, $completed)

    # 3. Save a valid assessment interval.
    $save = Invoke-Api -Path '/api/v1/settings' -Method POST -WithCsrf -Body '{"assess_enabled":true,"assess_interval":"24h"}'
    $afterSave = Get-Json (Invoke-Api -Path '/api/v1/settings')
    $intervalSaved = ($save.StatusCode -eq 200 -and $afterSave -and $afterSave.effective.assess_interval -eq '24h0m0s')
    Add-ValidationResult -Id 'W-LIF-03' -Area 'lifecycle' -Status (Get-Status $intervalSaved) `
        -Requirement 'Step 3: a valid assessment interval is saved' `
        -Observation ("post={0} effective interval={1}" -f $save.StatusCode, $(if ($afterSave) { $afterSave.effective.assess_interval } else { '<none>' }))

    # 4. Disable scheduling.
    $off = Invoke-Api -Path '/api/v1/settings' -Method POST -WithCsrf -Body '{"assess_enabled":false}'
    $afterOff = Get-Json (Invoke-Api -Path '/api/v1/settings')
    $disabled = ($off.StatusCode -eq 200 -and $afterOff -and $afterOff.effective.assess_enabled -eq $false)
    Add-ValidationResult -Id 'W-LIF-04' -Area 'lifecycle' -Status (Get-Status $disabled) `
        -Requirement 'Step 4: scheduling is disabled while the interval stays stored' `
        -Observation ("post={0} enabled={1} stored interval still={2}" -f $off.StatusCode, $(if ($afterOff) { $afterOff.effective.assess_enabled } else { '?' }), $(if ($afterOff) { $afterOff.effective.assess_interval } else { '?' }))

    # 5. Stop both processes.
    $corePid = $script:Core.Process.Id
    $gwPid = $script:Gateway.Process.Id
    Stop-Shield
    Start-Sleep -Seconds 2
    $coreGone = -not (Get-Process -Id $corePid -ErrorAction SilentlyContinue)
    $gwGone = -not (Get-Process -Id $gwPid -ErrorAction SilentlyContinue)
    Add-ValidationResult -Id 'W-LIF-05' -Area 'lifecycle' -Status (Get-Status ($coreGone -and $gwGone)) `
        -Requirement 'Step 5: both processes stop' `
        -Observation ("core still running={0}; gateway still running={1} (automated stop terminates; a graceful Ctrl+C stop is a manual sub-step)" -f (-not $coreGone), (-not $gwGone))

    # 6. Restart both against the same state.
    try {
        Start-Shield -RedirectRoots:$redirect | Out-Null
        Add-ValidationResult -Id 'W-LIF-06' -Area 'lifecycle' -Status 'PASS' `
            -Requirement 'Step 6: both processes restart against the same state directory' `
            -Observation ("state={0}" -f $script:Paths.StateDir)
    } catch {
        Add-ValidationResult -Id 'W-LIF-06' -Area 'lifecycle' -Status 'FAIL' `
            -Requirement 'Step 6: both processes restart against the same state directory' `
            -Observation "$($_.Exception.Message)"
        return
    }

    # 7. Re-enable scheduling WITHOUT resubmitting the interval (finding M-1).
    Connect-ShieldSession | Out-Null
    $reenable = Invoke-Api -Path '/api/v1/settings' -Method POST -WithCsrf -Body '{"assess_enabled":true}'
    $reGet = Get-Json (Invoke-Api -Path '/api/v1/settings')
    $reEnabled = ($reenable.StatusCode -eq 200 -and $reGet -and $reGet.effective.assess_enabled -eq $true)
    Add-ValidationResult -Id 'W-LIF-07' -Area 'lifecycle' -Status (Get-Status $reEnabled) `
        -Requirement 'Step 7: re-enabling with only assess_enabled=true is accepted after a process restart (finding M-1)' `
        -Observation ("post={0} body={1} effective interval={2}" -f $reenable.StatusCode, $reenable.Body, $(if ($reGet) { $reGet.effective.assess_interval } else { '<none>' }))

    # 8. Verify correct schedule status.
    $st8 = Get-Json (Invoke-Api -Path '/api/v1/settings')
    $st8s = Get-Json (Invoke-Api -Path '/api/v1/assessment/status')
    $nextOk = $false
    $cfgOk = $false
    if ($st8) { $cfgOk = ($st8.config_source -eq 'settings'); $nextOk = ($st8.PSObject.Properties['next_scan_at'] -and $st8.next_scan_at -and "$($st8.next_scan_at)" -notlike '0001-*') }
    Add-ValidationResult -Id 'W-LIF-08' -Area 'lifecycle' -Status (Get-Status ($reEnabled -and $cfgOk -and $nextOk)) `
        -Requirement 'Step 8: schedule status is correct (enabled, config_source=settings, real next scan, no zero timestamp)' `
        -Observation ("config_source={0} next_scan_at={1}" -f $(if ($st8) { $st8.config_source } else { '<none>' }), $(if ($st8 -and $st8.PSObject.Properties['next_scan_at']) { $st8.next_scan_at } else { '<absent>' }))

    # 9. Generate additional monitored file events.
    $beforeIds = @((Get-EventRecords -MaxRows $script:EventRowCap).Rows | ForEach-Object { $_.event_id })
    $probe = Test-MonitoredFileOperations -Label 'lifecycle' -Root $script:Paths.MonitoredStartup
    Add-ValidationResult -Id 'W-LIF-09' -Area 'lifecycle' -Status (Get-Status $probe.Ok) `
        -Requirement 'Step 9: additional monitored file events are generated after the restart' `
        -Observation $probe.Detail

    # 10. Newly generated events persist without ID collisions.
    $afterIds = @((Get-EventRecords -MaxRows $script:EventRowCap).Rows | ForEach-Object { $_.event_id })
    $fresh = @($afterIds | Where-Object { $beforeIds -notcontains $_ })
    $lost = @($beforeIds | Where-Object { $afterIds -notcontains $_ })
    $ok = ($fresh.Count -gt 0 -and $lost.Count -eq 0)
    Add-ValidationResult -Id 'W-LIF-10' -Area 'lifecycle' -Status (Get-Status $ok) `
        -Requirement 'Step 10: new events persist and no stored event ID collides across the restart (CE-6 defect D5)' `
        -Observation ("before={0} after={1} new={2} lost={3}" -f $beforeIds.Count, $afterIds.Count, $fresh.Count, $lost.Count)

    # Plan item 18: the schedule survives the restart too.
    Add-ValidationResult -Id 'W-LIF-SCHED-PERSIST' -Area 'lifecycle' -Status (Get-Status ($reEnabled -and $nextOk)) `
        -Requirement 'Plan item 18: the enabled schedule and its next scan survive a restart' `
        -Observation ("enabled={0} next_scan_at={1}" -f $reEnabled, $(if ($st8 -and $st8.PSObject.Properties['next_scan_at']) { $st8.next_scan_at } else { '<absent>' }))

    # Plan item 17: theme survives the restart.
    Add-ValidationResult -Id 'W-LIF-THEME-PERSIST' -Area 'lifecycle' -Status (Get-Status ($st8 -and $st8.effective.theme -eq 'light')) `
        -Requirement 'Plan item 17: stored theme is applied after a restart' `
        -Observation ("theme={0}" -f $(if ($st8) { $st8.effective.theme } else { '<none>' }))
}

# ===========================================================================
# Stage: shutdown and reliability (plan items 25-27)
# ===========================================================================
function Invoke-StageShutdown {
    if (-not $script:Binaries.ContainsKey('core')) { return }
    $redirect = (-not $AllowRealMonitoredRoots)

    # Item 27: kill the core mid-scan, restart, the run must not stay "running".
    if (-not $script:Session) { try { Connect-ShieldSession | Out-Null } catch { } }
    $scanAccepted = $false
    $activeRunId = 0
    if ($script:Session) {
        $scan = Invoke-Api -Path '/api/v1/assessment/scan' -Method POST -WithCsrf -Body '{}'
        $scanAccepted = $scan.StatusCode -eq 200
        $active = Get-Json (Invoke-Api -Path '/api/v1/assessment/status')
        if ($active -and $active.running -and $active.summary.PSObject.Properties['running_run_id']) { $activeRunId = $active.summary.running_run_id }
    }
    $killedPid = $script:Core.Process.Id
    Stop-ShieldProcess -Handle $script:Core
    $script:Core = $null
    Start-Sleep -Seconds 1
    try {
        Start-Shield -RedirectRoots:$redirect | Out-Null
        Connect-ShieldSession | Out-Null
        $s = Get-Json (Invoke-Api -Path '/api/v1/assessment/status')
        $running = if ($s -and $s.PSObject.Properties['running']) { [bool]$s.running } else { $false }
        $runs = Get-Json (Invoke-Api -Path '/api/v1/assessment/runs?limit=5')
        $states = @()
        if ($runs -and $runs.runs) { $states = @($runs.runs | ForEach-Object { [string]$_.status }) }
        $interrupted = @($runs.runs | Where-Object { $_.id -eq $activeRunId -and $_.status -eq 'interrupted' }).Count -eq 1
        $ok = ($scanAccepted -and $activeRunId -gt 0 -and $s -and $runs -and -not $running -and ($states -notcontains 'running') -and $interrupted)
        Add-ValidationResult -Id 'W-SDN-01' -Area 'shutdown' -Status (Get-Status $ok) `
            -Requirement 'Plan item 27: after the core is killed mid-scan and restarted, the run is never left as running' `
            -Observation ("killed pid={0}; status.running={1}; recent run states=[{2}]" -f $killedPid, $running, ($states -join ', '))
    } catch {
        Add-ValidationResult -Id 'W-SDN-01' -Area 'shutdown' -Status 'FAIL' `
            -Requirement 'Plan item 27: after the core is killed mid-scan and restarted, the run is never left as running' `
            -Observation "$($_.Exception.Message)"
    }

    # Item 26: data survives.
    if ($script:Session) {
        $inv = Get-Json (Invoke-Api -Path '/api/v1/inventory/status')
        $runs = Get-Json (Invoke-Api -Path '/api/v1/assessment/runs?limit=5')
        $ok = ($inv -and $inv.PSObject.Properties['software_count'] -and $inv.software_count -gt 0 -and $runs -and $runs.PSObject.Properties['runs'])
        Add-ValidationResult -Id 'W-SDN-02' -Area 'shutdown' -Status (Get-Status $ok) `
            -Requirement 'Plan item 26: monitor, assessment and inventory data are still present after a restart' `
            -Observation ("software_count={0} runs={1}" -f $(if ($inv) { $inv.software_count } else { '?' }), $(if ($runs -and $runs.runs) { @($runs.runs).Count } else { 0 }))
    }

    # Item 25: graceful shutdown cleans the runtime directory. Automated stop in
    # this harness is a hard terminate, so a graceful Ctrl+C stop must be done
    # by hand in two consoles.
    Stop-Shield
    Start-Sleep -Seconds 1
    $second = Start-ShieldProcess -Name 'core-after-stop' -ExePath $script:Binaries['core'] -Paths $script:Paths -RedirectMonitoredRoots:$redirect
    Start-Sleep -Seconds 3
    $restarted = -not $second.Process.HasExited
    $log2 = if (Test-Path $second.LogPath) { (Get-Content $second.LogPath -Raw -ErrorAction SilentlyContinue) } else { '' }
    Stop-ShieldProcess -Handle $second
    Add-ValidationResult -Id 'W-SDN-03' -Area 'shutdown' -Status (Get-Status $restarted) `
        -Requirement 'The instance lock does not survive process termination, so a restart is never blocked' `
        -Observation ("restarted cleanly={0}" -f $restarted)
    Add-ValidationResult -Id 'W-SDN-04' -Area 'shutdown' -Status 'BLOCKED' `
        -Requirement 'Plan item 25: a graceful stop exits both processes and cleans the runtime directory' `
        -Observation 'Automation cannot deliver Ctrl+C to a child started with CREATE_NO_WINDOW from PowerShell without a native CREATE_NEW_PROCESS_GROUP helper, and a hard terminate is not a graceful stop. Manual step: run each binary in its own console, press Ctrl+C, confirm both log "core stopped"/"gateway stopped" and that the runtime directory holds no live listener. The automated equivalent covered here is W-SDN-03.'
}

# ===========================================================================
# Stage: resources
# ===========================================================================
function Get-RunResources {
    $rows = @()
    foreach ($handle in @($script:Core, $script:Gateway)) {
        if (-not $handle -or $handle.Process.HasExited) { throw 'Resource sample requires both live validation processes.' }
        $p = $handle.Process
        $p.Refresh()
        $rows += [pscustomobject]@{ Name = $handle.Name; Pid = $p.Id; WorkingSetMB = [math]::Round($p.WorkingSet64 / 1MB, 2); PrivateMB = [math]::Round($p.PrivateMemorySize64 / 1MB, 2); CPUSeconds = $p.TotalProcessorTime.TotalSeconds }
    }
    return $rows
}

function Invoke-StageResources {
    param([switch]$Idle)
    $sample = @(Get-RunResources)
    $bounded = @($sample | Where-Object { $_.WorkingSetMB -le 0 -or $_.WorkingSetMB -ge 512 }).Count -eq 0
    $id = if ($Idle) { 'W-RES-IDLE' } else { 'W-RES-01' }
    Add-ValidationResult -Id $id -Area 'resources' -Status (Get-Status $bounded) `
        -Requirement 'Both validation process working sets are measured and below 512 MiB' `
        -Observation ($sample | ConvertTo-Json -Compress)
    if ($Idle) { return }
    $scan = Invoke-Api -Path '/api/v1/assessment/scan' -Method POST -WithCsrf -Body '{}'
    $samples = @($sample)
    $done = $false
    $deadline = (Get-Date).AddSeconds(180)
    do {
        $samples += @(Get-RunResources)
        $status = Get-Json (Invoke-Api -Path '/api/v1/assessment/status')
        if ($status -and -not $status.running -and $status.summary.PSObject.Properties['last_run'] -and $status.summary.last_run.status -eq 'completed') { $done = $true; break }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    $peak = ($samples | Measure-Object WorkingSetMB -Maximum).Maximum
    Add-ValidationResult -Id 'W-RES-SCAN' -Area 'resources' -Status (Get-Status ($scan.StatusCode -eq 200 -and $done -and $peak -lt 512)) `
        -Requirement 'Resident memory is sampled during a real assessment until completion' `
        -Observation ('scan HTTP={0} completed={1} samples={2} max working set={3} MiB; final={4}' -f $scan.StatusCode, $done, $samples.Count, $peak, (@(Get-RunResources) | ConvertTo-Json -Compress))
}

# ===========================================================================
# Main
# ===========================================================================

$overall = 'PASS'
try {
    Write-Host "Repo root : $RepoRoot"
    Write-Host "Layout    : $($script:Layout.Layout) (repository root source: $($script:Layout.RepoRootSource))"
    Write-Host "Work root : $WorkRoot"
    Write-Host "Stages    : $($Stage -join ', ')"
    Write-Host ""

    if (-not $SkipBuild -and -not $script:Layout.BuildPossible) {
        Write-Warning ('No Go module was found at the repository root {0}, so the build stage cannot compile the product in this layout. Run with -SkipBuild and -BinDir to validate prebuilt executables, or pass -RepoRoot pointing at a Radanor Sparrow checkout.' -f $RepoRoot)
    }

    $script:Paths = New-IsolatedValidationRoot -Root $WorkRoot -IsolateMonitoredRoots:(-not $AllowRealMonitoredRoots)

    if (Test-Stage 'environment') { Invoke-StageEnvironment }
    else { $script:Facts = Get-WindowsEnvironmentFacts }

    if (Test-Stage 'build') { Invoke-StageBuild } elseif ($script:Binaries.Count -eq 0) { try { $script:Binaries['core'] = Resolve-Executable -Name 'radanor-sparrow-core.exe'; $script:Binaries['gateway'] = Resolve-Executable -Name 'radanor-sparrow-gateway.exe' } catch { } }
    if (-not $script:Binaries.ContainsKey('core')) {
        if (Test-Stage 'build') { } else { try { $script:Binaries['core'] = Resolve-Executable -Name 'radanor-sparrow-core.exe'; $script:Binaries['gateway'] = Resolve-Executable -Name 'radanor-sparrow-gateway.exe' } catch { } }
    }

    $runtimeStages = @('startup','ipc','auth','storage','monitoring','posture','inventory','settings','lifecycle','resources','shutdown')
    $needsRuntime = @($script:Stage | Where-Object { $runtimeStages -contains $_ }).Count -gt 0
    if ($needsRuntime) {
        if (-not $script:Binaries.ContainsKey('core') -and -not $SkipBuild) { Invoke-StageBuild }
        Invoke-StageStartup
        if (-not $script:Origin) { throw 'Runtime prerequisite failed: core and gateway did not start.' }
        if (-not (Test-Stage 'auth')) { Connect-ShieldSession | Out-Null }
        if (Test-Stage 'resources') { Invoke-StageResources -Idle }
    }
    foreach ($name in @('ipc','auth','storage','monitoring','posture','inventory','settings','lifecycle','resources','shutdown')) {
        if (-not (Test-Stage $name)) { continue }
        try {
            & ('Invoke-Stage' + (Get-Culture).TextInfo.ToTitleCase($name))
            if ($name -eq 'monitoring') { Invoke-StageMonitoringRestart }
        } catch {
            Add-ValidationResult -Id ('W-HARNESS-' + $name.ToUpperInvariant()) -Area $name -Status 'FAIL' `
                -Requirement ('Selected stage completes: ' + $name) -Observation $_.Exception.Message
            Write-Host $_.ScriptStackTrace
            if (-not $script:Origin -and $name -ne 'shutdown') {
                try { Start-Shield -RedirectRoots | Out-Null; Connect-ShieldSession | Out-Null } catch { Write-Warning ('Recovery failed: ' + $_.Exception.Message) }
            }
        }
    }
} catch {
    $overall = 'FAIL'
    Write-Host "HARNESS ERROR: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host $_.ScriptStackTrace -ForegroundColor DarkGray
    try {
        Add-ValidationResult -Id 'W-HARNESS' -Area 'harness' -Status 'FAIL' `
            -Requirement 'The harness itself completed without an unhandled error' `
            -Observation "$($_.Exception.Message)"
    } catch { }
} finally {
    try { Stop-Shield } catch { }
}

$doc = Export-ValidationResults -JsonPath $ResultJson -MarkdownPath $ResultMarkdown
Write-Host ''
Write-Host 'Summary:' -ForegroundColor White
foreach ($k in $doc.summary.Keys) {
    Write-Host ('  {0,-9} {1}' -f $k, $doc.summary[$k])
}
Write-Host ''
Write-Host "JSON     : $ResultJson"
Write-Host "Markdown : $ResultMarkdown"

$failed = [int]$doc.summary['FAIL']
if (@(Get-ValidationResults).Count -eq 0 -or $failed -gt 0 -or $overall -eq 'FAIL') { exit 1 }
exit 0
