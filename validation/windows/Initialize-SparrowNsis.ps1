# Provision the exact reviewed compiler, independently of cached sync databases.
# No application/release settings are changed. NSIS 3.13-1 has a plugin-path bug;
# checking only upstream /VERSION cannot distinguish it from fixed 3.13-2.
param(
    [Parameter(Mandatory=$true)][string]$MsysRoot,
    [Parameter(Mandatory=$true)][string]$CacheDirectory,
    [switch]$VerifyOnly
)
$ErrorActionPreference = 'Stop'
$package = 'mingw-w64-ucrt-x86_64-nsis'
$packageVersion = '3.13-2'
$expectedCompilerVersion = 'v3.13'
$archive = "$package-$packageVersion-any.pkg.tar.zst"
$expectedSHA256 = '262c2453ee69924e4995a6f3c685edb96e57e57e749d38f8f89732be6ffccff7'
$baseURI = 'https://repo.msys2.org/mingw/ucrt64'

function Get-SparrowSHA256 {
    param([Parameter(Mandatory=$true)][string]$LiteralPath)

    # Get-FileHash is supplied by Microsoft.PowerShell.Utility and is not
    # available in every PowerShell environment used to exercise this script.
    # Use the runtime cryptography implementation directly so verification does
    # not depend on module discovery or auto-loading.
    $stream = [IO.File]::OpenRead($LiteralPath)
    $sha256 = [Security.Cryptography.SHA256]::Create()
    try {
        $digest = $sha256.ComputeHash($stream)
        return ([BitConverter]::ToString($digest)).Replace('-', '').ToLowerInvariant()
    } finally {
        $sha256.Dispose()
        $stream.Dispose()
    }
}

$root = (Resolve-Path -LiteralPath $MsysRoot).Path
$pacman = Join-Path $root 'usr/bin/pacman.exe'
$compiler = Join-Path $root 'ucrt64/bin/makensis.exe'
Write-Host "NSIS expected executable: $compiler"
Write-Host "NSIS required package: $package $packageVersion"
New-Item -ItemType Directory -Force -Path $CacheDirectory | Out-Null
$cache = (Resolve-Path -LiteralPath $CacheDirectory).Path
$config = Join-Path $cache 'pacman-nsis-required-signature.conf'
[IO.File]::WriteAllText($config, "[options]`nArchitecture = auto`nSigLevel = Required`nLocalFileSigLevel = Required`nRemoteFileSigLevel = Required`n")

if (-not $VerifyOnly) {
    $payload = Join-Path $cache $archive
    if (-not (Test-Path -LiteralPath $payload)) {
        Invoke-WebRequest -UseBasicParsing -Uri "$baseURI/$archive" -OutFile "$payload.partial"
        Move-Item -LiteralPath "$payload.partial" -Destination $payload -Force
    }
    $actualSHA256 = Get-SparrowSHA256 -LiteralPath $payload
    Write-Host "NSIS package URL: $baseURI/$archive"
    Write-Host "NSIS package SHA256: $actualSHA256"
    if ($actualSHA256 -ne $expectedSHA256) { throw 'NSIS package checksum mismatch; refusing installation' }
    # Require a trusted MSYS2 signature as well as the repository-pinned digest.
    Invoke-WebRequest -UseBasicParsing -Uri "$baseURI/$archive.sig" -OutFile "$payload.sig"
    & $pacman --config $config --noconfirm -U $payload
    if ($LASTEXITCODE -ne 0) { throw "Verified NSIS package installation failed: exit $LASTEXITCODE" }
}

$compiler = (Resolve-Path -LiteralPath $compiler).Path
$detected = ((& $compiler /VERSION 2>&1) -join "`n").Trim()
$compilerExit = $LASTEXITCODE
$installed = ((& $pacman --config $config -Q $package 2>&1) -join "`n").Trim()
$queryExit = $LASTEXITCODE
Write-Host "NSIS executable: $compiler"
Write-Host "NSIS /VERSION: [$detected] (exit $compilerExit)"
Write-Host "NSIS installed package: [$installed] (exit $queryExit)"
Write-Host "NSIS executable SHA256: $(Get-SparrowSHA256 -LiteralPath $compiler)"
# Pacman's ownership database uses paths relative to this MSYS2 root; a native
# C:\... spelling is not recognized by -Qo even though -U accepts native paths.
& $pacman --config $config -Qo /ucrt64/bin/makensis.exe
if ($LASTEXITCODE -ne 0) { throw "NSIS executable has no verified package owner: $compiler" }
if ($queryExit -ne 0 -or $installed -cne "$package $packageVersion") {
    throw "NSIS package mismatch: expected [$package $packageVersion], detected [$installed], executable [$compiler]"
}
if ($compilerExit -ne 0 -or $detected -cne $expectedCompilerVersion) {
    throw "NSIS compiler mismatch: expected [$expectedCompilerVersion], detected [$detected], executable [$compiler], exit $compilerExit"
}
$env:MAKENSIS = $compiler
