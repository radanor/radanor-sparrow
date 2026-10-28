# v0.1.0 final release checklist

**This is the single authoritative gate list for publishing Radanor Sparrow Community
Edition v0.1.0.** An item is satisfied only when the evidence named beside it exists. Any
item that is `BLOCKED` or `NOT RUN` stops publication.

**Current result: publication is NOT authorized.** Ten items are `PASS` outright, two are
`PASS` with a recorded finding, and three more are `PASS` in the half that engineering
controls. Five rows carry a `BLOCKED`: an administrative control, a credential or a human
visual decision that nobody has recorded. Six more are executable only against the final
tagged artifacts, so they are `NOT RUN`. Neither group is a formality: no item in this table
may be marked `PASS` because it is expected to pass.

The licensing rows moved on **2026-10-28**: row 1 is closed by an owner **risk decision**
(the legal interpretation stays open) and row 18 now passes. That is a policy outcome, not
third-party legal certification, and the wording of both rows says so.

Status vocabulary — used strictly:

| Status | Meaning |
| --- | --- |
| `PASS` | Verified on this repository, with the evidence named. |
| `BLOCKED` | Cannot proceed until a named external decision, credential or approval exists. |
| `NOT RUN` | Executable in principle, but not executed against the final tagged release artifacts. **Not** a pass. |
| `NOT APPLICABLE` | Does not apply to this release, with the reason given. |

## 1. The checklist

