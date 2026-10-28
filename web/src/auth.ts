let csrf = "";
let recovery: Promise<void> | null = null;
let initialization: Promise<void> | null = null;

export function consumeLaunchTicket(location: Pick<Location, "hash" | "pathname" | "search">, replace: (url: string) => void): string | null {
  const params = new URLSearchParams(location.hash.startsWith("#") ? location.hash.slice(1) : location.hash);
  const ticket = params.get("ticket");
  replace(location.pathname + location.search);
  return ticket && ticket.length >= 32 && ticket.length <= 128 ? ticket : null;
}

export async function exchangeTicket(ticket: string): Promise<void> {
  const response = await fetch("/api/v1/session/exchange", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ticket })
  });
  if (!response.ok) throw new Error("The launch link is invalid or expired. Close this tab and launch Radanor Sparrow again.");
  await rememberCSRF(response);
}

async function rememberCSRF(response: Response): Promise<void> {
  const body = await response.json() as { csrf_token?: unknown };
  if (typeof body.csrf_token !== "string" || !/^[A-Za-z0-9_-]{32,128}$/.test(body.csrf_token)) {
    throw new Error("A protected session could not be established. Launch Radanor Sparrow again.");
  }
  csrf = body.csrf_token;
}

async function recoverCSRF(): Promise<void> {
  if (!recovery) {
    recovery = (async () => {
      const response = await fetch("/api/v1/session/csrf", {
        method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: "{}"
      });
      if (!response.ok) throw new Error("Your session ended or changed. Launch Radanor Sparrow again to open the dashboard.");
      await rememberCSRF(response);
    })().finally(() => { recovery = null; });
  }
  await recovery;
}

// Also covers reloads and StrictMode's repeated initialization. Credentials
// stay in memory; browser tabs share only the HttpOnly cookie, not JS state.
export function initializeSession(ticket: string | null): Promise<void> {
  if (!initialization) {
    initialization = (ticket ? exchangeTicket(ticket) : recoverCSRF())
      .catch((error: unknown) => { initialization = null; throw error; });
  }
  return initialization;
}

// csrfToken returns the in-memory token minted by the session exchange.
export function csrfToken(): string {
  return csrf;
}

// postJSON sends a same-origin state-changing request with the CSRF header and
// an empty JSON body. The caller supplies only the path; there is no parameter
// that could name a target.
export async function postJSON(path: string): Promise<unknown> {
  return await postJSONBody(path, {});
}

// postJSONBody sends a same-origin state-changing request with the CSRF header.
// The body is restricted to the reviewed preference shape by the callers here;
// the gateway and the core validate the same fields again and reject anything
// unsupported, so this function cannot be used to store an arbitrary blob.
export async function postJSONBody(path: string, body: unknown): Promise<unknown> {
  const request = () => fetch(path, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
    body: JSON.stringify(body ?? {})
  });
  let response = await request();
  if (response.status === 401) {
    // Middleware rejects before dispatch, so one retry cannot duplicate an
    // accepted scan. Do not retry conflicts, server errors or transport errors.
    await recoverCSRF();
    response = await request();
  }
  if (!response.ok) {
    const refusal = await response.text();
    if (response.status === 409 || response.status === 400) {
      throw new Error(boundedRefusal(refusal) || "The request was refused.");
    }
    throw new Error("The action could not be completed.");
  }
  return await response.json();
}

// boundedRefusal keeps a server-supplied refusal short, single-line and free of
// control characters before it is shown to the user.
function boundedRefusal(value: string): string {
  return value.replace(/[\u0000-\u001f]+/g, " ").trim().slice(0, 200);
}
