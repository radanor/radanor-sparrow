# Current native validation — 2026-10-12

The Windows harness has now run natively under PowerShell 5.1: **82 PASS, 0 FAIL, 4 BLOCKED, 4 NOT RUN, 3 SIMULATED**. See the private engineering record. Native Linux and native race validation remain outstanding. Native Linux evidence for the desktop-window milestone (P4/L1) is recorded separately in the private rebranding record, the native desktop lifecycle and launcher integration of P4/L2 in the private desktop integration record, the P4/L3 WebKit sandbox and window-security measurements in the private rebranding record, and the P4/L4 AppImage packaging and its native smoke run in the private AppImage packaging record (script `linux/appimage-smoke.sh`, 29 checks), with the P4/L4.1 reproducible-build comparison in the private rebranding record (script `linux/appimage-repro.sh`); none of them changes a CE-7A row. The original overview below records the earlier harness-authoring snapshot.

<details>
<summary>Historical harness-authoring overview</summary>

# Validation harnesses

Platform validation for Radanor Sparrow Community Edition. Harnesses here run the
**real compiled binaries** against a real operating system and record one of five
statuses per requirement:

| Status | Meaning |
| --- | --- |
| `PASS` | natively executed and verified on that operating system |
| `FAIL` | executed natively and observed to be incorrect |
| `BLOCKED` | cannot be executed safely, or the required access is not available |
| `NOT RUN` | no native environment was available for this run |
| `SIMULATED` | fixture-based evidence only — never presented as native |

Cross-compilation is never recorded as a native `PASS`.

## Contents

| Path | Platform | Status |
| --- | --- | --- |
| `windows/` | Windows 11 x64 | harness built and machine-checked; **not yet executed on Windows** |
| `linux/appimage-smoke.sh` | Ubuntu 22.04.5 x86-64 | executed natively against the built AppImage: 29 checks, 0 failures |
| `linux/appimage-repro.sh` | Ubuntu 22.04.5 x86-64 | executed natively: two clean builds of one commit, one sha256 |

## Status of this directory after CE-7A

The Windows harness is runnable: `validation/windows/README.md` gives the exact
procedure. It has been

* parsed with the PowerShell language parser (all four files, zero errors,
  under PowerShell 7.4.6),
* smoke-tested as a module on Linux (result recording under `Set-StrictMode`,
  isolated-root creation, child environment construction, JSON and Markdown
  export),
* verified against the product's own IPC signing: the HMAC-SHA256 its client
  computes for a shared reference vector is byte-identical to
  `internal/ipc.sign` on the Go side,
* regression-covered on platform detection: `windows/Test-PlatformDetection.ps1`
  runs 29 synthetic-OS cases and 347 checks offline (exit 0 here), and five
  re-introduced defects still fail it — by 3, 11, 23, 39 and 7 checks,
* regression-covered on its own API usage: `windows/Test-HarnessContract.ps1`
  runs 34 checks offline (exit 0 here), driving the cursor-walking page reads
  with mock pages and asserting that no page limit exceeds the API's maximum of
  50 and that endpoints accepting no query string are never sent one,
* held to the product's real route table and query validators by
  `internal/gateway/harness_contract_test.go`, which runs in `go test ./...` on
  either platform.

It has **not** been executed on a Windows host, because no Windows host was
available. No Windows row may be promoted to `PASS` on the strength of the
checks above.

Linux evidence was produced by direct execution on Ubuntu 22.04 and is recorded
in `the private engineering record`.

</details>
