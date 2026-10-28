# Release signing, verification and updates

**Status: BLOCKED — no release key exists, no fingerprint has been published, and no
release is signed.** Checksum manifests and development verification paths are
tested. Production Authenticode trust remains unproven. Signing is wired into
the release workflow and is **mandatory**: a release run without an enabled key,
a published fingerprint and an approved Authenticode signer fails instead of producing
an unsigned artifact. This document is the proposal, the trust model and the
switch-on procedure. The build-tool side of the same problem — which Actions and
which AppImage toolchain may run — is recorded in
[RELEASE_SUPPLY_CHAIN.md](RELEASE_SUPPLY_CHAIN.md).

Nothing here authorises a release: the licensing gate and the approval checklist in
[../licensing/RELEASE_APPROVAL_CHECKLIST.md](../licensing/RELEASE_APPROVAL_CHECKLIST.md)
decide whether a release build may be published at all, and the checklist's item 6 is still
open.

## 1. What is proved today, and what is not

A release publishes, per artifact set:

| File | Purpose |
| --- | --- |
| `SHA256SUMS.appimage` | the AppImage's SHA-256 |
| `SHA256SUMS.portable` | the portable executable, `LICENSE` and `THIRD_PARTY_NOTICES.md` |
| `<artifact>.asc` | *(not produced yet)* detached, armored OpenPGP signature |
| `<artifact>.asc.pub` | *(not produced yet)* the public key that verifies that signature |

A signed release signs **all four published files** — the AppImage, the portable
executable and both checksum manifests — because a signature on one of them says nothing
about the others. The workflow fails if any of the four is unsigned, and
`verify-release.sh --require-all-signed` refuses a set with a gap rather than reporting a
partially signed release as signed.

The two manifests are written by `scripts/write-release-checksums.sh`, which reads the
artifact's own `BUILD-INFO` and refuses to write them unless it records
`variant=release`. A development build therefore cannot travel with the files a release
publishes. That is a statement about identity, not approval: the licensing gate still
decides whether a release build may be published at all.

| Check | Proves | Does **not** prove |
| --- | --- | --- |
| SHA-256 checksum | the file reached you byte-for-byte unchanged since the manifest was written | who wrote the manifest. A manifest can be replaced together with the file, so a checksum from the same download page as the file authenticates nothing |
| OpenPGP signature | the file was signed by the private key matching a public key, and that key has the fingerprint you checked | that the publisher is trustworthy, unless you obtained the fingerprint from somewhere other than the download |

Both checks are needed: the checksum is fast and catches corruption, the signature binds
the artifact to a publisher. Until a key exists, **only integrity is verifiable**, and
that is the honest description of this distribution today.

## 2. Verifying a download

```bash
# Integrity: the artifacts and their manifests, side by side.
sha256sum -c SHA256SUMS.appimage
sha256sum -c SHA256SUMS.portable

# The same checks with the repository's tool, which also reports what it did not check.
bash scripts/verify-release.sh ~/Downloads

# Once signatures exist: --require-signature fails when there is none, and the
# fingerprint you name is what makes the key trusted (see §5). A valid signature by
# any other key fails, even when that key's public key sits in the same directory.
bash scripts/verify-release.sh --require-signature --require-all-signed \
  --expect-fingerprint <40-HEX-FINGERPRINT> --key radanor-release-key.asc ~/Downloads
```

`scripts/verify-release.sh` exits non-zero on any mismatch, verifies signatures against a
keyring containing only the key you name, and compares every signing fingerprint against
the one you supplied. `--require-all-signed` additionally demands a verified signature for
every published file and names the ones that are unsigned. It never fetches anything, and
it never treats a public key found in the download directory as trusted.

The installer can check the manifest, and verify the signature before the image is
unpacked or run:

```bash
bash install-appimage.sh --checksums SHA256SUMS.appimage \
  --signature RadanorSparrow-<version>-x86_64.AppImage.asc \
  --public-key radanor-release-key.asc --fingerprint <40-HEX-FINGERPRINT> \
  RadanorSparrow-<version>-x86_64.AppImage
```

A mismatched or missing entry, or a signature that is missing, invalid or made by a key
other than the fingerprint you name, stops the installation before anything is written and
before the image's runtime is executed.

## 3. Recommended signing mechanism

**Detached, armored OpenPGP signatures, one per artifact, produced by
`scripts/sign-release.sh`.**

