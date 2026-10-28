import { useCallback, useEffect, useRef, useState } from "react";
import { formatDateTime } from "./assessment";
import {
  type InventoryStatus,
  type ItemRecord,
  attributionLabel,
  coverageNote,
  endpointIntent,
  endpointStateLabel,
  fetchInventoryItems,
  fetchInventoryStatus,
  fieldOr,
  isPartial,
  inventoryLoadMessage,
  refreshInventory,
  serviceDetail,
  serviceStateLabel,
  snapshotFor,
  transportLabel
} from "./inventory";
import { formatBytes } from "./monitoring";

// Network & Services: the listening endpoints and services this device reported.
//
// Counts and the category switch come first, then the two tables, and the
// methodology sits below them. Everything here was read locally from the
// operating system: Sparrow does not scan the network, does not contact remote
// hosts, and does not enumerate other devices. A listening socket is presented as
// exactly that — a local observation — and an entry whose owning process could
// not be read is reported as unattributed rather than guessed.

const pageSize = 25;

type Tab = "endpoint" | "service";

export default function NetworkServices() {
  const [tab, setTab] = useState<Tab>("endpoint");
  const [status, setStatus] = useState<InventoryStatus | null>(null);
  const [items, setItems] = useState<ItemRecord[]>([]);
  const [total, setTotal] = useState(0);
  const [nextAfter, setNextAfter] = useState<string | undefined>(undefined);
  const [sources, setSources] = useState<string[]>([]);
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [source, setSource] = useState("");
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState("");
  const [actionError, setActionError] = useState("");
  const requestId = useRef(0);

  useEffect(() => {
    const timer = window.setTimeout(() => setSearch(searchInput.trim()), 300);
    return () => window.clearTimeout(timer);
  }, [searchInput]);

  const load = useCallback(async (kind: Tab) => {
    const id = ++requestId.current;
    setLoading(true);
    setLoadingMore(false);
    setError("");
    try {
      const [nextStatus, page] = await Promise.all([
        fetchInventoryStatus().catch((failure: unknown) => { throw new Error(inventoryLoadMessage("status", failure)); }),
        fetchInventoryItems(kind, { search, source, limit: pageSize }).catch((failure: unknown) => { throw new Error(inventoryLoadMessage(kind, failure)); })
      ]);
      if (id !== requestId.current) return;
      setStatus(nextStatus);
      setItems(page.items);
      setTotal(page.total);
      setNextAfter(page.next_after);
      setSources(page.sources);
    } catch (loadError) {
      if (id !== requestId.current) return;
      setError(loadError instanceof Error ? loadError.message : inventoryLoadMessage(kind, loadError));
    } finally {
      if (id === requestId.current) setLoading(false);
    }
  }, [search, source]);

  useEffect(() => { void load(tab); }, [load, tab]);

  const loadMore = async () => {
    if (!nextAfter) return;
    const id = requestId.current;
    const kind = tab;
    setLoadingMore(true);
    setError("");
    try {
      const page = await fetchInventoryItems(kind, { search, source, after: nextAfter, limit: pageSize });
      if (id !== requestId.current) return;
      setItems((current) => [...current, ...page.items]);
      setNextAfter(page.next_after);
    } catch (loadError) {
      if (id !== requestId.current) return;
      setError(inventoryLoadMessage(kind, loadError));
    } finally {
      if (id === requestId.current) setLoadingMore(false);
    }
  };

  const refresh = async () => {
    setRefreshing(true);
    setActionError("");
    try {
      await refreshInventory();
      for (let attempt = 0; attempt < 15; attempt++) {
        await new Promise((resolve) => window.setTimeout(resolve, 1000));
        const next = await fetchInventoryStatus().catch((failure: unknown) => { throw new Error(inventoryLoadMessage("status", failure)); });
        if (!next.refreshing) { setStatus(next); break; }
      }
      await load(tab);
    } catch (refreshError) {
      setActionError(refreshError instanceof Error ? refreshError.message : "The inventory could not be refreshed.");
    } finally {
      setRefreshing(false);
    }
  };

  const snapshot = snapshotFor(status, tab);
  const failure = status?.failures?.[tab];
  const note = coverageNote(snapshot, failure);
  const partial = isPartial(snapshot);
  const filtered = Boolean(search || source);
  const category = tab === "endpoint" ? "listening endpoints" : "services";
  // A partial reading's count is a lower bound: the headline says "at least"
  // rather than presenting what was read as the whole set.
  const coverageHeadline = !snapshot
    ? "No network inventory yet"
    : partial
      ? "At least " + total + (filtered ? " matching entries read" : " " + category + " read")
      : total + (filtered ? " matching entries" : " observed entries");

  if (loading && !status && items.length === 0) {
    return <section className="panel" aria-busy="true"><h2>Loading network inventory…</h2><p>Reading the endpoints and services this device reported.</p></section>;
  }
  if (error && !status && items.length === 0) {
    return <section className="panel error" role="alert"><h2>Network inventory unavailable</h2><p>{error}</p><button className="secondary" onClick={() => void load(tab)}>Retry</button></section>;
  }

  return <div className="assessment">
    <section className="disclosure" aria-label="What this inventory is">
      <p className="disclosure-line"><strong>Local observations only.</strong>{" "}Sparrow lists sockets and services this device reports about itself, and makes no claim that a port is reachable from anywhere else.</p>
      <details>
        <summary>What this means</summary>
        <p>Sparrow performs no network scanning, sends no traffic to other hosts, and does not enumerate other devices. It performs no hostname resolution either.</p>
        <p className="live-meta">Nothing on this page opens a connection or changes a firewall rule.</p>
      </details>
    </section>

    {/* Counts, category and refresh first. */}
    <section className="panel compact">
      <div className="posture-head">
        <div className="posture-summary">
          <p className="label">{tab === "endpoint" ? "Listening endpoints" : "Services"}</p>
          <h2>{coverageHeadline}</h2>
          <ul className="stat-list">
            <li>{snapshot ? <>Collected <strong>{formatDateTime(snapshot.observed_at)}</strong> from <strong>{snapshot.source}</strong></> : "Run a scan or refresh the inventory to record one."}</li>
            <li>{snapshot ? <><strong>{snapshot.stored}</strong> of {snapshot.total} observed entries stored</> : "Nothing stored"}</li>
            <li>{status ? <><strong>{formatBytes(status.stored_bytes)}</strong> of {formatBytes(status.max_bytes)} local inventory budget</> : "Storage usage not reported"}</li>
          </ul>
        </div>
        <div className="scan-actions">
          <button className="primary" onClick={() => void refresh()} disabled={refreshing}>{refreshing ? "Refreshing…" : "Refresh inventory"}</button>
          <button className="secondary" onClick={() => void load(tab)} disabled={loading}>{loading ? "Reloading…" : "Reload"}</button>
        </div>
      </div>
      <div className="chip-row" role="tablist" aria-label="Inventory category">
        <button role="tab" aria-selected={tab === "endpoint"} className={"chip" + (tab === "endpoint" ? " active" : "")} onClick={() => setTab("endpoint")}>Listening endpoints</button>
        <button role="tab" aria-selected={tab === "service"} className={"chip" + (tab === "service" ? " active" : "")} onClick={() => setTab("service")}>Services</button>
      </div>
      <div className="filter-row">
        <label className="field">
          <span>{tab === "endpoint" ? "Search endpoints" : "Search services"}</span>
          <input type="search" value={searchInput} maxLength={128} placeholder={tab === "endpoint" ? "e.g. 0.0.0.0:22" : "e.g. ssh"} onChange={(event) => setSearchInput(event.target.value)} aria-label={tab === "endpoint" ? "Search endpoints" : "Search services"} />
        </label>
        <label className="field">
          <span>Source</span>
          <select value={source} onChange={(event) => setSource(event.target.value)} aria-label="Filter by source">
            <option value="">All sources</option>
            {sources.map((item) => <option key={item} value={item}>{item}</option>)}
          </select>
        </label>
      </div>
      {partial && <p className="live-warn" role="note">This inventory is incomplete: this list is not the full set on this device.</p>}
      {(note || snapshot?.source === "ss") && <details className="evidence">
        <summary>Collector coverage and process attribution</summary>
        {note && <p className="live-meta">{note}</p>}
        {snapshot?.source === "ss" && <p className="live-meta">Listening sockets were read with the platform's own socket statistics tool. The kernel only exposes an owning process for sockets this user may inspect, so entries without a name are shown as unattributed rather than guessed.</p>}
      </details>}
      {(error || status?.last_error) && <p className="live-error">{error || status?.last_error}</p>}
      {actionError && <p className="live-error" role="alert">{actionError}</p>}
    </section>

    {/* The two tables. */}
    <section className="panel">
      <div className="posture-head">
        <div className="posture-summary">
          <p className="label">{tab === "endpoint" ? "Endpoints" : "Services"}</p>
          <h2>{items.length === 0 ? "No entries to show" : items.length === 1 ? "1 entry shown" : items.length + " entries shown"}</h2>
        </div>
      </div>
      {items.length === 0
        ? <p className="live-meta">{snapshot ? "No entries match this filter." : "No inventory has been recorded yet. Run a scan from the Overview page, or use Refresh inventory above."}</p>
        : tab === "endpoint" ? <EndpointTable items={items} /> : <ServiceTable items={items} />}
      {items.length > 0 && <p className="live-meta">Showing {items.length} of {total} matching entries.</p>}
      {nextAfter && <button className="secondary" onClick={() => void loadMore()} disabled={loadingMore}>{loadingMore ? "Loading…" : "Load more"}</button>}
      {error && items.length > 0 && <p className="live-error">{error}</p>}
    </section>

    <section className="disclosure" aria-label="What this list does and does not tell you">
      <p className="disclosure-line"><strong>A listening socket is a local fact.</strong>{" "}Whether another machine can reach it depends on routing and your firewall, which Sparrow does not inspect.</p>
      <details>
        <summary>What this list does and does not tell you</summary>
        <ul className="live-limits">
          <li>A listening socket is a local fact. Whether another machine can reach it depends on routing and your firewall, which Sparrow does not inspect or modify.</li>
          <li>An entry whose owning process could not be read is reported as unattributed. Sparrow never guesses which program owns a socket.</li>
          <li>Sparrow lists what the operating system reports; a service that is masked, not loaded, or not reported by the platform will not appear.</li>
          <li>Nothing here opens a connection, resolves a hostname, or enumerates other devices on your network.</li>
        </ul>
      </details>
    </section>
  </div>;
}

function EndpointTable({ items }: { items: ItemRecord[] }) {
  return <div className="table-scroll">
    <table className="live-events">
      <thead><tr><th>Transport</th><th>Local address</th><th>Port</th><th>State</th><th>Owning process</th><th>Binding</th><th>Observed</th></tr></thead>
      <tbody>
        {items.map((item) => {
          const intent = endpointIntent(fieldOr(item.fields, "address"));
          return <tr key={item.kind + ":" + item.name}>
            <td>{transportLabel(fieldOr(item.fields, "transport"))}</td>
            <td>{fieldOr(item.fields, "address")}</td>
            <td>{fieldOr(item.fields, "port")}</td>
            <td>{endpointStateLabel(item.fields)}</td>
            <td>{attributionLabel(item.fields)}</td>
            <td><span className="inline-note" title={intent.note}>{intent.label}</span></td>
            <td>{formatDateTime(item.observed_at)}</td>
          </tr>;
        })}
      </tbody>
    </table>
  </div>;
}

function ServiceTable({ items }: { items: ItemRecord[] }) {
  return <div className="table-scroll">
    <table className="live-events">
      <thead><tr><th>Service</th><th>State</th><th>Detail</th><th>Attribution</th><th>Observed</th></tr></thead>
      <tbody>
        {items.map((item) => <tr key={item.kind + ":" + item.name}>
          <td>{item.name}</td>
          <td>{serviceStateLabel(item.fields)}</td>
          <td>{serviceDetail(item.fields)}</td>
          <td>{fieldOr(item.fields, "attribution")}</td>
          <td>{formatDateTime(item.observed_at)}</td>
        </tr>)}
      </tbody>
    </table>
  </div>;
}
