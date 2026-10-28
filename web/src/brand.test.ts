import { describe, expect, it } from "vitest";
import { brand } from "./brand";

// The artwork itself is pinned by digest in the Go test that reads the shipped
// bundle (internal/gateway/brand_assets_test.go). This file checks the interface
// the shell renders, so a wrong, duplicated or off-origin path fails before the
// bundle is built.
describe("approved brand artwork", () => {
  it("is served by the application's own origin and never by a third party", () => {
    for (const path of Object.values(brand.assets)) {
      expect(path.startsWith("/")).toBe(true);
      expect(path).not.toContain("://");
      expect(path).not.toContain("//");
      expect(path).not.toContain("..");
    }
  });

  it("names exactly the supplied files", () => {
    expect(brand.assets).toEqual({
      emblem: "/radanor-emblem-light.png",
      logo: "/radanor-logo-light.png",
      favicon: "/favicon.ico"
    });
    expect(new Set(Object.values(brand.assets)).size).toBe(3);
  });

  it("records the supplied dimensions and their shared proportions", () => {
    expect(brand.emblemSize).toEqual({ width: 167, height: 135 });
    expect(brand.logoSize).toEqual({ width: 527, height: 135 });
    // The emblem and the wordmark are the same lockup: both supplied files are
    // 135 px tall. A different ratio would mean artwork that was cropped or
    // stretched, which is not permitted.
    expect(brand.logoSize.height).toBe(brand.emblemSize.height);
    expect(brand.logoSize.width).toBeGreaterThan(brand.emblemSize.width);
  });
});
