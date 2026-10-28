package releasecheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The v0.1.1 public documentation set is checked here rather than trusted, and it is the
// v0.1.0 set carried forward: v0.1.0 was tagged and never published, so the documents that
// describe the release being published now name v0.1.1, while v0.1.0 survives as the
// historical tag the checklist records. These
// tests exist because a release document is a claim made to a stranger: it can be
// wrong in ways no compiler catches — a link to a file that was renamed, a licence
// date that drifted from LICENSE, an invented publisher, a plausible-looking hash
// that no artifact produced, or a sentence that reads as an approval nobody gave.
//
// They are deliberately about properties, not wording: a heading that is renamed
// only has to keep carrying its subject, while an approval that was never recorded
// must not appear to have been.

const releaseDocsDir = "docs/release"

func releaseDocPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(repoRoot(t), releaseDocsDir, name)
}

func readReleaseDocFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(releaseDocPath(t, name))
	if err != nil {
		t.Fatalf("release document %s is missing: %v", name, err)
	}
	return string(raw)
}

// TestReleaseDocumentsCarryTheirRequiredSections pins the subject of each document.
// A document that loses a section has stopped answering the question it exists for,
// which is a silent regression for a reader who has nothing else to consult.
func TestReleaseDocumentsCarryTheirRequiredSections(t *testing.T) {
	required := map[string][]string{
		"README.md": {
			"## The documents",
			"## Public and internal documents",
			"not a company",
		},
		"COMMUNITY_LICENSE_GUIDE.md": {
			"# Radanor Sparrow Community Edition — licence guide",
			"## What the Business Source License already lets you do",
			"## What the Additional Use Grant adds",
			"## The Change Date and the Change License",
		},
		"THIRD_PARTY_LICENSE_COMPLIANCE.md": {
			"## 1. What is actually distributed",
			"## 2. Dependency inventory",
			"## 3. LGPL-2.1 obligations",
			"## 5. `modernc.org/libc` header-derived material",
			"## 6. How these statements were verified",
		},
		"WINDOWS_INSTALLATION_GUIDE.md": {
			`## Expect an "Unknown Publisher" warning`,
			"## First launch",
			"## Where your data is kept",
			"## Uninstalling",
			"## Troubleshooting",
		},
		"SECURITY_AND_PRIVACY.md": {
			"## What leaves your computer",
			"## What it stores, and where",
			"## Limitations you should not misunderstand",
			"## Reporting a security problem",
		},
		"VERIFY_DOWNLOADS.md": {
			"## Windows — PowerShell",
			"## Ubuntu / Linux — `sha256sum`",
			"## What a matching checksum does and does not prove",
			"Get-FileHash",
			"sha256sum -c --strict",
		},
		"RELEASE_NOTES_V0_1_1.md": {
			"# Radanor Sparrow Community Edition v0.1.1",
			"## What this release does",
			"## Known limitations in v0.1.1",
			"## Licensing",
		},
		"SUPPORT_AND_SECURITY_REPORTING.md": {
			"## Reporting a security vulnerability — privately",
			"## Supported versions",
			"GitHub private vulnerability reporting",
		},
		"FINAL_RELEASE_CHECKLIST.md": {
			"## 1. The checklist",
			"## 2. The compliance change, and the documents this set defers to",
			"## 3. The exact publication sequence, once every gate passes",
		},
		"GITHUB_RELEASE_V0_1_1.md": {
			"## Radanor Sparrow Community Edition v0.1.1",
			"### ⚠ This release is unsigned",
			"### Verify before you run",
			"### Licence",
		},
	}
	for name, sections := range required {
		body := readReleaseDocFile(t, name)
		for _, section := range sections {
			if !strings.Contains(body, section) {
				t.Errorf("%s/%s must keep the section %q: without it the document no longer answers the question a reader opens it for", releaseDocsDir, name, section)
			}
		}
	}
}

var markdownLinkRe = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)

