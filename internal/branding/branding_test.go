package branding

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A rebrand touches four places that cannot import each other: this Go package,
// the TypeScript product descriptor, the generated dashboard bundle, and the
// shell/PowerShell build scripts. Nothing else notices when one of them still
// says the old name, so these tests read all four and compare them.

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found above the test's working directory")
		}
		dir = parent
	}
}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(append([]string{repoRoot(t)}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestIdentityIsTheSparrowRebrand(t *testing.T) {
	if Company != "Radanor" {
		t.Fatalf("company = %q", Company)
	}
	if Product != "Radanor Sparrow" {
		t.Errorf("product = %q, want %q", Product, "Radanor Sparrow")
	}
	if Edition != "Community Edition" {
		t.Errorf("edition = %q, want %q", Edition, "Community Edition")
	}
	if DisplayName != "Radanor Sparrow Community Edition" {
		t.Errorf("display name = %q, want %q", DisplayName, "Radanor Sparrow Community Edition")
	}
	if Slug != "radanor-sparrow" {
		t.Errorf("slug = %q, want %q", Slug, "radanor-sparrow")
	}
	if WindowsExecutable != "RadanorSparrow.exe" {
		t.Errorf("windows executable = %q", WindowsExecutable)
	}
	if LinuxExecutable != "radanor-sparrow" {
		t.Errorf("linux executable = %q", LinuxExecutable)
	}
	// A stale identifier is the failure this whole file exists to catch.
	for name, value := range map[string]string{
		"Product": Product, "Edition": Edition, "DisplayName": DisplayName,
		"Slug": Slug, "WindowsExecutable": WindowsExecutable, "LinuxExecutable": LinuxExecutable,
	} {
		if strings.Contains(strings.ToLower(value), "shield") {
			t.Errorf("%s still contains the pre-rebrand name: %q", name, value)
		}
	}
}

// TestFrontendIdentityMatchesBranding fails when the dashboard was renamed and
// the Go identity was not, or the other way round.
func TestFrontendIdentityMatchesBranding(t *testing.T) {
	source := readRepoFile(t, "web", "src", "product.ts")
	for _, want := range []struct {
		field string
		value string
	}{
		{"company", Company},
		{"name", Product},
		{"edition", Edition},
		{"displayName", DisplayName},
	} {
		pattern := regexp.MustCompile(`\b` + want.field + `:\s*"([^"]*)"`)
		match := pattern.FindStringSubmatch(source)
		if match == nil {
			t.Errorf("web/src/product.ts has no %s field", want.field)
			continue
		}
		if match[1] != want.value {
			t.Errorf("web/src/product.ts %s = %q, want %q (from internal/branding)", want.field, match[1], want.value)
		}
	}
}

// The dashboard names the build variant itself — it must say "Development
// build" or "Release build" in words — so the two variant names are pinned here
// against the Go constants, the same way the product name is pinned. Renaming a
// variant in Go without updating the dashboard (or the reverse) fails this test
// rather than silently labelling an approved release a development build.
func TestDashboardNamesTheSameBuildVariants(t *testing.T) {
	if VariantDevelopment == VariantRelease {
		t.Fatal("the two build variants are the same string")
	}
	source := readRepoFile(t, "web", "src", "build.ts")
	for _, want := range []struct {
		variant string
		label   string
	}{
		{VariantRelease, "Release build"},
		{VariantDevelopment, "Development build"},
	} {
		if !strings.Contains(source, `case "`+want.variant+`":`) {
			t.Errorf("web/src/build.ts does not recognise the variant %q from internal/branding", want.variant)
		}
		if !strings.Contains(source, `return "`+want.label+`";`) {
			t.Errorf("web/src/build.ts does not name the %q variant %q", want.variant, want.label)
		}
	}
}

// The About area shows the version and the variant of the build it is running
// in, and the core reports the metadata that was injected into that executable.
// This reads the built distribution and requires both strings to be present in
// it, so the identity on screen is the identity the artifact carries: a build
// that forgot to inject a version, or a variant the linker dropped because
// nothing referenced it, fails here.
func TestDistributionCarriesItsInjectedBuildIdentity(t *testing.T) {
	binary := os.Getenv("RADANOR_SPARROW_TEST_BINARY")
	if binary == "" {
		t.Skip("set RADANOR_SPARROW_TEST_BINARY to the built distribution executable")
	}
	executable, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	version := runVersionRule(t)
	if !bytes.Contains(executable, []byte(version)) {
		t.Errorf("%s does not carry the version the rule derives (%q), so the About area would show a version the executable does not have", binary, version)
	}
	variant := runVersionRule(t, "-field", "variant")
	if !bytes.Contains(executable, []byte(variant)) {
		t.Errorf("%s does not carry the injected build variant %q", binary, variant)
	}
	// The label is what the About area can show, so its wording has to ship
	// inside the executable too.
	for _, needle := range []string{"development build, not a release", "(release)"} {
		if !bytes.Contains(executable, []byte(needle)) {
			t.Errorf("%s does not carry the build label wording %q", binary, needle)
		}
	}
}

// runVersionRule runs cmd/radanor-version, so the expectation is derived from the
// checkout rather than hardcoded to one commit. It is the same rule both build
// scripts use to stamp an artifact.
func runVersionRule(t *testing.T, args ...string) string {
	t.Helper()
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("the go toolchain is not on PATH")
	}
	command := exec.Command(goTool, append([]string{"run", "./cmd/radanor-version"}, args...)...)
	command.Dir = repoRoot(t)
	command.Env = append(os.Environ(), "GOFLAGS=", "GOPROXY=off", "GOTOOLCHAIN=local")
	out, err := command.Output()
	if err != nil {
		t.Fatalf("the version rule could not be run: %v", err)
	}
	if value := strings.TrimSpace(string(out)); value != "" {
		return value
	}
	t.Fatal("the version rule printed nothing")
	return ""
}

// The About area can only name the build variant if the dashboard bundle that is
// actually served carries that wording. Reading web/src would pass even with a
// stale bundle in internal/gateway/assets, so this reads the committed bundle.
func TestEmbeddedDashboardCanNameTheBuildVariant(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join(repoRoot(t), "internal", "gateway", "assets", "assets", "*.js"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("the embedded dashboard has no JavaScript bundle")
	}
	var bundle strings.Builder
	for _, match := range matches {
		content, err := os.ReadFile(match)
		if err != nil {
			t.Fatal(err)
		}
		bundle.Write(content)
	}
	// The field names are the ones the gateway serves; the two labels are the
	// words a user reads.
	for _, want := range []string{"variant", "build_label", "Development build", "Release build"} {
		if !strings.Contains(bundle.String(), want) {
			t.Errorf("the embedded dashboard bundle cannot show %q, so the About area would state no build variant", want)
		}
	}
}

// TestEmbeddedDashboardCarriesTheProductName reads the committed build output
// that is actually served by the distribution.
func TestEmbeddedDashboardCarriesTheProductName(t *testing.T) {
	index := readRepoFile(t, "internal", "gateway", "assets", "index.html")
	if !strings.Contains(index, "<title>"+DisplayName+"</title>") {
		t.Errorf("embedded dashboard title does not carry %q", DisplayName)
	}
	if strings.Contains(index, "Shield") {
		t.Error("embedded dashboard index.html still names the old product")
	}
}

func TestBuildScriptsProduceTheBrandedArtifacts(t *testing.T) {
	for _, want := range []struct {
		file  string
		parts []string
	}{
		{"scripts/build-windows.ps1", []string{WindowsExecutable, "./cmd/radanor-sparrow"}},
		{"scripts/build-linux.sh", []string{LinuxExecutable, "./cmd/radanor-sparrow"}},
		{filepath.Join(".github", "workflows", "ci.yml"), []string{WindowsExecutable, LinuxExecutable}},
		{"scripts/install-linux.sh", []string{LinuxExecutable, Product}},
	} {
		content := readRepoFile(t, strings.Split(want.file, string(filepath.Separator))...)
		for _, needle := range want.parts {
			if !strings.Contains(content, needle) {
				t.Errorf("%s does not reference %q", want.file, needle)
			}
		}
		if strings.Contains(content, "Shield") {
			t.Errorf("%s still names the old product", want.file)
		}
	}
}

// TestLicenseSurfaceIsSourceAvailableNotOpenSource pins the wording the product
// may use about itself. Before a version's Change Date the Business Source
// License is not an open-source license, so nothing user-facing may say it is.
func TestLicenseSurfaceIsSourceAvailableNotOpenSource(t *testing.T) {
	if LicenseName != "Business Source License 1.1" {
		t.Errorf("license name = %q", LicenseName)
	}
	if LicenseURL != "https://mariadb.com/bsl11/" {
		t.Errorf("license url = %q", LicenseURL)
	}
	for _, want := range []string{"Source-available", "Business Source License 1.1", "Additional Use Grant", "commercial permission"} {
		if !strings.Contains(LicenseNotice, want) {
			t.Errorf("license notice does not mention %q: %q", want, LicenseNotice)
		}
	}
	for _, forbidden := range []string{"open source", "open-source", "OSI"} {
		if strings.Contains(strings.ToLower(LicenseNotice), strings.ToLower(forbidden)) {
			t.Errorf("license notice describes the product as %q, which is false while the BSL applies: %q", forbidden, LicenseNotice)
		}
	}
}

// The copyright holder is an individual named by the licence, and the same name
// has to appear in the Windows file properties as LegalCopyright. The check
// reads the Licensor straight out of LICENSE instead of comparing two copies of
// the name, so the two cannot drift apart.
func TestCopyrightNoticeNamesTheLicensorInLicense(t *testing.T) {
	// The file is read line by line rather than through normalizeLicenseText,
	// which joins the whole licence onto one line and would make the parameter
	// indistinguishable from the prose around it.
	licensor := ""
	for _, line := range strings.Split(readRepoFile(t, "LICENSE"), "\n") {
		if value, found := strings.CutPrefix(strings.TrimSpace(line), "Licensor:"); found {
			licensor = strings.TrimSpace(value)
		}
	}
	if licensor == "" {
		t.Fatal("LICENSE states no Licensor")
	}
	if CopyrightHolder != licensor {
		t.Errorf("copyright holder = %q, LICENSE licensor = %q", CopyrightHolder, licensor)
	}
	if CopyrightYear != "2026" {
		t.Errorf("copyright year = %q, want 2026", CopyrightYear)
	}
	if CopyrightNotice != "© 2026 "+licensor {
		t.Errorf("copyright notice = %q, want the holder named in LICENSE", CopyrightNotice)
	}
	if strings.Contains(strings.ToLower(CopyrightNotice), "radanor") {
		t.Errorf("copyright notice %q names the brand, but the licence names an individual holder", CopyrightNotice)
	}
}

// TestLicenseFileCarriesTheOfficialTextAndParameters reads LICENSE and checks the
// standardized Business Source License 1.1 text and every parameter the task
// required. It fails if the license is reworded, if a parameter is dropped, or if
// a bracketed placeholder is quietly replaced with a guessed legal entity.
func TestLicenseFileCarriesTheOfficialTextAndParameters(t *testing.T) {
	license := normalizeLicenseText(readRepoFile(t, "LICENSE"))
	for _, want := range []struct{ needle, why string }{
		{"Business Source License 1.1", "the license title"},
		{"License text copyright © 2024 MariaDB plc", "the standardized license-text copyright line, unmodified"},
		{"\"Business Source License\" is a trademark of MariaDB plc.", "the standardized trademark line"},
		{"Parameters", "the parameter block heading"},
		{"Licensor:", "the Licensor parameter"},
		{"Licensed Work:", "the Licensed Work parameter"},
		{"Additional Use Grant:", "the Additional Use Grant parameter"},
		{"Change Date:", "the Change Date parameter"},
		{"Change License:", "the Change License parameter"},
		{"Radanor Sparrow Community Edition", "the Licensed Work name"},
		{"GNU General Public License, version 3.0", "the requested Change License"},
		{"The Licensor hereby grants you the right to copy, modify, create derivative", "the verbatim Terms grant"},
		{"Effective on the Change Date, or the fourth anniversary of the first publicly", "the verbatim changeover clause"},
		{"Covenants of Licensor", "the standardized covenant heading"},
		{"Not to modify this License in any other way.", "covenant 4, unmodified"},
		{"The Business Source License (this document, or the \"License\") is not an Open", "the verbatim Notice"},
		{"Four years is the standard maximum", "the standard four-year maximum explanation"},
		{"This Additional Use Grant only adds permissions.", "the statement that the grant adds and does not restrict rights"},
		{"does not restrict or reduce any right granted to you in the Terms below", "the non-restriction clause required by the BSL covenant"},
		{"copy, modify, create derivative works, redistribute, and make non-production use of the Licensed Work", "the BSL grant the Additional Use Grant may not restrict"},
	} {
		if !strings.Contains(license, want.needle) {
			t.Errorf("LICENSE is missing %s (%q)", want.why, want.needle)
		}
	}
	// While the license is unresolved the placeholder may remain, but it must be
	// clearly marked: a silent placeholder is the failure. Once the licensing
	// release gate is satisfied the placeholders are gone, so these checks are
	// conditional rather than absolute. Whether the release gate is satisfied is
	// enforced by internal/releasecheck and `go run ./cmd/radanor-release-check`.
	if strings.Contains(license, "COPYRIGHT HOLDER") && !strings.Contains(license, "[RELEASE BLOCKER: the Licensor must be the legally verified copyright holder") {
		t.Error("a Licensor placeholder must carry its release-blocker marker, and must not be silently replaced with a guessed entity")
	}
	if strings.Contains(license, "RELEASE-SPECIFIC CHANGE DATE") && !strings.Contains(license, "NO VERSION HAS BEEN PUBLICLY RELEASED YET, SO NO CALENDAR DATE IS RECORDED HERE") {
		t.Error("a Change Date placeholder must state that no version has shipped and no calendar date is recorded")
	}
}

// TestLicensorPlaceholderOutlivesThePendingDecision enforces the release-decision
// instruction: while the private release decision log still records the
// licensor decision as pending, the LICENSE must keep the legal-name placeholder.
// An unverified company or individual must not be written in. Once the decision is
// resolved, this guard steps aside on its own.
func TestLicensorPlaceholderOutlivesThePendingDecision(t *testing.T) {
	content, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "licensing", "RELEASE_DECISIONS.md"))
	if os.IsNotExist(err) {
		// The decision log is an internal record and is not published with the source,
		// so this guard has nothing to read here. It still runs in the private
		// repository that holds the log.
		t.Skip("the licensor decision log is not part of this repository; nothing records the decision as pending")
	}
	if err != nil {
		t.Fatal(err)
	}
	decisions := string(content)
	if !strings.Contains(decisions, "Status: PENDING") {
		t.Skip("the licensor decision is no longer pending; the placeholder is no longer required")
	}
	license := readRepoFile(t, "LICENSE")
	if !strings.Contains(license, "COPYRIGHT HOLDER") {
		t.Error("the licensor decision is still PENDING in the private release decision log, so LICENSE must keep the legal-name placeholder and must not name an unverified entity")
	}
	if strings.Contains(license, "Radanor Private Limited") || strings.Contains(license, "Koshaliya Dangi") {
		t.Error("an unverified candidate Licensor must not be written into LICENSE while the decision is PENDING")
	}
}

