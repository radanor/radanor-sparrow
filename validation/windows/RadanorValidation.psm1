# Radanor Sparrow CE-7A - Windows validation helper module.
#
# Read-only with respect to the machine under test: nothing in this module
# changes Defender, the firewall, BitLocker, any service, any scheduled task or
# any production Event Log. State written by the harness lives under an isolated
# directory chosen by the operator, and the monitored folders are redirected
# into that directory so no real user Startup folder or .ssh directory is
# touched unless the operator explicitly opts in.
#
# Target: Windows PowerShell 5.1 (ships with Windows 11) and PowerShell 7+.

Set-StrictMode -Version Latest

$script:ValidStatuses = @('PASS', 'FAIL', 'BLOCKED', 'NOT RUN', 'SIMULATED')
$script:Results = New-Object System.Collections.Generic.List[object]
$script:Context = $null

# ---------------------------------------------------------------------------
# Result recording
# ---------------------------------------------------------------------------

function Reset-ValidationResults {
    [CmdletBinding()]
    param()
    $script:Results.Clear()
    $script:Context = $null
}

function Add-ValidationResult {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string]$Id,
        [Parameter(Mandatory)][string]$Area,
        [Parameter(Mandatory)][ValidateSet('PASS', 'FAIL', 'BLOCKED', 'NOT RUN', 'SIMULATED')][string]$Status,
        [Parameter(Mandatory)][string]$Requirement,
        [string]$Observation = '',
        [string]$Evidence = ''
    )
    $script:Results.Add([pscustomobject]@{
        Id          = $Id
        Area        = $Area
        Status      = $Status
        Requirement = $Requirement
        Observation = $Observation
        Evidence    = $Evidence
        RecordedAt  = (Get-Date).ToString('o')
    })
    $colour = switch ($Status) {
        'PASS' { 'Green' }
        'FAIL' { 'Red' }
        'BLOCKED' { 'Yellow' }
        'NOT RUN' { 'DarkGray' }
        'SIMULATED' { 'Cyan' }
        default { 'White' }
    }
    Write-Host ('[{0,-9}] {1} {2}' -f $Status, $Id, $Requirement) -ForegroundColor $colour
    if ($Observation) { Write-Host ('            {0}' -f $Observation) }
}

function Get-ValidationResults {
    [CmdletBinding()]
    param()
    # Returned as a plain array: PowerShell's @() operator cannot wrap a
    # System.Collections.Generic.List directly in some versions ("Argument
    # types do not match"), so every reader goes through this copy. The array
    # is returned without a unary comma so the pipeline unrolls it into items.
    return $script:Results.ToArray()
}

function Export-ValidationResults {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string]$JsonPath,
        [string]$MarkdownPath
    )
    $all = $script:Results.ToArray()
    $summary = [ordered]@{}
    foreach ($s in $script:ValidStatuses) { $summary[$s] = @($all | Where-Object Status -eq $s).Count }
    $doc = [pscustomobject]@{
        schema  = 'shield.ce7a.validation/v1'
        host    = Get-WindowsEnvironmentFacts
        summary = $summary
        results = $all
    }
    $dir = Split-Path -Parent $JsonPath
    if ($dir -and -not (Test-Path $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }
    $doc | ConvertTo-Json -Depth 6 | Set-Content -Path $JsonPath -Encoding UTF8
    if ($MarkdownPath) {
        $lines = @('# CE-7A validation run', '', (Get-Date).ToString('o'), '', '| Id | Area | Status | Requirement | Observation |', '| --- | --- | --- | --- | --- |')
        foreach ($r in $all) {
            $o = ($r.Observation -replace '\|', '\|' -replace "`r?`n", ' ')
            $req = ($r.Requirement -replace '\|', '\|' -replace "`r?`n", ' ')
            $lines += ('| {0} | {1} | {2} | {3} | {4} |' -f $r.Id, $r.Area, $r.Status, $req, $o)
        }
        $lines += ''
        $lines += '## Summary'
        $lines += ''
        foreach ($s in $script:ValidStatuses) { $lines += ('* {0}: {1}' -f $s, $summary[$s]) }
        $lines | Set-Content -Path $MarkdownPath -Encoding UTF8
    }
    return $doc
}

# ---------------------------------------------------------------------------
# Platform classification (pure - reads nothing from the machine)
#
# The Windows version family is decided by the OS *build number*. The registry
# `ProductName` string is branding only and never decides the family: genuine
# Windows 11 images - preinstalled OEM Home Single Language builds and
# Windows 10 to Windows 11 upgrades among them - still report the legacy
# "Windows 10 ..." ProductName, while their build number is unambiguous.
#
# The boundaries are platform invariants rather than a pinned build for one
# test machine, and are taken from Microsoft's release information:
#
#   10240  Windows 10 RTM (1507)
#   19045  Windows 10 22H2 - the final Windows 10 release build
#   22000  Windows 11 RTM (21H2)
#
# The band between 19045 and 22000 deliberately names no released product:
# pre-release and Insider builds live there (the leaked Windows 11 build was
# 21996), so a build in that band is recorded as pre-release/unclassified and
# never as Windows 10 or Windows 11.
#
# Build numbers are shared across products - Windows Server 2025 is also
# 26100, the same as Windows 11 24H2 - which is why the SKU is read from the
# CIM Caption as well.
# ---------------------------------------------------------------------------

$script:Windows11MinimumBuild = 22000
$script:Windows10MinimumBuild = 10240
$script:Windows10MaximumBuild = 19045

function ConvertTo-OsBuildNumber {
    <#
        Normalises a build value (registry CurrentBuild, CIM BuildNumber) to a
        positive integer, or $null when it is absent, blank or not a number.
        0 and negatives count as missing, never as "an old build". A value with
        a trailing component ("26200.0") keeps its leading integer rather than
        reading as missing.
    #>
    [CmdletBinding()]
    param([object]$Value)
    if ($null -eq $Value) { return $null }
    $text = ([string]$Value).Trim()
    if ([string]::IsNullOrWhiteSpace($text)) { return $null }
    $parsed = 0
    if ([int]::TryParse($text, [ref]$parsed) -and $parsed -gt 0) { return $parsed }
    if ($text -match '^(?<build>[0-9]+)') {
        if ([int]::TryParse($Matches['build'], [ref]$parsed) -and $parsed -gt 0) { return $parsed }
    }
    return $null
}

function ConvertFrom-VersionStringBuildNumber {
    <#
        Extracts the build component of a version string such as "10.0.26200" or
        "10.0.26200.1234". Returns $null when there is no numeric third part.
    #>
    [CmdletBinding()]
    param([string]$VersionString)
    if ([string]::IsNullOrWhiteSpace($VersionString)) { return $null }
    $parts = $VersionString.Split('.')
    if ($parts.Count -lt 3) { return $null }
    $parsed = 0
    if ([int]::TryParse($parts[2].Trim(), [ref]$parsed) -and $parsed -gt 0) { return $parsed }
    return $null
}

