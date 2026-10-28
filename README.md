# Radanor Sparrow Community Edition

Radanor Sparrow is a lightweight, local cybersecurity application for one Windows 11 or Ubuntu 22.04, 24.04 or 26.04 computer.

The repository is in **Linux release readiness (P5)**. It monitors file activity, assesses the security posture of the local computer with evidence-backed checks, shows the installed software and the listening network endpoints and services, and lets the user configure the scanner. Everything runs in two unprivileged local processes and nothing leaves the computer.

Radanor Sparrow is **not** antivirus and **not** EDR. It does not detect malware, block anything, remove anything, or produce a protection score.

## Product identity

| | |
| --- | --- |
| Company | Radanor |
| Product | Sparrow |
| Edition | Community Edition |
| Public name | Radanor Sparrow Community Edition |
| Description | Personal Device Security & Monitoring |
| Windows executable | `RadanorSparrow.exe` |
| Linux executable | `radanor-sparrow` |

The product was previously named *Radanor Shield*. Existing installations keep
their data: the private state directory, the Windows single-instance mutex and
pipe, the API schema versions and the `RADANOR_SHIELD_*` environment variables
are all still honoured. Retaining those names is deliberate: an installation made
before the rename must be found in place rather than orphaned.

## Components

- `radanor-sparrow-core` — background collector and storage process. Owns the SQLite database and talks only over a local IPC socket.
- `radanor-sparrow-gateway` — loopback HTTP server that serves the dashboard and proxies the core API behind a per-launch session.

The desktop distribution starts both components automatically from one executable.
Double-click `RadanorSparrow.exe` on Windows, or run `./radanor-sparrow` on Ubuntu.
It opens the authenticated local dashboard. Closing the browser keeps monitoring
running; use **Exit Radanor Sparrow** in the dashboard to stop the application.
Launch again to reopen the existing dashboard. Do not use Administrator or sudo.

See [desktop delivery](docs/deployment/DESKTOP_DELIVERY.md) for download/run,
per-user data locations, build commands, security boundaries and validation limits.
On Ubuntu, end users should start with the
[Linux installation guide](docs/deployment/LINUX_INSTALL_GUIDE.md), which covers the
AppImage, the application-menu installation, upgrades, uninstall and troubleshooting.
Developers can still start the core first, then the separate gateway.

## Development

Prerequisites: Go 1.27 and Node.js 24 LTS.

```sh
cd web
npm ci
npm test
npm run typecheck
npm run build
cd ..
go test ./...
go vet ./...
go build ./cmd/...
```

Cross-compilation checks for the Windows collectors:

```sh
GOOS=windows GOARCH=amd64 go build ./...
GOOS=windows GOARCH=amd64 go vet ./...
```

Packaging the desktop artifacts:

```sh
bash scripts/build-linux.sh              # dist/linux/radanor-sparrow
pwsh ./scripts/build-windows.ps1         # dist/windows/RadanorSparrow.exe
```

Each release directory also contains [`LICENSE`](LICENSE) and
[`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md), which must accompany any
redistribution of the executable.

### Release builds and the licensing gate

A plain build is a **development** build. A **public release** must use the
explicit release path, which runs the licensing release gate and fails closed
while any open licensing decision or package gap remains:

(The gate scans release-facing files for the vocabulary of an unfinished value.
This paragraph deliberately says "open licensing decision" rather than reusing that
vocabulary, so the sentence describing the gate cannot be mistaken for an
unresolved value inside it; `TestReadmeReleaseSectionAvoidsScannerVocabulary` keeps
it that way.)

```sh
bash scripts/build-linux.sh --release         # or:
pwsh ./scripts/build-windows.ps1 -Release
```

The gate can also be run on its own with
`bash scripts/validate-release-licensing.sh` or
`go run ./cmd/radanor-release-check` (add `--release-dir dist/linux` to validate a
built package). Add `--mode development` for development validation: it allows the
open licensing decisions recorded in the rights holder's private decision record
(today, the third-party licensing questions recorded in
[`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md) §6) but still rejects
altered license text, a missing or invalid license structure, a missing
Additional Use Grant, a missing GPLv3 Change License, and inconsistent licensing
metadata. Development CI runs that mode; the release workflow always runs the
strict default. The gate is **not** part of `go build`, `go test` or an ordinary
development build, so normal work is unaffected — and a green development CI
pipeline does **not** imply release approval. Public release artifacts come from
`.github/workflows/release.yml`, which invokes the gate and stops if it is
blocked. See [`docs/licensing/LICENSING.md`](docs/licensing/LICENSING.md).

## License

