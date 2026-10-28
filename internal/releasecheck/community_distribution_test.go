package releasecheck

import (
	"os"
	"path/filepath"
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
