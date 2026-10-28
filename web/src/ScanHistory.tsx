import { useCallback, useEffect, useState } from "react";
import {
  type RunPage,
  type RunRecord,
  fetchRun,
  fetchRuns,
  formatDateTime,
  formatDuration,
  partialScanNote,
  resultExplanation,
  resultLabel,
  resultTone,
  runStatusExplanation
} from "./assessment";

// Scan History: every recorded assessment run with its outcome, how many
// collectors could not be read, and the per-check results of one run on demand.
// The records come first; the explanation of what each outcome means is one
// disclosure away, and an interrupted or cancelled run is shown as such rather
// than quietly omitted.

export default function ScanHistory({ onNavigate }: { onNavigate?: (view: string) => void }) {
  const [runs, setRuns] = useState<RunRecord[]>([]);
  const [nextBefore, setNextBefore] = useState<number | undefined>(undefined);
  const [detail, setDetail] = useState<RunPage | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const page = await fetchRuns({ limit: 20 });
      setRuns(page.runs);
      setNextBefore(page.next_before);
      setDetail(null);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Scan history could not be loaded.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const loadMore = async () => {
    if (!nextBefore) return;
    try {
      const page = await fetchRuns({ before: nextBefore, limit: 20 });
      setRuns((current) => [...current, ...page.runs]);
      setNextBefore(page.next_before);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Older runs could not be loaded.");
    }
  };

  const openDetail = async (runId: number) => {
    try {
      setDetail(await fetchRun(runId));
    } catch (detailError) {
      setError(detailError instanceof Error ? detailError.message : "The run detail could not be loaded.");
    }
  };

  if (loading && runs.length === 0) return <section className="panel" aria-busy="true"><h2>Loading scan history…</h2><p>Reading recorded assessment runs.</p></section>;
  if (error && runs.length === 0) return <section className="panel error" role="alert"><h2>Scan history unavailable</h2><p>{error}</p><button className="secondary" onClick={() => void load()}>Retry</button></section>;

  const partial = runs.filter((run) => partialScanNote(run) !== null).length;

  return <div className="assessment">
    <section className="panel">
      <div className="posture-head">
        <div className="posture-summary">
          <p className="label">Recorded scans</p>
          <h2>{runs.length === 1 ? "1 recorded run" : runs.length + " recorded runs"}</h2>
          <p className="live-meta">
            {runs.length === 0 ? "No assessment has been recorded yet." : "Newest first. Every outcome below is the outcome the run actually reported."}
          </p>
        </div>
        <button className="secondary" onClick={() => void load()} disabled={loading}>{loading ? "Refreshing…" : "Refresh"}</button>
      </div>
      {partial > 0 && <p className="live-warn" role="note">{partial === 1 ? "1 run has partial coverage" : partial + " runs have partial coverage"}: a partial scan is not a clean baseline. Open a run's details to see what could not be read.</p>}
      {runs.length === 0 ? <p className="live-meta">Start one from the Overview page{onNavigate ? <>, or use <button className="link-button" onClick={() => onNavigate("overview")}>Scan now on Overview</button></> : null}.</p>
        : <>
          <div className="table-scroll"><table className="live-events">
            <thead><tr><th>Started</th><th>Trigger</th><th>Status</th><th>Duration</th><th>Checks (pass/fail/unknown)</th><th>Collectors incomplete</th><th>New findings</th><th /></tr></thead>
            <tbody>
              {runs.map((run) => <tr key={run.id}>
                <td>{formatDateTime(run.started_at)}</td>
                <td>{run.trigger}</td>
                <td><span className={"status " + statusTone(run.status)}>{run.status}</span></td>
                <td>{formatDuration(run.duration_ms)}</td>
                <td>{run.passed} / {run.failed} / {run.unknown}</td>
                <td>{run.collectors_failed} of {run.collectors_run}</td>
                <td>{run.new_findings}</td>
                <td>
                  {partialScanNote(run) && <span className="status warn">Partial</span>}{" "}
                  <button className="secondary" onClick={() => void openDetail(run.id)}>Details</button>
                </td>
              </tr>)}
            </tbody>
          </table></div>
          <p className="live-meta">Showing {runs.length} recorded runs.</p>
          {nextBefore ? <button className="secondary" onClick={() => void loadMore()}>Load older runs</button> : <p className="live-meta">End of recorded history.</p>}
          {error && <p className="live-error">{error}</p>}
        </>}
    </section>

    {detail && <section className="panel detail-panel">
      <div className="posture-head">
        <div className="posture-summary">
          <p className="label">Run {detail.run.id}</p>
          <h2>{detail.run.trigger} assessment · {detail.run.status}</h2>
          <p className="live-meta">Started {formatDateTime(detail.run.started_at)} · finished {formatDateTime(detail.run.finished_at)} · duration {formatDuration(detail.run.duration_ms)}</p>
        </div>
        <button className="secondary" onClick={() => setDetail(null)}>Close</button>
      </div>
      <p className="live-meta">{runStatusExplanation(detail.run.status)}</p>
      <ul className="stat-list">
        <li><strong>{detail.run.passed}</strong> passed · <strong>{detail.run.failed}</strong> failed · <strong>{detail.run.unknown}</strong> unknown</li>
        <li><strong>{detail.run.stale}</strong> stale · <strong>{detail.run.not_supported}</strong> not supported</li>
        <li><strong>{detail.run.collectors_failed}</strong> of {detail.run.collectors_run} collectors returned incomplete or failed evidence</li>
      </ul>
      {partialScanNote(detail.run) && <p className="live-warn" role="note">{partialScanNote(detail.run)}</p>}
      {detail.checks.length === 0 ? <p className="live-meta">No check results were recorded for this run.</p> : <>
        <p className="live-meta">A result here is the outcome of one check in this scan. The same explanation applies as on the Security Checks page.</p>
        <div className="table-scroll"><table className="live-events">
          <thead><tr><th>Check</th><th>Result</th><th>Detail</th><th>Meaning</th></tr></thead>
          <tbody>
            {detail.checks.map((check) => <tr key={check.check_id}>
              <td>{check.title}</td>
              <td><span className={"status " + resultTone(check.result)}>{resultLabel(check.result)}</span></td>
              <td>{check.detail || "—"}</td>
              <td>{resultExplanation(check.result)}</td>
            </tr>)}
          </tbody>
        </table></div>
      </>}
    </section>}

    <section className="disclosure" aria-label="How a run outcome is recorded">
      <p className="disclosure-line"><strong>History is never silently rewritten.</strong>{" "}A cancelled or interrupted scan is recorded as such, and storage is trimmed by whole runs rather than partially.</p>
      <details>
        <summary>What each run outcome means</summary>
        <ul className="legend">
          {["completed", "cancelled", "interrupted", "failed"].map((status) => <li key={status}><span className={"status " + statusTone(status)}>{status}</span> {runStatusExplanation(status)}</li>)}
        </ul>
        <p className="live-meta">Older runs beyond the retention window are removed as a whole. A partial scan is always labelled partial: it is not a clean baseline.</p>
      </details>
    </section>
  </div>;
}

function statusTone(status: string): "ok" | "bad" | "warn" | "neutral" {
  switch (status) {
    case "completed": return "ok";
    case "failed": return "bad";
    case "cancelled":
    case "interrupted": return "warn";
    default: return "neutral";
  }
}
