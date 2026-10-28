# Security and privacy

This page describes what Radanor Sparrow Community Edition actually does with your
computer and your data, in enough detail for you to decide whether to run it. It states
what was verified, and it names the limits of that verification instead of claiming more
than the software does.

## The design in one paragraph

Sparrow is a **local-first** application. It runs as your normal user account, in two
local processes, and it does not have an account system, a licence server, an update
channel or a telemetry pipeline. Everything it observes and everything it concludes is
written to a database in your own profile. Nothing is uploaded, because there is nothing
in the software that uploads it.

## The processes

| Process | What it does | Privileges |
| --- | --- | --- |
| `radanor-sparrow-core` (supervisor) | collects events, runs the posture checks, owns the SQLite database, serves a local IPC endpoint | your user |
| `radanor-sparrow-gateway` | serves the embedded dashboard on `127.0.0.1` and proxies the core API behind a per-launch session | your user |
| `radanor-sparrow-window` (Linux only) | presents the native GTK/WebKitGTK window and its WebKit sandbox | your user |

No component runs as a service, a scheduled task, a driver, or an elevated process. On
Windows the native window is WebView2, hosted in the same user session. **Nothing is ever
installed with administrator rights, and Sparrow refuses to run elevated.**

## What it collects

These are the features that exist in v0.1.0. Anything not listed here is not a feature of
this version, whatever a description elsewhere may suggest.

- **File activity** in the locations you choose to monitor, via the operating system's
  own notification mechanisms (inotify on Linux, the Windows change-notification APIs),
  with bounded queues and visible health/drop reporting when a source falls behind.
- **Posture checks** — evidence-backed assessments of the local computer's configuration.
  A check that cannot obtain evidence reports that it could not, rather than passing.
- **Installed software inventory**, read from the operating system's own records.
- **Listening network endpoints and services**, enumerated locally: which ports are
  listening on this machine and which processes own them. This *observes* local sockets;
  it does not capture packets, and Sparrow is not a network sensor.
- **Settings**, which you configure in the dashboard.

Sparrow does **not** read your documents, keystrokes, screen contents, clipboard, camera,
microphone, browser history or email. It does not install a kernel driver or a filter
driver. It has no anti-malware engine.

## What leaves your computer

**Nothing, from Sparrow's own code.** This is a property of the code, not a promise:

- The gateway listens on **`127.0.0.1` only**. It binds no other interface and does not
  accept connections from the network.
- The only outbound connection in the shipped Go code is a **local Unix-domain socket**
  between the dashboard gateway and the supervisor. There is no HTTP client in the
  application, no analytics endpoint, no crash reporter and no update check.
- Every request the dashboard makes is a **same-origin relative path** on the loopback
  gateway (`/api/v1/...`). The bundle contains no absolute external URL, no third-party
  script, and no web font.
- There is **no telemetry, no account, no licence key and no product activation.**
- **No auto-updater.** Updating means downloading a new artifact and replacing the file
  after verifying its checksum.

### The honest caveats

Two things are outside that statement, and hiding them would be dishonest:

1. **The rendering engine is not Sparrow.** On Windows the dashboard is rendered by
   **Microsoft Edge WebView2**; on Linux by **WebKitGTK**. Those are operating-system
   components with their own behaviour and their own privacy settings, maintained by
   Microsoft and the WebKitGTK/Ubuntu projects. Sparrow does not send anything through
   them, and it loads no remote content into them, but Sparrow cannot speak for their
   telemetry, update or crash-reporting behaviour. If your organisation manages those
   runtimes, manage them as you would for any WebView application.
2. **The operating system talks to the network on its own.** Windows Update, Ubuntu's
   package manager and your browser do what they do. Sparrow is not part of that.

If you need to prove the claim on your own machine rather than take this page's word for
it, run Sparrow and watch its sockets: `ss -tp | grep radanor` on Linux, or
`Get-NetTCPConnection -OwningProcess <pid>` on Windows. You should see a loopback
listener and no outbound connection.

## What it stores, and where

Everything is written to a private per-user directory. Directories are created `0700` and
files `0600` on Linux; the equivalent per-user ACL applies on Windows.

### Windows

