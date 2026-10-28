import { afterEach, describe, expect, it, vi } from "vitest";
import {
  type AssessmentSummary,
  type CheckResult,
  type Result,
  type RunRecord,
  attentionCount,
  checkOutcomeTitle,
  checkResultRank,
  collectionLabel,
  describeLastRun,
  describeSchedule,
  deviceDisplay,
  evidenceSummary,
  fetchDeviceInfo,
  fetchDeviceInfoForOverview,
  fetchRuns,
  formatDateTime,
  formatDuration,
  isOpenLifecycle,
  lifecycleLabel,
  partialScanNote,
  phaseLabel,
  postureCounts,
  postureHeadline,
  prioritiseChecks,
  resultLabel,
  resultTone,
  severityTone,
  summariseCheckResults,
  unverifiedSummary
} from "./assessment";
import { csrfToken, postJSON } from "./auth";

describe("assessment presentation helpers", () => {
  it("clarifies a failed system-volume encryption outcome without renaming the check", () => {
    expect(checkOutcomeTitle("disk_encryption_present", "fail", "The system volume uses managed encryption"))
      .toBe("System-volume encryption was not detected");
    expect(checkOutcomeTitle("disk_encryption_present", "pass", "The system volume uses managed encryption"))
      .toBe("The system volume uses managed encryption");
    expect(checkOutcomeTitle("firewall_enabled", "fail", "A firewall is enabled"))
      .toBe("A firewall is enabled");
  });

  it("renders the bounded device contract instead of searching security-check evidence", () => {
    expect(deviceDisplay({
      schema: "shield.device_info/v1", state: "available", collection_status: "collected",
      operating_system: "Ubuntu 22.04.5 LTS", architecture: "amd64", platform: "ubuntu",
      collected_at: "2026-10-12T14:00:00Z", stale: false, note: "fresh"
    })).toEqual({
      operatingSystem: "Ubuntu 22.04.5 LTS", architecture: "Architecture amd64",
      platform: "ubuntu", tone: "ok", note: ""
    });
    expect(deviceDisplay({
      schema: "shield.device_info/v1", state: "stale", collection_status: "collected",
      operating_system: "Ubuntu 22.04.5 LTS", architecture: "amd64", platform: "ubuntu",
      stale: true, note: "Run a new assessment to refresh it."
    }).note).toContain("refresh");
    expect(deviceDisplay(null).operatingSystem).toBe("Operating system not reported");
  });

  it("suppresses identity fields from unavailable evidence", () => {
    expect(deviceDisplay({
      schema: "shield.device_info/v1", state: "unavailable", collection_status: "failed",
      operating_system: "Old untrusted OS", architecture: "old-arch", platform: "old-platform",
      stale: false, note: "Device information could not be read."
    })).toEqual({
      operatingSystem: "Operating system not reported", architecture: "Architecture not reported",
      platform: "platform unknown", tone: "neutral", note: "Device information could not be read."
    });
  });
  it("never renders Go's zero timestamp as a date", () => {
    // CE-6 M-3: `omitempty` does not omit a Go time.Time, so a schedule with no
    // next scan used to serialise 0001-01-01T00:00:00Z and the dashboard showed
    // a real-looking date in the year 1.
    expect(formatDateTime("0001-01-01T00:00:00Z")).toBe("—");
    expect(formatDateTime("1970-01-01T00:00:00Z")).toBe("—");
    expect(formatDateTime("1899-12-31T23:59:59Z")).toBe("—");
    expect(formatDateTime("not a date")).toBe("—");
    expect(formatDateTime(undefined)).toBe("—");
    expect(formatDateTime("")).toBe("—");
    // A genuine timestamp still renders.
    const real = formatDateTime("2026-10-12T11:09:32+05:30");
    expect(real).not.toBe("—");
    expect(real).toContain("2026");
  });

  it("describes a disabled schedule without inventing a next scan", () => {
    const disabled = describeSchedule({ enabled: false, config_source: "disabled" });
    expect(disabled).toContain("disabled");
    expect(disabled).not.toContain("next");

    const enabled = describeSchedule({
      enabled: true, interval: "24h0m0s", config_source: "settings", next_scan_at: "2026-10-13T09:00:00Z"
    });
    expect(enabled).toContain("enabled every 24h0m0s");
    expect(enabled).toContain("settings");
    expect(enabled).toContain("next");
    expect(enabled).toContain("2026");

    // Enabled but with no published time: say so rather than dropping the clause.
    expect(describeSchedule({ enabled: true, interval: "6h", config_source: "environment" }))
      .toContain("no scan scheduled yet");

    // A zero timestamp must not survive as a date, or as an em dash either.
    const fabricated = describeSchedule({
      enabled: true, interval: "1h", config_source: "settings", next_scan_at: "0001-01-01T00:00:00Z"
    });
    expect(fabricated).toContain("no scan scheduled yet");
    expect(fabricated).not.toContain("—");
    expect(fabricated).not.toContain("0001");
  });
  it("labels every check result honestly", () => {
    expect(resultLabel("pass")).toBe("Pass");
    expect(resultLabel("fail")).toBe("Fail");
    expect(resultLabel("unknown")).toBe("Unknown");
    expect(resultLabel("not_supported")).toBe("Not supported");
    expect(resultLabel("stale")).toBe("Stale");
    expect(resultLabel("something_new")).toBe("something_new");
  });

  it("never renders unknown or unsupported results as a passing tone", () => {
    expect(resultTone("pass")).toBe("ok");
    expect(resultTone("fail")).toBe("bad");
    expect(resultTone("stale")).toBe("warn");
    expect(resultTone("unknown")).toBe("neutral");
    expect(resultTone("not_supported")).toBe("neutral");
  });

  it("maps severities and lifecycle states", () => {
    expect(severityTone("critical")).toBe("bad");
    expect(severityTone("medium")).toBe("warn");
    expect(severityTone("low")).toBe("neutral");
    expect(lifecycleLabel("reopened")).toBe("Reopened");
    expect(lifecycleLabel("superseded")).toBe("Superseded by a newer rule");
    expect(collectionLabel("permission_denied")).toBe("Permission denied");
    expect(phaseLabel("collecting")).toBe("Collecting system facts");
  });

  it("describes incomplete collection without calling every collector failed", () => {
    const note = partialScanNote({ collectors_run: 11, collectors_failed: 1, unknown: 4, not_supported: 0, stale: 0 }) ?? "";
    expect(note).toContain("incomplete, permission-denied or failed evidence");
    expect(note).not.toContain("collectors failed");
    expect(note).toContain("4 checks ended unknown or stale");
    // One unverified check reads as one check, not "1 check(s)".
    const single = partialScanNote({ collectors_run: 11, collectors_failed: 0, unknown: 1, not_supported: 0, stale: 0 }) ?? "";
    expect(single).toContain("1 check ended unknown or stale");
    expect(single).not.toContain("(s)");
  });

  it("treats only genuinely open lifecycles as actionable", () => {
    // A superseded finding is retained history, not a live weakness: the rule
    // that reported it was replaced and remediation was never verified.
    expect(isOpenLifecycle("new")).toBe(true);
    expect(isOpenLifecycle("continuing")).toBe(true);
    expect(isOpenLifecycle("reopened")).toBe(true);
    expect(isOpenLifecycle("resolved")).toBe(false);
    expect(isOpenLifecycle("superseded")).toBe(false);
  });

  it("summarises bounded evidence without leaking unbounded text", () => {
    const summary = evidenceSummary({
      id: "x",
      collector: "x",
      platform: "ubuntu",
      collected_at: "2026-01-01T00:00:00Z",
      status: "collected",
      source: "test",
      values: [
        { key: "enabled", kind: "bool", bool: true },
        { key: "count", kind: "int", int: 3 },
        { key: "long", kind: "string", text: "a".repeat(400) }
      ]
    });
    expect(summary).toContain("enabled: true");
    expect(summary).toContain("count: 3");
    expect(summary.length).toBeLessThan(400);
    expect(evidenceSummary(undefined)).toBe("No evidence recorded.");
  });

  it("formats durations truthfully", () => {
    expect(formatDuration(0)).toBe("—");
    expect(formatDuration(250)).toBe("250 ms");
    expect(formatDuration(2500)).toBe("2.5 s");
    expect(formatDuration(65000)).toBe("1m 5s");
  });
});

