//go:build windows

package collectors

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
)

func TestBitLockerEvidenceAndOSVolume(t *testing.T) {
	osOn := `{"MountPoint":"C:","VolumeType":"OperatingSystem","ProtectionStatus":"On","EncryptionPercentage":100}`
	osOff := `{"MountPoint":"C:","VolumeType":"OperatingSystem","ProtectionStatus":"Off"}`
	dataOn := `{"MountPoint":"D:","VolumeType":"Data","ProtectionStatus":"On"}`
	dataOff := `{"MountPoint":"D:","VolumeType":"Data","ProtectionStatus":"Off"}`
	tests := []struct {
		name, raw string
		err       error
		truncated bool
		status    assessment.CollectionStatus
		result    assessment.Result
	}{
		{"permission denial", "Access is denied (0x80070005)", errors.New("exit 1"), false, assessment.Incomplete, assessment.Unknown},
		{"permission denial zero exit", "0x80041003", nil, false, assessment.Incomplete, assessment.Unknown},
		{"generic query error", osOn, errors.New("CIM failure"), false, assessment.Incomplete, assessment.Unknown},
		{"missing tool", "", errors.New("powershell unavailable"), false, assessment.Incomplete, assessment.Unknown},
		{"truncated evidence", osOn, nil, true, assessment.Incomplete, assessment.Unknown},
		{"malformed JSON", "{broken", nil, false, assessment.Incomplete, assessment.Unknown},
		{"wrong JSON type", "42", nil, false, assessment.Incomplete, assessment.Unknown},
		{"empty output", "", nil, false, assessment.Incomplete, assessment.Unknown},
		{"empty array", "[]", nil, false, assessment.Incomplete, assessment.Unknown},
		{"null result", "null", nil, false, assessment.Incomplete, assessment.Unknown},
		{"empty object", "{}", nil, false, assessment.Incomplete, assessment.Unknown},
		{"null volume", "[null]", nil, false, assessment.Incomplete, assessment.Unknown},
		{"protected OS single", osOn, nil, false, assessment.Collected, assessment.Pass},
		{"unprotected OS single", osOff, nil, false, assessment.Collected, assessment.Fail},
		{"protected OS array", "[" + osOn + "]", nil, false, assessment.Collected, assessment.Pass},
		{"OS off data on", "[" + osOff + "," + dataOn + "]", nil, false, assessment.Collected, assessment.Fail},
		{"OS on data off", "[" + osOn + "," + dataOff + "]", nil, false, assessment.Collected, assessment.Pass},
		{"protected data only", dataOn, nil, false, assessment.Incomplete, assessment.Unknown},
		{"OS missing state", `{"MountPoint":"C:","VolumeType":"OperatingSystem"}`, nil, false, assessment.Incomplete, assessment.Unknown},
		{"OS unknown state", `{"MountPoint":"C:","VolumeType":"OperatingSystem","ProtectionStatus":"Unknown"}`, nil, false, assessment.Incomplete, assessment.Unknown},
		{"OS missing mount", `{"VolumeType":"OperatingSystem","ProtectionStatus":"On"}`, nil, false, assessment.Incomplete, assessment.Unknown},
		{"missing OS type", `{"MountPoint":"C:","ProtectionStatus":"On"}`, nil, false, assessment.Incomplete, assessment.Unknown},
		{"duplicate conflicting OS", "[" + osOn + "," + osOff + "]", nil, false, assessment.Incomplete, assessment.Unknown},
		{"duplicate identical OS", "[" + osOn + "," + osOn + "]", nil, false, assessment.Incomplete, assessment.Unknown},
		{"two OS mounts", "[" + osOn + `,{"MountPoint":"E:","VolumeType":"OperatingSystem","ProtectionStatus":"On"}]`, nil, false, assessment.Incomplete, assessment.Unknown},
		{"conflicting mount type", "[" + osOn + `,{"MountPoint":"c:","VolumeType":"Data","ProtectionStatus":"On"}]`, nil, false, assessment.Incomplete, assessment.Unknown},
		{"unreadable unrelated data", "[" + osOn + `,{"MountPoint":"D:","VolumeType":"Data"}]`, nil, false, assessment.Collected, assessment.Pass},
		{"partial unidentified volume", "[" + osOn + `,{"MountPoint":"D:","ProtectionStatus":"On"}]`, nil, false, assessment.Incomplete, assessment.Unknown},
		{"null alongside OS", "[" + osOn + `,null]`, nil, false, assessment.Incomplete, assessment.Unknown},
		{"OS on FixedData off", "[" + osOn + `,{"MountPoint":"D:","VolumeType":"FixedData","ProtectionStatus":"Off"}]`, nil, false, assessment.Collected, assessment.Pass},
		{"OS on RemovableData off", "[" + osOn + `,{"MountPoint":"E:","VolumeType":"RemovableData","ProtectionStatus":"Off"}]`, nil, false, assessment.Collected, assessment.Pass},
		{"OS off FixedData on", "[" + osOff + `,{"MountPoint":"D:","VolumeType":"FixedData","ProtectionStatus":"On"}]`, nil, false, assessment.Collected, assessment.Fail},
		{"OS off RemovableData on", "[" + osOff + `,{"MountPoint":"E:","VolumeType":"RemovableData","ProtectionStatus":"On"}]`, nil, false, assessment.Collected, assessment.Fail},
		{"FixedData only", `{"MountPoint":"D:","VolumeType":"FixedData","ProtectionStatus":"On"}`, nil, false, assessment.Incomplete, assessment.Unknown},
		{"RemovableData only", `{"MountPoint":"E:","VolumeType":"RemovableData","ProtectionStatus":"On"}`, nil, false, assessment.Incomplete, assessment.Unknown},
		{"OS unknown FixedData protected", `[{"MountPoint":"C:","VolumeType":"OperatingSystem","ProtectionStatus":"Unknown"},{"MountPoint":"D:","VolumeType":"FixedData","ProtectionStatus":"On"}]`, nil, false, assessment.Incomplete, assessment.Unknown},
		{"OS missing state RemovableData protected", `[{"MountPoint":"C:","VolumeType":"OperatingSystem"},{"MountPoint":"E:","VolumeType":"RemovableData","ProtectionStatus":"On"}]`, nil, false, assessment.Incomplete, assessment.Unknown},
		{"conflicting FixedData mount", "[" + osOn + `,{"MountPoint":"c:\\","VolumeType":"FixedData","ProtectionStatus":"On"}]`, nil, false, assessment.Incomplete, assessment.Unknown},
		{"conflicting RemovableData mount", "[" + osOn + `,{"MountPoint":"C:","VolumeType":"RemovableData","ProtectionStatus":"Off"}]`, nil, false, assessment.Incomplete, assessment.Unknown},
		{"unknown data type", "[" + osOn + `,{"MountPoint":"D:","VolumeType":"Unrecognized","ProtectionStatus":"On"}]`, nil, false, assessment.Incomplete, assessment.Unknown},
		{"normalized FixedData type", "[" + osOn + `,{"MountPoint":"D:","VolumeType":" fixedDATA "}]`, nil, false, assessment.Collected, assessment.Pass},
		{"OS with all data representations", "[" + osOn + `,{"MountPoint":"D:","VolumeType":"FixedData"},{"MountPoint":"E:","VolumeType":"RemovableData"},{"MountPoint":"F:","VolumeType":"Data"}]`, nil, false, assessment.Collected, assessment.Pass},
		{"normalized OS properties", `{"MountPoint":" c:\\ ","VolumeType":" operatingSystem ","ProtectionStatus":" on "}`, nil, false, assessment.Collected, assessment.Pass},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := bitlockerObservation(commandResult{Output: tt.raw, Truncated: tt.truncated}, tt.err)
			obs.CollectedAt = time.Now()
			result := bitlockerProtected().Evaluate(obs)
			if obs.Status != tt.status || result.Result != tt.result {
				t.Fatalf("status=%s result=%s error=%q; want %s %s", obs.Status, result.Result, obs.Error, tt.status, tt.result)
			}
			if obs.Status == assessment.Incomplete && obs.Error == "" {
				t.Fatal("incomplete evidence lacks explanation")
			}
		})
	}
	legacy := assessment.Observation{Status: assessment.Collected, CollectedAt: time.Now(), Values: []assessment.Value{presentInt("volumes_protected", 1)}}
	if result := bitlockerProtected().Evaluate(legacy); result.Result != assessment.Unknown {
		t.Fatal("legacy data-volume counts falsely passed")
	}
}

