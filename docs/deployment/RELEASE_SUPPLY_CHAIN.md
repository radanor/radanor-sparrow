# Release supply chain: actions, build tools and authorization

**Status: pinned and verified locally; no release is authorized.** This document
records *what* the release workflow executes and *how that identity was checked*,
so the pin table can be re-verified rather than trusted. It does not approve a
public release; the licensing gate and the owner authorization in
the private engineering record
still block one.

Evidence below was collected on 27 October 2026, on
`feature/sparrow-p9-release-preparation` based on `develop` at `bb21b61`.

## 1. GitHub Actions pins

Every action is referenced by a full 40-hex commit, never by a tag or a branch.
The two-digit form `@v4`/`@v5`/`@v6` is what this repository used before; a tag is
movable, so the bytes that run can change without any commit here changing.

| Action | Tag | Immutable commit | Commit signature |
| --- | --- | --- | --- |
| `actions/checkout` | `v5.1.0` | `fbc6f3992d24b796d5a048ff273f7fcc4a7b6c09` | present, verified |
| `actions/setup-go` | `v6.5.0` | `924ae3a1cded613372ab5595356fb5720e22ba16` | present, verified |
| `actions/setup-node` | `v5.0.0` | `a0853c24544627f65ddf259abe73b1d18a591444` | present, verified |
| `actions/upload-artifact` | `v4.6.2` | `ea165f8d65b6e75b540449e92b4886f43607fa02` | present, verified |
| `msys2/setup-msys2` | `v2.33.0` | `ec48f7c5447b3140e2b088413ae3a55687bccb6e` | **none — unsigned upstream commit** |

The Community Edition workflow (`.github/workflows/community-release.yml`) reuses
this same reviewed inventory and introduces no new action. That is enforced rather
than intended: `TestWorkflowActionsArePinnedToFullCommitSHAs` walks *every* file in
`.github/workflows/`, so a workflow that referenced an action by tag, or by a commit
nobody reviewed, would fail the test suite. The MSYS2 action's signature question is
separate from the GitHub-owned four and is discussed below.

How each claim was checked:

1. **Identity.** `git ls-remote --tags https://github.com/<owner>/<repo>` was run
   for each release tag, and the commit it names was compared with the pin. The
   same command was run for the movable `refs/tags/vN`; for all five actions the
   pin equals the commit the major tag resolves to today, so pinning changed the
   reference form without changing behaviour.
2. **Signature presence.** Each commit was fetched by SHA in a scratch repository
   and its raw object inspected for a `gpgsig` header. Four commits carry one; the
   `msys2/setup-msys2` v2.33.0 commit carries none.
3. **Signature validity.** The four signed commits were verified with
   `git verify-commit` against GitHub's published web-flow key
   (`968479A1AFF927E37D1A566BB5690EEEBB952194`, key id `B5690EEEBB952194`,
   downloaded from `https://github.com/web-flow.gpg`). Each reported
   `Good signature from "GitHub <noreply@github.com>"`.

What that does and does not mean:

* **Does** — the commit is the one the release tag names, the tag cannot be moved
  under us, and for four of the five the commit was signed by GitHub's
  automation identity.
* **Does not** — a valid signature from GitHub's web-flow key attests that the
  commit was pushed through GitHub, not that anyone in this repository reviewed
  the action's code. The `msys2/setup-msys2` pin is immutable but **unsigned**; its
  integrity rests on the commit identity alone. Treat it as unreviewed upstream
  code that runs on the Windows release runner, and keep it that way until a
  signed replacement (or a vetted alternative) is chosen.
* No tag object is signed: all five release tags are lightweight tags pointing
  directly at commits, so there is no tag-level signature to check.

Re-verifying (no credentials needed):

