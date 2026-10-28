import { postJSONBody } from "./auth";

// Read/write client for the CE-6 locally observed software and network
// inventory. Every endpoint is session-authenticated; the only state-changing
// call (refresh) takes no parameters and re-runs the fixed collector set.
//
// The module also holds the presentation vocabulary for inventory facts. Two
// rules are encoded here rather than in each page:
//
//   - An inventory item is an observation, never a verdict. A package version
//     is never matched against a vulnerability list, and a listening socket is
//     never described as reachable from another machine.
//   - Anything the platform could not read is labelled unknown/unsupported,
//     never rendered as an empty-but-healthy result.

export type Kind = "software" | "endpoint" | "service";
export type CollectionStatus = "collected" | "permission_denied" | "unsupported" | "incomplete" | "failed";

export type SnapshotRecord = {
  kind: Kind;
  observed_at: string;
  total: number;
  stored: number;
  truncated: boolean;
  source: string;
  note?: string;
};

export type ItemRecord = {
  kind: Kind;
  name: string;
  version?: string;
  source: string;
  platform?: string;
  fields?: Record<string, string>;
  observed_at: string;
};

export type InventoryStatus = {
  schema: string;
  has_snapshot: boolean;
  snapshots: SnapshotRecord[];
  stored_bytes: number;
  max_bytes: number;
  max_items_per_kind: number;
  storage_pressure: boolean;
  generated_at: string;
  limitations: string[];
  software_count: number;
  endpoint_count: number;
  service_count: number;
  refreshing: boolean;
  last_refresh?: string;
  last_error?: string;
  failures?: Record<string, string>;
};

export type ItemsPage = {
  schema: string;
  kind: Kind;
  items: ItemRecord[];
  total: number;
  next_after?: string;
  sources: string[];
  last_error?: string;
};

export type ItemQuery = { search?: string; source?: string; after?: string; limit?: number };

async function readJSON<T>(path: string): Promise<T> {
  let response: Response;
  try {
    response = await fetch(path, { credentials: "same-origin" });
  } catch {
    throw new Error("The local inventory service could not be reached.");
  }
  if (!response.ok) {
    switch (response.status) {
      case 400: throw new Error("The inventory request was refused as invalid.");
      case 401: throw new Error("Your dashboard session is no longer valid. Relaunch Radanor Sparrow to continue.");
      case 503: throw new Error("The background inventory service is unavailable.");
      default: throw new Error("The inventory service returned an unexpected response.");
    }
  }
  try {
    return await response.json() as T;
  } catch {
    throw new Error("The inventory service returned an invalid response.");
  }
}

export function inventoryLoadMessage(scope: "status" | "endpoint" | "service", error: unknown): string {
  const subject = scope === "status" ? "Inventory status" : scope === "endpoint" ? "Network endpoints" : "Services";
  const detail = error instanceof Error ? error.message : "Inventory data could not be loaded.";
  return subject + " could not be loaded. " + detail;
}

function itemsPath(kind: Kind, params: ItemQuery): string {
  const query = new URLSearchParams();
  if (params.search) query.set("search", params.search);
  if (params.source) query.set("source", params.source);
  if (params.after) query.set("after", params.after);
  if (params.limit) query.set("limit", String(params.limit));
  const suffix = query.toString();
  const route: Record<Kind, string> = {
    software: "software",
    endpoint: "endpoints",
    service: "services"
  };
  return "/api/v1/inventory/" + route[kind] + (suffix ? "?" + suffix : "");
}

export function fetchInventoryStatus(): Promise<InventoryStatus> {
  return readJSON<InventoryStatus>("/api/v1/inventory/status");
}

export function fetchInventoryItems(kind: Kind, params: ItemQuery = {}): Promise<ItemsPage> {
  return readJSON<ItemsPage>(itemsPath(kind, params));
}

// refreshInventory asks the core to re-run the fixed inventory collectors. It
// names no target: the collector set is fixed at compile time, and the call
// carries no body.
export function refreshInventory(): Promise<unknown> {
  return postJSONBody("/api/v1/inventory/refresh", {});
}

// snapshotFor returns the recorded coverage for one kind, if it was collected.
export function snapshotFor(status: InventoryStatus | null, kind: Kind): SnapshotRecord | undefined {
  return status?.snapshots.find((snapshot) => snapshot.kind === kind);
}

// isPartial reports whether a snapshot is known to be an incomplete reading.
// It is a single place for that question because a partial list must never be
// presented as a finished one, and "truncated" now covers two different causes:
// records the store dropped, and records the collector never managed to read.
export function isPartial(snapshot: SnapshotRecord | undefined): boolean {
  return snapshot?.truncated === true;
}

// truncationDisclosure is the clause the core appends to a kind's note when the
// collection itself was clipped. It is matched here only so the panel does not
// repeat the same sentence twice, never to decide whether the list is partial:
// that decision is `isPartial` above, which reads the boolean flag the core sends.
const truncationDisclosure = "cut off at the collection output limit";

function noteWithoutTruncationClause(note: string): string {
  return note
    .split("; ")
    .filter((clause) => !clause.includes(truncationDisclosure))
    .join("; ");
}

