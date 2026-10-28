# Public release approval checklist

**Status: items 1–5 are complete — item 5 by an owner risk decision rather than a legal
conclusion — and item 6 is OPEN. No public release may be published until every item is
completed and signed off here.** This is the human record that the machine gate
cannot replace; the gate enforces the parts of it that can be written down and checked
mechanically.

Do not fill any placeholder from assumption. A name, a date or an approval that has not
been verified in writing is worse than the placeholder it replaced, because it looks
settled. The current state of the parameters is in
the private release decision log; the permitted-use explanation is in
[LICENSING.md](LICENSING.md); the artifact-side work is in
[../deployment/RELEASE_SIGNING.md](../deployment/RELEASE_SIGNING.md).

## 1. The six requirements

| # | Requirement | What "complete" means (evidence) | Enforced by | Status |
| --- | --- | --- | --- | --- |
| 1 | **Verified copyright ownership** | Written evidence that the named party owns the copyright in the Licensed Work, or holds rights broad enough to grant the BSL: an assignment, an employment/contractor IP clause covering this work, or a signed transfer. Shareholdings, incorporation and a product name are **not** ownership | Not machine-checkable | **CLOSED BY DECLARATION 2026-10-27** — the rights holder declares the written record exists and is retained privately; the record is not in this repository (the private release decision log §1) |
| 2 | **Authorized Licensor identity** | The exact legal name that may appear as `Licensor:` and in the © line, matching the evidence in item 1, with both occurrences naming the same party | `releasecheck` rejects the placeholder and an empty value (`bsl.licensor.verified`, `bsl.parameters.missing`), and requires the © line to be present and equal to the Licensor (`bsl.copyright-holder.verified`) | **DONE 2026-10-27** — `LICENSE` names Radheshyam Dangi as Licensor and in the © line |
| 3 | **Version-specific Change Date** | A concrete `YYYY-MM-DD` that is after the actual first public distribution of *this version* and no later than four years after it, recorded together with that first-distribution date | `releasecheck` (`bsl.change-date.concrete`, `bsl.change-date.valid`, `bsl.change-date.order`, `bsl.change-date.maximum`) | **DONE 2026-10-27** — `Change Date: 2030-10-27` with `First public distribution of this version: 2026-10-27` (the intended release date; no version has been published). The actual date is confirmed or corrected at publication, and the Change Date must stay no earlier than it and no later than four years after it — see the private release decision log §2 |
| 4 | **Additional Use Grant** | The grant wording that states what is free (personal use and internal organizational use) is present and unaltered; the Terms, Covenants and Notice match the official BSL 1.1 text | `releasecheck` (`bsl.additional-use-grant.present`, `bsl.text.intact`, `bsl.structure.invalid`) | Present, wording to be **confirmed** with 1–3 |
| 5 | **Third-party licensing obligations** | Every distributed dependency's licence and attribution is reproduced: Go modules and npm packages in [THIRD_PARTY_NOTICES.md](../../THIRD_PARTY_NOTICES.md); GTK 3 and WebKitGTK 4.1 are used as **system libraries**, dynamically linked and not redistributed, which is why they are not bundled; no copyleft library is shipped inside the artifact; the full LGPL-2.1 text (appendix F) and the GCC Runtime Library Exception (appendix G) now accompany the artifact, so the LGPL notice element is discharged whether or not LGPL-2.1 §6 is held to be engaged | `releasecheck` (`package.notices.present`, `package.license.present`, `package.license.matches`) plus `internal/appimage` (no bundled GTK/WebKit) and `internal/releasecheck` (`TestThirdPartyNoticesCarryTheLicenceTextsTheyName`) | Notices and licence texts present; the obligations are discharged as shipped. **D1 was closed on 2026-10-28 by an owner risk decision, not by a legal conclusion** — see [THIRD_PARTY_COMPLIANCE_DECISION_SHEET.md](THIRD_PARTY_COMPLIANCE_DECISION_SHEET.md) §6 and the rights holder's decision record (held privately) D1 |
| 6 | **Authority to publish under the Radanor brand** | A decision, recorded here, that the release may be published under the Radanor product name and that the release naming is approved. **Radanor is a brand, not an incorporated entity**: there is no company to sign and no corporate body to accept the licence terms. The party granting the licence and publishing the work is the individual named as Licensor in `LICENSE`, so the authority this item asks for is his, recorded by name and date in the rights holder's decision record (held privately) | Not machine-checkable | **OPEN** |

