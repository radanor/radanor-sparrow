# Windows per-user installer

P8 development packaging reuses the reviewed historical NSIS implementation.
Read the private engineering record before
distribution. A development setup is not an authorized public release.

## Build

Use Windows 11 x64, Go 1.27, Node 24, Git for Windows Bash, reviewed Unicode
NSIS **3.13** and 7-Zip. Use a full-history checkout: `BUILD-INFO` and the embedded
ancestry use real Git commits, not a manually assigned release number. Keep tool
settings terminal-local; the shipped executable remains CGO-free.

```powershell
./scripts/build-windows.ps1
$tools = Join-Path $PWD 'dist/p72-tools/msys64/ucrt64/bin'
$env:Path = "$tools;C:\Program Files\Git\bin;" + $env:Path
$env:MAKENSIS = Join-Path $tools 'makensis.exe'
$env:RADANOR_7ZIP = Join-Path $tools '7z.exe'
./validation/windows/Initialize-SparrowNsis.ps1 -MsysRoot (Join-Path $PWD 'dist/p72-tools/msys64') -CacheDirectory (Join-Path $PWD 'dist/nsis-toolchain')
bash scripts/build-windows-installer.sh
if ($LASTEXITCODE -ne 0) { throw 'Installer build failed' }
$env:RADANOR_PORTABLE_EXE = (Resolve-Path dist/windows/RadanorSparrow.exe).Path
$env:RADANOR_SETUP_EXE = (Get-ChildItem dist/windows/*-setup.exe | Select-Object -First 1).FullName
$env:RADANOR_INSTALLER_PORTABLE_B = (Resolve-Path dist/windows).Path
$env:RADANOR_HEADLESS_INSTALLER = '1'
go test ./internal/wininstaller -count=1 -v
```

The local tool directory is an example, not a required installation location.
On this host signed MSYS2 packages `mingw-w64-ucrt-x86_64-nsis` **3.13-2** and
`mingw-w64-ucrt-x86_64-7zip` **26.05-1** were installed into the existing isolated
P7.2 runtime with signed pacman. See
[P7.2 environment instructions](../../validation/windows/P72_TEST_ENVIRONMENT.md)
for that runtime's provenance and terminal-local GCC/GnuPG setup. No signature
checks, Windows protection settings or global PATH changes are required.

CI installs the exact NSIS **3.13-2** archive from the official MSYS2 repository,
not the version selected by an image's cached package database. The initializer
requires the checked-in package SHA-256 and a trusted MSYS2 package signature,
then checks package revision, ownership and the explicit compiler's `/VERSION`.
Revision 3.13-1 is not accepted: it has a known plugin-directory regression
affecting MUI2/nsDialogs. Path, raw version, exit status, package revision and
checksums are printed before version refusal. See the
the private engineering record for the failed
Actions evidence and verified package provenance. `-VerifyOnly` inspects an
already provisioned runtime; it is not used to skip installation in CI.

Run local PowerShell files only through your organization's supported script
runner. The measured host blocks direct `.ps1` loading. Acceptance evaluated the
reviewed build body through PowerShell `-Command`, binding its script directory
explicitly; it did not change execution policy. Hosted CI uses its normal `pwsh`
runner. An official NSIS archive download that failed its expected checksum was
rejected and never executed; signed pacman supplied the measured compiler.

The portable build captures version/variant/numeric version/commit/commit count
before frontend generation, then writes its executable SHA-256 to `BUILD-INFO`.
Packaging verifies that record against the actual PE version, architecture,
subsystem, checksum, source commit and sequence. It copies the executable,
`LICENSE`, `THIRD_PARTY_NOTICES.md` and `BUILD-INFO` without modifying them. The
approved `web/public/favicon.ico` supplies setup/uninstaller icons. The NSIS zlib
stub/plugins notice is appended to the existing notices; current licensing text
and unresolved legal identity remain intact.

Use `--portable-dir DIR --source-dir CHECKOUT` only for development acceptance
with another real checkout, such as the older A build. Public packaging requires
the normal source tree and both existing licensing and exact clean-tag gates:
`build-windows.ps1 -Release`, then `build-windows-installer.sh --release`. These
remain blocked. Do not relabel a development record as a release.

