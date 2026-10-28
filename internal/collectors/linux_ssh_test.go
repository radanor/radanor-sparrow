//go:build linux

package collectors

import (
	"reflect"
	"strings"
	"testing"
)

// TestParseSSHDConfigFirstMatchWins pins sshd's documented semantics: the first
// occurrence of a keyword wins and later duplicates are ignored. A parser that
// took the last value would report the opposite of what sshd actually does.
//
// It also pins the key contract: both returned maps are keyed by the canonical
// spelling from trackedSSHKeys. The CE-5 review found the parser lowercasing
// keys while the collector and the evaluators looked them up in tracked
// casing, which silently discarded every directive read. Indexing with the
// tracked spelling is the assertion that guards that defect.
func TestParseSSHDConfigFirstMatchWins(t *testing.T) {
	text := strings.Join([]string{
		"# comment line",
		"PermitRootLogin prohibit-password",
		"PasswordAuthentication no",
		"",
		"# a later duplicate that sshd ignores",
		"PermitRootLogin yes",
		"PasswordAuthentication yes",
	}, "\n")
	effective, conditional, unset := parseSSHDConfig(text, trackedSSHKeys)

	if got := effective["PermitRootLogin"]; got != "prohibit-password" {
		t.Errorf("PermitRootLogin = %q; want first value %q", got, "prohibit-password")
	}
	if got := effective["PasswordAuthentication"]; got != "no" {
		t.Errorf("PasswordAuthentication = %q; want first value %q", got, "no")
	}
	if len(conditional) != 0 {
		t.Errorf("conditional = %v; want empty for a Match-free file", conditional)
	}
	// PermitEmptyPasswords was never set, so it must appear among the unset keys.
	found := false
	for _, k := range unset {
		if k == "PermitEmptyPasswords" {
			found = true
		}
	}
	if !found {
		t.Errorf("unset = %v; want it to include PermitEmptyPasswords", unset)
	}
	// Set and unset are counted over the same allowlist, so they must add up.
	if len(effective)+len(unset) != len(trackedSSHKeys) {
		t.Errorf("directives_set %d + directives_unset %d != %d tracked directives",
			len(effective), len(unset), len(trackedSSHKeys))
	}
}

// TestParseSSHDConfigMapKeysMatchTrackedSpelling is the direct regression test
// for the review's H1: every key the parser returns must be indexable with the
// exact spelling the collector emits and the evaluator reads.
func TestParseSSHDConfigMapKeysMatchTrackedSpelling(t *testing.T) {
	text := "PermitRootLogin yes\nPasswordAuthentication yes\nPermitEmptyPasswords yes\n"
	effective, _, _ := parseSSHDConfig(text, trackedSSHKeys)
	for _, key := range []string{"PermitRootLogin", "PasswordAuthentication", "PermitEmptyPasswords"} {
		if _, ok := effective[key]; !ok {
			t.Errorf("effective[%q] missing; keys were %v", key, mapKeys(effective))
		}
	}
}

func mapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestParseSSHDConfigWhitespaceSeparators covers sshd's argument separators:
// spaces, tabs, and the optional single '=' form. A parser that split only on
// a single space silently dropped tab-indented configuration, which is common
// in generated sshd_config fragments.
func TestParseSSHDConfigWhitespaceSeparators(t *testing.T) {
	text := strings.Join([]string{
		"PermitRootLogin\tno",
		"PasswordAuthentication\tyes",
		"PermitEmptyPasswords=no",
		"MaxAuthTries 3",
	}, "\n")
	effective, _, unset := parseSSHDConfig(text, trackedSSHKeys)
	cases := map[string]string{
		"PermitRootLogin":        "no",
		"PasswordAuthentication": "yes",
		"PermitEmptyPasswords":   "no",
		"MaxAuthTries":           "3",
	}
	for key, want := range cases {
		if got := effective[key]; got != want {
			t.Errorf("%s = %q, want %q (keys %v)", key, got, want, mapKeys(effective))
		}
	}
	if len(unset) != len(trackedSSHKeys)-len(cases) {
		t.Errorf("unset = %v, want %d entries", unset, len(trackedSSHKeys)-len(cases))
	}
}

