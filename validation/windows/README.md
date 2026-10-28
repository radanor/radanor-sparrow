# CE-7A — Windows 11 validation harness

Reproducible, **read-only** validation for a dedicated Windows 11 x64 test machine.

Everything here runs the **real compiled Windows binaries**. It does not simulate
the product, and it never records `PASS` for anything it did not natively execute.

Native Windows 11 / PowerShell 5.1 validation was completed on 2026-10-12: **82 PASS, 0 FAIL, 4 BLOCKED, 4 NOT RUN, 3 SIMULATED**. See the private engineering record for complete requirement evidence and outstanding manual checks. This run does not establish full CE-7A acceptance.

## What a GitHub-hosted runner can and cannot demonstrate

`.github/workflows/ci.yml` builds and exercises the Windows artifacts on a
GitHub-hosted `windows-2025` runner, on **development** builds. That run is real, not
simulated: it compiles the portable executable and the NSIS installer with the pinned
toolchain, then runs the silent installer, resolves the installed Start Menu target,
and exercises install, repair, staged-failure and killed-installer recovery,
locked-file uninstall retry, uninstall and user-data preservation, with the
unsigned-payload and disposable-signing checks under both Windows PowerShell 5.1 and
PowerShell 7. Release-variant artifacts are produced only by
`.github/workflows/community-release.yml` from an exact `vX.Y.Z` tag.

It is **not** a substitute for the native run described here. A hosted runner has no
interactive desktop and no standard-user interactive session, so the following are
unexecuted there and must be recorded as `NOT RUN`, never inferred from a green job:

* physical tray mouse interaction (`Invoke-SparrowTrayMouse.ps1`);
* the installer UI and the standard-user (non-elevated) token path;
* a real A-to-B upgrade from two different payloads — the headless lifecycle sets
  A = B by design and logs that A-to-B is separate native acceptance;
* downgrade refusal, which the lifecycle test runs only in its non-headless path;
* the manual sub-steps listed at the end of this file (browser window opened,
  elevation refusal, graceful Ctrl+C shutdown, Event Log channel clear, Defender
  detection event).

Run `Test-NativeHarness.ps1` for native HTTP transport, stage parsing and private ACL regressions (26 checks). Individual runtime stages automatically build/start/authenticate as needed. Comma-separated stage strings are normalized; invalid or empty selections fail. Always use a fresh GUID-named temp WorkRoot. Existing private children are preserved; root setup never recursively resets their ACLs.

## Files

| File | Purpose |
| --- | --- |
| `Invoke-RadanorValidation.ps1` | Orchestrator. Runs stages, records one of five statuses per requirement, writes JSON + Markdown results. |
| `RadanorValidation.psm1` | Helper module: process control, named-pipe IPC client with HMAC, HTTP client with session/CSRF, native Windows comparison sources, platform classification, result recording. |
| `Test-PlatformDetection.ps1` | Offline regression suite for the W-ENV-04 platform-detection rule. No Pester, no CIM, no registry, no network. |
| `Test-HarnessContract.ps1` | Offline contract suite for this harness's own API usage: cursor-walking page reads driven with mock pages, and a static check that no page limit exceeds the API's maximum. No product, no Windows, no network. |
| `README.md` | This runbook. |

## Prerequisites

* Windows 11 x64 (the product's target; Windows Server is acceptable for the
  `go test ./...` leg only and must be recorded as Server, not 11).
* An account that is a **standard user** for the main run. Do not run the main
  run elevated — the gateway refuses an elevated token by design.
* Go 1.27.x on `PATH` **or** prebuilt `radanor-sparrow-core.exe` and
  `radanor-sparrow-gateway.exe` passed with `-BinDir`.
* Windows PowerShell 5.1 (ships with Windows 11) or PowerShell 7+.
* Execution policy: `powershell -ExecutionPolicy Bypass -File ...` for this
  script only. The product itself passes no `-ExecutionPolicy`, and this harness
  does not change the machine policy.

## Run it

```powershell
cd <repo>\validation\windows

# Full run, building the binaries on the Windows machine:
$runRoot = Join-Path $env:TEMP ('radanor-ce7a-' + [guid]::NewGuid().ToString('N'))
powershell -ExecutionPolicy Bypass -File .\Invoke-RadanorValidation.ps1 `
    -WorkRoot $runRoot

