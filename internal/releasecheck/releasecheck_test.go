package releasecheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validLicense is a complete, release-ready license: concrete Licensor, concrete
// Change Date within the four-year maximum, a real Additional Use Grant, the
// configured GPLv3 Change License, and the untouched canonical body.
func validLicense() string {
	return `Business Source License 1.1

License text copyright © 2024 MariaDB plc, All Rights Reserved.
“Business Source License” is a trademark of MariaDB plc.

Parameters

Licensor:             Radanor Technologies Ltd.

Licensed Work:        Radanor Sparrow Community Edition
                      The Licensed Work is © Radanor Technologies Ltd.

Additional Use Grant: You may use the Licensed Work for personal use and for
                      internal business or organizational purposes, including
                      use in a commercial production environment.

Change Date:          2030-07-01
                      First public distribution of this version: 2026-07-01

Change License:       GNU General Public License, version 3.0

` + canonicalBody
}

func writeRoot(t *testing.T, license string) string {
	t.Helper()
	root := t.TempDir()
	if license != "" {
		if err := os.WriteFile(filepath.Join(root, "LICENSE"), []byte(license), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "THIRD_PARTY_NOTICES.md"), []byte("# Third-Party Notices\n\nContent.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func codes(res Result) map[string]bool {
	out := map[string]bool{}
	for _, f := range res.Blocking() {
		out[f.Code] = true
	}
	return out
}

// allowedCodes are the findings development mode reports but tolerates: the
// documented placeholders, which a release must still reject.
func allowedCodes(res Result) map[string]bool {
	out := map[string]bool{}
	for _, f := range res.Allowed() {
		out[f.Code] = true
	}
	return out
}

func TestCompleteMetadataPasses(t *testing.T) {
	res := Run(Input{Root: writeRoot(t, validLicense())})
	if res.Blocked() {
		t.Fatalf("a complete license must pass, got: %+v", res.Blocking())
	}
	if len(res.Findings) != 0 {
		t.Fatalf("expected no findings at all, got: %+v", res.Findings)
	}
}

func TestPlaceholderLicensorBlocks(t *testing.T) {
	license := strings.Replace(validLicense(), "Licensor:             Radanor Technologies Ltd.",
		"Licensor:             <COPYRIGHT HOLDER — LEGAL NAME NOT YET VERIFIED>", 1)
	if !codes(Run(Input{Root: writeRoot(t, license)}))[CodeLicensorUnverified] {
		t.Fatal("a placeholder Licensor must block the release")
	}
}

// The BSL names the holder twice: the Licensor parameter and the © line inside the
// Licensed Work. The gate used to read only the first, so a change that fixed the
// Licensor could leave the copyright line as a placeholder and still pass. These
// tests pin both halves.
func TestCopyrightHolderMustMatchLicensor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		license string
		blocked bool
	}{
		{"matching holder", validLicense(), false},
		{
			"different holder",
			strings.Replace(validLicense(), "The Licensed Work is © Radanor Technologies Ltd.",
				"The Licensed Work is © Someone Else Ltd.", 1),
			true,
		},
		{
			"missing holder",
			strings.Replace(validLicense(), "\n                      The Licensed Work is © Radanor Technologies Ltd.", "", 1),
			true,
		},
		{
			"wrapped holder still matches",
			strings.Replace(validLicense(), "The Licensed Work is © Radanor Technologies Ltd.",
				"The Licensed Work is © Radanor Technologies\n                      Ltd.", 1),
			false,
		},
	} {
		for _, mode := range []Mode{Release, Development} {
			res := Run(Input{Root: writeRoot(t, tc.license), Mode: mode})
			if got := codes(res)[CodeCopyrightHolder]; got != tc.blocked {
				t.Errorf("%s (%v): copyright-holder finding = %v, want %v; findings: %+v", tc.name, mode, got, tc.blocked, res.Findings)
			}
		}
	}
}

