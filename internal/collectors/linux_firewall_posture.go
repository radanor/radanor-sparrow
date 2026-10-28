//go:build linux

package collectors

import (
	"context"
	"sort"
	"strings"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// FirewallPostureCollector merges the three native firewall readings (ufw,
// firewalld, nftables) into a single factual observation.
//
// This exists for one reason: a single superficial configuration flag must
// never be presented as "the firewall is on". The aggregate records, per tool,
// whether it reported an active ruleset, reported no ruleset, or could not be
// read at all. The check that consumes it therefore reasons over the whole
// picture and reports UNKNOWN when every readable source is inconclusive,
// rather than defaulting to a reassuring verdict.
type FirewallPostureCollector struct{}

func (FirewallPostureCollector) ID() string { return "firewall_posture" }

func (FirewallPostureCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "firewall_posture", Collector: "firewall_posture", Platform: ubuntuPlatformID,
		Status: assessment.Collected, Source: "ufw status / firewall-cmd / nft list ruleset",
	}

	type sub struct {
		name string
		c    assessment.Collector
	}
	subs := []sub{
		{"ufw", UfwCollector{}},
		{"firewalld", FirewalldCollector{}},
		{"nftables", NftablesCollector{}},
	}

	var activeTools, inactiveTools, unknownTools []string
	for _, s := range subs {
		readings, err := s.c.Collect(ctx)
		if err != nil || len(readings) == 0 {
			unknownTools = append(unknownTools, s.name)
			continue
		}
		reading := readings[0]
		switch reading.Status {
		case assessment.Collected, assessment.Incomplete:
			state := firewallToolState(s.name, reading)
			switch state {
			case "active":
				activeTools = append(activeTools, s.name)
			case "inactive":
				inactiveTools = append(inactiveTools, s.name)
			default:
				unknownTools = append(unknownTools, s.name)
			}
		default:
			// permission_denied / unsupported / failed: the tool could not be
			// read, so it contributes no conclusion in either direction.
			unknownTools = append(unknownTools, s.name)
		}
	}
	sort.Strings(activeTools)
	sort.Strings(inactiveTools)
	sort.Strings(unknownTools)

	obs.Values = append(obs.Values,
		presentInt("tools_active", int64(len(activeTools))),
		presentInt("tools_inactive", int64(len(inactiveTools))),
		presentInt("tools_unknown", int64(len(unknownTools))),
		presentValue("active_tools", strings.Join(activeTools, ",")),
		presentValue("inactive_tools", strings.Join(inactiveTools, ",")),
		presentValue("unknown_tools", strings.Join(unknownTools, ",")),
	)

	// If nothing could be read at all, the observation itself is inconclusive.
	if len(activeTools) == 0 && len(inactiveTools) == 0 {
		obs.Status = assessment.Incomplete
		obs.Error = "no firewall tool could be read on this system; the firewall state is not known"
	}
	return []assessment.Observation{obs}, nil
}

// firewallToolState maps one tool's factual reading onto active / inactive /
// inconclusive without asserting more than the tool actually reported.
func firewallToolState(tool string, obs assessment.Observation) string {
	switch tool {
	case "ufw":
		if active, ok := boolValue(obs, "active"); ok {
			if active {
				return "active"
			}
			return "inactive"
		}
	case "firewalld":
		if running, ok := boolValue(obs, "running"); ok {
			if running {
				return "active"
			}
			return "inactive"
		}
	case "nftables":
		empty, ok := boolValue(obs, "ruleset_empty")
		if ok {
			if !empty {
				return "active"
			}
			return "inactive"
		}
	}
	return "unknown"
}