// TestAdditionalUseGrantAddsFreePersonalAndInternalProductionUse confirms, from
// the license text itself, that the grant permits free personal and internal
// company production use and does not remove the BSL rights to copy, modify and
// redistribute.
func TestAdditionalUseGrantAddsFreePersonalAndInternalProductionUse(t *testing.T) {
	license := normalizeLicenseText(readRepoFile(t, "LICENSE"))
	for _, want := range []struct{ needle, why string }{
		{"(a) for personal use; and", "free personal use"},
		{"internal business or organizational purposes, including use in a commercial production environment", "free internal company production use"},
		{"installing and running the Licensed Work on your own or your organization's computers", "installation across the organization's own computers"},
		{"without payment of a fee", "the grant is free of charge"},
		{"This Additional Use Grant only adds permissions.", "the grant adds rather than restricts"},
		{"copy, modify, create derivative works, redistribute, and make non-production use of the Licensed Work", "the BSL copy/modify/redistribute rights are preserved, not removed"},
	} {
		if !strings.Contains(license, want.needle) {
			t.Errorf("Additional Use Grant is missing %s (%q)", want.why, want.needle)
		}
	}
}

// TestBuildScriptsShipLicenseAndNotices pins the packaging step: a release must
// distribute the license and the third-party notices beside the artifact, or a
// binary redistribution breaches the BSL display rule and the MIT/BSD-3
// attribution rule. This is a source check because the scripts need a full
// toolchain to run.
func TestBuildScriptsShipLicenseAndNotices(t *testing.T) {
	// The exact copy/install command, not just the file name: a mere mention
	// (for example in a checksum line) is not proof the document is shipped.
	for _, want := range []struct {
		file  string
		lines []string
	}{
		{"scripts/build-linux.sh", []string{
			"install -m 644 -- LICENSE dist/linux/LICENSE",
			"install -m 644 -- THIRD_PARTY_NOTICES.md dist/linux/THIRD_PARTY_NOTICES.md",
		}},
		{"scripts/build-windows.ps1", []string{
			"Copy-Item -Force -LiteralPath LICENSE -Destination dist/windows/LICENSE",
			"Copy-Item -Force -LiteralPath THIRD_PARTY_NOTICES.md -Destination dist/windows/THIRD_PARTY_NOTICES.md",
		}},
		{"scripts/install-linux.sh", []string{
			`install -m 644 -- "$repo/LICENSE" "$docdir/LICENSE"`,
			`install -m 644 -- "$repo/THIRD_PARTY_NOTICES.md" "$docdir/THIRD_PARTY_NOTICES.md"`,
		}},
	} {
		content := readRepoFile(t, strings.Split(want.file, "/")...)
		for _, line := range want.lines {
			if !strings.Contains(content, line) {
				t.Errorf("%s does not ship a release document: missing %q", want.file, line)
			}
		}
	}
}

