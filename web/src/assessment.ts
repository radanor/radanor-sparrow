import { postJSON } from "./auth";

// Read/write client for the CE-5 posture assessment. Every endpoint is
// session-authenticated, and the two state-changing calls present the CSRF
// token. Nothing here can name a scan target: the collector set is fixed.

export type Result = "pass" | "fail" | "unknown" | "not_supported" | "stale";
export type Severity = "low" | "medium" | "high" | "critical";
export type Lifecycle = "new" | "continuing" | "resolved" | "reopened" | "superseded";

// isOpenLifecycle reports whether a finding still tracks a live weakness.
// Superseded findings are retained for history but are not actionable: the rule
// that reported them was replaced, and the weakness was never verified fixed.
export function isOpenLifecycle(lifecycle: string): boolean {
  return lifecycle === "new" || lifecycle === "continuing" || lifecycle === "reopened";
}
export type CollectionStatus = "collected" | "permission_denied" | "unsupported" | "incomplete" | "failed";

export type EvidenceValue = { key: string; kind: "bool" | "string" | "int"; bool?: boolean; int?: number; text?: string };

export type Observation = {
  id: string;
  collector: string;
  platform: string;
  collected_at: string;
  status: CollectionStatus;
  source: string;
  values: EvidenceValue[] | null;
  error?: string;
  doc_url?: string;
};

export type CheckResult = {
  run_id: number;
  check_id: string;
  check_version: string;
  category: string;
  title: string;
  platform: string;
  result: Result;
  severity?: Severity;
  detail?: string;
  explanation: string;
  action: string;
  verify_method: string;
  limitations?: string[];
  evidence: Observation;
  collected_at: string;
  evaluated_at: string;
  fresh: boolean;
};

export type Finding = {
  id: number;
  fingerprint: string;
  check_id: string;
  check_version: string;
  platform: string;
  severity: Severity;
  lifecycle: Lifecycle;
  title: string;
  summary?: string;
  action: string;
  verify_method: string;
  limitations?: string[];
  evidence: Observation;
  first_seen_at: string;
  last_seen_at: string;
  resolved_at?: string;
  reopened_at?: string;
  run_id: number;
};

export type HistoryRecord = { run_id: number; lifecycle: Lifecycle; result: Result; detail?: string; recorded_at: string };

export type RunRecord = {
  id: number;
  trigger: string;
  status: "running" | "completed" | "cancelled" | "failed" | "interrupted";
  started_at: string;
  finished_at: string;
  duration_ms: number;
  checks_total: number;
  passed: number;
  failed: number;
  unknown: number;
  not_supported: number;
  stale: number;
  collectors_run: number;
  collectors_failed: number;
  new_findings: number;
  open_findings: number;
};

export type AssessmentSummary = {
  has_run: boolean;
  last_run?: RunRecord;
  last_complete_at?: string;
  open_findings: number;
  by_severity: Record<string, number>;
  failed_checks: number;
  unknown_checks: number;
  stale_checks: number;
  not_supported_checks: number;
  total_checks: number;
  checks_fresh: boolean;
  running_run_id?: number;
};

export type ScanProgress = {
  phase: "idle" | "collecting" | "evaluating" | "persisting" | "done";
  run_id?: number;
  trigger?: string;
  collectors_done: number;
  collectors_total: number;
  started_at?: string;
};

export type AssessmentStatus = {
  schema: string;
  generated_at: string;
  availability: "available" | "unavailable";
  note: string;
  scan: ScanProgress;
  running: boolean;
  scheduler: { enabled: boolean; interval?: string; config_source: string; next_scan_at?: string };
  summary: AssessmentSummary;
  last_outcome?: unknown;
  last_error?: string;
  limits: string[];
};

export type DeviceInfo = {
  schema: "shield.device_info/v1";
  state: "available" | "missing" | "incomplete" | "unavailable" | "stale";
  collection_status?: CollectionStatus;
  collected_at?: string;
  operating_system?: string;
  architecture?: string;
  platform?: string;
  stale: boolean;
  note: string;
};

