import { afterEach, describe, expect, it, vi } from "vitest";

afterEach(() => { vi.unstubAllGlobals(); vi.resetModules(); });

describe("session cookie and tab CSRF synchronization", () => {
  it("does not repeat network failures or accepted mutations with unreadable responses", async () => {
    for (const mode of ["network", "accepted", "accepted-invalid-json"]) {
      vi.resetModules();
      const auth = await import("./auth");
      let attempts = 0;
      vi.stubGlobal("fetch", async () => {
        attempts++;
        if (mode === "network") throw new TypeError("network failure");
        if (mode === "accepted-invalid-json") return new Response("invalid", { status: 200 });
        return Response.json({ accepted: true });
      });
      if (mode === "accepted") await auth.postJSON("/api/v1/assessment/scan");
      else await expect(auth.postJSON("/api/v1/assessment/scan")).rejects.toThrow();
      expect(attempts === 1).toBe(true);
    }
  });

  it("shares one recovery across concurrent requests in a stale tab", async () => {
    vi.resetModules();
    const auth = await import("./auth");
    const current = "e".repeat(43);
    let recoveries = 0;
    let attempts = 0;
    let accepted = 0;
    vi.stubGlobal("fetch", async (path: string, init?: RequestInit) => {
      if (path === "/api/v1/session/csrf") {
        recoveries++;
        await new Promise(resolve => setTimeout(resolve, 10));
        return Response.json({ csrf_token: current });
      }
      attempts++;
      if (new Headers(init?.headers).get("X-CSRF-Token") !== current) return new Response("authentication required", { status: 401 });
      accepted++;
      return Response.json({ accepted: true });
    });
    await Promise.all([auth.postJSON("/api/v1/settings"), auth.postJSON("/api/v1/settings")]);
    expect(recoveries === 1 && attempts === 4 && accepted === 2).toBe(true);
  });

  it("keeps independent tabs synchronized after cookie rotation without retrying the accepted tab", async () => {
    vi.resetModules();
    const olderTab = await import("./auth");
    vi.resetModules();
    const newerTab = await import("./auth");
    let current = "f".repeat(43);
    let exchanges = 0;
    let recoveries = 0;
    let attempts = 0;
    let accepted = 0;
    vi.stubGlobal("fetch", async (path: string, init?: RequestInit) => {
      if (path === "/api/v1/session/exchange") {
        exchanges++;
        current = (exchanges === 1 ? "f" : "g").repeat(43);
        return Response.json({ csrf_token: current });
      }
      if (path === "/api/v1/session/csrf") { recoveries++; return Response.json({ csrf_token: current }); }
      attempts++;
      if (new Headers(init?.headers).get("X-CSRF-Token") !== current) return new Response("authentication required", { status: 401 });
      accepted++;
      return Response.json({ accepted: true });
    });
    await olderTab.exchangeTicket("h".repeat(43));
    await newerTab.exchangeTicket("i".repeat(43));
    await Promise.all([olderTab.postJSON("/api/v1/settings"), newerTab.postJSON("/api/v1/settings")]);
    expect(recoveries === 1 && attempts === 3 && accepted === 2).toBe(true);
  });
  it("recovers an old tab after another launch rotates the shared cookie", async () => {
    vi.resetModules();
    const auth = await import("./auth");
    const first = "a".repeat(43);
    const current = "b".repeat(43);
    let scans = 0;
    let recovered = false;
    vi.stubGlobal("fetch", async (path: string, init?: RequestInit) => {
      if (path === "/api/v1/session/exchange") return Response.json({ csrf_token: first });
      if (path === "/api/v1/session/csrf") {
        recovered = init?.credentials === "same-origin";
        return Response.json({ csrf_token: current });
      }
      if (path === "/api/v1/assessment/scan") {
        scans++;
        const matches = new Headers(init?.headers).get("X-CSRF-Token") === current;
        return matches ? Response.json({ accepted: true }) : new Response("authentication required", { status: 401 });
      }
      throw new Error("unexpected API request");
    });
    await auth.exchangeTicket("t".repeat(43));
    const result = await auth.postJSON("/api/v1/assessment/scan") as { accepted: boolean };
    expect(result.accepted && recovered && scans === 2).toBe(true);
  });

  it("does not retry a state-changing request for a non-authentication failure", async () => {
    vi.resetModules();
    const auth = await import("./auth");
    let scans = 0;
    vi.stubGlobal("fetch", async () => { scans++; return new Response("busy", { status: 409 }); });
    await expect(auth.postJSON("/api/v1/assessment/scan")).rejects.toThrow("busy");
    expect(scans === 1).toBe(true);
  });

  it("bootstraps a reload once and shares initialization across repeated effects", async () => {
    vi.resetModules();
    const auth = await import("./auth");
    const current = "c".repeat(43);
    let recovered = 0;
    vi.stubGlobal("fetch", async (path: string, init?: RequestInit) => {
      if (path === "/api/v1/session/csrf") { recovered++; return Response.json({ csrf_token: current }); }
      const matches = new Headers(init?.headers).get("X-CSRF-Token") === current;
      return Response.json({ accepted: matches });
    });
    await Promise.all([auth.initializeSession(null),auth.initializeSession(null)]);
    const result = await auth.postJSON("/api/v1/assessment/scan") as { accepted: boolean };
    expect(recovered === 1 && result.accepted).toBe(true);
  });

  it("does not recover expired authentication or retry a second unauthorized result", async () => {
    for (const validSession of [false,true]) {
      vi.resetModules();
      const auth = await import("./auth");
      let attempts = 0;
      let recoveries = 0;
      vi.stubGlobal("fetch", async (path: string) => {
        if (path === "/api/v1/session/csrf") {
          recoveries++;
          return validSession ? Response.json({ csrf_token: "d".repeat(43) }) : new Response("authentication required",{ status:401 });
        }
        attempts++;
        return new Response("authentication required",{ status:401 });
      });
      await expect(auth.postJSON("/api/v1/assessment/scan")).rejects.toThrow();
      expect(recoveries === 1 && attempts === (validSession ? 2 : 1)).toBe(true);
    }
  });
});