```bash
# One release signs four files. With an armored private key (CI secret or an export):
bash scripts/sign-release.sh --armored-key release-key.asc \
  --expect-fingerprint <FINGERPRINT> \
  RadanorSparrow-1.0.0-x86_64.AppImage radanor-sparrow \
  SHA256SUMS.appimage SHA256SUMS.portable

# With a key already in the maintainer's own keyring:
bash scripts/sign-release.sh --key <FINGERPRINT> --expect-fingerprint <FINGERPRINT> \
  RadanorSparrow-1.0.0-x86_64.AppImage radanor-sparrow \
  SHA256SUMS.appimage SHA256SUMS.portable
```

The script writes `<artifact>.asc` and `<artifact>.asc.pub` for each file, verifies every
signature it wrote, and prints the fingerprint and the SHA-256. `--expect-fingerprint` is
the key that is allowed to sign, so a misconfigured secret fails the run instead of
producing a release signed by the wrong key.

It also refuses to sign a development build: the artifact's own `BUILD-INFO` decides, and
anything whose variant is not `release` is rejected unless `--development` is passed
explicitly. That flag exists for a pipeline dry run; it prints that the result is **not** a
release, and a development build therefore cannot be made to look approved by signing it. It **never generates a key, never stores one
in the repository, and never signs with the maintainer's own keyring**: the key is
imported into a temporary, isolated GnuPG home that is destroyed on exit, and if signing
fails part-way the run says so rather than leaving a partial set that looks complete.

Why detached rather than an embedded signature:

* it covers both Linux artifacts, including the portable ELF executable, which is not an
  AppImage; Windows PE artifacts separately carry Authenticode signatures;
* verification needs only `gpg` — no AppImage runtime and no AppImage-specific validator;
  running `--appimage-signature` prints the embedded signature but does not verify it;
* it keeps the reproducible AppImage bytes unchanged: an embedded signature would be part
  of the artifact and would have to enter the reproducibility contract.

The alternative — `appimagetool --sign` (embedded signature) plus `-u`/`--updateinformation`
and a `.zsync` file — becomes attractive only if an AppImage-native update channel is
approved later (§7). It requires the signing key at packaging time and ties updates to
the AppImage ecosystem. If it is adopted, both mechanisms can coexist: the detached
signature remains the artifact-level check.

## 4. Key custody

| Aspect | Requirement |
| --- | --- |
| Key type | A dedicated release signing key (primary offline, or a signing subkey of it), created and held by the maintainer |
| Passphrase | The interactive key must be passphrase-protected. CI uses a separate export whose protection is the secret store itself |
| Where the private key lives | Offline media or the maintainer's own keyring. In CI: `secrets.RADANOR_SIGNING_KEY_ARMORED` only, never in the repository, never in an artifact |
| Where the public key lives | Published beside the artifact (`*.asc.pub`) **and** in a trusted, stable location named in §5 |
| Access | One release signer for the first release; adding a second signer is a deliberate change to this document |
| Rotation | New key, new fingerprint published, old key's revocation certificate held offline; the old key stays published for verifying older releases |
| Revocation | A revocation certificate must exist before the first release, stored apart from the key |

No key material may be committed: `.gitignore` already excludes build output, and no
script in this repository writes a key anywhere except a temporary directory.

## 5. Trust model and public-key distribution

The chain is deliberately short and manual:

1. The maintainer publishes the release key's **full 40-hex fingerprint** in three places
   that a downloader can reach without the download directory: this repository (this
   file, once a key exists), the release notes, and the project website.
2. A user obtains the public key from any of those, imports it, and **compares the
   fingerprint character by character** with one of the other sources.
3. Only then does `gpg --verify` or `verify-release.sh` mean anything.

A key discovery service (for example `keys.openpgp.org`) may be used to *fetch* the key,
but it is not the trust anchor: the anchor is the fingerprint published out of band.
Keys found inside a download directory are never trusted automatically — `verify-release.sh`
imports only the key named on the command line and says so.

## 6. How the release workflow uses this

`.github/workflows/release.yml` has four jobs, and each one either passes its gate or
stops the run:

