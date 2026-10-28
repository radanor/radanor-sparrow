package releasecheck

// These tests guard the release process itself rather than the licence text: the
// workflow that would publish an artifact, the checksum manifests that accompany
// it, and the signing scripts. They run without any credentials, because the
// property that matters most right now is that everything refuses to proceed
// without them.

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func script(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "scripts", name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return path
}

func run(t *testing.T, dir string, environment []string, name string, args ...string) (string, error) {
	t.Helper()
	command := exec.Command("bash", append([]string{name}, args...)...)
	command.Dir = dir
	command.Env = mergeEnvironment(os.Environ(), environment)
	var combined bytes.Buffer
	command.Stdout, command.Stderr = &combined, &combined
	err := command.Run()
	return combined.String(), err
}

// mergeEnvironment applies overrides the way a shell does: a later value for a
// name replaces an earlier one. Appending instead would leave two copies of a name
// such as PATH or GNUPGHOME in the environment, and the child would read whichever
// one its C library returns first, so a test could silently run against the
// developer's value rather than its own.
func mergeEnvironment(base, overrides []string) []string {
	merged := make([]string, 0, len(base)+len(overrides))
	index := make(map[string]int, len(base)+len(overrides))
	for _, entry := range append(append([]string{}, base...), overrides...) {
		name, _, _ := strings.Cut(entry, "=")
		if at, seen := index[name]; seen {
			merged[at] = entry
			continue
		}
		index[name] = len(merged)
		merged = append(merged, entry)
	}
	return merged
}

// TestReleaseWorkflowCannotPublishWithoutSatisfyingItsPrerequisites pins the
// wiring that makes the release workflow fail closed: the gate runs first and the
// artifact job depends on it, the Linux artifact is the native-window AppImage
// built through the release path, the packaged helper and release variant are
// checked, and the workflow is only reachable deliberately.
func TestReleaseWorkflowCannotPublishWithoutSatisfyingItsPrerequisites(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(raw)

	for _, required := range []struct{ needle, why string }{
		{"needs: licensing-gate", "the artifact job must depend on the licensing gate"},
		{"go run ./cmd/radanor-release-check", "the gate job must run the strict licensing check"},
		{"bash scripts/build-linux.sh --release --appimage", "the Linux release must be built through the release path, as the native-window AppImage"},
		{"usr/bin/radanor-sparrow-window", "the release must verify that the window component is inside the image"},
		{"variant=release", "the release must verify that the image is a release build"},
		{"scripts/write-release-checksums.sh", "the release must publish checksum manifests"},
		{"scripts/verify-release.sh", "the release must verify the files it is about to publish"},
		{"workflow_dispatch:", "publication must stay a deliberate, manual act"},
		{"tags:", "a tag is the only push that may start a release"},
		{"--require-all-signed", "a signed release must cover every published artifact, not only the AppImage"},
		{"--expect-fingerprint", "verification must be pinned to a published fingerprint, not to a key beside the download"},
		{"RADANOR_RELEASE_FINGERPRINT", "the trusted fingerprint must be a repository variable the signing steps read"},
		{"environment: production-release", "release work must run through the named protected environment"},
		{"needs: [licensing-gate, release-authorization]", "artifact builds must wait for legal and environment authorization"},
		{"RADANOR_PRODUCTION_RELEASE_PROTECTED", "an administrator must attest that environment protections are available and configured"},
		{"GITHUB_REF_TYPE", "the authorization gate must inspect the triggering ref"},
		{"^v[0-9]+\\.[0-9]+\\.[0-9]+$", "only an exact semantic-version tag may authorize artifacts"},
		{"RADANOR_WINDOWS_SIGNING", "Windows release output must require production signing"},
		{"Assert-SparrowAuthenticode.ps1", "Windows signatures must be checked against the approved identity"},
		{"Initialize-SparrowNsis.ps1", "the release must provision the pinned verified NSIS compiler"},
		{"build-windows-installer.sh --release", "the Windows release must include the NSIS installer"},
		{"write-windows-release-checksums.sh", "the Windows release set must carry verified checksums"},
		{"Initialize-SparrowAppImageTool.sh", "the AppImage tool and runtime must be pinned and verified"},
	} {
		if !strings.Contains(workflow, required.needle) {
			t.Errorf("release.yml is missing %q: %s", required.needle, required.why)
		}
	}

	// Development validation must never be reached from the release workflow: the
	// strict gate is the only acceptable mode there.
	if strings.Contains(workflow, "--mode development") {
		t.Error("release.yml must not run development licensing validation")
	}
	// Signing is mandatory for an authorized release, and the switch that arms it is
	// read from the protected environment rather than defaulting to on: a run whose
	// key material is missing fails instead of publishing unsigned bytes as signed.
	if !strings.Contains(workflow, "vars.RADANOR_RELEASE_SIGNING") {
		t.Error("release.yml does not gate signing on an explicit opt-in variable")
	}
	// The workflow must not publish anything to a GitHub Release by itself.
	if strings.Contains(workflow, "gh release") || strings.Contains(workflow, "softprops/action-gh-release") {
		t.Error("release.yml must not publish a GitHub Release automatically")
	}
	if strings.Contains(workflow, "releases/download/continuous") {
		t.Error("release.yml downloads a mutable continuous build tool or runtime")
	}

	// The signing step must cover every published file, and the verification step
	// must demand that full coverage against the pinned fingerprint. Slicing the
	// steps keeps the assertion about the signing path rather than about the file as
	// a whole, where the artifact names also appear in the upload lists.
	signStart := strings.Index(workflow, "name: Sign every published Linux artifact")
	verifyStart := strings.Index(workflow, "name: Verify every signature")
	if signStart < 0 || verifyStart < 0 || verifyStart < signStart {
		t.Fatal("release.yml does not have the expected signing and signature-verification steps")
	}
	signStep := workflow[signStart:verifyStart]
	verifyStep := workflow[verifyStart:]
	for _, name := range []string{"radanor-sparrow", "SHA256SUMS.appimage", "SHA256SUMS.portable"} {
		if !strings.Contains(signStep, name) {
			t.Errorf("the signing step does not sign %s, so a partially signed release could be reported as signed", name)
		}
	}
	for _, flag := range []string{"--require-signature", "--require-all-signed", "--expect-fingerprint"} {
		if !strings.Contains(verifyStep, flag) {
			t.Errorf("the signature-verification step is missing %s", flag)
		}
	}
}