export function deviceDisplay(info: DeviceInfo | null): { operatingSystem: string; architecture: string; platform: string; tone: "ok" | "warn" | "neutral"; note: string } {
	const mayShowIdentity = info?.state === "available" || info?.state === "incomplete" || info?.state === "stale";
  return {
	operatingSystem: mayShowIdentity && info?.operating_system ? info.operating_system : "Operating system not reported",
	architecture: mayShowIdentity && info?.architecture ? "Architecture " + info.architecture : "Architecture not reported",
	platform: mayShowIdentity && info?.platform ? info.platform : "platform unknown",
    tone: info?.state === "available" ? "ok" : info?.state === "stale" || info?.state === "incomplete" ? "warn" : "neutral",
    note: info && info.state !== "available" ? info.note : ""
  };
}

export type CheckDefinition = { id: string; version: string; category: string; title: string; severity?: Severity; platforms: string[] };
export type ChecksPage = { schema: string; run_id?: number; checks: CheckResult[]; summary: AssessmentSummary; definitions?: CheckDefinition[] };
export type FindingsPage = { schema: string; findings: Finding[]; total_open: number; summary: AssessmentSummary };
export type FindingPage = { schema: string; finding: Finding; history: HistoryRecord[] };
export type RunsPage = { schema: string; runs: RunRecord[]; next_before?: number };
export type RunPage = { schema: string; run: RunRecord; checks: CheckResult[] };

class DeviceRequestError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
  }
}

function unavailableDeviceInfo(): DeviceInfo {
  return {
    schema: "shield.device_info/v1",
    state: "unavailable",
    stale: false,
    note: "Device information is unavailable from this version of the background service."
  };
}

async function readJSON<T>(path: string): Promise<T> {
  const response = await fetch(path, { credentials: "same-origin" });
  if (!response.ok) {
    const message = response.status === 503 ? "The background assessment service is unavailable."
      : response.status === 401 ? "Your dashboard session is no longer valid. Relaunch Radanor Sparrow to continue."
      : "Assessment data could not be loaded.";
    if (path === "/api/v1/assessment/device") throw new DeviceRequestError(response.status, message);
    throw new Error(message);
  }
  try {
    return await response.json() as T;
  } catch {
    if (path === "/api/v1/assessment/device") throw new DeviceRequestError(response.status, "Device information could not be loaded.");
    throw new Error("Assessment data could not be loaded.");
  }
}

export function fetchAssessmentStatus(): Promise<AssessmentStatus> { return readJSON<AssessmentStatus>("/api/v1/assessment/status"); }
export function fetchDeviceInfo(): Promise<DeviceInfo> { return readJSON<DeviceInfo>("/api/v1/assessment/device"); }

// Device information was added after the other Overview contracts. A missing
// or temporarily unsupported device operation must not hide valid posture
// data, while an expired session must still fail the Overview as authentication.
export async function fetchDeviceInfoForOverview(): Promise<DeviceInfo> {
	try {
		return await fetchDeviceInfo();
	} catch (error) {
		if (error instanceof DeviceRequestError && error.status === 401) throw error;
		return unavailableDeviceInfo();
	}
}
export function fetchChecks(): Promise<ChecksPage> { return readJSON<ChecksPage>("/api/v1/assessment/checks"); }
export function fetchFindings(params: { lifecycle?: Lifecycle; before?: number; limit?: number } = {}): Promise<FindingsPage> {
  const query = new URLSearchParams();
  if (params.lifecycle) query.set("lifecycle", params.lifecycle);
  if (params.before) query.set("before", String(params.before));
  if (params.limit) query.set("limit", String(params.limit));
  const suffix = query.toString();
  return readJSON<FindingsPage>("/api/v1/assessment/findings" + (suffix ? "?" + suffix : ""));
}
export function fetchFinding(fingerprint: string): Promise<FindingPage> {
  return readJSON<FindingPage>("/api/v1/assessment/finding?fingerprint=" + encodeURIComponent(fingerprint));
}
export function fetchRuns(params: { before?: number; limit?: number } = {}): Promise<RunsPage> {
  const query = new URLSearchParams();
  if (params.before) query.set("before", String(params.before));
  if (params.limit) query.set("limit", String(params.limit));
  const suffix = query.toString();
  return readJSON<RunsPage>("/api/v1/assessment/runs" + (suffix ? "?" + suffix : ""));
}
export function fetchRun(runId: number): Promise<RunPage> { return readJSON<RunPage>("/api/v1/assessment/run?run_id=" + runId); }

