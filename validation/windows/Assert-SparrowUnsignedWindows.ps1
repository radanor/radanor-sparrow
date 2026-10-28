# Absence of a signature is mandatory for explicitly unsigned Community artifacts.
# This verifier never signs, creates/imports a certificate or authorizes a release.
[CmdletBinding(DefaultParameterSetName='Artifacts')]
param(
    [Parameter(Mandatory=$true,ParameterSetName='Artifacts')][string[]]$Path,
    [Parameter(Mandatory=$true,ParameterSetName='Installer')][string]$Setup,
    [Parameter(Mandatory=$true,ParameterSetName='Installer')][string]$PortableDirectory,
    [Parameter(Mandatory=$true,ParameterSetName='Installer')][string]$SevenZip
)
$ErrorActionPreference = 'Stop'
foreach ($module in @('Microsoft.PowerShell.Security','Microsoft.PowerShell.Utility')) {
    Import-Module -Name (Join-Path $PSHOME "Modules/$module/$module.psd1") -ErrorAction Stop
}
function Assert-UnsignedPE([string]$Candidate) {
    $resolved = (Resolve-Path -LiteralPath $Candidate).Path
    $bytes = [IO.File]::ReadAllBytes($resolved)
    if ($bytes.Length -lt 256 -or $bytes[0] -ne 77 -or $bytes[1] -ne 90) { throw 'Unsigned verification requires a PE executable' }
    $pe = [BitConverter]::ToInt32($bytes,60)
    if ($pe -lt 64 -or $pe -gt $bytes.Length-160 -or [BitConverter]::ToUInt32($bytes,$pe) -ne 17744) { throw 'Invalid PE header' }
    $optional = $pe+24; $magic = [BitConverter]::ToUInt16($bytes,$optional)
    if ($magic -ne 267 -and $magic -ne 523) { throw 'Unsupported PE optional header' }
    $directory = $optional + $(if ($magic -eq 523) {112} else {96}) + 32
    if ($directory+8 -gt $bytes.Length) { throw 'Invalid PE security directory' }
    # Get-AuthenticodeSignature alone can report NotSigned on unrecognized data.
    # Also reject any certificate-table offset/size, including malformed evidence.
    if ([BitConverter]::ToUInt32($bytes,$directory) -ne 0 -or [BitConverter]::ToUInt32($bytes,$directory+4) -ne 0) {
        throw "Unexpected PE signature table: $resolved"
    }
    $signature = Get-AuthenticodeSignature -LiteralPath $resolved
    if ($signature.Status -ne 'NotSigned' -or $null -ne $signature.SignerCertificate -or $null -ne $signature.TimeStamperCertificate) {
        throw "Artifact is not unsigned: $resolved status=$($signature.Status)"
    }
    Write-Host "Unsigned PE verified: $resolved NotSigned; no certificate table or timestamp"
}
if ($PSCmdlet.ParameterSetName -eq 'Artifacts') {
    foreach ($candidate in $Path) { Assert-UnsignedPE $candidate }
    return
}
Assert-UnsignedPE $Setup
$before = (Get-FileHash -LiteralPath $Setup -Algorithm SHA256).Hash
Assert-UnsignedPE (Join-Path $PortableDirectory 'RadanorSparrow.exe')
if (-not [IO.Path]::IsPathRooted($SevenZip)) { throw 'Explicit verified 7-Zip executable required' }
$temp = Join-Path ([IO.Path]::GetTempPath()) ('sparrow-unsigned-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $temp | Out-Null
try {
    & $SevenZip x -y ('-o'+$temp) $Setup
    if ($LASTEXITCODE -ne 0) { throw "Unsigned installer extraction failed: exit $LASTEXITCODE" }
    $uninstallers = @(Get-ChildItem -LiteralPath $temp -Recurse -File -Filter Uninstall.exe)
    if ($uninstallers.Count -ne 1) { throw 'Exactly one embedded uninstaller required' }
    Assert-UnsignedPE $uninstallers[0].FullName
    foreach ($name in @('RadanorSparrow.exe','BUILD-INFO','LICENSE','THIRD_PARTY_NOTICES.md')) {
        $files = @(Get-ChildItem -LiteralPath $temp -Recurse -File -Filter $name)
        if ($files.Count -ne 1 -or (Get-FileHash -LiteralPath $files[0].FullName -Algorithm SHA256).Hash -cne
            (Get-FileHash -LiteralPath (Join-Path $PortableDirectory $name) -Algorithm SHA256).Hash) { throw "Unsigned installer payload mismatch: $name" }
    }
    $record = [IO.File]::ReadAllText((Join-Path $PortableDirectory 'BUILD-INFO'))
    $hashes = [regex]::Matches($record,'(?m)^binary\.RadanorSparrow\.exe=([a-f0-9]{64})\r?$')
    if ($hashes.Count -ne 1 -or @([regex]::Matches($record,'(?m)^binary\.RadanorSparrow\.exe=')).Count -ne 1 -or
        $hashes[0].Groups[1].Value -ine (Get-FileHash -LiteralPath (Join-Path $PortableDirectory 'RadanorSparrow.exe') -Algorithm SHA256).Hash) {
        throw 'Unsigned portable BUILD-INFO checksum mismatch'
    }
    if ((Get-FileHash -LiteralPath $Setup -Algorithm SHA256).Hash -cne $before) { throw 'Setup changed during unsigned verification' }
    Write-Host 'Unsigned setup, embedded uninstaller and exact portable payload verified'
} finally { Remove-Item -LiteralPath $temp -Recurse -Force }
