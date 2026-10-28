# Verify your download

A published file can be damaged in transit, truncated by a proxy, or replaced by
something else entirely. A SHA-256 checksum comparison is how you tell the first two from
the third. This page gives the exact commands for Windows and Ubuntu, and it is explicit
about what the result does and does not prove.

**Time needed:** about a minute.

## Where the official files are

Downloads are published in one place: the project's own **GitHub Releases** page for the
Radanor Sparrow repository. The official Radanor site, **radanor.com**, is a front door to
those same files — its download links resolve to this repository's release assets rather than
to a separately built copy. Any other site, mirror, forum attachment or package re-upload is
unauthorised, and its checksum files prove nothing about where the binaries came from.

A release publishes these files:

| File | Platform | What it is |
| --- | --- | --- |
| `RadanorSparrow.exe` | Windows | portable executable |
| `RadanorSparrow-<version>-setup.exe` | Windows | NSIS installer |
| `SHA256SUMS.windows` | Windows | the manifest to compare against |
| `RadanorSparrow-<version>-x86_64.AppImage` | Linux | the desktop application |
| `radanor-sparrow` | Linux | portable supervisor, no GTK/WebKitGTK needed |
| `SHA256SUMS.appimage` | Linux | manifest for the AppImage |
| `SHA256SUMS.portable` | Linux | manifest for the portable supervisor and documents |
| `LICENSE`, `THIRD_PARTY_NOTICES.md`, `BUILD-INFO` | both | licence, notices, build identity |
| `UNSIGNED-DISTRIBUTION.md` | both | the unsigned-build disclosure |

Download the artifact **and** its manifest from that same page, into the same folder.

## Windows — PowerShell

Run PowerShell in the download folder. `Get-FileHash` is built in; nothing needs
installing.

```powershell
cd $HOME\Downloads

# 1. Compute the hash of the file you downloaded
Get-FileHash -Algorithm SHA256 .\RadanorSparrow.exe
Get-FileHash -Algorithm SHA256 .\RadanorSparrow-<version>-setup.exe

# 2. Show the published manifest
Get-Content .\SHA256SUMS.windows
```

Each `Get-FileHash` line prints a `Hash` value — 64 hexadecimal characters. Find the line
in `SHA256SUMS.windows` for the same file name and compare the two, character for
character.

To script the comparison rather than eyeball it:

```powershell
$manifest = @{}
Get-Content .\SHA256SUMS.windows | ForEach-Object {
  if ($_ -match '^([0-9a-fA-F]{64})\s+\*?(.+)$') { $manifest[$Matches[2].Trim()] = $Matches[1].ToLower() }
}
foreach ($file in 'RadanorSparrow.exe', 'RadanorSparrow-<version>-setup.exe') {
  $actual = (Get-FileHash -Algorithm SHA256 ".\$file").Hash.ToLower()
  if ($manifest.ContainsKey($file) -and $manifest[$file] -eq $actual) {
    Write-Host "OK      $file"
  } else {
    Write-Host "MISMATCH $file  (expected $($manifest[$file]), got $actual)"
  }
}
```

On PowerShell 7 you can also use `Get-FileHash -Algorithm SHA256 -Path <file> | Select-Object Hash`,
which behaves identically for this purpose.

## Ubuntu / Linux — `sha256sum`

```bash
cd ~/Downloads

# The AppImage
sha256sum -c --strict SHA256SUMS.appimage
# RadanorSparrow-<version>-x86_64.AppImage: OK

# The portable supervisor and the documents
sha256sum -c --strict SHA256SUMS.portable
```

`-c` means "check this manifest"; `--strict` makes `sha256sum` fail on a malformed
manifest line instead of ignoring it. A single `OK` per file is the pass condition. Any
`FAILED` line means the file is not the published file.

If you only want the value to compare by hand:

```bash
sha256sum RadanorSparrow-<version>-x86_64.AppImage
```

## What a matching checksum does and does not prove

