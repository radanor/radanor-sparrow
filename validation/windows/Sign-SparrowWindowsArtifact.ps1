# Release-only Windows certificate-store signer. No key import/export or downloads.
param(
    [Parameter(Mandatory=$true)][string]$Path,
    [Parameter(Mandatory=$true)][ValidateSet('Portable','Uninstaller','Setup')][string]$Kind,
    [string]$PortableDirectory = $env:RADANOR_SIGNING_PORTABLE_DIR
)
$ErrorActionPreference = 'Stop'
# PowerShell 5.1 uses .NET Framework; hosted pwsh uses the .NET reference pack.
$signingReferences = @('System.Security', 'System.Core')
if ($PSVersionTable.PSEdition -eq 'Core') {
    $signingReferences = @((Get-ChildItem (Join-Path $PSHOME 'ref') -Filter '*.dll').FullName)
    $pkcs = [Security.Cryptography.Pkcs.SignedCms].Assembly.Location
    $signingReferences = @($signingReferences | Where-Object { [IO.Path]::GetFileName($_) -ne [IO.Path]::GetFileName($pkcs) }) + $pkcs
}
if ($env:RADANOR_WINDOWS_SIGNING -cne 'enabled') { throw 'Windows production signing is not enabled' }
$recordPath = Join-Path $PortableDirectory 'BUILD-INFO'
$record = [IO.File]::ReadAllText($recordPath)
if (@([regex]::Matches($record, '(?m)^variant=release\r?$')).Count -ne 1 -or
    @([regex]::Matches($record, '(?m)^variant=')).Count -ne 1) { throw 'Only release BUILD-INFO may enter production signing' }
$portable = Join-Path $PortableDirectory 'RadanorSparrow.exe'
$hashField = [regex]::Matches($record, '(?m)^binary\.RadanorSparrow\.exe=([a-f0-9]{64})\r?$')
if ($hashField.Count -ne 1 -or @([regex]::Matches($record, '(?m)^binary\.RadanorSparrow\.exe=')).Count -ne 1 -or
    (Get-FileHash -LiteralPath $portable -Algorithm SHA256).Hash -ine $hashField[0].Groups[1].Value) { throw 'Portable BUILD-INFO checksum mismatch' }
foreach ($thumb in @($env:RADANOR_AUTHENTICODE_THUMBPRINT, $env:RADANOR_TIMESTAMP_THUMBPRINT)) {
    if ($thumb -notmatch '^[0-9A-Fa-f]{40}$') { throw 'Approved code signer and timestamp certificate thumbprints are required' }
}
$tsa = [uri]$env:RADANOR_TIMESTAMP_URL
if ($env:RADANOR_TIMESTAMP_POLICY_OID -notmatch '^[0-2](\.[0-9]+){2,}$') { throw 'Approved RFC3161 timestamp policy OID required' }
if (-not $tsa.IsAbsoluteUri -or $tsa.Scheme -cne 'https' -or $tsa.UserInfo -or $tsa.Fragment) { throw 'An approved HTTPS RFC3161 timestamp URL is required' }
if (-not [IO.Path]::IsPathRooted($env:RADANOR_SIGNTOOL) -or $env:RADANOR_SIGNTOOL_SHA256 -notmatch '^[0-9a-fA-F]{64}$') { throw 'Approved absolute SDK SignTool path and SHA256 required' }
$tool = (Resolve-Path -LiteralPath $env:RADANOR_SIGNTOOL).Path
if ((Get-FileHash -LiteralPath $tool -Algorithm SHA256).Hash -ine $env:RADANOR_SIGNTOOL_SHA256) { throw 'SignTool checksum mismatch' }
$verify = Join-Path $PSScriptRoot 'Assert-SparrowAuthenticode.ps1'
if ($Kind -ne 'Portable') { & $verify -Path $portable -ExpectedSignerThumbprint $env:RADANOR_AUTHENTICODE_THUMBPRINT }
if ($Kind -eq 'Setup') {
    & (Join-Path $PSScriptRoot 'Assert-SparrowInstallerSigning.ps1') -Setup $Path -PortableDirectory $PortableDirectory -SignedUninstaller $env:RADANOR_SIGNED_UNINSTALLER -BeforeSetupSigning
}
$resolved = (Resolve-Path -LiteralPath $Path).Path
if ($Kind -eq 'Portable' -and $resolved -ine (Resolve-Path -LiteralPath $portable).Path) { throw 'Portable path mismatch' }
if (-not ('SparrowAuthenticode' -as [type])) { Add-Type -Path (Join-Path $PSScriptRoot 'SparrowAuthenticode.cs') -ReferencedAssemblies $signingReferences }
$covered = [SparrowAuthenticode]::PayloadHash($resolved)
$staged = Join-Path (Split-Path -Parent $resolved) (([guid]::NewGuid().ToString('N')) + '.signing.exe')
try {
    Copy-Item -LiteralPath $resolved -Destination $staged
    # CurrentUser\My only; no /a certificate auto-selection, PFX, passwords or keys.
    & $tool sign /s My /sha1 $env:RADANOR_AUTHENTICODE_THUMBPRINT /fd SHA256 /tr $tsa.AbsoluteUri /td SHA256 $staged
    if ($LASTEXITCODE -ne 0) { throw "Signing failed (including warnings): exit $LASTEXITCODE" }
    & $verify -Path $staged -ExpectedSignerThumbprint $env:RADANOR_AUTHENTICODE_THUMBPRINT
    if ([SparrowAuthenticode]::PayloadHash($staged) -cne $covered) { throw 'Signing changed application payload bytes' }
    [IO.File]::Replace($staged, $resolved, $null)
    if ($Kind -eq 'Portable') {
        # Only the whole-file checksum changes, AFTER signature/trust verification.
        # The source-derived version, variant, numeric, commit and sequence remain.
        $signedHash = (Get-FileHash -LiteralPath $resolved -Algorithm SHA256).Hash.ToLowerInvariant()
        $position = $hashField[0].Groups[1].Index
        $updated = $record.Substring(0, $position) + $signedHash + $record.Substring($position + 64)
        $next = $recordPath + '.' + [guid]::NewGuid().ToString('N')
        try { [IO.File]::WriteAllText($next, $updated); [IO.File]::Replace($next, $recordPath, $null) }
        finally { if (Test-Path -LiteralPath $next) { Remove-Item -LiteralPath $next } }
    }
    if ($Kind -eq 'Uninstaller') {
        if (-not [IO.Path]::IsPathRooted($env:RADANOR_SIGNED_UNINSTALLER) -or (Test-Path -LiteralPath $env:RADANOR_SIGNED_UNINSTALLER)) { throw 'A fresh absolute uninstaller evidence path is required' }
        Copy-Item -LiteralPath $resolved -Destination $env:RADANOR_SIGNED_UNINSTALLER
    }
} finally { if (Test-Path -LiteralPath $staged) { Remove-Item -LiteralPath $staged } }