| # | Requirement | Owner | Status | Evidence |
| --- | --- | --- | --- | --- |
| 1 | **Approved licensing conclusions** — the LGPL GTK/WebKitGTK position and the `modernc.org/libc` header-derived material, approved here as an owner **risk decision** rather than a legal conclusion | Rights holder (risk decision), with counsel recommended | **PASS with a recorded finding** | **Risk decision by the rights holder, 2026-10-28** ("I approve D1"), recorded in the rights holder's decision record, which is held privately. **The legal interpretation of F1/F2 remains open and no legal certification is claimed**; the finding is that residual uncertainty. Facts behind it: 0 LGPL libraries bundled, 0 `RPATH`/`RUNPATH`, complete LGPL-2.1 and GCC-exception texts shipped, notice now stating that the libraries and their use are covered (row 19) |
| 2 | **Copyright and ownership** — Radheshyam Dangi named as Licensor and holder, © 2026 | Rights holder (declared) | **PASS** | [`LICENSE`](../../LICENSE) names both; `internal/releasecheck` enforces that the © line names the same party as the Licensor |
| 3 | **BSL parameters** — BSL 1.1, Change Date 2030-10-27, GPLv3 Change License, Additional Use Grant unchanged | Rights holder (declared) | **PASS** | `LICENSE`; the gate verifies the standardized text is verbatim, the Change License is GPLv3, and the Change Date is within the four-year maximum |
| 4 | **Signed owner decisions** D1–D6 | Rights holder | **BLOCKED** | D2, D3, D4 and D5 are recorded from the owner's declarations of **2026-10-28** (owner-provided; **identity not independently verified**). D5 accepts the disclosure text, which now names **radanor.com** as the official download route in front of the GitHub release assets. D1 and D6 still read awaiting, so the row stays blocked |
| 5 | **GitHub environment protection** — `community-release` (and `production-release`) with reviewers restricted to `v*` tags | `radanor-admin` | **NOT RUN** | Workflows *require* the environment and its approval variables, but a workflow cannot prove reviewers or tag restrictions are configured. No authenticated settings inspection was performed |
| 6 | **Protected release tag** | `radanor-admin` + release manager | **NOT RUN** | No `v0.1.0` tag exists. Tag protection rules are unverified |
| 7 | **Exact release commit** — clean tree, tag on `main`, version equal to the tag | Release manager | **NOT RUN** | `radanor-version -release` refuses an untagged or dirty tree; it has not been run against a real tag |
| 8 | **Windows native acceptance (development builds)** | Engineering | **PASS** | Windows community acceptance report (held privately): install, repair, A→B upgrade, downgrade refusal, staged-failure and killed-installer recovery, uninstall, data and unknown-file preservation, real tray mouse interaction |
| 9 | **Windows native acceptance (final release artifacts)** | Engineering, on a Windows host | **NOT RUN** | No release-variant Windows artifact has ever been built; only development builds and `SIMULATION-NOT-A-RELEASE` compiler fixtures exist |
| 10 | **Linux AppImage acceptance** | Engineering | **PASS** | Linux AppImage acceptance report (held privately): `appimage-smoke.sh --require-native-window` → 30 passed, 0 failed, 0 skipped, with a real native window, confirmed sandbox and no zombie |
| 11 | **Linux acceptance of the final tagged artifact** | Engineering | **NOT RUN** | The accepted artifact was built from a working tree, not from a clean tag |
| 12 | **Reproducibility** | Engineering | **PASS** (mechanism) / **NOT RUN** (final tag) | Two clean clones of `81745ba` produced byte-identical AppImages ([`RELEASE_SUPPLY_CHAIN.md`](../deployment/RELEASE_SUPPLY_CHAIN.md) §2). Must be re-proven at the release tag |
| 13 | **SHA-256 checksums** | Engineering | **PASS** (mechanism) / **NOT RUN** (final manifests) | `scripts/write-release-checksums.sh` and `write-windows-release-checksums.sh` refuse any artifact whose `BUILD-INFO` is not `variant=release`; both were exercised and refused a development artifact as designed |
| 14 | **Installer payload integrity** — signed or unsigned mode, exact payload, reconstructed uninstaller | Engineering | **PASS** (verifier) / **BLOCKED** (production signing) | `Assert-SparrowUnsignedWindows.ps1` verifies portable, setup and the reconstructed embedded uninstaller independently; the signed-release path is unchanged and still requires a certificate that does not exist |
| 15 | **No development artifacts presented as a release** | Engineering | **PASS** | Both release build paths and both checksum writers refuse `variant=development`; the release workflows run the strict gate first |
| 16 | **Immutable GitHub Actions pins and least privilege** | Engineering | **PASS** | Every action is pinned to a full commit SHA with a version comment; `internal/releasecheck` asserts the pins and permissions ([`RELEASE_SUPPLY_CHAIN.md`](../deployment/RELEASE_SUPPLY_CHAIN.md)) |
| 17 | **Pinned build toolchains** — AppImage runtime/appimagetool, NSIS 3.13-2, 7-Zip | Engineering | **PASS** | SHA-256 verified before execution (`Initialize-SparrowAppImageTool.sh`, `Initialize-SparrowNsis.ps1`); both fail closed on mismatch |
| 18 | **Strict licensing gate** | Engineering | **PASS** | `go run ./cmd/radanor-release-check` → **exit 0**, no findings, re-run after the D1 risk decision and the marker removal. The gate checks metadata completeness and structure; it does not encode the legal interpretation, which row 1 records as accepted risk rather than settled |
| 19 | **Third-party notices complete** | Engineering | **PASS** | Dependency inventory and obligations in [`THIRD_PARTY_LICENSE_COMPLIANCE.md`](THIRD_PARTY_LICENSE_COMPLIANCE.md). `THIRD_PARTY_NOTICES.md` appendix F reproduces the complete LGPL-2.1 text and appendix G the GCC Runtime Library Exception, so a recipient holds the terms rather than a reference to them. Whether anything *further* is owed is row 1, not this row |
| 20 | **Artwork acceptance** — 128 px and 256 px launcher icons | Rights holder / designer | **BLOCKED** | The icons are rendered from the approved 48 px master by a pinned script and pass dimension/format checks, but the upscale from a low-resolution master needs a human visual decision |
| 21 | **Secret scanning** | Engineering | **PASS with a recorded finding** | `gitleaks` over the tree reports one finding: the GnuPG flag `--export-secret-keys` inside a path-translation fixture, with no key material. It is recorded rather than suppressed — a suppression was tested and rejected because it also hid a synthetic credential in the same file |
| 22 | **Release notes and installation guidance** | Engineering | **PASS** | [`RELEASE_NOTES_V0_1_0.md`](RELEASE_NOTES_V0_1_0.md), [`WINDOWS_INSTALLATION_GUIDE.md`](WINDOWS_INSTALLATION_GUIDE.md), [`LINUX_INSTALL_GUIDE.md`](../deployment/LINUX_INSTALL_GUIDE.md), [`VERIFY_DOWNLOADS.md`](VERIFY_DOWNLOADS.md) |
| 23 | **Correct download links** | Release manager | **NOT RUN** | No release page exists, so no asset URL can be checked. [`GITHUB_RELEASE_V0_1_0.md`](GITHUB_RELEASE_V0_1_0.md) carries marked fields instead of invented URLs |
| 24 | **Recorded first public distribution date** | Rights holder + release manager | **BLOCKED until publication** | `LICENSE` records 2026-10-27 as the **intended** date and states that nothing had been distributed when it was set. The actual date must be confirmed at publication; if it falls after 2030-10-27 the Change Date must be re-approved |
| 25 | **Public publication authorization** | Rights holder | **BLOCKED** | Decision D6 plus rows 4, 5, 9, 20 and 24. Row 1 is no longer among them, so publication is now blocked by administration, untested release artifacts and a visual decision — not by licensing |

