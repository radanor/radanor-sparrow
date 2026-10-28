import { type EvidenceValue, type Observation, collectionExplanation, collectionLabel, formatDateTime } from "./assessment";

// Evidence inspection.
//
// Every security verdict in the dashboard is backed by one collector reading.
// This component shows that reading in full: which collector produced it, when,
// how it succeeded, and every bounded value it recorded. Values are collector
// output and are treated as untrusted text — React escapes them, and each value
// is length-capped so a hostile or malformed reading cannot dominate the page.

const maxValueChars = 300;

export default function Evidence({ observation }: { observation?: Observation }) {
  if (!observation) {
    return <p className="live-meta">No evidence record is attached to this result.</p>;
  }
  const values = observation.values ?? [];
  return <details className="evidence">
    <summary>Show evidence ({values.length} recorded {values.length === 1 ? "value" : "values"})</summary>
    <ul className="evidence-meta">
      <li><strong>Collector:</strong> {observation.collector || "unknown"}</li>
      <li><strong>Source:</strong> {observation.source || "not reported"}</li>
      <li><strong>Platform:</strong> {observation.platform || "not reported"}</li>
      <li><strong>Collected:</strong> {formatDateTime(observation.collected_at)}</li>
      <li><strong>Collection status:</strong> {collectionLabel(observation.status)} — {collectionExplanation(observation.status)}</li>
      {observation.error && <li className="live-error"><strong>Collector note:</strong> {observation.error.slice(0, maxValueChars)}</li>}
    </ul>
    {values.length > 0
      ? <div className="table-scroll"><table className="live-events">
        <thead><tr><th>Value</th><th>Reading</th></tr></thead>
        <tbody>
          {values.map((value) => <tr key={value.key}>
            <td>{value.key}</td>
            <td>{render(value).slice(0, maxValueChars)}</td>
          </tr>)}
        </tbody>
      </table></div>
      : <p className="live-meta">The collector recorded no values. This is why the check could not be verified.</p>}
  </details>;
}

function render(value: EvidenceValue): string {
  if (value.kind === "bool") return value.bool ? "true" : "false";
  if (value.kind === "int") return String(value.int ?? 0);
  return value.text ?? "";
}