// TestParseSSHDConfigMatchBlocksAreNotGlobal guards against promoting a
// conditional directive to a global one. A directive inside a Match block
// applies only to matched connections, so reporting it as the effective global
// setting would overstate the host's posture.
func TestParseSSHDConfigMatchBlocksAreNotGlobal(t *testing.T) {
	text := strings.Join([]string{
		"PasswordAuthentication no",
		"Match Address 10.0.0.0/8",
		"    PasswordAuthentication yes",
		"    PermitRootLogin yes",
	}, "\n")
	effective, conditional, _ := parseSSHDConfig(text, trackedSSHKeys)

	if got := effective["PasswordAuthentication"]; got != "no" {
		t.Errorf("global PasswordAuthentication = %q; must stay %q (Match block must not override)", got, "no")
	}
	if got := conditional["PasswordAuthentication"]; got != "yes" {
		t.Errorf("conditional PasswordAuthentication = %q; want %q", got, "yes")
	}
	if _, leaked := effective["PermitRootLogin"]; leaked {
		t.Error("PermitRootLogin set only inside a Match block leaked into the global map")
	}
	if got := conditional["PermitRootLogin"]; got != "yes" {
		t.Errorf("conditional PermitRootLogin = %q; want %q", got, "yes")
	}
}

// TestParseSSHDConfigMatchKeywordIsCaseInsensitive checks that a lowercased
// Match keyword still opens a conditional scope; otherwise the block's
// directives would be silently promoted to global.
func TestParseSSHDConfigMatchKeywordIsCaseInsensitive(t *testing.T) {
	text := "PasswordAuthentication no\nMATCH User root\n\tPasswordAuthentication yes\n"
	effective, conditional, _ := parseSSHDConfig(text, trackedSSHKeys)
	if got := effective["PasswordAuthentication"]; got != "no" {
		t.Errorf("global PasswordAuthentication = %q, want no", got)
	}
	if got := conditional["PasswordAuthentication"]; got != "yes" {
		t.Errorf("conditional PasswordAuthentication = %q, want yes", got)
	}
}

// TestParseSSHDConfigTrackedValuesPreserved confirms a value with internal
// spacing survives trimming intact.
func TestParseSSHDConfigTrackedValuesPreserved(t *testing.T) {
	text := "AllowGroups sudo ssh-users\nAllowUsers alice bob\n"
	effective, _, _ := parseSSHDConfig(text, trackedSSHKeys)
	if !reflect.DeepEqual(effective["AllowGroups"], "sudo ssh-users") {
		t.Errorf("AllowGroups = %q; want %q", effective["AllowGroups"], "sudo ssh-users")
	}
	if !reflect.DeepEqual(effective["AllowUsers"], "alice bob") {
		t.Errorf("AllowUsers = %q; want %q", effective["AllowUsers"], "alice bob")
	}
}

// TestParseSSHDConfigKeepsUntrackedKeywordsOut ensures only the reviewed
// allowlist reaches evidence, while untracked keywords never masquerade as
// tracked ones.
func TestParseSSHDConfigKeepsUntrackedKeywordsOut(t *testing.T) {
	text := "Port 2222\nListenAddress 0.0.0.0\nPermitRootLogin no\n"
	effective, conditional, unset := parseSSHDConfig(text, trackedSSHKeys)
	if len(effective) != 1 {
		t.Errorf("effective = %v, want only PermitRootLogin", mapKeys(effective))
	}
	if len(conditional) != 0 {
		t.Errorf("conditional = %v, want empty", mapKeys(conditional))
	}
	if len(unset) != len(trackedSSHKeys)-1 {
		t.Errorf("unset = %v, want %d entries", unset, len(trackedSSHKeys)-1)
	}
}