## 2. The compliance change, and the documents this set defers to

The compliance change (`feature/sparrow-v0.1.0-final-compliance`) is **merged into
`develop` at `9f8387f`** — verified by ancestry (`git merge-base --is-ancestor d944b45
origin/develop`), not by assuming the pull request description was current. It supplied
the following, which this documentation set references rather than copies, so that each
fact has one source of truth:

| Item | What it provides | Related row |
| --- | --- | --- |
| [`THIRD_PARTY_COMPLIANCE_DECISION_SHEET.md`](../licensing/THIRD_PARTY_COMPLIANCE_DECISION_SHEET.md) | the recorded D1 outcome and the evidence behind it | Row 4 |
| `THIRD_PARTY_NOTICES.md` appendices F and G | the complete LGPL-2.1 text and the GCC Runtime Library Exception | Row 19 |
| `internal/releasecheck/licensing_documents_test.go` | assertions that the notices carry the licence texts they name, that no corporate publisher is invented, and that the owner record is not pre-populated | Rows 2, 4 and 19 |

**There is exactly one authorisation document, and it is that decision record.** Do not
create a second one: an `RELEASE_AUTHORIZATION.md` that restated the same fields would be
a competing source of truth, and the first person to fill in one of them would leave the
other looking unapproved.

After the merge, rows 3, 4 (structurally) and 19 moved forward. Rows 1, 5, 6, 7, 9, 20, 23,
24 and 25 still require actions by the rights holder, counsel or a repository
administrator — no merge of documentation can satisfy them.

## 3. The exact publication sequence, once every gate passes

Nothing below may be run out of order, and none of it is authorised yet.

```sh
# 1. Every row above must be PASS. The gate is the last machine check, not the first.
go run ./cmd/radanor-release-check                 # must exit 0

# 2. The release commit must be merged to main and tagged, then built from that tag.
git switch main && git pull --ff-only
git tag -s v0.1.0 -m 'Radanor Sparrow Community Edition v0.1.0'   # signed tag
git push origin v0.1.0                              # only after review

# 3. Build from the tag with the release path, which re-runs the gate itself.
bash scripts/build-linux.sh --release
pwsh ./scripts/build-windows.ps1 -Release
```

The Community unsigned path then builds the Windows installer through
`.github/workflows/community-release.yml`, which re-checks the recorded approval, the
exact tag, the tag's place on `main`, and that nothing carries a signature — and still
does not publish anything. **Creating the GitHub Release is a separate reviewed act by a
human**, who has read `UNSIGNED-DISTRIBUTION.md` and the final checksums.

## 4. What "ready for a release candidate" means here

Engineering is complete: the build, the gate, the packaging, the verifiers and the
documentation all exist and fail closed. What remains is not development work. It is one
legal conclusion to be adopted in D1, the recorded decisions D2–D6, one GitHub
administrator session, one human look at two derived icons, a first-distribution date
recorded at publication, and a Windows host for the tagged artifacts. v0.1.0 is a
**release candidate that is correctly blocked**, not a release that is nearly finished.

## 5. Related documents

- [`THIRD_PARTY_COMPLIANCE_DECISION_SHEET.md`](../licensing/THIRD_PARTY_COMPLIANCE_DECISION_SHEET.md)
  — the third-party findings, their evidence and their recorded outcomes.
- [`COMMUNITY_UNSIGNED_DISTRIBUTION_POLICY.md`](../deployment/COMMUNITY_UNSIGNED_DISTRIBUTION_POLICY.md)
  — the unsigned-distribution process this checklist gates.
- [`RELEASE_SIGNING.md`](../deployment/RELEASE_SIGNING.md) — the signing profiles that rows
  14 and 20 depend on.
