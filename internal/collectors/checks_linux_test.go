//go:build linux

package collectors

import (
	"strings"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
)

func obsWith(values ...assessment.Value) assessment.Observation {
	return assessment.Observation{
		ID: "x", Collector: "x", Platform: "ubuntu", CollectedAt: time.Now(),
		Status: assessment.Collected, Source: "test", Values: values,
	}
}

func checkByID(checks []assessment.CheckDefinition, id string) assessment.CheckDefinition {
	for _, c := range checks {
		if c.ID == id {
			return c
		}
	}
	panic("check not found: " + id)
}

var linuxChecks = PlatformChecks()

func TestFirewallCheckNeverPassesOnUnknownTools(t *testing.T) {
	check := checkByID(linuxChecks, "firewall_enabled")
	cases := []struct {
		name string
		obs  assessment.Observation
		want assessment.Result
	}{
		{"one active tool", obsWith(valueInt("tools_active", 1), valueInt("tools_inactive", 0), valueStr("active_tools", "ufw")), assessment.Pass},
		{"all tools inactive", obsWith(valueInt("tools_active", 0), valueInt("tools_inactive", 2)), assessment.Fail},
		{"no tool readable", obsWith(valueInt("tools_active", 0), valueInt("tools_inactive", 0)), assessment.Unknown},
		{"counts absent", obsWith(), assessment.Unknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := check.Evaluate(tc.obs); got.Result != tc.want {
				t.Fatalf("result = %q (%s), want %q", got.Result, got.Detail, tc.want)
			}
		})
	}
}

// TestDiskEncryptionNeverClaimsDisabledWithoutEvidence is root-scoped: the
// check is decided by the system volume, so an unrelated encrypted volume can
// neither pass it nor a determined-plain root escape it.
func TestDiskEncryptionNeverClaimsDisabledWithoutEvidence(t *testing.T) {
	check := checkByID(linuxChecks, "disk_encryption_present")
	// The root volume could not be traced: UNKNOWN, never FAIL.
	unknown := check.Evaluate(obsWith(valueStr("root_state", "undetermined"), valueInt("undetermined_volumes", 2)))
	if unknown.Result != assessment.Unknown {
		t.Fatalf("undetermined root = %q, want unknown", unknown.Result)
	}
	// A determined plain system root is a genuine failure.
	fail := check.Evaluate(obsWith(valueStr("root_state", "plain"), valueStr("root_device", "sda2")))
	if fail.Result != assessment.Fail {
		t.Fatalf("plain root = %q, want fail", fail.Result)
	}
	// An encrypted system root passes.
	pass := check.Evaluate(obsWith(valueStr("root_state", "encrypted"), valueStr("root_device", "ubuntu--vg-root")))
	if pass.Result != assessment.Pass {
		t.Fatalf("encrypted root = %q, want pass", pass.Result)
	}
	// No root volume at all is UNKNOWN.
	if got := check.Evaluate(obsWith(valueStr("root_state", "not_mounted"))); got.Result != assessment.Unknown {
		t.Fatalf("no root volume = %q, want unknown", got.Result)
	}
	// A count of encrypted containers must not decide the verdict by itself: an
	// encrypted data volume alongside a plain system root is still a failure.
	mixed := check.Evaluate(obsWith(valueStr("root_state", "plain"), valueInt("encrypted_containers", 1), valueStr("encrypted_devices", "sdb1")))
	if mixed.Result != assessment.Fail {
		t.Fatalf("plain root with an encrypted data volume = %q, want fail", mixed.Result)
	}
}

// TestDiskEncryptionCheckIgnoresUnrelatedEvidence pins the review's M4 finding
// directly: an encrypted container somewhere in the layout is not evidence
// about the system volume.
func TestDiskEncryptionCheckIgnoresUnrelatedEvidence(t *testing.T) {
	check := checkByID(linuxChecks, "disk_encryption_present")
	got := check.Evaluate(obsWith(valueInt("encrypted_containers", 3), valueStr("encrypted_devices", "sdb1,sdc1,sdd1")))
	if got.Result != assessment.Unknown {
		t.Fatalf("result = %q (%s), want unknown when no root fact was recorded", got.Result, got.Detail)
	}
}

