//go:build windows

package collectors

import (
	"strings"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// PlatformCollectors returns the native Windows collector set.
//
// NOTE: these collectors have been cross-compiled for windows/amd64 but have
// not been validated against a native Windows 11 x64 host. That validation is
// recorded as outstanding in the private engineering record; nothing here claims
// verified native behaviour.
func PlatformCollectors() []assessment.Collector {
	return []assessment.Collector{
		WindowsPlatformCollector{},
		WindowsSecurityConfigCollector{},
		DefenderCollector{},
		WindowsFirewallCollector{},
		WindowsUpdateCollector{},
		BitLockerCollector{},
		// Context collectors: genuine inventory facts without a PASS/FAIL rule.
		WindowsApplicationsCollector{},
		WindowsServicesCollector{},
		WindowsNetworkCollector{},
	}
}

// PlatformChecks returns the versioned Windows check definitions. The two
// Defender checks live in windows_defender_model.go, which carries no build
// tag, so their parsing and evaluation are exercised by the suite that runs on
// every platform rather than only being cross-compiled.
func PlatformChecks() []assessment.CheckDefinition {
	return []assessment.CheckDefinition{
		defenderEnabled(),
		defenderSignaturesCurrent(),
		windowsFirewallEnabled(),
		bitlockerProtected(),
		windowsUpdateConfigured(),
		windowsSecurityControls(),
		windowsServicesHealthy(),
	}
}

func windowsFirewallEnabled() assessment.CheckDefinition {
	return mkCheck("windows_firewall_enabled", "windows_firewall", "network", "Windows Firewall protects active profiles",
		assessment.SeverityHigh, windowsPlatforms,
		"A disabled firewall profile exposes every listening service on that profile to the network.",
		"Enable Windows Firewall for all profiles in Windows Security.",
		"Re-run the assessment; this check passes when no readable profile is disabled.",
		func(obs assessment.Observation) assessment.Eval {
			enabled, _ := intValue(obs, "profiles_enabled")
			disabled, _ := intValue(obs, "profiles_disabled")
			switch {
			case enabled > 0 && disabled == 0:
				return assessment.Eval{Result: assessment.Pass, Detail: "every readable firewall profile is enabled"}
			case disabled > 0:
				return assessment.Eval{Result: assessment.Fail, Detail: "one or more firewall profiles are disabled"}
			default:
				return unknownEval("no firewall profile could be read")
			}
		},
		"Only the three built-in network profiles are examined; third-party firewalls are not observed.")
}

func bitlockerProtected() assessment.CheckDefinition {
	check := mkCheck("bitlocker_protection_enabled", "bitlocker", "storage", "BitLocker protects the operating-system volume",
		assessment.SeverityMedium, windowsPlatforms,
		"Without encryption at rest, data is readable by anyone who gains physical possession of the device.",
		"Enable BitLocker on the operating-system volume and store the recovery key safely.",
		"Re-run the assessment; this check passes when the identified operating-system volume reports protection on.",
		func(obs assessment.Observation) assessment.Eval {
			state, ok := stringValue(obs, "os_volume_protection_status")
			mount, identified := stringValue(obs, "os_volume_mountpoint")
			if !ok || !identified || mount == "" {
				return unknownEval("operating-system volume protection evidence could not be read")
			}
			if strings.EqualFold(strings.TrimSpace(state), "on") {
				return assessment.Eval{Result: assessment.Pass, Detail: "the operating-system volume reports BitLocker protection on"}
			}
			if strings.EqualFold(strings.TrimSpace(state), "off") {
				return assessment.Eval{Result: assessment.Fail, Detail: "the operating-system volume reports BitLocker protection off"}
			}
			return unknownEval("the operating-system volume did not report a decidable protection state")
		},
		"Unreadable, missing or conflicting OS-volume evidence is UNKNOWN. Data-volume protection does not establish OS-volume protection.")
	check.Version = "1.1.0"
	return check
}

func windowsUpdateConfigured() assessment.CheckDefinition {
	return mkCheck("windows_update_configured", "windows_update", "updates", "Windows Update is not disabled by policy",
		assessment.SeverityMedium, windowsPlatforms,
		"A device whose update mechanism is switched off accumulates unpatched vulnerabilities.",
		"Remove the policy that disables automatic updates, or point Windows Update at a managed update source.",
		"Re-run the assessment; this check passes when updates are not disabled and automatic installation is configured.",
		func(obs assessment.Observation) assessment.Eval {
			if disabled, ok := boolValue(obs, "auto_update_disabled_by_policy"); ok && disabled {
				return assessment.Eval{Result: assessment.Fail, Detail: "automatic updates are disabled by policy"}
			}
			if installed, ok := boolValue(obs, "auto_install_configured"); ok && installed {
				return assessment.Eval{Result: assessment.Pass, Detail: "automatic update installation is configured"}
			}
			return unknownEval("no explicit update policy is configured; the platform default applies and is not asserted")
		},
		"Only policy configuration is read; the collector cannot enumerate pending updates without querying the update service.")
}

func windowsSecurityControls() assessment.CheckDefinition {
	return mkCheck("windows_security_controls", "windows_security_config", "platform", "Local security protections are enabled",
		assessment.SeverityMedium, windowsPlatforms,
		"User Account Control, LSA protection and credential guard are documented mitigations against local privilege escalation and credential theft.",
		"Re-enable the disabled protections in Local Security Policy or through your management tooling.",
		"Re-run the assessment; this check passes when every readable protection reports enabled.",
		func(obs assessment.Observation) assessment.Eval {
			var disabled []string
			readable := 0
			for _, key := range []string{"uac_enabled", "lsa_protection_enabled", "vbs_enabled", "credential_guard_enabled"} {
				value, ok := boolValue(obs, key)
				if !ok {
					continue
				}
				readable++
				if !value {
					disabled = append(disabled, key)
				}
			}
			if readable == 0 {
				return unknownEval("no local security protection state could be read")
			}
			if len(disabled) > 0 {
				return assessment.Eval{Result: assessment.Fail, Detail: "disabled protections: " + strings.Join(disabled, ", ")}
			}
			return assessment.Eval{Result: assessment.Pass, Detail: "every readable local protection is enabled"}
		},
		"Only a reviewed subset of protections is examined; a value that is absent is not counted either way.")
}

func windowsServicesHealthy() assessment.CheckDefinition {
	return mkCheck("windows_services_active", "windows_services", "platform", "Security services are running",
		assessment.SeverityMedium, windowsPlatforms,
		"A security service that is configured to start automatically but is stopped is not protecting the device.",
		"Start the stopped service and check the System event log for the reason it stopped.",
		"Re-run the assessment; this check passes when no automatically-started security service is stopped.",
		func(obs assessment.Observation) assessment.Eval {
			present, ok := intValue(obs, "services_present")
			if !ok || present == 0 {
				return unknownEval("no security service state could be read")
			}
			var stopped []string
			for _, name := range windowsSecurityServices {
				status, sok := stringValue(obs, "service."+name+".status")
				start, aok := stringValue(obs, "service."+name+".start")
				if !sok || !aok {
					continue
				}
				if status == "stopped" && start == "automatic" {
					stopped = append(stopped, name)
				}
			}
			if len(stopped) > 0 {
				return assessment.Eval{Result: assessment.Fail, Detail: "stopped automatic services: " + strings.Join(stopped, ", ")}
			}
			return assessment.Eval{Result: assessment.Pass, Detail: "no automatically-started security service is stopped"}
		},
		"A service that is absent on this edition is reported as absent and does not affect the result.")
}