// TestReleaseArtifactsIncludeLicenseAndNotices checks a real build output when
// one is provided, the way the packaging job does. Set
// RADANOR_SPARROW_TEST_BINARY to the built executable; the notices must sit
// beside it. Skips when no artifact is supplied, like the distribution-asset
// test.
func TestReleaseArtifactsIncludeLicenseAndNotices(t *testing.T) {
	binary := os.Getenv("RADANOR_SPARROW_TEST_BINARY")
	if binary == "" {
		t.Skip("set RADANOR_SPARROW_TEST_BINARY to the built release executable")
	}
	dir := filepath.Dir(binary)
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("release executable not found: %v", err)
	}
	for _, name := range []string{"LICENSE", "THIRD_PARTY_NOTICES.md"} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("release package is missing %s beside the executable: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("release package %s is empty", name)
		}
	}
}

// normalizeLicenseText collapses line wrapping and quote style so assertions do
// not depend on how the license happens to be wrapped. It does not change any
// word, so a reworded license still fails.
func normalizeLicenseText(text string) string {
	replacer := strings.NewReplacer("\u2018", "'", "\u2019", "'", "\u201c", "\"", "\u201d", "\"")
	return strings.Join(strings.Fields(replacer.Replace(text)), " ")
}

// TestCompatibilityIdentifiersAreRetained is the guard on the other half of the
// rebrand: the identifiers deliberately left alone. Renaming any of these would
// silently break an existing installation, so each one is pinned to the file
// that owns it. See the private engineering record.
func TestCompatibilityIdentifiersAreRetained(t *testing.T) {
	for _, want := range []struct {
		file   string
		needle string
		why    string
	}{
		{"go.mod", "module github.com/radanor/radanor-shield", "the Go module path, until the repository migration is planned separately"},
		{filepath.Join("internal", "lifecycle", "instance_windows.go"), `Local\RadanorShield-`, "the Windows single-instance mutex: the new build must collide with a running pre-rebrand instance instead of starting a second writer"},
		{filepath.Join("internal", "ipc", "platform_windows.go"), `\\.\pipe\RadanorShieldCore`, "the core IPC pipe name"},
		{filepath.Join("internal", "config", "config.go"), `"Radanor", "Shield"`, "the Windows private state directory that holds the only copy of the user's history"},
		{filepath.Join("internal", "config", "config.go"), `"radanor-shield"`, "the Linux state and runtime directory names"},
		{filepath.Join("internal", "config", "env.go"), `LegacyEnvPrefix = "RADANOR_SHIELD_"`, "the pre-rebrand environment variable names that must keep working"},
		{filepath.Join("internal", "monitoring", "types.go"), `"shield.event/v1"`, "the persisted monitoring event schema version"},
		{filepath.Join("internal", "monitoring", "source.go"), `"shield.source/v1"`, "the source contract version"},
		{filepath.Join("internal", "assessment", "types.go"), `"shield.assessment/v1"`, "the assessment schema version"},
		{filepath.Join("internal", "coreapp", "monitoring_api.go"), `"shield.monitoring_status/v1"`, "a wire schema version"},
		{filepath.Join("internal", "coreapp", "inventory_api.go"), `"shield.inventory_status/v1"`, "a wire schema version"},
		{filepath.Join("internal", "coreapp", "assessment_api.go"), `"shield.assessment_status/v1"`, "a wire schema version"},
		{filepath.Join("internal", "coreapp", "settings_api.go"), `"shield.settings/v1"`, "a wire schema version"},
	} {
		if content := readRepoFile(t, strings.Split(want.file, string(filepath.Separator))...); !strings.Contains(content, want.needle) {
			t.Errorf("%s no longer contains %q — %s", want.file, want.needle, want.why)
		}
	}
}

