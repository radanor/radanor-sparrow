import { afterEach, describe, expect, it, vi } from "vitest";
import {
  type InventoryStatus,
  type SnapshotRecord,
  attributionLabel,
  coverageNote,
  endpointIntent,
  endpointStateLabel,
  fetchInventoryItems,
  fetchInventoryStatus,
  fieldOr,
  isPartial,
  inventoryLoadMessage,
  kindLabel,
  refreshInventory,
  serviceDetail,
  serviceStateLabel,
  snapshotFor,
  transportLabel
} from "./inventory";

describe("inventory query construction", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("uses the gateway's exact collection routes with only allowlisted query parameters", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ schema: "shield.inventory_items/v1", kind: "software", items: [], total: 0, sources: [] }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    await fetchInventoryItems("software", { search: "openssh", source: "dpkg", after: "bash", limit: 25 });
    const url = String(fetchMock.mock.calls[0]?.[0]);
    expect(url).toBe("/api/v1/inventory/software?search=openssh&source=dpkg&after=bash&limit=25");
    // No parameter other than the reviewed four is ever sent.
    await fetchInventoryItems("endpoint", { limit: 25 });
    expect(String(fetchMock.mock.calls[1]?.[0])).toBe("/api/v1/inventory/endpoints?limit=25");
    await fetchInventoryItems("service", { limit: 25 });
    expect(String(fetchMock.mock.calls[2]?.[0])).toBe("/api/v1/inventory/services?limit=25");
  });

  it("reads status from its own endpoint and reports a truthful failure", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ schema: "shield.inventory_status/v1" }), { status: 200 })));
    await fetchInventoryStatus();
    vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
    await expect(fetchInventoryStatus()).rejects.toThrow(/inventory service is unavailable/);
  });

  it.each([
    [400, "refused as invalid"],
    [401, "session is no longer valid"],
    [503, "inventory service is unavailable"],
    [500, "unexpected response"]
  ])("classifies HTTP %d without exposing the response body", async (status, message) => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("sensitive backend detail", { status })));
    await expect(fetchInventoryItems("endpoint")).rejects.toThrow(message);
    await expect(fetchInventoryItems("endpoint")).rejects.not.toThrow(/sensitive backend detail/);
  });

  it("classifies a transport failure separately", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => { throw new TypeError("socket path and secret detail"); }));
    await expect(fetchInventoryItems("service")).rejects.toThrow(/could not be reached/);
    await expect(fetchInventoryItems("service")).rejects.not.toThrow(/secret detail/);
  });

  it("does not expose malformed response content", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("private malformed detail", { status: 200 })));
    await expect(fetchInventoryItems("endpoint")).rejects.toThrow(/invalid response/);
    await expect(fetchInventoryItems("endpoint")).rejects.not.toThrow(/private malformed detail/);
  });

  it("distinguishes status, endpoint and service loading failures", () => {
    const failure = new Error("The background inventory service is unavailable.");
    expect(inventoryLoadMessage("status", failure)).toMatch(/^Inventory status could not be loaded/);
    expect(inventoryLoadMessage("endpoint", failure)).toMatch(/^Network endpoints could not be loaded/);
    expect(inventoryLoadMessage("service", failure)).toMatch(/^Services could not be loaded/);
  });

  it("refreshes through a CSRF-protected POST with no parameters", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ accepted: true }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    await refreshInventory();
    const [, init] = fetchMock.mock.calls[0] ?? [];
    expect((init as RequestInit).method).toBe("POST");
    expect((init as RequestInit).body).toBe("{}");
  });
});