```bash
# identity: the pin must equal the commit the release tag names
git ls-remote --tags https://github.com/actions/checkout | grep 'refs/tags/v5.1.0'

# signature presence and validity
git init /tmp/probe && git -C /tmp/probe fetch --depth=1 https://github.com/actions/checkout \
  fbc6f3992d24b796d5a048ff273f7fcc4a7b6c09
GNUPGHOME=$(mktemp -d) && curl -sSL https://github.com/web-flow.gpg | GNUPGHOME=$GNUPGHOME gpg --import
GNUPGHOME=$GNUPGHOME git -C /tmp/probe verify-commit FETCH_HEAD
```

`internal/releasecheck/release_process_test.go` keeps this from drifting: every
`uses:` in `.github/workflows` must be a full 40-hex commit, and must appear in the
`reviewedActionPins` inventory. Adding or changing a pin therefore requires editing
the inventory in the same change as this table.

## 2. AppImage build toolchain

The AppImage is produced by `appimagetool`, which is itself an AppImage, plus a
type-2 runtime that the tool would otherwise download for itself. Both are now
selected by immutable version, downloaded over HTTPS and checked against a pinned
SHA-256 **before** the tool is made executable, extracted, or allowed to package
anything.

| Input | Version | SHA-256 | Size |
| --- | --- | --- | --- |
| `appimagetool-x86_64.AppImage` | `1.9.1` | `ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0` | 15092216 |
| `runtime-x86_64` (type2-runtime) | `20251108` | `2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d` | 944632 |

Each pin was independently corroborated on 2026-10-27, rather than trusted because
it was written down once. GitHub's release API publishes a digest for every uploaded
asset, computed by GitHub when the asset was uploaded, and both pins equal it exactly:

```
$ curl -s https://api.github.com/repos/AppImage/appimagetool/releases/tags/1.9.1 \
    | python3 -c 'import json,sys; print([a["digest"] for a in json.load(sys.stdin)["assets"] if a["name"]=="appimagetool-x86_64.AppImage"][0])'
sha256:ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0
$ curl -s https://api.github.com/repos/AppImage/type2-runtime/releases/tags/20251108 \
    | python3 -c 'import json,sys; print([a["digest"] for a in json.load(sys.stdin)["assets"] if a["name"]=="runtime-x86_64"][0])'
sha256:2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d
```

That corroboration covers the middle link of the chain (the tag names those bytes). It
does **not** prove who produced the release — neither upstream project signs its
release assets — so the remaining trust is in the TLS connection to
`github.com`/`release-assets.githubusercontent.com` and in the repositories being the
ones they claim to be. This is recorded because "the digest is pinned" and "the digest
was obtained independently" are different statements, and only the second one is worth
much on its own.

The sizes above were re-measured from the actual assets (`stat -c %s`), not copied
from the API listing:

Source URLs, pinned by version rather than by the mutable `continuous` alias:

* `https://github.com/AppImage/appimagetool/releases/download/1.9.1/appimagetool-x86_64.AppImage`
* `https://github.com/AppImage/type2-runtime/releases/download/20251108/runtime-x86_64`

`validation/linux/Initialize-SparrowAppImageTool.sh` performs the download, prints
both digests, refuses a mismatch (`checksum mismatch; refusing to execute or
package unverified bytes`), and exports `APPIMAGETOOL` and `APPIMAGE_RUNTIME` for
the build. A cached file with the wrong digest is refused exactly like a fresh
download, so a poisoned cache cannot become a build input.

`scripts/build-appimage.sh` now **fails** a release build that has no
`APPIMAGE_RUNTIME`: without one, `appimagetool` fetches the `continuous` runtime
itself, which would make two builds of one commit differ. A development build keeps
the convenience fallback and prints what it did.

Two consequences worth recording:

* **A pinned tool is not automatically compatible.** appimagetool 1.9.1 refuses a
  *relative* `SOURCE` directory (`Error: no such file or directory` for a directory
  that exists), which the previous `continuous` tool accepted. The packaging script
  therefore resolves the AppDir and output to absolute paths before invoking it.
  This was found by running the pinned toolchain, not by reading the YAML.
