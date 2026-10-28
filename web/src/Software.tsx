import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { formatDateTime } from "./assessment";
import {
  type InventoryStatus,
  type ItemRecord,
  coverageNote,
  fetchInventoryItems,
  fetchInventoryStatus,
  fieldOr,
  isPartial,
  refreshInventory,
  snapshotFor
} from "./inventory";
import { formatBytes } from "./monitoring";

// Software: the installed packages and applications this device reported.
//
// The results come first: coverage, refresh and filtering are one compact block
// above the list, and the explanation of what a package list can and cannot
// prove sits below it. This page is an inventory, not an assessment — it shows
// what was observed and never labels an entry vulnerable, because matching a
// version offline against an advisory list would misreport distributions that
// backport fixes under an unchanged version.

const pageSize = 25;
const searchDebounceMs = 300;

export default function Software() {
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

  // Debounce the search box so typing does not issue a request per keystroke.
  useEffect(() => {
    const timer = window.setTimeout(() => setSearch(searchInput.trim()), searchDebounceMs);
    return () => window.clearTimeout(timer);
  }, [searchInput]);

  const load = useCallback(async () => {
    const id = ++requestId.current;
    setLoading(true);
    setError("");
    try {
      const [nextStatus, page] = await Promise.all([
        fetchInventoryStatus(),
        fetchInventoryItems("software", { search, source, limit: pageSize })
      ]);
      if (id !== requestId.current) return;
      setStatus(nextStatus);
      setItems(page.items);
      setTotal(page.total);
      setNextAfter(page.next_after);
      setSources(page.sources);
    } catch (loadError) {
      if (id !== requestId.current) return;
      setError(loadError instanceof Error ? loadError.message : "Software inventory could not be loaded.");
    } finally {
      if (id === requestId.current) setLoading(false);
    }
  }, [search, source]);

  useEffect(() => { void load(); }, [load]);

  const loadMore = async () => {
    if (!nextAfter) return;
    setLoadingMore(true);
    setError("");
    try {
      const page = await fetchInventoryItems("software", { search, source, after: nextAfter, limit: pageSize });
      setItems((current) => [...current, ...page.items]);
      setNextAfter(page.next_after);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Older entries could not be loaded.");
    } finally {
      setLoadingMore(false);
    }
  };

  const refresh = async () => {
    setRefreshing(true);
    setActionError("");
    try {
      await refreshInventory();
      // The collectors run in the background; poll briefly until the snapshot
      // timestamp moves rather than reporting success before it is stored.
      for (let attempt = 0; attempt < 15; attempt++) {
        await new Promise((resolve) => window.setTimeout(resolve, 1000));
        const next = await fetchInventoryStatus();
        if (!next.refreshing) {
          setStatus(next);
          break;
        }
      }
      await load();
    } catch (refreshError) {
      setActionError(refreshError instanceof Error ? refreshError.message : "The inventory could not be refreshed.");
    } finally {
      setRefreshing(false);
    }
  };

  const snapshot = useMemo(() => snapshotFor(status, "software"), [status]);
  const failure = status?.failures?.software;
  const note = coverageNote(snapshot, failure);

  if (loading && !status && items.length === 0) {
    return <section className="panel" aria-busy="true"><h2>Loading software inventory…</h2><p>Reading the packages and applications this device reported.</p></section>;
  }
  if (error && !status && items.length === 0) {
    return <section className="panel error" role="alert"><h2>Software inventory unavailable</h2><p>{error}</p><button className="secondary" onClick={() => void load()}>Retry</button></section>;
  }

  const hasSnapshot = Boolean(snapshot);
  const partial = isPartial(snapshot);
  const filtered = Boolean(search || source);
  // A partial reading's count is a lower bound, so the headline says so instead
  // of presenting the count of what was read as the count of what is installed.
  const coverageHeadline = !hasSnapshot
    ? "No software inventory yet"
    : partial
      ? "At least " + total + (filtered ? " matching entries read" : " installed entries read")
      : total + (filtered ? " matching entries" : " installed entries");

  return <div className="assessment">
    <section className="disclosure" aria-label="What this inventory is">
      <p className="disclosure-line"><strong>An inventory, not a verdict.</strong>{" "}Sparrow lists what the operating system reports as installed; it never decides that a package is vulnerable.</p>
      <details>
        <summary>What this means</summary>
        <ul className="live-limits">
          <li>A package database lists installed packages; it is not a catalogue of every application, and files installed outside the package manager will not appear.</li>
          <li>No version is compared against a vulnerability list. A security advisory for a version you see here is not implied, and its absence is not evidence of safety.</li>
          <li>Entries are a snapshot: they reflect the last collection, not the present moment.</li>
          <li>Sparrow reads this unprivileged and treats it as read-only. Nothing here can install, upgrade or remove anything.</li>
        </ul>
      </details>
    </section>

    {/* Coverage, refresh and filtering, compact, directly above the results. */}
    <section className="panel compact">
      <div className="posture-head">
        <div className="posture-summary">
          <p className="label">Coverage</p>
          <h2>{coverageHeadline}</h2>
          <ul className="stat-list">
            <li>{snapshot ? <>Collected <strong>{formatDateTime(snapshot.observed_at)}</strong> from <strong>{snapshot.source}</strong></> : "Run a scan or refresh the inventory to record one."}</li>
            <li>{snapshot ? <><strong>{snapshot.stored}</strong> of {snapshot.total} observed entries stored</> : "Nothing stored"}</li>
            <li>{status ? <><strong>{formatBytes(status.stored_bytes)}</strong> of {formatBytes(status.max_bytes)} local inventory budget</> : "Storage usage not reported"}</li>
          </ul>
        </div>
        <div className="scan-actions">
          <button className="primary" onClick={() => void refresh()} disabled={refreshing}>{refreshing ? "Refreshing…" : "Refresh inventory"}</button>
          <button className="secondary" onClick={() => void load()} disabled={loading}>{loading ? "Reloading…" : "Reload"}</button>
        </div>
      </div>
      {partial && <p className="live-warn" role="note">This inventory is incomplete: this list is not the full set on this device.</p>}
      {note && <p className="live-meta">{note}</p>}
      {(error || status?.last_error) && <p className="live-error">{error || status?.last_error}</p>}
      {actionError && <p className="live-error" role="alert">{actionError}</p>}
      {status?.storage_pressure && <p className="live-error">Inventory storage is at its local limit. Older entries were dropped to stay inside the budget.</p>}
      <div className="filter-row">
        <label className="field">
          <span>Search by package or application name</span>
          <input
            type="search"
            value={searchInput}
            maxLength={128}
            placeholder="e.g. openssh"
            onChange={(event) => setSearchInput(event.target.value)}
            aria-label="Search by package or application name"
          />
        </label>
        <label className="field">
          <span>Source</span>
          <select value={source} onChange={(event) => setSource(event.target.value)} aria-label="Filter by inventory source">
            <option value="">All sources</option>
            {sources.map((item) => <option key={item} value={item}>{item}</option>)}
          </select>
        </label>
      </div>
      <p className="live-meta">Search matches the beginning of a name, case-insensitively. The result list is bounded and paged.</p>
    </section>

    {/* The results themselves. */}
    <section className="panel">
      <div className="posture-head">
        <div className="posture-summary">
          <p className="label">Installed software</p>
          <h2>{items.length === 0 ? "No entries to show" : items.length === 1 ? "1 entry shown" : items.length + " entries shown"}</h2>
        </div>
      </div>
      {items.length === 0
        ? <p className="live-meta">{hasSnapshot ? "No entries match this filter." : "No software inventory has been recorded yet. Run a scan from the Overview page, or use Refresh inventory above."}</p>
        : <>
          <div className="table-scroll">
            <table className="live-events">
              <thead><tr><th>Name</th><th>Version</th><th>Source</th><th>Observed</th></tr></thead>
              <tbody>
                {items.map((item) => <tr key={item.kind + ":" + item.name + ":" + (item.version ?? "")}>
                  <td>{item.name}</td>
                  <td>{item.version || "not reported"}</td>
                  <td>{item.source}{fieldOr(item.fields, "publisher") !== "—" ? " · " + fieldOr(item.fields, "publisher") : ""}</td>
                  <td>{formatDateTime(item.observed_at)}</td>
                </tr>)}
              </tbody>
            </table>
          </div>
          <p className="live-meta">Showing {items.length} of {total} matching entries.</p>
          {nextAfter ? <button className="secondary" onClick={() => void loadMore()} disabled={loadingMore}>{loadingMore ? "Loading…" : "Load more"}</button> : <p className="live-meta">{partial ? "End of the entries that were read; the reading itself was partial." : "End of the recorded inventory."}</p>}
        </>}
      {error && items.length > 0 && <p className="live-error">{error}</p>}
    </section>
  </div>;
}
