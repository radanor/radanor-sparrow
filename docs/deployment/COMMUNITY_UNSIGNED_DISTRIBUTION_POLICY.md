# Community Edition unsigned distribution policy

**Status: in force for v0.1.0.** The rights holder approved this amendment on
2026-10-28, and the approval record described in §8 is held privately. Nothing here
relaxes the signed path: `docs/deployment/RELEASE_SIGNING.md` still states that
signing is **mandatory** there, and a release run without an enabled key and a
published fingerprint fails rather than producing a signed claim. This document
defines a *second*, separately named process for artifacts that are deliberately
unsigned, and the workflow it describes fails closed on every run until both
approval variables read `approved`.

## 1. Why an amendment rather than a workaround

Radanor Sparrow Community Edition should reach users without first buying a
Windows code-signing certificate or provisioning an OpenPGP release key. Neither
credential is a technical prerequisite for a working, verifiable download; both are
prerequisites for a *trusted publisher identity*. Those are different things and the
project should be able to say which one it has.

The existing policy forbids publishing an unsigned artifact **as a signed release**.
It does not need to forbid publishing an artifact that is openly, loudly unsigned.
So this amendment adds a process that is:

- separately named (`community-release`, never `Release`),
- separately authorized (two independent attestation variables, §4),
- structurally incapable of producing a signed-looking artifact (§6),
- unable to reach the production environment, the production signing variables or
  the `production-release` environment, and
- honest in the artifact itself about what a SHA-256 checksum does and does not
  prove (§7).

## 2. Scope

| In scope | Out of scope |
| --- | --- |
| Linux AppImage (native-window) and the portable Linux executable, unsigned | Any artifact presented as Attested, Authenticode-signed or OpenPGP-signed |
| Windows portable `RadanorSparrow.exe` and the per-user NSIS installer, unsigned (the installer is built with `--community-unsigned`, which defines no signing hook, so its uninstaller is unsigned too) | Windows code signing, timestamping, or a signed embedded uninstaller |
| Community Edition v0.1.0 and later versions the owner approves individually | Any commercial edition, and any artifact built outside the release path |

An artifact produced under this policy is a **Community Edition unsigned build**.
It is never "the release". The signed process in `.github/workflows/release.yml`
remains the only way to publish a signed artifact, and remains blocked until real
credentials exist.

## 3. What is preserved

This amendment changes nothing in the signed path:

- `.github/workflows/release.yml` is not modified by it.
- `RADANOR_WINDOWS_SIGNING`, `RADANOR_AUTHENTICODE_THUMBPRINT`,
  `RADANOR_TIMESTAMP_*`, `RADANOR_RELEASE_SIGNING`, `RADANOR_SIGNING_KEY_ARMORED`
  and `RADANOR_RELEASE_FINGERPRINT` keep their exact current meanings.
- `scripts/build-windows-installer.sh --release` still **refuses to run** unless
  `RADANOR_WINDOWS_SIGNING=enabled` and signed-uninstaller evidence is supplied.
- `scripts/write-release-checksums.sh` and
  `scripts/write-windows-release-checksums.sh` still refuse a non-`release` variant.
- The strict licensing gate runs first in both processes and is never relaxed.

## 4. Authorization

Two independent attestations are required, both read from the `community-release`
GitHub environment. Both must read exactly `approved`.

| Variable | Meaning | Absent or different |
| --- | --- | --- |
| `RADANOR_COMMUNITY_UNSIGNED_RELEASE` | The owner has approved unsigned Community Edition distribution under this policy. | The workflow stops before building anything. |
| `RADANOR_UNSIGNED_WINDOWS_DISTRIBUTION` | The owner has separately approved distributing an **unsigned Windows** build, understanding the Unknown Publisher and SmartScreen consequences in §7. | The Windows job is skipped; the Linux job is unaffected. |

The unattested default is the current behaviour: no artifacts. "Not configured" and
"declined" are the same state, and neither produces a download.

Windows packaging rechecks both approval variables in its own process, in addition
to `RADANOR_COMMUNITY_UNSIGNED=acknowledged`; that acknowledgement alone is not an
owner approval. The workflow passes the configured environment values, never
invented approvals. These remain administrator/owner attestations, not proof that
a person approved them. The policy approval table below must still be completed.

Installer compiler policy is separate from provenance: `PACKAGING_MODE` is
`development`, `signed-release`, or `community-unsigned`. Development requires
development identity; both other modes require release identity. A generic release
identity defaults to signed-release and still requires signing hooks. Only explicit
Community mode omits them, and it rejects either hook define. The build script
selects it only after the existing legal, approval, clean-tag and identity checks.
Before packaging and again after reconstruction, Windows APIs require the EXE,
setup and embedded uninstaller to be NotSigned with no certificate table/timestamp;
all four embedded portable payload files must match. Unknown/invalid signatures
are rejected, not treated as unsigned.

## 5. Additional gates the process enforces

Beyond the licensing gate and the two attestations, every run must pass all of:

