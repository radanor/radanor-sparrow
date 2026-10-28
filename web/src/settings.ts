import { postJSONBody } from "./auth";

// Read/write client for the CE-6 supported preferences.
//
// The list of what may be changed is owned by the core, not by this file: the
// settings response carries the supported keys and their bounds, and the page
// renders controls from that descriptor. The client can therefore never offer a
// preference the backend would refuse, and a new preference can be added without
// a second hardcoded list drifting out of sync.

export type SettingType = "bool" | "duration" | "int" | "enum";

export type SettingDescriptor = {
  key: string;
  type: SettingType;
  description: string;
  minimum?: string;
  maximum?: string;
  options?: string[];
};

export type EffectiveSettings = {
  assess_enabled: boolean;
  assess_interval: string;
  assess_max_runs: number;
  theme: string;
};

export type StorageUsage = {
  monitoring_bytes: number;
  monitoring_max_bytes: number;
  assessment_bytes: number;
  assessment_max_bytes: number;
  inventory_bytes: number;
  inventory_max_bytes: number;
  database_bytes: number;
  event_retention_days: number;
  max_events: number;
  max_inventory_items_per_kind: number;
};

export type SettingsResponse = {
  schema: string;
  settings: Record<string, string>;
  effective: EffectiveSettings;
  supported: SettingDescriptor[];
  storage: StorageUsage;
  config_source: string;
  next_scan_at?: string;
  limitations: string[];
};

export type SettingsUpdate = {
  assess_enabled?: boolean;
  assess_interval?: string;
  assess_max_runs?: number;
  theme?: string;
};

async function readJSON<T>(path: string): Promise<T> {
  const response = await fetch(path, { credentials: "same-origin" });
  if (!response.ok) {
    throw new Error(response.status === 503
      ? "The background settings service is unavailable."
      : "Settings could not be loaded.");
  }
  return await response.json() as T;
}

export function fetchSettings(): Promise<SettingsResponse> {
  return readJSON<SettingsResponse>("/api/v1/settings");
}

// updateSettings sends only the reviewed preference keys. The gateway and the
// core validate the same shape again; anything unsupported is refused and
// nothing is persisted.
export function updateSettings(update: SettingsUpdate): Promise<SettingsResponse> {
  return postJSONBody("/api/v1/settings", update) as Promise<SettingsResponse>;
}

// defaultInterval is the interval the schedule controls show when nothing has
// been stored yet. It is also what the page must compare against when deciding
// whether the form has been edited: the fallback is the UI's own default, not a
// change the user made.
export const defaultInterval = "24h";

// isScheduleFormDirty reports whether the schedule form differs from what is
// actually in force.
//
// The interval controls show this page's own default when the core reports no
// interval at all, so that default has to compare equal to the effective value.
// Otherwise a page whose schedule had never been configured read as edited the
// moment it loaded, and the "you have unsaved changes" warning appeared for an
// edit the user had never made.
export function isScheduleFormDirty(
  effective: { assess_enabled: boolean; assess_interval: string; assess_max_runs: number },
  form: { enabled: boolean; interval: string; maxRuns: number }
): boolean {
  const expectedInterval = effective.assess_interval || defaultInterval;
  return form.enabled !== effective.assess_enabled
    || form.interval !== expectedInterval
    || form.maxRuns !== effective.assess_max_runs;
}

// intervalOptions builds the schedule choices the core would accept from the
// descriptor's own bounds, so the UI cannot offer an out-of-range interval.
export function intervalOptions(descriptor: SettingDescriptor | undefined): Array<{ value: string; label: string }> {
  const all: Array<{ value: string; label: string }> = [
    { value: "1h", label: "Every hour" },
    { value: "3h", label: "Every 3 hours" },
    { value: "6h", label: "Every 6 hours" },
    { value: "12h", label: "Every 12 hours" },
    { value: defaultInterval, label: "Every day" },
    { value: "72h", label: "Every 3 days" },
    { value: "168h", label: "Every 7 days" }
  ];
  const min = parseDurationMinutes(descriptor?.minimum);
  const max = parseDurationMinutes(descriptor?.maximum);
  return all.filter((option) => {
    const minutes = parseDurationMinutes(option.value);
    // Every built-in option is a valid duration; an unparseable one is kept so
    // the core, not this list, is the thing that refuses it.
    if (minutes === null) return true;
    if (min !== null && minutes < min) return false;
    if (max !== null && minutes > max) return false;
    return true;
  });
}

// parseDurationMinutes parses the Go duration strings the core reports (for
// example "1h", "168h30m"). An unparseable bound is treated as absent rather
// than as zero, so a malformed bound cannot silently hide every option.
export function parseDurationMinutes(value: string | undefined): number | null {
  if (!value) return null;
  const match = /^(\d+(?:\.\d+)?)(h|m|s)$/.exec(value.trim());
  if (!match) return null;
  const amount = Number(match[1]);
  if (!Number.isFinite(amount)) return null;
  switch (match[2]) {
    case "h": return amount * 60;
    case "m": return amount;
    case "s": return amount / 60;
    default: return null;
  }
}

export function parseIntBound(value: string | undefined): number | null {
  if (!value) return null;
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) ? parsed : null;
}

// describeInterval renders a Go duration string in words for a summary line.
export function describeInterval(value: string | undefined): string {
  const minutes = parseDurationMinutes(value);
  if (minutes === null) return "not reported";
  if (minutes < 60) return Math.round(minutes) + " minutes";
  const hours = minutes / 60;
  if (hours < 48) return hours + (hours === 1 ? " hour" : " hours");
  return Math.round(hours / 24) + " days";
}

// storageRows turns the reported usage into display rows. It never fabricates a
// total: each number is exactly what the core measured for that scope.
export function storageRows(storage: StorageUsage): Array<{ label: string; used: number; limit: number; note: string }> {
  return [
    {
      label: "Monitoring events",
      used: storage.monitoring_bytes,
      limit: storage.monitoring_max_bytes,
      note: "Live event history. This budget is reserved: it is never spent by assessments or inventory."
    },
    {
      label: "Assessment history",
      used: storage.assessment_bytes,
      limit: storage.assessment_max_bytes,
      note: "Recorded scans, check results and findings, in their own independent budget."
    },
    {
      label: "Software and network inventory",
      used: storage.inventory_bytes,
      limit: storage.inventory_max_bytes,
      note: "The current snapshot only. Inventory is replaced in place, so it does not accumulate."
    },
    {
      label: "Whole database file",
      used: storage.database_bytes,
      limit: 0,
      note: "The on-disk size of the database and its write-ahead log, for context."
    }
  ];
}

export function usagePercent(used: number, limit: number): number {
  if (!Number.isFinite(used) || !Number.isFinite(limit) || limit <= 0) return 0;
  return Math.min(100, Math.max(0, Math.round((used / limit) * 100)));
}