### Release mode in the release workflow

`.github/workflows/release.yml` builds the public Windows set itself, in a fixed
order that `internal/releasecheck` asserts rather than documents:

1. `build-windows.ps1 -Release` — portable executable, `BUILD-INFO` recording
   `variant=release`, licensing gate passed;
2. `Sign-SparrowWindowsArtifact.ps1 -Kind Portable` signs and verifies portable
   using the configured certificate-store provider and RFC3161 policy, preserving
   covered payload bytes and updating only the binary checksum in `BUILD-INFO`;
3. pinned NSIS 3.13-2 and 7-Zip from signed MSYS2 packages
   (`Initialize-SparrowNsis.ps1`), then `build-windows-installer.sh --release`. The
   installer therefore wraps those exact signed bytes and re-runs the licensing
   gate itself. Release-only `!uninstfinalize ... = 0` signs and verifies the
   generated uninstaller before embedding; a failed hook aborts compilation;
4. payload, resource and version checks through `go test ./internal/wininstaller`,
   with the counts asserted: exactly one setup must exist, and each of the three
   artifact tests must report an actual `pass` event, because every one of them
   skips itself when it has no artifact;
5. Setup signing first reconstructs and verifies the signed embedded uninstaller
   and exact portable payload. Final Authenticode verification and extraction are
   repeated after signing, then
   `scripts/write-windows-release-checksums.sh`, which refuses anything whose
   `BUILD-INFO` is not `variant=release` and writes `SHA256SUMS.windows` over the
   five published files;
6. upload of the portable executable, the setup, `SHA256SUMS.windows`, `BUILD-INFO`,
   `LICENSE` and `THIRD_PARTY_NOTICES.md` as one artifact.

Until `RADANOR_WINDOWS_SIGNING=enabled` and `RADANOR_AUTHENTICODE_THUMBPRINT` are
set on the `production-release` environment, step 2 fails and nothing from the
Windows matrix is uploaded. That is the intended state while no code-signing
identity exists: the workflow produces no Windows release artifact at all.

