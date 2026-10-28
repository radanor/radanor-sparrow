# GitHub release description — v0.1.1

**No release has been published.** This is a template: copy the block below into the
GitHub release form when the release is authorized, and not before. Everything in
`⟨angle brackets⟩` is a field that **must be filled from the final artifacts** at
publication. Do not publish this text with a bracket still in it, and do not publish it
before every row of [`FINAL_RELEASE_CHECKLIST.md`](FINAL_RELEASE_CHECKLIST.md) is `PASS`.

Fields that must be filled at publication:

| Field | Where it comes from |
| --- | --- |
| `⟨release-date⟩` | the actual publication date (also confirms the BSL first-distribution date) |
| `⟨sha256 of …⟩` | `SHA256SUMS.windows`, `SHA256SUMS.appimage`, `SHA256SUMS.portable` from the final build |
| `⟨release-notes-url⟩` | the URL of the notes once rendered |
| `⟨issue-tracker-url⟩` | the repository's issue tracker URL |

Tag: `v0.1.1`. Release title: **Radanor Sparrow Community Edition v0.1.1**. Attach the
files listed in "Downloads" below, unmodified, straight from the release build — not
renamed, not re-zipped, not rebuilt.

---

## Radanor Sparrow Community Edition v0.1.1

**Release date:** ⟨release-date⟩

Radanor Sparrow is a lightweight, **local** cybersecurity and monitoring application for a
single computer. It watches file activity, assesses the machine's security posture with
evidence-backed checks, and shows the installed software and the services listening on
local ports.

It runs as your normal user account, keeps everything in your own profile, needs no
account or service, and sends nothing off the computer. No administrator rights are needed
on either platform, and it refuses to run elevated.

**Sparrow is not antivirus and not EDR.** It does not detect malware, block, quarantine or
remove anything, and it does not produce a protection score.

This is the first public release of the Community Edition. The product was developed and
tested as *Radanor Shield* until now; an existing installation is found in place, and its
data is not migrated or orphaned.

`v0.1.0` was tagged during preparation but never published — its Windows build failed, so
no artifact from it exists and no download of it was ever offered.

### Downloads

**Windows 11, 64-bit**

| File | Size | SHA-256 |
| --- | --- | --- |
| `RadanorSparrow.exe` — portable executable | ⟨bytes⟩ | ⟨sha256 of RadanorSparrow.exe⟩ |
| `RadanorSparrow-0.1.1-setup.exe` — per-user installer | ⟨bytes⟩ | ⟨sha256 of RadanorSparrow-0.1.1-setup.exe⟩ |
| `SHA256SUMS.windows` | ⟨bytes⟩ | ⟨sha256 of SHA256SUMS.windows⟩ |

**Ubuntu 22.04 / 24.04 / 26.04, x86-64**

| File | Size | SHA-256 |
| --- | --- | --- |
| `RadanorSparrow-0.1.1-x86_64.AppImage` — native-window desktop app | ⟨bytes⟩ | ⟨sha256 of the AppImage⟩ |
| `radanor-sparrow` — portable supervisor, no GTK/WebKitGTK required | ⟨bytes⟩ | ⟨sha256 of radanor-sparrow⟩ |
| `SHA256SUMS.appimage` | ⟨bytes⟩ | ⟨sha256 of SHA256SUMS.appimage⟩ |
| `SHA256SUMS.portable` | ⟨bytes⟩ | ⟨sha256 of SHA256SUMS.portable⟩ |

Both platforms also include `LICENSE`, `THIRD_PARTY_NOTICES.md`, `BUILD-INFO` and
`UNSIGNED-DISTRIBUTION.md`. Keep the licence and notices with the binaries if you pass
them on.

### ⚠ This release is unsigned

These artifacts carry **no Authenticode signature and no OpenPGP signature**, and no
trusted timestamp.

- On Windows you will see an **Unknown Publisher** prompt, and SmartScreen or Smart App
  Control may warn about or block the installer.
- On Linux there is no published key fingerprint, so a download cannot be attributed to a
  signing key.

**A SHA-256 checksum proves the file arrived intact. It does not prove who built it.**
Someone who could replace the download could replace the checksum file beside it. The
integrity chain is therefore TLS to this page plus the checksum manifest — weaker than a
signed release, and stated plainly rather than glossed over.