export function startScan(): Promise<unknown> { return postJSON("/api/v1/assessment/scan"); }
export function cancelScan(): Promise<unknown> { return postJSON("/api/v1/assessment/cancel"); }

const resultLabels: Record<Result, string> = {
  pass: "Pass",
  fail: "Fail",
  unknown: "Unknown",
  not_supported: "Not supported",
  stale: "Stale"
};
export function resultLabel(result: string): string { return resultLabels[result as Result] ?? result; }

// Keep the stable check identity and recorded evaluation untouched while making
// the one outcome whose positive rule title reads ambiguously as a failure clear
// in the interface. All other checks retain the title supplied by the core.
export function checkOutcomeTitle(checkID: string, result: string, title: string): string {
  if (checkID === "disk_encryption_present" && result === "fail") {
    return "System-volume encryption was not detected";
  }
  return title;
}

// resultTone maps a check result onto a truthful visual tone. Unknown and
// not-supported are never rendered as a reassuring green.
export function resultTone(result: string): "ok" | "bad" | "warn" | "neutral" {
  switch (result) {
    case "pass": return "ok";
    case "fail": return "bad";
    case "stale": return "warn";
    default: return "neutral";
  }
}

const severityLabels: Record<Severity, string> = { low: "Low", medium: "Medium", high: "High", critical: "Critical" };
export function severityLabel(severity: string): string { return severityLabels[severity as Severity] ?? severity; }
export function severityTone(severity: string): "ok" | "bad" | "warn" | "neutral" {
  switch (severity) {
    case "critical":
    case "high": return "bad";
    case "medium": return "warn";
    case "low": return "neutral";
    default: return "neutral";
  }
}

const lifecycleLabels: Record<Lifecycle, string> = {
  new: "New",
  continuing: "Continuing",
  resolved: "Resolved",
  reopened: "Reopened",
  superseded: "Superseded by a newer rule"
};
export function lifecycleLabel(lifecycle: string): string { return lifecycleLabels[lifecycle as Lifecycle] ?? lifecycle; }

// lifecycleExplanation explains what a finding's current state means, including
// the two states that are easy to misread as "fixed".
const lifecycleExplanations: Record<Lifecycle, string> = {
  new: "Seen for the first time in the latest scan.",
  continuing: "Still present in the latest scan. Nothing about it has changed.",
  resolved: "A later scan collected fresh evidence that the weakness is gone. The passing evidence is kept with the finding.",
  reopened: "A weakness that had been resolved was observed again, so it is open once more.",
  superseded: "The rule that reported this was replaced by a newer version. This is retained history, not a verified fix: Sparrow never closed it on the strength of an unread or failed collection."
};
export function lifecycleExplanation(lifecycle: string): string {
  return lifecycleExplanations[lifecycle as Lifecycle] ?? "This state is not one Sparrow recognises.";
}

const collectionLabels: Record<CollectionStatus, string> = {
  collected: "Collected",
  permission_denied: "Permission denied",
  unsupported: "Unsupported",
  incomplete: "Incomplete",
  failed: "Failed"
};
export function collectionLabel(status: string): string { return collectionLabels[status as CollectionStatus] ?? status; }

// resultExplanation states, for a non-expert reader, exactly what one result
// means. It never upgrades an unverified result into reassurance: unknown,
// not-supported and stale all say plainly that nothing was proven.
const resultExplanations: Record<Result, string> = {
  pass: "The check ran, the collected evidence met the expected condition, and the reading was current when it was evaluated. A pass covers only this check on this device.",
  fail: "The check ran and the collected evidence showed the weakness. A finding was opened with the evidence attached.",
  unknown: "Sparrow could not complete this check — the platform refused access, the collector errored, or the evidence was not usable. Nothing was proven either way, so this is reported as unknown rather than as a pass.",
  not_supported: "This check does not apply to this platform or the platform does not expose what it needs. It is not a pass, and it is not a failure.",
  stale: "The check was evaluated earlier, but the evidence behind it is older than Sparrow considers current. The result is shown for history; re-run a scan to get a current answer."
};
export function resultExplanation(result: string): string {
  return resultExplanations[result as Result] ?? "This result is not one Sparrow recognises, so it is shown without interpretation.";
}

