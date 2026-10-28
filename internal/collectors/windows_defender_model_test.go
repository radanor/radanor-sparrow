package collectors

import (
	"strings"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// These fixtures drive the real Defender parsing and the real evaluator, which
// is why the model carries no build tag: the CE-5 review found Windows code
// that could only ever be cross-compiled, and a fabricated `false` for a
// missing property is exactly the kind of defect that survives untested code.

// defenderObservationFromJSON runs a fixture document through the same decode
// and evidence path the Windows collector uses.
func defenderObservationFromJSON(t *testing.T, raw, products string) assessment.Observation {
	t.Helper()
	obs := assessment.Observation{
		ID: "windows_defender", Collector: "windows_defender", Platform: "windows",
		Status: assessment.Collected, Source: "fixture", CollectedAt: time.Now(),
	}
	reading, err := parseDefenderStatus(raw)
	if err != nil {
		obs.Status = assessment.Incomplete
		obs.Error = err.Error()
		return obs
	}
	obs.Values = defenderEvidence(reading)
	if products != "" {
		names, perr := parseSecurityCenterProducts(products)
		if perr != nil {
			t.Fatalf("fixture products: %v", perr)
		}
		obs.Values = append(obs.Values, presentValue("third_party_antivirus", strings.Join(names, ",")))
	}
	if reading.Present == 0 {
		obs.Status = assessment.Incomplete
		obs.Error = "the Defender API returned no protection properties"
	}
	return obs
}

func defenderCheck() assessment.CheckDefinition { return defenderEnabled() }

// TestDefenderMissingPropertiesAreNeverReportedAsDisabled is the M3 regression.
// A document that omits a property must produce UNKNOWN, not a confident
// failure built from an invented false.
func TestDefenderMissingPropertiesAreNeverReportedAsDisabled(t *testing.T) {
	cases := []struct {
		name        string
		json        string
		wantPartial bool
	}{
		// A document with nothing usable in it is an incomplete collection.
		{"every protection property absent", `{}`, false},
		{"nulls are not affirmative values", `{"AntivirusEnabled":null,"RealTimeProtectionEnabled":null}`, false},
		// A document with some of the properties is a genuine, partial reading:
		// what it did return is still evidence, so it stays `collected` and only
		// the absent property loses its verdict.
		{"only the engine is reported", `{"AntivirusEnabled":true,"AMRunningMode":"Normal"}`, true},
		{"only real-time protection is reported", `{"RealTimeProtectionEnabled":true,"AMRunningMode":"Normal"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs := defenderObservationFromJSON(t, tc.json, "")
			if tc.wantPartial && obs.Status != assessment.Collected {
				t.Fatalf("status = %q (%s), a partial document is still a reading", obs.Status, obs.Error)
			}
			got := defenderCheck().Evaluate(obs)
			if got.Result != assessment.Unknown {
				t.Fatalf("result = %q (%s), want UNKNOWN: a property the API did not return must never be scored as disabled",
					got.Result, got.Detail)
			}
		})
	}
}

// TestDefenderMissingPropertyIsAbsentFromEvidence checks the evidence itself,
// not only the verdict: the key must be missing, with the absence recorded as a
// count, rather than present and false.
func TestDefenderMissingPropertyIsAbsentFromEvidence(t *testing.T) {
	obs := defenderObservationFromJSON(t, `{"AntivirusEnabled":true,"AMRunningMode":"Normal"}`, "")
	if _, ok := boolValue(obs, "antivirus_enabled"); !ok {
		t.Fatal("the reported engine state should be present")
	}
	if _, ok := boolValue(obs, "realtime_protection_enabled"); ok {
		t.Fatal("realtime_protection_enabled was fabricated from a property the API did not return")
	}
	missing, ok := intValue(obs, "defender_properties_missing")
	if !ok || missing == 0 {
		t.Fatalf("defender_properties_missing = %d (%v), want it to record the absence", missing, ok)
	}
	if absent, ok := stringValue(obs, "defender_absent_properties"); !ok || !strings.Contains(absent, "RealTimeProtectionEnabled") {
		t.Fatalf("defender_absent_properties = %q, want it to name the absent property", absent)
	}
}

// TestDefenderExplicitFalseValuesStillFail keeps detection intact: an
// affirmative false is a real finding and must not be softened by the fix.
func TestDefenderExplicitFalseValuesStillFail(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"engine disabled", `{"AntivirusEnabled":false,"RealTimeProtectionEnabled":true,"AMRunningMode":"Normal"}`},
		{"real-time protection disabled", `{"AntivirusEnabled":true,"RealTimeProtectionEnabled":false,"AMRunningMode":"Normal"}`},
		{"both disabled", `{"AntivirusEnabled":false,"RealTimeProtectionEnabled":false,"AMRunningMode":"Normal"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := defenderCheck().Evaluate(defenderObservationFromJSON(t, tc.json, ""))
			if got.Result != assessment.Fail {
				t.Fatalf("result = %q (%s), want FAIL for an affirmative disabled state", got.Result, got.Detail)
			}
		})
	}
}

func TestDefenderHealthyConfigurationPasses(t *testing.T) {
	obs := defenderObservationFromJSON(t,
		`{"AMServiceEnabled":true,"AntivirusEnabled":true,"RealTimeProtectionEnabled":true,"BehaviorMonitorEnabled":true,"IoavProtectionEnabled":true,"NISEnabled":true,"AMRunningMode":"Normal","AntivirusSignatureVersion":"1.413.0.0","AntivirusSignatureAge":0}`,
		`[]`)
	if obs.Status != assessment.Collected {
		t.Fatalf("status = %q (%s)", obs.Status, obs.Error)
	}
	if got := defenderCheck().Evaluate(obs); got.Result != assessment.Pass {
		t.Fatalf("result = %q (%s), want pass", got.Result, got.Detail)
	}
	sig := defenderSignaturesCurrent().Evaluate(obs)
	if sig.Result != assessment.Pass {
		t.Fatalf("signature check = %q (%s), want pass", sig.Result, sig.Detail)
	}
}

// TestDefenderPassiveModeIsNotAFailure covers the distinction the review asked
// for: a passive Defender is a Defender that another product replaced, and its
// own flags say nothing about the device's protection.
func TestDefenderPassiveModeIsNotAFailure(t *testing.T) {
	t.Run("passive with a named third-party product", func(t *testing.T) {
		obs := defenderObservationFromJSON(t,
			`{"AntivirusEnabled":false,"RealTimeProtectionEnabled":false,"AMRunningMode":"Passive"}`,
			`[{"displayName":"Acme Endpoint Protection"}]`)
		got := defenderCheck().Evaluate(obs)
		if got.Result != assessment.Unknown {
			t.Fatalf("result = %q (%s), want UNKNOWN for a passive Defender", got.Result, got.Detail)
		}
		if !strings.Contains(got.Detail, "Acme Endpoint Protection") {
			t.Fatalf("detail = %q, want it to name the active product", got.Detail)
		}
		if strings.Contains(strings.ToLower(got.Detail), "disabled") {
			t.Fatalf("detail = %q, a passive Defender must not be described as disabled", got.Detail)
		}
	})

	t.Run("passive without a readable third-party product", func(t *testing.T) {
		obs := defenderObservationFromJSON(t,
			`{"AntivirusEnabled":false,"RealTimeProtectionEnabled":false,"AMRunningMode":"Passive"}`, "")
		got := defenderCheck().Evaluate(obs)
		if got.Result != assessment.Unknown {
			t.Fatalf("result = %q, want UNKNOWN", got.Result)
		}
		if !strings.Contains(got.Detail, "passive") {
			t.Fatalf("detail = %q, want it to say Defender is passive", got.Detail)
		}
	})

	t.Run("defender not running at all", func(t *testing.T) {
		obs := defenderObservationFromJSON(t, `{"AMRunningMode":"None"}`, `[{"displayName":"Contoso AV"}]`)
		if got := defenderCheck().Evaluate(obs); got.Result != assessment.Unknown {
			t.Fatalf("result = %q, want UNKNOWN when Defender is not running", got.Result)
		}
	})

	t.Run("audit mode records rather than blocks", func(t *testing.T) {
		obs := defenderObservationFromJSON(t, `{"AntivirusEnabled":true,"RealTimeProtectionEnabled":true,"AMRunningMode":"Audit"}`, "")
		if got := defenderCheck().Evaluate(obs); got.Result != assessment.Unknown {
			t.Fatalf("result = %q, want UNKNOWN in audit mode", got.Result)
		}
	})
}

// TestDefenderModeIsReadNotInferred keeps an unknown mode from being treated as
// normal: the evaluator must fall through to the property checks only when the
// mode says Defender is the active engine.
func TestDefenderModeIsReadNotInferred(t *testing.T) {
	for _, raw := range []string{`{"AMRunningMode":"SomethingNew"}`, `{"AMRunningMode":""}`, `{}`} {
		reading, err := parseDefenderStatus(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", raw, err)
		}
		if reading.Mode != defenderModeUnknown {
			t.Fatalf("mode for %s = %q, want unknown", raw, reading.Mode)
		}
	}
	obs := defenderObservationFromJSON(t, `{"AntivirusEnabled":false,"RealTimeProtectionEnabled":false,"AMRunningMode":"SomethingNew"}`, "")
	if got := defenderCheck().Evaluate(obs); got.Result != assessment.Fail {
		t.Fatalf("result = %q, want fail: an unrecognised mode must not hide an affirmative disabled state", got.Result)
	}
}

func TestDefenderMalformedOutputIsInconclusive(t *testing.T) {
	for _, raw := range []string{"", "   ", "<html>not json</html>", `{"AntivirusEnabled":tr`} {
		if _, err := parseDefenderStatus(raw); err == nil {
			t.Fatalf("input %q was accepted as a Defender document", raw)
		} else if err != errPowerShellMalformed {
			t.Fatalf("error = %v, want errPowerShellMalformed", err)
		}
	}
	obs := defenderObservationFromJSON(t, "not json", "")
	if obs.Status != assessment.Incomplete {
		t.Fatalf("status = %q, want incomplete for malformed output", obs.Status)
	}
	if got := defenderCheck().Evaluate(obs); got.Result != assessment.Unknown {
		t.Fatalf("result = %q, want UNKNOWN", got.Result)
	}
}

func TestSecurityCenterProductParsing(t *testing.T) {
	t.Run("an array of products", func(t *testing.T) {
		got, err := parseSecurityCenterProducts(`[{"displayName":"Acme AV"},{"displayName":"Defender"}]`)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0] != "Acme AV" {
			t.Fatalf("products = %v, want only the third-party entry", got)
		}
	})
	t.Run("a single product object", func(t *testing.T) {
		got, err := parseSecurityCenterProducts(`{"displayName":"Contoso Endpoint"}`)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0] != "Contoso Endpoint" {
			t.Fatalf("products = %v", got)
		}
	})
	t.Run("no products", func(t *testing.T) {
		got, err := parseSecurityCenterProducts(`[]`)
		if err != nil || len(got) != 0 {
			t.Fatalf("products = %v (%v), want none", got, err)
		}
	})
	t.Run("malformed output is an error", func(t *testing.T) {
		if _, err := parseSecurityCenterProducts("nonsense"); err == nil {
			t.Fatal("malformed product output was accepted")
		}
	})
}

