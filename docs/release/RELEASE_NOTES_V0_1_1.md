# Radanor Sparrow Community Edition v0.1.1

**Release date:** set at publication.

**Status: prepared, not published.** These notes describe the v0.1.1 release candidate.
No v0.1.1 artifact has been published, and publication is gated on the items in
[`FINAL_RELEASE_CHECKLIST.md`](FINAL_RELEASE_CHECKLIST.md). Where a value only exists once
the release is published — the date, the artifact hashes — these notes say so rather than
filling it in.

**Why v0.1.1 and not v0.1.0.** `v0.1.0` was tagged on 2026-10-28 at `45f44ef`, but its
Community workflow failed while building the Windows installer: that checkout materialised
the embedded dashboard bundle with CRLF line endings, the frontend build rewrote it with
LF, and the release version rule then refused the tag as a checkout with uncommitted
changes. Nothing was published from `v0.1.0` — no GitHub Release and no binary — and the
tag is preserved unchanged as the record of that attempt. `v0.1.1` carries the same product
content from a commit that fixes that packaging defect.

## Overview

Radanor Sparrow is a lightweight, **local** cybersecurity and monitoring application for a
single computer. It watches file activity, assesses the security posture of the machine
with evidence-backed checks, shows the installed software and the services listening on
local ports, and lets you configure what it watches.

It runs as your ordinary user account in two local processes, keeps its data in your own
profile, needs no account or service, and sends nothing off the computer. It also does
not need administrator rights on either platform, and refuses to run elevated.

**v0.1.1 is the first public release of the Community Edition** under the product's new
name. It was previously developed and tested as *Radanor Shield*; the data directory,
single-instance lock and environment variables keep their old `radanor-shield` names so
an existing installation is found in place rather than orphaned.

**Sparrow is not antivirus and not EDR.** It does not detect malware, block anything,
quarantine anything or remove anything, and it does not produce a protection score.

## What this release does

Verified on the builds described below:

- **File activity monitoring** using the operating system's own change notifications
  (inotify on Linux, the Windows notification APIs), with bounded queues, visible
  monitoring health, and explicit gaps when a source drops events instead of silently
  losing them.
- **Security posture assessment** — checks against the local computer's configuration,
  each carrying the evidence it examined. A check that cannot obtain evidence reports
  that, rather than passing.
- **Installed software inventory**, read from the operating system's own records.
- **Listening endpoints** — which local ports are listening and which process owns them.
  Observation only: no packet capture.
- **Authenticated local dashboard** with settings for what is monitored. A one-time launch
  ticket is exchanged for a short-lived session cookie; the gateway is bound to
  `127.0.0.1` only.
- **Native desktop window**: WebView2 on Windows, a GTK 3 / WebKitGTK 4.1 component on
  Linux. If the native window cannot be shown, the authenticated dashboard opens in your
  browser instead — an unsandboxed window is never presented on Linux.
- **Single instance**: launching again reopens the running instance rather than starting a
  second monitoring core. Closing the window keeps monitoring running; **Exit Radanor
  Sparrow** in the dashboard stops it.

## Windows

Supported: **Windows 11, x64**.

| | |
| --- | --- |
| `RadanorSparrow.exe` | portable executable, nothing installed |
| `RadanorSparrow-0.1.1-setup.exe` | per-user NSIS installer |

- Per-user installation into `%LOCALAPPDATA%\Programs\Radanor Sparrow`, with a Start Menu
  shortcut and an uninstall entry in HKCU. **No administrator rights**, no service, no
  scheduled task, no autostart entry, no driver, no desktop shortcut.
- **Generational installs**: each install, upgrade or repair writes a new complete
  generation and switches the shortcut and registration over only after the files are
  finished. An interrupted update leaves the previous complete version usable; rerun setup
  to repair.
- **Upgrade, repair and downgrade handling** from the build's real identity: a newer
  version upgrades, the same version repairs, an older version is refused rather than
  silently replacing a newer installation, and a development build cannot replace a
  release build.
