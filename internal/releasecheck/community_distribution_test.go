package releasecheck

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readCommunityWorkflow(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "community-release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestCommunityWorkflowKeepsEveryGateTheSignedReleaseHas pins the controls that
// make the unsigned process no easier to get through than the signed one. It
// differs from the Release workflow in exactly one respect — it makes no trust
// claim — so the licensing gate, the exact-tag rule, the main-line ancestry check,
// the shared version rule, the pinned toolchains and the checksum manifests all
// still have to be there.
func TestCommunityWorkflowKeepsEveryGateTheSignedReleaseHas(t *testing.T) {
	workflow := readCommunityWorkflow(t)
	for _, required := range []struct{ needle, why string }{
		{"go run ./cmd/radanor-release-check", "the strict licensing gate must run before anything is built"},
		{"needs: licensing-gate", "artifact work must depend on the licensing gate"},
		{"workflow_dispatch:", "an unsigned distribution must be a deliberate, manual act"},
		{"permissions:\n  contents: read", "the workflow must not hold a token that could write to the repository"},
		{"environment: community-release", "unsigned work must run through its own named environment, not the production one"},
		{"RADANOR_COMMUNITY_UNSIGNED_RELEASE", "the recorded policy approval must be a variable the run reads"},
		{"RADANOR_UNSIGNED_WINDOWS_DISTRIBUTION", "unsigned Windows distribution needs its own recorded approval"},
		{"if: vars.RADANOR_UNSIGNED_WINDOWS_DISTRIBUTION == 'approved'", "the Windows job must be skipped, not silently built, without that approval"},
		{"RADANOR_WINDOWS_SIGNING", "the run must refuse while production signing is enabled"},
		{"RADANOR_RELEASE_SIGNING", "the run must refuse while Linux production signing is enabled"},
		{"GITHUB_REF_TYPE", "the authorization step must inspect the triggering ref"},
		{"^v[0-9]+\\.[0-9]+\\.[0-9]+$", "only an exact semantic-version tag may authorize artifacts"},
		{"merge-base --is-ancestor", "the tagged commit must be proved to sit on main"},
		{"radanor-version -release", "the tag's version and the build's version must be the same value"},
		{"Initialize-SparrowAppImageTool.sh", "the AppImage toolchain must be the pinned, verified one"},
		{"Initialize-SparrowNsis.ps1", "the NSIS toolchain must be the pinned, verified one"},
		{"build-windows-installer.sh --community-unsigned", "the installer must come from the openly unsigned path"},
		{"write-release-checksums.sh", "the Linux set must carry checksum manifests"},
		{"write-windows-release-checksums.sh", "the Windows set must carry a checksum manifest"},
		{"UNSIGNED_DISTRIBUTION_NOTICE.md", "the disclosure must ship beside the artifacts it describes"},
		{"Get-AuthenticodeSignature", "the absence of a Windows signature must be asserted, not assumed"},
		{"NotSigned", "an unsigned run must verify that nothing signed its output"},
		{"Assert-SparrowUnsignedWindows.ps1", "the actual embedded uninstaller and payload must be verified"},
		{"RADANOR_UNSIGNED_POWERSHELL", "the packaging verifier must use the current installed host"},
	} {
		if !strings.Contains(workflow, required.needle) {
			t.Errorf("community-release.yml is missing %q: %s", required.needle, required.why)
		}
	}
}

// TestCommunityWorkflowReportsWhichPlatformsProducedArtifacts pins the run's own
// account of itself. The Windows job is skipped when its approval is absent, and a
// skipped job leaves the run green; that is acceptable only if the run says so.
// An approved Windows job that produced nothing is not an omission at all but a
// contradiction, so it must fail the summary instead of passing quietly.
func TestCommunityWorkflowReportsWhichPlatformsProducedArtifacts(t *testing.T) {
	workflow := readCommunityWorkflow(t)
	for _, required := range []struct{ needle, why string }{
		{"needs.unsigned-linux-artifacts.result", "the summary must report the Linux job's outcome"},
		{"needs.unsigned-windows-artifacts.result", "the summary must report the Windows job's outcome"},
		{"vars.RADANOR_UNSIGNED_WINDOWS_DISTRIBUTION", "the summary must distinguish an unauthorized Windows run from a missing one"},
		{"Linux-only distribution", "a run without Windows artifacts must say so rather than read as a cross-platform release"},
		{"::error::Windows distribution is approved", "an approved but missing Windows job must fail the run, not pass silently"},
	} {
		if !strings.Contains(workflow, required.needle) {
			t.Errorf("community-release.yml is missing %q: %s", required.needle, required.why)
		}
	}
}

// TestCommunityWorkflowCannotSignOrPublish pins the prohibitions. Each one has a
// tempting shortcut — sign it anyway, reuse the production environment, publish the
// release page from CI — and each would turn an honest unsigned distribution into a
// false claim or an unreviewed publication.
func TestCommunityWorkflowCannotSignOrPublish(t *testing.T) {
	workflow := readCommunityWorkflow(t)
	for _, forbidden := range []struct{ needle, why string }{
		{"gh release", "publication must not happen inside this workflow"},
		{"softprops/action-gh-release", "publication must not happen inside this workflow"},
		{"--mode development", "the unsigned path must never relax the licensing gate"},
		{"releases/download/continuous", "no mutable upstream tool or runtime may be fetched"},
		{"sign-release.sh", "an unsigned distribution must not sign anything"},
		{"Sign-SparrowWindowsArtifact.ps1", "an unsigned distribution must not invoke the signing hooks"},
		{"Assert-SparrowAuthenticode.ps1", "an unsigned distribution must not verify a signature it never made"},
		{"production-release", "the production environment, its reviewers and its secrets must stay untouched"},
		{"RADANOR_AUTHENTICODE_THUMBPRINT", "producing a signed artifact belongs to the Release workflow"},
		{"RADANOR_SIGNING_KEY_ARMORED", "producing a signed artifact belongs to the Release workflow"},
	} {
		if strings.Contains(workflow, forbidden.needle) {
			t.Errorf("community-release.yml contains %q: %s", forbidden.needle, forbidden.why)
		}
	}
	// Development CI must not be able to start it: only a dispatch may.
	for _, trigger := range []string{"push:", "pull_request:", "schedule:"} {
		if strings.Contains(workflow, trigger) {
			t.Errorf("community-release.yml has a %s trigger, so a push could reach an unsigned distribution", trigger)
		}
	}
}

// TestSignedReleaseWorkflowIsUntouchedByTheUnsignedPath pins the constraint that
// this amendment was allowed to add a process but not to change the signed one: the
// Release workflow still requires its signing gates and still names its own
// environment, and it does not know about the community environment at all.
func TestSignedReleaseWorkflowIsUntouchedByTheUnsignedPath(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	release := string(raw)
	for _, required := range []string{
		"environment: production-release",
		"RADANOR_WINDOWS_SIGNING",
		"RADANOR_RELEASE_SIGNING",
		"Assert-SparrowAuthenticode.ps1",
		"sign-release.sh",
		"--require-all-signed",
	} {
		if !strings.Contains(release, required) {
			t.Errorf("release.yml lost %q, so the signed path was weakened by adding the unsigned one", required)
		}
	}
	if strings.Contains(release, "community-release") {
		t.Error("release.yml references the community environment; the two processes must not share reviewers or variables")
	}
}

// TestDashboardBuildLeavesAWindowsCheckoutClean pins the property the Windows
// artifact job depends on: `vite build` writes the embedded dashboard back into
// the version-controlled directory internal/gateway/assets, replacing files that
// are committed as build output, so every tracked text file there must be
// checked out with LF on every platform.
//
// It is not cosmetic, and it is not about the dirty-tree rule being too strict.
// git itself reported the file as modified: a checkout with core.autocrlf=true
// materialises the bundle with CRLF, the rebuild writes it back with LF, and
// although the normalised bytes are identical the on-disk size is not, so
// `git status --porcelain` lists it and `radanor-version -release` refuses the
// exact `vX.Y.Z` tag it was asked to build. That is how the first Community
// Windows job failed on v0.1.0, after the portable executable had already been
// built from the same clean tree.
//
// Binary files are skipped rather than exempted: git never converts them, so
// their checkout and the copied-through build output cannot disagree.
func TestDashboardBuildLeavesAWindowsCheckoutClean(t *testing.T) {
	root := repoRoot(t)
	output := viteDashboardOutput(t, root)
	tracked := trackedFilesUnder(t, root, output)
	if len(tracked) == 0 {
		t.Skipf("%s is not version-controlled, so the dashboard build writes only ignored files and no checkout can go dirty", output)
	}
	for _, file := range tracked {
		raw, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Errorf("cannot read %s: %v", file, err)
			continue
		}
		if bytes.IndexByte(raw, 0) >= 0 {
			continue
		}
		if got := gitAttribute(t, root, "eol", file); got != "lf" {
			t.Errorf("%s is a text file the dashboard build rewrites, but its checkout line ending is %q, not \"lf\": with core.autocrlf=true a Windows checkout materialises it as CRLF, the rebuild writes LF, and `git status` reports it modified, so radanor-version refuses an exact vX.Y.Z tag. Give it an `eol=lf` rule in .gitattributes.", file, got)
		}
	}
}