| Control | Enforced by |
| --- | --- |
| The strict BSL licensing gate runs **before** any artifact is built | `go run ./cmd/radanor-release-check` as the first job, and again against each packaged directory |
| The ref is an exact `vX.Y.Z` tag | regex on `GITHUB_REF_NAME`, `GITHUB_REF_TYPE == tag` |
| The requested version equals the tag version | comparison against the dispatch input |
| The tag names a commit on `main` | `git merge-base --is-ancestor` against `origin/main` |
| The tag version equals the version this checkout builds | `go run ./cmd/radanor-version -release` |
| The build is not dirty and not a development build | the version rule refuses a dirty tree; both packaging scripts refuse a non-`release` variant |
| The AppImage toolchain is the pinned, digest-verified one | `validation/linux/Initialize-SparrowAppImageTool.sh` |
| The NSIS toolchain is the pinned, verified one | `validation/windows/Initialize-SparrowNsis.ps1` |
| Checksums are computed from the final artifacts, after any packaging | the two checksum scripts, each verifying its own manifest with `sha256sum --check --strict` |
| Nothing is published | the workflow has `permissions: contents: read` and creates no GitHub Release |
| No signing variable is enabled | the authorization job **fails** if `RADANOR_WINDOWS_SIGNING` or `RADANOR_RELEASE_SIGNING` reads `enabled`, because that artifact belongs in the signed process |
| Developer artifacts stay separate from release candidates | an unsigned run uploads its files only under a distinct `radanor-sparrow-community-unsigned-*` artifact name, and a development build cannot obtain a release checksum manifest at all (both checksum scripts refuse a non-`release` variant) |

## 6. What the process must never do

Restated as prohibitions, because each has a tempting shortcut:

1. It must not produce, embed, or imply an Authenticode signature, an OpenPGP
   signature, or an RFC3161 timestamp.
2. It must not generate a self-signed or disposable certificate and present it as
   publisher identity. A disposable certificate may exist only in a negative test,
   never in a distributed artifact.
3. It must not label an artifact "signed", "verified publisher", or "trusted".
4. It must not reuse the `production-release` environment, its variables or its
   secrets.
5. It must not publish a GitHub Release, upload to a release page, or push a tag.
6. It must not weaken, bypass or reword the licensing gate.
7. It must not build from a dirty tree, a development variant, or a commit that is
   not on `main` under an exact tag.

## 7. The reduced guarantee, stated plainly

This text ships beside the artifacts as `UNSIGNED-DISTRIBUTION.md`:

- **A SHA-256 checksum proves the file you have is the file named in the manifest.**
  If you obtained both from the official origin over TLS, a mismatch means the
  download was corrupted or altered in transit, and a match means it was not.
- **A checksum proves nothing about who built the file.** It is not a signature. A
  copy that arrived alongside a forged manifest would match that manifest perfectly.
- **Consequences on Windows:** no publicly trusted publisher signature, so Windows
  shows an *Unknown Publisher* warning, and SmartScreen or Smart App Control may
  block or quarantine the installer. **Do not disable those protections to run it.**
  If SmartScreen blocks it, the correct response is to verify the checksum and decide
  whether you trust the origin — not to turn the protection off.
- **Consequences on Linux:** no OpenPGP signature and no published fingerprint, so
  a download cannot be attributed to a key the project has published. Integrity
  rests on TLS from the official origin plus the checksum manifest.
- **What would close the gap:** a Windows code-signing certificate with an RFC3161
  timestamp fixture, and an OpenPGP release key with a published fingerprint and a
  custody procedure. Both are specified in [`RELEASE_SIGNING.md`](RELEASE_SIGNING.md)
  and neither is required for an unsigned build.

The official origin is the project's own download page **radanor.com**, whose links resolve to
the project's own GitHub releases page; the artifacts themselves are hosted there. Any other
mirror is unauthorised and its checksums prove nothing about the file's provenance.

## 8. Approval record

This amendment is in force only when this table is completed by the rights holder.
An engineer may not fill it in, and the workflow refuses to build until
`RADANOR_COMMUNITY_UNSIGNED_RELEASE` is set to `approved` in the repository's
`community-release` environment — a second, independent act.

| Decision | Approved by | Date |
| --- | --- | --- |
| Adopt unsigned Community Edition distribution for Linux | Approved — 2026-10-28, rights holder | record held privately |
| Adopt unsigned Community Edition distribution for Windows (`RADANOR_UNSIGNED_WINDOWS_DISTRIBUTION`) | Approved — 2026-10-28, rights holder | record held privately |
| Accept the §7 disclosure text as the user-facing statement of guarantees | Approved — 2026-10-28, rights holder | record held privately |

Signed: ______________________  **Radheshyam Dangi**, rights holder and Licensor.

Adopting this policy is the one-time act this table records. Authorising a
particular release under it — including the third-party conclusions and the publisher
identity — is recorded per version in the rights holder's decision record, which is
held privately and is the record the v0.1.0 workflow variables refer to. Signing one
does not sign the other.

## 9. Sunset

This amendment exists because no signing credential exists yet. When a Windows
certificate and a Linux release key are available and trusted, the owner should
withdraw it: at that point the signed process in `release.yml` can publish both
platforms, and the unsigned process should be deleted rather than left dormant.
Until then, an unsigned artifact that carries the §7 disclosure is more honest than
no artifact plus a claim of readiness.
