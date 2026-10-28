#Requires -Version 5.1
<#
    Radanor Sparrow CE-7A - regression suite for Windows platform detection.

    Guards the W-ENV-04 rule in Invoke-RadanorValidation.ps1: the supported
    platform is decided by the OS **build number**, corroborated by the CIM
    Caption, and never by the registry ProductName. Some genuine Windows 11
    images - preinstalled Home Single Language OEM builds and Windows 10 to
    Windows 11 upgrades among them - still report "Windows 10 ..." in
    ProductName, and an earlier harness version failed W-ENV-04 on exactly such
    a machine.

    Pure and offline: no CIM, no registry, no network, no Pester, no machine
    state. Every case feeds synthetic OS evidence to the same
    Get-WindowsPlatformClassification function the live harness calls, so a
    regression here is a regression in what the Windows run will record.

    Covered:

      * Windows 11 x64 with legacy Windows 10 product naming (the field case)
      * Windows 11 x64 with consistent branding, and the 22000 RTM boundary
      * genuine Windows 10 x64 (22H2, one build past it, and pre-Windows-10)
      * the 19046-21999 band, which names no released product and whose
        pre-release builds (the leaked Windows 11 was 21996) are not Windows 11
      * harness path resolution: no param-block default may read $PSScriptRoot
        (the Windows PowerShell 5.1 defect), the repository root stays two
        folders above the harness script, a standalone binary-only package
        resolves its module from beside the script, and a missing script path
        or missing module is refused with a clear message
      * a Windows 10 host whose branding claims Windows 11
      * missing, blank, zero and non-numeric build information
      * CIM-only and version-string-only build fallbacks, and disagreeing sources
      * non-x64: Windows 11 ARM64, 32-bit Windows, AMD64 under ARM64 emulation
      * a 32-bit PowerShell on an x64 host, which must still be supported
      * Windows Server 2025, whose build 26100 matches Windows 11 24H2
      * a property check that branding can never grant support, only restrict it
      * a live smoke test of Get-WindowsEnvironmentFacts on whatever host runs it

    Usage:
        powershell -ExecutionPolicy Bypass -File .\Test-PlatformDetection.ps1

    Exit codes: 0 when every check passed, 1 when at least one failed.
#>
[CmdletBinding()]
param(
    # Resolved in the body below, NOT here. Under Windows PowerShell 5.1 a
    # param-block default sees $PSScriptRoot as empty, so the old default
    # (Join-Path $PSScriptRoot 'RadanorValidation.psm1') crashed at parameter
    # binding before the script body ever ran - reported by a native 5.1 run of
    # this file. PowerShell 7.4 populates it, which is why the defect only
    # surfaced on the Windows test machine.
    [string]$ModulePath = ''
)

$ErrorActionPreference = 'Stop'

# --- locate the module after parameter binding ------------------------------
# $PSCommandPath and $MyInvocation.MyCommand.Path are file-based references to
# this script and are populated by the time the body runs, on Windows
# PowerShell 5.1 and on PowerShell 7+, whatever the invocation style. $PSScriptRoot
# is consulted only as a last resort, because it is the one that read as empty.
# This block deliberately runs before Set-StrictMode so a missing optional
# reference falls through the chain instead of terminating the script.
$scriptRoot = ''
if ($PSCommandPath) { $scriptRoot = Split-Path -Parent $PSCommandPath }
if (-not $scriptRoot -and $MyInvocation.MyCommand.Path) { $scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path }
if (-not $scriptRoot) { $scriptRoot = $PSScriptRoot }
if (-not $scriptRoot) {
    throw 'Cannot determine where this script lives, so RadanorValidation.psm1 cannot be located. Re-run with an explicit -ModulePath.'
}
$scriptPath = Join-Path $scriptRoot 'Test-PlatformDetection.ps1'
if (-not $ModulePath) { $ModulePath = Join-Path $scriptRoot 'RadanorValidation.psm1' }
if (-not (Test-Path -LiteralPath $ModulePath)) {
    throw "RadanorValidation.psm1 was not found at '$ModulePath' (resolved from '$scriptPath'). Run this script from validation\windows, or pass -ModulePath."
}
$ModulePath = (Resolve-Path -LiteralPath $ModulePath).Path

Set-StrictMode -Version Latest

Import-Module $ModulePath -Force

# ---------------------------------------------------------------------------
# Tiny assertion runner (deliberately not Pester: the Windows test machine must
# be able to run this with nothing installed)
# ---------------------------------------------------------------------------

$script:CaseName = ''
$script:CaseCount = 0
$script:CheckCount = 0
$script:Failures = New-Object System.Collections.Generic.List[string]

function Format-Value {
    param($Value)
    if ($null -eq $Value) { return '<null>' }
    if ($Value -is [string] -and $Value -eq '') { return '<empty>' }
    return [string]$Value
}

function Assert-Condition {
    param(
        [Parameter(Mandatory)][bool]$Ok,
        [Parameter(Mandatory)][string]$What,
        [string]$Detail = ''
    )
    $script:CheckCount++
    if ($Ok) {
        Write-Host ('      ok   {0}' -f $What) -ForegroundColor DarkGreen
        return
    }
    Write-Host ('      FAIL {0}' -f $What) -ForegroundColor Red
    if ($Detail) { Write-Host ('           {0}' -f $Detail) -ForegroundColor DarkGray }
    $script:Failures.Add(('{0} :: {1}' -f $script:CaseName, $What))
}

function Assert-Equal {
    param(
        [Parameter(Mandatory)][string]$What,
        $Actual,
        $Expected
    )
    $ok = ($Actual -eq $Expected)
    $detail = ''
    if (-not $ok) { $detail = ("expected '{0}', got '{1}'" -f (Format-Value $Expected), (Format-Value $Actual)) }
    Assert-Condition -Ok $ok -What ('{0} is {1}' -f $What, (Format-Value $Expected)) -Detail $detail
}

function Assert-True {
    param([Parameter(Mandatory)][string]$What, [bool]$Actual)
    Assert-Condition -Ok $Actual -What ('{0} is true' -f $What)
}

function Assert-False {
    param([Parameter(Mandatory)][string]$What, [bool]$Actual)
    Assert-Condition -Ok (-not $Actual) -What ('{0} is false' -f $What)
}

function Assert-Match {
    param(
        [Parameter(Mandatory)][string]$What,
        [string]$Text,
        [Parameter(Mandatory)][string]$Pattern
    )
    $ok = ([string]$Text) -match $Pattern
    Assert-Condition -Ok $ok -What ('{0} matches /{1}/' -f $What, $Pattern) -Detail ([string]$Text)
}

function Merge-Evidence {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][hashtable]$Base,
        [hashtable]$Override
    )
    $result = @{}
    foreach ($key in $Base.Keys) { $result[$key] = $Base[$key] }
    if ($Override) { foreach ($key in $Override.Keys) { $result[$key] = $Override[$key] } }
    return $result
}