// collectionExplanation explains a collector status, which is the usual reason a
// check could not be completed.
const collectionExplanations: Record<CollectionStatus, string> = {
  collected: "The collector read the platform successfully and returned usable facts.",
  permission_denied: "The operating system refused the read for an unprivileged process. Sparrow does not elevate, so this check stays unverified until it can be read another way.",
  unsupported: "This source genuinely does not apply on this platform, so nothing was read.",
  incomplete: "Part of the information was read and part was not. The result is treated as unverified.",
  failed: "The collector could not complete. The error is shown with the evidence."
};
export function collectionExplanation(status: string): string {
  return collectionExplanations[status as CollectionStatus] ?? "The collector reported a status Sparrow does not recognise.";
}

// runStatusExplanation explains a scan's recorded outcome, including the partial
// and interrupted cases that are easy to misread as a clean result.
const runStatusExplanations: Record<string, string> = {
  running: "The scan is still in progress. Results below are the ones recorded so far.",
  completed: "The scan finished. Every compiled check was evaluated; any check that could not be read is recorded as unknown rather than being skipped silently.",
  cancelled: "You stopped the scan. Whatever had already been collected and evaluated was recorded; the rest was not assessed.",
  failed: "The scan could not finish. Its partial results are retained but do not represent a complete assessment of this device.",
  interrupted: "The scan was cut short — for example by Sparrow stopping — before it finished. Its results are partial and are not a clean baseline."
};
export function runStatusExplanation(status: string): string {
  return runStatusExplanations[status] ?? "The recorded outcome for this run is not one Sparrow recognises.";
}

// partialScanNote summarises how much of a run was incomplete, so a partial scan
// is never mistaken for full coverage.
export function partialScanNote(run: { collectors_run: number; collectors_failed: number; unknown: number; not_supported: number; stale: number }): string | null {
  const unverified = run.unknown + run.stale;
  if (run.collectors_failed === 0 && unverified === 0) return null;
  const parts: string[] = [];
  if (run.collectors_failed > 0) parts.push(run.collectors_failed + " of " + run.collectors_run + " collectors returned incomplete, permission-denied or failed evidence");
  if (unverified > 0) parts.push(unverified + " check" + (unverified === 1 ? "" : "s") + " ended unknown or stale");
  return "Partial coverage: " + parts.join(" and ") + ", so this scan is not a clean bill of health.";
}

export function phaseLabel(phase: string): string {
  switch (phase) {
    case "collecting": return "Collecting system facts";
    case "evaluating": return "Evaluating checks";
    case "persisting": return "Recording results";
    case "done": return "Finishing";
    default: return "Idle";
  }
}

// evidenceSummary renders a short, bounded, human-readable list of a collector's
// facts. Values are untrusted collector output; React escapes them and this caps
// how much is shown.
export function evidenceSummary(observation: Observation | undefined, max = 6): string {
  if (!observation?.values?.length) return observation?.error ?? "No evidence recorded.";
  const parts: string[] = [];
  for (const value of observation.values.slice(0, max)) {
    let rendered: string;
    if (value.kind === "bool") rendered = value.bool ? "true" : "false";
    else if (value.kind === "int") rendered = String(value.int ?? 0);
    else rendered = (value.text ?? "").slice(0, 80);
    parts.push(value.key + ": " + rendered);
  }
  if (observation.values.length > max) parts.push("+" + (observation.values.length - max) + " more");
  return parts.join(" · ");
}

export function formatDateTime(value?: string): string {
  if (!value) return "—";
  const date = new Date(value);
  // A timestamp at or before the Unix epoch is not an event time in this
  // product: it is Go's zero value (0001-01-01T00:00:00Z) or an epoch
  // placeholder, both of which would otherwise be rendered as a real date. A
  // reader must see "not reported" rather than a date that never happened.
  if (Number.isNaN(date.getTime()) || date.getTime() <= 0) return "—";
  return date.toLocaleString();
}

