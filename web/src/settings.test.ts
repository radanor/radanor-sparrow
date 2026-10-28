import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  type SettingsResponse,
  defaultInterval,
  describeInterval,
  fetchSettings,
  intervalOptions,
  isScheduleFormDirty,
  parseIntBound,
  parseDurationMinutes,
  storageRows,
  updateSettings,
  usagePercent
} from "./settings";
import { applyTheme, currentTheme, parseTheme, subscribeTheme, themeOptions } from "./theme";

const descriptor = { key: "assess_interval", type: "duration" as const, description: "How often a scheduled assessment runs.", minimum: "1h", maximum: "168h" };

describe("settings bounds", () => {
  it("parses the Go duration strings the core reports", () => {
    expect(parseDurationMinutes("1h")).toBe(60);
    expect(parseDurationMinutes("168h")).toBe(10080);
    expect(parseDurationMinutes("30m")).toBe(30);
    expect(parseDurationMinutes("nonsense")).toBeNull();
    expect(parseDurationMinutes(undefined)).toBeNull();
  });

  it("offers only intervals the core would accept", () => {
    const values = intervalOptions(descriptor).map((option) => option.value);
    expect(values).toContain("1h");
    expect(values).toContain("168h");
    const narrow = intervalOptions({ ...descriptor, minimum: "12h", maximum: "24h" }).map((option) => option.value);
    expect(narrow).toEqual(["12h", "24h"]);
    // A missing bound must not silently hide every option.
    expect(intervalOptions(undefined).length).toBeGreaterThan(1);
  });

  it("does not call an untouched form edited when no interval has been stored", () => {
    // CE-6 L-5: a core with no stored interval reports an empty effective
    // interval, and the page seeds the select with its own default. Comparing
    // that default against the empty effective value made the page dirty on
    // load, so "you have unsaved changes" appeared without any edit.
    const effective = { assess_enabled: false, assess_interval: "", assess_max_runs: 200 };
    const untouched = { enabled: false, interval: defaultInterval, maxRuns: 200 };
    expect(isScheduleFormDirty(effective, untouched)).toBe(false);

    // Every real edit is still reported.
    expect(isScheduleFormDirty(effective, { ...untouched, interval: "1h" })).toBe(true);
    expect(isScheduleFormDirty(effective, { ...untouched, enabled: true })).toBe(true);
    expect(isScheduleFormDirty(effective, { ...untouched, maxRuns: 300 })).toBe(true);

    // A stored interval is compared against exactly, in the form the core
    // reports it, so a genuine change to it is still detected.
    const stored = { ...effective, assess_interval: "24h0m0s" };
    expect(isScheduleFormDirty(stored, { ...untouched, interval: "24h0m0s" })).toBe(false);
    expect(isScheduleFormDirty(stored, { ...untouched, interval: "24h" })).toBe(true);
  });

  it("reads integer bounds and renders intervals in words", () => {
    expect(parseIntBound("10")).toBe(10);
    expect(parseIntBound(undefined)).toBeNull();
    expect(describeInterval("1h")).toBe("1 hour");
    expect(describeInterval("24h")).toBe("24 hours");
    expect(describeInterval("168h")).toBe("7 days");
    expect(describeInterval("garbage")).toBe("not reported");
  });
});

describe("storage reporting", () => {
  const storage = {
    monitoring_bytes: 1_000_000,
    monitoring_max_bytes: 64_000_000,
    assessment_bytes: 500_000,
    assessment_max_bytes: 16_000_000,
    inventory_bytes: 100_000,
    inventory_max_bytes: 8_000_000,
    database_bytes: 2_000_000,
    event_retention_days: 30,
    max_events: 200000,
    max_inventory_items_per_kind: 20000
  };

  it("reports each scope against its own independent budget", () => {
    const rows = storageRows(storage);
    expect(rows.map((row) => row.label)).toEqual([
      "Monitoring events",
      "Assessment history",
      "Software and network inventory",
      "Whole database file"
    ]);
    const inventory = rows.find((row) => row.label.includes("inventory"));
    expect(inventory?.limit).toBe(8_000_000);
    // Monitoring's budget is never presented as shared with the other scopes.
    expect(rows[0]?.note).toContain("never spent by assessments or inventory");
    // The database row has no separate budget of its own, so no percentage is
    // claimed for it.
    expect(rows[3]?.limit).toBe(0);
    expect(usagePercent(0, 0)).toBe(0);
    expect(usagePercent(9, 10)).toBe(90);
    expect(usagePercent(20, 10)).toBe(100);
  });
});