function Classify {
    [CmdletBinding()]
    param([Parameter(Mandatory)][hashtable]$Evidence)
    return (Get-WindowsPlatformClassification @Evidence)
}

# ---------------------------------------------------------------------------
# Synthetic evidence
# ---------------------------------------------------------------------------

$win11X64 = @{
    BuildNumber      = 26100
    CIMBuildNumber   = 26100
    VersionString    = '10.0.26100'
    Caption          = 'Microsoft Windows 11 Pro'
    ProductName      = 'Windows 11 Pro'
    DisplayVersion   = '24H2'
    Architecture     = 'AMD64'
    OSArchitecture   = '64-bit'
    Architecture6432 = ''
}

$win10X64 = @{
    BuildNumber      = 19045
    CIMBuildNumber   = 19045
    VersionString    = '10.0.19045'
    Caption          = 'Microsoft Windows 10 Pro'
    ProductName      = 'Windows 10 Pro'
    DisplayVersion   = '22H2'
    Architecture     = 'AMD64'
    OSArchitecture   = '64-bit'
    Architecture6432 = ''
}

$cases = @(
    @{
        Name = 'field case - Windows 11 x64 reporting legacy Windows 10 product naming'
        Evidence = (Merge-Evidence $win11X64 @{
            BuildNumber    = 26200
            CIMBuildNumber = 26200
            VersionString  = '10.0.26200'
            Caption        = 'Microsoft Windows 11 Home Single Language'
            ProductName    = 'Windows 10 Home Single Language'
            DisplayVersion = '25H2'
        })
        Checks = {
            param($c)
            Assert-Equal 'the version family' $c.VersionFamily 'Windows 11'
            Assert-Equal 'the OS build number' $c.BuildNumber 26200
            Assert-Equal 'the build source' $c.BuildSource 'registry CurrentBuild'
            Assert-Equal 'the caption family' $c.CaptionFamily 'Windows 11'
            Assert-Equal 'the product-name family' $c.ProductNameFamily 'Windows 10'
            Assert-True 'support for a Windows 11 x64 host' $c.IsSupported
            Assert-True 'the legacy product-name branding flag' $c.LegacyProductNameBranding
            Assert-Equal 'the number of branding conflicts' @($c.BrandingConflicts).Count 1
            Assert-Match 'the conflict note' ($c.BrandingConflicts -join ' ') 'legacy branding'
            Assert-Match 'the conflict note' ($c.BrandingConflicts -join ' ') 'Windows 10 Home Single Language'
            Assert-Match 'the conflict note' ($c.BrandingConflicts -join ' ') 'build number is authoritative'
            Assert-Equal 'the unsupported reason' $c.UnsupportedReason ''
            Assert-True 'the caption corroborating the build' $c.BrandingCorroboratesBuild
        }
    },
    @{
        Name = 'Windows 11 x64 with consistent branding'
        Evidence = $win11X64
        Checks = {
            param($c)
            Assert-Equal 'the version family' $c.VersionFamily 'Windows 11'
            Assert-True 'support' $c.IsSupported
            Assert-False 'the legacy product-name branding flag' $c.LegacyProductNameBranding
            Assert-Equal 'the number of branding conflicts' @($c.BrandingConflicts).Count 0
            Assert-Equal 'the number of diagnostics' @($c.Diagnostics).Count 0
            Assert-True 'the caption corroborating the build' $c.BrandingCorroboratesBuild
            Assert-True 'the x64 host decision' $c.IsX64OperatingSystem
            Assert-True 'the 64-bit-process decision' $c.IsX64Process
        }
    },
    @{
        Name = 'Windows 11 RTM boundary - build 22000'
        Evidence = (Merge-Evidence $win11X64 @{
            BuildNumber    = 22000
            CIMBuildNumber = 22000
            VersionString  = '10.0.22000'
            Caption        = 'Microsoft Windows 11 Pro'
            DisplayVersion = '21H2'
        })
        Checks = {
            param($c)
            Assert-Equal 'the boundary published by the classifier' $c.Windows11MinimumBuild 22000
            Assert-Equal 'the version family at the boundary' $c.VersionFamily 'Windows 11'
            Assert-True 'support at the boundary' $c.IsSupported
        }
    },
    @{
        Name = 'build below Windows 11 RTM but above the last Windows 10 release'
        Evidence = (Merge-Evidence $win10X64 @{
            BuildNumber    = 21999
            CIMBuildNumber = 21999
            VersionString  = '10.0.21999'
            Caption        = 'Microsoft Windows 10 Pro'
            ProductName    = 'Windows 10 Pro'
        })
        Checks = {
            param($c)
            Assert-Equal 'the version family' $c.VersionFamily 'Pre-release or unclassified'
            Assert-False 'the Windows 10 classification' $c.IsWindows10
            Assert-False 'support for a build that names no released product' $c.IsSupported
            Assert-Match 'the unsupported reason' $c.UnsupportedReason 'no released product'
            Assert-Match 'the unsupported reason' $c.UnsupportedReason '22000'
            Assert-Match 'the diagnostic' ($c.Diagnostics -join ' ') 'names no released product'
            Assert-Equal 'the number of branding conflicts (caption and product name both disagree)' @($c.BrandingConflicts).Count 2
        }
    },
    @{
        Name = 'pre-release Windows 11 build (the leaked 21996) is not Windows 11'
        Evidence = (Merge-Evidence $win11X64 @{
            BuildNumber    = 21996
            CIMBuildNumber = 21996
            VersionString  = '10.0.21996'
            Caption        = 'Microsoft Windows 11 Pro'
            ProductName    = 'Windows 11 Pro'
            DisplayVersion = 'Dev'
        })
        Checks = {
            param($c)
            Assert-Equal 'the version family' $c.VersionFamily 'Pre-release or unclassified'
            Assert-False 'the Windows 11 classification' $c.IsWindows11
            Assert-False 'support for a pre-release build' $c.IsSupported
            Assert-Match 'the conflict note about the pre-release caption' ($c.BrandingConflicts -join ' ') 'names Windows 11'
        }
    },
    @{
        Name = 'build 19046 is just past the last Windows 10 release'
        Evidence = (Merge-Evidence $win10X64 @{
            BuildNumber    = 19046
            CIMBuildNumber = 19046
            VersionString  = '10.0.19046'
            Caption        = 'Microsoft Windows 10 Pro'
        })
        Checks = {
            param($c)
            Assert-Equal 'the version family' $c.VersionFamily 'Pre-release or unclassified'
            Assert-False 'support' $c.IsSupported
        }
    },
    @{
        Name = 'genuine Windows 10 22H2 x64'
        Evidence = $win10X64
        Checks = {
            param($c)
            Assert-Equal 'the version family' $c.VersionFamily 'Windows 10'
            Assert-Equal 'the build-derived family' $c.VersionFamilyFromBuild 'Windows 10'
            Assert-Equal 'the last Windows 10 release build' $c.Windows10MaximumBuild 19045
            Assert-True 'the Windows 10 classification' $c.IsWindows10
            Assert-False 'the Windows 11 classification' $c.IsWindows11
            Assert-False 'support for Windows 10' $c.IsSupported
            Assert-Match 'the unsupported reason' $c.UnsupportedReason 'is Windows 10'
            Assert-Equal 'the number of branding conflicts' @($c.BrandingConflicts).Count 0
        }
    },
    @{
        Name = 'Windows 10 host whose branding claims Windows 11'
        Evidence = (Merge-Evidence $win10X64 @{
            Caption     = 'Microsoft Windows 10 Pro'
            ProductName = 'Windows 11 Pro'
        })
        Checks = {
            param($c)
            Assert-Equal 'the version family' $c.VersionFamily 'Windows 10'
            Assert-False 'support despite the Windows 11 product name' $c.IsSupported
            Assert-False 'the Windows 11 classification' $c.IsWindows11
            Assert-Equal 'the number of branding conflicts' @($c.BrandingConflicts).Count 1
            Assert-Match 'the conflict note' ($c.BrandingConflicts -join ' ') 'Windows 11 Pro'
        }
    },
    @{
        Name = 'Windows 10 host whose only branding claims Windows 11'
        Evidence = (Merge-Evidence $win10X64 @{
            Caption     = ''
            ProductName = 'Windows 11 Pro'
        })
        Checks = {
            param($c)
            Assert-Equal 'the version family' $c.VersionFamily 'Windows 10'
            Assert-False 'support despite the Windows 11 product name' $c.IsSupported
            Assert-Match 'the diagnostic about the missing caption' ($c.Diagnostics -join ' ') 'No CIM Caption'
        }
    },
    @{
        Name = 'pre-Windows-10 build - Windows 7 SP1'
        Evidence = (Merge-Evidence $win10X64 @{
            BuildNumber    = 7601
            CIMBuildNumber = 7601
            VersionString  = '6.1.7601'
            Caption        = 'Microsoft Windows 7 Professional'
            ProductName    = 'Windows 7 Professional'
            DisplayVersion = 'Service Pack 1'
        })
        Checks = {
            param($c)
            Assert-Equal 'the version family' $c.VersionFamily 'Older than Windows 10'
            Assert-False 'support' $c.IsSupported
            Assert-Match 'the unsupported reason' $c.UnsupportedReason 'predates Windows 10'
        }
    },
    @{
        Name = 'missing build information everywhere'
        Evidence = (Merge-Evidence $win11X64 @{
            BuildNumber    = $null
            CIMBuildNumber = $null
            VersionString  = $null
            Caption        = 'Microsoft Windows 11 Pro'
            ProductName    = 'Windows 11 Pro'
        })
        Checks = {
            param($c)
            Assert-False 'the build-known flag' $c.BuildKnown
            Assert-Equal 'the OS build number' $c.BuildNumber $null
            Assert-Equal 'the build source' $c.BuildSource 'none'
            Assert-Equal 'the version family' $c.VersionFamily 'Unknown'
            Assert-False 'support without a build number' $c.IsSupported
            Assert-Match 'the unsupported reason' $c.UnsupportedReason 'could not be determined'
            Assert-Match 'the diagnostic about the unusable build' ($c.Diagnostics -join ' ') 'No usable OS build number'
            Assert-Match 'the diagnostic about unconfirmable branding' ($c.Diagnostics -join ' ') 'no valid OS build number was available to confirm'
        }
    },
    @{
        Name = 'invalid build information'
        Evidence = (Merge-Evidence $win11X64 @{
            BuildNumber    = 'unknown-build'
            CIMBuildNumber = 'n/a'
            VersionString  = '10.0'
        })
        Checks = {
            param($c)
            Assert-False 'the build-known flag' $c.BuildKnown
            Assert-Equal 'the version family' $c.VersionFamily 'Unknown'
            Assert-Equal 'the build source' $c.BuildSource 'none'
            Assert-False 'support' $c.IsSupported
        }
    },
    @{
        Name = 'blank build information'
        Evidence = (Merge-Evidence $win11X64 @{
            BuildNumber    = '   '
            CIMBuildNumber = ''
            VersionString  = '   '
        })
        Checks = {
            param($c)
            Assert-False 'the build-known flag' $c.BuildKnown
            Assert-Equal 'the version family' $c.VersionFamily 'Unknown'
            Assert-False 'support' $c.IsSupported
        }
    },
    @{
        Name = 'zero build values are missing, not old'
        Evidence = (Merge-Evidence $win11X64 @{
            BuildNumber    = 0
            CIMBuildNumber = '0'
            VersionString  = '10.0.0'
        })
        Checks = {
            param($c)
            Assert-False 'the build-known flag' $c.BuildKnown
            Assert-Equal 'the version family' $c.VersionFamily 'Unknown'
            Assert-False 'support' $c.IsSupported
        }
    },
    @{
        Name = 'build value with a trailing component keeps its leading integer'
        Evidence = (Merge-Evidence $win11X64 @{
            BuildNumber    = '26200.0'
            CIMBuildNumber = '26200.0'
            VersionString  = '10.0.26200.0'
        })
        Checks = {
            param($c)
            Assert-True 'the build-known flag' $c.BuildKnown
            Assert-Equal 'the OS build number' $c.BuildNumber 26200
            Assert-Equal 'the version family' $c.VersionFamily 'Windows 11'
            Assert-True 'support' $c.IsSupported
        }
    },
    @{
        Name = 'version string is the only build source'
        Evidence = (Merge-Evidence $win11X64 @{
            BuildNumber    = $null
            CIMBuildNumber = $null
            VersionString  = '10.0.26200'
        })
        Checks = {
            param($c)
            Assert-True 'the build-known flag' $c.BuildKnown
            Assert-Equal 'the build source' $c.BuildSource 'version string'
            Assert-Equal 'the version family' $c.VersionFamily 'Windows 11'
            Assert-True 'support' $c.IsSupported
        }
    },
    @{
        Name = 'CIM build number is used when the registry value is unreadable'
        Evidence = (Merge-Evidence $win11X64 @{
            BuildNumber   = $null
            VersionString = '10.0.26100'
        })
        Checks = {
            param($c)
            Assert-Equal 'the build source' $c.BuildSource 'CIM BuildNumber'
            Assert-Equal 'the version family' $c.VersionFamily 'Windows 11'
            Assert-True 'support' $c.IsSupported
        }
    },
    @{
        Name = 'registry and CIM build numbers disagree'
        Evidence = (Merge-Evidence $win11X64 @{
            BuildNumber    = 26200
            CIMBuildNumber = 19045
            VersionString  = '10.0.26200'
        })
        Checks = {
            param($c)
            Assert-Equal 'the OS build number from the registry' $c.BuildNumber 26200
            Assert-Equal 'the build source' $c.BuildSource 'registry CurrentBuild'
            Assert-Equal 'the version family' $c.VersionFamily 'Windows 11'
            Assert-True 'support' $c.IsSupported
            Assert-Match 'the diagnostic about the disagreement' ($c.Diagnostics -join ' ') 'disagree'
        }
    },
    @{
        Name = 'Windows 11 ARM64'
        Evidence = (Merge-Evidence $win11X64 @{
            BuildNumber    = 22631
            CIMBuildNumber = 22631
            VersionString  = '10.0.22631'
            Architecture   = 'ARM64'
            OSArchitecture = '64-bit'
        })
        Checks = {
            param($c)
            Assert-Equal 'the version family' $c.VersionFamily 'Windows 11'
            Assert-False 'the x64 host decision' $c.IsX64OperatingSystem
            Assert-False 'the 64-bit-AMD64-process decision' $c.IsX64Process
            Assert-False 'support for an ARM64 host' $c.IsSupported
            Assert-Match 'the unsupported reason' $c.UnsupportedReason 'not x64'
        }
    },
    @{
        Name = '32-bit PowerShell on a Windows 11 x64 host (WOW64)'
        Evidence = (Merge-Evidence $win11X64 @{
            Architecture     = 'x86'
            Architecture6432 = 'AMD64'
            OSArchitecture   = '64-bit'
        })
        Checks = {
            param($c)
            Assert-False 'the 64-bit-AMD64-process decision' $c.IsX64Process
            Assert-True 'the WOW64-on-x64 decision' $c.IsWow64OnX64
            Assert-True 'the x64 host decision' $c.IsX64OperatingSystem
            Assert-True 'support for the x64 host' $c.IsSupported
            Assert-Match 'the diagnostic recommending 64-bit PowerShell' ($c.Diagnostics -join ' ') '32-bit on an x64 host'
        }
    },
    @{
        Name = '32-bit Windows 11'
        Evidence = (Merge-Evidence $win11X64 @{
            Architecture     = 'x86'
            Architecture6432 = ''
            OSArchitecture   = '32-bit'
        })
        Checks = {
            param($c)
            Assert-False 'the x64 host decision' $c.IsX64OperatingSystem
            Assert-False 'support for a 32-bit host' $c.IsSupported
            Assert-Match 'the unsupported reason' $c.UnsupportedReason 'not x64'
        }
    },
    @{
        Name = 'AMD64 process under ARM64 emulation'
        Evidence = (Merge-Evidence $win11X64 @{
            Architecture     = 'AMD64'
            Architecture6432 = 'ARM64'
            OSArchitecture   = '64-bit'
        })
        Checks = {
            param($c)
            Assert-Equal 'the version family' $c.VersionFamily 'Windows 11'
            Assert-False 'the x64 host decision' $c.IsX64OperatingSystem
            Assert-False 'support under ARM64 emulation' $c.IsSupported
            Assert-Match 'the diagnostic about emulation' ($c.Diagnostics -join ' ') 'ARM64 emulation'
        }
    },
    @{
        Name = 'Windows Server 2025 - build 26100, the same build as Windows 11 24H2'
        Evidence = (Merge-Evidence $win11X64 @{
            Caption     = 'Microsoft Windows Server 2025 Standard'
            ProductName = 'Windows Server 2025 Standard'
        })
        Checks = {
            param($c)
            Assert-Equal 'the build-derived family' $c.VersionFamilyFromBuild 'Windows 11'
            Assert-Equal 'the published version family' $c.VersionFamily 'Windows Server'
            Assert-True 'the Server SKU flag' $c.IsServerSku
            Assert-False 'the Windows 11 classification' $c.IsWindows11
            Assert-False 'support for a Server host' $c.IsSupported
            Assert-Match 'the unsupported reason' $c.UnsupportedReason 'Windows Server'
            Assert-Match 'the diagnostic about the shared build' ($c.Diagnostics -join ' ') 'not a Windows 11 client target'
        }
    },
    @{
        Name = 'Windows Server 2022 with no caption'
        Evidence = (Merge-Evidence $win11X64 @{
            BuildNumber    = 20348
            CIMBuildNumber = 20348
            VersionString  = '10.0.20348'
            Caption        = ''
            ProductName    = 'Windows Server 2022 Standard'
            DisplayVersion = '21H2'
        })
        Checks = {
            param($c)
            Assert-True 'the Server SKU flag' $c.IsServerSku
            Assert-Equal 'the published version family' $c.VersionFamily 'Windows Server'
            Assert-False 'support for a Server host' $c.IsSupported
            Assert-Match 'the diagnostic about the missing caption' ($c.Diagnostics -join ' ') 'No CIM Caption'
        }
    },
    @{
        Name = 'no CIM Caption available'
        Evidence = (Merge-Evidence $win11X64 @{
            Caption   = ''
            ProductName = 'Windows 11 Pro'
        })
        Checks = {
            param($c)
            Assert-Equal 'the version family' $c.VersionFamily 'Windows 11'
            Assert-True 'support without a caption' $c.IsSupported
            Assert-Equal 'the corroboration value' $c.BrandingCorroboratesBuild $null
            Assert-Match 'the diagnostic about the missing caption' ($c.Diagnostics -join ' ') 'No CIM Caption'
        }
    },
    @{
        Name = 'no branding evidence at all'
        Evidence = (Merge-Evidence $win11X64 @{
            Caption        = ''
            ProductName    = ''
            DisplayVersion = ''
        })
        Checks = {
            param($c)
            Assert-Equal 'the version family' $c.VersionFamily 'Windows 11'
            Assert-True 'support on the build number alone' $c.IsSupported
            Assert-Equal 'the number of diagnostics' @($c.Diagnostics).Count 2
            Assert-Equal 'the number of branding conflicts' @($c.BrandingConflicts).Count 0
        }
    }
)