function Get-BrandingVersionFamily {
    <#
        Reads a version family out of a branding string (CIM Caption or registry
        ProductName). 'Windows Server' is checked first because the Server 2025
        build (26100) is the same as Windows 11 24H2 and must not be reported as
        Windows 11. 'Windows 11' is then checked before 'Windows 10', because
        'Windows 11' contains the 'Windows 1' prefix of 'Windows 10'.
    #>
    [CmdletBinding()]
    param([string]$Text)
    if ([string]::IsNullOrWhiteSpace($Text)) { return 'Unknown' }
    if ($Text -match 'Windows\s+Server') { return 'Windows Server' }
    if ($Text -match 'Windows\s*11') { return 'Windows 11' }
    if ($Text -match 'Windows\s*10') { return 'Windows 10' }
    return 'Unknown'
}

function Get-WindowsPlatformClassification {
    <#
        .SYNOPSIS
            Classifies a Windows host from supplied OS evidence.

        .DESCRIPTION
            Pure: every input is passed in, so the live run and the regression
            suite (Test-PlatformDetection.ps1) exercise exactly the same rules.

            Version family - the OS build number is authoritative. It is taken
            from the registry CurrentBuild value, then the CIM
            Win32_OperatingSystem BuildNumber, then the version string. The CIM
            Caption corroborates it. A registry ProductName that disagrees is
            reported in BrandingConflicts, and as LegacyProductNameBranding when
            a Windows 11 host still reports "Windows 10 ..." - the field case
            this function exists to classify correctly.

            x64 - describes the operating system, not the PowerShell running
            the harness. A 32-bit PowerShell on 64-bit Windows (WOW64) reports
            an x64 host with IsX64Process = $false and IsWow64OnX64 = $true; an
            AMD64 process under ARM64 emulation is explicitly not an x64 host.

            A host is supported only when the build is a known Windows 11 build
            and the OS is x64. An unknown or invalid build is never supported.
    #>
    [CmdletBinding()]
    param(
        [object]$BuildNumber,
        [object]$CIMBuildNumber,
        [string]$VersionString,
        [string]$Caption,
        [string]$ProductName,
        [string]$DisplayVersion,
        [string]$Architecture,
        [string]$OSArchitecture,
        [string]$Architecture6432 = ''
    )

    $diagnostics = New-Object System.Collections.Generic.List[string]
    $conflicts = New-Object System.Collections.Generic.List[string]

    # --- authoritative build number ----------------------------------------
    $cimBuild = ConvertTo-OsBuildNumber -Value $CIMBuildNumber
    $build = ConvertTo-OsBuildNumber -Value $BuildNumber
    $buildSource = 'none'
    if ($null -ne $build) {
        $buildSource = 'registry CurrentBuild'
    } else {
        $build = $cimBuild
        if ($null -ne $build) {
            $buildSource = 'CIM BuildNumber'
        } else {
            $build = ConvertFrom-VersionStringBuildNumber -VersionString $VersionString
            if ($null -ne $build) { $buildSource = 'version string' }
        }
    }
    if ($buildSource -eq 'none') {
        $diagnostics.Add('No usable OS build number: the registry CurrentBuild value, the CIM BuildNumber and the version string were all absent or non-numeric.')
    }
    if ($null -ne $build -and $null -ne $cimBuild -and $cimBuild -ne $build) {
        $diagnostics.Add("The registry build ($build) and the CIM build ($cimBuild) disagree; the registry value is treated as authoritative.")
    }

    $familyFromBuild = 'Unknown'
    if ($null -ne $build) {
        if ($build -ge $script:Windows11MinimumBuild) {
            $familyFromBuild = 'Windows 11'
        } elseif ($build -ge $script:Windows10MinimumBuild -and $build -le $script:Windows10MaximumBuild) {
            $familyFromBuild = 'Windows 10'
        } elseif ($build -ge $script:Windows10MinimumBuild) {
            # Above the last Windows 10 release build and below Windows 11 RTM.
            # Pre-release and Insider builds live here, so the build number
            # alone names no released product; say so instead of guessing.
            $familyFromBuild = 'Pre-release or unclassified'
            $diagnostics.Add(("Build {0} is above the last Windows 10 release build ({1}) and below Windows 11 RTM ({2}), so it names no released product: it is recorded as a pre-release or unclassified build." -f $build, $script:Windows10MaximumBuild, $script:Windows11MinimumBuild))
        } else {
            $familyFromBuild = 'Older than Windows 10'
        }
    }
    $family = $familyFromBuild

    # --- branding, recorded diagnostically ---------------------------------
    $captionFamily = Get-BrandingVersionFamily -Text $Caption
    $productNameFamily = Get-BrandingVersionFamily -Text $ProductName

    # Windows Server 2025 shares build 26100 with Windows 11 24H2, so the build
    # number alone cannot separate them. The SKU is therefore read from the CIM
    # Caption and ProductName, and a Server host is never a Windows 11 host.
    $isServerSku = (($captionFamily -eq 'Windows Server') -or ($productNameFamily -eq 'Windows Server'))
    if ($isServerSku) {
        $family = 'Windows Server'
        $serverIdentity = if ($captionFamily -eq 'Windows Server') { $Caption } else { $ProductName }
        $diagnostics.Add(("The host identifies as Windows Server ('{0}'), which is not a Windows 11 client target. Build {1} on its own maps to the {2} family, so the SKU is taken from the CIM Caption and ProductName." -f $serverIdentity, $build, $familyFromBuild))
    }

    $legacyProductNameBranding = $false
    $brandingCorroboratesBuild = $null
    if ($family -ne 'Unknown') {
        if ($captionFamily -ne 'Unknown') { $brandingCorroboratesBuild = ($captionFamily -eq $family) }
        if ($captionFamily -ne 'Unknown' -and $captionFamily -ne $family) {
            $conflicts.Add(("CIM Caption '{0}' names {1} but the authoritative OS build {2} is {3}; the build number decides." -f $Caption, $captionFamily, $build, $family))
        }
        if ($productNameFamily -ne 'Unknown' -and $productNameFamily -ne $family) {
            if ($family -eq 'Windows 11' -and $productNameFamily -eq 'Windows 10') {
                $legacyProductNameBranding = $true
                $conflicts.Add(("Registry ProductName '{0}' still carries Windows 10 branding on this {1} host (build {2}, DisplayVersion {3}). This is the legacy branding left behind by Windows 10 OEM images and Windows 10 to Windows 11 upgrades; the build number is authoritative, so this is a recorded diagnostic and not a platform failure." -f $ProductName, $family, $build, $DisplayVersion))
            } else {
                $conflicts.Add(("Registry ProductName '{0}' names {1} but the authoritative OS build {2} is {3}; the build number decides." -f $ProductName, $productNameFamily, $build, $family))
            }
        }
    } elseif ($productNameFamily -eq 'Windows 11' -or $captionFamily -eq 'Windows 11') {
        $diagnostics.Add('Branding names Windows 11, but no valid OS build number was available to confirm the version family.')
    }
    if ($captionFamily -eq 'Unknown') {
        $diagnostics.Add('No CIM Caption was available, so the version family rests on the OS build number alone.')
    }
    if ($productNameFamily -eq 'Unknown') {
        $diagnostics.Add('No registry ProductName was available, so branding could not be cross-checked.')
    }

    # --- host architecture --------------------------------------------------
    $processArchitecture = if ($null -eq $Architecture) { '' } else { ([string]$Architecture).Trim().ToUpperInvariant() }
    $architecture6432 = if ($null -eq $Architecture6432) { '' } else { ([string]$Architecture6432).Trim().ToUpperInvariant() }
    $osArchitectureText = if ($null -eq $OSArchitecture) { '' } else { ([string]$OSArchitecture).Trim().ToLowerInvariant() }

    $isX64Process = ($processArchitecture -eq 'AMD64')
    $isWow64OnX64 = ((-not $isX64Process) -and ($processArchitecture -eq 'X86') -and ($architecture6432 -eq 'AMD64'))
    $osReportsArm = ($osArchitectureText -match 'arm') -or ($architecture6432 -match 'ARM')
    $osReports32Bit = ($osArchitectureText -match '32-bit')
    $isX64OperatingSystem = (($isX64Process -or $isWow64OnX64) -and (-not $osReportsArm) -and (-not $osReports32Bit))

    if ($isX64Process -and $osReportsArm) {
        $diagnostics.Add('An AMD64 process is running under ARM64 emulation, so the host is not an x64 platform.')
    } elseif ($isWow64OnX64) {
        $diagnostics.Add("This PowerShell is 32-bit on an x64 host (PROCESSOR_ARCHITECTURE=x86, PROCESSOR_ARCHITEW6432=AMD64). The host is x64; run the harness from 64-bit PowerShell so the W-ENV-02 process check applies.")
    } elseif (-not $isX64OperatingSystem) {
        $diagnostics.Add(("PROCESSOR_ARCHITECTURE='{0}', PROCESSOR_ARCHITEW6432='{1}', OSArchitecture='{2}' do not describe an x64 host." -f $processArchitecture, $architecture6432, $osArchitectureText))
    }

    $isSupported = (($family -eq 'Windows 11') -and $isX64OperatingSystem)

    $unsupportedReason = ''
    if (-not $isSupported) {
        if ($null -eq $build) {
            $unsupportedReason = 'the OS build number could not be determined, so the version family cannot be confirmed as Windows 11'
        } elseif ($family -eq 'Windows Server') {
            $unsupportedReason = ("the host is Windows Server ({0}); the plan requires a Windows 11 client (build {1} or later)" -f $(if ($captionFamily -eq 'Windows Server') { $Caption } else { $ProductName }), $script:Windows11MinimumBuild)
        } elseif ($family -eq 'Pre-release or unclassified') {
            $unsupportedReason = ("the OS build {0} names no released product: Windows 10 ends at build {1} and Windows 11 starts at build {2}, so it is treated as a pre-release or unclassified build and is not supported" -f $build, $script:Windows10MaximumBuild, $script:Windows11MinimumBuild)
        } elseif ($family -eq 'Windows 10') {
            $unsupportedReason = ("the OS build {0} is Windows 10; the plan requires Windows 11 (build {1} or later)" -f $build, $script:Windows11MinimumBuild)
        } elseif ($family -eq 'Older than Windows 10') {
            $unsupportedReason = ("the OS build {0} predates Windows 10; the plan requires Windows 11 (build {1} or later)" -f $build, $script:Windows11MinimumBuild)
        } else {
            $unsupportedReason = ("the OS build {0} is {1} but the host is not x64 (PROCESSOR_ARCHITECTURE='{2}', PROCESSOR_ARCHITEW6432='{3}', OSArchitecture='{4}')" -f $build, $family, $processArchitecture, $architecture6432, $osArchitectureText)
        }
    }

    [pscustomobject]@{
        BuildNumber               = $build
        BuildKnown                = ($null -ne $build)
        BuildSource               = $buildSource
        CIMBuildNumber            = $cimBuild
        VersionString             = $VersionString
        Windows11MinimumBuild     = $script:Windows11MinimumBuild
        Windows10MinimumBuild     = $script:Windows10MinimumBuild
        Windows10MaximumBuild     = $script:Windows10MaximumBuild
        VersionFamily             = $family
        VersionFamilyFromBuild    = $familyFromBuild
        IsWindows11               = ($family -eq 'Windows 11')
        IsWindows10               = ($family -eq 'Windows 10')
        IsServerSku               = $isServerSku
        ProcessArchitecture       = $processArchitecture
        Architecture6432          = $architecture6432
        OsArchitecture            = $osArchitectureText
        IsX64Process              = $isX64Process
        IsWow64OnX64              = $isWow64OnX64
        IsX64OperatingSystem      = $isX64OperatingSystem
        Caption                   = $Caption
        ProductName               = $ProductName
        DisplayVersion            = $DisplayVersion
        CaptionFamily             = $captionFamily
        ProductNameFamily         = $productNameFamily
        BrandingCorroboratesBuild = $brandingCorroboratesBuild
        LegacyProductNameBranding = $legacyProductNameBranding
        BrandingConflicts         = $conflicts.ToArray()
        Diagnostics               = $diagnostics.ToArray()
        IsSupported               = $isSupported
        UnsupportedReason         = $unsupportedReason
    }
}