func TestAccessDeniedMarkersAreRecognised(t *testing.T) {
	for _, output := range []string{
		"Get-MpComputerStatus : Access is denied. (Exception from HRESULT: 0x80070005)",
		"Access denied",
		"AccessDenied",
		"UnauthorizedAccessException caught",
		"provider failure 0x80041003",
	} {
		if !accessDeniedOutput(output) {
			t.Errorf("output %q was not recognised as a refusal", output)
		}
	}
	for _, output := range []string{"", "AntivirusEnabled  true", "no such class"} {
		if accessDeniedOutput(output) {
			t.Errorf("output %q was misread as a refusal", output)
		}
	}
	status, err := classifyPowerShellFailure("Access is denied")
	if status != assessment.PermissionDenied || err != errPowerShellDenied {
		t.Fatalf("classification = %q (%v), want permission_denied", status, err)
	}
}

// TestDefenderEvidenceCarriesNoVerdict keeps the collector/verifier separation
// observable in the Windows path too.
func TestDefenderEvidenceCarriesNoVerdict(t *testing.T) {
	obs := defenderObservationFromJSON(t, `{"AntivirusEnabled":true,"AMRunningMode":"Normal"}`, `[]`)
	for _, v := range obs.Values {
		switch v.Key {
		case "pass", "fail", "result", "verdict":
			t.Errorf("collector emitted a verdict-shaped evidence key: %q", v.Key)
		}
	}
}