# ---------------------------------------------------------------------------
# Run the cases
# ---------------------------------------------------------------------------

Write-Host ''
Write-Host 'Radanor Sparrow CE-7A - platform detection regression suite' -ForegroundColor White
Write-Host ("Module: {0}" -f (Resolve-Path $ModulePath).Path) -ForegroundColor DarkGray
Write-Host ''

$classifications = New-Object System.Collections.Generic.List[object]
foreach ($case in $cases) {
    $script:CaseCount++
    $script:CaseName = $case.Name
    Write-Host ('  {0}' -f $case.Name) -ForegroundColor White
    $classification = Classify -Evidence $case.Evidence
    $classifications.Add([pscustomobject]@{ Name = $case.Name; Evidence = $case.Evidence; Result = $classification })
    & $case.Checks $classification
}

# ---------------------------------------------------------------------------
# Invariants that must hold for every case
# ---------------------------------------------------------------------------

$script:CaseName = 'invariants - support requires a known Windows 11 build on x64'
Write-Host ('  {0}' -f $script:CaseName) -ForegroundColor White
foreach ($entry in $classifications) {
    $r = $entry.Result
    if ($r.IsSupported) {
        Assert-Equal ('family of the supported case [{0}]' -f $entry.Name) $r.VersionFamily 'Windows 11'
        Assert-True ('x64 host of the supported case [{0}]' -f $entry.Name) $r.IsX64OperatingSystem
        Assert-Equal ('unsupported reason of the supported case [{0}]' -f $entry.Name) $r.UnsupportedReason ''
    } else {
        Assert-Condition -Ok (-not [string]::IsNullOrWhiteSpace([string]$r.UnsupportedReason)) -What ('the unsupported case [{0}] explains why' -f $entry.Name)
    }
    if (-not $r.BuildKnown) {
        Assert-False ('support when the build is unknown [{0}]' -f $entry.Name) $r.IsSupported
    }
    if (-not $r.IsX64OperatingSystem) {
        Assert-False ('support on a non-x64 host [{0}]' -f $entry.Name) $r.IsSupported
    }
    if ($r.VersionFamily -eq 'Windows 10') {
        Assert-False ('support on Windows 10 [{0}]' -f $entry.Name) $r.IsSupported
    }
    if ($r.VersionFamily -eq 'Pre-release or unclassified') {
        Assert-False ('support for a pre-release or unclassified build [{0}]' -f $entry.Name) $r.IsSupported
    }
}

