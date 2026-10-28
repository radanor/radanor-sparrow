package wininstaller

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// communityPayload writes the four files the installer script insists on before it
// reaches any authorization decision, plus a release-variant identity record, so a
// refusal test fails on the rule under test rather than on a missing input. The
// executable is never read: every refusal below happens before the identity check,
// which is itself the point — an unauthorized build must stop earlier than that.
func communityPayload(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	record := "version=0.1.0\nvariant=release\nnumeric=0.1.0.0\nsequence=1\ncommit=" + testCommit +
		"\nbinary." + PortableName + "=" + testSum + "\n"
	if err := os.WriteFile(filepath.Join(dir, BuildInfoName), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{PortableName, "LICENSE", "THIRD_PARTY_NOTICES.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("payload:"+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runInstallerScript(t *testing.T, environment []string, args ...string) (string, error) {
	t.Helper()
	command := exec.Command(bashForTests(t),
		append([]string{filepath.Join(repoRoot(t), "scripts", "build-windows-installer.sh")}, args...)...)
	command.Env = append(os.Environ(), "MAKENSIS="+fakeMakensis(t))
	command.Env = append(command.Env, environment...)
	out, err := command.CombinedOutput()
	return string(out), err
}

// The unsigned mode is gated on a recorded authorization of its own. Without it the
// script must refuse before building, and the refusal has to name both the switch
// and the policy document, so an operator is sent to the decision rather than to a
// guess.
func TestCommunityUnsignedInstallerRequiresTheRecordedAuthorization(t *testing.T) {
	dir := communityPayload(t)
	output, err := runInstallerScript(t, nil, "--community-unsigned", "--portable-dir", dir)
	if err == nil {
		t.Fatalf("an unsigned installer was built with no recorded authorization:\n%s", output)
	}
	for _, required := range []string{"RADANOR_COMMUNITY_UNSIGNED", "COMMUNITY_UNSIGNED_DISTRIBUTION_POLICY.md"} {
		if !strings.Contains(output, required) {
			t.Errorf("the refusal does not mention %s, so it does not point at the authorization it wants:\n%s", required, output)
		}
	}
}

// The unsigned mode must never be a way around the signed one. When signing is
// configured the signed installer is both available and required, so this mode has
// to refuse rather than quietly produce a weaker artifact next to it.
func TestCommunityUnsignedInstallerRefusesWhileSigningIsEnabled(t *testing.T) {
	dir := communityPayload(t)
	output, err := runInstallerScript(t,
		[]string{"RADANOR_COMMUNITY_UNSIGNED=acknowledged", "RADANOR_WINDOWS_SIGNING=enabled"},
		"--community-unsigned", "--portable-dir", dir)
	if err == nil {
		t.Fatalf("--community-unsigned produced an artifact while signing was enabled:\n%s", output)
	}
	if !strings.Contains(output, "--release") {
		t.Errorf("the refusal does not direct the operator to the signed path:\n%s", output)
	}
}

// A run that asks for both is asking for a signed release and an openly unsigned
// artifact at once. The script refuses instead of resolving the contradiction by
// choosing one, because either resolution would misreport the artifact.
func TestCommunityUnsignedAndReleaseCannotBeCombined(t *testing.T) {
	dir := communityPayload(t)
	output, err := runInstallerScript(t, nil, "--release", "--community-unsigned", "--portable-dir", dir)
	if err == nil {
		t.Fatalf("the script accepted --release together with --community-unsigned:\n%s", output)
	}
	if !strings.Contains(output, "Refusing both --release and --community-unsigned") {
		t.Errorf("the refusal is not explicit about the conflict:\n%s", output)
	}
}

// The signed installer path is unchanged by the unsigned one, and it is still
// closed. The licensing metadata now passes, so what stops a release build is the
// next rule in the same block: a public installer may only be built from a clean
// checkout of an exact vX.Y.Z tag. This test exists so the amendment cannot be used
// to claim the signed path was opened, and so the order — licensing, then provenance,
// then signing — stays asserted.
func TestSignedReleaseInstallerRequiresATaggedCleanCheckout(t *testing.T) {
	dir := communityPayload(t)
	output, err := runInstallerScript(t, nil, "--release", "--portable-dir", dir)
	if err == nil {
		t.Fatalf("--release produced an installer outside an exact release tag:\n%s", output)
	}
	if !strings.Contains(output, "Release blocked: a public installer must be built from a clean checkout of an exact vX.Y.Z tag.") {
		t.Errorf("--release must stop at the exact-tag rule, got:\n%s", output)
	}
	// The gate must have run and passed before that refusal. Without this, a build
	// that skipped licensing entirely would satisfy the assertion above.
	if !strings.Contains(output, "licensing metadata satisfies this mode") {
		t.Errorf("the packaging path no longer runs the strict licensing gate first:\n%s", output)
	}
}

// The unsigned mode is gated on a recorded authorization of its own, and on the
// same licensing and provenance rules as the signed one. These are refusal
// boundaries only: the real repository's licensing metadata is never modified, and
// no case here can produce an installer or record a real approval.
//
// The third case is the one that changed when D1 was recorded. With the licensing
// gate passing, a fully authorized unsigned build is refused by the *next* rule —
// no exact release tag — and the test asserts the gate's own success line as well,
// so it cannot pass by the gate being skipped.
func TestCommunityUnsignedApprovalsAndLicensingFailClosed(t *testing.T) {
	dir := communityPayload(t)
	// SIMULATIONS ONLY: these child-process fixture values test refusal boundaries.
	// None of these negative cases can produce an installer or record a real approval.
	for _, tc := range []struct {
		name string
		env  []string
		want []string
	}{
		{
			"missing owner",
			[]string{"RADANOR_COMMUNITY_UNSIGNED=acknowledged", "RADANOR_COMMUNITY_UNSIGNED_RELEASE=", "RADANOR_UNSIGNED_WINDOWS_DISTRIBUTION="},
			[]string{"Community owner approval"},
		},
		{
			"missing Windows approval",
			[]string{"RADANOR_COMMUNITY_UNSIGNED=acknowledged", "RADANOR_COMMUNITY_UNSIGNED_RELEASE=approved", "RADANOR_UNSIGNED_WINDOWS_DISTRIBUTION="},
			[]string{"Separate Windows approval"},
		},
		{
			"licensing passes, release tag missing",
			[]string{"RADANOR_COMMUNITY_UNSIGNED=acknowledged", "RADANOR_COMMUNITY_UNSIGNED_RELEASE=approved", "RADANOR_UNSIGNED_WINDOWS_DISTRIBUTION=approved"},
			[]string{
				"licensing metadata satisfies this mode",
				"Release blocked: a public installer must be built from a clean checkout of an exact vX.Y.Z tag.",
			},
		},
		{
			"Linux signing enabled",
			[]string{"RADANOR_COMMUNITY_UNSIGNED=acknowledged", "RADANOR_RELEASE_SIGNING=enabled"},
			[]string{"use --release"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := append([]string{"RADANOR_WINDOWS_SIGNING=", "RADANOR_RELEASE_SIGNING="}, tc.env...)
			out, err := runInstallerScript(t, env, "--community-unsigned", "--portable-dir", dir)
			if err == nil {
				t.Fatalf("the packaging path accepted this case (%v): %s", tc.want, out)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("expected refusal %q, got: %v %s", want, err, out)
				}
			}
		})
	}
}

// A real licensing defect must still stop a release path, and it must stop it
// before the exact-tag rule — otherwise a metadata problem would be reported as
// "no tag" and the finding would never be seen.
//
// The fixture is a temporary directory carrying this repository's own LICENSE and a
// notices file that genuinely contains an unresolved marker. Nothing in the
// repository is modified. The check runs through scripts/validate-release-licensing.sh,
// which is the wrapper every release path calls, so this is the real gate and not a
// re-implementation of it.
func TestReleasePackagingRefusesInvalidLicensingBeforeTheTagRule(t *testing.T) {
	repo := repoRoot(t)

	fixture := t.TempDir()
	license, err := os.ReadFile(filepath.Join(repo, "LICENSE"))
	if err != nil {
		t.Fatalf("the repository LICENSE cannot be read: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "LICENSE"), license, 0o644); err != nil {
		t.Fatal(err)
	}
	notices := "# Third-party notices\n\n" +
		"- **A system library — a release blocker until counsel records a conclusion.**\n"
	if err := os.WriteFile(filepath.Join(fixture, "THIRD_PARTY_NOTICES.md"), []byte(notices), 0o644); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(bashForTests(t), filepath.Join(repo, "scripts", "validate-release-licensing.sh"), "--root", fixture)
	command.Dir = repo
	out, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("the strict gate accepted a fixture that carries an unresolved marker:\n%s", out)
	}
	for _, want := range []string{"BLOCKED", "metadata.placeholder.free", "THIRD_PARTY_NOTICES.md"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the gate's refusal does not mention %q, so the fixture is not testing the finding it claims:\n%s", want, out)
		}
	}

	// Ordering inside the packaging path: the licensing gate runs before the tag
	// rule, so a licensing defect is never masked by "no release tag".
	script, err := os.ReadFile(filepath.Join(repo, "scripts", "build-windows-installer.sh"))
	if err != nil {
		t.Fatal(err)
	}
	gate := strings.Index(string(script), "go run ./cmd/radanor-release-check")
	tag := strings.Index(string(script), "radanor-version -release")
	if gate < 0 || tag < 0 {
		t.Fatalf("the packaging script no longer contains both a licensing gate (%d) and a tag rule (%d)", gate, tag)
	}
	if gate > tag {
		t.Error("the exact-tag rule now runs before the licensing gate, so a licensing finding would be reported as a missing tag")
	}
}