* **Reproducibility holds from clean clones, not just from one working tree.**
  `validation/linux/appimage-repro.sh --ref 81745ba` clones the commit twice into
  fresh trees, builds the whole path in each (frontend, supervisor, window
  component, AppImage) and requires the two artifacts to be byte-identical. Both
  builds produced
  `0d8cb9cb5719e68e21ed130a4ad3faa87d562f4a1811a0d058de45f11dc67e86`
  (`RadanorSparrow-0.1.0-dev.0+g81745baed8c9-x86_64.AppImage`, 15223288 bytes), and
  the same digest was produced by a third build in the working tree. Two clean
  clones agreeing removes the working tree from the input set, which the earlier
  same-tree check could not do. An earlier assessment recorded the same property
  from a dirty tree (`c79c44a9…`, 15112696 bytes); that datum is superseded by this
  one, which needs no caveat.

The Windows side already pinned its NSIS compiler (`validation/windows/Initialize-SparrowNsis.ps1`,
`mingw-w64-ucrt-x86_64-nsis` `3.13-2`, SHA-256
`262c2453ee69924e4995a6f3c685edb96e57e57e749d38f8f89732be6ffccff7`, plus a required
MSYS2 package signature). The release workflow now calls it instead of accepting
whatever the runner's package cache contains.

## 3. What the workflow may execute

`.github/workflows/release.yml` builds every artifact on a hosted runner from a
clean checkout of an exact tag. It does not run any script from the network except
the two verified tool downloads above. Specifically:

* no `curl … | bash`, no `releases/download/continuous`, and no unpinned installer
  script;
* the Linux job installs distribution packages with `apt-get` and nothing else;
* the Windows job provisions `7zip` through `msys2/setup-msys2` and the NSIS
  compiler through the checksum-and-signature-verified initializer;
* `internal/releasecheck` asserts that neither workflow contains
  `releases/download/continuous`.

## 4. Release authorization: what is enforced where

| Property | Enforced by | Kind of evidence |
| --- | --- | --- |
| Strict BSL gate runs before artifacts | job dependency `needs: licensing-gate` | machine-checked in the workflow and executed by the gate itself |
| Artifacts wait for authorization | `needs: [licensing-gate, release-authorization]` | machine-checked |
| Only an exact `vX.Y.Z` tag releases | `GITHUB_REF_TYPE`/`GITHUB_REF_NAME` test in the gate | executed by a test against a throwaway repository |
| The tag names a commit on `main` | `git merge-base --is-ancestor HEAD origin/main` | executed by the same test |
| Tag version and build version agree | `radanor-version -release` compared with the tag | executed by the same test |
| Signing cannot run without key material | mandatory `RADANOR_RELEASE_SIGNING` check before upload | machine-checked; the signing itself is exercised with throwaway keys |
| An unsigned Windows artifact is never uploaded | portable, embedded uninstaller and setup signing/verification gates | P10.2 adds actual Windows disposable-signature and NSIS reconstruction evidence; approved production trust/timestamps remain NOT RUN |
| A skipped artifact test cannot pass silently | `-json` log + required `pass` events in the release job | the assertion logic is exercised with fixture events |
| No GitHub Release is published automatically | absence of `gh release` / `action-gh-release`, asserted by a test | machine-checked |
| An unsigned distribution needs its own recorded approval | `RADANOR_COMMUNITY_UNSIGNED_RELEASE` must read `approved` in the `community-release` environment before anything is built | machine-checked against the workflow text; the variable itself is administrator-set |
| Unsigned **Windows** distribution needs its own separate approval | `if: vars.RADANOR_UNSIGNED_WINDOWS_DISTRIBUTION == 'approved'` on the Windows job | machine-checked; without it the job is **skipped**, which the acceptance record reports as skipped rather than as a pass |
| The unsigned process produces no signature and claims none | no `*.asc` may exist in `dist/linux`, and every Windows artifact must report `Get-AuthenticodeSignature … NotSigned` | machine-checked as a positive requirement, not as an unchecked default |
| The unsigned process cannot drift into the signed one | it runs in `community-release` with no signing variables, and refuses outright when either signing switch reads `enabled` | machine-checked |
| Environment reviewers, tag and branch protection | **GitHub settings only** | **administrator verification required** |