The full signer/TSA/digest/revocation/tool-hash policy and remaining enablement
requirements are in [RELEASE_SIGNING.md](RELEASE_SIGNING.md#10-windows-authenticode-profile-p102).
Development installers remain unsigned and are never relabeled as releases.

## Installation and recovery

Setup requests a normal user token, installs by default into
`%LOCALAPPDATA%\Programs\Radanor Sparrow`, creates a current-user Start Menu
shortcut and registers uninstall in HKCU. It creates no service, scheduled task,
autostart entry, driver or desktop shortcut. The product name is **Radanor Sparrow
Community Edition**; Radanor is brand metadata, not an invented legal publisher.

WebView2 Evergreen enables the native desktop window. The existing executable
already embeds its loader and monitoring core; no second core binary is needed.
If Evergreen is unavailable, the existing application opens its authenticated
dashboard in the default browser. Setup explains this policy and downloads
nothing. Runtime bootstrapper installation is deliberately absent.

Use **Exit Radanor Sparrow** before setup or uninstall; X only hides its window.
Setup retains compatible desktop/core/gateway mutex handles for its lifetime,
closing the old probe-to-replacement race. A running or inaccessible lock refuses
the operation; no process is killed. A separate installer mutex serializes setup
and uninstall. These use existing current-session production lock names without
changing application IPC, authentication or private-directory protections.

Every install, upgrade or repair writes a new immutable `versions/<unique>`
generation. All files and the uninstaller are completed before activation. Only
then are HKCU targets and the Start Menu link updated. The pending link replaces
the old link with Windows `MoveFileExW` replace/write-through. Earlier complete
generations remain usable after a staging error or interrupted staging.

Numeric version, real commit count, release/development variant and commit
ancestry prevent older or unrelated builds silently replacing a newer lineage.
Development cannot replace a release. Rerunning the same build is repair. Builds
with different uncommitted contents at the same commit/version are treated as
repair; this development facility is not a signed update trust mechanism.
Legacy/uncertain registrations fail closed and require explicit uninstall first.

Registry activation consists of multiple writes, not an atomic Windows registry
transaction. A failure during activation retains complete old/new generations
and returns an error; rerun setup to repair registration/shortcut consistency.
If interrupted first-install registration is incomplete and setup refuses its
identity, use its registered uninstaller and reinstall; data remains preserved.
No successful recovery from every possible power-loss boundary is claimed.
Earlier generations consume disk until uninstall; automatic pruning is deferred.

Uninstall deletes only exact known files beneath generations with the matching
ownership marker and root, plus its own shortcut/registration. It uses no
recursive deletion. Locked owned files return a failure and leave registration
and ownership available for retry. Unknown files are retained. It never opens or
deletes application state, SQLite, preferences, findings or history, including on
upgrade. No data-removal option is provided; removing retained data is a separate
explicit user action outside setup.

| Exit code | Meaning |
| --- | --- |
| 0 | Completed |
| 2 | Running, competing or inaccessible session mutex |
| 3 | Older/incompatible version or source lineage |
| 5 | Unsupported platform, path or incomplete/legacy registration |
| 41 | Injected acceptance staging failure (test fixture only) |
| 42 | Activation incomplete; complete payloads retained for repair |
| 43–50 | Directory, payload, marker, uninstaller or shortcut staging failure |
| 51 | Locked owned file during uninstall; close locking program and retry |

Silent setup accepts `/S /D=C:\absolute path` with `/D=` last and unquoted,
according to NSIS syntax. Ordinary interactive use selects the directory in the
wizard. Silent uninstall is the registered quoted command plus `/S`.

## Native acceptance

Run from an interactive Windows 11 x64 standard-user desktop with Sparrow exited.
The test writes only a temporary install/profile plus random acceptance-specific
HKCU keys and a random Start Menu folder. A restricted workspace sandbox must
allow those owned shell/registry operations. It never uses the default Sparrow
profile. Native GUI input requires an unlocked desktop; avoid other mouse input.

```powershell
$env:RADANOR_NATIVE_INSTALLER = '1'
$env:RADANOR_INSTALLER_PORTABLE_A = 'C:\absolute\older-real-build'
$env:RADANOR_INSTALLER_PORTABLE_B = (Resolve-Path dist/windows).Path
go test ./internal/wininstaller -run '^TestNativeInstallerLifecycle$' -count=2 -v
```

The older A must come from an actual ancestor checkout with valid resources and
`BUILD-INFO`; do not invent version numbers to simulate an upgrade. The test
compiles namespaced A/B and failure/pause fixtures. Test-only compiler defines
are absent from the ordinary artifact. Diagnostics contain only fixture paths
and setup arguments, never launch tickets, cookies or IPC secrets.

The test resolves the real installed Start Menu shortcut and launches its target
with explicit isolated state. This is not a physical Start Menu click. Complete
the separate clean-account wizard, Start Menu click and Windows trust-prompt
walkthrough before release. Do not disable Defender, SmartScreen or Smart App
Control to complete it. Windows 10, ARM64, cross-session installations and
machine-wide scope are not qualified by P8.

## CI and integration

The Windows leg of `desktop-packaging` now gets full Git history, provisions
signed MSYS2 7-Zip and pinned NSIS 3.13-2, enforces compiler/package identity,
builds a development setup and checks
Windows version/icon APIs plus independently extracted payload bytes. Linux's
checkout depth and build commands are retained. A separate headless test actually
executes namespaced silent install/repair/recovery/uninstall fixtures with the
current payload, checks the shortcut target and data retention, and simulates a
held application mutex. CI requires explicit PASS events for that test and all
three artifact checks, preventing skipped opt-ins from giving a false green.
It does not launch the application or qualify the runner's token as a normal
user, and does not exercise A-to-B upgrade/downgrade or interactive tray behavior.
Those remain separate native acceptance. It publishes no new installer artifact.
Changing compiler versions requires review rather than silently changing stubs.

Public release still requires verified BSL identity/date/authorization, a real
individual Authenticode identity and reviewed EXE/setup/uninstaller signing and
timestamping, the existing P6 detached-signature controls, and clean reruns from
the approved committed source. No production key or legal identity is supplied
by this work.