// TestReleaseDocumentationLinksResolve keeps every relative link honest. A release
// document is read by someone who cannot inspect the repository layout, so a link to
// a renamed file is a dead end for exactly the reader who needed it.
func TestReleaseDocumentationLinksResolve(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, releaseDocsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", releaseDocsDir, err)
	}
	documents := 0
	links := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		documents++
		body := readReleaseDocFile(t, entry.Name())
		for _, match := range markdownLinkRe.FindAllStringSubmatch(body, -1) {
			target := match[1]
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "#") {
				continue
			}
			// An anchor is a property of the rendered page, not of the file we can
			// read here; the file it names is what must exist.
			if index := strings.Index(target, "#"); index >= 0 {
				target = target[:index]
			}
			if target == "" {
				continue
			}
			links++
			resolved := filepath.Clean(filepath.Join(dir, filepath.FromSlash(target)))
			rel, relErr := filepath.Rel(root, resolved)
			if relErr != nil {
				t.Errorf("%s: link %q cannot be resolved: %v", entry.Name(), match[1], relErr)
				continue
			}
			if _, statErr := os.Stat(resolved); statErr == nil {
				continue
			}
			t.Errorf("%s: link %q points at %s, which does not exist", entry.Name(), match[1], rel)
		}
	}
	if documents < 10 {
		t.Fatalf("only %d release documents were checked; the set is incomplete", documents)
	}
	if links < 30 {
		t.Fatalf("only %d relative links were checked, which suggests the link scanner stopped matching", links)
	}
}

// TestReleaseDocumentationKeepsTheApprovedLicenceIdentity ties the public documents
// to LICENSE. A release guide that names a different Change Date or a different
// Change License than the file beside the binary is worse than none: it tells a
// reader the terms are something they are not.
func TestReleaseDocumentationKeepsTheApprovedLicenceIdentity(t *testing.T) {
	license, err := os.ReadFile(filepath.Join(repoRoot(t), "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	licenseText := normalizeEOL(string(license))
	params := parseParameters(licenseText)

	// The documents must agree with the authoritative file, not with a constant, so a
	// future Change Date cannot pass this test while leaving the guides stale.
	changeDate := strings.TrimSpace(firstLine(params["Change Date"]))
	if changeDate == "" {
		t.Fatal("LICENSE has no Change Date to check the documentation against")
	}
	if !strings.HasPrefix(changeDate, "2030-10-27") {
		t.Fatalf("this test encodes the approved Change Date 2030-10-27, but LICENSE now reads %q; update the documentation and this expectation together", changeDate)
	}

	// THIRD_PARTY_LICENSE_COMPLIANCE.md is deliberately not in this list: it covers the
	// third-party material and points at LICENSE for the product's own terms, so
	// requiring it to restate the Change Date would invite a second copy that drifts.
	documents := []string{"COMMUNITY_LICENSE_GUIDE.md", "RELEASE_NOTES_V0_1_1.md", "GITHUB_RELEASE_V0_1_1.md"}
	for _, name := range documents {
		body := readReleaseDocFile(t, name)
		for _, required := range []string{
			"2030-10-27",
			"Business Source License 1.1",
			"GNU General Public License",
			"Radheshyam Dangi",
			"© 2026",
		} {
			if !strings.Contains(body, required) {
				t.Errorf("%s must state %q so it matches LICENSE", name, required)
			}
		}
		if strings.Contains(body, "Apache License") && !strings.Contains(body, "typescript") {
			t.Errorf("%s mentions an Apache License without tying it to the build tool that carries it, which could read as Sparrow's Change License", name)
		}
	}
}

// corporatePublisherRe matches a legal-form suffix attached to the brand. Radanor is
// a product name; the Licensor and copyright holder is an individual. Presenting the
// brand as an incorporated entity would be an invented fact about who the other party
// to the licence is.
var corporatePublisherRe = regexp.MustCompile(`(?i)\bRadanor\b\s*(?:Pvt|Private|Ltd|Limited|LLC|L\.L\.C\.|Inc|Incorporated|GmbH|AG|Corp|Corporation)\b`)

func TestReleaseDocumentationDoesNotInventACorporatePublisher(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, releaseDocsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		body := readReleaseDocFile(t, entry.Name())
		if match := corporatePublisherRe.FindString(body); match != "" {
			t.Errorf("%s describes Radanor as %q; Radanor is a brand and the rights holder is an individual", entry.Name(), match)
		}
	}
}

var sha256LiteralRe = regexp.MustCompile(`\b[0-9a-fA-F]{64}\b`)

// TestReleaseDocumentationPublishesNoUnverifiedValues is the guard against the most
// tempting form of inaccuracy in a release: filling in a hash, a URL or a date that
// looks right so the page reads as finished. No artifact has been published, so no
// document may present a digest or a download URL as if one had.
func TestReleaseDocumentationPublishesNoUnverifiedValues(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, releaseDocsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		body := readReleaseDocFile(t, entry.Name())
		if match := sha256LiteralRe.FindString(body); match != "" {
			t.Errorf("%s contains the 64-character hex value %q; a digest may only be published from a real artifact, so the release template must keep it as a marked field", entry.Name(), match)
		}
		for _, fabricated := range []string{"releases/download/", "releases/tag/v0.1.1"} {
			if strings.Contains(body, fabricated) {
				t.Errorf("%s contains %q, which would be a download or tag URL for a release that does not exist", entry.Name(), fabricated)
			}
		}
	}

	// The template must still show where the real values go, or the fields would be
	// silently dropped at publication time.
	template := readReleaseDocFile(t, "GITHUB_RELEASE_V0_1_1.md")
	for _, marker := range []string{"⟨sha256 of RadanorSparrow.exe⟩", "⟨release-date⟩", "⟨issue-tracker-url⟩"} {
		if !strings.Contains(template, marker) {
			t.Errorf("GITHUB_RELEASE_V0_1_1.md must keep the field %s marked for filling at publication", marker)
		}
	}
}

