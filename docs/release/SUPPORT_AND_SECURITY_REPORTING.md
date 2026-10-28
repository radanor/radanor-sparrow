# Support and security reporting

This page lists the channels that **actually exist** for Radanor Sparrow Community
Edition, what to put in a report so it can be acted on, and which versions receive fixes.
It deliberately does not list a support email address, a response guarantee or a bug
bounty, because none of those exist.

## Before you report: is it already answered?

Most questions have a written answer:

| Question | Where |
| --- | --- |
| How do I install it? | [Windows](WINDOWS_INSTALLATION_GUIDE.md) · [Ubuntu](../deployment/LINUX_INSTALL_GUIDE.md) |
| Why does Windows say "Unknown Publisher"? | [Windows guide § warnings](WINDOWS_INSTALLATION_GUIDE.md#expect-an-unknown-publisher-warning) and [`UNSIGNED_DISTRIBUTION_NOTICE.md`](../deployment/UNSIGNED_DISTRIBUTION_NOTICE.md) |
| Did my download arrive intact? | [`VERIFY_DOWNLOADS.md`](VERIFY_DOWNLOADS.md) |
| Where is my data, and how do I delete it? | [`SECURITY_AND_PRIVACY.md`](SECURITY_AND_PRIVACY.md) |
| What am I allowed to do with it? | [`COMMUNITY_LICENSE_GUIDE.md`](COMMUNITY_LICENSE_GUIDE.md) |
| Why does it say it cannot show a window? | The reason is written to `application.log`; the Linux guide has a [symptom table](../deployment/LINUX_INSTALL_GUIDE.md#troubleshooting) |
| What is still missing from this version? | [Known limitations](RELEASE_NOTES_V0_1_0.md#known-limitations-in-v010) |

## Reporting an ordinary bug

Use the project's **GitHub issue tracker** for the Radanor Sparrow repository. Ordinary
bugs, questions about behaviour, documentation errors and installation problems belong
there, in public, so that the next person with the same problem finds the answer.

Please include:

- **the version**, taken from `BUILD-INFO` beside the executable — or, for the AppImage,
  `--appimage-extract 'usr/share/radanor-sparrow/BUILD-INFO'`;
- **the platform**: Windows 11 build number, or the Ubuntu release and whether you used
  the native window or the browser fallback;
- **the exact steps**, in order, from a known state — including whether Sparrow was
  already running;
- **what you expected and what happened**, with the exact error text or exit code;
- **the relevant part of `application.log`**, trimmed to what matters.

Before pasting a log publicly, read it: log lines can contain local paths, service names
and hostnames. Remove anything you would not post in a public forum.

**Do not include launch tickets, session cookies, IPC secrets or database contents.**

## Reporting a security vulnerability — privately

**Do not open a public issue for a security problem.** A public issue tells everyone about
a weakness in a monitoring tool before a fix exists.

**Preferred channel: GitHub private vulnerability reporting**, if it is enabled for this
repository — **Security** tab → **Report a vulnerability**. That keeps the report private,
ties it to the repository, and allows a fix to be prepared before disclosure.

Private reporting has to be switched on by a repository administrator. If the Security tab
does not offer it, it is not enabled — do not assume a report sent some other way has been
read.

**There is no security mailbox, and no address is published for one.** A published
address nobody monitors is worse than none, because a reporter would believe the report
had been received. Until a monitored private channel is approved and recorded here,
GitHub private vulnerability reporting is the only private channel.

If you have no private channel available: do not test the limits of the software on
systems you do not own, and do not publish details. Opening a public issue that says only
*"I may have found something, how do I report it privately?"* is acceptable.

### What to include in a security report

- the version, or the commit if you are testing a development build;
- the SHA-256 of the file you tested, and where you got it;
- the platform and whether the window or the browser fallback was used;
- what an attacker gains, and what they must already be able to do;
- the smallest reproduction you have, with exact commands and log lines;
- whether you have shared it with anyone else, and whether you intend to publish.

Please do not include other people's personal data, and do not attach a working exploit
for a high-impact issue before a fix exists.

## What to expect

These are the project's **stated targets, not a service-level agreement**. This is a small
community project without a staffed security team or on-call rotation.

| Stage | Target |
| --- | --- |
| Acknowledge a private report | 5 business days |
| Initial assessment (is it a vulnerability, how severe, does it need an out-of-band release) | 10 business days |
| Fix and coordinated disclosure for high and critical issues | 90 days from acknowledgement |
| Public disclosure | by agreement, after a fixed release, or 90 days after acknowledgement — whichever comes first |

If a report is out of scope, you will be told plainly and quickly, with the reason.
Credit is offered in the release notes unless you prefer otherwise. **No bug bounty
exists**, and nothing here obliges the project to pay one.

## Supported versions

**No release has been published yet**, so no version is supported today. A development
build from any branch is not supported and is not covered by this policy, however it is
labelled.

Once v0.1.0 is published, this is the policy, updated at each release:

| Version | Supported |
| --- | --- |
| the latest published `vX.Y.Z` release | security fixes |
| the previous minor release (`vX.(Y-1).*`) | fixes for high and critical issues only |
| older releases, development builds, and unmodified forks | not supported |

Fixes are published as a new release; there is no back-porting beyond the row above, and
no automatic update channel. Only artifacts published through the project's release
workflow are supported. A build whose `BUILD-INFO` says `variant=development` is a
development build whatever it is named or renamed to.

## Scope

**In scope:** the application (supervisor, dashboard, native window component, the local
IPC between them and the platform boundary), the data it keeps, the build/install/verify
path including the checksum and signature tooling, and anything that could make a rejected
artifact installable.

**Out of scope:** vulnerabilities in GTK 3, WebKitGTK, WebView2 or the Ubuntu AppArmor
policy — report those to their maintainers, they are the system components and Sparrow
does not bundle them; the AppImage runtime upstream; anything that requires an attacker to
already have your account or root; and missing hardening with no demonstrated impact.

## Safe harbour

Good-faith research is welcome. Test only systems you own or are authorised to test, keep
the data you find to the minimum needed to demonstrate the issue, give the project a
reasonable chance to fix it before publishing, and act within the law in your
jurisdiction. Do not test against other people's machines, and do not use a vulnerability
to access, change or destroy data that is not yours.

## Related documents

- [`SECURITY.md`](../../SECURITY.md) — the repository security policy these expectations
  come from.
- [`SECURITY_AND_PRIVACY.md`](SECURITY_AND_PRIVACY.md) — what the software does with your
  data.
- [`SECURITY_MODEL.md`](../architecture/SECURITY_MODEL.md) — the threat model.