// viteDashboardOutput reads the directory `vite build` writes into from the build
// configuration, so this test follows the build instead of hard-coding a path a
// later change could silently move.
func viteDashboardOutput(t *testing.T, root string) string {
	t.Helper()
	config := filepath.Join(root, "web", "vite.config.ts")
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatalf("cannot read %s: %v", config, err)
	}
	match := regexp.MustCompile(`outDir:\s*"([^"]+)"`).FindStringSubmatch(string(raw))
	if match == nil {
		t.Fatalf("%s no longer names an outDir, so the dashboard build's output location is unknown", config)
	}
	if filepath.IsAbs(match[1]) {
		t.Fatalf("outDir %q is absolute, which would place the dashboard outside the checkout", match[1])
	}
	return filepath.Clean(filepath.Join("web", match[1]))
}

// trackedFilesUnder lists the repository's tracked files below a directory. It
// skips where git or the checkout is unavailable, matching the version rule's own
// tests: the property is about a git checkout, and a source export is not one.
func trackedFilesUnder(t *testing.T, root, dir string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "ls-files", "--", dir).Output()
	if err != nil {
		if _, lookErr := exec.LookPath("git"); lookErr != nil {
			t.Skip("git is not available, so the checkout's attributes cannot be read")
		}
		t.Skipf("%s is not a git checkout, so it has no checkout attributes", root)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files
}