**It proves:** the file you have is byte-for-byte the file the manifest describes. That
catches a corrupted download, a truncated transfer, a mangled USB copy, and a file that
was altered after publication *if* the manifest was obtained independently.

**It does not prove:** that the file came from the publisher, that whoever produced it
had good intentions, or that the manifest itself is authentic. A checksum is not a
signature. Someone able to replace the download could replace the manifest beside it, and
your comparison would still pass perfectly.

That distinction matters more for these builds than for most software:

- **The Windows artifacts carry no Authenticode signature and no trusted timestamp.**
  Windows will show *Unknown Publisher*, and
  `Get-AuthenticodeSignature .\RadanorSparrow.exe` returns **`NotSigned`** with a null
  signer certificate. That is the correct, expected result for this release — not a
  sign that your download is corrupted.
- **The Linux artifacts carry no OpenPGP signature and no published key fingerprint.**
  There is no key to attribute a download to.

So for an unsigned release, the practical chain is: TLS to the official origin + a
checksum manifest from that same origin + your own judgement about the origin. That is a
weaker guarantee than a signed release, and it is stated plainly rather than dressed up.

## Check that the build is the one you asked for

The checksum tells you the bytes are intact. `BUILD-INFO` tells you *what* those bytes
are — and a correctly-hashed development build is still not a release.

**Windows:** `Get-Content .\BUILD-INFO` beside the executable, or right-click the
executable → **Properties → Details** for its version.

**Linux:**

```bash
./RadanorSparrow-<version>-x86_64.AppImage --appimage-extract 'usr/share/radanor-sparrow/BUILD-INFO'
cat squashfs-root/usr/share/radanor-sparrow/BUILD-INFO
```

A published release records `variant=release`. If a file says `variant=development`, it is
a development build — do not treat it as a release, whatever its name.

## When the release is signed

Signing is not in place yet: no code-signing certificate and no OpenPGP release key
exist. If a future release is signed, the directory will also carry signatures and the
public key, and verification will bind the file to the publisher — but only if you
compare the key's fingerprint with one you obtained somewhere other than the download
page.

The Linux verification tool already implements that pinning:

```bash
bash verify-release.sh --require-signature --require-all-signed \
  --expect-fingerprint <40-HEX-FINGERPRINT> --key radanor-release-key.asc .
```

A valid signature by any other key is refused, and so is a release in which one of the
files has no signature. On Windows, a signed release will show a real publisher name in
the Authenticode signature and the SmartScreen prompt will no longer say *Unknown
Publisher*. The mechanism and its trust model are in
[`RELEASE_SIGNING.md`](../deployment/RELEASE_SIGNING.md).

## If the checksum does not match

1. **Do not run the file.**
2. Delete it and download again from the official Releases page — a partial download is
   the most common cause.
3. If the second download still mismatches, stop. Do not "try anyway", and do not go
   looking for a mirror that produces a matching value. Report what you saw
   ([`SUPPORT_AND_SECURITY_REPORTING.md`](SUPPORT_AND_SECURITY_REPORTING.md)), including
   the file name, the expected hash and the hash you computed.

A mismatch on a file that other people verified successfully is worth reporting: it can
indicate a compromised mirror, a hostile proxy, or a replaced manifest.

## Related documents

- [`WINDOWS_INSTALLATION_GUIDE.md`](WINDOWS_INSTALLATION_GUIDE.md) — installing on Windows.
- [`LINUX_INSTALL_GUIDE.md`](../deployment/LINUX_INSTALL_GUIDE.md) — installing on Ubuntu.
- [`UNSIGNED_DISTRIBUTION_NOTICE.md`](../deployment/UNSIGNED_DISTRIBUTION_NOTICE.md) — the
  disclosure that ships beside the artifacts.
- [`RELEASE_SIGNING.md`](../deployment/RELEASE_SIGNING.md) — what a signature adds, and
  the still-incomplete key and trust model.
