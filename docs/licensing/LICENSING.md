# Licensing — Radanor Sparrow Community Edition

> **Status: proposed, not legally approved.** This document and the accompanying
> [`LICENSE`](../../LICENSE) are a proposal for legal review. Nothing here states
> that Radanor Sparrow is licensed, cleared, or approved for publication. Two
> values in the `LICENSE` parameter block are deliberately left as placeholders
> because no verified fact supports them yet (see
> [Open items](#open-items-for-legal-review)).

## What this project is — and is not

Radanor Sparrow Community Edition is **source-available** software. Its source is
public and may be read, and the license grants real freedoms, but it is **not**
open-source software as the Open Source Initiative defines the term, and it is
**not** released under an OSI-approved license while the Business Source License
1.1 applies to it.

The Business Source License 1.1 (`BUSL-1.1`, sometimes written `BSL 1.1`) says
this about itself, and the project repeats it rather than softens it:

> The Business Source License (this document, or the "License") is not an Open
> Source license. However, the Licensed Work will eventually be made available
> under an Open Source License, as stated in this License.

This is a deliberate, time-limited arrangement: every version released under the
license becomes open-source software on a fixed schedule (the [Change
Date](#the-four-year-changeover)). Until then, the description to use in
documentation, the dashboard, releases and conversation is **source-available**,
never "open source".

## What the Business Source License already permits

It is easy to overstate what a source-available license forbids. The BSL 1.1
grant is broad, and choosing it does **not** prohibit modification, redistribution
or resale. From the "Terms" of the license, the Licensor grants you the right to:

- **copy** the Licensed Work,
- **modify** it and **create derivative works**,
- **redistribute** the original or a modified copy, and
- **make non-production use** of it,

free of charge, with no requirement to pay the Licensor. A recipient who does any
of these must carry the license and its notices with the work, and the license
terminates if its terms are violated — but modification and redistribution are
permitted acts, not prohibited ones. What the BSL limits by default is
**production** use; that limit is relaxed for you by the Additional Use Grant
below.

Do not describe this license as forbidding modification, redistribution or
resale. The accurate statements are: source is available; non-production use is
free; production use is permitted only within the Additional Use Grant or under a
commercial license; and the work becomes open-source software on the Change Date.

## The Additional Use Grant: free personal and internal use

The Additional Use Grant in [`LICENSE`](../../LICENSE) **adds** permissions on top
of the BSL grant. It does not take any right away, and it does not narrow the BSL
grant — the BSL's own covenant requires that an Additional Use Grant impose no
additional restriction, and the grant is written to respect that.

Within the Additional Use Grant, and at no cost, you may reproduce, install and
operate Radanor Sparrow Community Edition in production and non-production
environments:

| Use | Permitted by the Additional Use Grant |
| --- | --- |
| Personal use on your own computers | Yes |
| Internal business or organizational use | Yes |
| Internal use in a commercial production environment | Yes |
| Installing and running it across your organization's own computers | Yes |
| Internal administration, security monitoring and compliance on those computers | Yes |
| Offering Sparrow, or a substantially Sparrow-based service, to third parties as a competing hosted/managed security service | No — outside the grant |
| Commercially licensing, sublicensing, distributing or reselling Sparrow to customers | No — outside the grant |
| Giving third parties access to a competing Sparrow-based managed security platform | No — outside the grant |

So, in plain terms: **a person may run Sparrow on their own machines for free; a
company may run it on its own machines for free, including in production, to
administer and monitor those machines.** Turning Sparrow into a business that
resells it, or into a competing hosted/managed security platform for third
parties, is not covered by the free grant and needs separate permission.

### Ambiguities flagged for legal review

The following wordings in the Additional Use Grant are drafted but not yet
settled. Each needs a lawyer's eye, and none should be represented as final:

- **"competes with" / "competing"** is the operative test for the excluded
  hosted/managed service, but it is not defined. Whether an organization offering a
  *non-security* platform that incidentally uses Sparrow internally "competes" is
  not obvious from the text.
- **"substantially based on"** bounds what counts as a Sparrow-derived competing
  service; the threshold is a judgment call.
- **"internal"** describes the permitted organizational use but does not say
  whether an organization may run Sparrow for its own staff who are remote, or for
  contractors, or across corporate affiliates.
- **"third parties"** is undefined; whether it includes an organization's own
  customers in an incidental way needs review.
- Whether the exclusions should be phrased as scope limits (as here) or as
  conditions, and whether any jurisdiction would read them as adding a restriction
  contrary to the BSL covenant, is a question for counsel.

## The four-year changeover

The Business Source License is designed to end. Two independent triggers convert a
version to the Change License, **whichever comes first**:

1. the **Change Date** specified for that version, or
2. the **fourth anniversary of the first publicly available distribution** of that
   version under this License.

The `Change Date` parameter in this project's `LICENSE` records a concrete date for
the first version: **2030-10-27**, which is exactly the fourth anniversary of the
recorded first public distribution date, **2026-10-27**. The two triggers therefore
coincide: **each released version becomes open-source software four years after it is
first publicly distributed.** The license applies separately to each version, so
later versions are on their own timetables, and the BSL text's own "whichever comes
first" clause guarantees that a version cannot stay source-available longer than
four years even if a Change Date were set later.

The recorded first public distribution date is the **intended** release date: no
version has been publicly distributed yet, and the parameter states that rather than
asserting a publication that has not happened. It is recorded because the gate
cannot verify the four-year maximum without it. At publication the actual date
confirms or replaces it, and the Change Date must remain no earlier than that date
and no later than four years after it.

**Four years is the standard maximum**, and this License's own "whichever comes
first" wording means a version may not remain source-available for longer than four
years even if a later Change Date were written into the parameter block. The
publication-time check is tracked as an open item below.

**Change License: GNU General Public License version 3.0.**

When the changeover takes effect for a version, the rights you receive are the
rights of that Change License, and the BSL grants for that version terminate. In
practical terms:

- from the Change Date (or fourth anniversary) onward, that version is governed by
  **GPLv3** and is genuinely open-source software;
- GPLv3 is a **strong copyleft** license: distributing the work or a derivative
  obliges you to provide complete corresponding source under GPLv3 and to preserve
  the same freedoms for downstream recipients;
- the BSL covenant permits the Change License to be "the GPL Version 2.0 or any
  later version"; **GPLv3 is such a later version**, so it satisfies the covenant.

The project's own dependency set has been checked against this choice: the
dependencies that ship in the binary and dashboard are permissively licensed
(MIT, BSD-3-Clause, Apache-2.0 and public domain), and no copyleft dependency is
linked. See [`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md). Permissive
dependencies can be conveyed under GPLv3 without conflict; this is stated here so
that the compatibility question is answered with evidence rather than assumed.

### What the changeover does and does not do

- It does **not** retroactively change the license of older copies you already
  received; it grants a forward-looking Change License for the applicable version.
- It does **not** apply to third-party components, which keep their own licenses.
- It does **not** oblige anyone to relicense their own independent work.
- It means the "source-available, not open source" framing in this document is
  **temporary** and should be updated per version once each Change Date passes.

## Commercial licensing

The Additional Use Grant covers free personal and internal organizational use. Uses
outside it — most importantly reselling Sparrow, or operating it as a competing
hosted/managed security service for third parties — are not permitted by the free
grant and require a separate commercial license from the Licensor.

**No commercial license is available for purchase today.** No price, terms or
sales channel exists, and none should be advertised, linked, or promised until the
Licensor has actually established one. Where the product or documentation needs to
mention it, the correct phrasing is that such uses "may require commercial
permission" and that a commercial license "is not currently offered".

## Release gate

Because the placeholders above are intentional during development, the project
has an explicit **licensing release gate** that must pass before a public release.
It fails closed:

```sh
bash scripts/validate-release-licensing.sh                 # run the gate alone
bash scripts/build-linux.sh --release                      # release build (Linux)
pwsh ./scripts/build-windows.ps1 -Release                  # release build (Windows)
```

The gate checks, from the files themselves:

1. the standardized **Business Source License 1.1 text is intact** — the Terms,
   Covenants and Notice must match the official text verbatim;
2. the **Licensor is a real, non-placeholder identity**, and the **copyright line in
the Licensed Work names the same party**, so the two places the BSL names the
holder cannot disagree;
3. a **concrete calendar Change Date** exists for the released version;
4. the **Change Date satisfies BSL 1.1** — it is a valid date, not before first
   distribution, and never later than the four-year maximum;
5. the **Additional Use Grant is present** (not empty and not "None");
6. the configured **GPLv3 Change License is present**;
7. the release package **carries `LICENSE` and `THIRD_PARTY_NOTICES.md`**, and the
   packaged `LICENSE` matches the repository copy;
8. **no unresolved licensing placeholders remain** in release-facing metadata
   (`LICENSE`, `README.md`, `THIRD_PARTY_NOTICES.md`, the application's licensing
   strings).

A blocked gate exits non-zero and the release build stops before producing a
public artifact. Development validation (`--mode development`) allows the
documented placeholders below while still rejecting structural problems, and is
used by CI so real defects stay visible without gating ordinary work. **A
green development pipeline is not a release approval**; only the release workflow
(`.github/workflows/release.yml`) runs the gate as a pass/fail check.

The gate enforces what can be read from files. The decisions a file check cannot
verify — verified copyright ownership, the authorised Licensor identity, this
version's Change Date, the Additional Use Grant wording, third-party obligations and
authority to publish under the Radanor brand (Radanor is a brand, not an incorporated
entity, so there is no corporate body to approve anything) — are tracked item by item in
[`RELEASE_APPROVAL_CHECKLIST.md`](RELEASE_APPROVAL_CHECKLIST.md). That checklist is
open in full; a passing gate is necessary but not sufficient for a public release.

### Required Change Date format at release

The gate reads the Change Date parameter for a concrete date **and** the first public
distribution date — without the second line it reports `bsl.change-date.maximum`,
because the four-year rule cannot otherwise be checked. This project's parameter
reads:

```
Change Date:          2030-10-27
                      First public distribution of this version: 2026-10-27
```

The Change Date must be on the same line as the `Change Date:` label (it is the
first `YYYY-MM-DD` date in the parameter), must not precede the first public
distribution, and must not be later than four years after it.

## Files that carry this license

| File | Purpose |
| --- | --- |
| [`LICENSE`](../../LICENSE) | The Business Source License 1.1 with this project's parameter block. The standardized license terms are reproduced unmodified. |
| `docs/licensing/LICENSING.md` | This document: permitted uses, the changeover, and open questions. |
| [`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md) | Dependency licenses and the attribution obligations they impose. |
| [`CONTRIBUTING.md`](../../CONTRIBUTING.md) | What a contribution must carry, and the licensing authority a contributor must have. |

## Open items for legal review

These must be resolved by the copyright holder with counsel **before any public
release or publication**, and are the reason this license is described as
proposed rather than approved. The licensor and Change Date decisions already taken
are recorded, with the items still open, in the rights holder's private decision
record, which is not published with the source:

1. **Licensor identity (settled 2026-10-27).** `LICENSE` names **Radheshyam
   Dangi**, an individual, in both places the BSL requires: the `Licensor:`
   parameter and the © line of the Licensed Work. The placeholder is gone, and no
   entity, jurisdiction or signatory is asserted that the rights holder has not
   declared. The declaration states that the written ownership record — covering
   the contribution history, the shared engineering identities and the supplied
   artwork — exists and is retained privately; it is deliberately not in this
   repository, because contracts carrying personal data are not published with the
   source. That private record carries the basis of the declaration and the condition
   under which the placeholder must be restored.

   Hosting the repository in the GitHub organization `radanor`, administered by
   `radanor-admin`, does not change that conclusion. GitHub repository control is
   not evidence that the organization account or its administrator owns the
   Radanor brand, the software copyright, or the legal right to publish it.

   This settles the identity and ownership question only. The separate provenance
   review in item 4 and the contribution-licensing question in item 5 remain open. The
   third-party questions recorded in
   [`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md) were closed on 2026-10-28 by the
   rights holder as a recorded **risk acceptance**: the interpretation is still open and no
   legal certification is claimed. Publication is blocked by the release controls and the
   untested release artifacts, not by licensing.
2. **Change Date (set to 2030-10-27).** The parameter records the concrete date
   2030-10-27 together with `First public distribution of this version: 2026-10-27`.
   The 2026 date is the **intended** release date: no version has been published, and
   the parameter says so rather than asserting that one has. 2030-10-27 is exactly
   four years after it — the latest date the licence allows for that
   first-distribution date.

   At publication the actual first public distribution date must be recorded, and the
   Change Date must be no earlier than it and no later than four years after it; if
   publication slips past 2030-10-27 the Change Date must move with it. The Change
   Date applies separately to each version. Counsel should confirm that a Change Date
   sitting on the four-year boundary is the intended reading, since it leaves no
   margin. The basis for that reading is in the same private record.
3. **Copyright headers.** No source file carries a copyright header, and no
   `NOTICE`/copyright file exists. Whether to add headers, and in what form, is
   undecided and is not done automatically here.
4. **Ownership and provenance.** The architecture records state that final
   ownership and provenance review (including any relationship to material from an
   earlier, separate project) is outstanding. The project must be able to warrant that
   it has the authority to license every file it offers under the BSL. This is the
   single largest unresolved question.
5. **Contribution licensing.** No contributor license agreement and no recorded
   contribution history exist; see [`CONTRIBUTING.md`](../../CONTRIBUTING.md).
6. **Trademark.** The BSL grants no trademark rights. The status of the "Radanor"
   and "Sparrow" names and marks is separate from copyright and is not addressed
   by this license.
7. **Ambiguous Additional Use Grant terms** listed above.

**Nothing in this document may be cited as legal advice or as approval to publish.**
