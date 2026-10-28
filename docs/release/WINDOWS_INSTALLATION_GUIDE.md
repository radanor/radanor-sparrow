# Installing Radanor Sparrow on Windows

This is the end-user guide for the Windows distribution of Radanor Sparrow Community
Edition v0.1.0. It covers the portable executable, the NSIS installer, first launch,
verification, upgrades, uninstall and the warnings you should expect to see.

**No release of Sparrow has been published yet.** This guide describes the installation
that a published v0.1.0 release will perform. It is written from the measured behaviour
of development builds and of the installer's own acceptance runs, and it is factually
incomplete in one place on purpose: it cannot name the final file hashes, because no
release artifact exists yet. Where a value is only known at release time, this guide
says so instead of guessing.

## What you need

| Requirement | Detail |
| --- | --- |
| Operating system | **Windows 11, 64-bit (x64)**. Measured on Windows 11 25H2, build 26200. |
| User rights | A normal user account. **No administrator rights** are needed or requested. |
| Disk | About 20 MB for the portable executable; the installer keeps earlier generations until uninstall |
| WebView2 | **Microsoft Edge WebView2 Evergreen Runtime**, for the native window. Present on current Windows 11. If it is missing, Sparrow opens its dashboard in your default browser instead — it does not install anything |
| Browser | Any current browser, for the fallback path |

**Windows 10, Windows on ARM, and 32-bit Windows are not qualified and are not
supported.** They may work; they have not been tested, and this guide will not claim
they do.

## Which file to download

A Windows release publishes two different artifacts. They install the same application
in different ways:

| Artifact | What it is | Use it when |
| --- | --- | --- |
| `RadanorSparrow.exe` | the portable executable. Nothing is installed; this one file **is** the application | you want to try Sparrow, keep it on a USB stick, or avoid an installer entirely |
| `RadanorSparrow-<version>-setup.exe` | the NSIS installer. Installs per user, adds a Start Menu shortcut and an uninstall entry | you want it in the Start Menu, with a normal uninstall |

Both are accompanied by:

| File | Purpose |
| --- | --- |
| `SHA256SUMS.windows` | SHA-256 of every published file |
| `BUILD-INFO` | the build's version, variant, commit and binary checksum |
| `LICENSE` | the licence, which you must keep if you pass the software on |
| `THIRD_PARTY_NOTICES.md` | third-party licences and attributions |
| `UNSIGNED-DISTRIBUTION.md` | the unsigned-build disclosure |