| Job | Runs on | Refuses to continue when |
| --- | --- | --- |
| `licensing-gate` | `ubuntu-24.04`, no environment | the strict licensing gate finds any placeholder or structural problem |
| `release-authorization` | `ubuntu-24.04`, `environment: production-release` | `vars.RADANOR_PRODUCTION_RELEASE_PROTECTED` is not exactly `verified`; the ref is not a tag; the tag is not `vX.Y.Z`; the manual `version` input disagrees with the tag; the tag does not name a commit on `main`; or the checkout's own version rule reports a different version than the tag |
| `release-artifacts` (Linux) | `ubuntu-24.04`, `environment: production-release` | the AppImage toolchain or runtime digest does not match; `APPIMAGE_RUNTIME` is unset for a release; the image is not `variant=release`; checksums fail; or signing is not enabled with a key and a published fingerprint |
| `release-artifacts` (Windows) | `windows-2025`, `environment: production-release` | `vars.RADANOR_WINDOWS_SIGNING` is not `enabled`; no `vars.RADANOR_AUTHENTICODE_THUMBPRINT` is configured; the portable executable is not validly signed by that thumbprint; or the final setup executable is not signed and timestamped by it |

`release-artifacts` declares `needs: [licensing-gate, release-authorization]`, so no
artifact job can start while either gate is unmet, and the workflow contains no
`continue-on-error`.

Settings the artifact job reads:

| Variable / secret | Effect |
| --- | --- |
| `RADANOR_RELEASE_SIGNING` unset (today) | the Linux job **fails** before uploading: an unsigned artifact is never published as a signed release |
| `RADANOR_RELEASE_SIGNING=enabled` | the AppImage, the portable executable and both checksum manifests are signed from `secrets.RADANOR_SIGNING_KEY_ARMORED`, verified with `--require-signature --require-all-signed --expect-fingerprint`, and the `.asc` / `.asc.pub` files are uploaded as a separate artifact |
| enabled but the secret, or `vars.RADANOR_RELEASE_FINGERPRINT`, is missing or empty | the job **fails** before uploading: a signature nobody can check is not a signed release |
| the key does not match `vars.RADANOR_RELEASE_FINGERPRINT` | `sign-release.sh` refuses and the job fails: a wrong or rotated secret cannot publish under the old fingerprint |
| `RADANOR_WINDOWS_SIGNING` unset (today) | the Windows job **fails** directly after building the portable executable, before the installer is packaged |
| `RADANOR_AUTHENTICODE_THUMBPRINT` | approved certificate identity for portable, embedded uninstaller and setup; no certificate auto-selection |
| `RADANOR_SIGNTOOL`, `RADANOR_SIGNTOOL_SHA256` | absolute installed SDK SignTool path and independently reviewed SHA-256; mismatch fails before execution |
| `RADANOR_TIMESTAMP_URL` | approved HTTPS RFC3161 endpoint; no timestamp-free retry |
| `RADANOR_TIMESTAMP_THUMBPRINT`, `RADANOR_TIMESTAMP_POLICY_OID` | approved TSA leaf identity and signed TSTInfo policy OID; rotation requires review |

Windows order is: build portable; sign and verify portable; update only its
whole-file checksum in `BUILD-INFO`; provision pinned NSIS; sign and verify its
generated uninstaller using `!uninstfinalize ... = 0`; compile setup; reconstruct
and verify its embedded uninstaller and exact payload; sign and verify setup;
reconstruct and verify again; write checksums; upload. Section 10 defines the
strict verification profile and its limits. Development packaging uses no hook.

### Controls that workflow YAML cannot prove

Several properties of this design live in GitHub settings, not in the repository, and a
workflow expression cannot check them:

* that the `production-release` environment exists and is restricted to protected `v*`
  tags — `radanor-admin` creates it; the workflow only *uses* it;
* that a required reviewer other than the person who triggered the run is configured, and
  that self-review is blocked where the plan supports it;
* that `vars.RADANOR_PRODUCTION_RELEASE_PROTECTED=verified` was set **after** those rules
  were verified and not merely because the run needed to get past this gate;
* that `main` and the `v*` tag namespace are protected against force-push, deletion and
  unauthorised creation.

The attestation variable is an operator's statement, not cryptographic proof. The
ancestry check is the part that is machine-verified: it proves the tagged commit is on the
protected `main` line, but it cannot prove that a human reviewed it. Both facts are
recorded as administrator verification items in
[RELEASE_SUPPLY_CHAIN.md](RELEASE_SUPPLY_CHAIN.md) §5 and in the P9 record.

Windows checksums are written after all Authenticode operations. Linux manifests
precede their detached signatures. Nothing is uploaded if any gate fails.

## 7. Updates: manual only