// describeSchedule renders the scheduler state in plain language for the
// dashboard. It never invents a next-scan time: when the core omits one, the
// sentence says either that automatic assessments are off or that no scan is
// scheduled yet, rather than leaving the reader to infer it from an absent
// fragment.
export function describeSchedule(scheduler: { enabled: boolean; interval?: string; config_source: string; next_scan_at?: string }): string {
  const state = scheduler.enabled
    ? "enabled every " + (scheduler.interval || "an interval that has not been set")
    : "automatic assessments are disabled";
  const parts = ["Scheduler: " + state];
  if (scheduler.config_source) parts.push(scheduler.config_source);
  // formatDateTime answers "—" for a timestamp it refuses to render (an
  // unreadable one or a zero value). An unusable time is treated as absent, so
  // the sentence ends in "no scan scheduled yet" instead of "next —".
  const next = scheduler.next_scan_at ? formatDateTime(scheduler.next_scan_at) : "";
  if (next && next !== "—") {
    parts.push("next " + next);
  } else if (scheduler.enabled) {
    parts.push("no scan scheduled yet");
  }
  return parts.join(" · ");
}

export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "—";
  if (ms < 1000) return ms + " ms";
  const seconds = ms / 1000;
  if (seconds < 60) return seconds.toFixed(1) + " s";
  return Math.floor(seconds / 60) + "m " + Math.round(seconds % 60) + "s";
}

// ---------------------------------------------------------------------------
// Overview presentation.
//
// The Overview answers "what is the state of this device?" from recorded data
// only. These helpers keep that summary honest and testable: the headline names
// what the results actually show, the counts are the assessment's own numbers
// rather than anything derived, and a reassuring tone is only ever used for a
// completed pass. Nothing here may describe the device as secure.
// ---------------------------------------------------------------------------

export type PostureTone = "ok" | "bad" | "warn" | "neutral";

export type PostureHeadline = { text: string; tone: PostureTone; detail: string };

// postureHeadline states the most serious thing the recorded results show, in
// the order a reader needs it: something failed, a finding is still open,
// results exist but were never proven, the evidence is out of date, or nothing
// is confirmed broken. The last case is deliberately not a clean bill of health.
export function postureHeadline(summary: AssessmentSummary | undefined): PostureHeadline {
  if (!summary?.has_run) {
    return {
      text: "No assessment has run yet",
      tone: "neutral",
      detail: "Run a scan to record what this device's checks can and cannot verify."
    };
  }
  const unverified = summary.unknown_checks + summary.stale_checks;
  if (summary.total_checks === 0) {
    return {
      text: "No check results were recorded",
      tone: "warn",
      detail: "The last assessment recorded no check results, so nothing about this device has been verified yet. Run a new scan."
    };
  }
  if (summary.failed_checks > 0) {
    return {
      text: summary.failed_checks === 1 ? "1 check failed" : summary.failed_checks + " checks failed",
      tone: "bad",
      detail: summary.open_findings === 0
        ? "No finding is open against these results yet; open Security Checks for the evidence."
        : summary.open_findings === 1
          ? "1 open finding carries the evidence that produced it."
          : summary.open_findings + " open findings carry the evidence that produced them."
    };
  }
  if (summary.open_findings > 0) {
    return {
      text: summary.open_findings === 1 ? "1 finding is still open" : summary.open_findings + " findings are still open",
      tone: "bad",
      detail: "No check failed in the latest assessment, but findings recorded earlier are not verified as fixed."
    };
  }
  if (unverified > 0) {
    return {
      text: unverified === 1 ? "1 check could not be verified" : unverified + " checks could not be verified",
      tone: "warn",
      detail: "Nothing failed, but these results were not proven either way, so they are not passes."
    };
  }
  if (!summary.checks_fresh) {
    return {
      text: "The evidence is out of date",
      tone: "warn",
      detail: "The last assessment finished, but its evidence is older than Sparrow treats as current. Run a new scan for a current answer."
    };
  }
  return {
    text: "No confirmed weaknesses",
    tone: "ok",
    detail: "Every check that ran met its expected condition. This covers only the checks Sparrow compiles for this platform; it says nothing about what was not checked."
  };
}

export type PostureCount = { key: string; label: string; count: number; tone: PostureTone };

