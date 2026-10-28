# Contributing to Radanor Sparrow Community Edition

Thanks for your interest. Radanor Sparrow is source-available software (see
[`LICENSE`](LICENSE) and [`docs/licensing/LICENSING.md`](docs/licensing/LICENSING.md)).
This file explains what a contribution must carry and what you must have the
authority to do before it can be accepted.

## Before you start

- **Open an issue or discussion first** for anything larger than a documentation
  fix or an obvious bug fix. The project has a documented architecture and
  milestone process (`docs/architecture/`) and prefers a design conversation before
  a large change.
- **Match the existing conventions.** Go 1.27, Node.js 24 LTS, the existing package
  layout, and the project's testing style. Run the checks in [Verifying your
  change](#verifying-your-change) before opening a pull request.
- **Do not import proprietary material.** Do not copy source, fixtures, artwork,
  Git history or prose from another project into this tree. Such material is design
  evidence only and must never be committed here.
- **Do not "resolve" a licensing placeholder yourself.** The Licensor legal
  identity and the per-version Change Date are placeholders on purpose; naming a
  legal entity or inventing a release date is a legal decision, not a code change.
  The licensing release gate (`scripts/validate-release-licensing.sh`, run by the
  release build path and the release workflow) keeps those placeholders from
  reaching a public release. See
  [`docs/licensing/LICENSING.md`](docs/licensing/LICENSING.md).

## Licensing of contributions

The project is **inbound = outbound**. By submitting a contribution (a pull
request, patch, or any other form), you agree that:

1. **Your contribution is licensed under the project's license** — the Business
   Source License 1.1 with the Additional Use Grant in [`LICENSE`](LICENSE) — on the
   same terms as the rest of the project.
2. **Your contribution will also become available under the Change License
   (GNU General Public License version 3.0)** for any version whose Change Date has
   passed, exactly as the BSL 1.1 provides for the rest of the work.
3. You understand the project is **source-available, not open source**, while the
   BSL 1.1 applies, and that this is disclosed rather than hidden.

### You must have the right to contribute

Before submitting, confirm all of the following:

- **The work is yours to license.** It is your original work, or you otherwise hold
  rights sufficient to license it under the terms above.
- **It is not copied from a third party** in a way that would require a license the
  project does not have. Do not paste code, prose, artwork, or data from another
  project unless its license is compatible **and** you include the required
  attribution.
- **It carries no employer or client restriction** that would prevent licensing it
  as described. If you contribute in the course of employment or under a contract,
  confirm you have permission to do so.
- **If you add or change a dependency**, you update
  [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md) and keep the dependency set
  under permissive licenses. The project does not currently accept a new
  copyleft-licensed dependency in the shipped binary or dashboard; raise it first if
  you believe one is needed.
- **New files you add do not need a copyright header today** (the project has none),
  but do not add a header that misstates who holds the copyright.

### Contributor sign-off

Please add a `Signed-off-by: Your Name <you@example.com>` line to each commit
message, certifying you have the rights described above (the same statement used by
the Developer Certificate of Origin). This is a record-keeping practice, not a
transfer of ownership.

## Contributor License Agreement: not currently required

**No CLA exists and none is required to contribute right now.** This is a known
gap, not a settled position:

- The project currently has **no recorded contribution history and no verified
  copyright holder** (see [`docs/licensing/LICENSING.md`](docs/licensing/LICENSING.md)).
- Because contributions are inbound = outbound, the project receives them under the
  BSL 1.1 and the Change License. It does **not** obtain any right to relicense a
  contribution under a different, non-open commercial license.
- **Adding a CLA requires the copyright holder's explicit approval** and is
  deliberately not done here.

If a CLA is adopted later, it will be announced and will apply only to future
contributions unless contributors agree otherwise.

### Future dual or commercial licensing of contributions

The project may in future offer commercial licenses (see
[`docs/licensing/LICENSING.md`](docs/licensing/LICENSING.md)) that cover the same
source as the BSL. Whether an external contribution can be included in that
offer depends on the rights the project holds in it, and **inbound = outbound
alone is not sufficient** for this purpose:

- The BSL 1.1 inbound = outbound grant lets the project distribute a contribution
  under the BSL, and under GPLv3 from the Change Date. It does **not** grant the
  right to license the contribution under a separate, non-open commercial license.
- Therefore, to include a contributed change in a commercially licensed edition,
  the project would require **either a Contributor License Agreement (CLA)** that
  grants the Licensor the right to relicense the contribution, **or a separate,
  explicit commercial relicensing grant** from the contributor covering that
  contribution.
- **Without one of those, a contribution cannot be used in a commercially
  licensed edition.** It would have to be left out, or independently rewritten,
  until the required grant is obtained.
- A `Signed-off-by` line certifies origin; it is **not** a CLA and conveys **no**
  relicensing right.

**Neither a CLA nor a commercial relicensing grant is requested or required from
contributors today**, and no such agreement is being introduced by this document.
Adopting either requires the copyright holder's explicit approval, a clear
announcement, and — for existing contributors — their separate agreement; a new
agreement would not silently apply to work already contributed.

## Verifying your change

Run the checks the project itself runs. From the repository root:

```sh
# Go
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
gofmt -l ./cmd ./internal

# Frontend (Node.js 24 LTS)
cd web
npm ci
npm test
npm run typecheck
npm run build
cd ..
```

If your change touches the dashboard, rebuild the embedded assets so
`internal/gateway/assets/` matches `web/` and the distribution-asset test still
passes. Do not hand-edit the generated bundle.

If your change touches licensing files, keep [`LICENSE`](LICENSE) identical to the
standardized Business Source License 1.1 text; only the `Parameters` block may be
completed. Do not edit the `Terms`, `Covenants of Licensor`, or `Notice`.

## What makes a good pull request

- One focused change, with a clear description of **why** it is needed.
- Tests for behavior you add or fix. Do not weaken or delete an existing test to
  make a change pass.
- Documentation updated where behavior, security boundaries, or limitations change.
- No secrets, credentials, private URLs, or personal data.
- Honest reporting: if something could not be run or verified, say so in the pull
  request rather than implying it passed.

## Security

Do not report security issues in a public issue or pull request. Follow the
project's security disclosure process once it is published; until then, contact the
maintainers privately.
