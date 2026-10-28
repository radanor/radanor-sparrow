# Installing Radanor Sparrow on Ubuntu

This is the end-user guide for the Linux distribution of Radanor Sparrow Community
Edition. It covers the AppImage, the per-user application-menu installation, upgrades,
uninstall, and what to do when the native window cannot be shown.

Nothing here needs `sudo`, an account, a service, or a second Sparrow binary. The
application runs as your normal user, stores its data in your home directory, and
never sends anything off the computer.

> **Status of this distribution.** The AppImage builds reproducibly and its native
> window and WebKit sandbox are validated on Ubuntu 22.04 and 26.04 (X11) and on
> 24.04 with an administrator-installed AppArmor profile. Public-release signing,
> automatic updates and the larger icon sizes are **not** available yet; the
> [limitations](#what-is-not-provided-yet) section says exactly what that means.

## Which file to download

A Linux release publishes two different artifacts. They are not interchangeable:

| Artifact | What it is | Use it when |
| --- | --- | --- |
| `RadanorSparrow-<version>-x86_64.AppImage` | the desktop application: the supervisor **and** the native GTK/WebKitGTK window component | you want the native window and the application-menu entry — this is the normal choice |
| `radanor-sparrow` | one static executable, no window component | you want a browser-first deployment with no GTK/WebKitGTK on the machine at all |

`SHA256SUMS.appimage` and `SHA256SUMS.portable` accompany them. `LICENSE` and
`THIRD_PARTY_NOTICES.md` ship with both.

The AppImage is a single file that contains the application. GTK 3 and WebKitGTK 4.1
are **not** inside it: they come from your Ubuntu installation (see
[requirements](#what-your-system-must-provide)), so the distribution keeps patching
them and the renderer keeps its own sandbox.

## What your system must provide

| Requirement | Ubuntu 22.04 | Ubuntu 24.04 | Ubuntu 26.04 |
| --- | --- | --- | --- |
| Kernel FUSE support and `/dev/fuse` | present | present | present |
| A `libfuse` package | **not needed** | **not needed** | **not needed** |
| GTK 3 and WebKitGTK 4.1 | on a desktop install | on a desktop install | on a desktop install |
| `bubblewrap` and unprivileged user namespaces (for the native window) | allowed | **restricted by AppArmor** | allowed |
| `xdg-utils` (for the browser fallback) | present | present | present |

* The AppImage runtime is statically linked, so it does **not** need `libfuse2` or
  `libfuse2t64`. It uses the kernel's FUSE support directly. If mounting is
  unavailable (a container, for example), run it with
  `--appimage-extract-and-run` — see [troubleshooting](#troubleshooting).
* The native window needs `libgtk-3-0` and `libwebkit2gtk-4.1-0`. A standard Ubuntu
  desktop already has both; a minimal installation may not, and then the
  authenticated dashboard opens in your browser instead.
* `bubblewrap` is what WebKit uses to sandbox its renderer. On **stock Ubuntu 24.04**
  AppArmor restricts unprivileged user namespaces for unconfined programs, so the
  sandbox cannot start: Sparrow then refuses the window and opens the authenticated
  dashboard in your browser. That is the designed behavior — an unsandboxed window is
  never shown. The reason is written to `application.log`; an administrator can
  review and install
  [`packaging/apparmor/bwrap-userns-restrict`](../../packaging/apparmor/bwrap-userns-restrict)
  to allow the window. No Sparrow script installs it, and Sparrow never disables the
  restriction itself.

Check what your machine reports:

```bash
cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns 2>/dev/null || echo 'not restricted'
/usr/bin/bwrap --ro-bind / / --proc /proc -- /bin/true && echo 'sandbox can start'
```

## Download and verify

1. Download the AppImage and `SHA256SUMS.appimage` into the same directory.
2. Verify the checksum:

```bash
cd ~/Downloads
sha256sum -c SHA256SUMS.appimage
# RadanorSparrow-<version>-x86_64.AppImage: OK
```

A checksum proves the file arrived intact; it does not prove who built it. Public
release signing is **not** in place yet (no release key exists), so a checksum from a
trusted channel is currently the strongest check available — treat a mismatch, or a
file from an untrusted mirror, as a reason not to run it.

When a release is signed, the directory also carries `.asc` signatures and the public
key, and verification then binds the file to the publisher — but only if you compare the
key's fingerprint with one you got somewhere other than the download page. Every published
file is signed (the AppImage, the portable executable and both checksum manifests), and
the pin below is what makes the check mean something:

```bash
cd ~/Downloads
bash verify-release.sh --require-signature --require-all-signed \
  --expect-fingerprint <40-HEX-FINGERPRINT> --key radanor-release-key.asc .
```

A valid signature by any other key is refused, and so is a release where one of the files
has no signature. The mechanism, the trust model and the switch-on procedure are in
[RELEASE_SIGNING.md](RELEASE_SIGNING.md).

The installer can perform the checksum check, and verify a signature **before the image is
unpacked or run**, as part of installing:

```bash
bash install-appimage.sh --checksums SHA256SUMS.appimage \
  --signature RadanorSparrow-<version>-x86_64.AppImage.asc \
  --public-key radanor-release-key.asc --fingerprint <40-HEX-FINGERPRINT> \
  --release RadanorSparrow-<version>-x86_64.AppImage
```

`--fingerprint` is required with `--signature`: the key that came with the download is not
trusted on its own, so you name the fingerprint you verified out of band, or point at a
keyring that already holds the key with `--keyring DIR`. If the signature is missing,
invalid, or by a different key, nothing is installed and the image is never run.

## Run it

```bash
chmod +x ./RadanorSparrow-<version>-x86_64.AppImage
./RadanorSparrow-<version>-x86_64.AppImage
```

The AppImage can live anywhere: `~/Downloads`, `~/Applications`, a USB stick. On the
first run the application:

1. takes its single-instance lock, so a second launch reopens the running instance
   instead of starting another monitoring core;
2. opens the native window when GTK/WebKitGTK are present and the WebKit sandbox can
   start, and otherwise opens the authenticated dashboard in your default browser
   with a one-time launch ticket;
3. starts monitoring and shows status in the dashboard.

**Never run it with `sudo`.** It refuses to run elevated, and elevation is not needed.

Closing the window (or the browser tab) **keeps monitoring running**. To stop
everything, use **Exit Radanor Sparrow** in the dashboard header. A launch that has
nothing to show will report a startup error rather than silently detaching.

## Add it to the application menu

The AppImage runs fine on its own, but a launcher entry gives you the icon, the
application-menu entry, the dock association and the license documents in the
standard place. The installer works with the downloaded file and does **not** need the
source repository: the icons and the documents are taken from the image itself.

```bash
# Download the installer next to the AppImage (or from the repository), then:
bash install-appimage.sh --release RadanorSparrow-<version>-x86_64.AppImage
```

Why `--release`: the installer is the development path by default, so it accepts a
development build and refuses a release build unless you say `--release`. That is the
variant check described below, not an obstacle.

It writes exactly these paths and nothing else:

| Path | What |
| --- | --- |
| `~/.local/bin/radanor-sparrow.AppImage` | the image, copied unchanged |
| `~/.local/share/applications/radanor-sparrow.desktop` | the launcher entry (`Terminal=false`, `StartupWMClass=radanor-sparrow`) |
| `~/.local/share/icons/hicolor/{16x16,32x32,48x48}/apps/radanor-sparrow.png` | the approved icons, exactly as they are inside the image |
| `~/.local/share/doc/radanor-sparrow/{LICENSE,THIRD_PARTY_NOTICES.md}` | the license and notices the image carries |

Then launch **Radanor Sparrow** from the applications menu or the dock. No terminal is
needed afterwards.

The installer refuses, rather than half-installing:

* a signature that is missing, invalid, or made by a key other than the trusted
  fingerprint (checked before the image is unpacked);
* a file that is not executable (it tells you to run `chmod +x`);
* a file whose `BUILD-INFO` is missing, has no version, or names a variant other than
  `development` or `release`;
* an artifact whose variant does not match the request — a release artifact without
  `--release`, or a development artifact with `--release`;
* an image that does not carry the approved 16/32/48 px icons or its own license
  documents;
* `sudo`, and home paths that cannot be represented safely in a desktop entry.

## Where your data lives

Everything stays in your home directory, in private (`0700` directories, `0600`
files) locations that keep their pre-rebrand `radanor-shield` names so an existing
installation is found in place rather than orphaned:

| Data | Path |
| --- | --- |
| History, assessments, findings, inventory, settings | `${XDG_STATE_HOME:-~/.local/state}/radanor-shield/monitoring.db` |
| Application log | `…/radanor-shield/application.log` (private, 1 MiB, rotated per owned launch) |
| Window component log | `…/radanor-shield/desktop-window.log` |
| WebKit view data | `…/radanor-shield/webkit/` |
| Instance locks and the launch secret | `${XDG_RUNTIME_DIR:-…/run}/radanor-shield/` |

`RADANOR_SPARROW_STATE_DIR` and `RADANOR_SPARROW_RUNTIME_DIR` move the first and the
last of these; the legacy `RADANOR_SHIELD_*` names are still read for an installation
that predates the rename.

## Upgrade

An upgrade is a **file replacement**, performed by you. Sparrow never updates itself,
never contacts a server, and has no update channel: "manual update" is the policy, not
a missing feature, and anything claiming to update it automatically is unofficial. See
[RELEASE_SIGNING.md](RELEASE_SIGNING.md) §7 for what an authenticated update channel
would have to satisfy before it could be considered.

There is no installer state, no service and no data conversion.

1. Exit the running instance from the dashboard header (**Exit Radanor Sparrow**). Do
   not kill the process.
2. Replace the image: either overwrite the file you run directly, or run the installer
   again with the new file so the launcher entry is refreshed:

   ```bash
   bash install-appimage.sh --release RadanorSparrow-<new-version>-x86_64.AppImage
   ```

3. Launch again. Your history, assessments and settings are the same files as before.

Keep the old image until you are satisfied with the new one; the database schema only
ever gains tables, and a build that finds a **newer** schema than it understands refuses
to start rather than damaging it. To roll back, put the previous image back and launch
it again.

## Uninstall

```bash
bash install-appimage.sh --uninstall
```

That removes exactly what the installer wrote: the installed image, the launcher entry,
the three icons and `~/.local/share/doc/radanor-sparrow/`. It **keeps your monitoring
history and settings** in the state directory listed above.

To remove your data as well — and only if you mean to — delete the state directory:

```bash
rm -rf "${XDG_STATE_HOME:-$HOME/.local/state}/radanor-shield"
```

If you never installed the menu integration and only ran the AppImage, uninstalling is
deleting the file. Nothing else was written outside the state and runtime directories.

## Troubleshooting

Start with the log; the reason for any refused window is written there in one line:

```bash
tail -n 20 "${XDG_STATE_HOME:-$HOME/.local/state}/radanor-shield/application.log"
```

| Symptom | Cause and what to do |
| --- | --- |
| The dashboard opens in the browser instead of a window | Expected when the window component or the GUI runtime is unavailable. The log states which. On 22.04/26.04 install `libgtk-3-0` and `libwebkit2gtk-4.1-0`; on stock 24.04 it is the AppArmor restriction below |
| `AppArmor blocks unprivileged user namespaces (kernel.apparmor_restrict_unprivileged_userns=1) …` | Ubuntu 24.04's policy, not a Sparrow defect. Use the browser, or ask an administrator to review and install [`packaging/apparmor/bwrap-userns-restrict`](../../packaging/apparmor/bwrap-userns-restrict) |
| `the GTK/WebKit GUI runtime is unavailable: DISPLAY and WAYLAND_DISPLAY are unset` | There is no graphical session to open a window on (a TTY, a remote shell, or a container). Use the browser or run it inside your desktop session |
| `GTK could not open the display` | A display is set but unusable. Re-run from the desktop session, or start an X server |
| The AppImage does not start, no window, no browser | Run it from a terminal to see the error. If FUSE is unavailable, use `./RadanorSparrow-<version>-x86_64.AppImage --appimage-extract-and-run` |
| The browser opens but shows nothing | `xdg-utils` and a default browser are required for the fallback. Check `xdg-open https://example.com` |
| The window will not open although the machine allows the sandbox | Check the log line; a missing `radanor-sparrow-window` beside the executable in a portable deployment is the usual reason — the AppImage contains it, the bare executable does not |
| A second launch "does nothing" | That is the design: it reopens the running instance instead of starting a second monitoring core |
| You are asked for a password | You are not: Sparrow never requests elevation. If something asks for one, you are running a different command |
| Which version is this file? | `./RadanorSparrow-<version>-x86_64.AppImage --appimage-extract 'usr/share/radanor-sparrow/BUILD-INFO' && cat squashfs-root/usr/share/radanor-sparrow/BUILD-INFO` |

## What is not provided yet

These are recorded here rather than left implicit:

* **No signature yet — BLOCKED.** No release key exists and no fingerprint has been
  published, so verification is a checksum from a trusted channel. Everything that does
  not need a key is implemented and tested — checksum manifests, verification pinned to an
  explicit fingerprint, signatures covering every published file, and the installer's
  pre-flight signature check — and it stays inactive until a key exists
  ([RELEASE_SIGNING.md](RELEASE_SIGNING.md)).
* **No automatic updates.** Upgrading is replacing the file, as described above. Manual
  update is the recommended policy, not a temporary gap.
* **Larger artwork is derived, not supplied.** 128 and 256 px icons ship for HiDPI docks,
  rendered from the approved 48 px artwork by a pinned script (`scripts/build-icons.py`).
  They are upscales of that master rather than an owner-supplied high-resolution master,
  so they still need a human visual check; no SVG artwork exists.
* **No software-centre listing.** The AppImage carries AppStream metadata, but the
  catalogue/store listing is not set up.
* **Wayland, dock and accessibility on 24.04/26.04 are not measured.** Native-window
  evidence for those releases comes from X11 sessions.
* **x86-64 only.** No ARM64 or 32-bit Linux build is offered.

Measured detail behind all of this is in
the private compatibility record,
the private AppImage packaging record and
[DESKTOP_DELIVERY.md](DESKTOP_DELIVERY.md).