- **User data is preserved** across install, repair, upgrade and uninstall. Uninstall
  never deletes your database, history or settings.
- **Tray lifecycle** reliability work is integrated: the tray icon's menu now opens
  reliably under mouse input and recovers from overflow states.
- Silent installation: `/S /D=<absolute path>` (NSIS syntax, `/D=` last and unquoted).
  Exit codes are documented in the installation guide.

## Ubuntu / Linux

Supported: **Ubuntu 22.04, 24.04 and 26.04, x86-64**.

| | |
| --- | --- |
| `RadanorSparrow-0.1.1-x86_64.AppImage` | the desktop application: supervisor **and** the native window component |
| `radanor-sparrow` | portable supervisor, no GTK/WebKitGTK needed |

- **Native window** with the WebKit renderer inside its bubblewrap sandbox. GTK 3 and
  WebKitGTK 4.1 are used from your Ubuntu installation rather than bundled, so the
  distribution keeps patching them and the renderer keeps its own sandbox.
- **Desktop integration**: a per-user installer writes only the image, a
  `radanor-sparrow.desktop` entry, the 16/32/48 px icons and the licence documents — no
  `sudo`, no system directories.
- **AppArmor on stock Ubuntu 24.04**: unprivileged user namespaces are restricted for
  unconfined programs, so the WebKit sandbox cannot start. Sparrow then refuses the window
  and opens the authenticated dashboard in your browser instead. That is the designed
  behaviour, not a defect; an administrator can install the reviewed
  [`bwrap-userns-restrict`](../../packaging/apparmor/bwrap-userns-restrict) profile if the
  policy allows it. Sparrow never disables the restriction itself.
- **Reproducible build**: two clean clones of the same commit produced byte-identical
  AppImages in the recorded reproducibility run.

## Installation

- Windows: [`WINDOWS_INSTALLATION_GUIDE.md`](WINDOWS_INSTALLATION_GUIDE.md)
- Ubuntu: [`LINUX_INSTALL_GUIDE.md`](../deployment/LINUX_INSTALL_GUIDE.md)
- Both begin with [`VERIFY_DOWNLOADS.md`](VERIFY_DOWNLOADS.md): verify the SHA-256 before
  running anything.

## Security notices — read these

**This release is unsigned on both platforms.**

- The Windows artifacts carry **no Authenticode signature** and no trusted timestamp.
  Windows will show **Unknown Publisher**, and SmartScreen or Smart App Control may warn
  or block.
- The Linux artifacts carry **no OpenPGP signature**, and no key fingerprint is published,
  so a download cannot be attributed to a key this project has published.

Integrity therefore rests on the SHA-256 manifests plus TLS to the official origin. **A
checksum proves the file arrived intact; it does not prove who built it.** That reduced
authenticity guarantee is disclosed in
[`UNSIGNED_DISTRIBUTION_NOTICE.md`](../deployment/UNSIGNED_DISTRIBUTION_NOTICE.md), which
ships beside every artifact.

**Do not disable Windows Defender, SmartScreen, Smart App Control or any other security
protection to run this software.** Verify the checksum, confirm the origin, and decide
whether you trust it. Turning a protection off removes the only control that was telling
you something.

Sparrow's own behaviour with your data is described in
[`SECURITY_AND_PRIVACY.md`](SECURITY_AND_PRIVACY.md): local-first, no telemetry, no
account, no update channel.

## Upgrade notes

- **From a pre-rename installation (*Radanor Shield*):** your data is found in place. The
  state directory keeps its `radanor-shield` / `Radanor\Shield` name, as do the
  single-instance lock and the `RADANOR_SHIELD_*` environment variables. Nothing is
  migrated and nothing is orphaned.
- **From v0.1.1 to a later version:** manual. There is no auto-updater and no update
  channel; replacing the file after verifying its checksum is the supported update path.
