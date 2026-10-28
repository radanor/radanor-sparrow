package collectors

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// Microsoft Defender, and the two Windows checks that read it.
//
// This file deliberately carries no build tag. The CE-5 review could not verify
// any Windows behaviour on a native host, so the parsing and the evaluation are
// kept here where the test suite that runs on every change can actually drive
// them from fixtures. Only the command execution that produces the JSON lives
// behind the windows build tag.
//
// The rule this model exists to enforce: a property the API did not return is
// never reported as a disabled one. The previous collector filled every absent
// pointer with `false`, so a host that returned a partial document was shown as
// "real-time protection disabled" - a confident failure invented from missing
// data.

// errPowerShell* distinguish the ways a Windows CIM query can fail, so a
// collector can report refusal, absence and malformed output as the different
// facts they are instead of collapsing them into one "not supported".
var (
	errPowerShellMissing   = errors.New("powershell is not available")
	errPowerShellDenied    = errors.New("the query was refused by the operating system")
	errPowerShellEmpty     = errors.New("no structured output was returned")
	errPowerShellMalformed = errors.New("structured output could not be decoded")
)

// accessDeniedOutput reports whether a Windows command failed because the
// operating system refused the request. Windows surfaces this as a localized
// message plus a stable HRESULT, so the stable markers are matched: the
// documented access-denied codes, and the English strings the CIM providers
// emit on an English install.
//
// Native verification of the exact text on a non-English install is outstanding
// and recorded in the private engineering record; until then a refusal that this does
// not recognise falls back to a generic failure rather than to a verdict.
func accessDeniedOutput(output string) bool {
	lower := strings.ToLower(output)
	for _, marker := range []string{
		"access is denied",
		"access denied",
		"accessdenied",
		"unauthorizedaccess",
		"0x80070005", // E_ACCESSDENIED
		"0x80041003", // WBEM_E_ACCESS_DENIED
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// classifyPowerShellFailure maps a completed-but-failed command's output onto
// the collection status that describes it honestly.
func classifyPowerShellFailure(output string) (assessment.CollectionStatus, error) {
	if accessDeniedOutput(output) {
		return assessment.PermissionDenied, errPowerShellDenied
	}
	return assessment.Failed, errors.New("the Windows query did not complete")
}

// defenderStatus is the shape ConvertTo-Json produces for the Defender status
// object. Every field is a pointer so an absent property is distinguishable
// from an affirmative false.
type defenderStatus struct {
	AMServiceEnabled              *bool   `json:"AMServiceEnabled"`
	AntivirusEnabled              *bool   `json:"AntivirusEnabled"`
	RealTimeProtectionEnabled     *bool   `json:"RealTimeProtectionEnabled"`
	BehaviorMonitorEnabled        *bool   `json:"BehaviorMonitorEnabled"`
	IoavProtectionEnabled         *bool   `json:"IoavProtectionEnabled"`
	NISEnabled                    *bool   `json:"NISEnabled"`
	AMRunningMode                 *string `json:"AMRunningMode"`
	AntivirusSignatureVersion     *string `json:"AntivirusSignatureVersion"`
	AntivirusSignatureAge         *int64  `json:"AntivirusSignatureAge"`
	AntivirusSignatureLastUpdated *string `json:"AntivirusSignatureLastUpdated"`
}

// securityCenterProduct is one entry of the Security Center antivirus list,
// which is how a third-party product in charge of the device is observed.
type securityCenterProduct struct {
	DisplayName *string `json:"displayName"`
}

// defenderMode describes how Defender is running, read from the documented
// AMRunningMode property rather than inferred from the protection booleans, so
// a deliberately passive installation is never mistaken for a broken one.
type defenderMode string

const (
	defenderModeUnknown defenderMode = ""
	defenderModeNormal  defenderMode = "normal"
	defenderModePassive defenderMode = "passive"
	defenderModeAudit   defenderMode = "audit"
	defenderModeNone    defenderMode = "none"
)

// parseDefenderMode maps the documented AMRunningMode values. An unrecognised
// value stays unknown rather than being treated as normal.
func parseDefenderMode(raw *string) defenderMode {
	if raw == nil {
		return defenderModeUnknown
	}
	switch strings.ToLower(strings.TrimSpace(*raw)) {
	case "normal":
		return defenderModeNormal
	case "passive":
		return defenderModePassive
	case "audit", "audit mode":
		return defenderModeAudit
	case "none":
		return defenderModeNone
	default:
		return defenderModeUnknown
	}
}

// defenderReading is the decoded, classified result of one Defender query.
type defenderReading struct {
	Status   defenderStatus
	Mode     defenderMode
	Present  int
	Missing  int
	Absent   []string
	Products []string
}

// parseDefenderStatus decodes the ConvertTo-Json document. A document that is
// not valid JSON is an error, never a default reading.
func parseDefenderStatus(raw string) (defenderReading, error) {
	var status defenderStatus
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &status); err != nil {
		return defenderReading{}, errPowerShellMalformed
	}
	reading := defenderReading{Status: status, Mode: parseDefenderMode(status.AMRunningMode)}
	for name, present := range map[string]bool{
		"AMServiceEnabled":          status.AMServiceEnabled != nil,
		"AntivirusEnabled":          status.AntivirusEnabled != nil,
		"RealTimeProtectionEnabled": status.RealTimeProtectionEnabled != nil,
		"BehaviorMonitorEnabled":    status.BehaviorMonitorEnabled != nil,
		"IoavProtectionEnabled":     status.IoavProtectionEnabled != nil,
		"NISEnabled":                status.NISEnabled != nil,
	} {
		if present {
			reading.Present++
		} else {
			reading.Missing++
			reading.Absent = append(reading.Absent, name)
		}
	}
	return reading, nil
}

// parseSecurityCenterProducts decodes the antivirus products registered with
// Windows Security Center, which is how "another product is protecting this
// device" is established instead of assumed.
func parseSecurityCenterProducts(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errPowerShellEmpty
	}
	var products []securityCenterProduct
	if err := json.Unmarshal([]byte(trimmed), &products); err != nil {
		// A single product comes back as an object rather than an array.
		var single securityCenterProduct
		if err2 := json.Unmarshal([]byte(trimmed), &single); err2 != nil {
			return nil, errPowerShellMalformed
		}
		products = []securityCenterProduct{single}
	}
	names := make([]string, 0, len(products))
	for _, p := range products {
		if p.DisplayName == nil || strings.TrimSpace(*p.DisplayName) == "" {
			continue
		}
		name := strings.TrimSpace(*p.DisplayName)
		// Defender itself reports through Security Center; it is not a
		// third-party product and must not be counted as one.
		if strings.Contains(strings.ToLower(name), "defender") {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

// defenderEvidence builds the observation's values. A protection property is
// emitted only when the API returned it, so the evidence records what was
// actually observed and what was not.
func defenderEvidence(reading defenderReading) []assessment.Value {
	var values []assessment.Value
	emit := func(key string, pointer *bool) {
		if pointer == nil {
			return
		}
		values = append(values, presentBool(key, *pointer))
	}
	emit("defender_service_enabled", reading.Status.AMServiceEnabled)
	emit("antivirus_enabled", reading.Status.AntivirusEnabled)
	emit("realtime_protection_enabled", reading.Status.RealTimeProtectionEnabled)
	emit("behavior_monitor_enabled", reading.Status.BehaviorMonitorEnabled)
	emit("ioav_protection_enabled", reading.Status.IoavProtectionEnabled)
	emit("network_inspection_enabled", reading.Status.NISEnabled)

	values = append(values,
		presentInt("defender_properties_present", int64(reading.Present)),
		presentInt("defender_properties_missing", int64(reading.Missing)),
	)
	if reading.Mode != defenderModeUnknown {
		values = append(values, presentValue("defender_mode", string(reading.Mode)))
	}
	if len(reading.Absent) > 0 {
		values = append(values, presentValue("defender_absent_properties", strings.Join(reading.Absent, ",")))
	}
	if reading.Status.AntivirusSignatureVersion != nil {
		values = append(values, presentValue("signature_version", *reading.Status.AntivirusSignatureVersion))
	}
	if reading.Status.AntivirusSignatureAge != nil {
		values = append(values, presentInt("signature_age_days", *reading.Status.AntivirusSignatureAge))
	}
	if reading.Status.AntivirusSignatureLastUpdated != nil {
		values = append(values, presentValue("signature_updated", *reading.Status.AntivirusSignatureLastUpdated))
	}
	return values
}

var windowsPlatforms = []string{"windows"}

func defenderEnabled() assessment.CheckDefinition {
	return mkCheck("defender_protection_enabled", "windows_defender", "malware-protection", "Microsoft Defender protection is active",
		assessment.SeverityHigh, windowsPlatforms,
		"Real-time protection is what allows the platform to block known malicious software before it runs.",
		"Enable Microsoft Defender real-time protection in Windows Security, or confirm the third-party antivirus reporting through Security Center.",
		"Re-run the assessment; this check passes when Defender reports both the antivirus engine and real-time protection enabled.",
		func(obs assessment.Observation) assessment.Eval {
			// A deliberately passive or absent Defender is not a failure of
			// this check: the question is whether the device is protected, and
			// answering it from Defender's own flags when Defender is not the
			// active engine would misreport a host that another product covers.
			if mode, known := stringValue(obs, "defender_mode"); known {
				if mode == string(defenderModePassive) {
					if third, readable := stringValue(obs, "third_party_antivirus"); readable && third != "" {
						return unknownEval("Microsoft Defender is running in passive mode and " + third + " is the active antivirus; Defender's own protection flags do not describe this device's protection")
					}
					return unknownEval("Microsoft Defender is running in passive mode and is not the active antivirus engine; which product is protecting this device is not established")
				}
				if mode == string(defenderModeNone) {
					return unknownEval("Microsoft Defender is reported as not running; no verdict is drawn without knowing what is protecting this device")
				}
				if mode == string(defenderModeAudit) {
					return unknownEval("Microsoft Defender is running in audit mode, which records rather than blocks; the device is not being protected by it")
				}
			}
			// Absent properties are never read as "disabled". A document that
			// did not include a flag supports no verdict about that flag.
			engine, ok := boolValue(obs, "antivirus_enabled")
			if !ok {
				return unknownEval("the Defender antivirus engine state was not returned, so no verdict is asserted from missing data")
			}
			if !engine {
				return assessment.Eval{Result: assessment.Fail, Detail: "the Defender antivirus engine is not enabled"}
			}
			rtp, ok := boolValue(obs, "realtime_protection_enabled")
			if !ok {
				return unknownEval("the real-time protection state was not returned, so no verdict is asserted from missing data")
			}
			if !rtp {
				return assessment.Eval{Result: assessment.Fail, Detail: "the antivirus engine is enabled but real-time protection is off"}
			}
			return assessment.Eval{Result: assessment.Pass, Detail: "the antivirus engine and real-time protection are both enabled"}
		},
		"A third-party antivirus can place Defender in passive mode; that is reported as UNKNOWN with the active product named, never as a Defender failure and never as a pass.",
		"A property the Defender API does not return is reported as UNKNOWN rather than as disabled.")
}

func defenderSignaturesCurrent() assessment.CheckDefinition {
	return mkCheck("defender_signatures_current", "windows_defender", "malware-protection", "Defender signatures are current",
		assessment.SeverityMedium, windowsPlatforms,
		"Outdated signatures leave protection blind to malware defined after the last update.",
		"Allow Defender to update its signatures, or run the signature update from Windows Security.",
		"Re-run the assessment; this check passes when the signature age is within the freshness budget.",
		func(obs assessment.Observation) assessment.Eval {
			age, ok := intValue(obs, "signature_age_days")
			if !ok {
				return unknownEval("the signature age was not returned, so no verdict is asserted from missing data")
			}
			if age <= 7 {
				return assessment.Eval{Result: assessment.Pass, Detail: "signatures are within seven days"}
			}
			return assessment.Eval{Result: assessment.Fail, Detail: "signatures are older than seven days"}
		},
		"Seven days is the freshness budget; a disconnected device may legitimately be unable to update.")
}