// reviewedActionPins is the inventory of immutable action references this
// repository has actually checked: each SHA was confirmed to be the commit the
// named release tag points at, by resolving the tag through the git protocol, and
// the provenance of that resolution is recorded in
// docs/deployment/RELEASE_SUPPLY_CHAIN.md. Changing a pin therefore means adding
// an entry here with its evidence, which is what stops a pin from being edited to
// a value nobody reviewed.
var reviewedActionPins = map[string]string{
	"fbc6f3992d24b796d5a048ff273f7fcc4a7b6c09": "actions/checkout v5.1.0",
	"924ae3a1cded613372ab5595356fb5720e22ba16": "actions/setup-go v6.5.0",
	"a0853c24544627f65ddf259abe73b1d18a591444": "actions/setup-node v5.0.0",
	"ea165f8d65b6e75b540449e92b4886f43607fa02": "actions/upload-artifact v4.6.2",
	"ec48f7c5447b3140e2b088413ae3a55687bccb6e": "msys2/setup-msys2 v2.33.0",
}

func TestWorkflowActionsArePinnedToFullCommitSHAs(t *testing.T) {
	workflowDir := filepath.Join(repoRoot(t), ".github", "workflows")
	entries, err := os.ReadDir(workflowDir)
	if err != nil {
		t.Fatal(err)
	}
	uses := regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*([^\s#]+)`)
	immutable := regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+@[0-9a-f]{40}$`)
	for _, entry := range entries {
		if entry.IsDir() || (filepath.Ext(entry.Name()) != ".yml" && filepath.Ext(entry.Name()) != ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(workflowDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range uses.FindAllStringSubmatch(string(raw), -1) {
			if strings.HasPrefix(match[1], "./") {
				continue
			}
			if !immutable.MatchString(match[1]) {
				t.Errorf("%s has mutable or non-full action reference %q", entry.Name(), match[1])
				continue
			}
			repository, sha, _ := strings.Cut(match[1], "@")
			reviewed, known := reviewedActionPins[sha]
			if !known {
				t.Errorf("%s pins %s to an unreviewed commit %s; record its tag and provenance in RELEASE_SUPPLY_CHAIN.md and add it to reviewedActionPins", entry.Name(), repository, sha)
				continue
			}
			if !strings.HasPrefix(reviewed, repository+" ") {
				t.Errorf("%s pins %s to %s, which is recorded as %s", entry.Name(), repository, sha, reviewed)
			}
		}
	}
}

func TestAppImageToolProvisioningPinsAndChecksBothExecutables(t *testing.T) {
	path := filepath.Join(repoRoot(t), "validation", "linux", "Initialize-SparrowAppImageTool.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, required := range []string{
		"releases/download/${tool_version}",
		"releases/download/${runtime_version}",
		"tool_sha256=ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0",
		"runtime_sha256=2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d",
		"checksum mismatch; refusing to execute or package unverified bytes",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("AppImage initializer is missing %q", required)
		}
	}
	if strings.Contains(source, "releases/download/continuous") {
		t.Error("AppImage initializer still selects a mutable continuous asset")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "appimagetool-x86_64.AppImage"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := run(t, "", nil, path, "--directory", dir)
	if err == nil {
		t.Fatalf("a cached tool with the wrong digest was accepted:\n%s", output)
	}
	if !strings.Contains(output, "appimagetool checksum mismatch") {
		t.Errorf("checksum refusal was not explicit:\n%s", output)
	}
}

func TestWindowsChecksumManifestRequiresAReleaseVariant(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"RadanorSparrow.exe":             "portable",
		"RadanorSparrow-1.0.0-setup.exe": "setup",
		"LICENSE":                        "license",
		"THIRD_PARTY_NOTICES.md":         "notices",
		"BUILD-INFO":                     "version=1.0.0\nvariant=development\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writer := script(t, "write-windows-release-checksums.sh")
	if output, err := run(t, "", nil, writer, dir); err == nil {
		t.Fatalf("development Windows artifacts received a release manifest:\n%s", output)
	}
	if err := os.WriteFile(filepath.Join(dir, "BUILD-INFO"), []byte("version=1.0.0\nvariant=release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := run(t, "", nil, writer, dir)
	if err != nil {
		t.Fatalf("release Windows manifest failed: %v\n%s", err, output)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS.windows"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"RadanorSparrow.exe", "RadanorSparrow-1.0.0-setup.exe", "LICENSE", "THIRD_PARTY_NOTICES.md", "BUILD-INFO"} {
		if !strings.Contains(string(manifest), name) {
			t.Errorf("Windows checksum manifest does not cover %s", name)
		}
	}
}

// makeArtifacts writes a directory that looks like a built Linux release.
func makeArtifacts(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"RadanorSparrow-1.0.0-x86_64.AppImage": "image payload",
		"radanor-sparrow":                      "portable supervisor",
		"LICENSE":                              "license text",
		"THIRD_PARTY_NOTICES.md":               "# notices",
		// The provenance record the build writes beside the binaries. Checksums are
		// only written for an artifact whose own record says it came from the
		// release path.
		"BUILD-INFO": "version=1.0.0\nvariant=release\narchitecture=amd64\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestChecksumManifestsCoverEveryPublishedArtifact(t *testing.T) {
	dir := makeArtifacts(t)
	checksums := script(t, "write-release-checksums.sh")
	verify := script(t, "verify-release.sh")

	output, err := run(t, "", nil, checksums, dir)
	if err != nil {
		t.Fatalf("writing checksums failed: %v\n%s", err, output)
	}
	appimage, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS.appimage"))
	if err != nil {
		t.Fatalf("the AppImage manifest was not written: %v", err)
	}
	if !strings.Contains(string(appimage), "RadanorSparrow-1.0.0-x86_64.AppImage") {
		t.Errorf("the AppImage manifest does not cover the AppImage:\n%s", appimage)
	}
	portable, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS.portable"))
	if err != nil {
		t.Fatalf("the portable manifest was not written: %v", err)
	}
	for _, name := range []string{"radanor-sparrow", "LICENSE", "THIRD_PARTY_NOTICES.md"} {
		if !strings.Contains(string(portable), name) {
			t.Errorf("the portable manifest does not cover %s:\n%s", name, portable)
		}
	}
	// The recorded hash must be the file's real hash, not a placeholder.
	payload, err := os.ReadFile(filepath.Join(dir, "radanor-sparrow"))
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("%x", sha256.Sum256(payload)); !strings.Contains(string(portable), want) {
		t.Errorf("the portable manifest does not record the real digest %s", want)
	}

	if output, err := run(t, "", nil, verify, dir); err != nil {
		t.Fatalf("verifying an intact release directory failed: %v\n%s", err, output)
	}

	// A modified artifact must be caught.
	if err := os.WriteFile(filepath.Join(dir, "radanor-sparrow"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err = run(t, "", nil, verify, dir)
	if err == nil {
		t.Fatalf("a tampered artifact passed verification:\n%s", output)
	}
	if !strings.Contains(output, "checksum") && !strings.Contains(output, "Verification failed") {
		t.Errorf("the verification failure does not explain itself:\n%s", output)
	}
	if err := os.WriteFile(filepath.Join(dir, "radanor-sparrow"), payload, 0o644); err != nil {
		t.Fatal(err)
	}

	// Two AppImages would make the manifest ambiguous, so the writer refuses.
	if err := os.WriteFile(filepath.Join(dir, "RadanorSparrow-1.1.0-x86_64.AppImage"), []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := run(t, "", nil, checksums, dir); err == nil {
		t.Fatalf("a second AppImage was accepted:\n%s", output)
	}
	if err := os.Remove(filepath.Join(dir, "RadanorSparrow-1.1.0-x86_64.AppImage")); err != nil {
		t.Fatal(err)
	}

	// A missing release document stops the manifests too: an artifact without its
	// licence must not be published with a manifest that implies it is complete.
	if err := os.Remove(filepath.Join(dir, "LICENSE")); err != nil {
		t.Fatal(err)
	}
	if output, err := run(t, "", nil, checksums, dir); err == nil {
		t.Fatalf("a release directory without LICENSE was accepted:\n%s", output)
	}
}

// TestDevelopmentBuildsCannotAcquireReleaseChecksums is the checksum half of the
// "never present a development artifact as a release" rule. The manifests are what
// the release workflow uploads next to the binaries, so a development build that
// could get them would look like a release to anyone who downloads the directory.
func TestDevelopmentBuildsCannotAcquireReleaseChecksums(t *testing.T) {
	checksums := script(t, "write-release-checksums.sh")

	for _, variant := range []struct {
		name    string
		record  string
		missing bool
	}{
		{"development", "version=0.1.0-dev.1+gabc\nvariant=development\n", false},
		{"unlabelled", "version=1.0.0\n", false},
		{"absent", "", true},
	} {
		t.Run(variant.name, func(t *testing.T) {
			dir := makeArtifacts(t)
			provenance := filepath.Join(dir, "BUILD-INFO")
			if variant.missing {
				if err := os.Remove(provenance); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(provenance, []byte(variant.record), 0o644); err != nil {
				t.Fatal(err)
			}

			output, err := run(t, "", nil, checksums, dir)
			if err == nil {
				t.Fatalf("release checksums were written for a %s build:\n%s", variant.name, output)
			}
			for _, manifest := range []string{"SHA256SUMS.appimage", "SHA256SUMS.portable"} {
				if _, err := os.Stat(filepath.Join(dir, manifest)); !os.IsNotExist(err) {
					t.Errorf("%s exists although the artifact is not a release build", manifest)
				}
			}
		})
	}
}

func TestVerificationRefusesIncompleteEvidence(t *testing.T) {
	empty := t.TempDir()
	verify := script(t, "verify-release.sh")

	// Nothing to verify against.
	if output, err := run(t, "", nil, verify, empty); err == nil {
		t.Fatalf("an empty directory verified:\n%s", output)
	}
	// A required signature without a key is a usage error, not a pass: a signature
	// with no trusted public key proves nothing.
	output, err := run(t, "", nil, verify, "--require-signature", empty)
	if err == nil {
		t.Fatalf("--require-signature without a key was accepted:\n%s", output)
	}
	if !strings.Contains(output, "--key") {
		t.Errorf("the refusal does not ask for the public key:\n%s", output)
	}
	// A key without a trusted fingerprint is a usage error too: a valid signature
	// by any key the caller happens to hold is not evidence of a publisher.
	if output, err := run(t, "", nil, verify, "--require-signature", "--key", filepath.Join(empty, "key.asc"), empty); err == nil {
		t.Fatalf("--require-signature without a trusted fingerprint was accepted:\n%s", output)
	}
	// A directory with checksums but no signature cannot satisfy --require-signature.
	dir := makeArtifacts(t)
	if output, err := run(t, "", nil, script(t, "write-release-checksums.sh"), dir); err != nil {
		t.Fatalf("writing checksums failed: %v\n%s", err, output)
	}
	if output, err := run(t, "", nil, verify, "--require-signature", "--key", filepath.Join(dir, "LICENSE"), dir); err == nil {
		t.Fatalf("--require-signature passed without any signature:\n%s", output)
	}
	// A partial signature set must not pass when complete coverage is demanded.
	expected := "0123456789ABCDEF0123456789ABCDEF01234567"
	if output, err := run(t, "", nil, verify, "--require-all-signed", "--require-signature",
		"--expect-fingerprint", expected, "--key", filepath.Join(dir, "LICENSE"), dir); err == nil {
		t.Fatalf("--require-all-signed passed with no signatures at all:\n%s", output)
	}
}

func TestSigningRefusesWithoutAKey(t *testing.T) {
	dir := makeArtifacts(t)
	artifact := filepath.Join(dir, "RadanorSparrow-1.0.0-x86_64.AppImage")
	output, err := run(t, "", nil, script(t, "sign-release.sh"), artifact)
	if err == nil {
		t.Fatalf("signing without a key succeeded:\n%s", output)
	}
	if !strings.Contains(output, "BLOCKED") {
		t.Errorf("the refusal does not state that release signing is blocked:\n%s", output)
	}
	if _, err := os.Stat(artifact + ".asc"); !os.IsNotExist(err) {
		t.Error("a signature file exists although no key was available")
	}
}

// TestDevelopmentTreeCannotProduceAReleaseArtifact pins the second gate behind the
// licensing one: a development checkout has no release version, and whatever version
// it does have is labelled development rather than release. The licensing finding is
// tested elsewhere; this test is about the artifact identity, so it stays true even
// after the licence is resolved.
func TestDevelopmentTreeCannotProduceAReleaseArtifact(t *testing.T) {
	root := repoRoot(t)
	environment := append(os.Environ(), "GOTOOLCHAIN=local")

	release := exec.Command("go", "run", "./cmd/radanor-version", "-release")
	release.Dir = root
	release.Env = environment
	combined, err := release.CombinedOutput()
	if err == nil {
		t.Fatalf("a development checkout produced a release version:\n%s", combined)
	}
	if !strings.Contains(string(combined), "tag") {
		t.Errorf("the refusal does not explain that an exact vX.Y.Z tag is required:\n%s", combined)
	}

	// The variant the build scripts inject must be development, otherwise an artifact
	// built from this tree could present itself as a release build.
	variant := exec.Command("go", "run", "./cmd/radanor-version", "-field", "variant")
	variant.Dir = root
	variant.Env = environment
	out, err := variant.Output()
	if err != nil {
		t.Fatalf("reading the build variant failed: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "development" {
		t.Errorf("this checkout reports variant=%q; a tree that is not an exact release tag must build as development", got)
	}
}

// generateTestKey creates a throwaway signing key in a temporary GnuPG home and
// returns its fingerprint. It never touches the developer's keyring and never
// creates a project or production key; if gpg is unavailable the test reports that
// rather than passing silently.
func generateTestKey(t *testing.T, uid string) (environment []string, fingerprint string) {
	t.Helper()
	if _, err := exec.LookPath("gpg"); err != nil {
		// On a Linux CI runner this is a configuration problem, not a reason to drop
		// the signature coverage silently; only a local machine may skip.
		if runtime.GOOS == "linux" && os.Getenv("CI") != "" {
			t.Fatalf("gpg is not installed, so the signing mechanism cannot be exercised here: %v", err)
		}
		t.Skip("gpg is not installed, so the signing mechanism cannot be exercised here")
	}
	keyHome := filepath.Join(t.TempDir(), "gnupg")
	if err := os.MkdirAll(keyHome, 0o700); err != nil {
		t.Fatal(err)
	}
	environment = []string{"GNUPGHOME=" + keyHome}
	generate := exec.Command("gpg", "--batch", "--passphrase", "", "--quiet", "--quick-gen-key", uid, "ed25519", "sign", "0")
	generate.Env = append(os.Environ(), environment...)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Skipf("gpg cannot generate a throwaway key here (%v): %s", err, output)
	}
	list := exec.Command("gpg", "--batch", "--with-colons", "--list-secret-keys")
	list.Env = append(os.Environ(), environment...)
	listed, err := list.Output()
	if err != nil {
		t.Fatalf("listing the test key failed: %v", err)
	}
	for _, line := range strings.Split(string(listed), "\n") {
		if strings.HasPrefix(line, "fpr:") {
			fingerprint = strings.Split(line, ":")[9]
			break
		}
	}
	if fingerprint == "" {
		t.Fatal("the throwaway test key has no fingerprint")
	}
	return environment, fingerprint
}

// signReleaseSet signs the four artifacts a release publishes and returns the
// public key file written beside the AppImage.
func signReleaseSet(t *testing.T, dir, fingerprint string, environment []string) string {
	t.Helper()
	output, err := run(t, "", environment, script(t, "sign-release.sh"),
		"--key", fingerprint, "--expect-fingerprint", fingerprint,
		filepath.Join(dir, "RadanorSparrow-1.0.0-x86_64.AppImage"),
		filepath.Join(dir, "radanor-sparrow"),
		filepath.Join(dir, "SHA256SUMS.appimage"),
		filepath.Join(dir, "SHA256SUMS.portable"))
	if err != nil {
		t.Fatalf("signing the release set failed: %v\n%s", err, output)
	}
	publicKey := filepath.Join(dir, "RadanorSparrow-1.0.0-x86_64.AppImage.asc.pub")
	if _, err := os.Stat(publicKey); err != nil {
		t.Fatalf("the public key was not exported: %v", err)
	}
	return publicKey
}

// TestSigningMechanismWorksWithAProvidedKey proves the mechanism end to end using a
// throwaway key generated in a temporary GnuPG home.
func TestSigningMechanismWorksWithAProvidedKey(t *testing.T) {
	dir := makeArtifacts(t)
	if output, err := run(t, "", nil, script(t, "write-release-checksums.sh"), dir); err != nil {
		t.Fatalf("writing checksums failed: %v\n%s", err, output)
	}
	environment, fingerprint := generateTestKey(t, "Release Gate Test <release-gate-test@example.invalid>")

	artifact := filepath.Join(dir, "RadanorSparrow-1.0.0-x86_64.AppImage")
	output, err := run(t, "", environment, script(t, "sign-release.sh"), "--key", fingerprint, "--expect-fingerprint", fingerprint, artifact)
	if err != nil {
		t.Fatalf("signing with a provided key failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, fingerprint) {
		t.Errorf("the signing output does not name the fingerprint:\n%s", output)
	}
	signature := artifact + ".asc"
	publicKey := signature + ".pub"
	if _, err := os.Stat(signature); err != nil {
		t.Fatalf("no signature was written: %v", err)
	}
	if _, err := os.Stat(publicKey); err != nil {
		t.Fatalf("the public key was not exported: %v", err)
	}

	verify := script(t, "verify-release.sh")
	output, err = run(t, "", nil, verify, "--require-signature", "--expect-fingerprint", fingerprint, "--key", publicKey, dir)
	if err != nil {
		t.Fatalf("verifying a signed release failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, fingerprint) {
		t.Errorf("verification does not report the signing fingerprint:\n%s", output)
	}

	// A signature over different bytes must fail, and so must a signature checked
	// against a key that does not belong to it.
	if err := os.WriteFile(artifact, []byte("tampered after signing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := run(t, "", nil, verify, "--require-signature", "--expect-fingerprint", fingerprint, "--key", publicKey, dir); err == nil {
		t.Fatalf("a tampered artifact verified:\n%s", output)
	}
}

// TestVerificationRejectsASignatureFromAnotherKey is the property the audit found
// missing: a cryptographically valid signature by a key nobody trusted must fail,
// even when that key's public key sits in the same directory as the artifact.
func TestVerificationRejectsASignatureFromAnotherKey(t *testing.T) {
	dir := makeArtifacts(t)
	if output, err := run(t, "", nil, script(t, "write-release-checksums.sh"), dir); err != nil {
		t.Fatalf("writing checksums failed: %v\n%s", err, output)
	}
	trustedEnv, trusted := generateTestKey(t, "Trusted Publisher <trusted@example.invalid>")
	// A second, unrelated key signs the same file. Its own public key accompanies
	// the artifact, exactly as an attacker would ship it.
	foreignEnv, foreign := generateTestKey(t, "Foreign Signer <foreign@example.invalid>")
	if trusted == foreign {
		t.Fatal("the two throwaway keys are the same key")
	}
	publicKey := signReleaseSet(t, dir, trusted, trustedEnv)

	artifact := filepath.Join(dir, "radanor-sparrow")
	if output, err := run(t, "", foreignEnv, script(t, "sign-release.sh"), "--key", foreign, artifact); err != nil {
		t.Fatalf("foreign signing failed: %v\n%s", err, output)
	}
	// The foreign signature is valid on its own; it is simply not by the trusted key.
	if _, err := os.Stat(artifact + ".asc.pub"); err != nil {
		t.Fatalf("the foreign public key is missing: %v", err)
	}

	verify := script(t, "verify-release.sh")
	output, err := run(t, "", nil, verify, "--require-all-signed", "--require-signature",
		"--expect-fingerprint", trusted, "--key", publicKey, dir)
	if err == nil {
		t.Fatalf("a signature by a foreign key passed verification:\n%s", output)
	}
	if !strings.Contains(output, "UNTRUSTED KEY") {
		t.Errorf("the refusal does not name the untrusted key whose signature was found:\n%s", output)
	}
	if strings.Contains(output, foreign) {
		t.Errorf("the foreign fingerprint was treated as acceptable:\n%s", output)
	}
}

// TestPartialSignatureCoverageIsRejected: a release whose portable executable or
// manifests are unsigned must not be reported as signed. Each missing signature is
// named, so the failure is actionable rather than a bare count.
func TestPartialSignatureCoverageIsRejected(t *testing.T) {
	dir := makeArtifacts(t)
	if output, err := run(t, "", nil, script(t, "write-release-checksums.sh"), dir); err != nil {
		t.Fatalf("writing checksums failed: %v\n%s", err, output)
	}
	environment, fingerprint := generateTestKey(t, "Coverage Test <coverage@example.invalid>")
	publicKey := signReleaseSet(t, dir, fingerprint, environment)
	verify := script(t, "verify-release.sh")
	args := []string{"--require-all-signed", "--require-signature", "--expect-fingerprint", fingerprint, "--key", publicKey, dir}

	if output, err := run(t, "", nil, verify, args...); err != nil {
		t.Fatalf("a fully signed release set failed verification: %v\n%s", err, output)
	}

	for _, unsigned := range []string{"radanor-sparrow", "SHA256SUMS.appimage", "SHA256SUMS.portable"} {
		path := filepath.Join(dir, unsigned)
		signature := path + ".asc"
		if err := os.Rename(signature, signature+".off"); err != nil {
			t.Fatal(err)
		}
		output, err := run(t, "", nil, verify, args...)
		if err == nil {
			t.Errorf("verification passed while %s was unsigned:\n%s", unsigned, output)
		} else if !strings.Contains(output, unsigned) {
			t.Errorf("the refusal does not name the unsigned artifact %s:\n%s", unsigned, output)
		}
		if err := os.Rename(signature+".off", signature); err != nil {
			t.Fatal(err)
		}
	}
}

// TestSigningRefusesADevelopmentBuildWithoutTheDryRunFlag keeps a signature from
// making a development build look like an approved release. The artifact's own
// provenance record decides, and a deliberate dry run says so in its output.
func TestSigningRefusesADevelopmentBuildWithoutTheDryRunFlag(t *testing.T) {
	environment, fingerprint := generateTestKey(t, "Development Guard <dev-guard@example.invalid>")
	dir := makeArtifacts(t)
	if err := os.WriteFile(filepath.Join(dir, "BUILD-INFO"),
		[]byte("version=0.1.0-dev.1+gabc1234\nvariant=development\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(dir, "radanor-sparrow")
	sign := script(t, "sign-release.sh")

	output, err := run(t, "", environment, sign, "--key", fingerprint, artifact)
	if err == nil {
		t.Fatalf("a development build was signed as if it were a release:\n%s", output)
	}
	if _, statErr := os.Stat(artifact + ".asc"); !os.IsNotExist(statErr) {
		t.Error("a signature exists although the artifact is a development build")
	}
	if !strings.Contains(output, "--development") {
		t.Errorf("the refusal does not say how to sign a development build deliberately:\n%s", output)
	}

	// The dry-run flag is the only way through, and it says what the result is.
	output, err = run(t, "", environment, sign, "--key", fingerprint, "--development", artifact)
	if err != nil {
		t.Fatalf("the deliberate development dry run failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "NOT a release") {
		t.Errorf("the dry run does not state that the result is not a release:\n%s", output)
	}
	if _, statErr := os.Stat(artifact + ".asc"); statErr != nil {
		t.Errorf("the dry run wrote no signature: %v", statErr)
	}
}

// workflowStepNames lists the step names in a workflow, so a failure to find one
// says what the file did contain instead of only what was expected.
func workflowStepNames(workflow string) []string {
	names := make([]string, 0, 16)
	for _, line := range strings.Split(workflow, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- name: ") {
			continue
		}
		names = append(names, strings.TrimPrefix(trimmed, "- name: "))
	}
	return names
}

// indentOf counts the leading spaces of a YAML line, which is how a step's `run:
// |` block is delimited without a YAML dependency.
func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// stepScript returns the shell body of the named step in a workflow file, so a
// fail-closed gate can be executed rather than only string-matched. A workflow
// expression cannot prove a control works; running its script can.
func stepScript(t *testing.T, workflow, name string) string {
	t.Helper()
	// Line endings are a property of the checkout, not of the file: the CI matrix
	// includes windows-2025, where Git for Windows writes CRLF, and a marker that
	// ends in a bare "\n" never matches there even though the step is present. The
	// block is normalised before it is parsed and executed, so the gate is analysed
	// from the same bytes on every runner.
	workflow = strings.ReplaceAll(workflow, "\r\n", "\n")
	marker := "- name: " + name + "\n"
	index := strings.Index(workflow, marker)
	if index < 0 {
		t.Fatalf("the workflow has no step named %q (%d bytes read; step names found: %v)", name, len(workflow), workflowStepNames(workflow))
	}
	lines := strings.Split(workflow[index+len(marker):], "\n")
	runAt := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "run: |" {
			runAt = i
			break
		}
		if i > 0 && strings.TrimSpace(line) != "" && strings.HasPrefix(strings.TrimSpace(line), "- ") {
			break
		}
	}
	if runAt < 0 {
		t.Fatalf("the step %q has no run block", name)
	}
	base := indentOf(lines[runAt])
	end := runAt + 1
	for end < len(lines) {
		if strings.TrimSpace(lines[end]) != "" && indentOf(lines[end]) <= base {
			break
		}
		end++
	}
	body := lines[runAt+1 : end]
	strip := -1
	for _, line := range body {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if n := indentOf(line); strip < 0 || n < strip {
			strip = n
		}
	}
	if strip < 0 {
		t.Fatalf("the step %q has an empty run block", name)
	}
	out := make([]string, 0, len(body))
	for _, line := range body {
		if strings.TrimSpace(line) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, line[strip:])
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// releaseWorkflow reads release.yml with CRLF normalised, so every assertion in
// this file sees the same text on Linux, macOS and a Windows checkout.
func releaseWorkflow(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}

// authorizationFixture writes a repository whose main line and a side branch
// diverge, each carrying its own release tag, so the authorization gate's tag,
// version and ancestry rules can be exercised without GitHub. It also puts a
// stand-in `go` on PATH: the gate only needs the release version rule's answer,
// and that rule has its own tests.
func authorizationFixture(t *testing.T) (work, bin string) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	work = filepath.Join(root, "work")
	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Release Gate", "GIT_AUTHOR_EMAIL=gate@example.invalid",
			"GIT_COMMITTER_NAME=Release Gate", "GIT_COMMITTER_EMAIL=gate@example.invalid",
			"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "--quiet", "--bare", "-b", "main", origin)
	git("init", "--quiet", "-b", "main", work)
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(work, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.txt", "main\n")
	git("-C", work, "add", "main.txt")
	git("-C", work, "commit", "--quiet", "-m", "main line")
	git("-C", work, "tag", "-a", "v1.2.3", "-m", "release 1.2.3")
	git("-C", work, "checkout", "--quiet", "-b", "side")
	write("side.txt", "side\n")
	git("-C", work, "add", "side.txt")
	git("-C", work, "commit", "--quiet", "-m", "unreviewed work")
	git("-C", work, "tag", "-a", "v1.2.4", "-m", "release 1.2.4")
	git("-C", work, "checkout", "--quiet", "main")
	git("-C", work, "remote", "add", "origin", origin)
	git("-C", work, "push", "--quiet", "origin", "main")

	bin = filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\nprintf '%s\\n' \"${FAKE_RELEASE_VERSION:-}\"\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	return work, bin
}

// TestReleaseAuthorizationGateRefusesUntrustedRuns runs the release
// authorization steps themselves. An environment reviewer approves a run, not a
// commit, so the gate has to establish the properties it can establish: an
// attested environment, an exact tag, a tag that names a commit on main, one
// version shared by the tag and the build, and a requested version that agrees.
func TestReleaseAuthorizationGateRefusesUntrustedRuns(t *testing.T) {
	workflow := releaseWorkflow(t)
	gate := stepScript(t, workflow, "Require an attested protected environment and exact release tag") +
		"\n" + stepScript(t, workflow, "Require the tag to name a reviewed commit on main")
	work, bin := authorizationFixture(t)
	git := func(args ...string) {
		command := exec.Command("git", args...)
		command.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	for _, testCase := range []struct {
		name        string
		tag         string
		refType     string
		refName     string
		attestation string
		requested   string
		built       string
		wantFailure string
	}{
		{
			name:    "no protection attestation",
			tag:     "v1.2.3",
			refType: "tag", refName: "v1.2.3", built: "1.2.3",
			wantFailure: "protection is not verified",
		},
		{
			name:    "not triggered by a tag",
			refType: "branch", refName: "main", attestation: "verified", built: "1.2.3",
			wantFailure: "exact vX.Y.Z tag",
		},
		{
			name:    "tag is not a release version",
			tag:     "v1.2.3",
			refType: "tag", refName: "v1.2.3-rc1", attestation: "verified", built: "1.2.3",
			wantFailure: "exact vX.Y.Z tag",
		},
		{
			name:    "requested version disagrees with the tag",
			tag:     "v1.2.3",
			refType: "tag", refName: "v1.2.3", attestation: "verified", requested: "1.2.4", built: "1.2.3",
			wantFailure: "does not match tag version",
		},
		{
			name:    "tag on an unreviewed branch",
			tag:     "v1.2.4",
			refType: "tag", refName: "v1.2.4", attestation: "verified", built: "1.2.4",
			wantFailure: "does not point at a commit on main",
		},
		{
			name:    "tag and build version disagree",
			tag:     "v1.2.3",
			refType: "tag", refName: "v1.2.3", attestation: "verified", built: "1.2.4",
			wantFailure: "but this checkout builds as 1.2.4",
		},
		{
			name:    "authorized exact tag on main",
			tag:     "v1.2.3",
			refType: "tag", refName: "v1.2.3", attestation: "verified", requested: "1.2.3", built: "1.2.3",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			target := testCase.tag
			if target == "" {
				target = "main"
			}
			git("-C", work, "checkout", "--quiet", "--detach", target)
			environment := []string{
				"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"PROTECTION_ATTESTATION=" + testCase.attestation,
				"REQUESTED_VERSION=" + testCase.requested,
				"GITHUB_REF_TYPE=" + testCase.refType,
				"GITHUB_REF_NAME=" + testCase.refName,
				"FAKE_RELEASE_VERSION=" + testCase.built,
			}
			output, err := run(t, work, environment, "-c", gate)
			if testCase.wantFailure == "" {
				if err != nil {
					t.Fatalf("an authorized release run was refused: %v\n%s", err, output)
				}
				if !strings.Contains(output, "Authorized release candidate: v1.2.3") {
					t.Errorf("the authorized run does not report what it authorized:\n%s", output)
				}
				return
			}
			if err == nil {
				t.Fatalf("the gate accepted %s:\n%s", testCase.name, output)
			}
			if !strings.Contains(output, testCase.wantFailure) {
				t.Errorf("the refusal does not explain %q:\n%s", testCase.wantFailure, output)
			}
		})
	}
}

// TestReleaseArtifactsCannotReachAPublisherWithoutAuthorization pins the shape of
// the dependency graph and the order of the Windows stages: nothing may run
// without the gate, and no signature check may be skipped to reach an upload.
func TestReleaseArtifactsCannotReachAPublisherWithoutAuthorization(t *testing.T) {
	workflow := releaseWorkflow(t)

	for _, required := range []struct{ needle, why string }{
		{"needs: [licensing-gate, release-authorization]", "artifact builds must wait for both the licensing gate and the authorization gate"},
		{"refs/heads/main:refs/remotes/origin/main", "the tag must be shown to name a commit on the protected main line"},
		{"go run ./cmd/radanor-version -release", "the build version must be proven equal to the tag before artifacts exist"},
		{"radanor-sparrow-windows-amd64", "the Windows release set must be uploaded"},
		{"dist/windows/RadanorSparrow-*-setup.exe", "the installer, not only the portable executable, is the Windows download"},
		{"dist/windows/SHA256SUMS.windows", "the Windows release set must carry a checksum manifest"},
		{"Expected exactly one Windows setup executable", "a missing or ambiguous installer must fail rather than leave the artifact tests without one"},
		{"Required installer test did not execute successfully", "the artifact tests skip themselves without an artifact, so the run must require actual PASS events"},
	} {
		if !strings.Contains(workflow, required.needle) {
			t.Errorf("release.yml is missing %q: %s", required.needle, required.why)
		}
	}
	// A step that is allowed to fail is not a gate. None may be.
	if strings.Contains(workflow, "continue-on-error") {
		t.Error("release.yml allows a step to fail, so a failed gate could still reach an upload")
	}

	// Sign, then package, then verify: the installer must be built from the signed
	// portable executable and checksummed only after the final signatures exist.
	portableSignature := strings.Index(workflow, "name: Require and verify signed Windows portable executable")
	installerBuild := strings.Index(workflow, "build-windows-installer.sh --release")
	finalSignature := strings.Index(workflow, "name: Verify final Windows Authenticode signatures and checksums")
	upload := strings.Index(workflow, "name: Upload Windows release")
	if portableSignature < 0 || installerBuild < 0 || finalSignature < 0 || upload < 0 {
		t.Fatal("release.yml does not have the expected Windows release stages")
	}
	if !(portableSignature < installerBuild && installerBuild < finalSignature && finalSignature < upload) {
		t.Errorf("the Windows stages are out of order (portable %d, installer %d, final %d, upload %d): an unsigned payload must not be packaged or uploaded",
			portableSignature, installerBuild, finalSignature, upload)
	}
	checksums := strings.Index(workflow, "write-windows-release-checksums.sh")
	if checksums < 0 || checksums > upload {
		t.Error("the Windows checksum manifest must be written before the upload")
	}
}

// TestAppImageToolchainCannotFallBackToMutableOrUnverifiedInputs is the packaging
// half of the toolchain rule: the release path must refuse to let appimagetool
// fetch its own runtime, and the initializer must verify both inputs before either
// is executable.
func TestAppImageToolchainCannotFallBackToMutableOrUnverifiedInputs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "build-appimage.sh"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, required := range []string{
		"Release AppImage requires a verified APPIMAGE_RUNTIME",
		"runtime_args=(--runtime-file \"$APPIMAGE_RUNTIME\")",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("build-appimage.sh is missing %q", required)
		}
	}
	// The runtime fallback must be a development-only convenience: inside a
	// release build the script must stop instead of letting the tool reach the
	// network for whatever runtime it ships that day. The block between the two
	// markers is the whole decision, so it is checked as one unit.
	start := strings.Index(source, "runtime_args=()")
	fallback := strings.Index(source, "appimagetool fetches its type-2 runtime")
	if start < 0 || fallback < 0 || fallback < start {
		t.Fatal("build-appimage.sh no longer has the runtime decision block this test inspects")
	}
	decision := source[start:fallback]
	for _, required := range []string{"if (( release )); then", "Release AppImage requires a verified APPIMAGE_RUNTIME", "exit 1"} {
		if !strings.Contains(decision, required) {
			t.Errorf("the runtime decision allows a release build to fetch its own runtime: %q is missing", required)
		}
	}
}

// TestSigningRefusesAKeyThatIsNotTheTrustedFingerprint turns a misconfigured CI
// secret into a failure instead of a release signed by the wrong key.
func TestSigningRefusesAKeyThatIsNotTheTrustedFingerprint(t *testing.T) {
	_, trusted := generateTestKey(t, "Trusted Publisher <trusted@example.invalid>")
	environment, foreign := generateTestKey(t, "Wrong Key <wrong@example.invalid>")
	if trusted == foreign {
		t.Fatal("the two throwaway keys are the same key")
	}
	dir := makeArtifacts(t)
	artifact := filepath.Join(dir, "radanor-sparrow")
	output, err := run(t, "", environment, script(t, "sign-release.sh"),
		"--key", foreign, "--expect-fingerprint", trusted, artifact)
	if err == nil {
		t.Fatalf("signing with a key that is not the trusted fingerprint succeeded:\n%s", output)
	}
	if _, statErr := os.Stat(artifact + ".asc"); !os.IsNotExist(statErr) {
		t.Error("a signature file exists although the key did not match the trusted fingerprint")
	}
}