# ---------------------------------------------------------------------------
# Property - branding can never grant support, only restrict it
# ---------------------------------------------------------------------------

$script:CaseName = 'property - branding cannot grant support'
Write-Host ('  {0}' -f $script:CaseName) -ForegroundColor White
foreach ($entry in $classifications) {
    $stripped = Merge-Evidence $entry.Evidence @{ Caption = ''; ProductName = ''; DisplayVersion = '' }
    $blank = Classify -Evidence $stripped
    Assert-Condition -Ok ((-not $entry.Result.IsSupported) -or $blank.IsSupported) -What ('branding does not grant support [{0}]' -f $entry.Name)
    Assert-Equal ('the build number without branding [{0}]' -f $entry.Name) $blank.BuildNumber $entry.Result.BuildNumber
    Assert-Equal ('the x64 decision without branding [{0}]' -f $entry.Name) $blank.IsX64OperatingSystem $entry.Result.IsX64OperatingSystem
    if ($entry.Result.ProductNameFamily -eq 'Windows 10' -and $entry.Result.VersionFamilyFromBuild -eq 'Windows 11') {
        Assert-Condition -Ok ($entry.Result.IsSupported -and $entry.Result.LegacyProductNameBranding) -What ('legacy Windows 10 product naming does not fail a Windows 11 host [{0}]' -f $entry.Name)
    }
}