Download only from the official Radanor site **radanor.com** (whose download links resolve to
the project's own **GitHub Releases** assets) or from that GitHub Releases page directly. Any
other mirror is unauthorised, and its checksums say nothing about where the file came from.

## Expect an "Unknown Publisher" warning

**These builds carry no Authenticode signature.** No publicly trusted code-signing
certificate is used, so:

- Windows shows an **Unknown Publisher** prompt, because there is no publisher
  certificate to name.
- **SmartScreen** may warn about an unrecognised app.
- **Smart App Control**, if enabled on your PC, may block the installer outright.

This is expected and it is disclosed, not a fault in your download. What to do:

1. Verify the SHA-256 of the file against `SHA256SUMS.windows` — see
   [`VERIFY_DOWNLOADS.md`](VERIFY_DOWNLOADS.md).
2. Confirm you downloaded from the official Releases page.
3. Then decide whether you trust that origin, and choose the "keep anyway" / "run
   anyway" option that the prompt offers.

**Do not turn off SmartScreen, Smart App Control, Defender or any other Windows
security feature to run Sparrow.** Disabling the control removes the only signal that
was telling you something. If the block cannot be resolved by verifying the checksum,
the honest conclusion is that you should not run the file.

Because there is no signature, **a checksum proves integrity, not identity**. Someone who
could replace the download could replace the checksum file beside it. This is the
authenticity limitation of an unsigned build, and it is why the release is published
from exactly one place.

## Install with the installer

1. Verify the download ([`VERIFY_DOWNLOADS.md`](VERIFY_DOWNLOADS.md)).
2. Exit any running Sparrow: **Exit Radanor Sparrow** in the dashboard header, not the
   window's X button.
3. Run `RadanorSparrow-<version>-setup.exe`.
4. Choose the install directory, or accept the default:
   `%LOCALAPPDATA%\Programs\Radanor Sparrow`.
5. Finish the wizard. Setup writes the files, verifies them, and only then updates the
   Start Menu shortcut and the uninstall entry.

What setup does **not** do:

- it does not request elevation and installs for the current user only;
- it does not create a Windows service, scheduled task, driver, autostart entry or
  desktop shortcut;
- it does not modify `PATH`;
- it does not download anything, including WebView2;
- it does not delete, migrate or convert your existing Sparrow data.

### Silent installation

For scripted or fleet use, setup accepts NSIS's standard silent switches:

```bat
RadanorSparrow-0.1.0-setup.exe /S /D=C:\Tools\Radanor Sparrow
```

`/D=` must be **last** and **unquoted**, per NSIS syntax. Silent uninstall is the
registered uninstall command plus `/S`.

### Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Completed |
| 2 | A running, competing or inaccessible session lock — Sparrow is probably still running |
| 3 | Older or incompatible version, or a different source lineage (downgrade refused) |
| 5 | Unsupported platform, path, or an incomplete/legacy registration |
| 42 | Activation incomplete; the complete payloads were kept so a rerun can repair |
| 43–50 | Directory, payload, ownership-marker, uninstaller or shortcut staging failed |
| 51 | A file owned by Sparrow was locked during uninstall; close the program and retry |

Exit code 41 exists only in the test fixture and never in a published installer.

## Run the portable executable

1. Verify the download.
2. Put `RadanorSparrow.exe` wherever you like. Keep `BUILD-INFO`, `LICENSE` and
   `THIRD_PARTY_NOTICES.md` with it: they are part of the distribution.
3. Double-click it, or run it from a terminal.

Nothing is written to the registry and no shortcut is created. The application still
uses the same per-user data directory as an installed copy, so portable and installed
copies share your history and settings.

**Do not run it as Administrator.** Elevation is not needed and is refused.

## First launch

On first launch Sparrow:

1. takes its single-instance lock, so a second launch reopens the running instance
   instead of starting a second monitoring core;
2. opens the native window using the installed WebView2 runtime — or, if that is not
   available, opens the authenticated dashboard in your default browser with a one-time
   launch ticket;
3. starts monitoring and shows its status in the dashboard.

Closing the window (or the browser tab) **keeps monitoring running**. To stop everything,
use **Exit Radanor Sparrow** in the dashboard header. Launching again reopens the existing
session.

## Where your data is kept

Everything stays in your own profile:

| Data | Location |
| --- | --- |
| Monitoring database — history, assessments, findings, inventory, settings | `%LOCALAPPDATA%\Radanor\Shield\monitoring.db` |
| Application log | `%LOCALAPPDATA%\Radanor\Shield\application.log` |
| Window component log | `%LOCALAPPDATA%\Radanor\Shield\desktop-window.log` |
| WebView2 profile data | `%LOCALAPPDATA%\Radanor\Shield\webview2\` |
| Instance locks and launch secret | `%LOCALAPPDATA%\Radanor\Shield\run\` |
| Installed program files (installer only) | `%LOCALAPPDATA%\Programs\Radanor Sparrow\` |

The state directory keeps its pre-rebrand name `Radanor\Shield` on purpose: renaming it
would orphan the data of anyone who installed before the product was renamed to Sparrow.

These directories and files are created with permissions for your user only.

## Updating

There is **no automatic updater**. Sparrow does not contact a server and has no update
channel; manual update is the design, not a missing feature. Anything claiming to update
Sparrow automatically is unofficial.

To update:

1. **Exit Radanor Sparrow** from the dashboard header.
2. Download the new version and verify its checksum.
3. Run the new setup, or replace the portable executable.

Every install, upgrade or repair writes a **new** generation under `versions\` inside the
install directory. Files are completed before the Start Menu entry and the uninstall
registration are switched over, so an interrupted update leaves the previous complete
generation usable — rerun setup to repair.

Upgrades in either direction are constrained by the build's real identity:

- a **newer** version upgrades an older one;
- re-running the **same** version is a repair;
- an **older** version is refused with exit code 3, rather than silently replacing a
  newer installation;
- a *development* build cannot replace a *release* build.

Earlier generations consume disk until uninstall; pruning is not implemented in v0.1.0.

## Uninstalling

Use **Settings → Apps → Installed apps → Radanor Sparrow Community Edition → Uninstall**,
or the Start Menu uninstall entry. Exit Sparrow first.

Uninstall removes exactly the files setup created, its shortcut and its registration
entry. It does **not** delete your monitoring database, history, findings, settings,
logs or WebView2 data, and it is not offered as a data-removal tool. If a file it owns is
locked, uninstall fails with exit code 51 and leaves the registration in place so you can
close the program and retry. Files it does not recognise are left alone.

To remove your data as well — only if that is what you intend — exit Sparrow and delete:

```powershell
Remove-Item -Recurse -Force "$env:LOCALAPPDATA\Radanor\Shield"
```

## Troubleshooting

| Symptom | Cause and what to do |
| --- | --- |
| "Unknown Publisher" or SmartScreen warning | Expected for an unsigned build. Verify the SHA-256, confirm the official origin, then choose to keep the file |
| Smart App Control blocks the installer | The policy is refusing an unsigned binary. Do not disable Smart App Control. Verify the download; if it still blocks, do not run it |
| Setup exits with code 2 | Sparrow is still running, or its lock is inaccessible. Use **Exit Radanor Sparrow**, then retry |
| Setup exits with code 3 | You are installing an older or differently-sourced build over a newer one. Uninstall deliberately if you really mean to go backwards; your data is preserved either way |
| Setup exits with 43–50 | A staging step failed. Rerun setup; the previous complete version is still available |
| Setup exits with 42 | Activation did not finish. Rerun setup to repair registration and the shortcut |
| Uninstall exits with 51 | A file is in use. Close Sparrow and any program holding its files, then retry |
| The dashboard opens in the browser instead of a window | WebView2 is missing or unusable. Install the Evergreen runtime if your organisation permits it, or keep using the browser |
| Double-clicking twice "does nothing" | By design: the second launch reopens the running instance |
| You are asked for a password | Sparrow never requests elevation. If something does, you are running a different file |
| Which version is this? | `Get-Content BUILD-INFO` beside the executable, or read the version from the executable's Properties → Details |

Logs are the first place to look; the reason for a refused window is written to
`application.log` in the state directory above.

## What this build does not have

Stated here rather than left to be discovered:

- **No Authenticode signature and no trusted timestamp.** Integrity rests on the checksum
  manifest and the TLS connection to the official origin. See
  [`UNSIGNED_DISTRIBUTION_NOTICE.md`](../deployment/UNSIGNED_DISTRIBUTION_NOTICE.md).
- **No automatic updates, no update channel, no telemetry.**
- **No elevation, service or machine-wide install.** Per-user only.
- **Windows 10, ARM64 and 32-bit are not qualified.**
- **No tray-icon customisation or autostart.**

## Related documents

- [`VERIFY_DOWNLOADS.md`](VERIFY_DOWNLOADS.md) — checksum verification, step by step.
- [`SECURITY_AND_PRIVACY.md`](SECURITY_AND_PRIVACY.md) — what Sparrow stores and what it
  does not do.
- [`COMMUNITY_LICENSE_GUIDE.md`](COMMUNITY_LICENSE_GUIDE.md) — the licence in plain
  language.
- [`LINUX_INSTALL_GUIDE.md`](../deployment/LINUX_INSTALL_GUIDE.md) — the Ubuntu guide.
- [`WINDOWS_INSTALLER.md`](../deployment/WINDOWS_INSTALLER.md) — the engineering
  reference for the installer.
