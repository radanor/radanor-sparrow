//go:build linux

package collectors

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// These fixtures are real `lsblk -J -o NAME,TYPE,FSTYPE,MOUNTPOINT` shapes
// pushed through the collector's own classification and then the real check,
// so the verdict is decided by the block relationships rather than by an
// observation a test assembled by hand.

func diskObservationFromJSON(t *testing.T, raw string) assessment.Observation {
	t.Helper()
	var parsed lsblkOutput
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("fixture layout: %v", err)
	}
	obs := assessment.Observation{
		ID: "disk_encryption", Collector: "disk_encryption", Platform: "ubuntu",
		Status: assessment.Collected, Source: "fixture", CollectedAt: time.Now(),
		Values: diskEncryptionValues(parsed),
	}
	return obs
}

func diskVerdict(t *testing.T, raw string) assessment.CheckResult {
	t.Helper()
	return checkByID(linuxChecks, "disk_encryption_present").Evaluate(diskObservationFromJSON(t, raw))
}

func diskStringValue(t *testing.T, obs assessment.Observation, key string) string {
	t.Helper()
	value, ok := stringValue(obs, key)
	if !ok {
		t.Fatalf("evidence key %q missing", key)
	}
	return value
}

// An encrypted Ubuntu install: the root filesystem is an LVM mapping on a LUKS
// partition. The encryption marker is on the parent, so ancestry has to be
// followed for the check to pass.
func TestDiskEncryptionEncryptedRootLayout(t *testing.T) {
	layout := `{"blockdevices":[{"name":"sda","type":"disk","fstype":null,"mountpoint":null,"children":[
		{"name":"sda1","type":"part","fstype":"ext4","mountpoint":"/boot"},
		{"name":"sda2","type":"part","fstype":"crypto_LUKS","mountpoint":null,"children":[
			{"name":"ubuntu--vg-ubuntu--lv","type":"lvm","fstype":"ext4","mountpoint":"/"}
		]}]}]}`
	obs := diskObservationFromJSON(t, layout)
	if got := diskStringValue(t, obs, "root_state"); got != "encrypted" {
		t.Fatalf("root_state = %q, want encrypted", got)
	}
	if got := diskStringValue(t, obs, "root_device"); got != "ubuntu--vg-ubuntu--lv" {
		t.Fatalf("root_device = %q, want ubuntu--vg-ubuntu--lv", got)
	}
	if got := diskVerdict(t, layout); got.Result != assessment.Pass {
		t.Fatalf("result = %q (%s), want pass", got.Result, got.Detail)
	}
}

// A crypt mapping mounted directly at the root, the other common shape.
func TestDiskEncryptionCryptDeviceMountedAtRoot(t *testing.T) {
	layout := `{"blockdevices":[{"name":"nvme0n1","type":"disk","fstype":null,"mountpoint":null,"children":[
		{"name":"nvme0n1p2","type":"part","fstype":"crypto_LUKS","mountpoint":null,"children":[
			{"name":"cryptroot","type":"crypt","fstype":"ext4","mountpoint":"/"}
		]}]}]}`
	if got := diskVerdict(t, layout); got.Result != assessment.Pass {
		t.Fatalf("result = %q (%s), want pass", got.Result, got.Detail)
	}
}

// The M4 defect: an unencrypted system root next to an encrypted data volume
// must fail, because the encrypted volume does not protect the system.
func TestDiskEncryptionUnencryptedRootWithEncryptedDataVolume(t *testing.T) {
	layout := `{"blockdevices":[{"name":"sda","type":"disk","fstype":null,"mountpoint":null,"children":[
		{"name":"sda1","type":"part","fstype":"ext4","mountpoint":"/"},
		{"name":"sda2","type":"part","fstype":"ext4","mountpoint":"/home"}
	]},{"name":"sdb","type":"disk","fstype":null,"mountpoint":null,"children":[
		{"name":"sdb1","type":"part","fstype":"crypto_LUKS","mountpoint":null,"children":[
			{"name":"sdb1-crypt","type":"crypt","fstype":"ext4","mountpoint":"/mnt/data"}
		]}]}]}`
	obs := diskObservationFromJSON(t, layout)
	if got := diskStringValue(t, obs, "root_state"); got != "plain" {
		t.Fatalf("root_state = %q, want plain", got)
	}
	got := diskVerdict(t, layout)
	if got.Result != assessment.Fail {
		t.Fatalf("result = %q (%s), want FAIL: an unrelated encrypted volume must not excuse a plain system root", got.Result, got.Detail)
	}
}