func TestSSHChecksRespectUnsetAndConditionalDirectives(t *testing.T) {
	root := checkByID(linuxChecks, "ssh_root_login_restricted")
	pass := root.Evaluate(obsWith(valueStr("ssh.PermitRootLogin", "prohibit-password")))
	if pass.Result != assessment.Pass {
		t.Fatalf("prohibit-password = %q, want pass", pass.Result)
	}
	fail := root.Evaluate(obsWith(valueStr("ssh.PermitRootLogin", "yes")))
	if fail.Result != assessment.Fail {
		t.Fatalf("yes = %q, want fail", fail.Result)
	}
	// Unset globally, with a Match-block setting present: UNKNOWN, not a pass.
	unset := root.Evaluate(obsWith(valueStr("ssh_conditional.PermitRootLogin", "yes")))
	if unset.Result != assessment.Unknown {
		t.Fatalf("unset global directive = %q, want unknown", unset.Result)
	}

	passwords := checkByID(linuxChecks, "ssh_password_authentication_disabled")
	if got := passwords.Evaluate(obsWith(valueStr("ssh.PasswordAuthentication", "no"))); got.Result != assessment.Pass {
		t.Fatalf("PasswordAuthentication no = %q, want pass", got.Result)
	}
	if got := passwords.Evaluate(obsWith(valueStr("ssh.PasswordAuthentication", "yes"))); got.Result != assessment.Fail {
		t.Fatalf("PasswordAuthentication yes = %q, want fail", got.Result)
	}
	if got := passwords.Evaluate(obsWith()); got.Result != assessment.Unknown {
		t.Fatalf("unset PasswordAuthentication = %q, want unknown", got.Result)
	}
}

// TestSSHChecksNeverInventAVerdictForUnrecognisedValues pins the honesty rule
// for values the rule does not understand: sshd accepts only yes/no for these
// directives, so a value outside that set supports neither a pass nor a
// failure. The PasswordAuthentication and PermitEmptyPasswords checks used to
// fail anything that was not literally "no".
func TestSSHChecksNeverInventAVerdictForUnrecognisedValues(t *testing.T) {
	cases := []struct {
		check     string
		directive string
	}{
		{"ssh_root_login_restricted", "PermitRootLogin"},
		{"ssh_password_authentication_disabled", "PasswordAuthentication"},
		{"ssh_permit_empty_passwords_disabled", "PermitEmptyPasswords"},
	}
	for _, tc := range cases {
		t.Run(tc.check, func(t *testing.T) {
			check := checkByID(linuxChecks, tc.check)
			unrecognised := check.Evaluate(obsWith(valueStr("ssh."+tc.directive, "sometimes")))
			if unrecognised.Result != assessment.Unknown {
				t.Fatalf("unrecognised value = %q (%s), want unknown", unrecognised.Result, unrecognised.Detail)
			}
			// The value itself must be reported so the reader can see what was
			// found rather than a generic refusal.
			if !strings.Contains(unrecognised.Detail, "sometimes") {
				t.Errorf("detail = %q, want it to quote the offending value", unrecognised.Detail)
			}
			no := check.Evaluate(obsWith(valueStr("ssh."+tc.directive, "no")))
			if no.Result != assessment.Pass {
				t.Errorf("no = %q, want pass", no.Result)
			}
			yes := check.Evaluate(obsWith(valueStr("ssh."+tc.directive, "yes")))
			if yes.Result != assessment.Fail {
				t.Errorf("yes = %q, want fail", yes.Result)
			}
		})
	}
}

func TestPendingSecurityUpdatesCheck(t *testing.T) {
	check := checkByID(linuxChecks, "pending_security_updates")
	if got := check.Evaluate(obsWith(valueInt("pending_security_updates", 0))); got.Result != assessment.Pass {
		t.Fatalf("zero pending = %q, want pass", got.Result)
	}
	if got := check.Evaluate(obsWith(valueInt("pending_security_updates", 3), valueStr("pending_security_packages", "a,b,c"))); got.Result != assessment.Fail {
		t.Fatalf("three pending = %q, want fail", got.Result)
	}
	if got := check.Evaluate(obsWith()); got.Result != assessment.Unknown {
		t.Fatalf("missing count = %q, want unknown", got.Result)
	}
}

