package branding

import "strings"

// Build variant. The product is distributed as development builds until a
// public release is approved, and the licensing release gate keeps refusing to
// produce a release artifact while the BSL parameters are unresolved. A
// development package must therefore be distinguishable from an approved
// release package *after* it leaves the build log, because the two otherwise
// look identical on disk. These identifiers are carried by the version string,
// the Windows version resource and the dashboard, and the build scripts set
// them explicitly.
const (
	// VariantDevelopment is every ordinary build, including local `go build`.
	VariantDevelopment = "development"
	// VariantRelease is set only by the explicit release path
	// (`scripts/build-linux.sh --release`, `scripts/build-windows.ps1
	// -Release`), and only after the licensing release gate has passed and an
	// exact version tag has been checked out. Nothing else may set it.
	VariantRelease = "release"
)

// DevelopmentBase is the version line this source tree develops on. It is the
// base for an untagged build; the first `vX.Y.Z` tag replaces it, and no
// unreleased number is ever presented as a release.
const DevelopmentBase = "0.1.0"

// DevelopmentVersion is what a build reports when no version tag exists yet.
// It is deliberately a pre-release identifier, so a build of this tree can
// never be mistaken for a released version.
const DevelopmentVersion = DevelopmentBase + "-dev"

// Version is the product version, and Variant says what kind of build produced
// it. Both are variables rather than constants on purpose: the build scripts
// set them with `-ldflags -X`, so a version is derived from the checked-out
// commit once per build instead of being edited into the source. The defaults
// are the development values, which means a plain `go build`, `go test` or
// `go run` never claims to be a release.
//
// `-X` only accepts string variables, so neither of these may become a
// constant without losing build-time injection.
var (
	Version = DevelopmentVersion
	Variant = VariantDevelopment
)

// IsRelease reports whether this build came from the release path. It is a
// label, not an authorization: it is only ever set by a build that has already
// passed the licensing release gate.
func IsRelease() bool { return Variant == VariantRelease }

// BuildLabel is the single user-visible string that names the exact build. The
// dashboard, the build logs and the Windows version resource all use it, so a
// development artifact and a release artifact read differently wherever one can
// be seen.
func BuildLabel() string {
	version := strings.TrimSpace(Version)
	if version == "" {
		version = DevelopmentVersion
	}
	if IsRelease() {
		return DisplayName + " " + version + " (release)"
	}
	return DisplayName + " " + version + " (development build, not a release)"
}

// Semver returns just the version identifier, for surfaces that already carry
// the product name (the dashboard's foundation response, artifact filenames,
// the Windows version resource's string fields).
func Semver() string {
	version := strings.TrimSpace(Version)
	if version == "" {
		return DevelopmentVersion
	}
	return version
}

// NumericVersion returns the four-part numeric version the Windows version
// resource requires, taken from the version this build carries.
func NumericVersion() [4]uint16 {
	return NumericVersionOf(Semver())
}

// NumericVersionOf is NumericVersion for a version the caller already has, so a
// tool that was handed a version on its command line — the Windows resource
// generator, which is given the version the build derived from the commit —
// does not silently fall back to the number the source default carries.
//
// `VS_FIXEDFILEINFO` stores a version as four 16-bit integers, so neither a
// pre-release suffix nor build metadata can be represented there; those belong
// in the resource's string fields, which carry Semver. Any suffix is dropped
// rather than truncated into a wrong number, and a version that is not numeric
// at all falls back to 0.0.0.0 instead of guessing.
func NumericVersionOf(version string) [4]uint16 {
	var out [4]uint16
	core := strings.TrimSpace(version)
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	for index, field := range strings.Split(core, ".") {
		if index > 3 {
			break
		}
		value := 0
		for _, char := range field {
			if char < '0' || char > '9' {
				value = -1
				break
			}
			value = value*10 + int(char-'0')
			if value > 65535 {
				value = -1
				break
			}
		}
		if value < 0 {
			return [4]uint16{}
		}
		out[index] = uint16(value)
	}
	return out
}