# Or with binaries built elsewhere (for example produced by your CI):
powershell -ExecutionPolicy Bypass -File .\Invoke-RadanorValidation.ps1 `
    -BinDir C:\build\radanor\bin -SkipBuild

# Individual stages:
powershell -ExecutionPolicy Bypass -File .\Invoke-RadanorValidation.ps1 `
    -Stage environment,build,startup,ipc,auth,storage
```

Results land in `<WorkRoot>\results.json` and `<WorkRoot>\results.md`.
Exit code is `0` when no row is `FAIL`, `1` otherwise.

## Run the regression suite first

```powershell
powershell -ExecutionPolicy Bypass -File .\Test-PlatformDetection.ps1
```

`Test-PlatformDetection.ps1` is offline and self-contained — no Pester, no CIM,
no registry, no machine state — so it runs before the main harness and on any
host. It feeds synthetic OS evidence to the same classifier the harness uses:
29 cases, 347 checks, exit `0` when every check passes and `1` otherwise. Run it
first, so a platform-detection regression is not mistaken for a product failure
on the machine.

```powershell
powershell -ExecutionPolicy Bypass -File .\Test-HarnessContract.ps1
```

On Windows, also run the native transport and private-ACL regression:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\Test-NativeHarness.ps1
```

This creates its own fresh temporary root and runs 26 checks. Use the current
user's normal native process context; a restricted sandbox SID cannot access
directories protected by the intended current-user-only DACL.

`Test-HarnessContract.ps1` is also offline and runs on any host, including this
one before you ever reach the Windows machine: 34 checks, exit `0` or `1`. It
guards the two ways this harness previously went wrong — page limits the API
rejects, and query strings sent to endpoints that accept none — by driving the
cursor-walking page reads with mock pages (multi-page, cap-clipped, empty,
failed, non-advancing) and by reading this directory's own scripts to check every
page limit against the API's maximum of 50.

The same contract is enforced from the Go side by
[`internal/gateway/harness_contract_test.go`](../../internal/gateway/harness_contract_test.go),
which is part of `go test ./...` and validates every API call this harness makes
against the gateway's real routes and its own query validators. If you change how
the harness calls the API, that test fails rather than the Windows run.

## How the supported platform is decided (W-ENV-04)

| Source | Role |
| --- | --- |
| Registry `CurrentBuild`, then CIM `Win32_OperatingSystem.BuildNumber`, then the version string | **Authoritative for the version family** — see the build bands below |
| CIM `Win32_OperatingSystem.Caption` | Corroborates the build, and identifies the SKU — a Windows Server caption is never Windows 11 |
| Registry `ProductName` | **Branding only. It never decides the version family** |

Build bands, from Microsoft's Windows release information:

| Build | Family recorded | Supported |
| --- | --- | --- |
| ≥ 22000 | Windows 11 (RTM is 22000) | yes, when the host is x64 |
| 19046 – 21999 | Pre-release or unclassified — names no released product | no |
| 10240 – 19045 | Windows 10 (RTM 10240; the final release was 19045 / 22H2) | no |
| < 10240 | Older than Windows 10 | no |
| any build with a `Windows Server` caption | Windows Server — Server 2025 shares build 26100 with Windows 11 24H2 | no |

The 19046–21999 band is deliberately unnamed because it holds pre-release and
Insider builds — the leaked Windows 11 was 21996 — and guessing "Windows 10"
for it would be a claim the build number does not support. Such a host is not
supported either way.

The registry `ProductName` is deliberately not authoritative. Some genuine
Windows 11 machines — preinstalled OEM Home Single Language images and
Windows 10 → Windows 11 upgrades among them — still report `Windows 10 ...`
there. An earlier version of this harness tested
`ProductName -match 'Windows 11'` and duly recorded `W-ENV-04` as **FAIL** on
such a machine: build `26200`, caption `Microsoft Windows 11 Home Single
Language`, legacy `Windows 10 Home Single Language` product name. Independent
reports show the same pattern on other machines — for example a Windows 11 25H2
system with `CurrentBuild 26200`, `DisplayVersion 25H2` and a `Windows 10`
`ProductName` — so this is a property of the field, not of one image. That
combination is now recorded in `BrandingConflicts` and
`LegacyProductNameBranding`, surfaced as a warning and in the row's Evidence,
and is **not** a failure — the build number decides. A disagreement in the other
direction is ignored just as firmly: a Windows 10 build carrying a
`Windows 11` product name is still Windows 10 and is not supported. An unknown,
blank, zero or non-numeric build is never supported, and neither is a non-x64
host, so the correction relaxes nothing the plan requires.

