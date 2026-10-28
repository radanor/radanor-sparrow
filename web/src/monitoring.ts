// Read-only monitoring client for CE-4. Every endpoint is session-authenticated;
// this module can only read observations, never create or alter them.

export type HealthState =
  | "starting"
  | "healthy"
  | "degraded"
  | "permission_denied"
  | "unsupported"
  | "stopped"
  | "failed"
  | "recovering";

export type SourceStatus = {
  id: string;
  state: HealthState;
  recovery: string;
  platforms?: string[];
  capabilities?: string[];
  limitations?: string[];
  last_successful_event?: string;
  last_confirmation?: string;
  last_failure?: string;
  queue_overflows: number;
  dropped_events: number;
  duplicates: number;
  restart_count: number;
  monitoring_gaps: number;
  last_error?: string;
};

export type StorageInfo = {
  event_count: number;
  gap_count: number;
  database_bytes: number;
  max_events: number;
  max_database_bytes: number;
  retention_days: number;
  encrypted: boolean;
};

export type MonitoringStatus = {
  schema: string;
  generated_at: string;
  state: "active" | "partial" | "starting" | "unavailable";
  processed_events: number;
  sources: SourceStatus[];
  storage: StorageInfo;
  replay_limits: string[];
};

export type EventRecord = {
  cursor: number;
  event_id: string;
  source: string;
  type: string;
  platform: string;
  occurred_at: string;
  observed_at: string;
  severity?: string;
  evidence: string;
  schema_version: string;
  source_sequence?: string;
  metadata: Record<string, string>;
};

export type EventsPage = {
  schema: string;
  events: EventRecord[];
  next_before?: number;
  truncated: boolean;
};

export type GapRecord = {
  cursor: number;
  source: string;
  start: string;
  end: string;
  reason: string;
};

export type GapsPage = {
  schema: string;
  gaps: GapRecord[];
  total_gaps: number;
};

async function readJSON<T>(path: string): Promise<T> {
  const response = await fetch(path, { credentials: "same-origin" });
  if (!response.ok) {
    throw new Error(response.status === 503 ? "The core monitoring service is unavailable." : "Live monitoring data could not be loaded.");
  }
  return await response.json() as T;
}

export function fetchMonitoringStatus(): Promise<MonitoringStatus> {
  return readJSON<MonitoringStatus>("/api/v1/monitoring/status");
}

export function fetchMonitoringEvents(params: { before?: number; limit?: number; source?: string } = {}): Promise<EventsPage> {
  const query = new URLSearchParams();
  if (params.limit) query.set("limit", String(params.limit));
  if (params.before) query.set("before", String(params.before));
  if (params.source) query.set("source", params.source);
  const suffix = query.toString();
  return readJSON<EventsPage>("/api/v1/monitoring/events" + (suffix ? "?" + suffix : ""));
}

export function fetchMonitoringGaps(limit = 20): Promise<GapsPage> {
  return readJSON<GapsPage>("/api/v1/monitoring/gaps?limit=" + limit);
}

const stateLabels: Record<string, string> = {
  starting: "Starting",
  healthy: "Active",
  degraded: "Degraded",
  permission_denied: "Permission denied",
  unsupported: "Unsupported",
  stopped: "Stopped",
  failed: "Failed",
  recovering: "Recovering"
};

export function stateLabel(state: string): string { return stateLabels[state] ?? state; }

// stateTone maps a source's health onto a truthful visual tone. A source that
// stopped, failed, was refused or degraded access is never shown in the calm
// neutral used for "this does not apply here".
export function stateTone(state: string): "ok" | "bad" | "warn" | "neutral" {
  switch (state) {
    case "healthy": return "ok";
    case "failed": return "bad";
    case "degraded":
    case "permission_denied":
    case "stopped": return "warn";
    default: return "neutral";
  }
}

// describeCounters renders the per-source counters that indicate the recording
// was imperfect, and nothing else: a zero is noise, a non-zero is a material
// caveat. Dropped events and gaps are always named, because they are the two
// counters that mean observations were missed.
export function describeCounters(counters: { dropped_events: number; duplicates: number; restart_count: number; monitoring_gaps: number }): Array<{ label: string; value: number; material: boolean }> {
  return [
    { label: "dropped", value: counters.dropped_events, material: counters.dropped_events > 0 },
    { label: "duplicates", value: counters.duplicates, material: false },
    { label: "restarts", value: counters.restart_count, material: false },
    { label: "gaps", value: counters.monitoring_gaps, material: counters.monitoring_gaps > 0 }
  ];
}

const overallLabels: Record<string, string> = {
  active: "Monitoring active",
  partial: "Partial coverage",
  starting: "Starting",
  unavailable: "No active coverage"
};

export function overallLabel(state: string): string { return overallLabels[state] ?? state; }

export function formatTime(value?: string): string {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : date.toLocaleString();
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "—";
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + " KiB";
  return (bytes / (1024 * 1024)).toFixed(1) + " MiB";
}

// describeMetadata renders a short, bounded observation summary. Metadata values
// are untrusted source input; React escapes them and this caps what we show.
export function describeMetadata(metadata: Record<string, string>): string {
  const parts: string[] = [];
  for (const key of ["root", "name", "channel", "provider", "event_id"]) {
    const value = metadata[key];
    if (value) parts.push(key + ": " + value.slice(0, 120));
  }
  if (parts.length === 0) {
    for (const [key, value] of Object.entries(metadata).slice(0, 3)) {
      parts.push(key + ": " + value.slice(0, 80));
    }
  }
  return parts.join(" · ");
}
