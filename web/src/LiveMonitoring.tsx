import { useCallback, useEffect, useState } from "react";
import {
  type EventsPage,
  type GapsPage,
  type MonitoringStatus,
  describeCounters,
  describeMetadata,
  fetchMonitoringEvents,
  fetchMonitoringGaps,
  fetchMonitoringStatus,
  formatBytes,
  formatTime,
  overallLabel,
  stateLabel,
  stateTone
} from "./monitoring";

// Live Monitoring: the recorded local observations, in the order a reader needs
// them — coverage and source health first, then the events themselves and the
// interruptions, and only then the explanation of what these observations are and
// are not. Nothing here is a malware verdict, and the page never implies one.
//
// Every value is read from the authenticated monitoring endpoints. Counters that
// mean observations were missed (dropped events, gaps) are named and toned as
// caveats rather than being folded into a row of zeros.

const pageLimit = 20;

export default function LiveMonitoring() {
  const [status, setStatus] = useState<MonitoringStatus | null>(null);
  const [eventsPage, setEventsPage] = useState<EventsPage | null>(null);
  const [gaps, setGaps] = useState<GapsPage | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [nextStatus, nextEvents, nextGaps] = await Promise.all([
        fetchMonitoringStatus(),
        fetchMonitoringEvents({ limit: pageLimit }),
        fetchMonitoringGaps(pageLimit)
      ]);
      setStatus(nextStatus);
      setEventsPage(nextEvents);
      setGaps(nextGaps);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Live monitoring data could not be loaded.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const loadMore = async () => {
    if (!eventsPage?.next_before) return;
    try {
      const next = await fetchMonitoringEvents({ before: eventsPage.next_before, limit: pageLimit });
      setEventsPage({ ...next, events: [...eventsPage.events, ...next.events] });
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Older events could not be loaded.");
    }
  };

  if (loading && !status) return <section className="panel" aria-busy="true"><h2>Loading observations…</h2><p>Reading recorded events from the local monitoring store.</p></section>;
  if (error && !status) return <section className="panel error" role="alert"><h2>Live monitoring unavailable</h2><p>{error}</p><button className="secondary" onClick={() => void load()}>Retry</button></section>;

  const sources = status?.sources ?? [];
  const missing = sources.filter((source) => source.state !== "healthy");

  return <div className="live">
    <div className="duo">
      {/* Coverage and health, first. */}
      <section className="panel compact">
        <p className="label">Monitoring health</p>
        <h2>{status ? overallLabel(status.state) : "Unavailable"}</h2>
        {status ? <>
          <span className={"status " + (status.state === "active" ? "ok" : status.state === "partial" ? "warn" : "neutral")}>{status.state}</span>
          <ul className="stat-list">
            <li><strong>{status.processed_events}</strong> events processed</li>
            <li><strong>{status.storage.event_count}</strong> events stored · <strong>{gaps?.total_gaps ?? status.sources.reduce((total, source) => total + source.monitoring_gaps, 0)}</strong> recorded gaps</li>
            <li><strong>{formatBytes(status.storage.database_bytes)}</strong> of {formatBytes(status.storage.max_database_bytes)} local database budget</li>
            <li>Retention <strong>{status.storage.retention_days} days</strong> · up to <strong>{status.storage.max_events}</strong> events</li>
            <li>{status.storage.encrypted ? "Encrypted at rest" : "Not encrypted at rest"}</li>
          </ul>
          <details className="evidence">
            <summary>What this coverage does and does not include</summary>
            {status.replay_limits.length > 0 && <ul className="live-limits">{status.replay_limits.map((limit) => <li key={limit}>{limit}</li>)}</ul>}
            <p className="live-meta">Store generated {formatTime(status.generated_at)}.</p>
          </details>
        </> : <p className="live-meta">{error || "Status unavailable."}</p>}
      </section>

      {/* Interruptions sit beside health because they qualify it. */}
      <section className="panel compact">
        <p className="label">Monitoring interruptions</p>
        <h2>{gaps ? (gaps.total_gaps === 1 ? "1 gap recorded" : gaps.total_gaps + " gaps recorded") : "Not reported"}</h2>
        {gaps && gaps.gaps.length > 0 ? <ul className="live-gaps">
          {gaps.gaps.map((gap) => <li key={gap.cursor}>
            <strong>{gap.source}</strong> · {formatTime(gap.start)}{gap.end !== gap.start ? " → " + formatTime(gap.end) : ""}<br />
            <span>{gap.reason}</span>
          </li>)}
          <li className="live-meta">{gaps.total_gaps} interruptions recorded in total.</li>
        </ul> : <p className="live-meta">{error || "No monitoring gaps have been recorded. This does not prove uninterrupted or complete coverage; source limitations and downtime without replay can still leave observations missing."}</p>}
      </section>
    </div>

    {/* Source status: which sources are reading, and what they can see. */}
    <section className="panel">
      <div className="posture-head">
        <div className="posture-summary">
          <p className="label">Sources</p>
          <h2>{sources.length === 1 ? "1 source" : sources.length + " sources"}</h2>
          {missing.length > 0 && <p className="live-warn" role="note">{missing.length === 1 ? "1 source is not reading normally" : missing.length + " sources are not reading normally"}: {missing.map((source) => source.id + " (" + stateLabel(source.state) + ")").join(", ")}.</p>}
        </div>
        <button className="secondary" onClick={() => void load()} disabled={loading}>{loading ? "Refreshing…" : "Refresh"}</button>
      </div>
      {sources.length > 0 ? <ul className="live-sources">
        {sources.map((source) => <li key={source.id}>
          <div className="live-source-head">
            <strong>{source.id}</strong>
            <span className={"status " + stateTone(source.state)}>{stateLabel(source.state)}</span>
            <span className="live-meta">Last event {formatTime(source.last_successful_event)}</span>
          </div>
          {/* Counters that mean observations were missed are toned as caveats;
              the rest are shown quietly so a zero is not mistaken for a warning. */}
          <ul className="counts" aria-label={"Counters for " + source.id}>
            {describeCounters(source).map((counter) => <li key={counter.label} className={"count " + (counter.material ? "warn" : "neutral")}>
              <span className="count-value">{counter.value}</span>
              <span className="count-label">{counter.label}</span>
            </li>)}
          </ul>
          {source.last_error && <p className="live-error">Last error: {source.last_error}</p>}
          <details className="evidence">
            <summary>Coverage, replay and limitations for this source</summary>
            <ul className="evidence-meta">
              <li><strong>Coverage:</strong> {source.capabilities?.join("; ") || "not reported"}</li>
              <li><strong>Platforms:</strong> {source.platforms?.join(", ") || "not reported"}</li>
              <li><strong>Replay after downtime:</strong> {source.recovery === "checkpoint" ? "supported from the last checkpoint" : "not supported, so this source cannot recover observations missed while Sparrow was not running"}</li>
              {source.limitations && source.limitations.length > 0 && <li><strong>Limitations:</strong> {source.limitations.join("; ")}</li>}
            </ul>
          </details>
        </li>)}
      </ul> : <p className="live-meta">No monitoring sources are registered on this platform.</p>}
    </section>

    {/* The events themselves. */}
    <section className="panel">
      <div className="posture-head">
        <div className="posture-summary">
          <p className="label">Recent events</p>
          <h2>{eventsPage ? (eventsPage.events.length === 1 ? "1 recorded observation" : eventsPage.events.length + " recorded observations") : "Not reported"}</h2>
        </div>
      </div>
      {eventsPage && eventsPage.events.length > 0 ? <>
        <div className="table-scroll"><table className="live-events">
          <thead><tr><th>Observed</th><th>Source</th><th>Type</th><th>Evidence</th><th>Detail</th></tr></thead>
          <tbody>
            {eventsPage.events.map((event) => <tr key={event.event_id + ":" + event.cursor}>
              <td>{formatTime(event.observed_at)}</td>
              <td>{event.source}</td>
              <td>{event.type}</td>
              <td>{event.evidence}{event.severity ? " · " + event.severity : ""}</td>
              <td>{describeMetadata(event.metadata) || "—"}</td>
            </tr>)}
          </tbody>
        </table></div>
        <p className="live-meta">Showing {eventsPage.events.length} recorded observations.</p>
        {eventsPage.next_before ? <button className="secondary" onClick={() => void loadMore()}>Load older events</button> : <p className="live-meta">End of stored history.</p>}
        {eventsPage.truncated && <p className="live-meta">Some older events were omitted to keep this page small; narrow the window or refresh.</p>}
      </> : <p className="live-meta">{error || "No events have been observed yet. Events appear when files change in the monitored user directories."}</p>}
    </section>

    {/* The explanation, after the data it qualifies. */}
    <section className="disclosure" aria-label="How to read these observations">
      <p className="disclosure-line"><strong>Observations, not verdicts.</strong>{" "}A recorded change is an observation, never a malware or compromise verdict.</p>
      <details>
        <summary>What this means</summary>
        <p>Live monitoring records changes in a small set of user-owned locations. No scanning, detection or assessment is performed, and an observation on its own does not say whether anything is wrong.</p>
        <p className="live-meta">Sources marked <strong>Permission denied</strong>, <strong>Degraded</strong> or <strong>Failed</strong> are stating what they could not read, not that the device is compromised. A source that cannot replay after downtime leaves a genuine gap, which is why gaps and dropped events are shown with the health summary rather than below the fold.</p>
      </details>
    </section>
  </div>;
}