describe("inventory honesty helpers", () => {
  const status: InventoryStatus = {
    schema: "shield.inventory_status/v1",
    has_snapshot: true,
    snapshots: [
      { kind: "software", observed_at: "2026-01-01T00:00:00Z", total: 1200, stored: 1200, truncated: false, source: "dpkg" },
      { kind: "endpoint", observed_at: "2026-01-01T00:00:00Z", total: 900, stored: 400, truncated: true, source: "ss" }
    ],
    stored_bytes: 10,
    max_bytes: 100,
    max_items_per_kind: 20000,
    storage_pressure: false,
    generated_at: "2026-01-01T00:00:00Z",
    limitations: [],
    software_count: 1200,
    endpoint_count: 400,
    service_count: 0,
    refreshing: false
  };

  it("finds the coverage record for one kind only", () => {
    expect(snapshotFor(status, "software")?.source).toBe("dpkg");
    expect(snapshotFor(status, "endpoint")?.stored).toBe(400);
    expect(snapshotFor(status, "service")).toBeUndefined();
    expect(snapshotFor(null, "software")).toBeUndefined();
  });

  it("discloses truncation and failed collection instead of showing an empty list as healthy", () => {
    expect(coverageNote(snapshotFor(status, "endpoint"))).toContain("400 of 900");
    expect(coverageNote(undefined, "dpkg is not available")).toContain("dpkg is not available");
    expect(coverageNote(undefined)).toContain("No inventory has been recorded");
    expect(coverageNote(snapshotFor(status, "software"))).toBeNull();
  });

  it("never calls a partial reading complete when its counts agree", () => {
    // H-1: a listing cut short at the collection output limit can observe exactly
    // as many records as it stores. "2 of 2 stored" must not read as complete.
    const clipped: SnapshotRecord = {
      kind: "software", observed_at: "2026-01-01T00:00:00Z", total: 2, stored: 2,
      truncated: true, source: "dpkg", note: "the listing was cut off at the collection output limit, so it is partial"
    };
    const note = coverageNote(clipped);
    expect(note).not.toBeNull();
    expect(note).toContain("partial");
    expect(note).toContain("collection output limit");
    expect(note).not.toContain("2 of 2");
    expect(isPartial(clipped)).toBe(true);
    expect(isPartial(snapshotFor(status, "software"))).toBe(false);
    expect(isPartial(undefined)).toBe(false);
  });

  it("keeps a collector's own caveat alongside the truncation, without repeating it", () => {
    const clipped: SnapshotRecord = {
      kind: "endpoint", observed_at: "2026-01-01T00:00:00Z", total: 5, stored: 5, truncated: true, source: "ss",
      note: "the owning process could not be read for 3 endpoint(s); they are listed as unattributed; the listing was cut off at the collection output limit, so it is partial"
    };
    const note = coverageNote(clipped) ?? "";
    expect(note).toContain("unattributed");
    // The disclosure appears once, not once from the cause and again from the note.
    expect(note.split("collection output limit").length - 1).toBe(1);
  });

  it("combines a recorded problem with the coverage instead of hiding the coverage", () => {
    const snapshot = snapshotFor(status, "software");
    expect(coverageNote(snapshot, "the last refresh could not read the package database"))
      .toContain("the last refresh could not read the package database");
    // With a snapshot present the message must not claim the category is uncollected.
    expect(coverageNote(snapshot, "some problem")).not.toContain("could not be collected on this device");
  });

  it("shows an absent field as absent rather than as a value", () => {
    expect(fieldOr({ port: "22" }, "port")).toBe("22");
    expect(fieldOr({ port: "" }, "port")).toBe("—");
    expect(fieldOr(undefined, "port")).toBe("—");
    expect(fieldOr({ process: "sshd (pid 1)" }, "process")).toBe("sshd (pid 1)");
  });

  it("never presents an unattributed endpoint as attributed", () => {
    expect(attributionLabel({ attribution: "observed", process: "sshd (pid 912)" })).toBe("sshd (pid 912)");
    expect(attributionLabel({ attribution: "observed", process_id: "88" })).toBe("process id 88");
    expect(attributionLabel({ attribution: "unknown", attribution_note: "the owning process is not visible to this user" }))
      .toBe("Unknown — the owning process is not visible to this user");
    expect(attributionLabel(undefined)).toBe("Unknown — the owning process was not reported");
  });

  it("describes a wildcard bind without claiming external reachability", () => {
    const wildcard = endpointIntent("0.0.0.0");
    expect(wildcard.wildcard).toBe(true);
    expect(wildcard.note).toContain("Sparrow does not inspect");
    expect(wildcard.note.toLowerCase()).not.toContain("publicly exposed");
    expect(endpointIntent("::").wildcard).toBe(true);
    expect(endpointIntent("127.0.0.1").label).toBe("Loopback only");
    expect(endpointIntent("127.0.0.1").note).toContain("this device");
    expect(endpointIntent("192.168.1.5").label).toBe("Specific local address");
    expect(endpointIntent("").label).toBe("Address not reported");
  });

  it("labels transports and service state without inventing a reading", () => {
    expect(transportLabel("tcp")).toBe("TCP");
    expect(transportLabel("udp6")).toBe("UDP");
    expect(transportLabel("sctp")).toBe("SCTP");
    expect(transportLabel("")).toBe("—");
    expect(serviceStateLabel({ active: "active", sub: "running" })).toBe("active (running)");
    expect(serviceStateLabel({ status: "running", start_type: "automatic" })).toBe("running · automatic");
    expect(serviceStateLabel(undefined)).toBe("—");
    expect(serviceDetail({ description: "OpenSSH server daemon", display_name: "x" })).toBe("OpenSSH server daemon");
    expect(serviceDetail({ display_name: "Print Spooler" })).toBe("Print Spooler");
  });

  it("never presents a UDP binding as a listening socket", () => {
    // L-4: Windows reports a UDP endpoint as a bound local address with no
    // state. Rendering it as "Listen" would invent a TCP-shaped fact.
    expect(endpointStateLabel({ transport: "udp", state: "" })).toBe("Bound");
    expect(endpointStateLabel({ transport: "udp6" })).toBe("Bound");
    expect(endpointStateLabel({ transport: "udp", state: "listen" }).toLowerCase()).not.toBe("listening");
    // A TCP socket keeps the state the platform reported; anything else is absent.
    expect(endpointStateLabel({ transport: "tcp", state: "listen" })).toBe("listen");
    expect(endpointStateLabel({ transport: "tcp", state: "" })).toBe("—");
    expect(endpointStateLabel(undefined)).toBe("—");
  });

  it("names each inventory kind plainly", () => {
    expect(kindLabel("software")).toBe("Software");
    expect(kindLabel("endpoint")).toBe("Network endpoints");
    expect(kindLabel("service")).toBe("Services");
  });
});