// Only an encrypted volume and no visible system root: no verdict either way.
func TestDiskEncryptionEncryptedVolumeWithoutARoot(t *testing.T) {
	layout := `{"blockdevices":[{"name":"sdb","type":"disk","fstype":null,"mountpoint":null,"children":[
		{"name":"sdb1","type":"part","fstype":"crypto_LUKS","mountpoint":null,"children":[
			{"name":"sdb1-crypt","type":"crypt","fstype":"ext4","mountpoint":"/mnt/data"}
		]}]}]}`
	obs := diskObservationFromJSON(t, layout)
	if got := diskStringValue(t, obs, "root_state"); got != "not_mounted" {
		t.Fatalf("root_state = %q, want not_mounted", got)
	}
	if got := diskVerdict(t, layout); got.Result != assessment.Unknown {
		t.Fatalf("result = %q (%s), want unknown", got.Result, got.Detail)
	}
}

// A root device whose ancestry does not reach a physical disk is not evidence
// of anything, so it is reported as undetermined rather than as plain.
func TestDiskEncryptionAmbiguousAncestryIsUndetermined(t *testing.T) {
	layout := `{"blockdevices":[{"name":"vg-root","type":"lvm","fstype":"ext4","mountpoint":"/"}]}`
	obs := diskObservationFromJSON(t, layout)
	if got := diskStringValue(t, obs, "root_state"); got != "undetermined" {
		t.Fatalf("root_state = %q, want undetermined", got)
	}
	if reason := diskStringValue(t, obs, "root_state_reason"); reason == "" {
		t.Fatal("an undetermined root must explain why")
	}
	if got := diskVerdict(t, layout); got.Result != assessment.Unknown {
		t.Fatalf("result = %q (%s), want unknown", got.Result, got.Detail)
	}
}

// An empty layout, as an unprivileged or unusual host can produce, draws no
// conclusion.
func TestDiskEncryptionEmptyLayoutIsUnknown(t *testing.T) {
	for _, layout := range []string{`{"blockdevices":[]}`, `{}`} {
		if got := diskVerdict(t, layout); got.Result != assessment.Unknown {
			t.Fatalf("layout %s = %q, want unknown", layout, got.Result)
		}
	}
}

// Loopback and snap images are not system volumes and must not be scored.
func TestDiskEncryptionLoopDevicesAreIgnored(t *testing.T) {
	layout := `{"blockdevices":[{"name":"loop0","type":"loop","fstype":"squashfs","mountpoint":"/snap/core/1"},{"name":"sda","type":"disk","fstype":null,"mountpoint":null,"children":[{"name":"sda1","type":"part","fstype":"ext4","mountpoint":"/"}]}]}`
	obs := diskObservationFromJSON(t, layout)
	if plain, _ := intValue(obs, "mounted_plain_volumes"); plain != 1 {
		t.Fatalf("mounted_plain_volumes = %d, want 1 (the loop image must not count)", plain)
	}
	if got := diskVerdict(t, layout); got.Result != assessment.Fail {
		t.Fatalf("result = %q (%s), want fail for the plain root", got.Result, got.Detail)
	}
}

// A whole disk with no crypto marker and nothing mounted says nothing, so it
// stays undetermined rather than being counted as plain.
func TestDiskEncryptionBareDiskIsUndetermined(t *testing.T) {
	layout := `{"blockdevices":[{"name":"sdb","type":"disk","fstype":null,"mountpoint":null}]}`
	obs := diskObservationFromJSON(t, layout)
	if undetermined, _ := intValue(obs, "undetermined_volumes"); undetermined != 1 {
		t.Fatalf("undetermined_volumes = %d, want 1", undetermined)
	}
	if got := diskVerdict(t, layout); got.Result != assessment.Unknown {
		t.Fatalf("result = %q, want unknown", got.Result)
	}
}

// The evidence stays a set of facts: no verdict-shaped key, and the root facts
// are present so the dashboard can show what was decided on.
func TestDiskEncryptionEvidenceIsFactual(t *testing.T) {
	layout := `{"blockdevices":[{"name":"sda","type":"disk","fstype":null,"mountpoint":null,"children":[{"name":"sda1","type":"part","fstype":"ext4","mountpoint":"/"}]}]}`
	obs := diskObservationFromJSON(t, layout)
	for _, v := range obs.Values {
		switch v.Key {
		case "pass", "fail", "result", "verdict":
			t.Errorf("collector emitted a verdict-shaped evidence key: %q", v.Key)
		}
	}
	if _, ok := stringValue(obs, "root_state"); !ok {
		t.Error("the evidence does not record the root state it was decided on")
	}
}