| Data | Path |
| --- | --- |
| Database: history, assessments, findings, inventory, settings | `%LOCALAPPDATA%\Radanor\Shield\monitoring.db` |
| Application log | `%LOCALAPPDATA%\Radanor\Shield\application.log` |
| Window component log | `%LOCALAPPDATA%\Radanor\Shield\desktop-window.log` |
| WebView2 profile | `%LOCALAPPDATA%\Radanor\Shield\webview2\` |
| Instance locks and launch secret | `%LOCALAPPDATA%\Radanor\Shield\run\` |

### Linux

| Data | Path |
| --- | --- |
| Database | `${XDG_STATE_HOME:-~/.local/state}/radanor-shield/monitoring.db` |
| Application log | `…/radanor-shield/application.log` (private, 1 MiB, rotated per launch) |
| Window component log | `…/radanor-shield/desktop-window.log` |
| WebKit view data | `…/radanor-shield/webkit/` |
| Instance locks and launch secret | `${XDG_RUNTIME_DIR:-…/run}/radanor-shield/` |

The `radanor-shield` name is kept deliberately: an installation from before the product
was renamed to Sparrow must be found in place rather than orphaned.

**Logs can contain local detail** — paths examined, service names, error messages. They
are local files with owner-only permissions, and they are not uploaded anywhere. Treat
them as you would treat any diagnostic file before sharing it: read it first.

## Retention and deletion

Stored data is bounded, not unlimited: event history and assessment runs are subject to
retention limits you can configure in the dashboard. When a limit is reached, older
stored records are removed; the app does not compress the archive instead.

To delete everything:

- **Uninstalling does not remove your data.** That is deliberate — it protects history
  from an accidental uninstall. The database, logs and WebView2 data survive, and a later
  install uses them again.
- To remove your data, exit Sparrow and delete the state directory listed above.

There is no cloud copy, so deleting the local directory deletes all of it.

## Security controls that are implemented

These are real and tested, and they are the reason the dashboard is not simply an open
localhost page:

- a single-instance lock and a per-launch random **launch ticket**, exchanged for an
  `HttpOnly`, `SameSite=Strict` session cookie with bounded lifetimes;
- a session-bound CSRF token for state-changing requests, and exact `Origin`/`Host`
  validation against the loopback origin, with a Content-Security-Policy that permits no
  remote script;
- owner-only file permissions on the database, logs, IPC endpoint and runtime directory;
- parameterised SQL and bounded request bodies, so the dashboard cannot inject
  observations into the monitoring record: only the collectors under the supervisor can
  write findings;
- on Linux, the WebKit renderer runs inside its **bubblewrap sandbox**. If that sandbox
  cannot start, Sparrow refuses to show the window and opens your browser instead. It
  never shows an unsandboxed window.

The full threat model is in [`SECURITY_MODEL.md`](../architecture/SECURITY_MODEL.md).

## Limitations you should not misunderstand

- **Sparrow is not antivirus and not EDR.** It does not detect malware, block anything,
  quarantine anything or remove anything. It does not produce a "protection score".
- **It sees what the operating system tells it.** Events missed by the OS notification
  layer are shown as a gap in monitoring health, not silently dropped — but they are
  missed.
- **Its conclusions are evidence-backed assessments, not guarantees.** A passing check
  means the evidence it examined was consistent with the expected configuration.
- **A user who can read your files can read its database.** It defends against other
  local users and against browser-based attacks on the dashboard; it does not defend
  against an administrator or malware already running as you.
- **It is a monitoring tool, not an isolation boundary.** Running it does not make a
  compromised machine trustworthy.

## Reporting a security problem

Please report privately, not in a public issue. The channels that actually exist, and
what to expect, are in
[`SUPPORT_AND_SECURITY_REPORTING.md`](SUPPORT_AND_SECURITY_REPORTING.md).

## Related documents

- [`SECURITY_MODEL.md`](../architecture/SECURITY_MODEL.md) — assets, trust boundaries and
  the threat/control matrix.
- [`VERIFY_DOWNLOADS.md`](VERIFY_DOWNLOADS.md) — how to check that the file you have is
  the file that was published.
- [`THIRD_PARTY_LICENSE_COMPLIANCE.md`](THIRD_PARTY_LICENSE_COMPLIANCE.md) — what is
  bundled and what comes from your operating system.