func TestCommunityReleaseIdentityPredicates(t *testing.T) {
	for _, tc := range []struct{ name, variant, recorded, canonical string }{
		{"dirty", "release", "0.1.0.dirty", "0.1.0.dirty"},
		{"version mismatch", "release", "0.1.0", "0.2.0"},
		{"development identity", "development", "0.1.0", "0.1.0"},
		{"untagged", "release", "0.1.0", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Test the real predicate without simulating legal approval/tag creation.
			cmd := exec.Command(bashForTests(t), "-c", `source "$1"; assert_windows_release_identity "$2" "$3" "$4"`, "identity-test", filepath.Join(repoRoot(t), "scripts/windows-installer-policy.sh"), tc.variant, tc.recorded, tc.canonical)
			if out, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("invalid identity accepted: %s", out)
			}
		})
	}
}

func TestInstallerRejectsRepeatedAndContradictoryModes(t *testing.T) {
	for _, args := range [][]string{{"--release", "--release"}, {"--community-unsigned", "--community-unsigned"}, {"--development", "--release"}, {"--development", "--community-unsigned"}} {
		if out, err := runInstallerScript(t, nil, args...); err == nil || !strings.Contains(out, "Refusing repeated or contradictory") {
			t.Fatalf("mode combination %v: %v %s", args, err, out)
		}
	}
}