# ---------------------------------------------------------------------------
# Build parsing helpers
# ---------------------------------------------------------------------------

$script:CaseName = 'build parsing helpers'
Write-Host ('  {0}' -f $script:CaseName) -ForegroundColor White
Assert-Equal "ConvertTo-OsBuildNumber '26200'" (ConvertTo-OsBuildNumber -Value '26200') 26200
Assert-Equal 'ConvertTo-OsBuildNumber 26200 as an integer' (ConvertTo-OsBuildNumber -Value 26200) 26200
Assert-Equal "ConvertTo-OsBuildNumber ' 26200 '" (ConvertTo-OsBuildNumber -Value ' 26200 ') 26200
Assert-Equal "ConvertTo-OsBuildNumber '26200.0'" (ConvertTo-OsBuildNumber -Value '26200.0') 26200
Assert-Equal "ConvertTo-OsBuildNumber '19045'" (ConvertTo-OsBuildNumber -Value '19045') 19045
Assert-Equal "ConvertTo-OsBuildNumber '0'" (ConvertTo-OsBuildNumber -Value '0') $null
Assert-Equal "ConvertTo-OsBuildNumber '-5'" (ConvertTo-OsBuildNumber -Value '-5') $null
Assert-Equal "ConvertTo-OsBuildNumber ''" (ConvertTo-OsBuildNumber -Value '') $null
Assert-Equal 'ConvertTo-OsBuildNumber $null' (ConvertTo-OsBuildNumber -Value $null) $null
Assert-Equal "ConvertTo-OsBuildNumber 'not-a-build'" (ConvertTo-OsBuildNumber -Value 'not-a-build') $null
Assert-Equal "ConvertFrom-VersionStringBuildNumber '10.0.26200'" (ConvertFrom-VersionStringBuildNumber -VersionString '10.0.26200') 26200
Assert-Equal "ConvertFrom-VersionStringBuildNumber '10.0.26200.1234'" (ConvertFrom-VersionStringBuildNumber -VersionString '10.0.26200.1234') 26200
Assert-Equal "ConvertFrom-VersionStringBuildNumber '10.0.21999'" (ConvertFrom-VersionStringBuildNumber -VersionString '10.0.21999') 21999
Assert-Equal "ConvertFrom-VersionStringBuildNumber '10.0'" (ConvertFrom-VersionStringBuildNumber -VersionString '10.0') $null
Assert-Equal "ConvertFrom-VersionStringBuildNumber '6.8.0.138' (a Linux kernel, not a Windows build)" (ConvertFrom-VersionStringBuildNumber -VersionString '6.8.0.138') $null
Assert-Equal "ConvertFrom-VersionStringBuildNumber ''" (ConvertFrom-VersionStringBuildNumber -VersionString '') $null
Assert-Equal 'ConvertFrom-VersionStringBuildNumber $null' (ConvertFrom-VersionStringBuildNumber -VersionString $null) $null
Assert-Equal 'Get-BrandingVersionFamily Windows 11 caption' (Get-BrandingVersionFamily -Text 'Microsoft Windows 11 Home Single Language') 'Windows 11'
Assert-Equal 'Get-BrandingVersionFamily legacy Windows 10 product name' (Get-BrandingVersionFamily -Text 'Windows 10 Home Single Language') 'Windows 10'
Assert-Equal 'Get-BrandingVersionFamily Server caption' (Get-BrandingVersionFamily -Text 'Microsoft Windows Server 2025 Standard') 'Windows Server'
Assert-Equal 'Get-BrandingVersionFamily Windows 7' (Get-BrandingVersionFamily -Text 'Microsoft Windows 7 Professional') 'Unknown'
Assert-Equal 'Get-BrandingVersionFamily empty text' (Get-BrandingVersionFamily -Text '') 'Unknown'
Assert-Equal 'Get-BrandingVersionFamily $null' (Get-BrandingVersionFamily -Text $null) 'Unknown'

# ---------------------------------------------------------------------------
# Live smoke test - exercises the same facts function the harness calls
# ---------------------------------------------------------------------------

