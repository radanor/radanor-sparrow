//go:build linux

package collectors

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// securityRelevantServices are units whose state materially affects local
// security posture. The list is a reviewed allowlist: collectors never enumerate
// or execute arbitrary units supplied by a caller.
var securityRelevantServices = []string{
	"ssh.service",
	"ufw.service",
	"firewalld.service",
	"apparmor.service",
	"unattended-upgrades.service",
}

// ServicesCollector reports the lifecycle state of allowlisted security-relevant
// systemd units.
//
// Source: `systemctl show <unit>`. This is the documented, machine-readable
// interface and works without elevated privileges. A missing unit is reported
// via LoadState=not-found, which is distinct from an inactive unit; the
// collector never treats "absent" as "disabled".
type ServicesCollector struct{}

func (ServicesCollector) ID() string { return "services" }

func (ServicesCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "services", Collector: "services", Platform: ubuntuPlatformID,
		Status: assessment.Collected, Source: "systemctl show",
	}
	queried, loaded := 0, 0
	for _, unit := range securityRelevantServices {
		props, err := systemctlShow(ctx, unit)
		if err != nil {
			if isMissingTool(err) {
				// systemctl itself is absent; the whole collection is unsupported.
				obs.Status = assessment.Unsupported
				obs.Error = "systemctl is not available on this system"
				return []assessment.Observation{obs}, nil
			}
			// A failure on one unit does not invalidate the others; record it
			// and continue, marking the observation incomplete.
			obs.Status = assessment.Incomplete
			obs.Error = boundText("failed to query " + unit + ": " + err.Error())
			continue
		}
		queried++
		if props["LoadState"] == "not-found" {
			// The unit does not exist on this system. That is a fact, not a
			// security verdict, and not an error.
			obs.Values = append(obs.Values, presentValue("unit."+unit+".present", "absent"))
			continue
		}
		loaded++
		obs.Values = append(obs.Values,
			presentValue("unit."+unit+".active", props["ActiveState"]),
			presentValue("unit."+unit+".sub", props["SubState"]),
			presentValue("unit."+unit+".unit_file", props["UnitFileState"]),
		)
	}
	obs.Values = append(obs.Values,
		presentInt("units_queried", int64(queried)),
		presentInt("units_present", int64(loaded)),
	)
	return []assessment.Observation{obs}, nil
}

// systemctlShow parses `systemctl show` machine-readable property output into a
// map. An empty result with exit code zero still yields a map, so callers must
// check LoadState to distinguish a missing unit.
func systemctlShow(ctx context.Context, unit string) (map[string]string, error) {
	// unit comes only from the reviewed allowlist above, never from caller input.
	res, err := runCommand(ctx, "systemctl", "show", unit,
		"--property=Id,LoadState,ActiveState,SubState,UnitFileState")
	if err != nil {
		return nil, err
	}
	// A clipped property list could omit LoadState and make a present unit look
	// absent, so a clipped reading is refused rather than answered.
	if res.Truncated {
		return nil, errors.New(truncatedNote("the "+unit+" property listing", res.Cap))
	}
	props := map[string]string{}
	for _, line := range strings.Split(res.Output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			props[key] = strings.TrimSpace(value)
		}
	}
	if len(props) == 0 {
		return nil, os.ErrNotExist
	}
	return props, nil
}