func TestBitLockerCollectorQueriesOnce(t *testing.T) {
	for _, raw := range []string{`{"MountPoint":"C:","VolumeType":"OperatingSystem","ProtectionStatus":"On"}`, `[{"MountPoint":"C:","VolumeType":"OperatingSystem","ProtectionStatus":"Off"}]`, "{broken", "Access denied 0x80070005"} {
		t.Run(raw, func(t *testing.T) {
			dir := t.TempDir()
			payload := filepath.Join(dir, "payload.json")
			if err := os.WriteFile(payload, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			installPowerShellShim(t, payload)
			shimDir := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0]
			counter := filepath.Join(dir, "calls.txt")
			script := "@echo off\r\necho call>>\"" + counter + "\"\r\ntype \"" + payload + "\"\r\n"
			if err := os.WriteFile(filepath.Join(shimDir, "powershell.cmd"), []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			observations, err := (BitLockerCollector{}).Collect(context.Background())
			if err != nil || len(observations) != 1 {
				t.Fatalf("collection: %v", err)
			}
			calls, err := os.ReadFile(counter)
			if err != nil || len(strings.Fields(string(calls))) != 1 {
				t.Fatalf("query was not executed exactly once: %q %v", calls, err)
			}
		})
	}
}

func TestBitLockerRawEvidenceAndQueryAllowlist(t *testing.T) {
	obs := bitlockerObservation(commandResult{Output: `{"MountPoint":"C:","VolumeType":"OperatingSystem","ProtectionStatus":"On","KeyProtector":"sensitive-fixture"}`}, nil)
	if _, ok := intValue(obs, "volumes_protected"); !ok {
		t.Fatal("aggregate contract removed")
	}
	if raw, ok := stringValue(obs, "volume_evidence"); !ok || !strings.Contains(raw, `"ProtectionStatus":"On"`) || strings.Contains(raw, "sensitive-fixture") {
		t.Fatal("safe raw property projection changed")
	}
	for _, forbidden := range []string{"KeyProtector", "RecoveryPassword", "RecoveryKey", "Enable-BitLocker", "Disable-BitLocker", "Suspend-BitLocker", "Resume-BitLocker"} {
		if strings.Contains(bitlockerScript, forbidden) {
			t.Fatalf("unsafe query field/action: %s", forbidden)
		}
	}
}