$script:CaseName = 'live facts smoke test'
Write-Host ('  {0}' -f $script:CaseName) -ForegroundColor White
$live = Get-WindowsEnvironmentFacts
Assert-Condition -Ok ($null -ne $live) -What 'Get-WindowsEnvironmentFacts returns an object on this host'
Assert-Condition -Ok ($null -ne $live.Platform) -What 'the live facts carry a platform classification'
Assert-Condition -Ok (-not [string]::IsNullOrWhiteSpace([string]$live.Platform.VersionFamily)) -What 'the live classification names a version family'
Assert-Condition -Ok ($live.Platform.Diagnostics -is [array]) -What 'the live diagnostics are an array'
Assert-Condition -Ok ($live.Platform.BrandingConflicts -is [array]) -What 'the live branding conflicts are an array'
Assert-Equal 'the live Windows 11 boundary' $live.Platform.Windows11MinimumBuild 22000
Assert-Equal 'the live Windows 10 boundary' $live.Platform.Windows10MinimumBuild 10240
Assert-Equal 'the live last Windows 10 release build' $live.Platform.Windows10MaximumBuild 19045
Assert-Condition -Ok ($live.Platform.IsSupported -eq (($live.Platform.VersionFamily -eq 'Windows 11') -and $live.Platform.IsX64OperatingSystem)) -What 'the live support decision agrees with its own family and architecture'
Assert-Condition -Ok ((-not $live.Platform.IsSupported) -or (-not $live.WindowsProductName) -or ($live.WindowsProductName -match 'Windows 11') -or $live.Platform.LegacyProductNameBranding) -What 'a supported host with non-Windows-11 registry branding has it recorded as legacy branding'

# ---------------------------------------------------------------------------
# Harness path resolution (the Windows PowerShell 5.1 defect)
#
# Windows PowerShell 5.1 evaluates a param-block default with $PSScriptRoot
# empty, which crashed this suite at its -ModulePath default and crashed
# Invoke-RadanorValidation.ps1 at its -RepoRoot default. These checks pin the
# fix against the real files: no param default may read $PSScriptRoot, the
# repository root stays two folders above the harness script, a standalone
# binary-only package resolves its module from beside the script, and the
# harness still runs end to end in both layouts.
# ---------------------------------------------------------------------------

function Get-ScriptVariableReferences {
    <#
        How many times a variable is referenced in live code. The parser's
        token stream is used rather than a text search, so mentions of the name
        inside comments do not count - both harness files document this defect
        in comments, and those mentions must stay allowed. A count is returned
        rather than a line list so that an empty result cannot be mistaken for
        a one-element array by the caller.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$VariableName
    )
    $errors = $null
    $tokens = $null
    $null = [System.Management.Automation.Language.Parser]::ParseFile($Path, [ref]$tokens, [ref]$errors)
    if ($errors -and $errors.Count -gt 0) { return $null }
    $wanted = '$' + $VariableName
    $count = 0
    foreach ($tok in $tokens) {
        if ($tok.Kind -eq [System.Management.Automation.Language.TokenKind]::Variable -or
            $tok.Kind -eq [System.Management.Automation.Language.TokenKind]::SplattedVariable) {
            if ($tok.Text -eq $wanted) { $count++ }
        }
    }
    return [int]$count
}

function Get-ParamDefaultText {
    <#
        The text of a script's param-block default for a named parameter, or
        $null when the parameter has no default or does not exist.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$ParameterName
    )
    $errors = $null
    $tokens = $null
    $ast = [System.Management.Automation.Language.Parser]::ParseFile($Path, [ref]$tokens, [ref]$errors)
    if ($errors -and $errors.Count -gt 0) { return $null }
    if (-not $ast.ParamBlock) { return $null }
    foreach ($p in $ast.ParamBlock.Parameters) {
        if ($p.Name.VariablePath.UserPath -eq $ParameterName) {
            if ($p.DefaultValue) { return $p.DefaultValue.Extent.Text }
            return $null
        }
    }
    return $null
}

function Assert-ParamDefaultAvoidsScriptRoot {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$ParameterName
    )
    $default = Get-ParamDefaultText -Path $Path -ParameterName $ParameterName
    $offending = ("$default" -match 'PSScriptRoot')
    Assert-Condition -Ok (-not $offending) `
        -What ('{0}: the {1} param default does not read $PSScriptRoot' -f (Split-Path $Path -Leaf), $ParameterName) `
        -Detail $(if ($offending) { "default was: $default" } else { '' })
}

function Assert-Throws {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string]$What,
        [Parameter(Mandatory)][scriptblock]$Action,
        [string]$MessagePattern = ''
    )
    $script:CheckCount++
    $message = ''
    $threw = $false
    try { & $Action | Out-Null } catch { $threw = $true; $message = [string]$_.Exception.Message }
    $ok = ($threw -and ((-not $MessagePattern) -or ($message -match $MessagePattern)))
    if ($ok) {
        Write-Host ('      ok   {0}' -f $What) -ForegroundColor DarkGreen
        return
    }
    Write-Host ('      FAIL {0}' -f $What) -ForegroundColor Red
    if (-not $threw) { Write-Host '           no exception was raised' -ForegroundColor DarkGray }
    elseif ($MessagePattern -and ($message -notmatch $MessagePattern)) { Write-Host ("           message was: {0}" -f $message) -ForegroundColor DarkGray }
    $script:Failures.Add(('{0} :: {1}' -f $script:CaseName, $What))
}

function ConvertTo-ChildArgument {
    [CmdletBinding()]
    param([Parameter(Mandatory)][AllowEmptyString()][string]$Value)
    if ($Value -match '\s') { return '"{0}"' -f $Value }
    return $Value
}