// A copyright year printed beside the holder is the conventional form, and a year
// is not a party: "© 2026 Name" and "© Name" both name Name, so the comparison
// against the Licensor still holds. Only the year is ignored — a year beside a
// different name still fails, and a © line that carries only a year names nobody.
func TestCopyrightYearBesideTheHolderIsNotTheParty(t *testing.T) {
	rewrite := func(line string) string {
		return strings.Replace(validLicense(), "The Licensed Work is © Radanor Technologies Ltd.", line, 1)
	}
	for _, tc := range []struct {
		name    string
		license string
		blocked bool
	}{
		{"year before the holder", rewrite("The Licensed Work is © 2026 Radanor Technologies Ltd."), false},
		{"year after the holder", rewrite("The Licensed Work is © Radanor Technologies Ltd., 2026"), false},
		{"year before a different holder", rewrite("The Licensed Work is © 2026 Someone Else Ltd."), true},
		{"year instead of a holder", rewrite("The Licensed Work is © 2026"), true},
	} {
		for _, mode := range []Mode{Release, Development} {
			res := Run(Input{Root: writeRoot(t, tc.license), Mode: mode})
			if got := codes(res)[CodeCopyrightHolder]; got != tc.blocked {
				t.Errorf("%s (%v): copyright-holder finding = %v, want %v; findings: %+v", tc.name, mode, got, tc.blocked, res.Findings)
			}
			if res.Blocked() != tc.blocked {
				t.Errorf("%s (%v): blocked = %v, want %v; findings: %+v", tc.name, mode, res.Blocked(), tc.blocked, res.Findings)
			}
		}
	}
}

// An unresolved copyright line must never reach a release, whichever way it is
// unresolved: a placeholder that still names nothing, or an absent line that the
// old gate simply did not look for.
func TestUnresolvedCopyrightHolderBlocksRelease(t *testing.T) {
	// A placeholder that still names nobody is the documented intermediate state:
	// development reports it as allowed, a release blocks.
	placeholderHolder := strings.Replace(validLicense(), "The Licensed Work is © Radanor Technologies Ltd.",
		"The Licensed Work is © <COPYRIGHT HOLDER — LEGAL NAME NOT\n                      YET VERIFIED>", 1)
	dev := Run(Input{Root: writeRoot(t, placeholderHolder), Mode: Development})
	if dev.Blocked() {
		t.Fatalf("development mode must tolerate the documented placeholder state, got: %+v", dev.Blocking())
	}
	if !allowedCodes(dev)[CodeCopyrightHolder] {
		t.Fatalf("development mode must report the unresolved copyright holder rather than hide it: %+v", dev.Findings)
	}
	release := Run(Input{Root: writeRoot(t, placeholderHolder), Mode: Release})
	if !codes(release)[CodeCopyrightHolder] {
		t.Fatalf("release mode must block a placeholder copyright holder: %+v", release.Findings)
	}

	// A real Licensor with no © line at all is not a placeholder: the license is
	// structurally incomplete, so even development validation rejects it.
	missingHolder := strings.Replace(validLicense(), "\n                      The Licensed Work is © Radanor Technologies Ltd.", "", 1)
	for _, mode := range []Mode{Release, Development} {
		res := Run(Input{Root: writeRoot(t, missingHolder), Mode: mode})
		if !codes(res)[CodeCopyrightHolder] {
			t.Errorf("%v: a Licensed Work with no copyright holder and a real Licensor must block: %+v", mode, res.Findings)
		}
	}
}

func TestEmptyLicensorBlocks(t *testing.T) {
	license := strings.Replace(validLicense(), "Licensor:             Radanor Technologies Ltd.", "Licensor:", 1)
	if !codes(Run(Input{Root: writeRoot(t, license)}))[CodeLicensorUnverified] {
		t.Fatal("an empty Licensor must block the release")
	}
}

func TestPlaceholderChangeDateBlocks(t *testing.T) {
	license := strings.Replace(validLicense(),
		"Change Date:          2030-07-01\n                      First public distribution of this version: 2026-07-01",
		"Change Date:          [RELEASE-SPECIFIC CHANGE DATE: TO BE SET ON FIRST PUBLIC DISTRIBUTION]", 1)
	if !codes(Run(Input{Root: writeRoot(t, license)}))[CodeChangeDateMissing] {
		t.Fatal("a placeholder Change Date must block the release")
	}
}

func TestChangeDateBeyondFourYearMaximumBlocks(t *testing.T) {
	license := strings.Replace(validLicense(),
		"Change Date:          2030-07-01\n                      First public distribution of this version: 2026-07-01",
		"Change Date:          2031-07-01\n                      First public distribution of this version: 2026-07-01", 1)
	if !codes(Run(Input{Root: writeRoot(t, license)}))[CodeChangeDateMaximum] {
		t.Fatal("a Change Date later than the four-year maximum must block the release")
	}
}

