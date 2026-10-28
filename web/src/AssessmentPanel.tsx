import { useCallback, useEffect, useState } from "react";
import {
  type AssessmentStatus,
  type CheckResult,
  type DeviceInfo,
  type FindingsPage,
  cancelScan,
  checkOutcomeTitle,
  describeLastRun,
  describeSchedule,
  deviceDisplay,
  evidenceSummary,
  fetchAssessmentStatus,
  fetchChecks,
  fetchDeviceInfoForOverview,
  fetchFindings,
  formatDateTime,
  formatDuration,
  isOpenLifecycle,
  partialScanNote,
  phaseLabel,
  postureCounts,
  postureHeadline,
  prioritiseChecks,
  resultLabel,
  resultTone,
  severityLabel,
  severityTone,
  startScan,
  unverifiedSummary
} from "./assessment";
import { type MonitoringStatus, fetchMonitoringStatus, overallLabel } from "./monitoring";

// Overview: the first page, and the one that answers "what is the state of this
// device?" without making the reader hunt for the answer. The order is device
// identity, then the recorded results and the single primary action, then
// monitoring health, the last assessment, the findings, and the results
// themselves — with the limitations that qualify all of it kept visible but
// compact.
//
// Every value on this page comes from an authenticated endpoint response. Nothing
// is a placeholder, an unverified result is never dressed as a pass, and no
// sentence here claims the device is secure.

const CHECK_ROWS = 6;
const FINDING_ROWS = 3;