## 2. What the gate checks, and what it cannot

Two gates already exist and must both pass:

1. `go run ./cmd/radanor-release-check` — strict licensing validation. It rejects the
   unresolved placeholders, an invalid Change Date, modified BSL text, a missing
   Additional Use Grant, a missing GPLv3 Change License, release-facing metadata that
   still contains a placeholder marker, and a package that does not carry `LICENSE` and
   `THIRD_PARTY_NOTICES.md`.
2. `go run ./cmd/radanor-version -release` — requires a clean checkout of an exact
   `vX.Y.Z` tag. It does not replace the licensing gate and says so.

Neither can check items 1 and 6, and neither can judge whether the LGPL posture in item
5 is acceptable. Those are decisions, recorded here, not build outputs — which is why item
5 is closed by a named decision of the rights holder and not by a green gate.

`go run ./cmd/radanor-release-check` now **exits 0**. It reported six findings until
2026-10-27, when the Licensor, © and Change Date findings were cleared by the decisions in
the private release decision log §1 and §2; the seventh, a lexical hit on a
`README.md` sentence that described the gate using the scanner's own word, was removed by
rewording the sentence and is pinned by `TestReadmeReleaseSectionAvoidsScannerVocabulary`.
The last two were the LGPL and `modernc.org/libc` markers in `THIRD_PARTY_NOTICES.md` §6,
which is item 5 above; they came out on **2026-10-28** as the mechanical consequence of the
rights holder's D1 **risk decision**, in the same change that recorded it.
`TestOwnerRiskDecisionClosesTheGateWithoutClaimingClearance` pins the state that remains
true afterwards: the gate passes **and** the notices still say the legal interpretation is
open. `go run ./cmd/radanor-version -release` still exits 1 because no tag exists, and it
must: item 6 is open and no release has been cut.

## 3. Publication prerequisites, in order

1. Items 1–6 completed with evidence attached to this document (or to a referenced
   private record — do not paste contracts with personal data into a public repository).
   Items 5 and 6 are the two still open, and both are signed on
   the rights holder's decision record (held privately) rather than by
   editing this table, so the decision has a name and a date attached to it.
2. `LICENSE` updated with the verified Licensor and the concrete Change Date (**both
   done 2026-10-27**); the release-facing wording in `README.md`,
   `THIRD_PARTY_NOTICES.md` and `internal/branding` kept consistent with it, and the
   recorded first-distribution date re-verified against the actual release date.
3. `go run ./cmd/radanor-release-check` exits 0 on the tree that will be tagged.
4. `go run ./cmd/radanor-release-check --release-dir dist/linux` exits 0 on the built
   artifact directory.
5. A clean checkout of an exact `vX.Y.Z` tag: `go run ./cmd/radanor-version -release`
   exits 0.
6. Signing enabled and proven once, or explicitly deferred with the release notes stating
   the artifacts are unsigned ([RELEASE_SIGNING.md](../deployment/RELEASE_SIGNING.md)).
7. This document updated to `RESOLVED` with the date and the person who approved, and
   any residual limitation stated in the release notes.

## 4. What must not happen to get here

* No placeholder replaced by an assumption, a plausible company name or a guessed date.
* No weakening, skipping or removing of the release gate, the version rule or the
  artifact checks; no `--mode development` in a release path.
* No release build, tag, upload or publication before every item above is complete.
* No change to `LICENSE` terms or to the Additional Use Grant without the same evidence
  as items 1–4.
