//go:build linux

package collectors

import (
	"strings"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// PlatformCollectors returns the native Ubuntu collector set used by a scan.
// Each collector is unprivileged, bounded, and returns facts only. The set is
// deliberately small and each member either grounds one or more checks or
// supplies context the dashboard shows without a verdict.
func PlatformCollectors() []assessment.Collector {
	return []assessment.Collector{
		PlatformCollector{},
		SupportCollector{},
		FirewallPostureCollector{},
		AppArmorCollector{},
		AutomaticUpdatesCollector{},
		PendingSecurityUpdatesCollector{},
		DiskEncryptionCollector{},
		SSHServerCollector{},
		ServicesCollector{},
		// Context collectors: they record genuine facts (software inventory and
		// listening endpoints) but produce no PASS/FAIL verdict, so they cannot
		// be mistaken for a security conclusion.
		PackageInventoryCollector{},
		NetworkCollector{},
	}
}

var ubuntuPlatforms = []string{"ubuntu"}

// PlatformChecks returns the versioned Ubuntu check definitions.
func PlatformChecks() []assessment.CheckDefinition {
	return []assessment.CheckDefinition{
		osReleaseSupported(),
		firewallEnabled(),
		apparmorEnabled(),
		autoSecurityUpdates(),
		pendingSecurityUpdates(),
		diskEncryption(),
		sshRootLogin(),
		sshPasswordAuthentication(),
		sshEmptyPasswords(),
		securityUnitsHealthy(),
	}
}

func osReleaseSupported() assessment.CheckDefinition {
	return mkCheck("os_release_supported", "release_support", "platform", "Operating system release is supported",
		assessment.SeverityHigh, ubuntuPlatforms,
		"An unsupported operating system no longer receives security fixes, so every other control rests on a foundation that is no longer maintained.",
		"Upgrade to a supported Ubuntu LTS release (22.04 or 24.04).",
		"Re-run the assessment; this check passes when the release is within its published support window.",
		func(obs assessment.Observation) assessment.Eval {
			version, ok := stringValue(obs, "version_id")
			if !ok {
				return unknownEval("the release version could not be read")
			}
			ends, ok := stringValue(obs, "support_ends")
			if !ok {
				return unknownEval("no support window is recorded for release " + version)
			}
			end, err := time.Parse("2006-01-02", ends)
			if err != nil {
				return unknownEval("the recorded support window could not be interpreted")
			}
			if time.Now().After(end) {
				return assessment.Eval{Result: assessment.Fail, Detail: "Ubuntu " + version + " left mainstream support on " + ends}
			}
			return assessment.Eval{Result: assessment.Pass, Detail: "Ubuntu " + version + " is supported until " + ends}
		},
		"The support table is a reviewed, offline copy of Canonical's published release lifecycle; it covers the LTS releases this product targets.",
		"Extended Security Maintenance coverage is not evaluated, so a release past mainstream support may still receive fixes under an ESM subscription.")
}

func firewallEnabled() assessment.CheckDefinition {
	return mkCheck("firewall_enabled", "firewall_posture", "network", "A host firewall is active",
		assessment.SeverityHigh, ubuntuPlatforms,
		"An active host firewall is a primary control limiting which services other machines on the network can reach. The verdict is grounded in every readable firewall tool, not a single flag.",
		"Enable one host firewall (for example `sudo ufw enable` on Ubuntu) and confirm its ruleset matches the services you intend to expose.",
		"Re-run the assessment; this check passes when at least one firewall tool reports an active ruleset.",
		func(obs assessment.Observation) assessment.Eval {
			active, _ := intValue(obs, "tools_active")
			inactive, _ := intValue(obs, "tools_inactive")
			activeNames, _ := stringValue(obs, "active_tools")
			switch {
			case active > 0:
				return assessment.Eval{Result: assessment.Pass, Detail: "active firewall tooling: " + activeNames}
			case inactive > 0:
				return assessment.Eval{Result: assessment.Fail, Detail: "no firewall tool reported an active ruleset"}
			default:
				return unknownEval("no firewall tool could be read, so the firewall state is not known")
			}
		},
		"Only three native tools are examined (ufw, firewalld, nftables); another firewall implementation would not be observed.",
		"A non-empty nftables ruleset is treated as active even if it contains only non-filtering tables.")
}

func apparmorEnabled() assessment.CheckDefinition {
	return mkCheck("apparmor_enabled", "apparmor", "platform", "AppArmor confinement is enabled",
		assessment.SeverityMedium, ubuntuPlatforms,
		"AppArmor confines vulnerable programs to a limited set of resources even after they are compromised, which requires the kernel module to be enabled.",
		"Boot with AppArmor enabled and confirm `apparmor.service` is active (Ubuntu enables it by default).",
		"Re-run the assessment; this check passes when the kernel reports AppArmor enabled.",
		func(obs assessment.Observation) assessment.Eval {
			enabled, ok := boolValue(obs, "enabled")
			if !ok {
				return unknownEval("the AppArmor kernel flag could not be read")
			}
			if enabled {
				return assessment.Eval{Result: assessment.Pass, Detail: "the kernel reports AppArmor enabled"}
			}
			return assessment.Eval{Result: assessment.Fail, Detail: "the kernel reports AppArmor disabled"}
		},
		"Only the kernel's enabled flag is evaluated; individual profile enforcement modes are reported as evidence but not scored.")
}

func autoSecurityUpdates() assessment.CheckDefinition {
	return mkCheck("automatic_security_updates_enabled", "automatic_security_updates", "updates", "Automatic security updates are configured",
		assessment.SeverityMedium, ubuntuPlatforms,
		"Unattended security updates reduce the window in which a known, fixed vulnerability remains exploitable on the device.",
		"Enable unattended upgrades and ensure the Ubuntu security pocket is among the allowed origins (`/etc/apt/apt.conf.d/50unattended-upgrades`).",
		"Re-run the assessment; this check passes when unattended upgrades are enabled and at least one security origin is allowed.",
		func(obs assessment.Observation) assessment.Eval {
			enabled, ok := boolValue(obs, "unattended_upgrade_enabled")
			if !ok {
				return unknownEval("the unattended-upgrade setting could not be read")
			}
			if !enabled {
				return assessment.Eval{Result: assessment.Fail, Detail: "unattended upgrades are not enabled"}
			}
			origins, ok := intValue(obs, "security_origins")
			if !ok {
				return unknownEval("the allowed origins could not be read")
			}
			if origins == 0 {
				return assessment.Eval{Result: assessment.Fail, Detail: "unattended upgrades are enabled but no security origin is allowed"}
			}
			return assessment.Eval{Result: assessment.Pass, Detail: "unattended upgrades are enabled with at least one security origin"}
		},
		"The check verifies configuration only; it cannot confirm the timer actually ran or that updates were applied successfully offline.")
}

func pendingSecurityUpdates() assessment.CheckDefinition {
	return mkCheck("pending_security_updates", "pending_security_updates", "updates", "No pending security updates",
		assessment.SeverityMedium, ubuntuPlatforms,
		"Packages with an available upgrade from the distribution security pocket contain fixes for known vulnerabilities that are not yet applied to this device.",
		"Apply the pending security updates (`sudo apt-get upgrade`, rebooting if a kernel update is included) and re-assess.",
		"Re-run the assessment; this check passes when no candidate version originates from the security pocket.",
		func(obs assessment.Observation) assessment.Eval {
			count, ok := intValue(obs, "pending_security_updates")
			if !ok {
				return unknownEval("the pending security update count could not be read")
			}
			if count == 0 {
				return assessment.Eval{Result: assessment.Pass, Detail: "no candidate version originates from the security pocket"}
			}
			names, _ := stringValue(obs, "pending_security_packages")
			return assessment.Eval{Result: assessment.Fail, Detail: "pending security updates: " + names}
		},
		"Only the security pocket is counted; updates that fix vulnerabilities but are delivered through other pockets are not included.",
		"A reading derived from old package lists is qualified in the evidence, but the freshness of the lists themselves is not scored.")
}

func diskEncryption() assessment.CheckDefinition {
	return mkCheck("disk_encryption_present", "disk_encryption", "storage", "The system volume uses managed encryption",
		assessment.SeverityMedium, ubuntuPlatforms,
		"Full-disk encryption protects data at rest when the device is lost or stolen, and is a prerequisite for several compliance regimes. What matters is the volume that holds the operating system: an unrelated encrypted data volume does not protect the system partition.",
		"Rebuild the system volume on an encrypted LUKS container, or enable the platform's managed full-disk encryption option.",
		"Re-run the assessment; this check passes when the filesystem mounted at the system root sits on, or is, an encrypted container.",
		func(obs assessment.Observation) assessment.Eval {
			state, ok := stringValue(obs, "root_state")
			if !ok {
				return unknownEval("the encryption state of the system root was not recorded, so no verdict is asserted")
			}
			device, _ := stringValue(obs, "root_device")
			reason, _ := stringValue(obs, "root_state_reason")
			if reason != "" {
				reason = " (" + reason + ")"
			}
			switch rootState(state) {
			case rootEncrypted:
				if device == "" {
					return assessment.Eval{Result: assessment.Pass, Detail: "the system volume sits on an encrypted container"}
				}
				return assessment.Eval{Result: assessment.Pass, Detail: "the system volume " + device + " sits on an encrypted container"}
			case rootPlain:
				if device == "" {
					return assessment.Eval{Result: assessment.Fail, Detail: "the system volume was observed mounted without encryption"}
				}
				return assessment.Eval{Result: assessment.Fail, Detail: "the system volume " + device + " is mounted without encryption"}
			default:
				// An undetermined root is never an unencrypted root, and an
				// unrelated encrypted volume is never offered as its evidence.
				return unknownEval("the encryption state of the system volume could not be determined" + reason)
			}
		},
		"Only the filesystem mounted at the system root is scored; data, swap and removable volumes are reported as evidence but never decide the verdict.",
		"A volume whose ancestry cannot be traced back to a physical disk is reported as UNKNOWN, never as unencrypted.")
}

// sshDirective reads the effective global value of a tracked directive and
// reports whether it was set at all. Conditional (Match-block) settings are
// ignored here and only reported as evidence.
func sshDirective(obs assessment.Observation, name string) (string, bool) {
	if value, ok := stringValue(obs, "ssh."+name); ok {
		return strings.ToLower(value), true
	}
	return "", false
}

// sshConditionalNotes records a Match-block setting so a check never reports an
// unset global directive as if no configuration existed at all.
func sshConditionalNotes(obs assessment.Observation, name string) string {
	if value, ok := stringValue(obs, "ssh_conditional."+name); ok {
		return "; a conditional (Match block) setting of " + value + " exists but does not apply globally"
	}
	return ""
}

func sshRootLogin() assessment.CheckDefinition {
	return mkCheck("ssh_root_login_restricted", "ssh_server_config", "remote-access", "SSH root login is restricted",
		assessment.SeverityHigh, ubuntuPlatforms,
		"Allowing direct root login over SSH removes accountability and exposes the highest-privilege account to password guessing.",
		"Set `PermitRootLogin no` (or `prohibit-password`) in /etc/ssh/sshd_config and reload the SSH server.",
		"Re-run the assessment; this check passes when the effective global setting forbids password-based root login.",
		func(obs assessment.Observation) assessment.Eval {
			value, set := sshDirective(obs, "PermitRootLogin")
			if !set {
				return unknownEval("PermitRootLogin is not set globally; the compiled-in default applies and is not asserted" + sshConditionalNotes(obs, "PermitRootLogin"))
			}
			switch value {
			case "no", "prohibit-password", "without-password", "forced-commands-only":
				return assessment.Eval{Result: assessment.Pass, Detail: "PermitRootLogin " + value}
			case "yes":
				return assessment.Eval{Result: assessment.Fail, Detail: "PermitRootLogin yes allows direct root login"}
			default:
				return unknownEval("PermitRootLogin has an unrecognised value (" + value + "); it supports neither a pass nor a failure")
			}
		},
		"Directives inside Match blocks are reported separately and never treated as the global setting.",
		"When the directive is unset, the compiled-in default applies and the result is UNKNOWN rather than assumed.")
}

func sshPasswordAuthentication() assessment.CheckDefinition {
	return mkCheck("ssh_password_authentication_disabled", "ssh_server_config", "remote-access", "SSH password authentication is disabled",
		assessment.SeverityHigh, ubuntuPlatforms,
		"Password authentication over SSH is vulnerable to credential guessing; key-based authentication is materially stronger.",
		"Set `PasswordAuthentication no` in /etc/ssh/sshd_config and ensure authorised keys are installed before reloading.",
		"Re-run the assessment; this check passes when the effective global setting disables password authentication.",
		func(obs assessment.Observation) assessment.Eval {
			value, set := sshDirective(obs, "PasswordAuthentication")
			if !set {
				return unknownEval("PasswordAuthentication is not set globally; the compiled-in default applies and is not asserted" + sshConditionalNotes(obs, "PasswordAuthentication"))
			}
			switch value {
			case "no":
				return assessment.Eval{Result: assessment.Pass, Detail: "PasswordAuthentication no"}
			case "yes":
				return assessment.Eval{Result: assessment.Fail, Detail: "PasswordAuthentication yes permits password logins"}
			default:
				// An unrecognised value is not evidence that passwords are
				// permitted, and not evidence that they are refused: sshd only
				// accepts yes/no here, so such a configuration does not describe
				// a verified posture. Asserting either verdict would invent one.
				return unknownEval("PasswordAuthentication has an unrecognised value (" + value + "); sshd accepts only yes or no")
			}
		},
		"Only the global setting is scored; conditional directives and key-based access are not evaluated.",
		"An unrecognised value is reported as UNKNOWN rather than as a pass or a failure.")
}

func sshEmptyPasswords() assessment.CheckDefinition {
	return mkCheck("ssh_permit_empty_passwords_disabled", "ssh_server_config", "remote-access", "SSH empty passwords are refused",
		assessment.SeverityCritical, ubuntuPlatforms,
		"Permitting empty passwords lets an account with no password be used to log in over the network.",
		"Set `PermitEmptyPasswords no` in /etc/ssh/sshd_config and reload the SSH server.",
		"Re-run the assessment; this check passes when the effective global setting refuses empty passwords.",
		func(obs assessment.Observation) assessment.Eval {
			value, set := sshDirective(obs, "PermitEmptyPasswords")
			if !set {
				return unknownEval("PermitEmptyPasswords is not set globally; the compiled-in default applies and is not asserted" + sshConditionalNotes(obs, "PermitEmptyPasswords"))
			}
			switch value {
			case "no":
				return assessment.Eval{Result: assessment.Pass, Detail: "PermitEmptyPasswords no"}
			case "yes":
				return assessment.Eval{Result: assessment.Fail, Detail: "PermitEmptyPasswords yes permits empty passwords"}
			default:
				// Same honesty rule as the other SSH checks: a value this rule
				// does not recognise supports neither verdict.
				return unknownEval("PermitEmptyPasswords has an unrecognised value (" + value + "); sshd accepts only yes or no")
			}
		},
		"A host without an SSH server reports this check as NOT_SUPPORTED rather than passing it.",
		"An unrecognised value is reported as UNKNOWN rather than as a pass or a failure.")
}

func securityUnitsHealthy() assessment.CheckDefinition {
	return mkCheck("security_units_active", "services", "platform", "Security-relevant services are not failed",
		assessment.SeverityMedium, ubuntuPlatforms,
		"A security control whose systemd unit has failed is not protecting the device, even when its configuration looks correct.",
		"Inspect the failing unit with `systemctl status <unit>` and restart or repair it.",
		"Re-run the assessment; this check passes when no present security-relevant unit is in the failed state.",
		func(obs assessment.Observation) assessment.Eval {
			failed := failedSecurityUnits(obs)
			if len(failed) > 0 {
				return assessment.Eval{Result: assessment.Fail, Detail: "failed security-relevant units: " + strings.Join(failed, ", ")}
			}
			present, ok := intValue(obs, "units_present")
			if !ok {
				return unknownEval("the unit inventory could not be read")
			}
			if present == 0 {
				return unknownEval("none of the tracked security units are present on this system")
			}
			return assessment.Eval{Result: assessment.Pass, Detail: "no tracked security-relevant unit is in the failed state"}
		},
		"Only an allowlist of security-relevant units is examined; a unit that is absent from this system is reported as absent, not as failed.")
}

// failedSecurityUnits reports the allowlisted units observed in the failed
// state, using only the collector's factual key/value evidence.
func failedSecurityUnits(obs assessment.Observation) []string {
	var failed []string
	for _, unit := range securityRelevantServices {
		if state, ok := stringValue(obs, "unit."+unit+".active"); ok && state == "failed" {
			failed = append(failed, unit)
		}
	}
	return failed
}
