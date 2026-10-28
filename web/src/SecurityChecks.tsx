import { useCallback, useEffect, useMemo, useState } from "react";
import {
  type AssessmentSummary,
  type CheckDefinition,
  type CheckResult,
  attentionCount,
  checkOutcomeTitle,
  checkResultRank,
  collectionLabel,
  evidenceSummary,
  fetchChecks,
  formatDateTime,
  resultExplanation,
  resultLabel,
  resultTone,
  severityLabel,
  severityTone,
  startScan,
  summariseCheckResults
} from "./assessment";
import Evidence from "./Evidence";

// Security Checks: one row per compiled check, showing its result, evidence,
// freshness and remediation guidance. A check with no recorded result is shown
// as "Not yet evaluated" rather than being hidden or rendered as a pass.
//
// The page leads with what the recorded results actually are: failures first,
// then results that were never proven, then the checks that have not run, then
// the ones that do not apply here, and passes last. The legend that explains each
// verdict is one disclosure away rather than above the results, and the raw
// evidence for every verdict stays inspectable, because a result a user cannot
// check is not evidence-backed from their point of view.

type Row = { definition?: CheckDefinition; result?: CheckResult };

export default function SecurityChecks() {
  const [rows, setRows] = useState<Row[]>([]);
  const [summary, setSummary] = useState<AssessmentSummary | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState("all");
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const page = await fetchChecks();
      setSummary(page.summary);
      setRows(buildRows(page.definitions ?? [], page.checks));
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Check results could not be loaded.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  // Verification is a fresh observation: Sparrow re-reads the same fixed facts and
  // compares them. It never changes the system, so this button only ever starts
  // a new scan.
  const verify = async () => {
    setBusy(true);
    setActionError("");
    try {
      await startScan();
      await load();
    } catch (scanError) {
      setActionError(scanError instanceof Error ? scanError.message : "A verification scan could not be started.");
    } finally {
      setBusy(false);
    }
  };

  // The counts and the ordering are derived from the rows themselves, so what the
  // summary says and what the list shows can never disagree. The row order is
  // recorded order inside each group, because the sort is stable.
  const counts = useMemo(() => summariseCheckResults(rows.map((row) => row.result?.result)), [rows]);
  const attention = attentionCount(rows.map((row) => row.result?.result));
  const ordered = useMemo(
    () => [...rows].sort((a, b) => checkResultRank(a.result?.result) - checkResultRank(b.result?.result)),
    [rows]
  );

  if (loading && rows.length === 0) return <section className="panel" aria-busy="true"><h2>Loading checks…</h2><p>Reading the recorded check results for this device.</p></section>;
  if (error && rows.length === 0) return <section className="panel error" role="alert"><h2>Checks unavailable</h2><p>{error}</p><button className="secondary" onClick={() => void load()}>Retry</button></section>;

  const categories = ["all", "attention", ...Array.from(new Set(rows.map((row) => row.definition?.category ?? row.result?.category ?? "other"))).sort()];
  const visible = ordered.filter((row) => matches(row, filter));
  const failing = rows.filter((row) => row.result?.result === "fail").length;
  const headline = rows.length === 0
    ? "No checks were recorded"
    : failing > 0
      ? (failing === 1 ? "1 check failed" : failing + " checks failed")
      : attention > 0
        ? (attention === 1 ? "1 check needs attention" : attention + " checks need attention")
        : summary?.has_run
          ? "No failing or unverified checks"
          : "No assessment has run yet";

  return <div className="assessment">
    {/* What the results are, before how to read them. */}
    <section className="panel compact">
      <div className="posture-head">
        <div className="posture-summary">
          <p className="label">Recorded checks</p>
          <h2>{headline}</h2>
          <p className="live-meta">Every compiled check is listed below with its result. A check Sparrow could not read is reported as unknown, never as a pass.</p>
        </div>
        <div className="scan-actions">
          <button className="primary" onClick={() => void verify()} disabled={busy}>{busy ? "Starting…" : "Run verification scan"}</button>
          <button className="secondary" onClick={() => void load()} disabled={loading}>{loading ? "Refreshing…" : "Refresh"}</button>
        </div>
      </div>
      {actionError && <p className="live-error" role="alert">{actionError}</p>}
      <ul className="counts" aria-label="Recorded check results">
        {counts.map((count) => <li key={count.key} className={"count " + (count.count > 0 ? count.tone : "quiet")}>
          <span className="count-value">{count.count}</span>
          <span className="count-label">{count.label}</span>
        </li>)}
      </ul>
      {attention > 0 && <p className="live-warn" role="note">{attention === 1 ? "1 check either failed or was never proven" : attention + " checks either failed or were never proven"}: those are the results worth acting on first. A check that could not be read tells you nothing about the thing it measures.</p>}
      <div className="chip-row">
        {categories.map((item) => <button key={item} className={"chip" + (item === filter ? " active" : "")} aria-pressed={item === filter} onClick={() => setFilter(item)}>{item === "attention" ? "Needs attention" : item}</button>)}
      </div>
      <p className="live-meta">"Needs attention" shows failed, unknown, stale and not-yet-evaluated checks together — the results that either need a fix or could not be verified.</p>
    </section>

    {visible.length === 0
      ? <section className="panel compact"><p className="live-meta">No checks match this filter.</p></section>
      : visible.map((row) => <CheckCard key={row.definition?.id ?? row.result?.check_id} row={row} onVerify={() => void verify()} busy={busy} />)}

    {/* The legend, after the results it explains. */}
    <section className="disclosure" aria-label="How to read a result">
      <p className="disclosure-line"><strong>Every result is backed by evidence you can inspect.</strong>{" "}A collector's reading, its collection status and the time it was gathered are shown with each verdict.</p>
      <details>
        <summary>How to read a result</summary>
        <ul className="legend">
          <li><span className="status ok">Pass</span> {resultExplanation("pass")}</li>
          <li><span className="status bad">Fail</span> {resultExplanation("fail")}</li>
          <li><span className="status neutral">Unknown</span> {resultExplanation("unknown")}</li>
          <li><span className="status neutral">Not supported</span> {resultExplanation("not_supported")}</li>
          <li><span className="status warn">Stale</span> {resultExplanation("stale")}</li>
        </ul>
        <p className="live-meta">A refused or incomplete collection is reported as unknown or stale, never as a pass, and the collector's own note is shown with the check.</p>
      </details>
    </section>
  </div>;
}

