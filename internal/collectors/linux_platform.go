//go:build linux

package collectors

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// ubuntuPlatformID identifies the platform string stamped on every Ubuntu
// observation.
const ubuntuPlatformID = "ubuntu"

// osRelease is the parsed subset of /etc/os-release we rely on. This file is
// the documented, authoritative source for distribution identity.
type osRelease struct {
	ID        string
	VersionID string
	Pretty    string
	Codename  string
}

func readOSRelease() (osRelease, error) {
	text, err := readFileText("/etc/os-release")
	if err != nil {
		return osRelease{}, err
	}
	var r osRelease
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"'`)
		switch key {
		case "ID":
			r.ID = value
		case "VERSION_ID":
			r.VersionID = value
		case "PRETTY_NAME":
			r.Pretty = value
		case "VERSION_CODENAME":
			r.Codename = value
		}
	}
	if r.ID == "" {
		return osRelease{}, os.ErrNotExist
	}
	return r, nil
}

// PlatformCollector reports the operating-system identity and architecture.
// Source: /etc/os-release and the Go runtime. No privilege required.
type PlatformCollector struct{}

func (PlatformCollector) ID() string { return "platform" }

func (PlatformCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	release, err := readOSRelease()
	if err != nil {
		return nil, err
	}
	values := []assessment.Value{
		presentValue("os_id", release.ID),
		presentValue("version_id", release.VersionID),
		presentValue("pretty_name", release.Pretty),
		presentValue("codename", release.Codename),
		presentValue("arch", runtime.GOARCH),
	}
	// The kernel release is a genuine reading, reported only when the kernel
	// actually exposes it. A constant "kernel_present" value used to assert a
	// fact the collector had never observed; an unreadable source now simply
	// means the value is absent rather than fabricated.
	if text, kerr := readFileText("/proc/sys/kernel/osrelease"); kerr == nil {
		if kernel := strings.TrimSpace(text); kernel != "" {
			values = append(values, presentValue("kernel_release", kernel))
		}
	}
	return []assessment.Observation{{
		ID:        "platform",
		Collector: "platform",
		Platform:  ubuntuPlatformID,
		Status:    assessment.Collected,
		Source:    "/etc/os-release + /proc/sys/kernel/osrelease",
		Values:    values,
	}}, nil
}

// SupportCollector reports the Ubuntu release's mainstream support status.
//
// Source: a version table compiled from Canonical's published release
// lifecycle. This is deliberately a small, reviewed table rather than a network
// call, because the assessment must work offline and must never trust an
// unauthenticated remote feed for a security verdict. The table is bounded to
// the Ubuntu LTS releases this product targets; an unrecognized release yields
// an Incomplete observation rather than a guess.
type supportWindow struct {
	versionID      string
	major, minor   int
	supportEndsISO string
	lts            bool
}

var ubuntuSupportTable = []supportWindow{
	{versionID: "22.04", major: 22, minor: 4, supportEndsISO: "2027-04-21", lts: true},
	{versionID: "24.04", major: 24, minor: 4, supportEndsISO: "2029-04-25", lts: true},
}

type SupportCollector struct{}

func (SupportCollector) ID() string { return "release_support" }

func (SupportCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	release, err := readOSRelease()
	if err != nil {
		return nil, err
	}
	if release.ID != "ubuntu" {
		return []assessment.Observation{{
			ID: "release_support", Collector: "release_support", Platform: ubuntuPlatformID,
			Status: assessment.Unsupported, Source: "/etc/os-release",
			Values: []assessment.Value{presentValue("os_id", release.ID)},
			Error:  "release support status is only defined for Ubuntu",
		}}, nil
	}
	obs := assessment.Observation{
		ID: "release_support", Collector: "release_support", Platform: ubuntuPlatformID,
		Status: assessment.Collected, Source: "canonical release lifecycle table",
		Values: []assessment.Value{presentValue("version_id", release.VersionID)},
	}
	var match *supportWindow
	for i := range ubuntuSupportTable {
		if ubuntuSupportTable[i].versionID == release.VersionID {
			match = &ubuntuSupportTable[i]
			break
		}
	}
	if match == nil {
		// An unrecognized release is not a failure and not a pass: we cannot
		// assert its support status, so report it as incomplete.
		obs.Status = assessment.Incomplete
		obs.Error = "release version is not present in the reviewed support table"
		return []assessment.Observation{obs}, nil
	}
	obs.Values = append(obs.Values,
		presentBool("is_lts", match.lts),
		presentValue("support_ends", match.supportEndsISO),
		presentValue("codename", release.Codename),
	)
	return []assessment.Observation{obs}, nil
}

// AppArmorCollector reports the MAC confinement framework state.
//
// Source: /sys/module/apparmor/parameters/enabled (kernel module flag) and
// /sys/kernel/security/apparmor/profiles (loaded profiles). Both are
// world-readable when AppArmor is active; a permission failure is reported
// honestly rather than treated as "disabled".
type AppArmorCollector struct{}

func (AppArmorCollector) ID() string { return "apparmor" }

func (AppArmorCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "apparmor", Collector: "apparmor", Platform: ubuntuPlatformID,
		Status: assessment.Collected, Source: "/sys/module/apparmor/parameters/enabled",
	}
	enabledRaw, err := readFileText("/sys/module/apparmor/parameters/enabled")
	if err != nil {
		if os.IsPermission(err) {
			obs.Status = assessment.PermissionDenied
			obs.Error = "apparmor module parameters are not readable by this user"
			return []assessment.Observation{obs}, nil
		}
		// Absence of the module means the framework is not present on this
		// kernel; that is a fact, not a permission problem.
		obs.Status = assessment.Unsupported
		obs.Error = "apparmor is not present on this kernel"
		return []assessment.Observation{obs}, nil
	}
	enabled := strings.TrimSpace(enabledRaw) == "Y"
	obs.Values = append(obs.Values, presentBool("enabled", enabled))

	// Profile count is supplementary context; unreadable profile lists do not
	// invalidate the enabled flag, so the observation stays Collected.
	profiles, perr := readFileText("/sys/kernel/security/apparmor/profiles")
	switch {
	case perr == nil:
		count := 0
		for _, line := range strings.Split(profiles, "\n") {
			if strings.TrimSpace(line) != "" {
				count++
			}
		}
		obs.Values = append(obs.Values, presentInt("profiles_loaded", int64(count)))
		obs.Source = "/sys/kernel/security/apparmor/profiles"
	case os.IsPermission(perr):
		obs.Error = "apparmor profile list requires elevated privileges; profile count unknown"
	default:
		obs.Error = "apparmor profile list is unavailable on this system"
	}
	return []assessment.Observation{obs}, nil
}

// parsePositiveInt is a shared helper for reading bounded numeric fields.
func parsePositiveInt(s string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