# ---------------------------------------------------------------------------
# Harness layout resolution
#
# Where is the module, and what is the repository root? Answered from the
# harness script's own path rather than from $PSScriptRoot, because under
# Windows PowerShell 5.1 a param-block default sees $PSScriptRoot as empty
# (reported by a native 5.1 run of Test-PlatformDetection.ps1).
#
# Two layouts are supported and both are tested:
#
#   repository   <root>\validation\windows\{Invoke-RadanorValidation.ps1, RadanorValidation.psm1}
#   standalone   <any folder>\{Invoke-RadanorValidation.ps1, RadanorValidation.psm1} plus
#                <same folder>\bin\radanor-sparrow-core.exe and radanor-sparrow-gateway.exe
#
# A standalone package cannot build the product from source, so it is run with
# -SkipBuild and -BinDir; BuildPossible is the flag that says so.
# ---------------------------------------------------------------------------

function Resolve-ShieldHarnessLayout {
    <#
        .SYNOPSIS
            Resolves the module path, script folder and repository root for the
            CE-7A harness.

        .DESCRIPTION
            Pure apart from Test-Path/Resolve-Path, so the regression suite can
            drive it against synthetic repository and standalone-bundle
            layouts, including the failure cases: no script path, a script
            folder with no parent, and a missing module.

            -RequestedRepoRoot always wins. Without it the repository root is
            TWO folders above the script, because the repository layout is
            <root>\validation\windows - the same default the harness had
            before this refactor, expressed once instead of inside a param
            default. That default is only a hint: BuildPossible says whether a
            go.mod is really there. If the script folder has fewer than two
            parent folders the root is reported unresolved rather than
            silently becoming a relative path.
    #>
    [CmdletBinding()]
    param(
        [string]$ScriptPath,
        [string]$RequestedRepoRoot = ''
    )
    if ([string]::IsNullOrWhiteSpace($ScriptPath)) {
        throw 'The harness script path could not be determined, so RadanorValidation.psm1 and the repository root cannot be located. Re-run from a copy of validation\windows, or from a standalone bundle, that sits on a real filesystem path.'
    }
    $scriptRoot = Split-Path -Parent $ScriptPath
    if ([string]::IsNullOrWhiteSpace($scriptRoot)) {
        throw ("The harness script path '{0}' has no parent folder, so RadanorValidation.psm1 and the repository root cannot be located." -f $ScriptPath)
    }
    $modulePath = Join-Path $scriptRoot 'RadanorValidation.psm1'
    if (-not (Test-Path -LiteralPath $modulePath)) {
        throw ("RadanorValidation.psm1 was not found at '{0}'. Invoke-RadanorValidation.ps1 and RadanorValidation.psm1 must sit in the same folder - the repository's validation\windows, or a standalone bundle." -f $scriptRoot)
    }
    $repoRoot = ''
    $repoRootSource = ''
    if (-not [string]::IsNullOrWhiteSpace($RequestedRepoRoot)) {
        $repoRoot = $RequestedRepoRoot
        $repoRootSource = 'supplied by the operator'
    } else {
        # Two folders up, so that <root>\validation\windows resolves to <root>.
        $repoRoot = Split-Path -Parent (Split-Path -Parent $scriptRoot)
        if ([string]::IsNullOrWhiteSpace($repoRoot)) {
            $repoRootSource = 'unresolved: the harness folder has fewer than two parent folders'
        } else {
            $repoRootSource = 'two folders above the harness script (repository layout)'
        }
    }
    $goMod = ''
    if ($repoRoot) { $goMod = Join-Path $repoRoot 'go.mod' }
    $isRepository = ($goMod -and (Test-Path -LiteralPath $goMod))
    [pscustomobject]@{
        ScriptPath     = $ScriptPath
        ScriptRoot     = $scriptRoot
        ModulePath     = (Resolve-Path -LiteralPath $modulePath).Path
        RepoRoot       = $repoRoot
        RepoRootSource = $repoRootSource
        Layout         = if ($isRepository) { 'repository' } else { 'standalone bundle' }
        BuildPossible  = [bool]$isRepository
        GoModPath      = $goMod
    }
}