// TestFrontendBrandAssetNamesMatchBranding fails when the brand artwork is
// renamed in one place only. These constants and web/src/brand.ts name the same
// files in web/public/, and the shell loads what the TypeScript module says.
func TestFrontendBrandAssetNamesMatchBranding(t *testing.T) {
	source := readRepoFile(t, "web", "src", "brand.ts")
	for _, want := range []struct {
		field string
		file  string
	}{
		{"emblem", EmblemAsset},
		{"logo", LogoAsset},
		{"favicon", FaviconAsset},
	} {
		match := regexp.MustCompile(`\b` + want.field + `:\s*"([^"]*)"`).FindStringSubmatch(source)
		if match == nil {
			t.Errorf("web/src/brand.ts has no %s asset", want.field)
			continue
		}
		if filepath.Base(match[1]) != want.file {
			t.Errorf("web/src/brand.ts %s = %q, want the supplied file %q", want.field, match[1], want.file)
		}
		if !strings.HasPrefix(match[1], "/") || strings.Contains(match[1], "://") {
			t.Errorf("web/src/brand.ts %s = %q, want a root-relative path served by the local gateway", want.field, match[1])
		}
	}
}

// TestDashboardDeclaresTheBrandIcons pins the icons the browser is offered. The
// favicon set is the only brand mark a browser tab can show, so a dashboard that
// ships the files without referencing them still looks unbranded.
func TestDashboardDeclaresTheBrandIcons(t *testing.T) {
	index := readRepoFile(t, "web", "index.html")
	for _, want := range []struct{ needle, why string }{
		{`rel="icon"`, "the browser tab icon"},
		{`href="/favicon.ico"`, "the multi-size .ico fallback"},
		{`href="/favicon-16x16.png"`, "the 16 px icon"},
		{`href="/favicon-32x32.png"`, "the 32 px icon"},
		{`href="/favicon-48x48.png"`, "the 48 px icon"},
		{`rel="apple-touch-icon"`, "the touch icon"},
		{`href="/apple-touch-icon.png"`, "the supplied touch icon file"},
	} {
		if !strings.Contains(index, want.needle) {
			t.Errorf("web/index.html does not declare %s (%q)", want.why, want.needle)
		}
	}
	// The dashboard is local-first: it must not fetch a font, image or script
	// from somebody else's server.
	if strings.Contains(index, "http://") || strings.Contains(index, "https://") {
		t.Error("web/index.html loads something from an external origin")
	}
}

