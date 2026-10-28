#Requires -Version 5.1
# Development build by default; -Release is the explicit public release path,
# which runs the licensing release gate and fails closed.
param([ValidateSet('amd64', 'arm64')][string]$Architecture = 'amd64', [switch]$Release)
$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
foreach ($tool in @('go', 'node', 'npm.cmd')) {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { throw "Missing build prerequisite: $tool. See docs/deployment/DESKTOP_DELIVERY.md." }
}
function Invoke-Checked([scriptblock]$Command) {
    & $Command
    if ($LASTEXITCODE -ne 0) { throw "Build command failed with exit code $LASTEXITCODE" }
}
$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$previousCGO = $env:CGO_ENABLED
$previousToolchain = $env:GOTOOLCHAIN
Push-Location $repo
try {
    $env:GOTOOLCHAIN = 'local'
    if ((& go version) -notmatch 'go([0-9]+[.][0-9]+[.][0-9]+)') { throw 'Cannot determine Go version' }
    if ([version]$Matches[1] -lt [version]'1.27.0') { throw 'Go 1.27 or newer is required' }
    if ((& node --version) -notmatch '^v24[.]') { throw 'Node.js 24 LTS is required' }
    Invoke-Checked { npm.cmd --version }
    if ($Release) {
        Write-Host 'Release build: validating licensing metadata before building.'
        Invoke-Checked { go run ./cmd/radanor-release-check }
    }
    # The product version is derived from the checked-out commit, never edited
    # into the source. A release build additionally requires an exact, clean
    # `vX.Y.Z` tag, so an approved release cannot be assembled from an arbitrary
    # working tree. This is stricter than the licensing gate, never a way
    # around it. radanor-version is the only implementation of the rule, so the
    # Windows and Linux builds cannot disagree about it.
    $versionArguments = @()
    if ($Release) { $versionArguments += '-release' }
    # The rule is read with -join rather than .Trim() so that a failure, which
    # prints nothing, throws the message below instead of a null-reference error.
    $version = ((& go run ./cmd/radanor-version @versionArguments) -join "`n").Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($version)) {
        throw 'Release blocked: a public release must be built from a clean checkout of an exact vX.Y.Z tag.'
    }
    if ($version -match '\s') { throw "The version rule printed more than one value; refusing to inject $version" }
    $variant = ((& go run ./cmd/radanor-version @versionArguments -field variant) -join "`n").Trim()
    if ([string]::IsNullOrWhiteSpace($variant) -or $variant -match '\s') { throw 'The build variant could not be determined.' }
    Write-Host "Build version: $version ($variant build)"
    $recordArguments = @('-record')
    if ($Release) { $recordArguments += '-release' }
    $identity = ((& go run ./cmd/radanor-version @recordArguments) -join "`n").Trim()
    if ($LASTEXITCODE -ne 0 -or $identity -notmatch ('(?m)^version=' + [regex]::Escape($version) + '\r?$')) { throw 'Build identity mismatch' }
    Remove-Item -LiteralPath (Join-Path $repo 'dist/windows/BUILD-INFO') -Force -ErrorAction SilentlyContinue
    Push-Location web
    try {
        Invoke-Checked { npm.cmd ci --ignore-scripts }
        Invoke-Checked { npm.cmd run build }
    } finally { Pop-Location }
    # The executable's icon and version information are PE resources. They come
    # from a COFF object generated here in pure Go, so both build paths produce
    # the same resource and the Windows build needs no Resource Compiler.
    # radanor-winres runs on the host toolchain, before GOOS is switched, and
    # copies the approved artwork byte for byte. The object goes beside the main
    # package, where the linker folds it in for a Windows target only: its name
    # ends in `_windows_<arch>`, so no other target can see it, and it is a build
    # product that the finally block removes again.
    $resource = Join-Path 'cmd/radanor-sparrow' "radanor_windows_$Architecture.syso"
    Invoke-Checked { go run ./cmd/radanor-winres -arch $Architecture -version $version -out $resource }
    $env:GOOS = 'windows'
    $env:GOARCH = $Architecture
    $env:CGO_ENABLED = '0'
    $env:GOTOOLCHAIN = 'local'
    New-Item -ItemType Directory -Force -Path dist/windows | Out-Null
    # Injected build metadata, so the executable states the commit it came from
    # and whether it was produced by the release path.
    $ldflags = "-H=windowsgui -X github.com/radanor/radanor-shield/internal/branding.Version=$version -X github.com/radanor/radanor-shield/internal/branding.Variant=$variant"
    Invoke-Checked { go build -trimpath -buildvcs=false -ldflags $ldflags -o dist/windows/RadanorSparrow.exe ./cmd/radanor-sparrow }
    # A release must distribute the license and the third-party notices beside the
    # artifact: the Business Source License requires the license to accompany copies
    # of the Licensed Work, and the MIT/BSD-3 dependencies require their notices and
    # license texts to be reproduced with the distribution. This adds two documents
    # next to the single executable; it does not change the executable itself.
    foreach ($doc in @('LICENSE', 'THIRD_PARTY_NOTICES.md')) {
        if (-not (Test-Path -LiteralPath $doc)) { throw "Missing release document: $doc" }
    }
    Copy-Item -Force -LiteralPath LICENSE -Destination dist/windows/LICENSE
    Copy-Item -Force -LiteralPath THIRD_PARTY_NOTICES.md -Destination dist/windows/THIRD_PARTY_NOTICES.md
    $checksum = (Get-FileHash -Algorithm SHA256 dist/windows/RadanorSparrow.exe).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText((Join-Path $repo 'dist/windows/BUILD-INFO'), $identity + "`nbinary.RadanorSparrow.exe=$checksum`n")
    if ($Release) {
        # Validate the package that was actually produced, not just the source.
        Invoke-Checked { go run ./cmd/radanor-release-check --release-dir dist/windows }
        Write-Host 'Release build: licensing gate passed.'
    } else {
        Write-Warning 'Development build: NOT a release artifact; the licensing release gate was not run.'
    }
    Get-FileHash -Algorithm SHA256 dist/windows/RadanorSparrow.exe, dist/windows/LICENSE, dist/windows/THIRD_PARTY_NOTICES.md
} finally {
    # Leave the checkout as it was found: the generated resource object is an
    # intermediate, not source.
    Remove-Item -Force -ErrorAction SilentlyContinue -Path (Join-Path $repo 'cmd/radanor-sparrow/radanor_windows_*.syso')
    $env:GOOS = $previousGOOS
    $env:GOARCH = $previousGOARCH
    $env:CGO_ENABLED = $previousCGO
    $env:GOTOOLCHAIN = $previousToolchain
    Pop-Location
}
