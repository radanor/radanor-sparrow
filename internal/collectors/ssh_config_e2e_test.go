//go:build linux

package collectors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// These tests drive the *production* collector body (collectSSHDConfig, the
// exact function SSHServerCollector.Collect calls) from a configuration
// fixture, then feed its observation into the real check definitions. Nothing
// is hand-assembled: the CE-5 review's highest-severity finding was that the
// collector parsed directives correctly and then threw them away before the
// evaluator could see them, which no observation-level test could catch.

// sshFixture serves configuration content by path and can imitate a permission
// refusal, so the real permission-denied branch is exercised rather than
// described.
type sshFixture struct {
	files  map[string]string
	denied map[string]bool
}

func (f sshFixture) read(path string) (string, error) {
	if f.denied[path] {
		return "", &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
	}
	if text, ok := f.files[path]; ok {
		return text, nil
	}
	// Include targets are resolved with the real filesystem lookup, exactly as
	// production does, so fragments must exist as files rather than only as map
	// entries.
	text, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(text), nil
}

// runSSHFixture builds an sshd_config fixture inside the test's own directory
// tree, runs the real collector over it, stamps the observation the way the
// scanner does, and returns it.
func runSSHFixture(t *testing.T, main string, extra map[string]string, denied bool) assessment.Observation {
	t.Helper()
	root := t.TempDir()
	base := filepath.Join(root, "sshd_config")
	files := map[string]string{base: main}
	for rel, content := range extra {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("fixture directory: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("fixture file: %v", err)
		}
	}
	fx := sshFixture{files: files, denied: map[string]bool{}}
	if denied {
		fx.denied[base] = true
	}
	obs, err := collectSSHDConfig(base, true, nil, fx.read)
	if err != nil {
		t.Fatalf("collector error: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("collector returned %d observations, want 1", len(obs))
	}
	// The scanner owns the observation timestamp; mirror it so freshness holds.
	obs[0].CollectedAt = time.Now()
	return obs[0]
}

func evaluateSSH(t *testing.T, obs assessment.Observation, checkID string) assessment.CheckResult {
	t.Helper()
	return checkByID(linuxChecks, checkID).Evaluate(obs)
}

func sshValue(t *testing.T, obs assessment.Observation, key string) string {
	t.Helper()
	v, ok := lookupValue(obs, key)
	if !ok {
		t.Fatalf("evidence key %q missing (keys: %v)", key, evidenceKeys(obs))
	}
	return v.Text
}

// sshCount reads an integer evidence value; the collector's directive counters
// are ints, so reading them as text would silently compare against "".
func sshCount(t *testing.T, obs assessment.Observation, key string) int64 {
	t.Helper()
	n, ok := intValue(obs, key)
	if !ok {
		t.Fatalf("integer evidence key %q missing (keys: %v)", key, evidenceKeys(obs))
	}
	return n
}

func evidenceKeys(obs assessment.Observation) []string {
	keys := make([]string, 0, len(obs.Values))
	for _, v := range obs.Values {
		keys = append(keys, v.Key)
	}
	return keys
}

// TestSSHCollectorToEvaluatorSecureConfiguration drives a genuinely secure
// configuration through the collector and asserts the evaluators see it.
func TestSSHCollectorToEvaluatorSecureConfiguration(t *testing.T) {
	obs := runSSHFixture(t, "PermitRootLogin no\nPasswordAuthentication no\nPermitEmptyPasswords no\n", nil, false)
	if obs.Status != assessment.Collected {
		t.Fatalf("status = %q (%s), want collected", obs.Status, obs.Error)
	}
	for _, tc := range []struct{ check, detail string }{
		{"ssh_root_login_restricted", "PermitRootLogin"},
		{"ssh_password_authentication_disabled", "PasswordAuthentication"},
		{"ssh_permit_empty_passwords_disabled", "PermitEmptyPasswords"},
	} {
		if got := sshValue(t, obs, "ssh."+tc.detail); got != "no" {
			t.Errorf("evidence ssh.%s = %q, want no (keys %v)", tc.detail, got, evidenceKeys(obs))
		}
		if res := evaluateSSH(t, obs, tc.check); res.Result != assessment.Pass {
			t.Errorf("%s = %q (%s), want pass", tc.check, res.Result, res.Detail)
		}
	}
	if got := sshCount(t, obs, "directives_set"); got != 3 {
		t.Errorf("directives_set = %d, want 3", got)
	}
	if got := sshCount(t, obs, "directives_unset"); got != int64(len(trackedSSHKeys))-3 {
		t.Errorf("directives_unset = %d, want %d", got, len(trackedSSHKeys)-3)
	}
}

// TestSSHCollectorToEvaluatorInsecureConfiguration is the H1 regression: an
// explicitly insecure global directive must reach the evaluator and FAIL. It
// previously produced UNKNOWN because the evidence was dropped in the collector.
func TestSSHCollectorToEvaluatorInsecureConfiguration(t *testing.T) {
	obs := runSSHFixture(t, "PermitRootLogin yes\nPasswordAuthentication yes\nPermitEmptyPasswords yes\n", nil, false)
	if obs.Status != assessment.Collected {
		t.Fatalf("status = %q (%s), want collected", obs.Status, obs.Error)
	}
	for _, tc := range []struct{ check, directive string }{
		{"ssh_root_login_restricted", "PermitRootLogin"},
		{"ssh_password_authentication_disabled", "PasswordAuthentication"},
		{"ssh_permit_empty_passwords_disabled", "PermitEmptyPasswords"},
	} {
		if got := sshValue(t, obs, "ssh."+tc.directive); got != "yes" {
			t.Errorf("evidence ssh.%s = %q, want yes (keys %v)", tc.directive, got, evidenceKeys(obs))
		}
		res := evaluateSSH(t, obs, tc.check)
		if res.Result != assessment.Fail {
			t.Errorf("%s with %s yes = %q (%s), want FAIL: an insecure directive whose scope is known must not soften to unknown",
				tc.check, tc.directive, res.Result, res.Detail)
		}
	}
	// The directive counts must reflect what was actually read, not a
	// stale view of the allowlist.
	if got := sshCount(t, obs, "directives_set"); got != 3 {
		t.Errorf("directives_set = %d, want 3", got)
	}
	if got := sshCount(t, obs, "directives_unset"); got != 8 {
		t.Errorf("directives_unset = %d, want 8", got)
	}
}

// TestSSHCollectorToEvaluatorWhitespace covers tab-separated and '=' separated
// directives end to end; the old single-space split read these as absent.
func TestSSHCollectorToEvaluatorWhitespace(t *testing.T) {
	obs := runSSHFixture(t, "PermitRootLogin\tno\nPasswordAuthentication=no\nPermitEmptyPasswords   yes\n", nil, false)
	for _, tc := range []struct{ check, directive, value string }{
		{"ssh_root_login_restricted", "PermitRootLogin", "no"},
		{"ssh_password_authentication_disabled", "PasswordAuthentication", "no"},
		{"ssh_permit_empty_passwords_disabled", "PermitEmptyPasswords", "yes"},
	} {
		if got := sshValue(t, obs, "ssh."+tc.directive); got != tc.value {
			t.Errorf("evidence ssh.%s = %q, want %q", tc.directive, got, tc.value)
		}
		want := assessment.Pass
		if tc.value == "yes" {
			want = assessment.Fail
		}
		if res := evaluateSSH(t, obs, tc.check); res.Result != want {
			t.Errorf("%s = %q (%s), want %q", tc.check, res.Result, res.Detail, want)
		}
	}
}

// TestSSHCollectorToEvaluatorMissingDirectives proves no default is invented:
// an absent directive is UNKNOWN, never a pass, and never a fail.
func TestSSHCollectorToEvaluatorMissingDirectives(t *testing.T) {
	obs := runSSHFixture(t, "# everything commented out\n# PermitRootLogin prohibit-password\n", nil, false)
	if obs.Status != assessment.Collected {
		t.Fatalf("status = %q, want collected", obs.Status)
	}
	for _, check := range []string{
		"ssh_root_login_restricted",
		"ssh_password_authentication_disabled",
		"ssh_permit_empty_passwords_disabled",
	} {
		res := evaluateSSH(t, obs, check)
		if res.Result != assessment.Unknown {
			t.Errorf("%s with no configured directive = %q (%s), want unknown", check, res.Result, res.Detail)
		}
	}
	if got := sshCount(t, obs, "directives_set"); got != 0 {
		t.Errorf("directives_set = %d, want 0", got)
	}
	if got := sshCount(t, obs, "directives_unset"); got != int64(len(trackedSSHKeys)) {
		t.Errorf("directives_unset = %d, want %d", got, len(trackedSSHKeys))
	}
	if got := sshValue(t, obs, "unset_directives"); !strings.Contains(got, "PermitEmptyPasswords") {
		t.Errorf("unset_directives = %q, want it to name PermitEmptyPasswords", got)
	}
}

// TestSSHCollectorToEvaluatorMatchBlockBoundary proves a Match-scoped
// directive never becomes the global verdict: a secure global directive still
// passes, and a directive that exists only inside a Match block stays UNKNOWN.
func TestSSHCollectorToEvaluatorMatchBlockBoundary(t *testing.T) {
	obs := runSSHFixture(t, strings.Join([]string{
		"PermitRootLogin no",
		"PasswordAuthentication no",
		"Match Address 10.0.0.0/8",
		"    PasswordAuthentication yes",
		"    PermitEmptyPasswords yes",
	}, "\n"), nil, false)

	if res := evaluateSSH(t, obs, "ssh_root_login_restricted"); res.Result != assessment.Pass {
		t.Errorf("global PermitRootLogin no = %q (%s), want pass", res.Result, res.Detail)
	}
	// PasswordAuthentication is set globally, so the conditional copy must not
	// replace it.
	if got := sshValue(t, obs, "ssh.PasswordAuthentication"); got != "no" {
		t.Errorf("global ssh.PasswordAuthentication = %q, want no", got)
	}
	if got := sshValue(t, obs, "ssh_conditional.PasswordAuthentication"); got != "yes" {
		t.Errorf("conditional ssh_conditional.PasswordAuthentication = %q, want yes", got)
	}
	if res := evaluateSSH(t, obs, "ssh_password_authentication_disabled"); res.Result != assessment.Pass {
		t.Errorf("PasswordAuthentication = %q (%s), want pass", res.Result, res.Detail)
	}
	// PermitEmptyPasswords exists only inside the Match block: it does not apply
	// globally, so the check must not pass and must not fail.
	if res := evaluateSSH(t, obs, "ssh_permit_empty_passwords_disabled"); res.Result != assessment.Unknown {
		t.Errorf("Match-only PermitEmptyPasswords = %q (%s), want unknown", res.Result, res.Detail)
	}
}

// TestSSHCollectorPermissionDeniedRefusal proves an unreadable configuration is
// reported as a refusal and evaluated to UNKNOWN, never to a verdict.
func TestSSHCollectorPermissionDeniedRefusal(t *testing.T) {
	obs := runSSHFixture(t, "PermitRootLogin yes\n", nil, true)
	if obs.Status != assessment.PermissionDenied {
		t.Fatalf("status = %q (%s), want permission_denied", obs.Status, obs.Error)
	}
	for _, check := range []string{
		"ssh_root_login_restricted",
		"ssh_password_authentication_disabled",
		"ssh_permit_empty_passwords_disabled",
	} {
		if res := evaluateSSH(t, obs, check); res.Result != assessment.Unknown {
			t.Errorf("%s on a refused read = %q (%s), want unknown", check, res.Result, res.Detail)
		}
	}
}

// TestSSHCollectorMissingFileAndMissingServer cover the two unsupported paths
// so a host without an ssh server is never scored.
func TestSSHCollectorMissingFileAndMissingServer(t *testing.T) {
	obs, err := collectSSHDConfig(filepath.Join(t.TempDir(), "sshd_config"), false, nil, sshFixture{files: map[string]string{}}.read)
	if err != nil {
		t.Fatal(err)
	}
	if obs[0].Status != assessment.Unsupported {
		t.Fatalf("absent ssh server status = %q, want unsupported", obs[0].Status)
	}
	if res := checkByID(linuxChecks, "ssh_permit_empty_passwords_disabled").Evaluate(obs[0]); res.Result != assessment.NotSupported {
		t.Errorf("absent ssh server = %q, want not_supported", res.Result)
	}

	missing, err := collectSSHDConfig(filepath.Join(t.TempDir(), "sshd_config"), true, nil, sshFixture{files: map[string]string{}}.read)
	if err != nil {
		t.Fatal(err)
	}
	if missing[0].Status != assessment.Unsupported {
		t.Fatalf("absent config file status = %q, want unsupported", missing[0].Status)
	}
}

// TestSSHCollectorIncludeIsResolvedInPlace proves an Include is honoured as if
// its lines appeared where the directive sits, so precedence stays truthful.
func TestSSHCollectorIncludeIsResolvedInPlace(t *testing.T) {
	main := strings.Join([]string{
		"Include sshd_config.d/*.conf",
		"PermitRootLogin yes",
	}, "\n")
	// The included fragment sets PermitRootLogin first, so sshd's first-match-wins
	// makes it the effective value; a parser that ignored Include would report
	// the main file's "yes" as the whole story.
	obs := runSSHFixture(t, main, map[string]string{
		"sshd_config.d/10-hardening.conf": "PermitRootLogin no\nPermitEmptyPasswords no\n",
	}, false)
	if obs.Status != assessment.Collected {
		t.Fatalf("status = %q (%s), want collected", obs.Status, obs.Error)
	}
	if got := sshValue(t, obs, "ssh.PermitRootLogin"); got != "no" {
		t.Errorf("effective PermitRootLogin = %q, want no from the included fragment", got)
	}
	if got := sshValue(t, obs, "ssh.PermitEmptyPasswords"); got != "no" {
		t.Errorf("effective PermitEmptyPasswords = %q, want no from the included fragment", got)
	}
	if files := sshValue(t, obs, "include_files"); !strings.Contains(files, "10-hardening.conf") {
		t.Errorf("include_files = %q, want it to name the loaded fragment", files)
	}
	if res := evaluateSSH(t, obs, "ssh_root_login_restricted"); res.Result != assessment.Pass {
		t.Errorf("root login = %q (%s), want pass from the include", res.Result, res.Detail)
	}
}

// TestSSHCollectorUnresolvableIncludeRefusesToGuess is the honesty rule for
// configuration precedence: when an Include cannot be resolved the effective
// configuration is unknown, so every SSH check reports UNKNOWN instead of
// asserting what the visible file alone appears to say.
func TestSSHCollectorUnresolvableIncludeRefusesToGuess(t *testing.T) {
	obs := runSSHFixture(t, "Include /etc/ssh/absent.conf\nPermitRootLogin yes\n", nil, false)
	if obs.Status != assessment.Incomplete {
		t.Fatalf("status = %q (%s), want incomplete", obs.Status, obs.Error)
	}
	for _, check := range []string{
		"ssh_root_login_restricted",
		"ssh_password_authentication_disabled",
		"ssh_permit_empty_passwords_disabled",
	} {
		if res := evaluateSSH(t, obs, check); res.Result != assessment.Unknown {
			t.Errorf("%s with an unresolvable Include = %q, want unknown", check, res.Result)
		}
	}
}

// TestSSHCollectorIncludeOutsideRootIsRefused keeps the containment guarantee:
// a configuration must not be able to point an unprivileged collector at an
// arbitrary path.
func TestSSHCollectorIncludeOutsideRootIsRefused(t *testing.T) {
	obs := runSSHFixture(t, "Include ../../passwd\nPermitRootLogin yes\n", nil, false)
	if obs.Status != assessment.Incomplete {
		t.Fatalf("status = %q (%s), want incomplete", obs.Status, obs.Error)
	}
	if !strings.Contains(obs.Error, "outside") {
		t.Errorf("error = %q, want it to explain the containment refusal", obs.Error)
	}
}

// TestSSHCollectorIncludeCycleIsBounded proves a self-referential Include ends
// as an inconclusive reading rather than unbounded recursion.
func TestSSHCollectorIncludeCycleIsBounded(t *testing.T) {
	obs := runSSHFixture(t, "Include sshd_config\n", map[string]string{
		"sshd_config": "Include sshd_config\n",
	}, false)
	if obs.Status != assessment.Incomplete {
		t.Fatalf("status = %q (%s), want incomplete", obs.Status, obs.Error)
	}
}

// TestSSHCollectorEvidenceIsBounded guards the documented per-record cap.
func TestSSHCollectorEvidenceIsBounded(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("AllowUsers user")
		b.WriteString(strings.Repeat("x", 60))
		b.WriteString("\n")
	}
	obs := runSSHFixture(t, b.String(), nil, false)
	if len(obs.Values) > assessment.MaxEvidenceValues {
		t.Fatalf("evidence values = %d, want <= %d", len(obs.Values), assessment.MaxEvidenceValues)
	}
	for _, v := range obs.Values {
		if len(v.Text) > assessment.MaxTextValue {
			t.Fatalf("value %q exceeds the text bound: %d > %d", v.Key, len(v.Text), assessment.MaxTextValue)
		}
	}
}

// TestSSHCollectorNeverReturnsVerdictsReinforced keeps the collector/verifier
// separation observable: evidence values are plain facts with no result field.
func TestSSHCollectorNeverReturnsVerdictsReinforced(t *testing.T) {
	obs := runSSHFixture(t, "PermitRootLogin yes\n", nil, false)
	for _, v := range obs.Values {
		if strings.Contains(strings.ToLower(v.Key), "pass") || strings.Contains(strings.ToLower(v.Key), "fail") {
			t.Errorf("collector emitted a verdict-shaped evidence key: %q", v.Key)
		}
	}
}