// collapseSpaces makes phrase assertions independent of where the prose was wrapped.
// A sentence that reflows onto the next line is the same sentence; a claim that was
// deleted is not, and that is what these assertions are for.
func collapseSpaces(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// TestReleaseDocumentationStatesWhatIsNotYetTrue makes the negative claims explicit.
// A public document about an unpublished release is only safe if it says so; these
// assertions fail if that statement is edited away, not if it is reworded into a
// different honest sentence, because several accepted phrasings are listed.
func TestReleaseDocumentationStatesWhatIsNotYetTrue(t *testing.T) {
	notPublished := []string{
		"has not been published",
		"no release of sparrow has been published",
		"no release has been published",
		"not published",
		"publication is not authorized",
	}
	for _, name := range []string{"README.md", "RELEASE_NOTES_V0_1_1.md", "GITHUB_RELEASE_V0_1_1.md", "FINAL_RELEASE_CHECKLIST.md", "THIRD_PARTY_LICENSE_COMPLIANCE.md"} {
		body := collapseSpaces(strings.ToLower(readReleaseDocFile(t, name)))
		found := false
		for _, phrase := range notPublished {
			if strings.Contains(body, phrase) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s must state plainly that no release has been published", name)
		}
	}

	// Claims that would be false today. Exact strings, so honest prose about the
	// absence of these things is not caught.
	for _, name := range []string{"README.md", "RELEASE_NOTES_V0_1_1.md", "GITHUB_RELEASE_V0_1_1.md", "FINAL_RELEASE_CHECKLIST.md"} {
		body := collapseSpaces(strings.ToLower(readReleaseDocFile(t, name)))
		for _, claim := range []string{
			"approved for publication",
			"authorized for publication",
			"is now available for download",
			"has been released",
			"approved for public release",
		} {
			if strings.Contains(body, claim) {
				t.Errorf("%s contains the claim %q; no approval has been recorded and nothing has been published", name, claim)
			}
		}
	}
}

// TestFinalChecklistCoversEveryMandatoryGate keeps the checklist complete. Each
// entry below is a thing that must be true before publication; dropping one would
// quietly shorten the gate rather than fail it.
func TestFinalChecklistCoversEveryMandatoryGate(t *testing.T) {
	checklist := readReleaseDocFile(t, "FINAL_RELEASE_CHECKLIST.md")
	lower := strings.ToLower(checklist)
	required := []struct{ name, token string }{
		{"approved licensing conclusions", "approved licensing conclusions"},
		{"copyright and ownership", "copyright and ownership"},
		{"signed owner decisions", "signed owner decisions"},
		{"GitHub environment protection", "github environment protection"},
		{"protected release tag", "protected release tag"},
		{"exact release commit", "exact release commit"},
		{"Windows native acceptance", "windows native acceptance"},
		{"Linux AppImage acceptance", "linux appimage acceptance"},
		{"reproducibility", "reproducibility"},
		{"SHA-256 checksums", "sha-256 checksums"},
		{"installer payload integrity", "installer payload integrity"},
		{"no development artifacts", "no development artifacts presented as a release"},
		{"release notes", "release notes and installation guidance"},
		{"download links", "correct download links"},
		{"publication authorization", "public publication authorization"},
		{"first public distribution date", "recorded first public distribution date"},
	}
	for _, item := range required {
		if !strings.Contains(lower, item.token) {
			t.Errorf("FINAL_RELEASE_CHECKLIST.md must keep the gate %q", item.name)
		}
	}
	// The checklist is the authoritative gate, so it must not read as satisfied.
	if !strings.Contains(checklist, "NOT authorized") {
		t.Error("FINAL_RELEASE_CHECKLIST.md must state that publication is NOT authorized while any gate is open")
	}
	for _, status := range []string{"BLOCKED", "NOT RUN", "PASS"} {
		if !strings.Contains(checklist, status) {
			t.Errorf("FINAL_RELEASE_CHECKLIST.md must use the status %q; a single-status checklist cannot distinguish a pass from an untested item", status)
		}
	}
	// Any remaining gate that is open must be attributed to somebody. A gate with no
	// owner is a gate that waits forever.
	if !strings.Contains(lower, "radanor-admin") {
		t.Error("FINAL_RELEASE_CHECKLIST.md must name the repository administrator for the GitHub controls")
	}
}

// productVersionRe finds a version attached to the product: either in an artifact
// filename or in the product's own name. Dependency versions are matched by neither,
// which matters — `modernc.org/libc v1.77.1` and `go-winio v0.6.2` are third-party
// versions that these documents are supposed to state, and a bare `vX.Y.Z` search
// could not tell them apart from a release of Sparrow.
var (
	productFilenameVersionRe = regexp.MustCompile(`RadanorSparrow-(\d+\.\d+\.\d+)`)
	productNameVersionRe     = regexp.MustCompile(`(?i)\bsparrow\s+(?:community edition\s+)?v(\d+\.\d+\.\d+)`)
)

// TestReleaseDocumentationDoesNotInventAnotherVersion guards the version identity.
// These documents describe 0.1.1; a stray product reference to a later release would
// describe software that does not exist. 0.1.0 is allowed because it is a real, immutable
// tag: it was created, its Windows build failed, and nothing was published from it, so
// documents may still name it as that history.
func TestReleaseDocumentationDoesNotInventAnotherVersion(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, releaseDocsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{"0.1.0": true, "0.1.1": true}
	checked := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		body := collapseSpaces(readReleaseDocFile(t, entry.Name()))
		for _, re := range []*regexp.Regexp{productFilenameVersionRe, productNameVersionRe} {
			for _, match := range re.FindAllStringSubmatch(body, -1) {
				checked++
				if !known[match[1]] {
					t.Errorf("%s refers to the product version %q, but these documents describe 0.1.1, with 0.1.0 retained only as the tag that was never published", entry.Name(), match[1])
				}
			}
		}
	}
	if checked < 5 {
		t.Fatalf("only %d product version references were found; the version scanner is not matching and would not catch a wrong release number", checked)
	}
	for _, name := range []string{"RELEASE_NOTES_V0_1_1.md", "GITHUB_RELEASE_V0_1_1.md", "FINAL_RELEASE_CHECKLIST.md"} {
		if !strings.Contains(readReleaseDocFile(t, name), "0.1.1") {
			t.Errorf("%s must state the version it describes", name)
		}
	}
}

// TestReleaseDocumentationIndexIsReachableFromTheReadme checks the entry point. A
// reader arriving at the repository has to be able to find the relocation of the
// public documentation set without knowing its directory name.
func TestReleaseDocumentationIndexIsReachableFromTheReadme(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join(repoRoot(t), "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "docs/release/") {
		t.Error("README.md must link the public release documentation set, or it is only discoverable by guessing the path")
	}
	// The Linux guide is the long-standing one and lives elsewhere; the index defers
	// to it rather than duplicating it, so that link has to hold.
	guide := filepath.Join(repoRoot(t), "docs", "deployment", "LINUX_INSTALL_GUIDE.md")
	info, err := os.Stat(guide)
	if err != nil {
		t.Fatalf("the Linux installation guide the release documentation defers to is missing: %v", err)
	}
	if info.Size() < 2000 {
		t.Errorf("docs/deployment/LINUX_INSTALL_GUIDE.md is only %d bytes; it is the canonical Linux guide the release set points at", info.Size())
	}
}