// TestBrandArtworkIsNeverRecolouredOrDistorted pins the terms the artwork was
// approved under: the supplied files are used unchanged, their proportions are
// preserved, and the light surface behind them comes from the plaque rather than
// from editing the logo.
func TestBrandArtworkIsNeverRecolouredOrDistorted(t *testing.T) {
	styles := readRepoFile(t, "web", "src", "styles.css")
	forbidden := []string{
		"filter", "backdrop-filter", "opacity", "mix-blend-mode", "clip-path", "transform",
		"mask", "mask-image", "-webkit-mask", "-webkit-mask-image", "image-rendering", "object-fit",
		"color", "background", "background-color",
	}
	for _, want := range []struct{ selector, sized, automatic string }{
		{".brand-emblem", "height", "width"},
		{".about-logo", "width", "height"},
	} {
		declarations := cssDeclarations(t, styles, want.selector)
		for _, property := range forbidden {
			if _, found := declarations[property]; found {
				t.Errorf("%s must not declare %s: the approved artwork is never recoloured, filtered, cropped or masked", want.selector, property)
			}
		}
		if size, found := declarations[want.sized]; !found || !strings.HasSuffix(size, "px") {
			t.Errorf("%s must set %s to a pixel size, got %q", want.selector, want.sized, size)
		}
		if value := declarations[want.automatic]; value != "auto" {
			t.Errorf("%s must leave %s automatic so the supplied proportions are preserved, got %q", want.selector, want.automatic, value)
		}
	}
	if surface := cssDeclarations(t, styles, ".brand-plate")["background"]; surface != "var(--brand-surface)" {
		t.Errorf(".brand-plate background = %q, want var(--brand-surface): on a dark surface the unmodified artwork sits on its own neutral plaque", surface)
	}
}