- **Database schema:** only ever gains tables. A build that finds a *newer* schema than it
  understands refuses to start rather than damaging it, so a rollback to an older build
  may be refused after an upgrade — keep the previous artifact until you are satisfied.
- **Windows:** uninstall keeps your data. Going backwards through versions is refused by
  design; uninstall deliberately first if you mean to.

## Known limitations in v0.1.1

Stated because they are true, not as a caveat list to be skimmed:

- **No code signing on either platform** (see above). This is the single largest
  difference between these artifacts and a commercially signed product.
- **No automatic updates.** Manual update is the policy, not a gap to be filled later.
- **No telemetry or crash reporting.** A crash produces a local log and no report; you
  have to send it.
- **x86-64 only.** No ARM64 or 32-bit builds.
- **Windows 10, Windows on ARM and 32-bit Windows are not qualified.**
- **Wayland, dock and accessibility behaviour on Ubuntu 24.04/26.04 is not measured**;
  native-window evidence comes from X11 sessions.
- **The 128 px and 256 px launcher icons are derived** from the approved 48 px artwork by
  a pinned rendering script, not supplied as a high-resolution master. They await a human
  visual check.
- **No software-centre or AppStream catalogue listing.**
- **Earlier installer generations consume disk until uninstall**; automatic pruning is not
  implemented.
- **It is a monitoring and assessment tool, not a protection product.** No malware
  detection, no blocking, no removal.

## Licensing

Radanor Sparrow Community Edition is **source-available**, not open source. It is
distributed under the **Business Source License 1.1** until the Change Date,
**2030-10-27**, after which it becomes available under the **GNU General Public License,
version 3.0**.

Free production use is granted by the Additional Use Grant in [`LICENSE`](../../LICENSE):
personal use, and internal use by an organisation on computers it owns or operates.
Offering Sparrow as a competing hosted or managed security service, or commercially
licensing, reselling or distributing it to third parties, is outside that grant and needs
a commercial licence — which is not currently offered.

The Business Source License already permits copying, modification, redistribution and
non-production use; the grant only adds production-use permission. Plain-language
explanation: [`COMMUNITY_LICENSE_GUIDE.md`](COMMUNITY_LICENSE_GUIDE.md). Third-party
components keep their own licences:
[`THIRD_PARTY_LICENSE_COMPLIANCE.md`](THIRD_PARTY_LICENSE_COMPLIANCE.md) and
[`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md), which ship with every artifact.

Copyright © 2026 Radheshyam Dangi. Radanor is a product brand, not a company.

## Support and security reporting

Ordinary bugs: the project's issue tracker. Security problems: **privately**, never in a
public issue — see [`SUPPORT_AND_SECURITY_REPORTING.md`](SUPPORT_AND_SECURITY_REPORTING.md).

## Documentation

| Document | Purpose |
| --- | --- |
| [`README.md`](README.md) | index of the release documentation |
| [`VERIFY_DOWNLOADS.md`](VERIFY_DOWNLOADS.md) | checksum verification |
| [`WINDOWS_INSTALLATION_GUIDE.md`](WINDOWS_INSTALLATION_GUIDE.md) | installing on Windows |
| [`LINUX_INSTALL_GUIDE.md`](../deployment/LINUX_INSTALL_GUIDE.md) | installing on Ubuntu |
| [`SECURITY_AND_PRIVACY.md`](SECURITY_AND_PRIVACY.md) | what it stores and what it does not do |
| [`COMMUNITY_LICENSE_GUIDE.md`](COMMUNITY_LICENSE_GUIDE.md) | the licence in plain language |
| [`THIRD_PARTY_LICENSE_COMPLIANCE.md`](THIRD_PARTY_LICENSE_COMPLIANCE.md) | third-party licences |
| [`SUPPORT_AND_SECURITY_REPORTING.md`](SUPPORT_AND_SECURITY_REPORTING.md) | where to report what |
| [`FINAL_RELEASE_CHECKLIST.md`](FINAL_RELEASE_CHECKLIST.md) | what must be true before publication |