// coverageNote explains, in plain language, what one kind's snapshot did and did
// not cover. It returns null when there is genuinely nothing to disclose.
//
// The two truncation causes are stated separately rather than merged into one
// sentence about stored entries: a reading cut short at the collection output
// limit lost nothing to storage, and describing it as "N of N stored" would be
// both false and reassuring. A collection failure is combined with the coverage
// rather than replacing it, because a snapshot that exists but reported a
// problem is not the same as a category that could not be collected at all.
export function coverageNote(snapshot: SnapshotRecord | undefined, failure?: string): string | null {
  if (!snapshot) {
    return failure
      ? "This inventory could not be collected on this device: " + failure
      : "No inventory has been recorded for this category yet.";
  }
  const clauses: string[] = [];
  if (snapshot.truncated) {
    clauses.push(snapshot.stored < snapshot.total
      ? "This list is partial: " + snapshot.stored + " of " + snapshot.total + " observed entries are stored; the rest were removed to stay inside this device's storage limit."
      : "This list is partial: the collection stopped at this device's collection output limit, so only the entries it managed to read are stored. The count above is not the full number on this device.");
  }
  if (snapshot.note) {
    const remainder = snapshot.truncated ? noteWithoutTruncationClause(snapshot.note) : snapshot.note;
    if (remainder) clauses.push(remainder);
  }
  if (failure) clauses.push("The most recent collection reported a problem: " + failure);
  return clauses.length > 0 ? clauses.join(" ") : null;
}

// fieldOr returns a bounded field value, or an em dash when the platform did not
// report it. Absent is shown as absent rather than as a zero or a guess.
export function fieldOr(fields: Record<string, string> | undefined, key: string): string {
  const value = fields?.[key];
  return value && value.length > 0 ? value : "—";
}

// attributionLabel states honestly whether the owning process of an endpoint or
// service was actually observed.
export function attributionLabel(fields: Record<string, string> | undefined): string {
  if (fields?.attribution !== "observed") {
    return fieldOr(fields, "attribution_note") !== "—" ? "Unknown — " + fieldOr(fields, "attribution_note") : "Unknown — the owning process was not reported";
  }
  if (fields?.process) return fields.process;
  if (fields?.process_id) return "process id " + fields.process_id;
  return "Observed, process name not reported";
}

// endpointIntent classifies the bound address in plain terms. It deliberately
// stops at what the socket binding itself shows: a wildcard bind is a local
// fact, and Sparrow does not inspect the network or firewall to decide whether
// anything outside this machine can reach it.
export function endpointIntent(address: string): { label: string; note: string; wildcard: boolean } {
  const value = (address ?? "").trim();
  if (value === "0.0.0.0" || value === "::" || value === "*") {
    return {
      label: "All local interfaces",
      note: "Bound to every local interface. Whether another machine can reach it depends on your network and firewall, which Sparrow does not inspect.",
      wildcard: true
    };
  }
  if (value === "127.0.0.1" || value === "::1" || value.startsWith("127.")) {
    return { label: "Loopback only", note: "Reachable only from this device.", wildcard: false };
  }
  if (value === "" || value === "—") {
    return { label: "Address not reported", note: "The platform did not report the bound address.", wildcard: false };
  }
  return { label: "Specific local address", note: "Bound to one address reported by the platform.", wildcard: false };
}

// transportLabel normalises the socket/transport field without inventing a value
// when the collector reported something unexpected.
export function transportLabel(value: string): string {
  const lowered = (value ?? "").toLowerCase();
  if (lowered === "tcp" || lowered === "tcp6") return "TCP";
  if (lowered === "udp" || lowered === "udp6") return "UDP";
  return value ? value.toUpperCase() : "—";
}

// endpointStateLabel renders the socket state the platform reported, and
// nothing beyond it. A UDP endpoint has no TCP-style state at all — the
// platform reports a bound local address and nothing more — so an absent state
// is shown as "Bound" rather than as an invented "listening" value or an empty
// cell (CE-6 finding L-4).
export function endpointStateLabel(fields: Record<string, string> | undefined): string {
  const state = fields?.state;
  if (state && state.trim().length > 0) return state;
  const transport = (fields?.transport ?? "").toLowerCase();
  if (transport === "udp" || transport === "udp6") return "Bound";
  return "—";
}

// serviceStateLabel renders the recorded service state. Linux systemd units
// carry load/active/sub; Windows services carry status/start_type.
export function serviceStateLabel(fields: Record<string, string> | undefined): string {
  const active = fields?.active;
  const sub = fields?.sub;
  if (active && sub) return active + " (" + sub + ")";
  if (fields?.status) return fields.status + (fields.start_type ? " · " + fields.start_type : "");
  return "—";
}

// serviceDetail returns the most useful descriptive field for a service row.
export function serviceDetail(fields: Record<string, string> | undefined): string {
  return fieldOr(fields, "description") !== "—"
    ? fieldOr(fields, "description")
    : fieldOr(fields, "display_name");
}

export function kindLabel(kind: Kind): string {
  switch (kind) {
    case "software": return "Software";
    case "endpoint": return "Network endpoints";
    case "service": return "Services";
    default: return kind;
  }
}

// formatBytes reuses the monitoring wording so storage numbers read the same in
// every page.
export { formatBytes } from "./monitoring";