# ---------------------------------------------------------------------------
# Environment facts (Windows plan reporting rule: build, language, elevation)
# ---------------------------------------------------------------------------

function Get-ObjectProperty {
    <#
        StrictMode-safe property read. Returns $null when the object is null or
        the property is missing, so a partially readable machine (a denied
        registry key, a cmdlet that is absent) yields a missing value instead of
        a terminating error.
    #>
    [CmdletBinding()]
    param(
        [object]$InputObject,
        [Parameter(Mandatory)][string]$Name
    )
    if ($null -eq $InputObject) { return $null }
    $property = $InputObject.PSObject.Properties[$Name]
    if ($null -eq $property) { return $null }
    return $property.Value
}

function Get-WindowsEnvironmentFacts {
    [CmdletBinding()]
    param()
    $id = $null
    $elevated = $false
    try {
        $id = [System.Security.Principal.WindowsIdentity]::GetCurrent()
        $principal = New-Object System.Security.Principal.WindowsPrincipal($id)
        $elevated = $principal.IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)
    } catch { }
    $os = $null; $cs = $null; $bios = $null; $reg = $null
    try { $os = Get-CimInstance -ClassName Win32_OperatingSystem -ErrorAction Stop } catch { }
    try { $cs = Get-CimInstance -ClassName Win32_ComputerSystem -ErrorAction Stop } catch { }
    try { $bios = Get-CimInstance -ClassName Win32_BIOS -ErrorAction Stop } catch { }
    try { $reg = Get-ItemProperty -Path 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion' -ErrorAction Stop } catch { }
    # StrictMode-safe reads: a machine with a denied registry key or an absent
    # CIM class yields $null rather than terminating the run.
    $currentBuild = Get-ObjectProperty -InputObject $reg -Name 'CurrentBuild'
    $ubr = Get-ObjectProperty -InputObject $reg -Name 'UBR'
    $cimBuild = Get-ObjectProperty -InputObject $os -Name 'BuildNumber'
    $cimCaption = Get-ObjectProperty -InputObject $os -Name 'Caption'
    $versionString = if ($os) { [string]$os.Version } else { [System.Environment]::OSVersion.Version.ToString() }
    $platform = Get-WindowsPlatformClassification `
        -BuildNumber $currentBuild `
        -CIMBuildNumber $cimBuild `
        -VersionString $versionString `
        -Caption $cimCaption `
        -ProductName (Get-ObjectProperty -InputObject $reg -Name 'ProductName') `
        -DisplayVersion (Get-ObjectProperty -InputObject $reg -Name 'DisplayVersion') `
        -Architecture $env:PROCESSOR_ARCHITECTURE `
        -OSArchitecture (Get-ObjectProperty -InputObject $os -Name 'OSArchitecture') `
        -Architecture6432 $env:PROCESSOR_ARCHITEW6432
    [pscustomobject]@{
        ComputerName     = $env:COMPUTERNAME
        WindowsProductName = Get-ObjectProperty -InputObject $reg -Name 'ProductName'
        DisplayVersion   = Get-ObjectProperty -InputObject $reg -Name 'DisplayVersion'
        CurrentBuild     = $currentBuild
        UBR              = $ubr
        VersionString    = $versionString
        BuildString      = if ($null -ne $currentBuild) { '{0}.{1}' -f $currentBuild, $ubr } else { $null }
        Architecture     = $env:PROCESSOR_ARCHITECTURE
        Architecture6432 = $env:PROCESSOR_ARCHITEW6432
        OSArchitecture   = Get-ObjectProperty -InputObject $os -Name 'OSArchitecture'
        CIMCaption       = $cimCaption
        CIMBuildNumber   = $cimBuild
        Is64BitOS        = [System.Environment]::Is64BitOperatingSystem
        Is64BitProcess   = [System.Environment]::Is64BitProcess
        PSVersion        = $PSVersionTable.PSVersion.ToString()
        PSEdition        = if ($PSVersionTable.ContainsKey('PSEdition')) { [string]$PSVersionTable['PSEdition'] } else { 'Desktop' }
        User             = if ($id) { [string]$id.Name } else { $null }
        UserId           = if ($id -and $id.User) { [string]$id.User.Value } else { $null }
        Elevated         = [bool]$elevated
        Culture          = (Get-Culture).Name
        UICulture         = (Get-UICulture).Name
        TimeZone         = (Get-TimeZone).Id
        TotalVisibleMemoryKB = if ($cs) { $cs.TotalPhysicalMemory } else { $null }
        BIOSVersion      = if ($bios) { $bios.SMBIOSBIOSVersion } else { $null }
        Platform         = $platform
        CollectedAt      = (Get-Date).ToString('o')
    }
}

# ---------------------------------------------------------------------------
# Isolated state
# ---------------------------------------------------------------------------

function Test-ShieldPrivateAcl {
    [CmdletBinding()]
    param([Parameter(Mandatory)][string]$Path)
    $sid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $acl = Get-Acl -LiteralPath $Path
    $rules = @($acl.GetAccessRules($true, $true, [System.Security.Principal.SecurityIdentifier]))
    $allow = $false
    foreach ($rule in $rules) {
        if ($rule.AccessControlType -eq [System.Security.AccessControl.AccessControlType]::Deny) { return $false }
        if ($rule.IdentityReference.Value -ne $sid) { return $false }
        if (($rule.FileSystemRights -band [System.Security.AccessControl.FileSystemRights]::FullControl) -eq [System.Security.AccessControl.FileSystemRights]::FullControl -and
            ($rule.PropagationFlags -band [System.Security.AccessControl.PropagationFlags]::InheritOnly) -eq 0) { $allow = $true }
    }
    return $allow
}

function Resolve-ShieldStages {
    param([AllowEmptyCollection()][string[]]$Stages)
    $supported = @('environment','build','startup','ipc','auth','storage','monitoring','posture','inventory','settings','lifecycle','resources','shutdown')
    $selected = @($Stages | ForEach-Object { $_ -split ',' } | ForEach-Object { $_.Trim().ToLowerInvariant() } | Select-Object -Unique)
    if ($selected.Count -eq 0 -or $selected -contains '') { throw 'Stage selection is empty. Specify all or supported stage identifiers.' }
    foreach ($name in $selected) {
        if ($name -ne 'all' -and $supported -notcontains $name) { throw "Unsupported stage '$name'. Supported stages: $($supported -join ', '), all" }
    }
    if ($selected -contains 'all') { return $supported }
    return $selected
}

function New-IsolatedValidationRoot {
    <#
        Creates the isolated state tree and returns the paths the child
        processes need. When -IsolateMonitoredRoots is set (the default) the
        APPDATA and USERPROFILE environment variables of the core process are
        redirected here, so the two monitored folders the product opens by
        default (the user Startup folder and ~/.ssh) are created inside the
        scratch area and the real ones are never written to.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string]$Root,
        [switch]$IsolateMonitoredRoots = $true
    )
    $state = Join-Path $Root 'state'
    $runtime = Join-Path $Root 'run'
    $logs = Join-Path $Root 'logs'
    $scratch = Join-Path $Root 'scratch'
    foreach ($p in @($Root, $state, $runtime, $logs, $scratch)) {
        if (-not (Test-Path $p)) { New-Item -ItemType Directory -Path $p -Force | Out-Null }
        # Replace only this directory's DACL. Never recursively rewrite child ACLs.
        $sid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
        $acl = New-Object System.Security.AccessControl.DirectorySecurity
        $acl.SetAccessRuleProtection($true, $false)
        $rule = New-Object System.Security.AccessControl.FileSystemAccessRule($sid, 'FullControl', 'ContainerInherit, ObjectInherit', 'None', 'Allow')
        $acl.AddAccessRule($rule)
        (Get-Item -LiteralPath $p).SetAccessControl($acl)
        if (-not (Test-ShieldPrivateAcl -Path $p)) { throw "Private directory ACL verification failed: $p" }
        # Exercise actual access without disturbing any existing child.
        $probe = Join-Path $p ([guid]::NewGuid().ToString() + '.acl-probe')
        try {
            [System.IO.File]::WriteAllText($probe, 'private ACL access probe')
            if ([System.IO.File]::ReadAllText($probe) -ne 'private ACL access probe') { throw 'ACL readback failed' }
        } finally { if (Test-Path -LiteralPath $probe) { Remove-Item -LiteralPath $probe } }
    }
    $appData = Join-Path $scratch 'appdata'
    $userProfile = Join-Path $scratch 'profile'
    $startup = Join-Path $appData 'Microsoft\Windows\Start Menu\Programs\Startup'
    $ssh = Join-Path $userProfile '.ssh'
    foreach ($p in @($appData, $userProfile, $startup, $ssh)) {
        if (-not (Test-Path $p)) { New-Item -ItemType Directory -Path $p -Force | Out-Null }
    }
    [pscustomobject]@{
        Root                  = (Resolve-Path $Root).Path
        StateDir              = (Resolve-Path $state).Path
        RuntimeDir            = (Resolve-Path $runtime).Path
        LogDir                = (Resolve-Path $logs).Path
        Scratch               = (Resolve-Path $scratch).Path
        AppData               = (Resolve-Path $appData).Path
        UserProfile           = (Resolve-Path $userProfile).Path
        MonitoredStartup      = (Resolve-Path $startup).Path
        MonitoredSSH          = (Resolve-Path $ssh).Path
        IsolateMonitoredRoots = [bool]$IsolateMonitoredRoots
    }
}

function Get-ShieldChildEnvironment {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][object]$Paths,
        [switch]$RedirectMonitoredRoots
    )
    $envMap = @{
        RADANOR_SPARROW_STATE_DIR   = $Paths.StateDir
        RADANOR_SPARROW_RUNTIME_DIR = $Paths.RuntimeDir
    }
    if ($RedirectMonitoredRoots) {
        if (-not $Paths.IsolateMonitoredRoots) { throw 'RedirectMonitoredRoots requested but isolation is disabled' }
        $envMap['APPDATA'] = $Paths.AppData
        $envMap['USERPROFILE'] = $Paths.UserProfile
    }
    return $envMap
}

# ---------------------------------------------------------------------------
# Process control
# ---------------------------------------------------------------------------

function Start-ShieldProcess {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string]$ExePath,
        [Parameter(Mandatory)][object]$Paths,
        [string[]]$ArgumentList = @(),
        [hashtable]$ExtraEnvironment = @{},
        [switch]$RedirectMonitoredRoots
    )
    $logPath = Join-Path $Paths.LogDir ("{0}-{1}.log" -f $Name, [guid]::NewGuid().ToString('N'))
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $ExePath
    if ($ArgumentList -and $ArgumentList.Count -gt 0) { $psi.Arguments = ($ArgumentList -join ' ') }
    $psi.UseShellExecute = $false
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.CreateNoWindow = $true
    $envMap = Get-ShieldChildEnvironment -Paths $Paths -RedirectMonitoredRoots:$RedirectMonitoredRoots
    foreach ($k in $ExtraEnvironment.Keys) { $envMap[$k] = $ExtraEnvironment[$k] }
    foreach ($k in $envMap.Keys) { $psi.EnvironmentVariables[$k] = [string]$envMap[$k] }
    $proc = New-Object System.Diagnostics.Process
    $proc.StartInfo = $psi
    $null = $proc.Start()
    # Drain asynchronously so a chatty child can never block on a full pipe.
    $null = Register-ObjectEvent -InputObject $proc -EventName OutputDataReceived -Action {
        if ($EventArgs.Data) { Add-Content -Path $Event.MessageData -Value $EventArgs.Data }
    } -MessageData $logPath
    $null = Register-ObjectEvent -InputObject $proc -EventName ErrorDataReceived -Action {
        if ($EventArgs.Data) { Add-Content -Path $Event.MessageData -Value $EventArgs.Data }
    } -MessageData $logPath
    $proc.BeginOutputReadLine()
    $proc.BeginErrorReadLine()
    [pscustomobject]@{
        Name    = $Name
        Process = $proc
        Exe     = $ExePath
        LogPath = $logPath
        Started = Get-Date
    }
}

function Stop-ShieldProcess {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][object]$Handle,
        [int]$TimeoutSeconds = 10
    )
    $proc = $Handle.Process
    if (-not $proc) { return }
    try {
        if (-not $proc.HasExited) {
            $proc.CloseMainWindow() | Out-Null
            if (-not $proc.WaitForExit($TimeoutSeconds * 1000)) {
                $proc.Kill()
                $null = $proc.WaitForExit(5000)
            }
        }
    } catch { Write-Warning "stopping $($Handle.Name): $_" }
    finally {
        # Drop the two output-drain subscribers registered for this process so a
        # long harness run does not accumulate event handlers.
        try {
            Get-EventSubscriber -ErrorAction SilentlyContinue |
                Where-Object { $_.SourceObject -is [System.Diagnostics.Process] -and $_.SourceObject.Id -eq $proc.Id } |
                ForEach-Object { Unregister-Event -SubscriptionId $_.SubscriptionId -ErrorAction SilentlyContinue }
        } catch { }
    }
}

function Wait-ShieldGatewayOrigin {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][object]$Handle,
        [int]$TimeoutSeconds = 30
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        if (Test-Path $Handle.LogPath) {
            $line = Get-Content -Path $Handle.LogPath -ErrorAction SilentlyContinue | Where-Object { $_ -match '"origin"' } | Select-Object -First 1
            if ($line) {
                try {
                    $obj = $line | ConvertFrom-Json
                    if ($obj.origin) { return [string]$obj.origin }
                } catch { Start-Sleep -Milliseconds 200 }
            }
        }
        if ($Handle.Process.HasExited) { throw "$($Handle.Name) exited with code $($Handle.Process.ExitCode) before publishing an origin" }
        Start-Sleep -Milliseconds 200
    }
    throw "$($Handle.Name) did not publish an origin within $TimeoutSeconds seconds"
}

function Wait-ShieldLogMatch {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][object]$Handle,
        [Parameter(Mandatory)][string]$Pattern,
        [int]$TimeoutSeconds = 30
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        if (Test-Path $Handle.LogPath) {
            $hit = Get-Content -Path $Handle.LogPath -ErrorAction SilentlyContinue | Where-Object { $_ -match $Pattern } | Select-Object -First 1
            if ($hit) { return $hit }
        }
        Start-Sleep -Milliseconds 200
    }
    return $null
}

# ---------------------------------------------------------------------------
# IPC client: named pipe + HMAC, identical to internal/ipc
# ---------------------------------------------------------------------------

function Read-IpcSecret {
    [CmdletBinding()]
    param([Parameter(Mandatory)][string]$RuntimeDir)
    $path = Join-Path $RuntimeDir 'ipc.key'
    if (-not (Test-Path $path)) { throw "IPC secret not found at $path - is the core running?" }
    return [System.IO.File]::ReadAllBytes($path)
}

function Invoke-ShieldIpc {
    <#
        Speaks the product's own IPC protocol: one newline-terminated JSON
        request, one newline-terminated JSON response, HMAC-SHA256 over
        version\0operation\0value\0timestamp\0nonce keyed by the per-install
        32-byte secret. Used to obtain a launch ticket exactly the way the
        gateway does.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][byte[]]$Secret,
        [Parameter(Mandatory)][string]$Operation,
        [string]$Value = '',
        [int]$TimeoutSeconds = 5
    )
    $timestamp = [int64]([DateTimeOffset]::UtcNow.ToUnixTimeSeconds())
    $nonceBytes = New-Object byte[] 18
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    $rng.GetBytes($nonceBytes)
    $rng.Dispose()
    $nonce = [Convert]::ToBase64String($nonceBytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
    $parts = @([string]1, $Operation, $Value, [string]$timestamp, $nonce)
    $joined = [string]::Join([char]0, $parts)
    $hmac = New-Object System.Security.Cryptography.HMACSHA256
    $hmac.Key = $Secret
    $hash = $hmac.ComputeHash([System.Text.Encoding]::UTF8.GetBytes($joined))
    $hmac.Dispose()
    $mac = -join ($hash | ForEach-Object { $_.ToString('x2') })
    $req = [ordered]@{
        version   = 1
        operation = $Operation
        value     = $Value
        timestamp = $timestamp
        nonce     = $nonce
        mac       = $mac
    }
    $payload = ($req | ConvertTo-Json -Compress) + "`n"
    $pipe = New-Object System.IO.Pipes.NamedPipeClientStream('.', 'RadanorShieldCore', [System.IO.Pipes.PipeDirection]::InOut)
    $pipe.Connect($TimeoutSeconds * 1000)
    try {
        $bytes = [System.Text.Encoding]::UTF8.GetBytes($payload)
        $pipe.Write($bytes, 0, $bytes.Length)
        $pipe.Flush()
        $reader = New-Object System.IO.StreamReader($pipe, [System.Text.Encoding]::UTF8)
        $line = $reader.ReadLine()
        if (-not $line) { throw 'IPC returned no response line' }
        return ($line | ConvertFrom-Json)
    } finally {
        $pipe.Dispose()
    }
}