func TestChangeDateBeforeDistributionBlocks(t *testing.T) {
	license := strings.Replace(validLicense(),
		"Change Date:          2030-07-01\n                      First public distribution of this version: 2026-07-01",
		"Change Date:          2025-01-01\n                      First public distribution of this version: 2026-07-01", 1)
	if !codes(Run(Input{Root: writeRoot(t, license)}))[CodeChangeDateOrder] {
		t.Fatal("a Change Date before first distribution must block the release")
	}
}

func TestMissingFirstDistributionDateBlocks(t *testing.T) {
	license := strings.Replace(validLicense(),
		"Change Date:          2030-07-01\n                      First public distribution of this version: 2026-07-01",
		"Change Date:          2030-07-01", 1)
	if !codes(Run(Input{Root: writeRoot(t, license)}))[CodeChangeDateMaximum] {
		t.Fatal("a Change Date without the first-distribution date cannot verify the maximum and must block")
	}
}

func TestChangeLicenseMustBeGPL3(t *testing.T) {
	license := strings.Replace(validLicense(), "GNU General Public License, version 3.0", "Apache License 2.0", 1)
	if !codes(Run(Input{Root: writeRoot(t, license)}))[CodeChangeLicenseGPL3] {
		t.Fatal("a non-GPLv3 Change License must block the release")
	}
}

func TestMissingAdditionalUseGrantBlocks(t *testing.T) {
	for _, value := range []string{"", "None"} {
		license := strings.Replace(validLicense(),
			`Additional Use Grant: You may use the Licensed Work for personal use and for
                      internal business or organizational purposes, including
                      use in a commercial production environment.`,
			"Additional Use Grant: "+value, 1)
		if !codes(Run(Input{Root: writeRoot(t, license)}))[CodeAdditionalUseGrant] {
			t.Fatalf("Additional Use Grant %q must block the release", value)
		}
	}
}

func TestModifiedStandardizedTextBlocks(t *testing.T) {
	license := strings.Replace(validLicense(),
		"copy, modify, create derivative works, redistribute, and make non-production use of the Licensed Work.",
		"copy, modify, create derivative works, and redistribute the Licensed Work.", 1)
	if !codes(Run(Input{Root: writeRoot(t, license)}))[CodeTextNotIntact] {
		t.Fatal("a reworded license body must block the release")
	}
}

func TestMissingLicenseBlocks(t *testing.T) {
	if !codes(Run(Input{Root: writeRoot(t, "")}))[CodeLicenseMissing] {
		t.Fatal("a missing LICENSE must block the release")
	}
}

