import { useCallback, useEffect, useState } from "react";
import { type AssessmentStatus, fetchAssessmentStatus, formatDateTime, resultLabel } from "./assessment";
import {
  type SettingDescriptor,
  type SettingsResponse,
  type SettingsUpdate,
  defaultInterval,
  describeInterval,
  fetchSettings,
  intervalOptions,
  isScheduleFormDirty,
  parseIntBound,
  storageRows,
  updateSettings,
  usagePercent
} from "./settings";
import { applyTheme, parseTheme, themeOptions, type Theme } from "./theme";
import { formatBytes } from "./monitoring";

// Settings: every preference Sparrow supports, with its bounds shown.
//
// Two principles shape this page. First, the controls are built from the
// descriptor the core returns, so the UI cannot offer a preference the backend
// would refuse and unsupported keys are rejected by both the gateway and the
// core. Second, a behaviour change is explicit: schedule and retention edits are
// saved as one validated batch, and a refusal leaves the stored configuration
// untouched. The theme is a purely local display choice, so it is applied and
// saved immediately.
//
// The page is grouped the way it is used: what is in force, what can be changed,
// what is stored locally, and what this build deliberately does not do. Nothing
// here changes a preference's meaning or its bounds.

export default function Settings() {
  const [settings, setSettings] = useState<SettingsResponse | null>(null);
  const [assessment, setAssessment] = useState<AssessmentStatus | null>(null);
  const [enabled, setEnabled] = useState(false);
  const [interval, setInterval] = useState(defaultInterval);
  const [maxRuns, setMaxRuns] = useState(50);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [saveError, setSaveError] = useState("");
  const [saved, setSaved] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [next, nextAssessment] = await Promise.all([
        fetchSettings(),
        fetchAssessmentStatus().catch(() => null)
      ]);
      setSettings(next);
      setAssessment(nextAssessment);
      setEnabled(next.effective.assess_enabled);
      // An effective interval of "" means nothing has been stored and the UI's
      // own default applies. Comparing against that same default is what keeps
      // the page from reporting unsaved changes the moment it loads.
      setInterval(next.effective.assess_interval || defaultInterval);
      setMaxRuns(next.effective.assess_max_runs);
      applyTheme(parseTheme(next.effective.theme));
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Settings could not be loaded.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const save = async (update: SettingsUpdate, confirmation: string) => {
    setBusy(true);
    setSaveError("");
    setSaved("");
    try {
      const next = await updateSettings(update);
      setSettings(next);
      setEnabled(next.effective.assess_enabled);
      setInterval(next.effective.assess_interval || defaultInterval);
      setMaxRuns(next.effective.assess_max_runs);
      // Re-read the authoritative effective state rather than trusting what was
      // typed: the core is the only source of truth for what is now in force.
      setSaved(confirmation);
      void fetchAssessmentStatus().then(setAssessment).catch(() => undefined);
    } catch (saveFailure) {
      setSaveError(saveFailure instanceof Error ? saveFailure.message : "The settings could not be saved.");
    } finally {
      setBusy(false);
    }
  };

  const chooseTheme = async (theme: Theme) => {
    applyTheme(theme);
    await save({ theme }, "Theme saved.");
  };

  if (loading && !settings) return <section className="panel" aria-busy="true"><h2>Loading settings…</h2><p>Reading your stored preferences and local storage usage.</p></section>;
  if (error && !settings) return <section className="panel error" role="alert"><h2>Settings unavailable</h2><p>{error}</p><button className="secondary" onClick={() => void load()}>Retry</button></section>;
  if (!settings) return null;

  const descriptor = (key: string): SettingDescriptor | undefined => settings.supported.find((item) => item.key === key);
  const intervalDescriptor = descriptor("assess_interval");
  const runsDescriptor = descriptor("assess_max_runs");
  const minRuns = parseIntBound(runsDescriptor?.minimum) ?? 10;
  const maxRunsBound = parseIntBound(runsDescriptor?.maximum) ?? 5000;
  const options = intervalOptions(intervalDescriptor);
  const intervalChoices = options.some((option) => option.value === interval) ? options : [{ value: interval, label: describeInterval(interval) }, ...options];
  const rows = storageRows(settings.storage);
  // isScheduleFormDirty treats the interval shown when the core reports none as
  // this page's own default, so a freshly loaded page with no stored schedule is
  // not "edited". See the helper for why that matters.
  const effectiveInterval = settings.effective.assess_interval || defaultInterval;
  const dirty = isScheduleFormDirty(settings.effective, { enabled, interval, maxRuns });

  return <div className="assessment">
    {/* One line of context, with the detail one disclosure away. */}
    <section className="disclosure" aria-label="Where these settings live">
      <p className="disclosure-line"><strong>Everything here stays on this device.</strong>{" "}Preferences are stored locally and applied by the background service.</p>
      <details>
        <summary>What this means</summary>
        <p>Preferences are stored in your local state directory. There is no account, no sync, and nothing is sent anywhere. Sparrow runs as your user with no elevation, so it only offers settings it can honour that way.</p>
      </details>
    </section>

    {/* What is in force right now, and how to change the display. */}
    <div className="duo">
      <section className="panel compact appearance-settings">
        <div className="posture-head">
          <div className="posture-summary">
            <p className="label">Appearance</p>
            <h2>Theme: {settings.effective.theme}</h2>
          </div>
        </div>
        <div className="chip-row">
          {themeOptions().map((option) => <button
            key={option.value}
            className={"chip" + (settings.effective.theme === option.value ? " active" : "")}
            aria-pressed={settings.effective.theme === option.value}
            title={option.description}
            onClick={() => void chooseTheme(option.value)}
            disabled={busy}
          >{option.label}</button>)}
        </div>
        <p className="live-meta">The dashboard follows your operating system unless you choose otherwise, and respects your reduced-motion preference either way. The theme is saved as soon as you pick it.</p>
      </section>

      <section className="panel compact">
        <p className="label">Data freshness</p>
        <h2>{assessment?.summary.has_run ? "Last completed assessment " + formatDateTime(assessment.summary.last_complete_at) : "No assessment has completed yet"}</h2>
        <ul className="stat-list">
          <li>Check results: <strong>{assessment ? (assessment.summary.checks_fresh ? "fresh" : "stale") : "not reported"}</strong>{assessment && !assessment.summary.checks_fresh ? " — re-run a scan" : ""}</li>
          <li>Findings open: <strong>{assessment?.summary.open_findings ?? "—"}</strong></li>
          <li>Failed checks: <strong>{assessment?.summary.failed_checks ?? "—"}</strong></li>
          <li>Unverified checks: <strong>{assessment ? (assessment.summary.unknown_checks + assessment.summary.stale_checks) : "—"}</strong></li>
        </ul>
        <p className="live-meta">An inventory snapshot is a moment in time: the Software and Network &amp; Services pages show when their data was collected. Results are never presented as live readings.</p>
      </section>
    </div>

    {/* The one setting that changes behaviour. */}
    <section className="panel">
      <div className="posture-head">
        <div className="posture-summary">
          <p className="label">Automatic assessments</p>
          <h2>{settings.effective.assess_enabled ? "Enabled" : "Disabled"}</h2>
          <p className="live-meta">
            {settings.effective.assess_enabled
              ? "A scan runs every " + describeInterval(settings.effective.assess_interval) + "."
              : "Scans only run when you start one from the Overview page."}
          </p>
        </div>
        <div className="scan-actions">
          <button
            className="primary"
            disabled={busy || !dirty}
            onClick={() => void save({ assess_enabled: enabled, assess_interval: interval, assess_max_runs: maxRuns }, "Schedule and retention saved.")}
          >{busy ? "Saving…" : "Save schedule and retention"}</button>
          <button className="secondary" disabled={busy || !dirty} onClick={() => {
            setEnabled(settings.effective.assess_enabled);
            setInterval(effectiveInterval);
            setMaxRuns(settings.effective.assess_max_runs);
            setSaveError("");
            setSaved("Changes discarded.");
          }}>Discard changes</button>
        </div>
      </div>
      <label className="field checkbox-field">
        <input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />
        <span>Run a posture assessment automatically on a schedule</span>
      </label>
      <div className="filter-row">
        <label className="field">
          <span>Scan interval{intervalDescriptor ? " (" + (intervalDescriptor.minimum ?? "—") + " to " + (intervalDescriptor.maximum ?? "—") + ")" : ""}</span>
          <select value={interval} onChange={(event) => setInterval(event.target.value)} disabled={!enabled} aria-label="Scan interval">
            {intervalChoices.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
          </select>
        </label>
        <label className="field">
          <span>Assessment runs retained{runsDescriptor ? " (" + minRuns + " to " + maxRunsBound + ")" : ""}</span>
          <input
            type="number"
            min={minRuns}
            max={maxRunsBound}
            value={maxRuns}
            onChange={(event) => setMaxRuns(Number.parseInt(event.target.value, 10) || minRuns)}
            aria-label="Assessment runs retained"
          />
        </label>
      </div>
      {dirty && <p className="live-warn" role="note">You have unsaved changes. Nothing is applied until you save.</p>}
      {saveError && <p className="live-error" role="alert">{saveError}</p>}
      {saved && !saveError && <p className="live-ok" role="status">{saved}</p>}
      <ul className="stat-list">
        <li>Configured source: <strong>{settings.config_source}</strong></li>
        <li>{(settings.next_scan_at && formatDateTime(settings.next_scan_at) !== "—")
          ? <>Next scan: <strong>{formatDateTime(settings.next_scan_at)}</strong></>
          : settings.effective.assess_enabled
            ? "No scan scheduled yet"
            : "Automatic assessments are off, so no scan is scheduled"}</li>
      </ul>
      <details className="evidence">
        <summary>What is in force right now</summary>
        <ul className="live-limits">
          <li>Automatic assessments: {settings.effective.assess_enabled ? "enabled every " + describeInterval(settings.effective.assess_interval) : "disabled"}</li>
          <li>Assessment runs retained: {settings.effective.assess_max_runs}</li>
          <li>Latest recorded outcome: {assessment?.summary.last_run ? resultLabel(assessment.summary.last_run.status) : "no run recorded"}</li>
          <li>Theme: {settings.effective.theme}</li>
        </ul>
        <p className="live-meta">Every value here is the one the background service reports as in force, not what this page sent.</p>
      </details>
    </section>

    {/* What is on disk, which is the part a user can actually run out of. */}
    <section className="panel">
      <p className="label">Local storage</p>
      <h2>{formatBytes(settings.storage.database_bytes)} on disk</h2>
      <div className="meter-list">
        {rows.map((row) => <div className="meter" key={row.label}>
          <div className="meter-head">
            <strong>{row.label}</strong>
            <span>{row.limit > 0 ? formatBytes(row.used) + " of " + formatBytes(row.limit) : formatBytes(row.used)}</span>
          </div>
          {row.limit > 0 && <div className="meter-track" role="img" aria-label={row.label + " storage usage " + usagePercent(row.used, row.limit) + " percent"}>
            <div className={"meter-fill" + (usagePercent(row.used, row.limit) >= 90 ? " high" : "")} style={{ width: usagePercent(row.used, row.limit) + "%" }} />
          </div>}
          <p className="live-meta">{row.note}</p>
        </div>)}
      </div>
      <p className="live-meta">
        Event history is kept for {settings.storage.event_retention_days} days, up to {settings.storage.max_events} events. The inventory keeps at most {settings.storage.max_inventory_items_per_kind} entries per category.
      </p>
    </section>

    {/* The product's own limits, stated next to the controls rather than hidden. */}
    <section className="panel">
      <p className="label">Privacy and security limitations</p>
      <ul className="live-limits">
        {settings.limitations.map((limit) => <li key={limit}>{limit}</li>)}
      </ul>
      <p className="live-meta">
        Sparrow detects no malware, removes nothing, and makes no change to your system. It reads a small, fixed set of local facts and records them on this device only.
      </p>
    </section>

    <section className="disclosure" aria-label="Preferences this build does not offer">
      <p className="disclosure-line"><strong>Some preferences are deliberately absent.</strong>{" "}They are not hidden controls; this build has no such feature.</p>
      <details>
        <summary>Preferences this build does not offer</summary>
        <ul className="live-limits">
          <li>No cloud account, sign-in or sync. Preferences exist only on this device.</li>
          <li>No privileged or system-wide mode: Sparrow never asks for elevation, so it cannot offer a setting that would need it.</li>
          <li>No custom collector paths, commands or scan targets. The collector set is fixed in the program.</li>
          <li>No automatic remediation. Fixing anything stays entirely in your hands.</li>
        </ul>
        <p className="live-meta">Anything this page does not show is not supported, which is different from being unavailable: the background service refuses a preference it does not implement.</p>
      </details>
    </section>

    <p className="live-refresh"><button className="secondary" onClick={() => void load()} disabled={loading}>{loading ? "Refreshing…" : "Reload from service"}</button></p>
  </div>;
}