`W-ENV-05` describes the **host**, not the PowerShell running the harness: a
32-bit PowerShell on x64 Windows (WOW64) is still an x64 host, while ARM64 and
AMD64-under-ARM64 emulation are not. The boundaries (22000, 10240) are platform
invariants, not the tested machine's build.

### Stages

| Stage | Covers |
| --- | --- |
| `environment` | Windows build/architecture, user, privilege level, culture |
| `build` | native `go build ./cmd/...` (plan item 1) |
| `startup` | core/gateway start, instance lock, elevation refusal (plan items 2–5, 24) |
| `ipc` | named-pipe IPC, HMAC, secret DACL, ticket issue (plan: named-pipe IPC) |
| `auth` | browser authentication, session, CSRF, foreign-Origin, logout |
| `storage` | SQLite file, permissions, storage panel vs disk (plan item 20) |
| `monitoring` | Startup/.ssh folders, create/modify/rename/delete, health, ingest counters, restart persistence, `wevt:` identity, item 28 |
| `posture` | assessment run, check-result hygiene, Defender/firewall/BitLocker cross-checks |
| `inventory` | registry enumeration, services, endpoints, UDP labelling, attribution, Unicode |
| `settings` | theme, schedule, retention, validation refusals (plan items 17–19) |
| `lifecycle` | the ten-step two-process restart flow of CE-7A §6 |
| `shutdown` | plan items 26–27, instance-lock survival, graceful-stop sub-step |
| `resources` | resident memory of both processes |

`-Stage all` (the default) runs everything.

## Safety rules this harness follows

* **Isolated state.** Everything lives under `-WorkRoot`. The core is started
  with `RADANOR_SPARROW_STATE_DIR` and `RADANOR_SPARROW_RUNTIME_DIR` pointing into
  it, and — unless you pass `-AllowRealMonitoredRoots` — with `APPDATA` and
  `USERPROFILE` redirected into the scratch area, so the two folders the product
  watches by default (the user Startup folder and `~/.ssh`) are created *inside*
  the scratch tree. The real folders are never written to.
* **No security configuration is changed.** No Defender change, no firewall
  change, no BitLocker change, no service change, no scheduled task, no registry
  write, no production Event Log clear. Every comparison cmdlet
  (`Get-MpComputerStatus`, `Get-NetFirewallProfile`, `Get-BitLockerVolume`,
  `Get-HotFix`, `Get-Service`, `Get-NetTCPConnection`, `Get-NetUDPEndpoint`,
  `Get-ItemProperty ...Uninstall\*`) is read-only.
* **No credentials or private keys are embedded.** The per-install IPC secret is
  read at run time from the isolated runtime directory, used to sign one
  request, and never written into `results.json`, the logs or this repository.
  Tickets and CSRF tokens are reported only by length or redacted.
* **Channel clear is refused.** See `the private engineering record`.

## Manual sub-steps the harness cannot automate

These must be performed by the operator on the Windows 11 machine and recorded
next to the corresponding `BLOCKED` rows:

1. **Browser window actually opened** (harness row `W-AUT-04`). Start the
   gateway without `-no-browser` in its own console and confirm the default
   browser opens on `http://127.0.0.1:<port>/#ticket=…`. The fragment is never
   sent in an HTTP request, so it cannot be observed from outside the browser.
2. **Elevation refusal** (row `W-STA-04`) when the run is not elevated: from an
   elevated prompt run `radanor-sparrow-gateway.exe -no-browser` and confirm
   `gateway start refused`.