func TestReleasePackageMustCarryLicenseAndNotices(t *testing.T) {
	root := writeRoot(t, validLicense())
	release := t.TempDir()

	res := Run(Input{Root: root, ReleaseDir: release})
	got := codes(res)
	if !got[CodePackageLicense] || !got[CodeNoticesMissing] {
		t.Fatalf("a release directory without LICENSE and notices must block; got %v", got)
	}

	// Present but different from the repository LICENSE must also block.
	if err := os.WriteFile(filepath.Join(release, "LICENSE"), []byte("not the real license"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !codes(Run(Input{Root: root, ReleaseDir: release}))[CodePackageLicenseMatch] {
		t.Fatal("a release LICENSE that differs from the repository LICENSE must block")
	}

	// A complete package passes.
	if err := os.WriteFile(filepath.Join(release, "LICENSE"), []byte(validLicense()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "THIRD_PARTY_NOTICES.md"), []byte("notices\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := Run(Input{Root: root, ReleaseDir: release}); res.Blocked() {
		t.Fatalf("a complete release package must pass, got %+v", res.Blocking())
	}
}

// TestReadmeReleaseSectionAvoidsScannerVocabulary pins the fix for a lexical false
// positive rather than a licensing one. The README paragraph that describes the
// release gate used the scanner's own word for an unfinished value, so the scanner
// reported the sentence about the gate as an unresolved value inside it. The
// wording was changed and this test keeps it changed: the section may describe the
// gate in any words except the ones the scanner reserves, because a release cannot
// be unblocked by rewording real blockers, only by resolving them.
func TestReadmeReleaseSectionAvoidsScannerVocabulary(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(raw)
	start := strings.Index(readme, "### Release builds and the licensing gate")
	if start < 0 {
		t.Fatal("README.md no longer has the section that describes the release gate")
	}
	end := strings.Index(readme[start:], "\n## License")
	if end < 0 {
		t.Fatal("README.md's release-gate section has no following heading to bound it")
	}
	section := readme[start : start+end]
	if !strings.Contains(section, "fails closed") {
		t.Fatal("the release-gate section must still say the gate fails closed")
	}
	lower := strings.ToLower(section)
	for _, marker := range placeholderMarkers {
		if strings.Contains(lower, marker) {
			t.Errorf("README.md's release-gate section uses the scanner marker %q, so prose about the gate reads as an unresolved value; describe the gate without it", marker)
		}
	}
}

// TestOwnerRiskDecisionClosesTheGateWithoutClaimingClearance pins the state recorded on
// 2026-10-28. The rights holder closed D1 as a *risk decision*: the two section 6 markers
// were removed as the mechanical consequence, so the gate may reach exit 0 — but every
// document must keep saying that the legal question is not settled. Two failures matter
// equally here: a notice that regains a marker (the decision was reversed without a record)
// and a document that starts reading as legal clearance (an accepted risk presented as a
// conclusion). Neither may be fixed by editing wording alone.
func TestOwnerRiskDecisionClosesTheGateWithoutClaimingClearance(t *testing.T) {
	root := repoRoot(t)
	for _, finding := range Run(Input{Root: root}).Blocking() {
		t.Errorf("the gate must not block after the recorded D1 risk decision, got %s: %s", finding.Code, finding.Message)
	}

	// The owner decision record itself is not published with the source, so what this
	// repository asserts is the state the record produced: the gate passes and the
	// documents that do ship still describe the decision as a risk acceptance.

	// The notices are what a recipient reads. They must disclose the uncertainty, and they
	// must not carry a marker again: the gate passing and the notices disclosing are two
	// halves of the same honest state.
	notices := readReleaseDoc(t, "THIRD_PARTY_NOTICES.md")
	for _, required := range []struct{ token, why string }{
		{"not by legal certification", "the notices must not present the decision as clearance"},
		{"risk decision", "the notices must name the basis of the decision that released them"},
		{"residual uncertainty", "the uncertainty the owner accepted has to travel with the artifact"},
		{"Known concrete obligations are unaffected", "a known obligation is never absorbed by the decision"},
	} {
		if !strings.Contains(notices, required.token) {
			t.Errorf("the notices no longer say %q: %s", required.token, required.why)
		}
	}
	lower := strings.ToLower(notices)
	for _, marker := range placeholderMarkers {
		if strings.Contains(lower, marker) {
			t.Errorf("the notices carry the scanner marker %q again; only a recorded decision may change that state", marker)
		}
	}

	sheet := readReleaseDoc(t, "docs/licensing/THIRD_PARTY_COMPLIANCE_DECISION_SHEET.md")
	for _, required := range []string{"F1", "F2", "Risk accepted by the owner"} {
		if !strings.Contains(sheet, required) {
			t.Errorf("the decision sheet must record %q: a finding's outcome belongs beside its evidence", required)
		}
	}
}

func TestPlaceholderInReleaseFacingMetadataBlocks(t *testing.T) {
	root := writeRoot(t, validLicense())
	readme := "# Radanor Sparrow\n\nThe Licensor is not yet verified.\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	if !codes(Run(Input{Root: root}))[CodePlaceholder] {
		t.Fatal("an unresolved placeholder in release-facing metadata must block the release")
	}
}

// TestRealRepositoryMetadataIsWellFormed runs against this repository's actual
// release-facing metadata. It pins the facts that must hold whatever the
// placeholder state is: the standardized text is intact, every parameter is
// present, the structure is valid and the notices file exists. It deliberately
// does not assert whether the placeholders are resolved, so it stays true both
// before and after the license is completed.
//
// It validates the metadata twice: as committed, and as a Windows checkout with
// core.autocrlf, which rewrites every text file to CRLF. The two runs must
// produce identical findings and no diagnostic may carry a stray carriage
// return. This is the check that failed in CI: a CRLF LICENSE used to look like
// modified license text, and the Licensor diagnostic ended in "\r".
func TestRealRepositoryMetadataIsWellFormed(t *testing.T) {
	root := repoRoot(t)
	lf := Run(Input{Root: root, Mode: Development})
	crlfRepo := Run(Input{Root: crlfMirror(t, root), Mode: Development})

	got := codes(lf)
	for _, code := range []string{CodeTextNotIntact, CodeStructureInvalid, CodeParametersMissing, CodeNoticesMissing, CodeLicenseMissing} {
		if got[code] {
			t.Errorf("repository metadata is malformed (%s): %+v", code, lf.Blocking())
		}
	}
	if lf.Blocked() || crlfRepo.Blocked() {
		t.Errorf("the metadata must be well formed in either line ending; LF=%+v CRLF=%+v", lf.Blocking(), crlfRepo.Blocking())
	}
	if !sameCodes(lf.Findings, crlfRepo.Findings) {
		t.Errorf("LF and CRLF copies of this repository must produce identical findings; LF=%+v CRLF=%+v", lf.Findings, crlfRepo.Findings)
	}
	for _, res := range []Result{lf, crlfRepo} {
		for _, f := range res.Findings {
			if strings.ContainsAny(f.Message, "\r\n") {
				t.Errorf("diagnostic %s carries a stray line ending: %q", f.Code, f.Message)
			}
		}
	}
}

// crlfMirror copies this repository's release-facing metadata into a temporary
// root using CRLF line endings, reproducing what a Windows checkout with
// core.autocrlf produces.
func crlfMirror(t *testing.T, root string) string {
	t.Helper()
	dir := t.TempDir()
	for _, rel := range releaseFacingFiles {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			if rel == "LICENSE" || rel == "THIRD_PARTY_NOTICES.md" {
				t.Fatalf("repository is missing %s: %v", rel, err)
			}
			continue
		}
		target := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(crlf(normalizeEOL(string(raw)))), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestDevelopmentValidationPassesOnThisRepository is the development half of the
// split: the placeholders documented in the private release decision log are
// allowed, and the metadata is structurally sound. It must pass on every
// platform, including a Windows CRLF checkout.
//
// While the license is still unresolved it also pins today's state: the
// documented placeholder codes must be reported as allowed rather than hidden.
// That assertion is conditional on LICENSE actually carrying the placeholder, so
// the test does not turn into a false failure once the license is completed.
func TestDevelopmentValidationPassesOnThisRepository(t *testing.T) {
	res := Run(Input{Root: repoRoot(t), Mode: Development})
	if res.Blocked() {
		t.Errorf("development validation must pass on this repository; blocked by: %+v", res.Blocking())
	}
	for _, f := range res.Blocking() {
		if f.Placeholder {
			t.Errorf("development mode must not block on the documented placeholder %s", f.Code)
		}
	}
	if !licenseIsUnresolved(t) {
		return
	}
	allowed := map[string]bool{}
	for _, f := range res.Allowed() {
		allowed[f.Code] = true
	}
	for _, code := range []string{CodeLicensorUnverified, CodeChangeDateMissing, CodePlaceholder} {
		if !allowed[code] {
			t.Errorf("the documented placeholders are still unresolved, so development validation must report %s as allowed; got %v", code, res.Allowed())
		}
	}
}

// TestReleaseValidationBlocksThisRepository is the release half, stated as an
// invariant rather than as today's placeholder state: every finding that
// development tolerates must block a public release, so the gate is blocked
// while the documented placeholders remain and must pass once they are resolved.
func TestReleaseValidationBlocksThisRepository(t *testing.T) {
	root := repoRoot(t)
	dev := Run(Input{Root: root, Mode: Development})
	release := Run(Input{Root: root, Mode: Release})
	got := codes(release)
	for _, f := range dev.Allowed() {
		if !got[f.Code] {
			t.Errorf("release validation must reject %s, which development tolerates", f.Code)
		}
	}
	if unresolved := len(dev.Allowed()) > 0; unresolved && !release.Blocked() {
		t.Errorf("release validation must block while unresolved placeholders remain: %+v", release.Findings)
	}
	if len(dev.Allowed()) == 0 && release.Blocked() {
		t.Errorf("the licensing metadata is complete, so release validation must pass; blocked by %+v", release.Blocking())
	}
}

// licenseIsUnresolved reports whether this repository's LICENSE still carries the
// deliberate, documented placeholder Licensor decided in
// the private release decision log.
func licenseIsUnresolved(t *testing.T) bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	return containsPlaceholderMarker(parseParameters(normalizeEOL(string(raw)))["Licensor"])
}

func TestCRLFAndLFProduceEquivalentResults(t *testing.T) {
	complete := validLicense()
	lfRes := Run(Input{Root: writeRoot(t, complete), Mode: Release})
	crlfRes := Run(Input{Root: writeRoot(t, crlf(complete)), Mode: Release})

	if lfRes.Blocked() || crlfRes.Blocked() {
		t.Fatalf("a complete license must not block in either line ending; LF=%+v CRLF=%+v", lfRes.Blocking(), crlfRes.Blocking())
	}
	if lfRes.Blocked() != crlfRes.Blocked() || !sameCodes(lfRes.Findings, crlfRes.Findings) {
		t.Fatalf("LF and CRLF must produce equivalent findings; LF=%+v CRLF=%+v", lfRes.Findings, crlfRes.Findings)
	}

	// The same CRLF file with genuinely altered text must still be rejected, so
	// the normalization cannot mask a real modification.
	altered := crlf(strings.Replace(complete, "and make non-production use", "and make production use", 1))
	if !codes(Run(Input{Root: writeRoot(t, altered), Mode: Release}))[CodeTextNotIntact] {
		t.Error("a CRLF license with altered text must still be rejected")
	}
}

func TestReleasePackageLicenseMatchesAcrossLineEndings(t *testing.T) {
	root := writeRoot(t, validLicense())
	release := t.TempDir()
	if err := os.WriteFile(filepath.Join(release, "LICENSE"), []byte(crlf(validLicense())), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "THIRD_PARTY_NOTICES.md"), []byte("notices\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := Run(Input{Root: root, ReleaseDir: release, Mode: Release}); res.Blocked() {
		t.Fatalf("a CRLF-shipped LICENSE with identical content must match the repository LICENSE; got %+v", res.Blocking())
	}
}

func TestDevelopmentModeAllowsDocumentedPlaceholders(t *testing.T) {
	root := writeRoot(t, placeholderLicense())
	dev := Run(Input{Root: root, Mode: Development})
	if dev.Blocked() {
		t.Fatalf("development mode must allow the documented placeholders; got %+v", dev.Blocking())
	}
	if len(dev.Allowed()) == 0 {
		t.Fatal("development mode should report the placeholders as allowed")
	}
	if !Run(Input{Root: root, Mode: Release}).Blocked() {
		t.Fatal("release mode must reject the documented placeholders")
	}
}

func TestDevelopmentModeStillRejectsStructuralProblems(t *testing.T) {
	cases := []struct{ name, license, code string }{
		{"altered BSL text", strings.Replace(placeholderLicense(), "and make non-production use", "and make production use", 1), CodeTextNotIntact},
		{"missing structure", "Business Source License 1.1\n\nNo parameters or terms here.\n", CodeStructureInvalid},
		{"missing Additional Use Grant", strings.Replace(placeholderLicense(),
			"Additional Use Grant: You may use the Licensed Work for personal use and for\n                      internal business or organizational purposes.",
			"Additional Use Grant:", 1), CodeAdditionalUseGrant},
		{"wrong Change License", strings.Replace(placeholderLicense(), "GNU General Public License, version 3.0", "Mozilla Public License 2.0", 1), CodeChangeLicenseGPL3},
	}
	for _, tc := range cases {
		got := codes(Run(Input{Root: writeRoot(t, tc.license), Mode: Development}))
		if !got[tc.code] {
			t.Errorf("%s: development mode must still report %s; got %v", tc.name, tc.code, got)
		}
	}
}

func TestInvalidConcreteChangeDateIsRejected(t *testing.T) {
	for _, date := range []string{"2026-13-45", "not-a-date"} {
		license := strings.Replace(validLicense(), "Change Date:          2030-07-01", "Change Date:          "+date, 1)
		if !codes(Run(Input{Root: writeRoot(t, license), Mode: Release}))[CodeChangeDateInvalid] {
			t.Errorf("Change Date %q must be rejected as an invalid concrete date", date)
		}
	}
}

func TestInconsistentLicensingMetadataBlocks(t *testing.T) {
	for _, tc := range []struct {
		name    string
		nameVal string
		blocked bool
	}{
		{"matching application license", "Business Source License 1.1", false},
		{"contradictory application license", "Apache License 2.0", true},
	} {
		root := writeRoot(t, validLicense())
		if err := os.MkdirAll(filepath.Join(root, "internal", "branding"), 0o755); err != nil {
			t.Fatal(err)
		}
		source := "package branding\n\nconst (\n\tLicenseName = \"" + tc.nameVal + "\"\n)\n"
		if err := os.WriteFile(filepath.Join(root, "internal", "branding", "branding.go"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		got := codes(Run(Input{Root: root, Mode: Release}))
		if got[CodeMetadataInconsistent] != tc.blocked {
			t.Errorf("%s: inconsistent=%v, want %v", tc.name, got[CodeMetadataInconsistent], tc.blocked)
		}
	}
}

func crlf(text string) string { return strings.ReplaceAll(text, "\n", "\r\n") }

func sameCodes(a, b []Finding) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Code != b[i].Code || a[i].Placeholder != b[i].Placeholder {
			return false
		}
	}
	return true
}

// placeholderLicense mirrors this repository's intentionally unresolved license:
// a marked placeholder Licensor and Change Date, and an otherwise complete
// license body.
func placeholderLicense() string {
	return `Business Source License 1.1

License text copyright © 2024 MariaDB plc, All Rights Reserved.
“Business Source License” is a trademark of MariaDB plc.

Parameters

Licensor:             <COPYRIGHT HOLDER — LEGAL NAME NOT YET VERIFIED>
                      [RELEASE BLOCKER: the Licensor must be the legally
                       verified copyright holder.]

Licensed Work:        Radanor Sparrow Community Edition

Additional Use Grant: You may use the Licensed Work for personal use and for
                      internal business or organizational purposes.

Change Date:          [RELEASE-SPECIFIC CHANGE DATE: TO BE SET ON FIRST PUBLIC
                       DISTRIBUTION — NO VERSION HAS BEEN PUBLICLY RELEASED]

Change License:       GNU General Public License, version 3.0

` + canonicalBody
}

// TestReleaseBuildPathsInvokeTheGate pins the wiring: the explicit release build
// paths and the release workflow must invoke the gate, and the development
// workflow must not turn it into a build-blocking check.
func TestReleaseBuildPathsInvokeTheGate(t *testing.T) {
	root := repoRoot(t)
	read := func(parts ...string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
		if err != nil {
			t.Fatalf("reading %s: %v", filepath.Join(parts...), err)
		}
		return string(b)
	}

	for _, want := range []struct {
		file  string
		parts []string
	}{
		{"scripts/build-linux.sh", []string{"radanor-release-check", "--release", "--release-dir dist/linux"}},
		{"scripts/build-windows.ps1", []string{"radanor-release-check", "-Release", "--release-dir dist/windows"}},
		{"scripts/validate-release-licensing.sh", []string{"radanor-release-check"}},
		{filepath.Join(".github", "workflows", "release.yml"), []string{"radanor-release-check", "build-linux.sh --release", "build-windows.ps1 -Release"}},
	} {
		content := read(strings.Split(want.file, string(filepath.Separator))...)
		for _, needle := range want.parts {
			if !strings.Contains(content, needle) {
				t.Errorf("%s does not invoke the release gate: missing %q", want.file, needle)
			}
		}
	}

	// Development CI runs development validation: documented placeholders are
	// allowed, but it must never run the strict gate (that belongs to the release
	// workflow).
	ci := read(".github", "workflows", "ci.yml")
	if !strings.Contains(ci, "radanor-release-check --mode development") {
		t.Error("development CI must run development validation so structural problems stay visible")
	}
	if strings.Contains(ci, "radanor-release-check\"") || strings.Contains(ci, "radanor-release-check\n") {
		t.Error("development CI must not run the strict gate; that belongs to the release workflow")
	}
	if strings.Contains(ci, "--mode release") {
		t.Error("development CI must not run strict release validation")
	}
}

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
			t.Fatal("repository root not found")
		}
		dir = parent
	}
}