function Get-ShieldLaunchTicket {
    [CmdletBinding()]
    param([Parameter(Mandatory)][string]$RuntimeDir)
    $secret = Read-IpcSecret -RuntimeDir $RuntimeDir
    $resp = Invoke-ShieldIpc -Secret $secret -Operation 'issue_ticket'
    if (-not $resp.ok) { throw "issue_ticket failed: $($resp.error)" }
    return [string]$resp.value
}

# ---------------------------------------------------------------------------
# HTTP client (session cookie + CSRF + Origin)
# ---------------------------------------------------------------------------

function Invoke-ShieldHttp {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string]$Uri,
        [ValidateSet('GET', 'POST')][string]$Method = 'GET',
        [hashtable]$Headers = @{},
        [string]$Body,
        [object]$WebSession,
        [int]$TimeoutSeconds = 30
    )
    $params = @{
        Uri             = $Uri
        Method          = $Method
        Headers         = $Headers
        UseBasicParsing = $true
        TimeoutSec      = $TimeoutSeconds
    }
    if ($WebSession) { $params['WebSession'] = $WebSession }
    if ($PSBoundParameters.ContainsKey('Body')) {
        $params['Body'] = $Body
        $params['ContentType'] = 'application/json'
    }
    try {
        $r = Invoke-WebRequest @params
        [pscustomobject]@{
            StatusCode = [int]$r.StatusCode
            Body       = [string]$r.Content
            Headers    = $r.Headers
            Session    = $WebSession
            Error      = $null
            TransportError = $false
        }
    } catch {
        $status = 0
        $body = ''
        $failure = $_
        $resp = $null
        $exception = $failure.Exception
        $transportStatus = ''
        while ($exception) {
            if ($exception -is [System.Net.WebException]) { $transportStatus = [string]$exception.Status }
            if ($exception.PSObject.Properties['Response'] -and $exception.Response) { $resp = $exception.Response; break }
            $exception = $exception.InnerException
        }
        if ($resp -and $resp.PSObject.Properties['StatusCode']) { $status = [int]$resp.StatusCode }
        try {
            if ($resp -and $resp.GetResponseStream()) {
                $sr = New-Object System.IO.StreamReader($resp.GetResponseStream())
                $body = $sr.ReadToEnd()
                $sr.Dispose()
            }
        } catch { }
        if (-not $body -and $failure.ErrorDetails -and $failure.ErrorDetails.Message) { $body = $failure.ErrorDetails.Message }
        [pscustomobject]@{
            StatusCode = $status
            Body       = $body
            Headers    = $(if ($resp -and $resp.PSObject.Properties['Headers']) { $resp.Headers } else { @{} })
            Session    = $WebSession
            Error      = [string]$failure.Exception.Message
            TransportError = ($status -eq 0)
            TransportStatus = $transportStatus
        }
    }
}