// postureCounts renders one run's outcomes exactly as the run recorded them. The
// numbers are the run's own fields — nothing is derived by subtraction — and the
// pass count is only reassuring when the run actually completed. Zero counts are
// dropped so the row stays readable; the last-assessment card carries the full
// tally, zeros included.
export function postureCounts(run: RunRecord | undefined): PostureCount[] {
  if (!run) return [];
  const complete = run.status === "completed";
  const counts: PostureCount[] = [
    { key: "fail", label: "Fail", count: run.failed, tone: "bad" },
    { key: "unknown", label: "Unknown", count: run.unknown, tone: "neutral" },
    { key: "not_supported", label: "Not supported", count: run.not_supported, tone: "neutral" },
    { key: "stale", label: "Stale", count: run.stale, tone: "warn" },
    { key: "pass", label: "Pass", count: run.passed, tone: complete ? "ok" : "warn" }
  ];
  return counts.filter((count) => count.count > 0);
}

// describeLastRun is the full tally for one run, zeros included, so an absent
// category can never look like a hidden one.
export function describeLastRun(run: RunRecord | undefined): string {
  if (!run) return "No run recorded.";
  const parts = [
    run.trigger || "unspecified trigger",
    formatDuration(run.duration_ms),
    run.status,
    run.passed + " pass, " + run.failed + " fail, " + run.unknown + " unknown, " +
      run.not_supported + " not supported, " + run.stale + " stale of " + run.checks_total + " checks"
  ];
  return parts.join(" · ");
}

// unverifiedSummary states how much of the latest assessment was never proven,
// so the count cannot be read as progress towards a pass.
export function unverifiedSummary(summary: AssessmentSummary | undefined): string | null {
  if (!summary?.has_run) return null;
  const unverified = summary.unknown_checks + summary.stale_checks;
  if (unverified === 0) return null;
  return unverified + " of " + summary.total_checks + " checks in the latest assessment ended unknown or stale. They are neither passes nor failures.";
}

// checkResultRank orders results so what needs attention is read first while
// every recorded result is kept: failures, then results that were never proven,
// then checks that have not been evaluated at all (there is nothing to act on
// yet, but the check is not a pass), then checks that do not apply to this
// platform, then passes. A result the interface does not recognise sorts last
// rather than being folded into a neighbouring group.
export function checkResultRank(result?: string): number {
  switch (result) {
    case "fail": return 0;
    case "unknown":
    case "stale": return 1;
    case undefined: return 2;
    case "not_supported": return 3;
    case "pass": return 4;
    default: return 5;
  }
}

// prioritiseChecks is the recorded-order-preserving sort used by the Overview.
export function prioritiseChecks(checks: CheckResult[]): CheckResult[] {
  return checks
    .map((check, index) => ({ check, index }))
    .sort((a, b) => checkResultRank(a.check.result) - checkResultRank(b.check.result) || a.index - b.index)
    .map((entry) => entry.check);
}

// summariseCheckResults counts the outcomes that were actually recorded,
// including the checks that have no result at all. Each count is the number of
// checks in that state — nothing is derived by subtraction — and zeros are kept,
// because "0 failed" is information the reader came for.
//
// A result string the interface does not recognise is counted in no state rather
// than being forced into a wrong one. Such a check is still listed with the
// others, so nothing is hidden by the omission.
export function summariseCheckResults(results: Array<string | undefined>): PostureCount[] {
  const states: Array<{ key: string; label: string; tone: PostureTone; matches: (result: string | undefined) => boolean }> = [
    { key: "fail", label: "Fail", tone: "bad", matches: (result) => result === "fail" },
    { key: "unknown", label: "Unknown", tone: "neutral", matches: (result) => result === "unknown" },
    { key: "stale", label: "Stale", tone: "warn", matches: (result) => result === "stale" },
    { key: "not_supported", label: "Not supported", tone: "neutral", matches: (result) => result === "not_supported" },
    { key: "pass", label: "Pass", tone: "ok", matches: (result) => result === "pass" },
    { key: "unevaluated", label: "Not yet evaluated", tone: "warn", matches: (result) => result === undefined }
  ];
  return states.map((state) => ({
    key: state.key,
    label: state.label,
    count: results.filter((result) => state.matches(result)).length,
    tone: state.tone
  }));
}

// attentionCount is the number of checks that either failed or were never
// proven — the results worth acting on first.
export function attentionCount(results: Array<string | undefined>): number {
  return results.filter((result) => !result || result === "fail" || result === "unknown" || result === "stale").length;
}
