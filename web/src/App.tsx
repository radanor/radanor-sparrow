import { useEffect, useState } from "react";
import Assessment from "./AssessmentPanel";
import Findings from "./Findings";
import LiveMonitoring from "./LiveMonitoring";
import NetworkServices from "./NetworkServices";
import ScanHistory from "./ScanHistory";
import SecurityChecks from "./SecurityChecks";
import Settings from "./SettingsPanel";
import Software from "./Software";
import { consumeLaunchTicket, initializeSession, postJSON } from "./auth";
import { brand } from "./brand";
import { buildIdentity, buildVariant, buildVariantLabel } from "./build";
import { product } from "./product";
import { fetchSettings } from "./settings";
import { applyTheme, parseTheme } from "./theme";
import "./styles.css";

type Foundation = {
  version: string;
  // Additive: the gateway reports the build identity so the dashboard can say
  // which build this is. An older gateway omits both fields, and the About area
  // then shows the version alone.
  variant?: string;
  build_label?: string;
  // Additive: the gateway reports its licensing so the dashboard can show an
  // About/license line. An older gateway omits it and the block stays hidden.
  license?: { name: string; notice: string; url: string };
  monitoring: { state: string; message: string };
  assessment: { state: string; message: string };
};

// Every page now has a genuine backing source. Overview, Live Monitoring,
// Security Checks, Findings and Scan History read the posture assessment and the
// event store; Software and Network & Services read the locally collected
// inventory snapshot; Settings reads and writes the supported preferences.
const navigation: Array<{ id: string; label: string; hint: string }> = [
  { id: "overview", label: "Overview", hint: "Device posture at a glance" },
  { id: "monitoring", label: "Live Monitoring", hint: "Recorded local observations" },
  { id: "software", label: "Software", hint: "Installed packages and applications" },
  { id: "network", label: "Network & Services", hint: "Local endpoints and services" },
  { id: "checks", label: "Security Checks", hint: "Every check and its evidence" },
  { id: "findings", label: "Findings & Alerts", hint: "Confirmed weaknesses" },
  { id: "history", label: "Scan History", hint: "Past scans and their outcomes" },
  { id: "settings", label: "Settings", hint: "Schedule, retention and appearance" }
];