The last row is the important one. `vars.RADANOR_PRODUCTION_RELEASE_PROTECTED=verified`
is a statement an operator makes in repository settings; it is not proof. Workflow
YAML cannot read an environment's protection rules, reviewer list or tag rulesets,
so no amount of testing here can show they are configured. The machine-verified part
is the ancestry and version check, which limits what a compromised or mistaken
operator can release from: an unreviewed commit cannot be tagged and shipped without
it appearing on `main`.

## 5. Administrator-only checks (GitHub settings)

None of these can be performed or verified from this checkout. `radanor-admin` must
record the result for each before any release run is authorized.

- [ ] `production-release` environment created; deployment branches restricted to
      protected `v*` tags only.
- [ ] Required reviewers configured, and self-review disabled if the repository plan
      supports it (a private repository on a plan without that option cannot enforce
      it — record which applies).
- [ ] `main` protected: required pull request, required current CI checks, no force
      push, no deletion, administrator enforcement.
- [ ] `v*` tag ruleset: only the approved release operator may create, move or delete
      release tags.
- [ ] `RADANOR_SIGNING_KEY_ARMORED` stored as an **environment** secret on
      `production-release`, not as a repository secret.
- [ ] `RADANOR_PRODUCTION_RELEASE_PROTECTED=verified` set only after the four items
      above are confirmed; unset it immediately if any rule is removed.
- [ ] `RADANOR_RELEASE_SIGNING` and `RADANOR_WINDOWS_SIGNING` remain unset until real
      key material and an approved Authenticode identity exist.
- [ ] Actions policy: allowed actions restricted, `GITHUB_TOKEN` read-only by default,
      and Actions not permitted to approve pull requests.

For the Community Edition unsigned process, only after the policy's approval record
in [`COMMUNITY_UNSIGNED_DISTRIBUTION_POLICY.md`](COMMUNITY_UNSIGNED_DISTRIBUTION_POLICY.md)
is signed:

- [ ] `community-release` environment created; restricted to protected `v*` tags,
      approval required, and no signing secret placed in it.
- [ ] `RADANOR_COMMUNITY_UNSIGNED_RELEASE=approved` set only after that signature
      exists; unset it immediately if the policy is withdrawn.
- [ ] `RADANOR_UNSIGNED_WINDOWS_DISTRIBUTION=approved` set only after the rights
      holder has separately accepted the Windows consequences in the policy's §7.
- [ ] `RADANOR_WINDOWS_SIGNING` and `RADANOR_RELEASE_SIGNING` confirmed **unset or
      not `enabled`**, so an unsigned run cannot be started while signed artifacts are
      possible — the workflow refuses in that case, which is the intended deadlock.

## 6. Residual risks

* **`msys2/setup-msys2` is unsigned.** The pin is immutable but the commit carries no
  signature. It runs on the Windows release runner with access to that job's
  environment secrets.
* **Pinning does not audit.** A pinned commit is the code that runs; nobody in this
  repository has reviewed those action implementations. Reviewing the four signed
  GitHub-owned actions and the MSYS2 action remains a separate task.
* **Third-party download verification is digest-only.** The appimagetool and runtime
  digests were obtained by downloading from GitHub over TLS, not from a signed
  upstream provenance record. §2 now corroborates each pin against the digest
  GitHub's own release API publishes for the asset — a second, independent
  observation of the tag→bytes link — but that API value is computed by GitHub at
  upload time and is not a signature either. Neither upstream project signs its
  release assets. A compromise of the asset *before* it was uploaded would not be
  detectable from either value; the pin only prevents *later* silent replacement.
* **The attestation is a human statement.** See §4.
* **No release has run.** This whole document describes a workflow that has never
  completed, because the licensing gate and the signing prerequisites are unmet. It is
  verified by unit tests, by local packaging, and by `actionlint`, not by a
  green release run.
