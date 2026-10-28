# Single-executable desktop delivery

> **Provenance.** The run, install, data-location and build instructions below are
> kept current and were updated for the **Radanor Sparrow** rebrand (see
> the private rebranding record). The dated validation
> narrative in *Validation and remaining acceptance* and the *Changed-file
> inventory* record what was validated for the single-executable delivery
> milestone under the pre-rebrand name `Radanor Shield`; their file paths and
> artifact names are that milestone's history, not current instructions.
>
> Implementation branch: `feature/single-executable-delivery`, based on verified
> `origin/develop` at `2a76b57`. Local validation date: 2026-10-13.

## Architecture decision

Ship one executable with a private `--internal-core` mode. The ordinary launch
is the supervisor plus existing HTTP gateway; it starts the same executable as
a separate core process. The core alone owns SQLite. Every gateway-to-core call
still uses the existing current-user IPC ACL/permissions, HMAC and replay checks.
No core logic is linked into the HTTP request handlers. Developer core/gateway
commands remain available through a thin core wrapper and the existing gateway.
Lifecycle controls use new authenticated `/api/v1/application/status` and
`/api/v1/application/exit` endpoints. Existing foundation, device, assessment,
inventory and settings responses remain unchanged.

The core's initial stdin byte gates initialization until containment is attached.
Subsequent EOF requests context cancellation. Windows uses an unnamed Job Object
with kill-on-close, including descendants; Linux uses a separate process group,
parent-death SIGTERM and the inherited control pipe. Shutdown allows 15 seconds
for the managed core before terminating it. Gateway shutdown allows 5 seconds.
Startup failure tears down everything the launcher started. A core crash stops
the gateway and reports an error rather than keeping a disconnected dashboard.
Only a typed already-running lock result enables reopening. Permission/I/O
failures remain startup errors rather than being misreported as duplicate launches.

Both desktop and internal-core modes refuse elevated/root execution. Readiness
is authenticated and bound to a fresh launch identifier, so an unrelated existing
developer core cannot satisfy it. The launcher also takes the gateway lock and
does not take ownership of separately launched developer processes.

## Windows download and run

Use the reviewed `RadanorSparrow.exe` release artifact for Windows 11 x64. Put it
in a user-writable location and double-click it as your normal user. It starts
the core, binds an ephemeral `127.0.0.1` port and opens your default browser.
No Go, Node.js, npm, downloaded PowerShell or second Sparrow executable is needed.
No installer, service, startup registration or Administrator permission is used.

This development artifact is unsigned; a signed public release has not been
produced. Distribution trust/SmartScreen handling is a release acceptance task,
not a reason to disable security controls. Do not run it as Administrator.

The launch ticket is single-use, expires after 45 seconds, and travels only in a
local URL fragment. The frontend removes it before exchange. Session cookies,
Origin/Host restrictions and CSRF validation retain their existing behavior.
Tickets, cookies and launch URLs are never written into discovery files or logs.

Closing a browser tab **keeps monitoring running**. Launch the executable again
to reopen the existing application with a new authenticated session. Use **Exit
Radanor Sparrow** in the dashboard header to stop monitoring and the managed core.
Reloads recover the live session's CSRF token through an authenticated,
same-origin bootstrap. Old tabs recover once after a cookie/token mismatch;
unknown or expired sessions still require relaunch. Launching again does not
start a second core or reset persistent data.

Default data: `%LOCALAPPDATA%\Radanor\Shield`; runtime state is its `run`
subdirectory. `monitoring.db` and existing assessments/settings remain compatible.
The directory keeps its pre-rebrand name on purpose so an existing installation's
history is used in place; see the private rebranding record.
SQLite sidecars and the IPC secret keep their existing private protections.
`application.log` is private, bounded to 1 MiB and restarted on each owned launch.
`run\desktop-origin` contains only the protected loopback origin and is removed
on graceful exit. Stale discovery state is replaced after an OS lock is acquired;
no stored PID is used to kill another process.