export default function Assessment({ onNavigate }: { onNavigate?: (view: string) => void }) {
  const [status, setStatus] = useState<AssessmentStatus | null>(null);
  const [checks, setChecks] = useState<CheckResult[]>([]);
  const [findings, setFindings] = useState<FindingsPage | null>(null);
  const [monitoring, setMonitoring] = useState<MonitoringStatus | null>(null);
  const [device, setDevice] = useState<DeviceInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState("");

  const load = useCallback(async () => {
    setError("");
    try {
      const [nextStatus, nextChecks, nextFindings, nextMonitoring, nextDevice] = await Promise.all([
        fetchAssessmentStatus(),
        fetchChecks(),
        fetchFindings({ limit: 10 }),
        fetchMonitoringStatus(),
        fetchDeviceInfoForOverview()
      ]);
      setStatus(nextStatus);
      setChecks(nextChecks.checks);
      setFindings(nextFindings);
      setMonitoring(nextMonitoring);
      setDevice(nextDevice);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Assessment data could not be loaded.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  // While a scan is running the page polls the honest progress snapshot.
  useEffect(() => {
    if (!status?.running) return;
    const timer = window.setInterval(() => { void load(); }, 2000);
    return () => window.clearInterval(timer);
  }, [status?.running, load]);

  const runScan = async () => {
    setBusy(true);
    setActionError("");
    try {
      await startScan();
      await load();
    } catch (scanError) {
      setActionError(scanError instanceof Error ? scanError.message : "The scan could not be started.");
    } finally {
      setBusy(false);
    }
  };

  const stopScan = async () => {
    setBusy(true);
    setActionError("");
    try {
      await cancelScan();
      await load();
    } catch (cancelError) {
      setActionError(cancelError instanceof Error ? cancelError.message : "The scan could not be cancelled.");
    } finally {
      setBusy(false);
    }
  };

  if (loading && !status) return <section className="panel" aria-busy="true"><h2>Loading posture…</h2><p>Reading the recorded assessment results for this device.</p></section>;
  if (error && !status) return <section className="panel error" role="alert"><h2>Assessment unavailable</h2><p>{error}</p><button className="secondary" onClick={() => void load()}>Retry</button></section>;

  const summary = status?.summary;
  const headline = postureHeadline(summary);
  const counts = postureCounts(summary?.last_run);
  const deviceView = deviceDisplay(device);
  const actionable = (findings?.findings ?? []).filter((finding) => isOpenLifecycle(finding.lifecycle));
  const listed = prioritiseChecks(checks).slice(0, CHECK_ROWS);
  const coverage = summary?.last_run ? partialScanNote(summary.last_run) : null;
  const unverified = unverifiedSummary(summary);

  return <div className="assessment">
    {/* The limitation that qualifies every number below it: one line, with the
        full explanation and the core's own limits behind one disclosure. */}
    <section className="disclosure" aria-label="How to read these results">
      <p className="disclosure-line"><strong>Assessments state what was verified — and what was not.</strong>{" "}An incomplete check is recorded as unknown, never as a pass.</p>
      <details>
        <summary>What this means</summary>
        <p>Radanor Sparrow never reports a device as secure merely because nothing suspicious was observed. A check that could not be completed is shown as unknown, not as a pass.</p>
        {unverified && <p className="live-meta">{unverified}</p>}
        {status?.limits && status.limits.length > 0 && <ul className="live-limits">{status.limits.map((limit) => <li key={limit}>{limit}</li>)}</ul>}
        {onNavigate && <p><button className="link-button" onClick={() => onNavigate("checks")}>See every check and its evidence</button></p>}
      </details>
    </section>

    <div className="grid">
      {/* 1. Device identity and operating system. */}
      <section className="panel compact">
        <p className="label">Device</p>
        <h2>{deviceView.operatingSystem}</h2>
        <p className="live-meta">{deviceView.architecture}</p>
        <span className={"status " + deviceView.tone}>{deviceView.platform}</span>
        {device?.collected_at && <p className="live-meta">Collected {formatDateTime(device.collected_at)}{device.stale ? " · stale" : ""}</p>}
        {deviceView.note && <p className="live-meta">{deviceView.note}</p>}
      </section>

      {/* 2 and 3. What the results show, and the one primary action. */}
      <section className={"panel compact posture span-2 tone-" + headline.tone}>
        <div className="posture-head">
          <div className="posture-summary">
            <p className="label">Security posture</p>
            <h2>{headline.text}</h2>
            <p className="live-meta">{headline.detail}</p>
          </div>
          <div className="scan-actions">
            <button className="primary" onClick={() => void runScan()} disabled={busy || status?.running}>Scan now</button>
            <button className="secondary" onClick={() => void stopScan()} disabled={busy || !status?.running}>Cancel scan</button>
          </div>
        </div>
        {status && status.availability !== "available" && <p className="live-warn" role="note">{status.note}</p>}
        {status?.running && <p className="live-meta">{phaseLabel(status.scan.phase)} · {status.scan.collectors_done} of {status.scan.collectors_total} collectors complete</p>}
        {counts.length > 0 && <ul className="counts" aria-label="Results of the last assessment">
          {counts.map((count) => <li key={count.key} className={"count " + count.tone}>
            <span className="count-value">{count.count}</span>
            <span className="count-label">{count.label}</span>
          </li>)}
        </ul>}
        {summary?.last_run && <p className="live-meta">Last assessment: {describeLastRun(summary.last_run)}</p>}
        {coverage && <p className="live-warn" role="note">{coverage}</p>}
        <p className="live-meta">{status ? describeSchedule(status.scheduler) : "Scheduler: status not reported"}</p>
        {status?.last_error && <p className="live-error">Last error: {status.last_error}</p>}
        {actionError && <p className="live-error">{actionError}</p>}
      </section>
    </div>

    <div className="grid">
      {/* 4. Monitoring health. */}
      <section className="panel compact">
        <p className="label">Monitoring health</p>
        <h2>{monitoring ? overallLabel(monitoring.state) : "Unavailable"}</h2>
        <span className={"status " + (monitoring?.state === "active" ? "ok" : monitoring ? "warn" : "neutral")}>{monitoring ? monitoring.state : "unknown"}</span>
        {monitoring ? <ul className="stat-list">
          <li><strong>{monitoring.processed_events}</strong> events processed</li>
          <li><strong>{monitoring.sources.length}</strong> sources</li>
          <li><strong>{monitoring.storage.event_count}</strong> events stored · <strong>{monitoring.storage.gap_count}</strong> recorded gaps</li>
        </ul> : <p className="live-meta">Live monitoring data could not be read, so this card says nothing about coverage.</p>}
        {onNavigate && <p><button className="link-button" onClick={() => onNavigate("monitoring")}>Open live monitoring</button></p>}
      </section>

      {/* 5. Last assessment summary. */}
      <section className="panel compact">
        <p className="label">Last assessment</p>
        <h2>{summary?.last_complete_at ? formatDateTime(summary.last_complete_at) : "Not yet assessed"}</h2>
        <span className={"status " + (summary?.last_run?.status === "completed" ? "ok" : summary?.last_run ? "warn" : "neutral")}>{summary?.last_run?.status ?? "no run"}</span>
        {summary?.last_run ? <ul className="stat-list">
          <li>Trigger <strong>{summary.last_run.trigger}</strong></li>
          <li>Duration <strong>{formatDuration(summary.last_run.duration_ms)}</strong></li>
          <li><strong>{summary.last_run.collectors_run}</strong> {summary.last_run.collectors_run === 1 ? "collector" : "collectors"} run · <strong>{summary.last_run.collectors_failed}</strong> returned incomplete or failed</li>
          <li><strong>{summary.last_run.new_findings}</strong> new {summary.last_run.new_findings === 1 ? "finding" : "findings"}</li>
        </ul> : <p className="live-meta">Run a scan to record this device's first baseline.</p>}
        {onNavigate && <p><button className="link-button" onClick={() => onNavigate("history")}>Open scan history</button></p>}
      </section>

      {/* 2, continued: the findings behind the counts. */}
      <section className="panel compact">
        <p className="label">Open findings</p>
        <h2>{summary?.open_findings ?? 0}</h2>
        <p className="live-meta">{summary ? summariseSeverities(summary.by_severity) : "Findings could not be summarised."}</p>
        {actionable.length > 0 ? <ul className="finding-list">
          {actionable.slice(0, FINDING_ROWS).map((finding) => <li key={finding.fingerprint}>
            <div className="live-source-head">
              <strong>{checkOutcomeTitle(finding.check_id, "fail", finding.title)}</strong>
              <span className={"status " + severityTone(finding.severity)}>{severityLabel(finding.severity)}</span>
            </div>
            <p className="live-meta">{finding.action}</p>
            <p className="live-meta">First seen {formatDateTime(finding.first_seen_at)} · last seen {formatDateTime(finding.last_seen_at)}</p>
          </li>)}
        </ul> : <p className="live-meta">{summary?.has_run ? "No confirmed weakness is currently open." : "Run an assessment to record findings."}</p>}
        {onNavigate && <p><button className="link-button" onClick={() => onNavigate("findings")}>Open findings &amp; alerts</button></p>}
      </section>
    </div>

    {/* 6. The results themselves, most serious first, with the truncation stated. */}
    <section className="panel">
      <div className="posture-head">
        <div className="posture-summary">
          <p className="label">Check results</p>
          <h2>{checks.length > 0 ? checks.length + " recorded results" : "No check results recorded"}</h2>
        </div>
        {onNavigate && <button className="link-button" onClick={() => onNavigate("checks")}>Open security checks</button>}
      </div>
      {checks.length > 0 ? <>
        <div className="table-scroll"><table className="live-events">
          <thead><tr><th>Check</th><th>Result</th><th>Detail</th><th>Evidence</th></tr></thead>
          <tbody>
            {listed.map((check) => <tr key={check.check_id}>
              <td>{check.title}</td>
              <td><span className={"status " + resultTone(check.result)}>{resultLabel(check.result)}</span></td>
              <td>{check.detail || "—"}</td>
              <td>{evidenceSummary(check.evidence, 4)}</td>
            </tr>)}
          </tbody>
        </table></div>
        <p className="live-meta">Showing {listed.length} of {checks.length} recorded results, most serious first.</p>
      </> : <p className="live-meta">Nothing has been evaluated yet. Run a scan to record check results.</p>}
    </section>

    <p className="live-refresh"><button className="secondary" onClick={() => void load()} disabled={loading}>{loading ? "Refreshing…" : "Refresh"}</button></p>
  </div>;
}

function summariseSeverities(bySeverity: Record<string, number>): string {
  const order = ["critical", "high", "medium", "low"];
  const parts = order.filter((key) => (bySeverity[key] ?? 0) > 0).map((key) => (bySeverity[key] ?? 0) + " " + severityLabel(key).toLowerCase());
  return parts.length > 0 ? "Severity mix: " + parts.join(", ") : "No open findings by severity.";
}
