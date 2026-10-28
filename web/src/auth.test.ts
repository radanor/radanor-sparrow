import { describe, expect, it } from "vitest";
import { consumeLaunchTicket } from "./auth";

describe("launch ticket handling", () => {
  it("removes the fragment before returning the ticket", () => {
    let replaced = "";
    const ticket = "a".repeat(43);
    expect(consumeLaunchTicket({ hash: `#ticket=${ticket}`, pathname: "/", search: "" } as Location, (url) => { replaced = url; })).toBe(ticket);
    expect(replaced).toBe("/");
  });

  it("rejects missing and malformed tickets", () => {
    expect(consumeLaunchTicket({ hash: "#ticket=short", pathname: "/", search: "" } as Location, () => undefined)).toBeNull();
  });
});
