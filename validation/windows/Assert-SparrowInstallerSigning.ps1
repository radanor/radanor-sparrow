# Verify the real reconstructed WriteUninstaller payload, not just an audit copy.
param([Parameter(Mandatory=$true)][string]$Setup,
      [Parameter(Mandatory=$true)][string]$PortableDirectory,
      [Parameter(Mandatory=$true)][string]$SignedUninstaller,
      [string]$SevenZip = $env:RADANOR_7ZIP,
      [switch]$BeforeSetupSigning)
$ErrorActionPreference = 'Stop'
$verify = Join-Path $PSScriptRoot 'Assert-SparrowAuthenticode.ps1'
if (-not $BeforeSetupSigning) { & $verify -Path $Setup -ExpectedSignerThumbprint $env:RADANOR_AUTHENTICODE_THUMBPRINT }
if (-not [IO.Path]::IsPathRooted($SevenZip)) { throw 'An explicit verified 7-Zip executable path is required' }
$temp = Join-Path ([IO.Path]::GetTempPath()) ('sparrow-signature-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $temp | Out-Null
try {
    & $SevenZip x -y ('-o' + $temp) $Setup
    if ($LASTEXITCODE -ne 0) { throw "Installer extraction failed: exit $LASTEXITCODE" }
    $uninstallers = @(Get-ChildItem -LiteralPath $temp -Recurse -File -Filter Uninstall.exe)
    if ($uninstallers.Count -ne 1) { throw 'Exactly one embedded uninstaller is required' }
    if ((Get-FileHash -LiteralPath $uninstallers[0].FullName -Algorithm SHA256).Hash -cne
        (Get-FileHash -LiteralPath $SignedUninstaller -Algorithm SHA256).Hash) { throw 'Embedded uninstaller differs from the verified signed uninstaller' }
    & $verify -Path $uninstallers[0].FullName -ExpectedSignerThumbprint $env:RADANOR_AUTHENTICODE_THUMBPRINT
    foreach ($name in @('RadanorSparrow.exe', 'BUILD-INFO', 'LICENSE', 'THIRD_PARTY_NOTICES.md')) {
        $files = @(Get-ChildItem -LiteralPath $temp -Recurse -File -Filter $name)
        if ($files.Count -ne 1 -or (Get-FileHash -LiteralPath $files[0].FullName -Algorithm SHA256).Hash -cne
            (Get-FileHash -LiteralPath (Join-Path $PortableDirectory $name) -Algorithm SHA256).Hash) { throw "Installer payload mismatch: $name" }
    }
    Write-Host "Embedded signed uninstaller and exact portable payload verified: $Setup"
} finally {
    # Only the fresh directory created above; never an installation/profile path.
    Remove-Item -LiteralPath $temp -Recurse -Force
}
