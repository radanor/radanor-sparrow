// The approved Radanor brand artwork.
//
// The files live in web/public/ and the Vite build (vite.config.ts ->
// internal/gateway/assets) copies them into the embedded dashboard verbatim, so
// the local gateway serves them from the site root. internal/branding names the
// same files and internal/branding/branding_test.go fails if the Go identity and
// this module ever disagree.
//
// The supplied artwork is the light-background variant: dark navy ink on a
// transparent background. It is never recoloured, filtered, cropped or
// stretched. Where the surface behind it is dark, the shell places the
// unmodified artwork on its own neutral plaque (styles.css, --brand-surface)
// rather than changing the logo.
export const brand = {
  assets: {
    emblem: "/radanor-emblem-light.png",
    logo: "/radanor-logo-light.png",
    favicon: "/favicon.ico"
  },
  // The intrinsic pixel dimensions of the supplied files, mirrored exactly. They
  // are rendered as width/height attributes so the shell does not reflow while
  // the artwork loads. The stylesheet scales by height alone, which preserves
  // the original proportions.
  emblemSize: { width: 167, height: 135 },
  logoSize: { width: 527, height: 135 }
} as const;