Existing read-only Windows collectors may use **built-in Windows PowerShell**
and OS utilities. Startup and browser delivery do not require PowerShell. Its
absence, denied CIM access or restricted policy can limit evidence; the existing
assessment reports unavailable/unknown evidence rather than making permission
changes. This packaging work does not replace those collectors with native CIM.
Collector command windows are hidden without changing their arguments or policy.

## Ubuntu download and run

Target: Ubuntu Desktop 22.04/24.04, x86-64, with a default browser and the normal
OS `xdg-open` facility (`xdg-utils`). The Linux artifact contains frontend assets
and pure-Go SQLite; no developer toolchain or second Sparrow binary is required.

```bash
chmod 755 ./radanor-sparrow
./radanor-sparrow
```

Do not use `sudo`. For per-user installation from a built checkout:

```bash
bash scripts/install-linux.sh
```

Then launch **Radanor Sparrow** from the applications menu or the dock; no terminal
is needed afterwards. `bash scripts/install-linux.sh --uninstall` removes exactly
what it installed and leaves monitoring data in place.

The installer copies the binary into `~/.local/bin`, together with the optional
native window component (`radanor-sparrow-window`) when the build produced one, and
the approved icon into `~/.local/share/icons/hicolor/`. It writes
`~/.local/share/applications/radanor-sparrow.desktop` with `Terminal=false`,
`Icon=`, `StartupWMClass=` and `Categories=System;Security;`, validates it with
`desktop-file-validate` when that tool is present, refreshes the desktop and icon
caches, and copies `LICENSE` and `THIRD_PARTY_NOTICES.md` into
`~/.local/share/doc/radanor-sparrow/`. It refuses root and paths that cannot safely
be represented in a desktop entry. It does not install system services or change
security configuration.

On Ubuntu the product prefers its own GTK3/WebKitGTK 4.1 window and falls back to
the authenticated browser, with the reason in
`~/.local/state/radanor-shield/application.log`, when the window component or the
GUI runtime is unavailable. Closing that window stops only the window: monitoring
continues, and launching again asks the running instance for the dashboard, so a
repeat launch never starts a second core. The lifecycle, its evidence and its
limitations are recorded in
the private desktop integration record.

Data is `$XDG_STATE_HOME/radanor-shield`, defaulting to
`~/.local/state/radanor-shield`. Runtime is `$XDG_RUNTIME_DIR/radanor-shield`,
falling back to the state's `run` directory. Both keep their pre-rebrand names so
the instance locks that make duplicate launches fail are still found. Private directories are 0700 and
files 0600. Linux terminal errors are printed to stderr; private diagnostic logs
are in the state directory. The window component keeps its own log
(`desktop-window.log`) beside the application log, and its WebKit view data under
`webkit/`, both in that private directory.

## Build prerequisites and commands

Install Go 1.27 or newer, Node.js 24 LTS with its bundled npm, and Git separately
from their trusted distributions. Privileged tool installation, if required by
your organization, is a separate administrator task. Ordinary builds need no
Administrator/root permission. Scripts refuse missing/unsupported prerequisites.
They use the lockfile with `npm ci --ignore-scripts`, verify normal package
integrity through npm/Go, and use `GOTOOLCHAIN=local` to prevent an implicit Go
toolchain download. They do not fetch/run installer binaries or bypass policy.

Windows PowerShell (5.1+):

```powershell
git clone https://github.com/radanor/radanor-sparrow.git
Set-Location radanor-shield
.\scripts\build-windows.ps1
.\dist\windows\RadanorSparrow.exe
```

If organizational execution policy blocks unsigned scripts, leave it unchanged.
Use the equivalent build commands below or obtain an approved/signed script:

```powershell
Set-Location web
npm.cmd ci --ignore-scripts
npm.cmd run build
Set-Location ..
$env:GOTOOLCHAIN = 'local'
$version = ((& go run ./cmd/radanor-version) -join "`n").Trim()
$variant = ((& go run ./cmd/radanor-version -field variant) -join "`n").Trim()
# The icon and the version information are PE resources. They come from a
# resource object the linker folds in for a Windows target only, and it is a
# build product, so it is removed again once the executable is linked.
go run ./cmd/radanor-winres -arch amd64 -version $version -out cmd/radanor-sparrow/radanor_windows_amd64.syso
$env:CGO_ENABLED = '0'
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'
New-Item -ItemType Directory -Force dist/windows | Out-Null
go build -trimpath -buildvcs=false -ldflags "-H=windowsgui -X github.com/radanor/radanor-shield/internal/branding.Version=$version -X github.com/radanor/radanor-shield/internal/branding.Variant=$variant" -o dist/windows/RadanorSparrow.exe ./cmd/radanor-sparrow
Remove-Item -Force cmd/radanor-sparrow/radanor_windows_amd64.syso
.\dist\windows\RadanorSparrow.exe
```

Ubuntu Bash:

```bash
git clone https://github.com/radanor/radanor-sparrow.git
cd radanor-shield
bash scripts/build-linux.sh
./dist/linux/radanor-sparrow
```

End users do not build the product: the download, verification, per-user installation,
upgrade, uninstall and troubleshooting procedure is in
[LINUX_INSTALL_GUIDE.md](LINUX_INSTALL_GUIDE.md).

Default artifacts: `dist/windows/RadanorSparrow.exe` and
`dist/linux/radanor-sparrow`.

**Linux AppImage.** `bash scripts/build-linux.sh --appimage` additionally packages the
same binaries into `dist/linux/RadanorSparrow-<version>-x86_64.AppImage`, containing the
supervisor, the GTK/WebKitGTK window component, the launcher entry, the approved icon,
the license and the notices. `--appimage` and `--release` require the GTK/WebKitGTK
development packages and stop if the window component cannot be built: a plain
`bash scripts/build-linux.sh` still reports the omission and produces the portable,
browser-first executable. It deliberately contains no library: GTK, WebKitGTK,
JavaScriptCore, libsoup, bubblewrap and glibc come from the host, so the renderer keeps
running in the sandbox L3 measured and the distribution keeps patching those libraries.
Building it needs `appimagetool` (point `APPIMAGETOOL` at it or put it on `PATH`;
`scripts/build-appimage.sh` explains how to obtain it) and `unsquashfs`. Running it needs
no `libfuse` package: the embedded AppImage runtime carries its own static FUSE and
uses the kernel's FUSE support and `/dev/fuse`, and where those are unavailable it
extracts and runs (`--appimage-extract-and-run` remains the explicit form). An image
that carries no window component, or a host without a usable display, falls back to the
authenticated browser exactly like the installed product. The image can be run straight
from anywhere or integrated per user with
`bash scripts/install-appimage.sh [--release] <image>`; the installer reads the icons
and documents out of the image itself, so a downloaded file is the only input, and it
enforces the build variant the caller asks for (development by default, release with
`--release`). `--uninstall` removes exactly what it wrote, needs no image, and keeps the
private state directory. The user-facing procedure, including verification, upgrade and
troubleshooting, is [LINUX_INSTALL_GUIDE.md](LINUX_INSTALL_GUIDE.md); the measured
evidence is in the private AppImage packaging record.

**Verifying a downloaded artifact.** A release publishes `SHA256SUMS.appimage` and
`SHA256SUMS.portable` beside the files, and `scripts/verify-release.sh` checks them
(and, once a key exists, a signature). A checksum proves the file arrived unchanged;
only a signature checked against a fingerprint obtained out of band proves who
published it. Both, the manual-update policy and the (blocked) key setup are in
[RELEASE_SIGNING.md](RELEASE_SIGNING.md).

**What a Linux release packages.** The public release path
(`scripts/build-linux.sh --release --appimage`) produces two artifacts, and they are
not equivalent:

* the **AppImage** is the Linux desktop artifact. It is the only Linux artifact that
  contains the native GTK/WebKitGTK window component, so it shows the dedicated
  window where the host allows the WebKit sandbox and the authenticated browser where
  it does not;
* the **portable `radanor-sparrow` executable** is a single static binary for a
  browser-first deployment. It carries no window component and therefore always opens
  the authenticated dashboard in the default browser, even on 22.04 or 26.04 where
  the window would work.

The release workflow refuses to publish when the window component cannot be built,
checks that `usr/bin/radanor-sparrow-window` and `variant=release` are inside the
image, and uploads `SHA256SUMS.appimage` and `SHA256SUMS.portable` beside the two
artifacts. Development CI builds the same image on every push and runs the structural
tests and an X11 (Xvfb) smoke suite against the built file; Wayland, dock association
and accessibility on 24.04/26.04 remain outside CI, as the compatibility document
states.

**Linux desktop window prerequisites.** The window component is the only part that
needs a GUI stack, and it uses the distribution's copies rather than bundling them:
GTK 3 and WebKitGTK **4.1** at runtime, and `gcc`, `pkg-config`, `libgtk-3-dev` and
`libwebkit2gtk-4.1-dev` to build it. A standard Ubuntu Desktop already carries both
libraries (on 24.04 and 26.04 they arrive with `ubuntu-desktop-minimal`); a host
without them still runs the product in the authenticated browser. The renderer's
WebKit sandbox additionally needs `bubblewrap` and permission to create an
unprivileged user namespace. Ubuntu 22.04 and 26.04 allow that; stock Ubuntu 24.04
restricts it through AppArmor (`kernel.apparmor_restrict_unprivileged_userns=1`) and
ships no bubblewrap profile, so the window is refused and the browser is used — never
an unsandboxed window. That refusal is reported in the application log with the
setting named. An administrator may review and install
[`packaging/apparmor/bwrap-userns-restrict`](../../packaging/apparmor/bwrap-userns-restrict)
to allow it; no Radanor script installs that profile or changes the setting. The full
matrix and evidence are in the private compatibility record.

Each build also places `LICENSE` and
`THIRD_PARTY_NOTICES.md` in the same release directory, so a release carries the
license (which the Business Source License requires to accompany copies of the
Licensed Work) and the third-party notices (which the MIT and BSD-3 dependencies
require to be reproduced). The per-user Linux installer copies the same two
documents to `~/.local/share/doc/radanor-sparrow/`. Adding these documents does not
change the single-executable architecture or launch behavior. Both scripts also accept an arm64 architecture
selection (`-Architecture arm64` / positional `arm64`), but this local validation
qualifies only Windows amd64. ARM64 runtime behavior remains unverified.

A plain run is a **development** build. The explicit release path
(`bash scripts/build-linux.sh --release`, `pwsh ./scripts/build-windows.ps1 -Release`)
runs the licensing release gate first and again against the built package, and
fails closed: while any licensing placeholder remains (the Licensor identity and
the per-version Change Date, see [LICENSING.md](../licensing/LICENSING.md)), no
release artifact is produced. The gate is never a precondition for a development
build, and a green development CI run is not a release approval; public release
artifacts come from `.github/workflows/release.yml`.
Builds use CGO disabled, trimmed paths and no VCS metadata. Generated executable
artifacts stay in ignored `dist/`; they are not source changes or committed files.
Frontend HTML/JS/CSS are embedded with the existing `go:embed` boundary.

**Version metadata.** No script holds a version number. `cmd/radanor-version`
derives one from the checked-out commit and both scripts inject it into
`internal/branding.Version`, together with the build variant, so an artifact names
the commit it came from wherever the version is shown. An untagged tree reports
`0.1.0-dev.0+g<sha>`; past a `vX.Y.Z` tag it reports `X.Y.Z-dev.<n>+g<sha>`, with
`.dirty` appended when tracked files are modified; and the bare `X.Y.Z` appears
only when HEAD is exactly a clean version tag, which the release path additionally
requires. The Windows build also folds the approved artwork and that same version
into the executable's PE resources through `cmd/radanor-winres` — pure Go, no
Resource Compiler, so the Linux and Windows paths produce the same resource. The
file then carries the Radanor icon and its version in Explorer and in its
Properties dialog. The generated `.syso` is an intermediate beside the main
package: it is git-ignored, invisible to any non-Windows target, and the script
removes it when the build ends.

## Validation and remaining acceptance

Native Windows: complete `go test ./... -count=1` and `go vet ./...` passed,
including IPC. Launcher tests cover early elevated-token refusal by injection,
origin restrictions, bounded diagnostics, child start failure, graceful/forced
child shutdown and crash containment. Gateway tests verify authenticated Exit,
CSRF/Origin rejection and refusal to exit an unmanaged developer core.

The real GUI artifact test uses isolated temporary state/runtime directories.
It passed readiness, embedded HTML, ticket exchange, anonymous API rejection,
duplicate launch, authenticated Exit, core termination, settings persistence,
supervisor termination and restart after stale discovery/SQLite state.
The binary's PE subsystem is verified as Windows GUI. The automated lifecycle
normally uses `--no-browser`; HTTP ticket exchange is not a visual browser test.
Final native UI Automation now verifies the OS default browser automatically
renders an authenticated Scan now button, closing its test tab preserves the
parent, and a second executable launch reopens an authenticated dashboard.
The fresh-artifact Edge regression completes a scan after cookie rotation,
shows 7 actual recorded Security Checks cards and completed Scan History, clicks
Exit with confirmation, verifies parent/core termination, then restarts the
same isolated data and verifies exactly one completed assessment persists.
Its standalone executable copy has neither legacy root executable beside it.
Existing normal application data remains untouched. Explorer double-click itself
is not automated; direct executable launch exercises the same application.
A separate real-artifact invalid-configuration test covers startup
failure cleanup without displaying a diagnostic modal dialog.

Frontend at the preceding implementation validation: 63 tests passed; typecheck, production build and a fresh integrity-pinned
`npm ci --ignore-scripts` followed by build passed. PowerShell script parsed;
execution was BLOCKED by this host's Restricted policy. The equivalent build
commands produced the Windows artifact without changing that policy.

Linux amd64 artifact cross-build, launcher test cross-compilation, Linux-targeted
vet and Bash script syntax checks passed. WSL is not installed. Native Ubuntu
launch, shortcut, permissions, race tests and process-lifecycle execution remain
NOT RUN locally. The CI packaging matrix builds/tests both OS artifacts; those
new CI jobs have not been executed here. An elevated runner may skip the opt-in
real-lifecycle test; a skip is not native acceptance.
The Linux build script was also executed successfully under installed Git Bash
as a Windows-to-Linux cross-build; that does not establish native Ubuntu runtime
acceptance. The Linux browser-lifetime fixture is cross-compiled but NOT RUN
locally. Linux browser startup observes quick xdg-open failures but does not wait
for an attached browser process to exit before accepting lifecycle commands.

CE-7A event ingestion, durable bookmark replay and clear/rollover acceptance are
separate and remain as documented in the Windows event-log recovery notes, which are
held privately.
No Defender configuration, Windows Event Logs or registry security settings were
modified; no existing application was stopped. No existing API schemas, source IDs,
event allowlist or assessment terminology changed.

Native packaging validation:

```powershell
$env:RADANOR_SPARROW_TEST_BINARY = Join-Path $PWD 'dist/windows/RadanorSparrow.exe'
go test ./internal/desktop -count=1 -v
go test ./... -count=1
go vet ./...
git diff --check
```

Run this sequentially: the opt-in executable owns the fixed Windows IPC pipe.
Do not overlap it with IPC tests or another Sparrow instance. The test performs
crash injection only against its own isolated launcher, not an existing Sparrow.

Ubuntu validation after building:

```bash
RADANOR_SPARROW_TEST_BINARY="$PWD/dist/linux/radanor-sparrow" go test ./internal/desktop -count=1 -v
go test ./... -count=1
go vet ./...
```

Before public release, perform an Explorer double-click/visible authenticated
dashboard walkthrough, tab-close/reopen and Exit confirmation, Ubuntu desktop
acceptance, ARM64 qualification if offered, and release signing review.

References: [Windows Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects),
[COM initialization](https://learn.microsoft.com/en-us/windows/win32/api/combaseapi/nf-combaseapi-coinitializeex),
[desktop Exec escaping](https://specifications.freedesktop.org/desktop-entry/latest/exec-variables.html).

## Changed-file inventory

Core code and its existing regression tests were relocated from
`cmd/radanor-shield-core/` to `internal/coreapp/`, retaining these filenames:

- `assessment_api.go`
- `assessment_api_test.go`
- `assessment_resource_test.go`
- `ce6_api_test.go`
- `ce6_e2e_test.go`
- `inventory_api.go`
- `inventory_unicode_windows_test.go`
- `monitoring_api.go`
- `monitoring_api_test.go`
- `settings_api.go`
- `sources_linux.go`
- `sources_other.go`
- `sources_windows.go`

`internal/coreapp/main.go` contains the extracted core runtime, external context
cancellation and authenticated launcher readiness. `cmd/radanor-shield-core/main.go`
is replaced with a compatible standalone-command wrapper. The other relocated
files change only their package declaration (and Go formatting where applicable).

New launcher/runtime/test files:

- `cmd/radanor-shield/main.go`
- `internal/desktop/activation.go`
- `internal/desktop/app.go`
- `internal/desktop/artifact_windows_test.go`
- `internal/desktop/browser_linux_test.go`
- `internal/desktop/child.go`
- `internal/desktop/child_test.go`
- `internal/desktop/log.go`
- `internal/desktop/log_test.go`
- `internal/desktop/packaged_test.go`
- `internal/desktop/live_session_windows_test.go`
- `internal/desktop/scan_browser_windows_test.go`
- `internal/desktop/upgrade_windows_test.go`
- `internal/desktop/platform_linux.go`
- `internal/desktop/platform_other.go`
- `internal/desktop/platform_windows.go`

Other changed/new source and documentation files:

- `.github/workflows/ci.yml`
- `.gitignore`
- `README.md`
- `internal/collectors/exec_windows.go`
- `internal/gateway/server.go`
- `internal/gateway/assessment.go`
- `internal/gateway/distribution_assets_test.go`
- `internal/gateway/session_security_test.go`
- `internal/gateway/application_test.go`
- `internal/gateway/session_recovery_test.go`
- `internal/auth/sessions.go`
- `internal/auth/session_recovery_test.go`
- `internal/lifecycle/errors.go`
- `internal/lifecycle/errors_test.go`
- `internal/lifecycle/instance_windows.go`
- `internal/lifecycle/instance_unix.go`
- `web/src/App.tsx`
- `web/src/auth.ts`
- `web/src/auth_session.test.ts`
- `scripts/build-windows.ps1`
- `scripts/build-linux.sh`
- `scripts/build-appimage.sh`
- `scripts/install-linux.sh`
- `scripts/install-appimage.sh`
- `scripts/validate-scan-browser.mjs`
- `docs/deployment/DESKTOP_DELIVERY.md`
- `the private engineering record`
- `the private engineering record`

Generated embedded assets: `internal/gateway/assets/index.html` now references
`internal/gateway/assets/assets/index-BS3_8gMu.js`; the old
`internal/gateway/assets/assets/index-DxXjPDbd.js` is removed. The existing
`internal/gateway/assets/assets/index-DE87u2at.css` was regenerated with no
substantive content change after Git newline normalization.

Existing untracked `radanor-shield-core.exe` and `radanor-shield-gateway.exe` were
left untouched and are excluded from this patch. No files were staged; no commit,
push or PR was created. Distribution executables are ignored build outputs.

The final the private pre-commit security review records
66 passing frontend tests, stricter recovery-body validation, the rebuilt final
artifact, passing complete native suite including IPC, and successful fresh
packaged browser/lifecycle acceptance. The prior fixed-pipe failure is retained
as history. Windows amd64 staging readiness is confirmed; Linux/ARM64 runtime,
signing, Explorer interaction and unpublished CI remain outside this evidence.
