package branding

import (
	"strings"
	"testing"
)

// The default must stay a development build. If the source ever shipped a
// release variant by default, a plain `go build` or `go test` would produce
// something that presents itself as an approved release, which is exactly what
// the build-time injection exists to prevent.
func TestSourceDefaultsToDevelopment(t *testing.T) {
	if Variant != VariantDevelopment {
		t.Errorf("default variant = %q, want %q", Variant, VariantDevelopment)
	}
	if Version != DevelopmentVersion {
		t.Errorf("default version = %q, want %q", Version, DevelopmentVersion)
	}
	if IsRelease() {
		t.Error("the source default reports itself as a release build")
	}
	if VariantRelease == VariantDevelopment {
		t.Error("the two build variants are the same string")
	}
}

func TestBuildLabelDistinguishesDevelopmentFromRelease(t *testing.T) {
	for _, want := range []struct {
		variant string
		version string
		expect  []string
		reject  []string
	}{
		{
			variant: VariantDevelopment,
			version: "0.1.0-dev.0+gcc81e6633f55",
			expect:  []string{DisplayName, "0.1.0-dev.0+gcc81e6633f55", "development"},
			reject:  []string{"(release)"},
		},
		{
			variant: VariantRelease,
			version: "1.4.0",
			expect:  []string{DisplayName, "1.4.0", "release"},
			reject:  []string{"development"},
		},
	} {
		setVersion(t, want.version, want.variant)
		label := BuildLabel()
		for _, needle := range want.expect {
			if !strings.Contains(label, needle) {
				t.Errorf("BuildLabel() = %q, want it to contain %q", label, needle)
			}
		}
		for _, needle := range want.reject {
			if strings.Contains(label, needle) {
				t.Errorf("BuildLabel() = %q, must not contain %q", label, needle)
			}
		}
	}
}

// A blank injected value must not produce a label that names no version at all.
func TestBuildLabelFallsBackToTheDevelopmentVersion(t *testing.T) {
	setVersion(t, "   ", VariantDevelopment)
	if label, semver := BuildLabel(), Semver(); !strings.Contains(label, DevelopmentVersion) || semver != DevelopmentVersion {
		t.Errorf("BuildLabel() = %q and Semver() = %q, want both to fall back to %q", label, semver, DevelopmentVersion)
	}
}

// VS_FIXEDFILEINFO holds four 16-bit integers, so the Windows numeric version
// is taken from the numeric part of the version and every none-numeric input
// degrades to 0.0.0.0 rather than to a wrong number.
func TestNumericVersionTakesTheNumericFieldsOnly(t *testing.T) {
	for _, want := range []struct {
		version string
		expect  [4]uint16
	}{
		{"0.1.0-dev.0+gcc81e6633f55", [4]uint16{0, 1, 0, 0}},
		{"0.1.0-dev.0+gcc81e6633f55.dirty", [4]uint16{0, 1, 0, 0}},
		{"1.4.0", [4]uint16{1, 4, 0, 0}},
		{"2.4", [4]uint16{2, 4, 0, 0}},
		{"1.2.3.4", [4]uint16{1, 2, 3, 4}},
		{"1.2.3.4.5", [4]uint16{1, 2, 3, 4}},
		{"12.34.0+build.7", [4]uint16{12, 34, 0, 0}},
		// Not numeric, so no number is invented.
		{"not-a-version", [4]uint16{}},
		{"1.0.0-rc.1-x", [4]uint16{1, 0, 0, 0}},
		{"", [4]uint16{0, 1, 0, 0}},
		// 70000 does not fit a uint16, so the whole value degrades.
		{"70000.0.0", [4]uint16{}},
	} {
		setVersion(t, want.version, VariantDevelopment)
		if got := NumericVersion(); got != want.expect {
			t.Errorf("NumericVersion() for %q = %v, want %v", want.version, got, want.expect)
		}
	}
}

// The product's licence wording rule applies to the build label too: while the
// BSL applies, nothing user-visible may call it open source.
func TestBuildLabelNeverCallsTheProductOpenSource(t *testing.T) {
	for _, variant := range []string{VariantDevelopment, VariantRelease} {
		setVersion(t, "1.0.0", variant)
		lower := strings.ToLower(BuildLabel())
		for _, forbidden := range []string{"open source", "open-source", "osi"} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("BuildLabel() = %q, must not describe the product as %q", BuildLabel(), forbidden)
			}
		}
	}
}

// The Windows resource generator is handed the build's version, so the numeric
// fields must come from that version rather than from the version the source
// default carries. A tag-derived version must not report the development base.
func TestNumericVersionOfUsesTheGivenVersion(t *testing.T) {
	for _, want := range []struct {
		version string
		expect  [4]uint16
	}{
		{"0.3.0-dev.7+g19f4c2ab77e1", [4]uint16{0, 3, 0, 0}},
		{"2.1.9", [4]uint16{2, 1, 9, 0}},
		{"  1.0.0  ", [4]uint16{1, 0, 0, 0}},
		{"0.1.0-dev.0+gcc81e6633f55.dirty", [4]uint16{0, 1, 0, 0}},
	} {
		if got := NumericVersionOf(want.version); got != want.expect {
			t.Errorf("NumericVersionOf(%q) = %v, want %v", want.version, got, want.expect)
		}
	}
}

func setVersion(t *testing.T, version, variant string) {
	t.Helper()
	previousVersion, previousVariant := Version, Variant
	t.Cleanup(func() { Version, Variant = previousVersion, previousVariant })
	Version, Variant = version, variant
}