// TestBrandPlaqueExistsInEveryPalette keeps the logo readable whichever palette
// is in force. The dashboard reaches light mode by two routes (an explicit
// setting and the operating system preference), and both need the surface.
func TestBrandPlaqueExistsInEveryPalette(t *testing.T) {
	styles := readRepoFile(t, "web", "src", "styles.css")
	for _, variable := range []string{"--brand-surface:", "--brand-surface-border:"} {
		if count := strings.Count(styles, variable); count < 3 {
			t.Errorf("styles.css declares %s %d times; the dark palette, the explicit light palette and the system light palette all need it", variable, count)
		}
	}
}

// cssDeclarations returns the declarations of the first rule for a selector, so
// the artwork rules can be inspected instead of matched as raw text.
func cssDeclarations(t *testing.T, source, selector string) map[string]string {
	t.Helper()
	start := strings.Index(source, selector+" {")
	if start < 0 {
		t.Fatalf("styles.css has no %s rule", selector)
	}
	open := strings.Index(source[start:], "{")
	if open < 0 {
		t.Fatalf("styles.css %s rule has no opening brace", selector)
	}
	body := source[start+open+1:]
	end := strings.Index(body, "}")
	if end < 0 {
		t.Fatalf("styles.css %s rule is unterminated", selector)
	}
	declarations := map[string]string{}
	for _, declaration := range strings.Split(body[:end], ";") {
		declaration = strings.TrimSpace(declaration)
		if declaration == "" {
			continue
		}
		property, value, found := strings.Cut(declaration, ":")
		if !found {
			t.Fatalf("styles.css %s has an unparsable declaration %q", selector, declaration)
		}
		property, value = strings.TrimSpace(property), strings.TrimSpace(value)
		if strings.ContainsAny(property, "{} ") || value == "" || strings.ContainsAny(value, "{}") {
			t.Fatalf("styles.css %s has a malformed declaration %q", selector, declaration)
		}
		declarations[property] = value
	}
	if len(declarations) == 0 {
		t.Fatalf("styles.css %s declares nothing", selector)
	}
	return declarations
}