Radanor Sparrow Community Edition is **source-available** software, not open
source. It is distributed under the **Business Source License 1.1** (`BUSL-1.1`)
for each version until that version's Change Date, after which it becomes
available under the **GNU General Public License version 3.0**.

Free use is granted by the Additional Use Grant in [`LICENSE`](LICENSE):

- **personal use** on your own computers, and
- **internal use by an organization**, including commercial production
  environments, to install and run Sparrow across its own computers for internal
  administration and security monitoring.

Other uses — offering Sparrow as a competing hosted or managed security service,
commercially licensing or reselling it, or giving third parties access to a
competing Sparrow-based platform — are **not** covered by the free grant and may
require commercial permission. **A commercial license is not currently offered.**

The license becomes open source four years after each version's first public
distribution. The Business Source License already permits copying, modification,
redistribution and non-production use; it does not prohibit them. See
[`docs/licensing/LICENSING.md`](docs/licensing/LICENSING.md) for the permitted
uses and the changeover, and
[`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md) for dependency licenses.

> All four licence parameters in `LICENSE` are settled as of 2026-10-27: the Licensor
> and copyright holder, the Change Date (2030-10-27, recorded together with the
> **intended** first public distribution date of 2026-10-27 — no version has been
> published yet), and the GPLv3 Change License. The third-party questions were closed on
> **2026-10-28** by the rights holder as a recorded **risk acceptance**: the LGPL and
> header-derived readings stay legally uninterpreted, the notices shipped with the binary
> say so, and no legal certification is claimed
> ([`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md) §6).
> Nothing here states that the project is approved for publication: the release-time
> verification still owed is described in
> [`docs/release/FINAL_RELEASE_CHECKLIST.md`](docs/release/FINAL_RELEASE_CHECKLIST.md).

## Documentation

- [`docs/release/`](docs/release/README.md) — the v0.1.0 documentation written for
  people who download Sparrow: checksum verification, the Windows and Ubuntu
  installation guides, privacy, a plain-language licence guide, support and security
  reporting, the release notes, and the checklist every gate must satisfy before
  publication.
- [`docs/licensing/LICENSING.md`](docs/licensing/LICENSING.md) — permitted personal,
  business and commercial uses; the four-year changeover and GPLv3 obligations.
- [`docs/licensing/THIRD_PARTY_COMPLIANCE_DECISION_SHEET.md`](docs/licensing/THIRD_PARTY_COMPLIANCE_DECISION_SHEET.md)
  — every third-party finding, its evidence and its recorded outcome.
- [`docs/licensing/RELEASE_APPROVAL_CHECKLIST.md`](docs/licensing/RELEASE_APPROVAL_CHECKLIST.md)
  — everything that must be decided and recorded before any public release.
- [`docs/architecture/SECURITY_MODEL.md`](docs/architecture/SECURITY_MODEL.md) — assets,
  trust boundaries and the threat/control matrix.
- [`docs/deployment/LINUX_INSTALL_GUIDE.md`](docs/deployment/LINUX_INSTALL_GUIDE.md) —
  installing, upgrading, uninstalling and troubleshooting on Ubuntu.
- [`docs/deployment/RELEASE_SIGNING.md`](docs/deployment/RELEASE_SIGNING.md) — what a
  checksum proves and what only a signature does, and the key and trust model.
- [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md) — third-party dependency licenses and
  attribution obligations.
- [`SECURITY.md`](SECURITY.md) — supported versions, how to report a vulnerability
  privately, and what to expect.
- `CONTRIBUTING.md` — contribution and licensing requirements.

Single-executable packaging is implemented for Windows and Ubuntu, including a
reproducible AppImage that carries the native GTK/WebKitGTK window component. The
native window and its WebKit sandbox are validated on Ubuntu 22.04 and 26.04 under
X11 and on 24.04 with an administrator-installed AppArmor profile; stock 24.04
restricts unprivileged user namespaces, so it uses the authenticated browser, and
GNOME/Wayland, dock and accessibility behavior on 24.04/26.04 is not yet measured.
Public release signing, the human visual approval of the derived 128/256 px launcher art
and the GitHub release controls remain outstanding. The licensing gate now passes: the
third-party questions were closed on 2026-10-28 by the rights holder as a recorded **risk
acceptance** — the LGPL and header-derived interpretations remain legally uninterpreted,
and no legal certification is claimed. Updates are manual by
design: there is no auto-updater, and a downloadable artifact is checked against its
published checksum before it is installed. Details are in
[the Linux installation guide](docs/deployment/LINUX_INSTALL_GUIDE.md) and
[release signing and verification](docs/deployment/RELEASE_SIGNING.md).
