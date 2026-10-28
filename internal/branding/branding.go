// Package branding holds the public product identity. Every user-visible name
// the application prints comes from here, so a rebrand is one edit plus the
// regenerated dashboard rather than a search-and-replace.
//
// The identifiers deliberately *not* here — the Go module path, the IPC
// schema version strings, the Windows mutex and pipe names, and the private
// state directory — are compatibility-sensitive and documented in
// the private engineering record.
package branding

const (
	Company     = "Radanor"
	Product     = "Radanor Sparrow"
	Edition     = "Community Edition"
	DisplayName = Product + " " + Edition
	Slug        = "radanor-sparrow"

	// The product version deliberately does not live here. It is derived from
	// the checked-out commit once per build and injected into Version, which is
	// declared in version.go together with the build variant. Editing a number
	// into the source is what the release process must never have to do.

	// Copyright holder. The Business Source License names an individual, not the
	// brand: Radanor is a product name and not a legal person, so these strings
	// name the holder and never the company. branding_test.go keeps the holder
	// equal to the Licensor in LICENSE, and internal/winres writes
	// CopyrightNotice into the Windows file properties as LegalCopyright.
	CopyrightHolder = "Radheshyam Dangi"
	CopyrightYear   = "2026"
	CopyrightNotice = "© " + CopyrightYear + " " + CopyrightHolder

	// Licensing identity. The dashboard states its license from here so the
	// product cannot ship with no licensing information at all. These are a
	// user-facing summary only: the authoritative terms are in LICENSE, and the
	// explanation of permitted uses is in docs/licensing/LICENSING.md. The
	// wording is deliberate — before a version's Change Date the product is
	// source-available and must not be described as open source.
	LicenseName   = "Business Source License 1.1"
	LicenseNotice = "Source-available under Business Source License 1.1. Free personal and internal organizational use is permitted by the Additional Use Grant; other uses may require commercial permission."
	LicenseURL    = "https://mariadb.com/bsl11/"

	// Approved brand artwork. These are the supplied Radanor files in
	// web/public/, which the Vite build copies verbatim into the embedded
	// dashboard. They are never redrawn, recoloured or cropped; where the surface
	// behind them is dark, the shell gives the unmodified artwork its own neutral
	// plaque instead. Naming them here makes a swapped or missing logo a test
	// failure rather than a silent regression.
	EmblemAsset  = "radanor-emblem-light.png"
	LogoAsset    = "radanor-logo-light.png"
	FaviconAsset = "favicon.ico"

	// Distribution artifact names. The documented build scripts and CI must
	// produce exactly these, and internal/branding/branding_test.go fails if
	// they drift.
	WindowsExecutable = "RadanorSparrow.exe"
	LinuxExecutable   = "radanor-sparrow"
)