// The Overview leads with these summaries, so they are the page's honesty layer:
// a reassuring headline must never appear when something failed, when a result
// was never proven, or when the evidence is out of date.
describe("overview posture presentation", () => {
  const summary = (overrides: Partial<AssessmentSummary> = {}): AssessmentSummary => ({
    has_run: true,
    last_run: run(),
    last_complete_at: "2026-10-12T11:09:32+05:30",
    open_findings: 0,
    by_severity: {},
    failed_checks: 0,
    unknown_checks: 0,
    stale_checks: 0,
    not_supported_checks: 2,
    total_checks: 11,
    checks_fresh: true,
    ...overrides
  });
  const run = (overrides: Partial<RunRecord> = {}): RunRecord => ({
    id: 8,
    trigger: "manual",
    status: "completed",
    started_at: "2026-10-12T11:09:29+05:30",
    finished_at: "2026-10-12T11:09:32+05:30",
    duration_ms: 2500,
    checks_total: 11,
    passed: 8,
    failed: 0,
    unknown: 1,
    not_supported: 2,
    stale: 0,
    collectors_run: 11,
    collectors_failed: 1,
    new_findings: 0,
    open_findings: 0,
    ...overrides
  });

  it("states the most serious recorded fact, in order", () => {
    expect(postureHeadline(undefined)).toMatchObject({ text: "No assessment has run yet", tone: "neutral" });
    expect(postureHeadline(summary({ failed_checks: 2, open_findings: 2 })))
      .toMatchObject({ text: "2 checks failed", tone: "bad" });
    // Singular and plural read as English, not as "finding(s)".
    expect(postureHeadline(summary({ failed_checks: 1, open_findings: 1 })).detail).toContain("1 open finding carries the evidence that produced it.");
    expect(postureHeadline(summary({ failed_checks: 2, open_findings: 2 })).detail).toContain("2 open findings carry the evidence that produced them.");
    expect(postureHeadline(summary({ failed_checks: 2 })).detail).not.toContain("(s)");
    // A finding that is still open is not softened because the latest run passed.
    const open = postureHeadline(summary({ open_findings: 1 }));
    expect(open.tone).toBe("bad");
    expect(open.text).toContain("still open");
    // Unverified results never read as reassurance.
    const unverified = postureHeadline(summary({ failed_checks: 0, unknown_checks: 4, stale_checks: 1 }));
    expect(unverified).toMatchObject({ text: "5 checks could not be verified", tone: "warn" });
    expect(unverified.detail).toContain("not passes");
    expect(postureHeadline(summary({ failed_checks: 0, unknown_checks: 0, stale_checks: 0, checks_fresh: false })).tone).toBe("warn");
    // A run that recorded nothing must not be presented as a clean result.
    const empty = postureHeadline(summary({ total_checks: 0, failed_checks: 0, unknown_checks: 0, stale_checks: 0 }));
    expect(empty.tone).toBe("warn");
    expect(empty.text).toContain("No check results");
  });

  it("never tells the reader the device is secure", () => {
    const cases = [
      postureHeadline(undefined),
      postureHeadline(summary()),
      postureHeadline(summary({ failed_checks: 1 })),
      postureHeadline(summary({ open_findings: 3 })),
      postureHeadline(summary({ unknown_checks: 2 })),
      postureHeadline(summary({ total_checks: 0 }))
    ];
    for (const headline of cases) {
      expect(headline.text.toLowerCase()).not.toContain("secure");
      expect(headline.detail.toLowerCase()).not.toContain("secure");
      expect(headline.text).not.toBe("");
      expect(headline.detail).not.toBe("");
    }
    // The healthy case still states what it does not cover.
    const clear = postureHeadline(summary({ unknown_checks: 0, not_supported_checks: 0 }));
    expect(clear).toMatchObject({ text: "No confirmed weaknesses", tone: "ok" });
    expect(clear.detail).toContain("nothing about what was not checked");
  });

  it("reports only the counts the run itself recorded", () => {
    expect(postureCounts(undefined)).toEqual([]);
    const counts = postureCounts(run());
    // Zeros are dropped; nothing is inferred by subtraction (there is no pass
    // count on the summary, and the run's own number is used instead).
    expect(counts.map((count) => [count.label, count.count])).toEqual([["Unknown", 1], ["Not supported", 2], ["Pass", 8]]);
    expect(counts.find((count) => count.label === "Fail")).toBeUndefined();
    // A pass is only reassuring when the run completed.
    expect(postureCounts(run())[2].tone).toBe("ok");
    expect(postureCounts(run({ status: "cancelled" }))[2].tone).toBe("warn");
    // Unverified and inapplicable results are never toned as a pass.
    for (const label of ["Unknown", "Not supported"]) {
      expect(postureCounts(run()).find((count) => count.label === label)?.tone).not.toBe("ok");
    }
    expect(postureCounts(run({ failed: 3 }))[0]).toMatchObject({ label: "Fail", count: 3, tone: "bad" });
  });

  it("states the full tally of a run, zeros included", () => {
    expect(describeLastRun(undefined)).toBe("No run recorded.");
    const described = describeLastRun(run({ status: "interrupted", passed: 0, failed: 1, unknown: 0, not_supported: 0, stale: 0 }));
    expect(described).toContain("manual");
    expect(described).toContain("2.5 s");
    expect(described).toContain("interrupted");
    expect(described).toContain("0 pass, 1 fail, 0 unknown, 0 not supported, 0 stale of 11 checks");
  });

  it("quantifies unverified results only when there are some", () => {
    expect(unverifiedSummary(undefined)).toBeNull();
    expect(unverifiedSummary(summary({ unknown_checks: 0, stale_checks: 0 }))).toBeNull();
    const text = unverifiedSummary(summary({ unknown_checks: 3, stale_checks: 1 })) ?? "";
    expect(text).toContain("4 of 11 checks");
    expect(text).toContain("neither passes nor failures");
  });

  it("reads the most serious check results first without dropping any", () => {
    const check = (check_id: string, result: Result): CheckResult => ({
      run_id: 8, check_id, check_version: "1", category: "c", title: check_id, platform: "ubuntu",
      result, explanation: "", action: "", verify_method: "",
      evidence: { id: "e", collector: "c", platform: "ubuntu", collected_at: "2026-10-12T11:09:30Z", status: "collected", source: "s", values: [] },
      collected_at: "2026-10-12T11:09:30Z", evaluated_at: "2026-10-12T11:09:31Z", fresh: true
    });
    const results: Result[] = ["pass", "not_supported", "unknown", "pass", "fail", "stale"];
    const ordered = prioritiseChecks(results.map((result, index) => check("c" + index, result)));
    // Nothing is lost, and the order inside each group is the recorded order.
    expect(ordered.map((entry) => entry.check_id)).toEqual(["c4", "c2", "c5", "c1", "c0", "c3"]);
    expect(ordered.map((entry) => entry.result)).toEqual(["fail", "unknown", "stale", "not_supported", "pass", "pass"]);
  });

  it("ranks a result Sparrow has not seen last instead of guessing a group", () => {
    expect(checkResultRank("fail")).toBe(0);
    expect(checkResultRank("unknown")).toBe(1);
    expect(checkResultRank("stale")).toBe(1);
    // A check that never ran has nothing to act on yet, but it is not a pass
    // either, so it sorts above "not supported" and below the unproven results.
    expect(checkResultRank(undefined)).toBe(2);
    expect(checkResultRank("not_supported")).toBe(3);
    expect(checkResultRank("pass")).toBe(4);
    expect(checkResultRank("invented_future_result")).toBe(5);
    expect(checkResultRank("invented_future_result")).toBeGreaterThan(checkResultRank("pass"));
  });

  it("counts every recorded state, zeros included, without inventing a total", () => {
    const counts = summariseCheckResults(["fail", "fail", "unknown", "stale", "not_supported", "pass", undefined]);
    expect(counts.map((count) => [count.label, count.count])).toEqual([
      ["Fail", 2],
      ["Unknown", 1],
      ["Stale", 1],
      ["Not supported", 1],
      ["Pass", 1],
      ["Not yet evaluated", 1]
    ]);
    // A category with nothing in it is reported as zero rather than omitted, so
    // "0 failed" can be read on its own terms.
    const zeros = summariseCheckResults([undefined, undefined]);
    expect(zeros[0]).toMatchObject({ label: "Fail", count: 0, tone: "bad" });
    expect(zeros.every((count) => count.count === 0 || count.label === "Not yet evaluated")).toBe(true);
    // An unrecognised result is counted in no bucket (it stays visible in the
    // list) rather than being forced into a neighbouring one.
    const unknownValue = summariseCheckResults(["whatever_the_core_sends"]);
    expect(unknownValue.reduce((total, count) => total + count.count, 0)).toBe(0);
  });

  it("counts the results worth acting on first", () => {
    // Failed and never-proven results, including checks that never ran; a
    // not-supported check is not something the user can act on.
    expect(attentionCount(["fail", "unknown", "stale", undefined, "not_supported", "pass"])).toBe(4);
    expect(attentionCount(["pass", "not_supported"])).toBe(0);
    expect(attentionCount([])).toBe(0);
  });
});

