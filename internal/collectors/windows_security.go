//go:build windows

package collectors

import (
	"context"
	"sort"
	"strings"

	"github.com/radanor/radanor-shield/internal/assessment"
	"golang.org/x/sys/windows/registry"
)

// DefenderCollector reports Microsoft Defender's protection state and
// signature freshness.
//
// Source: the MSFT_MpComputerStatus CIM class, queried through PowerShell and
// returned as JSON, plus the Security Center antivirus list, which is how
// "another product is protecting this device" is established rather than
// assumed. Only English property identifiers are read, so the result does not
// depend on the display language. No command here changes any state: every
// cmdlet used is read-only.
//
// The decoding and the verdict live in windows_defender_model.go, which has no
// build tag, so a property the API did not return is never turned into an
// affirmative `false` here. That fabrication is the defect this collector was
// corrected for.
//
// This collector is cross-compiled but has not been validated on a native
// Windows host; that validation is recorded as outstanding in the CE-5
// acceptance document.
type DefenderCollector struct{}

func (DefenderCollector) ID() string { return "windows_defender" }

func (DefenderCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "windows_defender", Collector: "windows_defender", Platform: windowsPlatformID,
		Status: assessment.Collected, Source: "MSFT_MpComputerStatus (CIM) + SecurityCenter2 AntiVirusProduct",
	}
	const script = `$s = Get-MpComputerStatus -ErrorAction Stop; ` +
		`[pscustomobject]@{ ` +
		`AMServiceEnabled=$s.AMServiceEnabled; AntivirusEnabled=$s.AntivirusEnabled; ` +
		`RealTimeProtectionEnabled=$s.RealTimeProtectionEnabled; BehaviorMonitorEnabled=$s.BehaviorMonitorEnabled; ` +
		`IoavProtectionEnabled=$s.IoavProtectionEnabled; NISEnabled=$s.NISEnabled; ` +
		`AMRunningMode=[string]$s.AMRunningMode; ` +
		`AntivirusSignatureVersion=[string]$s.AntivirusSignatureVersion; ` +
		`AntivirusSignatureAge=[int]$s.AntivirusSignatureAge; ` +
		`AntivirusSignatureLastUpdated=[string]($s.AntivirusSignatureLastUpdated.ToString('o')) ` +
		`} | ConvertTo-Json -Compress`
	defenderRes, cmdErr := runCommand(ctx, "powershell", powershellArgs(script)...)
	raw := defenderRes.Output
	if cmdErr != nil && isMissingTool(cmdErr) {
		obs.Status = assessment.Unsupported
		obs.Error = "PowerShell is not available on this system"
		return []assessment.Observation{obs}, nil
	}
	if defenderRes.Truncated {
		// A clipped status document cannot be decoded, and must not be reported
		// as an absent or unprotected Defender.
		obs.Status = assessment.Incomplete
		obs.Error = boundText(truncatedNote("the Defender status query", defenderRes.Cap))
		return []assessment.Observation{obs}, nil
	}
	// A refusal is a refusal, and it is checked on the output rather than on
	// the exit status: PowerShell writes the CIM error to the stream and does
	// not reliably propagate it as a failing exit code.
	if accessDeniedOutput(raw) {
		obs.Status = assessment.PermissionDenied
		obs.Error = errPowerShellDenied.Error() + "; Microsoft Defender status requires more than this user has"
		return []assessment.Observation{obs}, nil
	}
	reading, parseErr := parseDefenderStatus(raw)
	if parseErr != nil {
		obs.Status = assessment.Incomplete
		if strings.TrimSpace(raw) == "" {
			obs.Error = errPowerShellEmpty.Error() + "; the Defender status returned no document"
		} else {
			obs.Error = errPowerShellMalformed.Error() + "; the Defender status document was not decodable"
		}
		return []assessment.Observation{obs}, nil
	}

	obs.Values = defenderEvidence(reading)
	if thirdParty := collectThirdPartyAntivirus(ctx); thirdParty.Key != "" {
		obs.Values = append(obs.Values, thirdParty)
	}

	// Nothing usable came back at all: that is an incomplete collection, not a
	// reading of a protection state. A partial document is left as `collected`
	// because the properties that were returned are still genuine facts, and
	// the evaluator reports UNKNOWN for each one that is absent rather than
	// downgrading the checks that do have evidence.
	if reading.Present == 0 {
		obs.Status = assessment.Incomplete
		obs.Error = "the Defender API returned no protection properties"
	}
	return []assessment.Observation{obs}, nil
}

// collectThirdPartyAntivirus reports which non-Defender antivirus products
// Windows Security Center knows about. Its absence of evidence is recorded
// explicitly: the evidence key is present, and empty, only when the query
// succeeded.
func collectThirdPartyAntivirus(ctx context.Context) assessment.Value {
	const script = `$v = @(Get-CimInstance -Namespace 'root/SecurityCenter2' -ClassName AntiVirusProduct -ErrorAction Stop | ` +
		`ForEach-Object { [pscustomobject]@{ displayName=[string]$_.displayName } }); ` +
		`ConvertTo-Json -InputObject $v -Compress`
	res, err := runCommand(ctx, "powershell", powershellArgs(script)...)
	if err != nil || res.Truncated || accessDeniedOutput(res.Output) {
		return assessment.Value{}
	}
	products, perr := parseSecurityCenterProducts(res.Output)
	if perr != nil {
		return assessment.Value{}
	}
	sort.Strings(products)
	return presentValue("third_party_antivirus", strings.Join(products, ","))
}