// gitAttribute reports one attribute's effective value for a path, as git
// resolves it from the checkout rather than from how this machine happens to
// have the file on disk.
func gitAttribute(t *testing.T, root, attribute, path string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "check-attr", attribute, "--", path).Output()
	if err != nil {
		t.Fatalf("git check-attr %s for %s: %v", attribute, path, err)
	}
	// "<path>: <attribute>: <value>", where the value is the final field.
	fields := strings.Split(strings.TrimSpace(string(out)), ": ")
	if len(fields) < 3 {
		t.Fatalf("cannot read the %s attribute of %s from %q", attribute, path, strings.TrimSpace(string(out)))
	}
	return fields[len(fields)-1]
}

// TestCommunityPolicyIsProposedAndUnapprovedUntilAHumanSignsIt pins the honesty of
// the policy document. It must describe itself as not in force, it must not record
// an approval nobody gave, and it must state the reduced guarantee a checksum-only
// distribution carries. The policy is in force for v0.1.0, so the document must name
// that state and the record it rests on rather than describing itself as a proposal.
func TestCommunityPolicyStatesItsApprovalAndItsVariables(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "deployment", "COMMUNITY_UNSIGNED_DISTRIBUTION_POLICY.md"))
	if err != nil {
		t.Fatal(err)
	}
	policy := string(raw)
	for _, required := range []string{
		"in force for v0.1.0",
		"RADANOR_COMMUNITY_UNSIGNED_RELEASE",
		"RADANOR_UNSIGNED_WINDOWS_DISTRIBUTION",
		"record held privately",
		"fails closed",
		"proves nothing about who built the file",
		"Do not disable those protections",
		"Unknown Publisher",
	} {
		if !strings.Contains(policy, required) {
			t.Errorf("the policy document must keep %q", required)
		}
	}

	// The shipped disclosure is what a downloader actually reads, so the two things
	// that could turn an honest unsigned build into a deceptive one are asserted
	// there as well: that a checksum is not proof of authorship, and that the answer
	// to a Windows block is never to switch the protection off.
	notice, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "deployment", "UNSIGNED_DISTRIBUTION_NOTICE.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"It does not prove who built the file",
		"Do not disable SmartScreen",
		"Unknown Publisher",
		"no Authenticode signature",
	} {
		if !strings.Contains(string(notice), required) {
			t.Errorf("the shipped disclosure must keep %q", required)
		}
	}
	// The approval table is the gate on the policy itself: a filled-in name with no
	// date, or a table quietly deleted, would let an engineer enact it alone.
	if !strings.Contains(policy, "Radheshyam Dangi") {
		t.Error("the policy must name whose approval puts it in force")
	}
}
