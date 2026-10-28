import { afterEach, describe, expect, it, vi } from "vitest";
import { describeCounters, describeMetadata, fetchMonitoringEvents, formatBytes, overallLabel, stateLabel, stateTone } from "./monitoring";

describe("monitoring presentation helpers", () => {
  it("maps health and overall states to truthful labels", () => {
    expect(stateLabel("healthy")).toBe("Active");
    expect(stateLabel("permission_denied")).toBe("Permission denied");
    expect(stateLabel("unknown_future_state")).toBe("unknown_future_state");
    expect(overallLabel("active")).toBe("Monitoring active");
    expect(overallLabel("partial")).toBe("Partial coverage");
    expect(overallLabel("unavailable")).toBe("No active coverage");
  });

  it("formats bounded sizes and missing timestamps", () => {
    expect(formatBytes(2048)).toBe("2.0 KiB");
    expect(formatBytes(3 * 1024 * 1024)).toBe("3.0 MiB");
    expect(formatBytes(-1)).toBe("—");
  });

  it("tones a source that failed or was refused as a warning, never as calm-neutral", () => {
    expect(stateTone("healthy")).toBe("ok");
    expect(stateTone("failed")).toBe("bad");
    expect(stateTone("degraded")).toBe("warn");
    expect(stateTone("permission_denied")).toBe("warn");
    expect(stateTone("stopped")).toBe("warn");
    // Unsupported genuinely does not apply here, so neutral is truthful.
    expect(stateTone("unsupported")).toBe("neutral");
    expect(stateTone("starting")).toBe("neutral");
  });

  it("surfaces only the counters that mean observations were missed", () => {
    const counters = describeCounters({ dropped_events: 3, duplicates: 0, restart_count: 1, monitoring_gaps: 0 });
    expect(counters.map((counter) => counter.label)).toEqual(["dropped", "duplicates", "restarts", "gaps"]);
    // Dropped events and gaps are material every time; duplicates are not a
    // coverage loss and must not be dressed as one.
    expect(counters[0].material).toBe(true);
    expect(counters[1].material).toBe(false);
    expect(counters[3].material).toBe(false);
    expect(describeCounters({ dropped_events: 0, duplicates: 0, restart_count: 0, monitoring_gaps: 2 })[3].material).toBe(true);
  });

  it("caps untrusted metadata rendering", () => {
    const summary = describeMetadata({ root: "ssh", name: "a".repeat(500), channel: "Defender" });
    expect(summary).toContain("root: ssh");
    expect(summary.length).toBeLessThan(400);
    expect(describeMetadata({ custom: "value" })).toBe("custom: value");
    expect(describeMetadata({})).toBe("");
  });
});

describe("monitoring query construction", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("sends only bounded, explicit parameters", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ schema: "shield.monitoring_events/v1", events: [], truncated: false }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    await fetchMonitoringEvents({ limit: 20, before: 42, source: "native.file_integrity" });
    const url = String(fetchMock.mock.calls[0]?.[0]);
    expect(url).toBe("/api/v1/monitoring/events?limit=20&before=42&source=native.file_integrity");
    await fetchMonitoringEvents();
    expect(String(fetchMock.mock.calls[1]?.[0])).toBe("/api/v1/monitoring/events");
  });

  it("surfaces a truthful error when the core is unavailable", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    await expect(fetchMonitoringEvents()).rejects.toThrow(/core monitoring service is unavailable/);
  });
});
