# Security policy

Radanor Sparrow Community Edition is a local security-monitoring application. It runs
with the privileges of the user who starts it, keeps its data in that user's home
directory, and does not contact the network. This file says which versions receive
security fixes and how to report a vulnerability privately.

## Supported versions

**There is no public release yet.** Until the first authorized release is published,
there is no supported version, and nothing may be described as one. The release
approval checklist in
[docs/licensing/RELEASE_APPROVAL_CHECKLIST.md](docs/licensing/RELEASE_APPROVAL_CHECKLIST.md)
is what authorizes a release; a development build from any branch is **not** supported
and is not covered by this policy.

Once a release exists, this table is the policy, and it is updated at each release:

| Version | Supported |
| --- | --- |
| the latest published `vX.Y.Z` release | security fixes |
| the previous minor release (`vX.(Y-1).*`) | fixes for high and critical issues only |
| older releases, development builds, and unmodified forks | not supported |

Fixes are published as a new release. There is no back-porting to older minors beyond the
row above, and there is no automatic update channel: upgrading means downloading the new
artifact, verifying it, and replacing the file
([docs/deployment/LINUX_INSTALL_GUIDE.md](docs/deployment/LINUX_INSTALL_GUIDE.md)).

Only artifacts published through
[.github/workflows/release.yml](.github/workflows/release.yml) are supported. A build
labelled `variant=development` — in its filename, in its `BUILD-INFO`, and in what the
installer prints — is a development build, whatever it is named.

## Reporting a vulnerability

**Do not open a public issue for a security problem.** A public issue tells everyone
about a weakness in a monitoring tool before a fix exists.

Preferred channel: **GitHub private vulnerability reporting**, if it is enabled for this
repository (repository **Security** tab → **Report a vulnerability**). It keeps the report
private, ties it to the repository, and lets a fix be prepared and released before
disclosure. Private reporting has to be switched on by a repository administrator; if the
security tab does not offer it, it is not enabled.

**The private contact is not yet decided.** No security mailbox exists and no address is
published here, because an address that nobody monitors is worse than none: a reporter
would believe the report had been received. Until a monitored private channel is approved
and recorded in this file, the only available private channel is GitHub private
vulnerability reporting, if it is enabled.

If no private channel is available to you, do not test the limits of the software on
systems you do not own, and do not publish details. Opening a public issue without
describing the weakness ("I may have found something, how do I report it privately?") is
acceptable, and the answer will be to wait until a private channel exists.

### What to include

* the version (`RadanorSparrow-<version>-x86_64.AppImage --appimage-extract
  'usr/share/radanor-sparrow/BUILD-INFO' && cat squashfs-root/usr/share/radanor-sparrow/BUILD-INFO`),
  or the commit if it is a development build;
* the SHA-256 of the file you tested, and its origin;
* the Ubuntu release and whether the window or the browser fallback was used;
* what an attacker gains, and what they must already be able to do;
* the smallest reproduction you have, and any evidence (log lines from
  `application.log`, the exact commands);
* whether you have shared it with anyone else, and whether you intend to publish.

Please do not include other people's personal data, and do not attach a working exploit
for a high-impact issue before a fix exists.

## What to expect

These are the project's stated targets, not a service-level agreement: this is a small
community project without a staffed security team.

| Stage | Target |
| --- | --- |
| Acknowledge a private report | 5 business days |
| Initial assessment (is it a vulnerability, how severe, does it need an out-of-band release) | 10 business days |
| Fix and coordinated disclosure for high and critical issues | 90 days from acknowledgement |
| Public disclosure | by agreement, after a fixed release, or 90 days after acknowledgement — whichever comes first, so a report cannot be left unresolved indefinitely by silence |

If a report is out of scope, that is said plainly and quickly, with the reason. Credit is
offered in the release notes unless the reporter prefers otherwise. Nothing here creates
an obligation to pay a bounty; no bounty program exists.

## Scope

In scope:

* the application itself: the supervisor, the dashboard, the native window component, the
  local IPC between them and the platform boundary around it;
* the data it keeps: the SQLite database, the log files and the runtime directory;
* the release and installation path: `scripts/build-*.sh`, `scripts/install-appimage.sh`,
  the checksum and signature tooling, `.github/workflows/release.yml`, and the artifacts
  those produce;
* the checksum/signature verification a user is told to perform, including anything that
  could make a rejected artifact installable.

Out of scope:

* vulnerabilities in GTK 3, WebKitGTK or the Ubuntu AppArmor policy — report those to their
  maintainers (they are the system libraries, and Sparrow does not bundle them);
* the AppImage runtime itself, upstream at the AppImage project;
* anything that requires an attacker to already have the user's account or root on the
  machine;
* missing hardening that has no demonstrated impact, and vulnerabilities in the reference
  material or unrelated projects that happen to be on the same storage.

The threat model and the security controls the product does implement are documented in
[the security model](docs/architecture/SECURITY_MODEL.md).

## Safe harbour

Good-faith research is welcome. If you test only systems you own or are authorized to
test, keep the data you find to the minimum needed to demonstrate the issue, give the
project a reasonable chance to fix it before publishing, and act in accordance with the
law in your jurisdiction, the project will not pursue legal action for that research. Do
not test against other people's machines, and do not use a vulnerability to access,
change or destroy data that is not yours.