**Do not disable Windows Defender, SmartScreen, Smart App Control or any other protection
to run this software.** Verify the checksum, confirm you downloaded from this page, and
decide whether you trust the origin.

### Verify before you run

```powershell
# Windows PowerShell, in the download folder
Get-FileHash -Algorithm SHA256 .\RadanorSparrow.exe
Get-Content .\SHA256SUMS.windows
```

```bash
# Ubuntu
sha256sum -c --strict SHA256SUMS.appimage
sha256sum -c --strict SHA256SUMS.portable
```

Full instructions, including how to script the comparison:
[`VERIFY_DOWNLOADS.md`](VERIFY_DOWNLOADS.md).

### Install

- **Windows:** [`WINDOWS_INSTALLATION_GUIDE.md`](WINDOWS_INSTALLATION_GUIDE.md) — portable
  or per-user installer, silent-install switches and exit codes, upgrades, uninstall.
- **Ubuntu:** [`LINUX_INSTALL_GUIDE.md`](../deployment/LINUX_INSTALL_GUIDE.md) — AppImage,
  application-menu installation, the stock-24.04 AppArmor behaviour, uninstall.

On Linux, the native window needs GTK 3 and WebKitGTK 4.1 from your Ubuntu installation. If
the WebKit sandbox cannot start — on stock Ubuntu 24.04, AppArmor restricts unprivileged
user namespaces — Sparrow refuses the window and opens the authenticated dashboard in your
browser instead. That is designed behaviour, not a fault.

### What is in this release

- file-activity monitoring through the operating system's own notifications, with visible
  monitoring health and explicit gaps instead of silent loss;
- evidence-backed posture checks — a check that cannot obtain evidence says so rather than
  passing;
- installed-software inventory and locally listening endpoints;
- an authenticated loopback dashboard, single-instance behaviour, and a native window on
  both platforms;
- per-user Windows installation with generational upgrades, repair, downgrade refusal and
  data preservation;
- a reproducible Linux AppImage carrying the native window component.

### Known limitations

- **Unsigned** on both platforms (above), and **no automatic updates** — updating means
  downloading and replacing the file.
- **x86-64 only.** Windows 10, Windows on ARM and 32-bit Windows are not qualified.
- **No telemetry or crash reporting:** a failure produces a local log and no report.
- Wayland, dock and accessibility behaviour on Ubuntu 24.04/26.04 is not measured;
  native-window evidence comes from X11 sessions.
- The 128 px and 256 px launcher icons are derived from the approved 48 px master and await
  a human visual check.
- Monitoring and assessment only: no malware detection, blocking or removal.

Full notes: [`RELEASE_NOTES_V0_1_1.md`](RELEASE_NOTES_V0_1_1.md).

### Privacy

No account, no licence key, no telemetry, no outbound connection from Sparrow's own code.
Everything is stored in your own profile; uninstalling keeps your data, and deleting the
state directory removes it. Details, including the honest caveats about the system
rendering engine: [`SECURITY_AND_PRIVACY.md`](SECURITY_AND_PRIVACY.md).

### Licence

Radanor Sparrow Community Edition is **source-available, not open source**. It is
distributed under the **Business Source License 1.1** until the Change Date
**2030-10-27**, after which it becomes available under the **GNU General Public License
v3.0**.

Free production use is granted by the Additional Use Grant: personal use, and internal use
by an organisation on computers it owns or operates. Offering Sparrow as a competing hosted
or managed security service, or commercially licensing or reselling it to third parties, is
outside the grant and needs a commercial licence, which is not currently offered. The
Business Source License already permits copying, modification, redistribution and
non-production use.

`LICENSE` is authoritative. Plain-language summary:
[`COMMUNITY_LICENSE_GUIDE.md`](COMMUNITY_LICENSE_GUIDE.md).

Copyright © 2026 Radheshyam Dangi. Radanor is a product brand, not a company.

### Support and security

Ordinary bugs and questions: ⟨issue-tracker-url⟩.

**Security problems: report privately, never in a public issue** — use this repository's
Security tab → *Report a vulnerability*, if it is enabled. See
[`SUPPORT_AND_SECURITY_REPORTING.md`](SUPPORT_AND_SECURITY_REPORTING.md) for what to include
and what response to expect.

Full documentation index: [`README.md`](README.md). Release notes:
⟨release-notes-url⟩.
