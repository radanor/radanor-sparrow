//go:build linux

package collectors

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// DiskEncryptionCollector reports whether the *system* volume is protected by
// managed encryption.
//
// Source: `lsblk -J -o NAME,TYPE,FSTYPE,MOUNTPOINT`. LUKS devices report
// fstype "crypto_LUKS" and a decrypted mapping reports type "crypt". The
// verdict is scoped to the filesystem mounted at "/": an unrelated encrypted
// data volume says nothing about the system volume, and reporting one as
// evidence of system encryption was the defect this collector was corrected
// for.
//
// Ancestry matters, so it is followed: on a typical encrypted Ubuntu install
// the root filesystem is a `crypt` or LVM mapping sitting on a LUKS partition,
// and the encryption marker is on the parent rather than on the mounted node
// itself. When that chain cannot be followed to a physical disk, the state is
// reported as undetermined rather than assumed.
type DiskEncryptionCollector struct{}

func (DiskEncryptionCollector) ID() string { return "disk_encryption" }

// rootState is the encryption state of the filesystem mounted at "/".
type rootState string

const (
	// rootNotMounted means no volume in the layout is the system root.
	rootNotMounted rootState = "not_mounted"
	// rootEncrypted means the root volume sits on or is a LUKS container.
	rootEncrypted rootState = "encrypted"
	// rootPlain means the layout was fully determined and shows no encryption
	// for the root volume.
	rootPlain rootState = "plain"
	// rootUndetermined means the ancestry needed to decide was not visible.
	rootUndetermined rootState = "undetermined"
)

type lsblkDevice struct {
	Name       string        `json:"name"`
	Type       string        `json:"type"`
	FSType     string        `json:"fstype"`
	MountPoint string        `json:"mountpoint"`
	Children   []lsblkDevice `json:"children"`
}

type lsblkOutput struct {
	Devices []lsblkDevice `json:"blockdevices"`
}

func (DiskEncryptionCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "disk_encryption", Collector: "disk_encryption", Platform: ubuntuPlatformID,
		Status: assessment.Collected, Source: "lsblk -J",
	}
	lsblkRes, err := runCommand(ctx, "lsblk", "-J", "-o", "NAME,TYPE,FSTYPE,MOUNTPOINT")
	out := lsblkRes.Output
	if err != nil {
		if isMissingTool(err) {
			obs.Status = assessment.Unsupported
			obs.Error = "lsblk is not available on this system"
			return []assessment.Observation{obs}, nil
		}
		if isPermissionError(out, err) {
			obs.Status = assessment.PermissionDenied
			obs.Error = "reading block-device layout requires elevated privileges"
			return []assessment.Observation{obs}, nil
		}
		obs.Status = assessment.Failed
		obs.Error = boundText(err.Error())
		return []assessment.Observation{obs}, nil
	}
	var parsed lsblkOutput
	if jsonErr := json.Unmarshal([]byte(out), &parsed); jsonErr != nil {
		obs.Status = assessment.Incomplete
		obs.Error = "block-device layout could not be parsed"
		if lsblkRes.Truncated {
			// Say which of the two reasons applies: a clipped document does not
			// parse because it is incomplete, not because the tool is broken.
			obs.Error = boundText(truncatedNote("the block-device listing", lsblkRes.Cap))
		}
		return []assessment.Observation{obs}, nil
	}
	if lsblkRes.Truncated {
		// A layout that happens to decode from a clipped document is still a
		// partial view of the block devices.
		obs.Status = assessment.Incomplete
		obs.Error = boundText(truncatedNote("the block-device listing", lsblkRes.Cap))
		return []assessment.Observation{obs}, nil
	}

	obs.Values = diskEncryptionValues(parsed)
	return []assessment.Observation{obs}, nil
}

// diskEncryptionValues turns a parsed block layout into the collector's
// evidence. It is separated from Collect so fixture layouts drive the real
// classification, rather than the behaviour being asserted on hand-built
// observations.
func diskEncryptionValues(parsed lsblkOutput) []assessment.Value {
	acc := &layoutAccumulator{root: rootFinding{
		state:  rootNotMounted,
		reason: "no volume in the block layout is mounted at the system root",
	}}
	for _, dev := range parsed.Devices {
		walkDevice(dev, false, false, acc)
	}
	encrypted, plaintextMounted, undetermined := acc.encrypted, acc.plain, acc.unknown
	encryptedNames, rootDevice, state, reason := acc.names, acc.root.device, acc.root.state, acc.root.reason

	values := []assessment.Value{
		presentInt("encrypted_containers", int64(encrypted)),
		presentInt("mounted_plain_volumes", int64(plaintextMounted)),
		presentInt("undetermined_volumes", int64(undetermined)),
		presentValue("encrypted_devices", strings.Join(encryptedNames, ",")),
		// The root-scoped facts decide the check; the counts above stay as
		// context for the dashboard.
		presentValue("root_state", string(state)),
	}
	if rootDevice != "" {
		values = append(values, presentValue("root_device", rootDevice))
	}
	if reason != "" {
		values = append(values, presentValue("root_state_reason", reason))
	}
	return values
}

// layoutAccumulator carries the block-layout walk's results.
type layoutAccumulator struct {
	encrypted, plain, unknown int
	names                     []string
	root                      rootFinding
}

type rootFinding struct {
	device string
	state  rootState
	reason string
}

// walkDevice visits a device and its children, carrying whether an encryption
// container was seen above it and whether the chain has been traced back to a
// physical disk. Both are needed to say anything honest about the root volume:
// the marker normally lives on the parent partition, and a chain that does not
// reach a disk is not evidence of anything.
func walkDevice(d lsblkDevice, inheritedEncrypted, sawDisk bool, acc *layoutAccumulator) {
	isEncrypted := inheritedEncrypted || d.Type == "crypt" || strings.EqualFold(d.FSType, "crypto_LUKS")
	switch d.Type {
	case "crypt":
		// A mapped crypt device is decrypted at rest by the kernel.
		acc.encrypted++
		acc.names = append(acc.names, d.Name)
	case "part", "lvm":
		if strings.EqualFold(d.FSType, "crypto_LUKS") {
			acc.encrypted++
			acc.names = append(acc.names, d.Name)
		} else if d.MountPoint != "" {
			// A mounted non-LUKS volume is plain. Snap/loopback images are not
			// system volumes and are excluded from the conclusion.
			if !strings.HasPrefix(d.Name, "loop") && d.Type != "loop" {
				acc.plain++
			}
		} else {
			acc.unknown++
		}
	case "rom", "disk":
		// A whole disk with no crypto marker and no children tells us nothing
		// definitive; treat as undetermined only if it is the system disk.
		if d.FSType == "" && len(d.Children) == 0 && d.Type == "disk" {
			acc.unknown++
		}
	}
	if d.Type == "disk" {
		sawDisk = true
	}
	if d.MountPoint == "/" {
		rf := rootFinding{device: d.Name}
		switch {
		case isEncrypted:
			rf.state = rootEncrypted
		case sawDisk:
			// The chain reaches a physical disk and contains no encryption
			// marker, so the system volume really is mounted in the clear.
			rf.state = rootPlain
		default:
			rf.state = rootUndetermined
			rf.reason = "the parent chain of the root device was not visible in the block layout"
		}
		acc.root = rf
	}
	for _, child := range d.Children {
		walkDevice(child, isEncrypted, sawDisk, acc)
	}
}