describe("settings requests", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("sends only the reviewed preference keys through a CSRF-protected POST", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ schema: "shield.settings/v1" }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    await updateSettings({ theme: "dark" });
    const [url, init] = fetchMock.mock.calls[0] ?? [];
    expect(String(url)).toBe("/api/v1/settings");
    expect((init as RequestInit).method).toBe("POST");
    expect((init as RequestInit).body).toBe(JSON.stringify({ theme: "dark" }));
    expect(((init as RequestInit).headers as Record<string, string>)["X-CSRF-Token"]).toBe("");
  });

  it("surfaces the core's refusal instead of reporting a silent success", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("theme must be one of light, dark, system", { status: 400 })));
    await expect(updateSettings({ theme: "neon" })).rejects.toThrow(/theme must be one of/);
  });

  it("reports a truthful error when the settings service is unavailable", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    await expect(fetchSettings()).rejects.toThrow(/settings service is unavailable/);
  });

  it("reads effective settings from the service", async () => {
    const payload: SettingsResponse = {
      schema: "shield.settings/v1",
      settings: {},
      effective: { assess_enabled: true, assess_interval: "24h", assess_max_runs: 50, theme: "system" },
      supported: [descriptor],
      storage: {
        monitoring_bytes: 0, monitoring_max_bytes: 0, assessment_bytes: 0, assessment_max_bytes: 0,
        inventory_bytes: 0, inventory_max_bytes: 0, database_bytes: 0, event_retention_days: 30,
        max_events: 10, max_inventory_items_per_kind: 20
      },
      config_source: "default",
      limitations: ["a limitation"]
    };
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify(payload), { status: 200 })));
    const settings = await fetchSettings();
    expect(settings.effective.assess_max_runs).toBe(50);
    expect(settings.supported[0]?.key).toBe("assess_interval");
  });
});

// The theme tests run without a DOM, so the document root is stubbed with the
// small surface applyTheme actually uses.
describe("theme handling", () => {
  const attributes = new Map<string, string>();
  beforeEach(() => {
    attributes.clear();
    vi.stubGlobal("document", {
      documentElement: {
        setAttribute: (key: string, value: string) => { attributes.set(key, value); },
        removeAttribute: (key: string) => { attributes.delete(key); },
        getAttribute: (key: string) => attributes.get(key) ?? null,
        hasAttribute: (key: string) => attributes.has(key)
      }
    });
    applyTheme("system");
  });
  afterEach(() => vi.unstubAllGlobals());

  it("falls back to the system preference for an unrecognised value", () => {
    expect(parseTheme("dark")).toBe("dark");
    expect(parseTheme("light")).toBe("light");
    expect(parseTheme("system")).toBe("system");
    expect(parseTheme("neon")).toBe("system");
    expect(parseTheme(undefined)).toBe("system");
    expect(parseTheme(3)).toBe("system");
  });

  it("applies the theme to the document and notifies subscribers", () => {
    const seen: string[] = [];
    const unsubscribe = subscribeTheme((theme) => seen.push(theme));
    applyTheme("dark");
    expect(attributes.get("data-theme")).toBe("dark");
    expect(currentTheme()).toBe("dark");
    applyTheme("system");
    // "system" removes the attribute so the operating system's preference wins.
    expect(attributes.has("data-theme")).toBe(false);
    expect(seen).toEqual(["dark", "system"]);
    unsubscribe();
    applyTheme("light");
    expect(seen).toEqual(["dark", "system"]);
  });

  it("offers each option with an explanation", () => {
    const options = themeOptions();
    expect(options.map((option) => option.value)).toEqual(["system", "light", "dark"]);
    for (const option of options) expect(option.description.length).toBeGreaterThan(10);
  });
});