// WindowsFirewallCollector reports the firewall enable flag for each of the
// three network profiles.
//
// Source: the documented SharedAccess FirewallPolicy registry keys, one per
// profile. Reading the registry avoids parsing the localized output of
// `netsh advfirewall`. A profile whose key is absent is reported as not
// present, never as disabled.
type WindowsFirewallCollector struct{}

func (WindowsFirewallCollector) ID() string { return "windows_firewall" }

var firewallProfiles = []struct {
	name string
	path string
}{
	{"domain", `SYSTEM\CurrentControlSet\Services\SharedAccess\Parameters\FirewallPolicy\DomainProfile`},
	{"private", `SYSTEM\CurrentControlSet\Services\SharedAccess\Parameters\FirewallPolicy\StandardProfile`},
	{"public", `SYSTEM\CurrentControlSet\Services\SharedAccess\Parameters\FirewallPolicy\PublicProfile`},
}

func (WindowsFirewallCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "windows_firewall", Collector: "windows_firewall", Platform: windowsPlatformID,
		Status: assessment.Collected, Source: "FirewallPolicy registry keys",
	}
	enabled, disabled, unknown := 0, 0, 0
	for _, p := range firewallProfiles {
		value, ok := regDWORD(registry.LOCAL_MACHINE, p.path, "EnableFirewall")
		if !ok {
			unknown++
			obs.Values = append(obs.Values, presentValue("profile."+p.name+".state", "not_present"))
			continue
		}
		if value == 1 {
			enabled++
			obs.Values = append(obs.Values, presentValue("profile."+p.name+".state", "enabled"))
			continue
		}
		disabled++
		obs.Values = append(obs.Values, presentValue("profile."+p.name+".state", "disabled"))
	}
	obs.Values = append(obs.Values,
		presentInt("profiles_enabled", int64(enabled)),
		presentInt("profiles_disabled", int64(disabled)),
		presentInt("profiles_unknown", int64(unknown)),
	)
	if enabled == 0 && disabled == 0 {
		obs.Status = assessment.Incomplete
		obs.Error = "no firewall profile could be read; the firewall state is not known"
	}
	return []assessment.Observation{obs}, nil
}

// WindowsUpdateCollector reports the automatic-update policy and the most
// recent successful update installation time.
//
// Source: the documented WindowsUpdate policy registry location for the policy,
// and the WMI Win32_QuickFixEngineering class for the newest installed hotfix.
// The collector never installs, repairs, or triggers an update.
type WindowsUpdateCollector struct{}

func (WindowsUpdateCollector) ID() string { return "windows_update" }

func (WindowsUpdateCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "windows_update", Collector: "windows_update", Platform: windowsPlatformID,
		Status: assessment.Collected, Source: "WindowsUpdate policy registry + Win32_QuickFixEngineering",
	}
	const policyPath = `SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate\AU`
	if noAuto, ok := regDWORD(registry.LOCAL_MACHINE, policyPath, "NoAutoUpdate"); ok {
		obs.Values = append(obs.Values, presentBool("auto_update_disabled_by_policy", noAuto == 1))
	}
	if auOptions, ok := regDWORD(registry.LOCAL_MACHINE, policyPath, "AUOptions"); ok {
		obs.Values = append(obs.Values, presentInt("au_options", auOptions))
		// 2 = notify only; 3 and 4 install updates automatically.
		obs.Values = append(obs.Values, presentBool("auto_install_configured", auOptions >= 3))
	}

	const script = `$q = Get-CimInstance -ClassName Win32_QuickFixEngineering -ErrorAction Stop | ` +
		`Sort-Object -Property InstalledOn -Descending | Select-Object -First 1; ` +
		`[pscustomobject]@{ HotFixID=[string]$q.HotFixID; InstalledOn=[string]($q.InstalledOn.ToString('o')) } | ConvertTo-Json -Compress`
	var newest struct {
		HotFixID    *string `json:"HotFixID"`
		InstalledOn *string `json:"InstalledOn"`
	}
	if err := windowsPowerShellJSON(ctx, script, &newest); err == nil {
		if newest.HotFixID != nil {
			obs.Values = append(obs.Values, presentValue("latest_hotfix", *newest.HotFixID))
		}
		if newest.InstalledOn != nil {
			obs.Values = append(obs.Values, presentValue("latest_hotfix_installed", *newest.InstalledOn))
		}
	} else {
		obs.Status = assessment.Incomplete
		obs.Error = "the installed-update history could not be read"
	}
	if len(obs.Values) == 0 {
		obs.Status = assessment.Incomplete
		obs.Error = "no Windows Update configuration could be read"
	}
	return []assessment.Observation{obs}, nil
}
