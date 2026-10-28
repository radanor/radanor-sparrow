# This build is unsigned

You are reading this because it shipped beside a Radanor Sparrow Community Edition
artifact. It is a deliberate, disclosed property of the download, not a mistake and
not a missing step in your installation.

## Download security notice

Radanor Sparrow Community Edition is currently distributed without a paid code-signing
certificate.

- **On Windows** you may see an **Unknown Publisher** prompt, and Microsoft **SmartScreen**
  may warn about or block the download.
- **On Linux** the downloads are also provided without a separate publisher signature.
  **SHA-256 checksums are available to verify file integrity, but checksums alone do not
  independently authenticate the publisher.**

Download Sparrow only from the official Radanor site, **radanor.com** — whose download
links resolve to the official GitHub release assets — or from those release assets
directly. **Do not disable your operating system's security protections to install it.**

## What you can verify

A **SHA-256 checksum** in the manifest beside this file proves that the bytes you
have are the bytes the manifest names. If you obtained both the artifact and its
manifest from the official origin over TLS:

- a **mismatch** means the file was corrupted or altered in transit — delete it and
  download it again;
- a **match** means the file arrived intact.

On Linux:

```sh
sha256sum --check --strict SHA256SUMS.appimage
sha256sum --check --strict SHA256SUMS.portable
```

On Windows (PowerShell, run from the download folder):

```powershell
Get-FileHash -Algorithm SHA256 .\RadanorSparrow.exe
Get-FileHash -Algorithm SHA256 .\RadanorSparrow-0.1.1-setup.exe
```

Compare each value with the line for that file in `SHA256SUMS.windows`.

## What a checksum does not prove

**It does not prove who built the file.** A checksum is not a signature. Someone who
could alter the artifact could alter the manifest beside it, and your download would
still match perfectly. What a checksum gives you is integrity relative to the
manifest — not publisher identity, and not authorship.

This build carries **no Authenticode signature** and **no OpenPGP signature**, and no
timestamp from a trusted authority. Nothing in it should be read as a claim that it
does.

## On Windows, expect these warnings

- Windows shows an **Unknown Publisher** warning, because there is no publicly
  trusted publisher certificate.
- **SmartScreen** or **Smart App Control** may block or quarantine the installer for
  the same reason.

**Do not disable SmartScreen, Smart App Control, Defender or any other Windows
security protection to run this software.** If a protection blocks the download, the
honest response is to verify the checksum, confirm you downloaded from the official
origin, and decide whether you trust that origin. Turning the protection off to get
past a warning removes the only control that was telling you something.

## On Linux

There is no OpenPGP signature and no published key fingerprint, so a download cannot
be attributed to a key this project has published. Integrity rests on TLS from the
official origin plus the checksum manifest. If you mirror or redistribute these
files, the checksums prove nothing about your copy.

## What would close the gap

A Windows code-signing certificate with a trusted timestamp, and an OpenPGP release
key with a published fingerprint and a documented custody procedure. Both are
specified in the project's release-signing documentation. Neither exists yet, which
is why this notice exists instead.

## Where the official copies are

There are exactly two origins, and they are the same files:

1. **radanor.com**, the project's official download page. It links to this repository's
   GitHub release assets; it does not serve a separately built copy.
2. **This repository's GitHub releases page**, which hosts the artifacts and the checksum
   manifests those links point at.

Any other mirror, re-upload or "portable download" site is unauthorised, and its checksums
say nothing about where the file came from. If a link you were given does not resolve to one
of the two origins above, treat the file as untrusted no matter what its checksum reads.
