import { useCallback, useEffect, useState } from "react";
import {
  type Finding,
  type FindingPage,
  type Lifecycle,
  checkOutcomeTitle,
  collectionLabel,
  evidenceSummary,
  fetchFinding,
  fetchFindings,
  formatDateTime,
  lifecycleExplanation,
  lifecycleLabel,
  resultLabel,
  severityLabel,
  severityTone,
  startScan
} from "./assessment";
import Evidence from "./Evidence";

// Findings: confirmed weaknesses with their evidence, lifecycle and verification
// history. A failed collection can never resolve a finding, so a resolved finding
// always shows the passing evidence that resolved it.
//
// The page answers three questions for each finding: what was observed, what to do
// about it, and how to confirm the fix. Nothing here estimates a risk score or
// promises that a device is clean, and the counts come from the API's own totals.

const filters: Array<{ key: string; label: string }> = [
  { key: "", label: "All" },
  { key: "new", label: "New" },
  { key: "continuing", label: "Continuing" },
  { key: "reopened", label: "Reopened" },
  { key: "resolved", label: "Resolved" },
  // Superseded findings are retained history: the rule that reported them was
  // replaced and the weakness was never verified fixed, so they are kept
  // separate from resolved findings.
  { key: "superseded", label: "Superseded" }
];