**Recommended and current policy: manual update.** The user downloads the new AppImage,
verifies it, replaces the file (or re-runs the installer), and keeps the old image until
satisfied. Nothing in the product contacts the network, and no automatic updater is
implemented or proposed for the first release.

An authenticated update channel is a separate decision with its own requirements, and
must not be built before them:

* an approved channel that serves artifacts over HTTPS with range support;
* `.zsync` delta metadata (or an equivalent) generated per release and signed;
* the AppImage embedding update information (`appimagetool -u`), which changes the
  artifact bytes and therefore the reproducibility contract;
* signature verification **before** the update is applied, with a documented failure mode;
* a rollback path that keeps the previous image and the database intact;
* explicit sign-off, since an update channel is remote code delivery to users.

Until then, treat any page or tool claiming to update Radanor Sparrow automatically as
unofficial.

## 8. Enablement checklist (ordered)

1. Generate the release key offline with a passphrase and a revocation certificate
   (§4). Not done by any script here.
2. Publish the fingerprint in the repository, the release notes and the site (§5).
3. After the `production-release` environment exists with its protection rules, set
   `vars.RADANOR_RELEASE_SIGNING=enabled`, `vars.RADANOR_RELEASE_FINGERPRINT` to the
   published fingerprint and the `RADANOR_SIGNING_KEY_ARMORED` secret **on that
   environment**, not at repository scope, so a job outside the protected environment
   cannot read them. Set `vars.RADANOR_PRODUCTION_RELEASE_PROTECTED=verified` only once
   the rules have been checked.
4. Resolve the licensing gate
   ([RELEASE_APPROVAL_CHECKLIST.md](../licensing/RELEASE_APPROVAL_CHECKLIST.md)): a
   signature on an artifact that may not be published is worthless.
5. Run a release and confirm the workflow signs all four files, verifies with
   `--require-signature --require-all-signed --expect-fingerprint`, uploads `.asc` and
   `.asc.pub`, and that `verify-release.sh` passes on the downloaded files from a clean
   machine with the published fingerprint.
6. Record the fingerprint and the release's SHA-256 in the release notes.

Steps 1, 2 and 6 need the maintainer; steps 3–5 need the licensing decisions first.
Until step 5 has happened once and been recorded, the signing path is **implemented but
unproven in a real release**, and this document says so rather than implying otherwise.

## 9. What is tested without a key

`internal/releasecheck/release_process_test.go` proves, on every `go test ./...`:

* the release workflow keeps the licensing gate first, builds the native-window AppImage
  through the release path, verifies the packaged helper and release variant, publishes
  checksums, verifies them, and never creates a GitHub Release by itself;
* the **authorization gate's own shell script is executed** against a throwaway repository
  with a tag on `main` and one off it: it refuses a missing attestation, a branch trigger,
  a non-release tag name, a requested version that disagrees with the tag, a tag on an
  unreviewed branch, and a tag whose version the checkout does not build as — and admits
  the one authorized combination;
* the Windows release stages keep their order (signed portable executable, installer built
  from it, final signature and timestamp check, checksums, then upload), and no step in
  the release workflow is allowed to fail without failing the run;
* every action reference in `.github/workflows` is a full 40-hex commit that appears in the
  reviewed pin inventory, so an unreviewed pin fails the build;
* the AppImage initializer refuses a cached tool whose digest does not match, and the
  packaging script cannot let a release fetch its own type-2 runtime;
* checksum manifests cover exactly the published files, with their real digests, and the
  writer refuses a directory with two AppImages or a missing `LICENSE`;
* a tampered artifact fails verification;
* `--require-signature` without a key, and without a trusted fingerprint, is a usage
  error rather than a pass;
* a **valid signature by another key fails**, and says so, even when that key's public key
  accompanies the artifact;
* a **partially signed set fails** under `--require-all-signed`, and the refusal names the
  artifacts that have no signature;
* signing **refuses a development build** unless `--development` asks for a dry run, so a
  signature cannot turn a development artifact into something that looks approved;
* signing **refuses a key that is not the expected fingerprint**, so a wrong CI secret
  cannot publish;
* signing without a key refuses and states that release signing is blocked;
* the signing mechanism itself works end to end, using a throwaway key generated in a
  temporary GnuPG home, including detecting a tampered artifact afterwards.

