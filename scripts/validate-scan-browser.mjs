// Developer regression driver: Node 24 + installed Edge/Chrome; no downloads.
// URLs arrive over stdin, never argv/logs. Incognito contexts do not persist
// session cookies. No network header/body tracing, screenshots or HAR files.
import { spawn } from "node:child_process";
import { readFile, mkdtemp, rm } from "node:fs/promises";
import { join, resolve, sep, basename } from "node:path";

let browser, socket, profile, privateRoot;
let stage = "browser-startup";
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
try {
  let input = "";
  for await (const chunk of process.stdin) input += chunk;
  const config = JSON.parse(input);
  privateRoot = resolve(config.privateDir);
  profile = await mkdtemp(join(privateRoot, "browser-profile-"));
  browser = spawn(config.browser, ["--headless=new", "--incognito", "--no-first-run", "--no-default-browser-check",
    "--disable-background-networking", "--disable-component-update", "--disable-sync", "--disable-extensions",
    "--disable-breakpad", "--disable-crash-reporter", "--remote-debugging-address=127.0.0.1",
    "--remote-debugging-port=0", "--user-data-dir=" + profile, "about:blank"], { windowsHide: true, stdio: "ignore" });
  let address;
  for (let i = 0; i < 200; i++) {
    try {
      const info = (await readFile(join(profile, "DevToolsActivePort"), "utf8")).trim().split(/\r?\n/);
      address = "ws://127.0.0.1:" + info[0] + info[1];
      break;
    } catch { await sleep(100); }
  }
  if (!address) throw new Error("browser startup");
  socket = new WebSocket(address);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = () => reject(new Error("browser connection")); });
  let sequence = 0;
  let acceptExitDialog = false;
  const pending = new Map();
  socket.onmessage = ({ data }) => {
    const packet = JSON.parse(data);
    if (acceptExitDialog && packet.method === "Page.javascriptDialogOpening" && packet.params.type === "confirm") {
      void command("Page.handleJavaScriptDialog", { accept: true }, packet.sessionId).catch(() => {});
      return;
    }
    const entry = pending.get(packet.id);
    if (!entry) return; // Ignore events, including URLs; never log them.
    pending.delete(packet.id);
    packet.error ? entry.reject(new Error("browser command")) : entry.resolve(packet.result);
  };
  const command = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
    const id = ++sequence;
    pending.set(id, { resolve, reject });
    socket.send(JSON.stringify({ id, method, params, sessionId }));
  });
  const evaluate = async (session, expression) => {
    const result = await command("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true }, session);
    if (result.exceptionDetails) throw new Error("page evaluation");
    return result.result.value;
  };
  const waitFor = async (session, expression, attempts = 300) => {
    for (let i = 0; i < attempts; i++) { if (await evaluate(session, expression)) return; await sleep(200); }
    throw new Error("page condition timeout");
  };
  const context = await command("Target.createBrowserContext", { disposeOnDetach: true });
  const open = async (url) => {
    const target = await command("Target.createTarget", { url: "about:blank", browserContextId: context.browserContextId });
    const attached = await command("Target.attachToTarget", { targetId: target.targetId, flatten: true });
    await command("Page.enable", {}, attached.sessionId);
    await command("Page.navigate", { url }, attached.sessionId);
    await waitFor(attached.sessionId, 'Array.from(document.querySelectorAll("button")).some(b => b.textContent === "Scan now" && !b.disabled)');
    return attached.sessionId;
  };
  const first = await open(config.urls[0]);
  stage = "second-tab-login";
  await open(config.urls[1]); // Same context: rotates the shared HttpOnly cookie.
  stage = "scan-authentication-recovery";
  await evaluate(first, `(() => {
    const original = window.fetch.bind(window);
    window.__scanStatuses = [];
    window.fetch = async (...args) => {
      const response = await original(...args);
      if (args[0] === "/api/v1/assessment/scan") window.__scanStatuses.push(response.status);
      return response;
    };
    Array.from(document.querySelectorAll("button")).find(b => b.textContent === "Scan now").click();
    return true;
  })()`);
  await waitFor(first, 'window.__scanStatuses.length === 2 && window.__scanStatuses[0] === 401 && window.__scanStatuses[1] === 200');
  stage = "scan-completion";
  await waitFor(first, `fetch("/api/v1/assessment/status", { credentials: "same-origin" }).then(r => r.json()).then(s => !s.running && s.summary.last_run?.status === "completed")`, 600);
  await evaluate(first, 'Array.from(document.querySelectorAll("nav button")).find(b => b.textContent === "Security Checks").click()');
  stage = "recorded-security-checks";
  await waitFor(first, 'document.querySelector("h1")?.textContent === "Security Checks" && document.querySelectorAll(".check-card .check-body").length > 0');
  const checks = await evaluate(first, 'document.querySelectorAll(".check-card").length');
  const recorded = await evaluate(first, 'fetch("/api/v1/assessment/checks", { credentials: "same-origin" }).then(r => r.json()).then(s => s.checks.length)');
  // The existing bounded API can return fewer records than compiled definitions.
  // Verify actual result rows, without treating definition placeholders as data.
  const visibleRecords = await evaluate(first, 'document.querySelectorAll(".check-card .check-body").length');
  if (recorded <= 0 || recorded !== visibleRecords) throw new Error("recorded check count mismatch");
  await evaluate(first, 'Array.from(document.querySelectorAll("nav button")).find(b => b.textContent === "Scan History").click()');
  stage = "scan-history";
  await waitFor(first, 'document.querySelector("h1")?.textContent === "Scan History" && Array.from(document.querySelectorAll("tbody tr")).some(r => r.textContent.includes("completed"))');
  if (config.exitApplication) {
    stage = "authenticated-ui-exit";
    acceptExitDialog = true;
    await evaluate(first, 'Array.from(document.querySelectorAll("button")).find(b => b.textContent === "Exit Radanor Sparrow").click()');
    await waitFor(first, 'document.body.textContent.includes("Radanor Sparrow is stopping")');
  }
  console.log(JSON.stringify({ scanRejectedBeforeRecovery: true, scanAcceptedAfterRecovery: true, completed: true, securityCheckCards: checks, recordedCheckCount: recorded, historyVisible: true }));
  await command("Target.disposeBrowserContext", { browserContextId: context.browserContextId });
  socket.send(JSON.stringify({ id: ++sequence, method: "Browser.close" }));
  await sleep(1000);
} catch {
  console.error("Native browser scan regression failed at " + stage + "; credential diagnostics are disabled.");
  process.exitCode = 1;
} finally {
  socket?.close();
  if (browser && browser.exitCode === null) browser.kill();
  if (profile && resolve(profile).startsWith(privateRoot + sep) && basename(profile).startsWith("browser-profile-")) {
    try { await rm(profile, { recursive: true, force: true }); } catch {}
  }
}