function New-ShieldSession {
    <#
        Obtains a launch ticket over IPC, exchanges it for a session cookie and
        a CSRF token, and returns the handle the rest of the run uses. The
        ticket is single-use, exactly as in the product.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string]$Origin,
        [Parameter(Mandatory)][string]$RuntimeDir,
        [switch]$OpenBrowser
    )
    $ticket = Get-ShieldLaunchTicket -RuntimeDir $RuntimeDir
    $launchUrl = '{0}/#ticket={1}' -f $Origin, $ticket
    $session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
    if ($OpenBrowser) {
        # The harness performs the same launch the gateway performs; a human
        # operator records whether the default browser opened with the ticket.
        Start-Process $launchUrl | Out-Null
    }
    $r = Invoke-ShieldHttp -Uri ('{0}/api/v1/session/exchange' -f $Origin) -Method POST `
        -Headers @{ Origin = $Origin } -Body ('{{"ticket":"{0}"}}' -f $ticket) -WebSession $session
    $csrf = $null
    if ($r.StatusCode -eq 200) {
        try { $csrf = ($r.Body | ConvertFrom-Json).csrf_token } catch { }
    }
    [pscustomobject]@{
        Origin     = $Origin
        Session    = $session
        CsrfToken  = $csrf
        LaunchUrl  = $launchUrl
        StatusCode = $r.StatusCode
        Body       = $r.Body
    }
}

function Assert-Status {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][object]$Response,
        [Parameter(Mandatory)][int[]]$Expected,
        [string]$Because = ''
    )
    if ($Expected -contains [int]$Response.StatusCode) { return $true }
    return $false
}

# ---------------------------------------------------------------------------
# Native comparison sources (read-only)
# ---------------------------------------------------------------------------

function Get-NativeWindowsInventory {
    <#
        Authoritative readings the Shield inventory is compared against. Every
        call here is read-only; none of them alter system state.
    #>
    [CmdletBinding()]
    param()
    $tcp = @()
    $udp = @()
    $svc = @()
    $apps = @()
    $tcpError = $null
    $udpError = $null
    $svcError = $null
    try { $tcp = @(Get-NetTCPConnection -State Listen -ErrorAction Stop | Select-Object LocalAddress, LocalPort, OwningProcess) } catch { $tcp = @(); $tcpError = $_.Exception.Message }
    try { $udp = @(Get-NetUDPEndpoint -ErrorAction Stop | Select-Object LocalAddress, LocalPort, OwningProcess) } catch { $udp = @(); $udpError = $_.Exception.Message }
    try { $svc = @(Get-Service -ErrorAction Stop | Select-Object Name, Status, StartType) } catch { $svc = @(); $svcError = $_.Exception.Message }
    foreach ($root in @(
        'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*',
        'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*',
        'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*'
    )) {
        try {
            $apps += @(Get-ItemProperty -Path $root -ErrorAction Stop |
                Where-Object { (Get-ObjectProperty -InputObject $_ -Name 'DisplayName') } |
                ForEach-Object { [pscustomobject]@{ Name = $_.DisplayName; Version = (Get-ObjectProperty -InputObject $_ -Name 'DisplayVersion'); Publisher = (Get-ObjectProperty -InputObject $_ -Name 'Publisher') } })
        } catch { }
    }
    [pscustomobject]@{
        TCPListen    = $tcp
        TCPError     = $tcpError
        UDPEndpoints = $udp
        UDPError     = $udpError
        Services     = $svc
        ServiceError = $svcError
        Apps         = $apps
        CollectedAt  = (Get-Date).ToString('o')
    }
}

function Get-NativeWindowsPosture {
    <#
        Authoritative posture readings: Defender, firewall, BitLocker, updates,
        OS build. All read-only cmdlets; none of them change a policy.
    #>
    [CmdletBinding()]
    param()
    $out = [ordered]@{}
    try {
        $mp = Get-MpComputerStatus -ErrorAction Stop
        $out['defender'] = [pscustomobject]@{
            AMServiceEnabled          = $mp.AMServiceEnabled
            AntispywareEnabled        = $mp.AntispywareEnabled
            AntivirusEnabled          = $mp.AntivirusEnabled
            RealTimeProtectionEnabled = $mp.RealTimeProtectionEnabled
            BehaviorMonitorEnabled    = $mp.BehaviorMonitorEnabled
            IoavProtectionEnabled     = $mp.IoavProtectionEnabled
            NISEnabled                = $mp.NISEnabled
            AMRunningMode             = $mp.AMRunningMode
            AntivirusSignatureLastUpdated = if ($mp.AntivirusSignatureLastUpdated) { $mp.AntivirusSignatureLastUpdated.ToString('o') } else { $null }
            Error                     = $null
        }
    } catch { $out['defender'] = [pscustomobject]@{ Error = $_.Exception.Message } }
    try {
        $fw = Get-NetFirewallProfile -ErrorAction Stop
        $out['firewallProfiles'] = @($fw | ForEach-Object { [pscustomobject]@{ Name = $_.Name; Enabled = $_.Enabled; DefaultInboundAction = "$($_.DefaultInboundAction)"; DefaultOutboundAction = "$($_.DefaultOutboundAction)" } })
    } catch { $out['firewallProfiles'] = @(); $out['firewallError'] = $_.Exception.Message }
    try {
        $bl = Get-BitLockerVolume -ErrorAction Stop
        $out['bitlocker'] = @($bl | ForEach-Object { [pscustomobject]@{ MountPoint = $_.MountPoint; VolumeStatus = "$($_.VolumeStatus)"; ProtectionStatus = "$($_.ProtectionStatus)"; VolumeType = "$($_.VolumeType)" } })
    } catch { $out['bitlocker'] = @(); $out['bitlockerError'] = $_.Exception.Message }
    try {
        $hotfix = @(Get-HotFix -ErrorAction Stop)
        $out['hotfixCount'] = $hotfix.Count
        $out['newestHotfix'] = if ($hotfix.Count) { ($hotfix | Sort-Object InstalledOn -Descending | Select-Object -First 1).HotFixID } else { $null }
    } catch { $out['hotfixError'] = $_.Exception.Message }
    $reg = Get-ItemProperty -Path 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion' -ErrorAction SilentlyContinue
    $out['os'] = if ($reg) { [pscustomobject]@{ ProductName = $reg.ProductName; DisplayVersion = $reg.DisplayVersion; CurrentBuild = "$($reg.CurrentBuild).$($reg.UBR)"; UBR = $reg.UBR } } else { $null }
    [pscustomobject]$out
}

# ---------------------------------------------------------------------------
# Working-set sampling (resource measurement)
# ---------------------------------------------------------------------------

function Get-ShieldWorkingSet {
    [CmdletBinding()]
    param([Parameter(Mandatory)][string[]]$ProcessName)
    $rows = @()
    foreach ($n in $ProcessName) {
        foreach ($p in (Get-Process -Name $n -ErrorAction SilentlyContinue)) {
            $rows += [pscustomobject]@{
                Process      = $p.ProcessName
                Id           = $p.Id
                WorkingSetMB = [math]::Round($p.WorkingSet64 / 1MB, 2)
                PrivateMB    = [math]::Round($p.PrivateMemorySize64 / 1MB, 2)
                CPUSeconds   = [math]::Round($p.CPU, 2)
                SampledAt    = (Get-Date).ToString('o')
            }
        }
    }
    return $rows
}

# ---------------------------------------------------------------------------
# Paged API reads
# ---------------------------------------------------------------------------
# The gateway caps every paged read at 50 rows and answers a larger limit with
# HTTP 400 rather than a bigger page:
#
#   internal/gateway/server.go      maxMonitoringLimit = 50
#   internal/gateway/assessment.go  maxAssessmentLimit = 50
#   internal/storage/store.go       DefaultLimits().MaxPageSize = 50
#
# A harness that asks for one large page therefore gets an error, not evidence.
# These helpers walk the documented cursors instead, and report whether they
# stopped because the data ended or because they hit the row cap, so a caller
# can never mistake a clipped read for a complete one.

$script:MaxApiPageSize = 50

function Get-PagedEvents {
    <#
    .SYNOPSIS
    Reads monitoring events newest-first, walking the next_before cursor.
    .DESCRIPTION
    $Request must accept one path string and return the parsed JSON page, or
    $null when the request failed. Keeping the transport out of this function
    makes the cursor walk testable without a running product.
    .OUTPUTS
    An object with Rows (all rows read), Pages, Truncated ($true when the walk
    stopped at MaxRows with the API still offering more) and Failed.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][scriptblock]$Request,
        [int]$MaxRows = 200,
        [string]$Source,
        [string]$Type
    )
    if ($MaxRows -lt 1) { throw 'MaxRows must be at least 1' }
    $rows = @()
    $before = [int64]0
    $next = [int64]0
    $pages = 0
    $failed = $false
    $base = '/api/v1/monitoring/events?limit=' + $script:MaxApiPageSize
    if ($Source) { $base += '&source=' + [uri]::EscapeDataString($Source) }
    if ($Type) { $base += '&type=' + [uri]::EscapeDataString($Type) }
    while ($rows.Count -lt $MaxRows) {
        $path = if ($before -gt 0) { '&before=' + $before } else { '' }
        $page = & $Request ($base + $path)
        $pages++
        if ($null -eq $page) { $failed = $true; break }
        if (-not $page.PSObject.Properties['events']) { $failed = $true; break }
        $batch = @($page.events)
        if ($batch.Count -eq 0) { break }
        $rows += $batch
        $next = [int64]0
        if ($page.PSObject.Properties['next_before'] -and $null -ne $page.next_before) {
            $next = [int64]$page.next_before
        }
        if ($next -le 0 -or $next -eq $before) { break }
        $before = $next
    }
    [pscustomobject]@{
        Rows      = $rows
        Pages     = $pages
        Truncated = ($next -gt 0 -and $rows.Count -ge $MaxRows)
        Failed    = $failed
    }
}

function Get-PagedInventory {
    <#
    .SYNOPSIS
    Reads inventory rows, walking the next_after name cursor.
    .DESCRIPTION
    $Request has the same contract as in Get-PagedEvents. Total is the count the
    API reports for the whole kind, so a clipped read is visible even when the
    row cap is reached.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][scriptblock]$Request,
        [Parameter(Mandatory)][ValidateSet('software', 'endpoints', 'services')][string]$Kind,
        [int]$MaxRows = 500,
        [string]$Search
    )
    if ($MaxRows -lt 1) { throw 'MaxRows must be at least 1' }
    $rows = @()
    $after = ''
    $next = ''
    $pages = 0
    $failed = $false
    $total = 0
    $sources = @()
    $base = '/api/v1/inventory/' + $Kind + '?limit=' + $script:MaxApiPageSize
    if ($Search) { $base += '&search=' + [uri]::EscapeDataString($Search) }
    while ($rows.Count -lt $MaxRows) {
        $path = if ($after) { '&after=' + [uri]::EscapeDataString($after) } else { '' }
        $page = & $Request ($base + $path)
        $pages++
        if ($null -eq $page) { $failed = $true; break }
        if (-not $page.PSObject.Properties['items']) { $failed = $true; break }
        if ($pages -eq 1) {
            if ($page.PSObject.Properties['total']) { $total = [int]$page.total }
            if ($page.PSObject.Properties['sources'] -and $page.sources) { $sources = @($page.sources) }
        }
        $batch = @($page.items)
        if ($batch.Count -eq 0) { break }
        $rows += $batch
        $next = ''
        if ($page.PSObject.Properties['next_after'] -and $page.next_after) { $next = [string]$page.next_after }
        if (-not $next -or $next -eq $after) { break }
        $after = $next
    }
    [pscustomobject]@{
        Rows      = $rows
        Total     = $total
        Sources   = $sources
        Pages     = $pages
        Truncated = ($next -ne '' -and $rows.Count -ge $MaxRows)
        Failed    = $failed
    }
}

Export-ModuleMember -Function Reset-ValidationResults, Add-ValidationResult, Get-ValidationResults, `
    Export-ValidationResults, Get-WindowsEnvironmentFacts, Get-WindowsPlatformClassification, `
    ConvertTo-OsBuildNumber, ConvertFrom-VersionStringBuildNumber, Get-BrandingVersionFamily, `
    Resolve-ShieldHarnessLayout, New-IsolatedValidationRoot, Test-ShieldPrivateAcl, Resolve-ShieldStages, Get-ObjectProperty, `
    Get-ShieldChildEnvironment, Start-ShieldProcess, Stop-ShieldProcess, Wait-ShieldGatewayOrigin, `
    Wait-ShieldLogMatch, Read-IpcSecret, Invoke-ShieldIpc, Get-ShieldLaunchTicket, Invoke-ShieldHttp, `
    New-ShieldSession, Assert-Status, Get-NativeWindowsInventory, Get-NativeWindowsPosture, `
    Get-ShieldWorkingSet, Get-PagedEvents, Get-PagedInventory