describe("assessment query construction", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("builds only bounded, allowlisted run query parameters", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ schema: "shield.assessment_runs/v1", runs: [] }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    await fetchRuns({ limit: 10, before: 42 });
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe("/api/v1/assessment/runs?before=42&limit=10");
    await fetchRuns();
    expect(String(fetchMock.mock.calls[1]?.[0])).toBe("/api/v1/assessment/runs");
  });

  it("reads device information from its dedicated authenticated endpoint", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      schema: "shield.device_info/v1", state: "available", operating_system: "Ubuntu 22.04.5 LTS",
      architecture: "amd64", platform: "ubuntu", stale: false, note: "fresh"
    }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const device = await fetchDeviceInfo();
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe("/api/v1/assessment/device");
    expect(device.operating_system).toBe("Ubuntu 22.04.5 LTS");
    expect(device.architecture).toBe("amd64");
  });

  it.each([404, 503])("degrades device HTTP %d without hiding the Overview", async (status) => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unsupported private detail", { status })));
    const device = await fetchDeviceInfoForOverview();
    expect(device.state).toBe("unavailable");
    expect(device.operating_system).toBeUndefined();
    expect(device.note).not.toContain("private detail");
  });

  it("degrades a malformed device response without publishing identity", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("not json", { status: 200 })));
    await expect(fetchDeviceInfoForOverview()).resolves.toMatchObject({ state: "unavailable", stale: false });
  });

  it("does not swallow an expired device-session response", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("private session detail", { status: 401 })));
    await expect(fetchDeviceInfoForOverview()).rejects.toThrow(/session is no longer valid/);
    await expect(fetchDeviceInfoForOverview()).rejects.not.toThrow(/private session detail/);
  });

  it("surfaces a truthful error when the core is unavailable", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    await expect(fetchRuns()).rejects.toThrow(/assessment service is unavailable/);
  });
});

describe("state-changing assessment calls", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("sends the CSRF token but no target parameters", async () => {
    expect(csrfToken()).toBe("");
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ schema: "shield.assessment_scan/v1", accepted: true }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    await postJSON("/api/v1/assessment/scan");
    const [, init] = fetchMock.mock.calls[0] ?? [];
    expect((init as RequestInit).method).toBe("POST");
    expect((init as RequestInit).body).toBe("{}");
    expect(((init as RequestInit).headers as Record<string, string>)["X-CSRF-Token"]).toBe("");
  });
});