The installer's half of this is in `internal/appimage/install_security_test.go`: a
signature is checked before the image's runtime is executed, a tampered image and a
signature by an untrusted key are both refused before anything is unpacked, and an
interrupted or refused upgrade leaves the previous installation and its database intact.

## 10. Windows Authenticode profile (P10.2)

`Sign-SparrowWindowsArtifact.ps1` supports the installed CurrentUser `My`
certificate-store provider (including a compatible provisioned hardware-backed
provider). It imports/exports no keys, downloads nothing, and selects only the
configured full certificate thumbprint. Provider provisioning, individual publisher
identity, SDK tool provenance/hash and production custody still need approval.
Other cloud signing protocols are not silently substituted. No secrets were added.

SignTool uses `/fd SHA256 /tr <approved-HTTPS-URL> /td SHA256`. Every native
nonzero result, including warning exit 2, fails. There is no unsigned or
untimestamped fallback. Signing happens in a sibling temporary copy; the original
is replaced only after verification and a normalized payload-byte hash agrees.
That comparison excludes only the PE checksum, security-directory entry and final
certificate table, normalizing zero alignment padding. It is an invariant of this
signing transaction, **not** a substitute for Authenticode hashing or trust.
Only the portable checksum field is then updated; source-derived identity remains.
An interruption can leave a stale checksum record: packaging rejects it, and a
fresh build is required. This is not a multi-file atomic signing transaction.

`Assert-SparrowAuthenticode.ps1` requires all of:

- One embedded PE signature and the approved signer thumbprint; no catalog-only,
  nested or multiple-signature profile is accepted.
- SHA-256, SHA-384 or SHA-512 OIDs for the file digest and CMS signer digest.
- Exactly one RFC3161 attribute, a mathematically valid token, approved TSA leaf
  and policy OID, a strong timestamp CMS/message-imprint digest, and an imprint
  bound to the actual file signer's encrypted signature.
- Timestamp `genTime` within the code signer's validity, not in the future beyond
  five minutes; exclusive critical time-stamping EKU; a trusted timestamp-EKU
  X509 chain at `genTime`, online revocation, no ignored errors. Unavailable
  revocation evidence fails closed. The build runner therefore needs reliable
  clock, trusted roots and approved CA/TSA revocation access.
- Windows `Get-AuthenticodeSignature` **Valid**, matching signer and timestamp
  identities, then the approved SDK SignTool `verify /pa /all /v /tw` with exit 0.
  File hashes before/after verification must agree.

The certificate thumbprint is its Windows SHA-1 identity fingerprint; this does
not permit SHA-1 **file signatures**. Certificate renewals and TSA rotations must
update approved configuration deliberately. Legacy Authenticode countersignatures
are rejected; only the configured RFC3161 profile is supported.

Windows APIs/SignTool check the file's Authenticode digest, signature mathematics
and Windows trust policy. CMS parsing separately enforces digest, binding and
policy evidence: merely finding a timestamp certificate cannot pass. These checks
depend on the runner's root store, clock, network/revocation state, Windows SIPs
and approved verifier binary. They do not prove publisher authority, legal rights,
environment protection, key custody, malware safety, SmartScreen reputation, TSA
operational honesty or a future machine's trust state. An expired code-signing
certificate is acceptable only through this verified trusted timestamp path; no
expiration error is manually ignored.

`Assert-SparrowInstallerSigning.ps1` uses verified 7-Zip to reconstruct the actual
`WriteUninstaller` output without executing setup. Its SHA-256 must equal the
signed hook evidence, it must independently pass the full verifier, and all four
portable payload files must match. Setup signing calls this first; the final
release gate repeats it after setup signing. No install/uninstall runtime safety
or profile-data behavior changes.

Development acceptance uses non-exportable, disposable CurrentUser `My` code and
timestamp certificates, removes their keys in `finally`, and never trusts a test
root. Locally signed RFC3161 fixtures test invalid signatures, wrong imprints,
invalid times, wrong policy and untrusted chains; they are **not trusted timestamp
acceptance**. Approved public-chain timestamps and expiration survival remain
**NOT RUN** until real approved infrastructure exists. See
the private signing acceptance record.

Primary references: [NSIS uninstaller finalization](https://nsis.sourceforge.io/Docs/Chapter5.html#uninstfinalize),
[Microsoft SignTool options and exit codes](https://learn.microsoft.com/en-us/windows/win32/seccrypto/signtool),
[RFC3161](https://www.rfc-editor.org/rfc/rfc3161).
