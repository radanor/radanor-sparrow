# Radanor Sparrow Community Edition — licence guide

This guide explains, in ordinary language, what the licence on Radanor Sparrow
Community Edition allows. It is written for two readers: an individual installing
Sparrow on their own computer, and an organisation considering it for its own fleet.

**`LICENSE` in the repository — and in every download — is the authoritative text.**
This page is an explanation. Where the two differ, `LICENSE` governs. This page is not
legal advice, and no explanation can create a permission the licence does not grant.

## The short version

| | |
| --- | --- |
| Product | Radanor Sparrow Community Edition |
| Licence | Business Source License 1.1 (`BUSL-1.1`) |
| Licensor and copyright holder | Radheshyam Dangi, © 2026 |
| Status today | **source-available**, not open source |
| Free production use | personal use, and internal use by an organisation, under the Additional Use Grant |
| Becomes open source | on the Change Date, **2030-10-27**, under the GNU General Public License v3.0 |
| Commercial licence | not currently offered |

If you are using Sparrow on your own computer, or inside your own organisation to
administer and monitor computers you own or operate, you do not need to ask anyone
for permission and you do not owe a fee.

## What "source-available" means here

The source code is published. You can read it, build it, audit it and change it. But
until the Change Date it is **not** an open-source licence, and "open source" would be
the wrong description of it today. On the Change Date the same code becomes available
under the GPLv3 — at which point the open-source description becomes correct.

There is no per-seat counting, no activation, no licence key, no phone-home check and
no account. Sparrow does not know or care who installed it; the licence is what
governs your use, not a technical control inside the application.

## What the Business Source License already lets you do

This is the part most often misreported. Business Source License 1.1 is **not** a
licence that forbids copying, modification or redistribution. Its Terms grant you the
right to:

- **copy** the Licensed Work;
- **modify** it and **create derivative works**;
- **redistribute** it; and
- make **non-production use** of it.

Those rights come from the licence text itself, not from the Additional Use Grant. The
Additional Use Grant only **adds** production-use permission; it does not take any of
the above away. See the `LICENSE` parameter text, which says the grant "only adds
permissions" and does not restrict the rights in the Terms.

Two obligations travel with those rights, and they are in `LICENSE` too:

- you must **conspicuously display the licence** on each original or modified copy you
  distribute;
- the licence, and its terms, apply to anyone who receives the work from you.

## What the Additional Use Grant adds

In addition to non-production use, the Licensor grants you the non-exclusive right to
reproduce, install and operate Sparrow **in production**, without a fee:

1. **for personal use**; and
2. **for your internal business or organisational purposes**, including in a
   commercial production environment, while administering, securing and monitoring
   computers that you or your organisation own or operate.

That includes installing it across your organisation's own computers for internal
administration, security monitoring and compliance purposes.

### What the grant does not cover

The grant names three exclusions. None of them is a restriction on the rights in the
Terms; they describe the boundary of the free production-use permission:

- **(a)** offering Sparrow, or a service substantially based on it, to third parties as
  a **hosted or managed security service that competes** with Sparrow;
- **(b)** **commercially licensing, sublicensing, distributing or reselling** Sparrow
  to customers or third parties;
- **(c)** giving third parties **access to a managed security platform based on
  Sparrow that competes with it**.

If your intended use is one of those, you need a separate commercial licence from the
Licensor. **No commercial licence is currently offered**, so the honest answer today
is that such a use is not licensed — not that it is licensed for a fee.

### The cases people actually ask about

These follow the grant's wording. Where the wording genuinely does not settle your
case, ask the Licensor in writing before you rely on it.

| Situation | Reading of the grant |
| --- | --- |
| Sparrow on my own laptop at home | Personal use — permitted. |
| Sparrow on my work laptop, monitored by my employer's IT team | Internal organisational use — permitted. |
| Rolled out to every workstation and server my company owns and operates | Internal organisational use — permitted, at no fee. |
| Running it at a company I am contracted to, on machines they own, as their staff would | Their internal use, not yours — permitted for them. |
| A managed service provider running Sparrow on **its own** machines to monitor **its own** estate | Internal use by that organisation — permitted. |
| A managed service provider running Sparrow to monitor **client** machines, as the service it sells | Not covered by (a)/(c): a competing managed security service. Needs a commercial licence, which is not currently offered. |
| Forking Sparrow and publishing your changes | Permitted by the Terms, with the licence displayed on every copy you distribute. |
| Shipping Sparrow inside a product you sell | Production use for third parties under (b). Needs a commercial licence, which is not currently offered. |
| Bundling Sparrow on an appliance you hand to customers | Same as above: distribution or access for third parties under (b)/(c). |
| Using it to study, test, or evaluate it without production use | Permitted by the Terms. |

## The Change Date and the Change License

Two parameters in `LICENSE` control the changeover:

- **Change Date: 2030-10-27.**
- **Change License: GNU General Public License, version 3.0.**

The Change Date applies **separately to each version** of Sparrow, and it is measured
from that version's **first public distribution**. Business Source License 1.1 itself
also converts a version on the **fourth anniversary of its first public distribution**,
whichever comes first. So no version can stay source-available longer than four years
after it is first published.

On that date the rights granted under the Business Source License terminate and the
same version becomes available to you under the GPLv3. From then on the GPLv3 — not
this page — defines what you may do, including its copyleft obligations for
distribution.

`LICENSE` also records the **planned first public distribution date, 2026-10-27**, and
records the actual first public distribution as `none`: no version of Sparrow has been
published yet, and no actual date is claimed. The actual date is recorded at publication,
and it must not fall after the Change Date.

## Third-party components have their own licences

Sparrow includes and links third-party software, and the licence above covers only the
Licensed Work — Radanor Sparrow Community Edition. Third-party components remain under
their own terms.

- Every dependency's licence and required attribution is in
  [`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md), which ships beside every
  artifact.
- The Linux build uses GTK 3 and WebKitGTK 4.1 from your Ubuntu installation rather than
  bundling them; those are LGPL-2.1-or-later libraries, and `THIRD_PARTY_NOTICES.md`
  reproduces that licence in full.
- The public summary of those obligations is
  [`THIRD_PARTY_LICENSE_COMPLIANCE.md`](THIRD_PARTY_LICENSE_COMPLIANCE.md).

If you redistribute Sparrow, you must carry `LICENSE`, `THIRD_PARTY_NOTICES.md` and the
notices those files contain. They are already inside the AppImage and beside the
Windows artifacts so that this is not something you have to reconstruct.

## What the licence does not give you

- **No warranty.** `LICENSE` provides the work "AS IS", disclaiming warranties of
  merchantability, fitness for a particular purpose, non-infringement and title, to the
  extent the law allows.
- **No trademark right.** The licence grants no right in any trademark or logo. Radanor
  is a product brand; it is not a company, and nothing here grants the right to present
  your build as an official Radanor release.
- **No support obligation.** Support and security-reporting expectations are described
  in [`SUPPORT_AND_SECURITY_REPORTING.md`](SUPPORT_AND_SECURITY_REPORTING.md); they are
  not licence terms.

## Related documents

- [`LICENSE`](../../LICENSE) — the authoritative licence text.
- [`docs/licensing/LICENSING.md`](../licensing/LICENSING.md) — the permitted uses, the
  changeover and the third-party position in more detail.
- [`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md) — dependency licences,
  attributions and the full LGPL-2.1 and GCC Runtime Library Exception texts.
- [`RELEASE_NOTES_V0_1_0.md`](RELEASE_NOTES_V0_1_0.md) — what this version contains.
