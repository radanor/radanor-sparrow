import { describe, expect, it } from "vitest";
import { buildIdentity, buildVariant, buildVariantLabel } from "./build";

// The values below are the shapes the gateway actually serves: the version is
// derived from the checked-out commit at build time, and the variant says
// whether that build came from the release path.
const development = {
  version: "0.1.0-dev.0+gcc81e6633f55",
  variant: "development",
  build_label: "Radanor Sparrow Community Edition 0.1.0-dev.0+gcc81e6633f55 (development build, not a release)"
};

const release = {
  version: "2.0.0",
  variant: "release",
  build_label: "Radanor Sparrow Community Edition 2.0.0 (release)"
};

describe("build identity", () => {
  it("names a development build as a development build, never as a release", () => {
    const label = buildVariantLabel(development);
    expect(label).toBe("Development build");
    expect(label.toLowerCase()).not.toContain("release");
  });

  it("names a release build as a release build", () => {
    expect(buildVariantLabel(release)).toBe("Release build");
    expect(buildVariantLabel(release).toLowerCase()).not.toContain("development");
  });

  it("reports the variant exactly as the core sent it", () => {
    expect(buildVariant(development)).toBe("development");
    expect(buildVariant(release)).toBe("release");
    // The core is the only source: a differently cased value is still the same
    // variant, and nothing here promotes it to a release.
    expect(buildVariant({ version: "1.0.0", variant: "  RELEASE " })).toBe("release");
  });

  it("adds no claim when an older core reports no variant", () => {
    const legacy = { version: "0.1.0-dev" };
    expect(buildVariant(legacy)).toBe("");
    expect(buildVariantLabel(legacy)).toBe("");
    expect(buildIdentity(legacy)).toBe("");
    // The version itself is still shown, so the user loses no information.
    expect(legacy.version).toBe("0.1.0-dev");
  });

  it("makes no release claim for a variant it does not recognise", () => {
    expect(buildVariantLabel({ version: "1.0.0", variant: "nightly" })).toBe("");
  });

  it("passes the core's own build line through unchanged", () => {
    expect(buildIdentity(development)).toBe(development.build_label);
    expect(buildIdentity(release)).toBe(release.build_label);
  });
});