export default function Findings() {
  const [findings, setFindings] = useState<Finding[]>([]);
  const [total, setTotal] = useState(0);
  const [filter, setFilter] = useState("");
  const [detail, setDetail] = useState<FindingPage | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState("");

  const load = useCallback(async (lifecycle: string) => {
    setLoading(true);
    setError("");
    try {
      const page = await fetchFindings({ lifecycle: lifecycle ? (lifecycle as Lifecycle) : undefined, limit: 50 });
      setFindings(page.findings);
      setTotal(page.total_open);
      setDetail(null);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Findings could not be loaded.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(filter); }, [filter, load]);

  const openDetail = async (fingerprint: string) => {
    try {
      setDetail(await fetchFinding(fingerprint));
    } catch (detailError) {
      setError(detailError instanceof Error ? detailError.message : "The finding detail could not be loaded.");
    }
  };

  // Verifying a finding means re-reading the same facts with the rule that
  // reported it. Sparrow does not touch the system, so this only starts a scan.
  const verify = async () => {
    setBusy(true);
    setActionError("");
    try {
      await startScan();
      await load(filter);
    } catch (scanError) {
      setActionError(scanError instanceof Error ? scanError.message : "A verification scan could not be started.");
    } finally {
      setBusy(false);
    }
  };

  return <div className="assessment">
    {/* What a finding is, in one line, with the full rule one disclosure away. */}
    <section className="disclosure" aria-label="What a finding is">
      <p className="disclosure-line"><strong>Findings are confirmed weaknesses only.</strong>{" "}Unknown, unsupported and stale results never create or resolve a finding.</p>
      <details>
        <summary>What this means</summary>
        <p>A finding exists only when a check failed with reliable evidence. A failed, refused or incomplete collection never closes one, so a weakness stays open until a later scan collects evidence that it is gone.</p>
        <p className="live-meta">Superseded findings are kept for history: the rule that reported them was replaced, and the weakness was never verified fixed.</p>
      </details>
    </section>

    <section className="panel compact">
      <div className="posture-head">
        <div className="posture-summary">
          <p className="label">Open findings</p>
          <h2>{total === 1 ? "1 finding is open" : total + " findings are open"}</h2>
          <p className="live-meta">{findings.length} shown for the “{filters.find((item) => item.key === filter)?.label ?? "All"}” filter.</p>
        </div>
        <div className="scan-actions">
          <button className="primary" onClick={() => void verify()} disabled={busy}>{busy ? "Starting…" : "Re-verify with a scan"}</button>
          <button className="secondary" onClick={() => void load(filter)} disabled={loading}>{loading ? "Refreshing…" : "Refresh"}</button>
        </div>
      </div>
      <div className="chip-row">
        {filters.map((item) => <button key={item.key || "all"} className={"chip" + (item.key === filter ? " active" : "")} aria-pressed={item.key === filter} onClick={() => setFilter(item.key)}>{item.label}</button>)}
      </div>
      <p className="live-meta">A finding stays open until a later scan collects evidence that it is gone.</p>
    </section>

    {actionError && <section className="panel error" role="alert"><p>{actionError}</p></section>}
    {error && <section className="panel error" role="alert"><p>{error}</p></section>}

    {findings.length === 0
      ? <section className="panel compact"><p className="live-meta">{filter === "resolved" ? "No findings have been resolved yet." : "No findings match this filter. Run a scan from the Overview page if no assessment has been completed."}</p></section>
      : <section className="panel">
        <ul className="finding-list">
          {findings.map((finding) => <li key={finding.fingerprint}>
            <div className="live-source-head">
              <strong>{checkOutcomeTitle(finding.check_id, "fail", finding.title)}</strong>
              <span className={"status " + severityTone(finding.severity)}>{severityLabel(finding.severity)}</span>
              <span className="status neutral">{lifecycleLabel(finding.lifecycle)}</span>
            </div>
            <p className="live-meta">{finding.summary || finding.action}</p>
            <p className="live-meta">{lifecycleExplanation(finding.lifecycle)}</p>
            <p className="live-meta">First seen {formatDateTime(finding.first_seen_at)} · last observed {formatDateTime(finding.last_seen_at)}{finding.resolved_at ? " · resolved " + formatDateTime(finding.resolved_at) : ""}</p>
            <button className="secondary" onClick={() => void openDetail(finding.fingerprint)}>Show evidence and history</button>
          </li>)}
        </ul>
      </section>}

    {detail && <section className="panel detail-panel">
      <div className="posture-head">
        <div className="posture-summary">
          <p className="label">{detail.finding.check_id} · rule v{detail.finding.check_version}</p>
          <h2>{checkOutcomeTitle(detail.finding.check_id, "fail", detail.finding.title)}</h2>
        </div>
        <button className="secondary" onClick={() => setDetail(null)}>Close</button>
      </div>
      <p className="live-meta">Fingerprint {detail.finding.fingerprint.slice(0, 16)}… · platform {detail.finding.platform} · lifecycle {lifecycleLabel(detail.finding.lifecycle)}</p>
      <p className="live-meta">{lifecycleExplanation(detail.finding.lifecycle)}</p>
      <p className="live-meta"><strong>Last observed:</strong> {formatDateTime(detail.finding.last_seen_at)} · first seen {formatDateTime(detail.finding.first_seen_at)}</p>
      <p className="live-meta"><strong>Recommended action:</strong> {detail.finding.action}</p>
      <p className="live-meta"><strong>How to verify the fix:</strong> {detail.finding.verify_method}</p>
      <p className="live-meta">Evidence source {detail.finding.evidence?.source || "—"} · collection {collectionLabel(detail.finding.evidence?.status ?? "failed")} · {evidenceSummary(detail.finding.evidence, 8)}</p>
      {detail.finding.limitations && detail.finding.limitations.length > 0 && <ul className="live-limits">{detail.finding.limitations.map((limit) => <li key={limit}>{limit}</li>)}</ul>}
      <Evidence observation={detail.finding.evidence} />
      <button className="secondary" onClick={() => void verify()} disabled={busy}>{busy ? "Starting…" : "Verify with a new scan"}</button>
      <p className="label">Verification history</p>
      <p className="live-meta">Every transition is recorded against the scan that caused it, so you can see when a weakness was first seen, when it changed, and what evidence resolved it.</p>
      {detail.history.length > 0 ? <div className="table-scroll"><table className="live-events">
        <thead><tr><th>Recorded</th><th>Run</th><th>Result</th><th>Lifecycle</th><th>Detail</th></tr></thead>
        <tbody>
          {detail.history.map((entry, index) => <tr key={entry.run_id + ":" + index}>
            <td>{formatDateTime(entry.recorded_at)}</td>
            <td>#{entry.run_id}</td>
            <td>{resultLabel(entry.result)}</td>
            <td>{lifecycleLabel(entry.lifecycle)}</td>
            <td>{entry.detail || "—"}</td>
          </tr>)}
        </tbody>
      </table></div> : <p className="live-meta">No transitions have been recorded for this finding yet.</p>}
    </section>}
  </div>;
}