function Invoke-HarnessChild {
    <#
        Runs the real orchestrator in a child process of the same engine and
        returns its exit code and output. Windows PowerShell 5.1 and
        PowerShell 7+ both accept -ExecutionPolicy Bypass. The main stage used by
        the suite is 'build' with -SkipBuild, which resolves placeholders without
        starting a product process, so this stays read-only with respect to the machine.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string]$HarnessPath,
        [string[]]$Arguments = @()
    )
    $exeName = 'pwsh'
    if ($PSVersionTable.PSEdition -eq 'Desktop') { $exeName = 'powershell.exe' }
    $runRoot = Join-Path ([System.IO.Path]::GetTempPath()) ('ce7a-child-' + [System.Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $runRoot -Force | Out-Null
    try {
        $outFile = Join-Path $runRoot 'stdout.txt'
        $errFile = Join-Path $runRoot 'stderr.txt'
        # Quote an argument only when it contains whitespace: the child
        # engine receives this string verbatim on non-Windows hosts, where
        # stray quotes become part of the value.
        $argString = '-NoProfile -ExecutionPolicy Bypass -File {0}' -f (ConvertTo-ChildArgument -Value $HarnessPath)
        foreach ($a in $Arguments) { $argString += ' {0}' -f (ConvertTo-ChildArgument -Value $a) }
        $proc = Start-Process -FilePath $exeName -ArgumentList $argString -Wait -PassThru -NoNewWindow `
            -RedirectStandardOutput $outFile -RedirectStandardError $errFile
        $text = ''
        if (Test-Path $outFile) { $raw = Get-Content $outFile -Raw; if ($raw) { $text = $raw } }
        $errText = ''
        if (Test-Path $errFile) { $raw = Get-Content $errFile -Raw; if ($raw) { $errText = $raw } }
        [pscustomobject]@{
            ExitCode = $proc.ExitCode
            Output   = $text
            StdErr   = $errText
            All      = ($text + "`n" + $errText)
        }
    } finally {
        Remove-Item -LiteralPath $runRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}

$harnessScript = Join-Path $scriptRoot 'Invoke-RadanorValidation.ps1'

$script:CaseName = 'harness path resolution - static'
$script:CaseCount++
Write-Host ('  {0}' -f $script:CaseName) -ForegroundColor White
Assert-Condition -Ok (Test-Path -LiteralPath $harnessScript) -What 'Invoke-RadanorValidation.ps1 sits beside this suite'
foreach ($target in @($scriptPath, $harnessScript)) {
    $parseErrors = $null
    $parseTokens = $null
    $null = [System.Management.Automation.Language.Parser]::ParseFile($target, [ref]$parseTokens, [ref]$parseErrors)
    Assert-Equal ('{0} parses with no errors' -f (Split-Path $target -Leaf)) (@($parseErrors).Count) 0
}
Assert-ParamDefaultAvoidsScriptRoot -Path $scriptPath -ParameterName 'ModulePath'
Assert-ParamDefaultAvoidsScriptRoot -Path $harnessScript -ParameterName 'RepoRoot'
Assert-Match 'the harness resolves its own path from $PSCommandPath first' `
    (Get-Content $harnessScript -Raw) 'if \(\$PSCommandPath\)'
Assert-Match 'this suite resolves its own path from $PSCommandPath first' `
    (Get-Content $scriptPath -Raw) 'if \(\$PSCommandPath\)'

# A body-level $PSScriptRoot reference is the same defect as a param default:
# it works on PowerShell 7 and fails on Windows PowerShell 5.1. The token
# stream is scanned so that comments documenting the defect stay allowed, and
# exactly one live reference is permitted - the resolver's last-resort
# fallback, which by construction only runs after $PSCommandPath and
# $MyInvocation.MyCommand.Path have both come back empty.
foreach ($target in @($scriptPath, $harnessScript)) {
    $refCount = Get-ScriptVariableReferences -Path $target -VariableName 'PSScriptRoot'
    Assert-Equal ('{0} consults $PSScriptRoot exactly once in live code' -f (Split-Path $target -Leaf)) $refCount 1
    $fallbackLine = Get-Content $target | Where-Object { $_ -match '^if \(-not \$scriptRoot\) \{ \$scriptRoot = \$PSScriptRoot \}$' }
    Assert-Equal ('{0} keeps its only reference as the documented last resort' -f (Split-Path $target -Leaf)) (@($fallbackLine).Count) 1
}

$script:CaseName = 'harness layout resolution - repository and standalone bundle'
$script:CaseCount++
Write-Host ('  {0}' -f $script:CaseName) -ForegroundColor White
$layoutRoot = Join-Path ([System.IO.Path]::GetTempPath()) ('ce7a-layout-' + [System.Guid]::NewGuid().ToString('N'))
$repoFixture = Join-Path $layoutRoot 'repo'
$repoScriptDir = Join-Path (Join-Path $repoFixture 'validation') 'windows'
$bundleFixture = Join-Path $layoutRoot 'bundle'
$bundleBin = Join-Path $bundleFixture 'bin'
$noModuleFixture = Join-Path $layoutRoot 'nomodule'
foreach ($d in @($repoFixture, $repoScriptDir, $bundleFixture, $bundleBin, $noModuleFixture)) {
    New-Item -ItemType Directory -Path $d -Force | Out-Null
}
New-Item -ItemType File -Path (Join-Path $repoFixture 'go.mod') -Force | Out-Null
$repoScript = Join-Path $repoScriptDir 'Invoke-RadanorValidation.ps1'
$bundleScript = Join-Path $bundleFixture 'Invoke-RadanorValidation.ps1'
Copy-Item $scriptPath, $ModulePath, $harnessScript -Destination $repoScriptDir -Force
Copy-Item $scriptPath, $ModulePath, $harnessScript -Destination $bundleFixture -Force
# Deliberately NOT copying the module: this is the damaged package the last
# case runs, so the orchestrator must be present and its module missing.
Copy-Item $harnessScript -Destination $noModuleFixture -Force
New-Item -ItemType File -Path (Join-Path $bundleBin 'radanor-sparrow-core.exe') -Force | Out-Null
New-Item -ItemType File -Path (Join-Path $bundleBin 'radanor-sparrow-gateway.exe') -Force | Out-Null

$repoLayout = Resolve-ShieldHarnessLayout -ScriptPath $repoScript
Assert-Equal 'the repository root is two folders above the harness script' $repoLayout.RepoRoot $repoFixture
Assert-Equal 'the repository layout name' $repoLayout.Layout 'repository'
Assert-True 'the build stage can run in a repository layout' $repoLayout.BuildPossible
Assert-Equal 'the repository root source' $repoLayout.RepoRootSource 'two folders above the harness script (repository layout)'
Assert-Condition -Ok ($repoLayout.ModulePath -eq (Resolve-Path (Join-Path $repoScriptDir 'RadanorValidation.psm1')).Path) `
    -What 'the module beside the harness script in a repository layout'

$bundleLayout = Resolve-ShieldHarnessLayout -ScriptPath $bundleScript
Assert-Equal 'the standalone bundle layout name' $bundleLayout.Layout 'standalone bundle'
Assert-False 'the build stage cannot run in a standalone bundle' $bundleLayout.BuildPossible
Assert-Condition -Ok ($bundleLayout.ModulePath -eq (Resolve-Path (Join-Path $bundleFixture 'RadanorValidation.psm1')).Path) `
    -What 'the module beside the harness script in a standalone bundle'

$explicitLayout = Resolve-ShieldHarnessLayout -ScriptPath $bundleScript -RequestedRepoRoot $repoFixture
Assert-Equal 'an explicit repository root' $explicitLayout.RepoRoot $repoFixture
Assert-Equal 'the explicit root source' $explicitLayout.RepoRootSource 'supplied by the operator'
Assert-True 'an explicit repository root makes the build stage possible' $explicitLayout.BuildPossible

Assert-Throws 'a missing script path is refused' { Resolve-ShieldHarnessLayout -ScriptPath '' } 'could not be determined'
Assert-Throws 'a script path with no parent folder is refused' { Resolve-ShieldHarnessLayout -ScriptPath 'Invoke-RadanorValidation.ps1' } 'no parent folder'
Assert-Throws 'a missing module is refused' { Resolve-ShieldHarnessLayout -ScriptPath (Join-Path $noModuleFixture 'Invoke-RadanorValidation.ps1') } 'was not found'

$script:CaseName = 'harness end-to-end run in both layouts'
$script:CaseCount++
Write-Host ('  {0}' -f $script:CaseName) -ForegroundColor White
$workRoot = Join-Path $layoutRoot 'work'
$resultJson = Join-Path $layoutRoot 'results.json'
$run = Invoke-HarnessChild -HarnessPath $bundleScript -Arguments @(
    '-Stage', 'build', '-SkipBuild', '-BinDir', $bundleBin,
    '-WorkRoot', $workRoot, '-ResultJson', $resultJson)
Assert-Equal 'the standalone bundle run exits 0' $run.ExitCode 0
Assert-Match 'the standalone bundle is reported as the layout' $run.Output 'Layout    : standalone bundle'
$doc = $null
if (Test-Path -LiteralPath $resultJson) { $doc = Get-Content $resultJson -Raw | ConvertFrom-Json }
Assert-Condition -Ok ($null -ne $doc) -What 'the run wrote a parseable results.json'
if ($doc) {
    Assert-Equal 'the result schema' $doc.schema 'shield.ce7a.validation/v1'
    Assert-Equal 'the number of rows the prebuilt build stage records' (@($doc.results).Count) 1
    Assert-Equal 'the recorded requirement id' $doc.results[0].Id 'W-BLD-01'
    Assert-Equal 'the recorded status' $doc.results[0].Status 'PASS'
    Assert-Equal 'the recorded summary' $doc.summary.'PASS' 1
}

$explicitRun = Invoke-HarnessChild -HarnessPath $bundleScript -Arguments @(
    '-Stage', 'build', '-SkipBuild', '-BinDir', $bundleBin,
    '-RepoRoot', $repoFixture, '-WorkRoot', (Join-Path $layoutRoot 'work2'),
    '-ResultJson', (Join-Path $layoutRoot 'results2.json'))
Assert-Equal 'the explicit -RepoRoot run exits 0' $explicitRun.ExitCode 0
Assert-Condition -Ok ($explicitRun.Output -like ('*Repo root : ' + $repoFixture + '*')) `
    -What 'the harness reports exactly the repository root it was given'
Assert-Match 'the repository layout is recognised' $explicitRun.Output 'Layout    : repository'
Assert-Match 'the root is attributed to the operator' $explicitRun.Output 'supplied by the operator'

# The same run WITHOUT -RepoRoot, from a repository layout: the resolved root
# must reach the run, not just the resolver. Dropping the assignment inside the
# orchestrator would leave this printing an empty root while the resolver
# itself still answered correctly.
$defaultRootRun = Invoke-HarnessChild -HarnessPath $repoScript -Arguments @(
    '-Stage', 'build', '-SkipBuild', '-BinDir', $bundleBin,
    '-WorkRoot', (Join-Path $layoutRoot 'work3'),
    '-ResultJson', (Join-Path $layoutRoot 'results3.json'))
Assert-Equal 'the repository-layout run without -RepoRoot exits 0' $defaultRootRun.ExitCode 0
Assert-Condition -Ok ($defaultRootRun.Output -like ('*Repo root : ' + $repoFixture + '*')) `
    -What 'the resolved repository root reaches the run when -RepoRoot is omitted'
Assert-Match 'the repository layout is recognised without -RepoRoot' $defaultRootRun.Output 'Layout    : repository'
Assert-Condition -Ok ($defaultRootRun.Output -notlike '*WARNING: No Go module*') `
    -What 'no standalone-package warning is raised inside a repository'

# A bundle run WITHOUT -SkipBuild must be told why the build stage cannot work,
# rather than being left to discover it as a failure.
$warnRun = Invoke-HarnessChild -HarnessPath $bundleScript -Arguments @(
    '-Stage', 'resources', '-BinDir', $bundleBin,
    '-WorkRoot', (Join-Path $layoutRoot 'work4'),
    '-ResultJson', (Join-Path $layoutRoot 'results4.json'))
Assert-Equal 'a runtime stage with placeholder binaries exits nonzero' $warnRun.ExitCode 1
Assert-Match 'the operator is told the build stage cannot work here' $warnRun.All 'No Go module was found'
Assert-Condition -Ok ($warnRun.All -like '*-SkipBuild*') `
    -What 'the warning says how to run a standalone package'

# The build stage in a bundle has no source tree to compile from. It must
# record the EXISTING requirement id as BLOCKED - not a new id, and not a
# crash - whether or not a Go toolchain happens to be installed.
$blockedBuildRun = Invoke-HarnessChild -HarnessPath $bundleScript -Arguments @(
    '-Stage', 'build', '-BinDir', $bundleBin,
    '-WorkRoot', (Join-Path $layoutRoot 'work5'),
    '-ResultJson', (Join-Path $layoutRoot 'results5.json'))
Assert-Equal 'the bundle build run exits 0' $blockedBuildRun.ExitCode 0
$blockedDoc = $null
if (Test-Path -LiteralPath (Join-Path $layoutRoot 'results5.json')) { $blockedDoc = Get-Content (Join-Path $layoutRoot 'results5.json') -Raw | ConvertFrom-Json }
Assert-Condition -Ok ($null -ne $blockedDoc -and @($blockedDoc.results).Count -eq 1) `
    -What 'the build stage in a bundle records exactly one row'
if ($blockedDoc) {
    Assert-Equal 'the id of the blocked build row is the existing W-BLD-01' $blockedDoc.results[0].Id 'W-BLD-01'
    Assert-Equal 'the status of the blocked build row' $blockedDoc.results[0].Status 'BLOCKED'
}

$brokenRun = Invoke-HarnessChild -HarnessPath (Join-Path $noModuleFixture 'Invoke-RadanorValidation.ps1') `
    -Arguments @('-Stage', 'resources')
Assert-Condition -Ok ($brokenRun.ExitCode -ne 0) -What 'a bundle without the module exits non-zero'
Assert-Match 'the missing module is named in the failure' $brokenRun.All 'was not found'

try { Remove-Item -LiteralPath $layoutRoot -Recurse -Force -ErrorAction SilentlyContinue } catch { }

Write-Host ''
Write-Host ('Cases: {0}   Checks: {1}   Failures: {2}' -f $script:CaseCount, $script:CheckCount, $script:Failures.Count) -ForegroundColor White
if ($script:Failures.Count -gt 0) {
    Write-Host 'Failed checks:' -ForegroundColor Red
    foreach ($failure in $script:Failures) { Write-Host ('  - {0}' -f $failure) -ForegroundColor Red }
    exit 1
}
Write-Host 'All platform-detection regression checks passed.' -ForegroundColor Green
exit 0