// matches applies the category chips, including the derived "needs attention"
// filter that groups the results which are not a clean pass.
function matches(row: Row, filter: string): boolean {
  if (filter === "all") return true;
  if (filter === "attention") return checkResultRank(row.result?.result) <= 2;
  return (row.definition?.category ?? row.result?.category) === filter;
}

function CheckCard({ row, onVerify, busy }: { row: Row; onVerify: () => void; busy: boolean }) {
  const { definition, result } = row;
  const recordedTitle = definition?.title ?? result?.title ?? "Unknown check";
  const id = definition?.id ?? result?.check_id ?? "";
  const title = checkOutcomeTitle(id, result?.result ?? "", recordedTitle);
  const severity = definition?.severity ?? result?.severity;
  const evaluated = Boolean(result);
  const unread = evaluated && result?.evidence?.status !== "collected";

  return <section className="panel check-card">
    <div className="posture-head">
      <div className="posture-summary">
        <p className="label">{definition?.category ?? result?.category ?? "check"} · {id}</p>
        <h2>{title}</h2>
      </div>
      <div className="chip-row">
        {severity && evaluated && <span className={"status " + severityTone(severity)}>{severityLabel(severity)}</span>}
        <span className={"status " + (evaluated ? resultTone(result!.result) : "neutral")}>{evaluated ? resultLabel(result!.result) : "Not yet evaluated"}</span>
      </div>
    </div>

    {evaluated && result ? <div className="check-body">
      {/* The concrete reading first: what was observed, and what to do about it. */}
      <p>{result.detail || result.explanation}</p>
      {result.evidence?.error && <p className="live-error">Collection note: {result.evidence.error}</p>}
      {unread && <p className="live-warn" role="note">This result rests on a collection that was not clean ({collectionLabel(result.evidence?.status ?? "failed")}), so it is reported as {resultLabel(result.result).toLowerCase()} rather than as a pass.</p>}
      {result.limitations && result.limitations.length > 0 && <ul className="live-limits">{result.limitations.map((limit) => <li key={limit}>{limit}</li>)}</ul>}
      <p className="live-meta"><strong>What to do:</strong> {result.action}</p>
      <details className="evidence">
        <summary>How this result was read</summary>
        <ul className="evidence-meta">
          <li><strong>What this result means:</strong> {resultExplanation(result.result)}</li>
          <li><strong>Last observed:</strong> {formatDateTime(result.collected_at)} · evaluated {formatDateTime(result.evaluated_at)}</li>
          <li><strong>Evidence:</strong> {result.fresh ? "fresh" : "stale"} · rule v{result.check_version} · source {result.evidence?.source || "—"} · collection {collectionLabel(result.evidence?.status ?? "failed")}</li>
          <li><strong>Collected facts:</strong> {evidenceSummary(result.evidence)}</li>
          <li><strong>How to verify the fix:</strong> {result.verify_method}</li>
        </ul>
        <Evidence observation={result.evidence} />
        <p className="live-meta">Sparrow never changes your system, so verification means re-reading the same facts and comparing them.</p>
      </details>
      <button className="secondary" onClick={onVerify} disabled={busy}>{busy ? "Starting…" : "Verify with a new scan"}</button>
    </div> : <p className="live-meta">This check has not been evaluated yet. Run a scan from the Overview page to record a result.</p>}
  </section>;
}

// buildRows joins the compiled check definitions with their newest results so a
// check that never ran is still visible and honestly labelled.
function buildRows(definitions: CheckDefinition[], results: CheckResult[]): Row[] {
  const byID = new Map<string, CheckResult>();
  for (const result of results) byID.set(result.check_id, result);
  const rows: Row[] = definitions.map((definition) => ({ definition, result: byID.get(definition.id) }));
  for (const result of results) {
    if (!definitions.some((definition) => definition.id === result.check_id)) rows.push({ result });
  }
  return rows;
}
