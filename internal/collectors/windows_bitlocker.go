//go:build windows

package collectors

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// BitLockerCollector reads protection facts only; no recovery-key fields or
// methods that change encryption/protection state are queried.
type BitLockerCollector struct{}

func (BitLockerCollector) ID() string { return "bitlocker" }

const bitlockerScript = `$ErrorActionPreference='Stop'; $v = @(Get-BitLockerVolume -ErrorAction Stop | ForEach-Object { ` +
	`[pscustomobject]@{ MountPoint=[string]$_.MountPoint; VolumeType=[string]$_.VolumeType; ` +
	`ProtectionStatus=[string]$_.ProtectionStatus; EncryptionPercentage=[double]$_.EncryptionPercentage; ` +
	`LockStatus=[string]$_.LockStatus } }); ConvertTo-Json -InputObject $v -Compress`

type bitlockerVolume struct {
	MountPoint        string   `json:"MountPoint"`
	VolumeType        string   `json:"VolumeType"`
	ProtectionStatus  string   `json:"ProtectionStatus"`
	EncryptionPercent *float64 `json:"EncryptionPercentage,omitempty"`
	LockStatus        *string  `json:"LockStatus,omitempty"`
}

func (BitLockerCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	res, err := runCommand(ctx, "powershell", powershellArgs(bitlockerScript)...)
	return []assessment.Observation{bitlockerObservation(res, err)}, nil
}

func bitlockerObservation(res commandResult, commandErr error) assessment.Observation {
	obs := assessment.Observation{ID: "bitlocker", Collector: "bitlocker", Platform: windowsPlatformID,
		Status: assessment.Incomplete, Source: "Get-BitLockerVolume (CIM)"}
	if accessDeniedOutput(res.Output) {
		obs.Error = "BitLocker evidence could not be read: access denied"
		return obs
	}
	if commandErr != nil || res.Truncated {
		obs.Error = "BitLocker evidence query failed or returned truncated output"
		return obs
	}
	raw := strings.TrimSpace(res.Output)
	var volumes []bitlockerVolume
	var err error
	if strings.HasPrefix(raw, "{") {
		var single bitlockerVolume
		err = json.Unmarshal([]byte(raw), &single)
		volumes = []bitlockerVolume{single}
	} else {
		err = json.Unmarshal([]byte(raw), &volumes)
	}
	if err != nil {
		obs.Error = "BitLocker volume JSON could not be decoded"
		return obs
	}
	if len(volumes) == 0 {
		obs.Error = "no BitLocker volume information was returned"
		return obs
	}
	protected, unprotected, unknown := 0, 0, 0
	var protectedNames []string
	var osVolumes []bitlockerVolume
	for _, volume := range volumes {
		switch strings.ToLower(strings.TrimSpace(volume.ProtectionStatus)) {
		case "on":
			protected++
			protectedNames = append(protectedNames, volume.MountPoint)
		case "off":
			unprotected++
		default:
			unknown++
		}
		if strings.EqualFold(strings.TrimSpace(volume.VolumeType), "OperatingSystem") {
			osVolumes = append(osVolumes, volume)
		}
	}
	// Retain the existing aggregate keys for consumers; they no longer decide
	// whether the operating-system volume is protected.
	obs.Values = []assessment.Value{presentInt("volumes_protected", int64(protected)),
		presentInt("volumes_unprotected", int64(unprotected)), presentInt("volumes_unknown", int64(unknown)),
		presentValue("protected_volumes", strings.Join(protectedNames, ","))}
	// Bounded allowlisted raw property projection, never arbitrary query fields.
	if evidence, err := json.Marshal(volumes); err == nil {
		obs.Values = append(obs.Values, presentValue("volume_evidence", string(evidence)))
		obs.Values = append(obs.Values, presentBool("volume_evidence_truncated", len(evidence) > assessment.MaxTextValue))
	}
	for _, volume := range volumes {
		kind := strings.ToLower(strings.TrimSpace(volume.VolumeType))
		if kind != "operatingsystem" && kind != "fixeddata" && kind != "removabledata" && kind != "data" {
			obs.Error = "volume-type evidence is missing or unreadable; OS-volume identity cannot be confirmed"
			return obs
		}
	}
	if len(osVolumes) != 1 {
		obs.Error = "BitLocker evidence does not identify exactly one operating-system volume"
		return obs
	}
	osVolume := osVolumes[0]
	mount := strings.ToUpper(strings.TrimRight(strings.TrimSpace(osVolume.MountPoint), `\/`))
	if len(mount) != 2 || mount[0] < 'A' || mount[0] > 'Z' || mount[1] != ':' {
		obs.Error = "the operating-system volume mount point is missing or invalid"
		return obs
	}
	for _, volume := range volumes {
		if strings.EqualFold(strings.TrimRight(strings.TrimSpace(volume.MountPoint), `\/`), mount) && !strings.EqualFold(strings.TrimSpace(volume.VolumeType), "OperatingSystem") {
			obs.Error = "conflicting volume-type evidence for the operating-system mount point"
			return obs
		}
	}
	obs.Values = append(obs.Values, presentValue("os_volume_mountpoint", osVolume.MountPoint),
		presentValue("os_volume_protection_status", osVolume.ProtectionStatus))
	state := strings.ToLower(strings.TrimSpace(osVolume.ProtectionStatus))
	if state != "on" && state != "off" {
		obs.Error = "operating-system volume protection status is missing or unreadable"
		return obs
	}
	obs.Status = assessment.Collected
	return obs
}
