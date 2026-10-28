# P7.2 isolated Windows validation environment

Use an interactive, non-administrator Windows 11 terminal. Stop existing Sparrow
instances through their own Exit action before executable/IPC tests. This setup
does not change machine PATH, execution policy, production keyrings, IPC ACLs or
the P6 signing scripts. Downloaded tools remain under ignored `dist/p72-tools`.

## Tools measured on this host

- MSYS2 official base `2026-09-27`, archive
  `msys2-base-x86_64-20260927.tar.xz`, SHA-256
  `ea2f31a0b6ade63914ce441ffb022f0f6aa96982bfefa2326460a26d5fb01322`.
  The downloaded file was compared with the official GitHub release asset digest
  before extraction. [Official release](https://github.com/msys2/msys2-installer/releases/tag/2026-09-27).
- Signed pacman packages `mingw-w64-ucrt-x86_64-gcc` **16.2.0-6** and
  `mingw-w64-ucrt-x86_64-gnupg` **2.4.9-1**, installed through the workspace-local
  package manager. GCC reports **x86_64-w64-mingw32**. No signature checks were
  disabled. Initial public-mirror synchronization failed; the required `msys` and
  `ucrt64` repositories were retried against the official `repo.msys2.org` server.
- Successful signing tests use the existing **Git for Windows MSYS GnuPG** via
  `gpg-msys`, not the UCRT GnuPG. The native UCRT package failed to establish its
  agent on this host. The adapter does real path conversion only and forwards
  stdin/stdout/stderr and the actual GnuPG exit status unchanged. It requires an
  explicit `GNUPGHOME`; there is no fallback to a default user keyring.

MSYS2 provisioning reference: [official installer options](https://www.msys2.org/docs/installer/),
[UCRT64 GCC](https://www.mingw-w64.org/getting-started/msys2/).

## Reproducing the local setup

Extract the verified archive under `dist/p72-tools` so its root is
`dist/p72-tools/msys64`. Initialize the shell with `MSYSTEM=UCRT64`,
`HOME=/home/p72`, and `CHERE_INVOKING=1`, then update and install the packages with
signed pacman. An official-server-only local configuration used here was:

```ini
# dist/p72-tools/msys64/etc/pacman-p72.conf
[options]
Architecture = auto
SigLevel = Required DatabaseOptional
LocalFileSigLevel = Optional
ParallelDownloads = 4
[msys]
Server = https://repo.msys2.org/msys/$arch
[ucrt64]
Server = https://repo.msys2.org/mingw/ucrt64
```

```powershell
$env:MSYSTEM = 'UCRT64'
$env:HOME = '/home/p72'
$env:CHERE_INVOKING = '1'
& ./dist/p72-tools/msys64/usr/bin/bash.exe --login -c 'pacman --config /etc/pacman-p72.conf -Syu --noconfirm'
# If the core runtime update closes the shell, start a fresh invocation.
& ./dist/p72-tools/msys64/usr/bin/bash.exe --login -c 'pacman --config /etc/pacman-p72.conf -Syu --noconfirm mingw-w64-ucrt-x86_64-gcc mingw-w64-ucrt-x86_64-gnupg'
```

In **this isolated runtime's** `etc/fstab`, change the cygdrive option `noacl` to
`acl`. This enables, rather than disables, NTFS ACL handling. Its default noacl
mounts caused GNU `mkdir -m 700` in the unchanged signing scripts to fail even
though separate mkdir/chmod succeeded. Do not change Git's installed mount
configuration or relax the script's private-directory requirement.

Build the test path adapter as a normal CGO-free Windows tool:

```powershell
$env:CGO_ENABLED = '0'
go build -o dist/p72-tools/gpg-bridge/gpg.exe ./validation/windows/gpg-msys
```

MSYS GnuPG cannot use Windows backslash `GNUPGHOME` values directly. The adapter
converts absolute drive/UNC paths to MSYS syntax, leaving operation flags, UIDs,
fingerprints and payload bytes untouched. Its path-conversion regression test
runs in the normal/race suite. This is a test-runner adapter, not a supported
production release-signing entry point.

## Terminal-local test session

MSYS agent socket names also exceed their usable length in several default Go
test temporary homes. Use an unused short drive alias for a new OS temp directory
**outside the checkout**. A temporary root below the checkout makes version tests
which deliberately require an outside-Git directory invalid; that failed setup
was detected and corrected without changing a version rule or test assertion.

Run from the repository root in a dedicated terminal:

```powershell
$ErrorActionPreference = 'Stop'
if (Test-Path 'P:\') { throw 'P: is already in use; choose another unused letter and update TMPDIR too' }
$root = Join-Path $PWD 'dist/p72-tools'
$shortTemp = Join-Path ([IO.Path]::GetTempPath()) ('radanor-p72-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $shortTemp | Out-Null
subst.exe P: $shortTemp
if ($LASTEXITCODE -ne 0) { throw 'Temporary drive mapping failed' }
try {
    $env:RADANOR_TEST_MSYS_GPG = 'C:\Program Files\Git\usr\bin\gpg.exe'
    $env:Path = (Join-Path $root 'gpg-bridge') + ';' +
        (Join-Path $root 'msys64/ucrt64/bin') + ';' +
        (Join-Path $root 'msys64/usr/bin') + ';' + $env:Path
    $env:TEMP = 'P:\'
    $env:TMP = 'P:\'
    $env:TMPDIR = '/p'
    $env:CC = Join-Path $root 'msys64/ucrt64/bin/gcc.exe'
    $env:CGO_ENABLED = '1'
    $env:RADANOR_SPARROW_TEST_BINARY = Join-Path $PWD 'dist/windows/RadanorSparrow.exe'
    $env:RADANOR_SPARROW_TEST_WEBVIEW = '1'
    & $env:CC -dumpmachine
    go test ./... -count=1
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
    go test -race ./... -count=1
    if ($LASTEXITCODE -ne 0) { throw 'Race tests failed' }
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Vet failed' }
} finally {
    subst.exe P: /d
    # Remove only our own empty root. Retain unexpected contents for inspection.
    if ((Get-ChildItem -LiteralPath $shortTemp -Force | Measure-Object).Count -eq 0) {
        Remove-Item -LiteralPath $shortTemp
    }
}
```

Close this dedicated terminal afterward so its environment overrides cannot be
mistaken for system configuration. The adapter and runtime binaries are ignored
local tools and must not be staged or attached to a release. Tests generate only
fixture keys in temporary homes and exercise the unmodified P6 signing scripts;
no production key, trusted fingerprint or publication action is configured.

To audit the five previously skipped tests specifically, run within the same
configured session:

```powershell
go test ./internal/releasecheck -run '^(TestSigningMechanismWorksWithAProvidedKey|TestVerificationRejectsASignatureFromAnotherKey|TestPartialSignatureCoverageIsRejected|TestSigningRefusesADevelopmentBuildWithoutTheDryRunFlag|TestSigningRefusesAKeyThatIsNotTheTrustedFingerprint)$' -count=1 -v
```

Require **five PASS results and zero SKIP results**. A zero package exit status
with skipped key generation is not sufficient evidence.