3. **Graceful shutdown** (row `W-SDN-04`, plan item 25): run each binary in its
   own console, press Ctrl+C, confirm `core stopped` / `gateway stopped` in the
   logs and that no listener survives. PowerShell cannot deliver Ctrl+C to a
   child started with `CREATE_NO_WINDOW` without a native
   `CREATE_NEW_PROCESS_GROUP` helper, so the harness's automated stop is a hard
   terminate — which is deliberately the same shape as the crash case in plan
   item 27.
4. **Event Log channel clear** (row `W-MON-ITEM28`, CE-6 plan item 28): either
   create a disposable channel for the test and clear that, or record the row
   `BLOCKED` with the reason. Never clear a production channel — the CE-7A
   constraints prohibit it, and the security review records that prohibition
   (`the private engineering record`, §2.7). See
   `the private engineering record` §4.4 for the item-28 handling.
5. **Defender detection event** (row `W-MON-DEFENDER`): the Defender Operational
   channel only receives records when Defender acts.

If `W-ENV-04` reports legacy `Windows 10` branding on a Windows 11 host, **do
not** "fix" the machine to make the row pass. The build number is authoritative,
the branding is a recorded diagnostic, and editing `ProductName` would change
the machine under test for a cosmetic string.

## After the run

Copy `<WorkRoot>\results.json` and `results.md` next to
`the private engineering record` and fold the rows in. Do not promote a row
to `PASS` unless the observation named in the requirement was actually seen.

## Updating the harness on the test machine

When only the harness changes, transfer **only the harness files**, never the
whole tree — the test machine's checkout may hold local results, and the product
source must not move underneath a validation run.

On the machine holding this checkout:

```bash
BUNDLE=/tmp/ce7a-refresh.tgz
MANIFEST=/tmp/ce7a-refresh.sha256
tar -czf "$BUNDLE" \
    validation/windows/RadanorValidation.psm1 \
    validation/windows/Invoke-RadanorValidation.ps1 \
    validation/windows/Test-PlatformDetection.ps1 \
    validation/windows/Test-HarnessContract.ps1
sha256sum validation/windows/RadanorValidation.psm1 \
          validation/windows/Invoke-RadanorValidation.ps1 \
          validation/windows/Test-PlatformDetection.ps1 \
          validation/windows/Test-HarnessContract.ps1 | tee "$MANIFEST"
scp "$BUNDLE" "$MANIFEST" <user>@<windows-host>:C:/Users/<user>/AppData/Local/Temp/
```

The bundle holds exactly those four files and nothing else, so nothing else can
change on the test machine.

On the Windows test machine, from the repository root:

```powershell
$Staging = Join-Path $env:TEMP 'ce7a-refresh'
Remove-Item $Staging -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force $Staging | Out-Null
tar -xzf "$env:TEMP\ce7a-refresh.tgz" -C $Staging

# 1. Verify every extracted file against the manifest BEFORE installing anything.
$bad = 0
foreach ($line in (Get-Content "$env:TEMP\ce7a-refresh.sha256")) {
    $expected, $path = ($line -split '\s+', 2)
    $actual = (Get-FileHash -Algorithm SHA256 (Join-Path $Staging $path.Trim())).Hash.ToLower()
    if ($actual -ne $expected.ToLower()) { Write-Host "MISMATCH $path"; $bad++ }
}
if ($bad) { throw "$bad mismatched file(s); nothing was installed" }
Write-Host 'All hashes verified.'

# 2. Install only these four files, keeping a .bak of whatever was there.
foreach ($rel in 'validation/windows/RadanorValidation.psm1',
                 'validation/windows/Invoke-RadanorValidation.ps1',
                 'validation/windows/Test-PlatformDetection.ps1',
                 'validation/windows/Test-HarnessContract.ps1') {
    $dst = Join-Path (Get-Location) $rel.Replace('/', '\')
    if (Test-Path $dst) { Copy-Item $dst "$dst.bak" -Force }
    Copy-Item (Join-Path $Staging $rel) $dst -Force
    Write-Host "installed $rel"
}

# 3. Prove the update landed before running the main harness.
powershell -ExecutionPolicy Bypass -File .\validation\windows\Test-PlatformDetection.ps1
```

The install step is rejected wholesale if any hash disagrees, and the previous
files are kept alongside as `.bak`, so a failed transfer cannot leave the test
machine with a half-updated harness.
