# Radanor Sparrow Community Edition — release documentation

This directory holds the documentation written for **people who download and run
Radanor Sparrow Community Edition v0.1.1**: installation, verification, licensing in plain
language, privacy, support, and the release notes.

**v0.1.1 has not been published.** Nothing here announces a release. The authoritative
gate is [`FINAL_RELEASE_CHECKLIST.md`](FINAL_RELEASE_CHECKLIST.md), and it currently
reports publication as not authorized.

## The documents

| Document | Read it when you want to… | Audience |
| --- | --- | --- |
| [`VERIFY_DOWNLOADS.md`](VERIFY_DOWNLOADS.md) | check that a download arrived intact, and understand what a checksum does not prove | everyone, first |
| [`WINDOWS_INSTALLATION_GUIDE.md`](WINDOWS_INSTALLATION_GUIDE.md) | install, upgrade, or uninstall on Windows 11; understand the Unknown Publisher prompt | Windows users |
| [`LINUX_INSTALL_GUIDE.md`](../deployment/LINUX_INSTALL_GUIDE.md) | install, upgrade, or uninstall on Ubuntu; understand the AppArmor window behaviour | Ubuntu users |
| [`SECURITY_AND_PRIVACY.md`](SECURITY_AND_PRIVACY.md) | know what is stored, where, what leaves the machine, and what the tool cannot do | everyone |
| [`COMMUNITY_LICENSE_GUIDE.md`](COMMUNITY_LICENSE_GUIDE.md) | know what you may do with Sparrow, in plain language | users and organisations |
| [`THIRD_PARTY_LICENSE_COMPLIANCE.md`](THIRD_PARTY_LICENSE_COMPLIANCE.md) | see which third-party components ship, under which licences, with what obligations | redistributors, auditors |
| [`SUPPORT_AND_SECURITY_REPORTING.md`](SUPPORT_AND_SECURITY_REPORTING.md) | report a bug or a vulnerability, and know which channels actually exist | everyone |
| [`RELEASE_NOTES_V0_1_1.md`](RELEASE_NOTES_V0_1_1.md) | see what this version contains and what it deliberately does not | everyone |
| [`GITHUB_RELEASE_V0_1_1.md`](GITHUB_RELEASE_V0_1_1.md) | publish the release, with every value that must be filled at that moment marked | release manager |
| [`FINAL_RELEASE_CHECKLIST.md`](FINAL_RELEASE_CHECKLIST.md) | see exactly what still blocks publication, and who owns each item | release manager, rights holder |

The Linux installation guide lives at
[`docs/deployment/LINUX_INSTALL_GUIDE.md`](../deployment/LINUX_INSTALL_GUIDE.md) rather
than here: it is the long-standing, maintained guide, and a second copy in this directory
would be a competing source of truth that could drift from it.

## Public and internal documents

The project keeps two kinds of record, and they are deliberately separate.

**Public — safe to publish, written for a reader outside the project.** Everything in this
directory, plus `THIRD_PARTY_NOTICES.md` and `LICENSE` at the repository root, and
[`UNSIGNED_DISTRIBUTION_NOTICE.md`](../deployment/UNSIGNED_DISTRIBUTION_NOTICE.md), which
ships beside every artifact.

**Internal — written for the rights holder, counsel or maintainers.** These are working
records, not publication material:

| Document | Why it is internal |
| --- | --- |
| [`THIRD_PARTY_COMPLIANCE_DECISION_SHEET.md`](../licensing/THIRD_PARTY_COMPLIANCE_DECISION_SHEET.md) | open legal questions and the evidence supporting each |
| [`RELEASE_APPROVAL_CHECKLIST.md`](../licensing/RELEASE_APPROVAL_CHECKLIST.md) | internal approval record |
| the rights holder's owner decision record, the release decision log and the per-milestone acceptance records | personal decisions, signatures, and engineering evidence with host names and build paths; all held privately |
| [`RELEASE_SIGNING.md`](../deployment/RELEASE_SIGNING.md), [`RELEASE_SUPPLY_CHAIN.md`](../deployment/RELEASE_SUPPLY_CHAIN.md) | implementer-level security detail |

The public compliance summary
([`THIRD_PARTY_LICENSE_COMPLIANCE.md`](THIRD_PARTY_LICENSE_COMPLIANCE.md)) states the
measured facts and what they oblige; it does not reproduce the internal legal analysis,
and it does not answer the legal questions. That is on purpose: a public document must not
present an unresolved conclusion as settled.

## What is not published, and why

- **No invented values.** No download URL, no asset hash and no release date appears in
  these documents as if it were known. The GitHub release text uses marked fields that must
  be filled from the final artifacts.
- **No invented approvals.** No document here records that the rights holder, counsel or a
  repository administrator approved anything.
- **No invented identity.** Radheshyam Dangi is the individual copyright holder and
  Licensor. **Radanor is a product brand, not a company** — nothing here presents it as an
  incorporated entity, and there is no invented publisher address or registration number.

## Related records outside this directory

- [`LICENSE`](../../LICENSE) — the authoritative licence.
- [`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md) — authoritative third-party
  notices.
- [`SECURITY.md`](../../SECURITY.md) — the repository security policy.
- [`README.md`](../../README.md) — the project README.
