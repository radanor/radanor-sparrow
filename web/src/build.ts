// The build identity the About area shows.
//
// Both values come from the core's foundation response, which reports the
// metadata the executable was built with (`internal/branding`), so the interface
// never invents a version and never presents a development build as a release.
// The variant values are the ones internal/branding declares, and a Go test
// there fails if this file drifts from them.

export type FoundationBuild = {
  version: string;
  // Additive: a core older than the build identity omits both, and the About
  // area then shows the version alone.
  variant?: string;
  build_label?: string;
};

// buildVariant returns the variant the core reports, or an empty string when it
// reports none. Nothing here assesses the variant: an unrecognised value is
// treated as no claim rather than as a release.
export function buildVariant(foundation: FoundationBuild): string {
  return (foundation.variant ?? "").trim().toLowerCase();
}

// buildVariantLabel is the short name shown under the version. A build the core
// did not label as a release is named a development build; a core that reports
// no variant adds no claim at all.
export function buildVariantLabel(foundation: FoundationBuild): string {
  switch (buildVariant(foundation)) {
    case "release":
      return "Release build";
    case "development":
      return "Development build";
    default:
      return "";
  }
}

// buildIdentity is the core's own description of the build — the sentence the
// build log and the Windows version resource also carry — for the places that
// want the whole line rather than the short name.
export function buildIdentity(foundation: FoundationBuild): string {
  return (foundation.build_label ?? "").trim();
}