export default function App() {
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [message, setMessage] = useState("");
  const [foundation, setFoundation] = useState<Foundation | null>(null);
  const [view, setView] = useState("overview");
  const [navOpen, setNavOpen] = useState(false);
  const [exiting, setExiting] = useState(false);
  const [managed, setManaged] = useState(false);

  const exitApplication = async () => {
    if (!window.confirm("Exit Radanor Sparrow? Monitoring will stop until you launch it again.")) return;
    try {
      await postJSON("/api/v1/application/exit");
      setExiting(true);
    } catch {
      window.alert("Radanor Sparrow could not exit. Try again from this authenticated dashboard.");
    }
  };

  useEffect(() => {
    const start = async () => {
      try {
        const ticket = consumeLaunchTicket(window.location, (url) => window.history.replaceState(null, "", url));
        await initializeSession(ticket);
        const response = await fetch("/api/v1/foundation", { credentials: "same-origin" });
        if (!response.ok) throw new Error("Launch Radanor Sparrow from its trusted launcher to open the dashboard.");
        setFoundation(await response.json() as Foundation);
        // Lifecycle controls are a separate extension; existing foundation and
        // assessment response schemas remain unchanged. Older gateways omit it.
        void fetch("/api/v1/application/status", { credentials: "same-origin" })
          .then(async (status) => { if (status.ok) setManaged((await status.json() as { managed?: boolean }).managed === true); })
          .catch(() => setManaged(false));
        setState("ready");
        // The stored theme is a preference like any other, so it comes from the
        // authenticated service rather than from browser storage.
        void fetchSettings()
          .then((settings) => applyTheme(parseTheme(settings.effective.theme)))
          .catch(() => applyTheme("system"));
      } catch (error) {
        setMessage(error instanceof Error ? error.message : "The dashboard could not start.");
        setState("error");
      }
    };
    void start();
  }, []);

  const current = navigation.find((item) => item.id === view) ?? navigation[0];
  const select = (id: string) => { setView(id); setNavOpen(false); };

  return <div className={"app-shell" + (navOpen ? " nav-open" : "")}>
    <a className="skip-link" href="#content">Skip to main content</a>
    <aside id="primary-navigation">
      <div className="brand">
        {/* The approved emblem, unmodified, on its own neutral surface. It is
            decorative here: the product name and edition follow as text. */}
        <span className="brand-plate">
          <img
            className="brand-emblem"
            src={brand.assets.emblem}
            alt=""
            aria-hidden="true"
            width={brand.emblemSize.width}
            height={brand.emblemSize.height}
          />
        </span>
        <div><strong>{product.name}</strong><small>{product.edition}</small></div>
      </div>
      <nav aria-label="Primary navigation">
        {navigation.map((item) => <button
          key={item.id}
          className={item.id === view ? "active" : ""}
          aria-current={item.id === view ? "page" : undefined}
          title={item.hint}
          onClick={() => select(item.id)}
        >{item.label}</button>)}
      </nav>
      <p className="privacy">Local-only<br />No telemetry configured</p>
      {foundation && <section className="about" aria-label="About this application">
        {/* The approved full logo, unmodified, on its own neutral surface. */}
        <span className="brand-plate about-plate">
          <img
            className="about-logo"
            src={brand.assets.logo}
            alt=""
            aria-hidden="true"
            width={brand.logoSize.width}
            height={brand.logoSize.height}
          />
        </span>
        <p className="about-product">{product.displayName}</p>
        {/* The version, and the variant below it, are the ones the core reports
            from the metadata the executable was built with; the interface never
            invents a version, and a development build is named as one instead
            of being assumed to be a release. The full build line is the core's
            own, kept on the hover so the panel stays uncluttered. */}
        <p className="about-version" title={buildIdentity(foundation) || undefined}>Version {foundation.version}</p>
        {buildVariantLabel(foundation) && <p className="about-variant" data-variant={buildVariant(foundation)}>{buildVariantLabel(foundation)}</p>}
        {foundation.license && <>
          <p className="about-license"><a href={foundation.license.url} target="_blank" rel="noreferrer noopener">{foundation.license.name}</a></p>
          <p className="about-notice">{foundation.license.notice}</p>
        </>}
      </section>}
    </aside>

    <main id="content">
      <header>
        <div>
          <p className="eyebrow">{current.hint}</p>
          <h1>{current.label}</h1>
        </div>
        <div className="header-actions">
          <span className="edition" title="Application version">{foundation?.version ?? product.edition}</span>
          {state === "ready" && managed && !exiting &&
            <button onClick={() => void exitApplication()}>Exit Radanor Sparrow</button>}
          <button className="nav-toggle" aria-expanded={navOpen} aria-controls="primary-navigation" onClick={() => setNavOpen((open) => !open)}>
            {navOpen ? "Close menu" : "Menu"}
          </button>
        </div>
      </header>

      {state === "loading" && <section className="panel"><h2>Starting securely…</h2><p>Establishing a protected local session.</p></section>}
      {state === "error" && <section className="panel error" role="alert"><h2>Dashboard unavailable</h2><p>{message}</p></section>}

      {exiting && <section className="panel"><h2>Radanor Sparrow is stopping</h2><p>You can close this tab. Launch Radanor Sparrow again to resume monitoring.</p></section>}
      {state === "ready" && foundation && !exiting && <>
        {view === "overview" && <Assessment onNavigate={select} />}
        {view === "monitoring" && <LiveMonitoring />}
        {view === "software" && <Software />}
        {view === "network" && <NetworkServices />}
        {view === "checks" && <SecurityChecks />}
        {view === "findings" && <Findings />}
        {view === "history" && <ScanHistory onNavigate={select} />}
        {view === "settings" && <Settings />}
      </>}
    </main>
  </div>;
}
