# Security Model and Threat Model

## Assets and trust boundaries

Protected assets are local findings/history, device inventory, authentication material, advisory data, exports, application binaries, check definitions, and the integrity of reported results.

Trust boundaries exist between browser and on-demand loopback gateway; gateway and background-core IPC; core and native event sources; core and local database; core and optional privileged helper; runtime and advisory servers; installer/update artifacts and installed code; and untrusted OS/event data and parsers.

The browser, all collected strings, local HTTP callers, environment variables, local files not owned by the product, advisory responses, and timestamps are untrusted. Administrator/root access can ultimately modify local state; Shield CE provides defense in depth and tamper evidence, not protection from a fully compromised administrator.

## Local authentication

The core owns a user-restricted local IPC endpoint and authenticates peers using OS credentials/ACLs and protocol versioning. The trusted launcher starts or contacts the UI gateway and requests a cryptographically random, short-lived, single-use browser ticket. The launcher places it in a URL fragment. Frontend bootstrap removes the fragment with `history.replaceState`, exchanges it by POST, and receives an opaque rotating session cookie with `HttpOnly`, `SameSite=Strict`, bounded idle/absolute lifetimes, and narrow path. Gateway-to-core calls use protected IPC; browser input cannot inject event observations.

Mutating requests also require a session-bound CSRF token. Ticket redemption is rate-limited and audited. Long-lived secrets never appear in URLs. A future headless command authenticates over local IPC, not HTTP bearer tokens stored on disk.

## HTTP controls

- Bind only `127.0.0.1` and, after dual-stack tests, `::1`; never wildcard.
- Verify accepted peer is loopback.
- Strict `Host` allowlist for expected loopback literals and actual port to resist DNS rebinding.
- Exact `Origin` validation for state-changing/ticket requests; no CORS.
- CSP with no remote scripts, `frame-ancestors 'none'`, `nosniff`, no-referrer, restrictive permissions policy, and cache controls for authenticated data.
- Bounded headers, body, JSON depth/fields, concurrency, SSE clients, and timeouts.
- Embedded asset allowlist; state/config/database/export directories are never served.

## Data and process controls

- Owner-only database, key, IPC, and log permissions; protected machine ACLs for helper state.
- Parameterized SQL, foreign keys, transactions, integrity checks, monotonic migrations, and bounded retention.
- Canonical hashes for check definitions and result content. Hashes indicate unexpected change; they do not make a compromised local database immutable.
- Typed evidence, output encoding, safe filenames, atomic export creation, no-follow/reparse-point protections, and user-confirmed overwrite.
- No shell execution. Any fallback subprocess has a fixed executable and arguments, clean environment, working directory, deadline, output cap, and process-tree termination.
- Collector results are authenticated across privileged IPC and tagged with operation/schema version. The runtime never accepts browser-supplied observations.
- Native event callbacks perform minimal bounded work and never wait indefinitely on storage.
- Event fields are allowlisted, length-bounded, sanitized, and sensitivity-classified before storage or logging.
- Source checkpoints, record identifiers, queue high-water/drop counters, restarts, clock anomalies, and observable native overflow become monitoring-health evidence.

## Threat/control/test matrix

| Threat | Primary controls | Required tests |
|---|---|---|
| Unauthorized localhost app | one-time launcher tickets, sessions, rate limits | unrelated process/browser cannot obtain a session; replay fails |
| CSRF/cross-origin access | SameSite, CSRF token, strict Origin, no CORS | hostile origins cannot read or mutate |
| DNS rebinding/Host confusion | loopback peer and exact Host validation | hostile Host and rebinding simulations rejected |
| Session theft | HttpOnly cookie, short lifetimes, rotation, no URL secret | fixation, replay, expiry, logout tests |
| Privilege escalation | unprivileged HTTP, fixed helper operations, OS ACL/peer identity | unauthorized user/client and malformed IPC fuzz tests |
| Command injection | native APIs; no shell; fixed fallback invocations | metacharacter/path/environment test corpus |
| Path traversal/export attacks | trusted roots, safe names, atomic no-follow writes | `..`, symlink, junction/reparse, overwrite tests |
| Forged observations | collectors only, IPC identity, schemas/hashes, scan transaction | API cannot submit evidence; tampered payload rejected |
| Stale/missing evidence shown secure | versioned freshness and fail-honest evaluator | every failure/age branch cannot produce pass |
| Database tampering | permissions, integrity checks, hashes, audit events | permission and modified-row detection tests |
| Feed manipulation/decompression bomb | HTTPS, signature/hash policy, size/ratio/XML limits, atomic last-known-good | corrupt, rollback, oversized, stale feed tests |
| Installer/update compromise | Authenticode, signed APT repo/package, checksums/provenance, rollback | invalid signature and downgrade rejection |
| Sensitive-data leakage | minimal collection/logging, no telemetry, passive reports | log/report redaction and network-egress tests |
| Event-flood denial of service | bounded byte/count queues, source quotas, conservative dedupe | burst/sustained flood and starvation tests |
| Silent monitoring gaps | checkpoints, source heartbeat, overflow/error recognition, gap states | clear/rotation, overflow, suspend, restart tests |
| Malicious event payload | typed parsers, limits, escaping, no event-controlled command/path | fuzz and hostile payload corpus |
| Core/gateway confused deputy | peer credentials, narrow versioned IPC, UI operation allowlist | unauthorized peer and browser-to-event injection tests |

## Service isolation and privileged helper

The baseline core and gateway run as the interactive user. The core has no TCP listener; the gateway is on demand and never elevated. No helper or machine service is approved merely by this architecture. Tests must show that high-value monitoring/check coverage cannot be reported adequately as a normal user. Any privileged component is separately threat-modeled with fixed operation/source allowlists, no arbitrary paths or queries, bounded replies, replay protection where needed, no HTTP, service hardening, and independent audit/fuzz coverage.

## Update and advisory trust

Automatic application self-update is excluded from v1. OS package mechanisms verify release artifacts. Advisory refresh stores source URL, retrieval time, upstream issuance/version, digest/signature result, parser version, and last-known-good status. Failure leaves the old feed available but visibly stale; it never silently clears vulnerabilities.

## Privacy

No mandatory account or telemetry exists. Reports are local and may contain hostname, software, ports, and findings; export UI warns accordingly. A future telemetry feature requires a separate opt-in design and approval.