func TestAutoSecurityUpdatesCheck(t *testing.T) {
	check := checkByID(linuxChecks, "automatic_security_updates_enabled")
	pass := check.Evaluate(obsWith(valueBoolV("unattended_upgrade_enabled", true), valueInt("security_origins", 1)))
	if pass.Result != assessment.Pass {
		t.Fatalf("enabled with a security origin = %q, want pass", pass.Result)
	}
	noOrigins := check.Evaluate(obsWith(valueBoolV("unattended_upgrade_enabled", true), valueInt("security_origins", 0)))
	if noOrigins.Result != assessment.Fail {
		t.Fatalf("enabled without a security origin = %q, want fail", noOrigins.Result)
	}
	disabled := check.Evaluate(obsWith(valueBoolV("unattended_upgrade_enabled", false), valueInt("security_origins", 3)))
	if disabled.Result != assessment.Fail {
		t.Fatalf("disabled = %q, want fail", disabled.Result)
	}
}

func TestSecurityUnitsCheck(t *testing.T) {
	check := checkByID(linuxChecks, "security_units_active")
	pass := check.Evaluate(obsWith(valueInt("units_present", 3), valueStr("unit.ssh.service.active", "active"), valueStr("unit.apparmor.service.active", "active")))
	if pass.Result != assessment.Pass {
		t.Fatalf("no failed units = %q, want pass", pass.Result)
	}
	fail := check.Evaluate(obsWith(valueInt("units_present", 3), valueStr("unit.apparmor.service.active", "failed")))
	if fail.Result != assessment.Fail {
		t.Fatalf("a failed unit = %q, want fail", fail.Result)
	}
	none := check.Evaluate(obsWith(valueInt("units_present", 0)))
	if none.Result != assessment.Unknown {
		t.Fatalf("no units present = %q, want unknown", none.Result)
	}
}

func TestOSReleaseSupportCheck(t *testing.T) {
	check := checkByID(linuxChecks, "os_release_supported")
	supported := check.Evaluate(obsWith(valueStr("version_id", "22.04"), valueStr("support_ends", "2027-04-21")))
	if supported.Result != assessment.Pass {
		t.Fatalf("22.04 = %q, want pass", supported.Result)
	}
	// The date is in the past, so the fix is a removal of the window rather than
	// a change to it: use a release whose window has already closed.
	expired := check.Evaluate(obsWith(valueStr("version_id", "20.04"), valueStr("support_ends", "2020-01-01")))
	if expired.Result != assessment.Fail {
		t.Fatalf("expired release = %q, want fail", expired.Result)
	}
	unknown := check.Evaluate(obsWith(valueStr("version_id", "26.04")))
	if unknown.Result != assessment.Unknown {
		t.Fatalf("unknown window = %q, want unknown", unknown.Result)
	}
}

func TestPlatformCollectorsCoverEveryCheckBinding(t *testing.T) {
	collectorIDs := map[string]bool{}
	for _, c := range PlatformCollectors() {
		collectorIDs[c.ID()] = true
	}
	for _, check := range PlatformChecks() {
		if !collectorIDs[check.CollectorID] {
			t.Errorf("check %s binds collector %q, which is not in the platform collector set", check.ID, check.CollectorID)
		}
		if check.EvaluateFn == nil {
			t.Errorf("check %s has no evaluator", check.ID)
		}
		if check.Version == "" || check.Action == "" || check.VerifyMethod == "" {
			t.Errorf("check %s is missing life-cycle metadata", check.ID)
		}
		// Every defined check must carry an explicit freshness policy, so an
		// omitted budget cannot silently reintroduce "forever fresh".
		if check.MaxAge <= 0 {
			t.Errorf("check %s has no freshness budget (MaxAge)", check.ID)
		}
	}
}

// valueStr, valueInt and valueBoolV build evidence values in the collector's
// own shape for synthetic evaluation.
func valueStr(key, text string) assessment.Value {
	return assessment.Value{Key: key, Kind: assessment.ValueString, Text: text}
}
func valueInt(key string, v int64) assessment.Value {
	i := v
	return assessment.Value{Key: key, Kind: assessment.ValueInt, Int: &i}
}
func valueBoolV(key string, v bool) assessment.Value {
	b := v
	return assessment.Value{Key: key, Kind: assessment.ValueBool, Bool: &b}
}
