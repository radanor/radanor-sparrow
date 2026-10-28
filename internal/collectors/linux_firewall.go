//go:build linux

package collectors

import (
	"context"
	"os"
	"strings"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// FirewalldCollector reports the firewalld state via its D-Bus-free CLI.
//
// Source: `firewall-cmd --state` and `firewall-cmd --get-active-zones`. The
// tool refuses without the required privilege, which is reported as
// PermissionDenied rather than as "firewall off".
type FirewalldCollector struct{}

func (FirewalldCollector) ID() string { return "firewalld" }

func (FirewalldCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "firewalld", Collector: "firewalld", Platform: ubuntuPlatformID,
		Status: assessment.Collected, Source: "firewall-cmd",
	}
	stateRes, err := runCommand(ctx, "firewall-cmd", "--state")
	out := stateRes.Output
	if err != nil {
		if strings.Contains(strings.ToLower(out+err.Error()), "not running") ||
			strings.Contains(strings.ToLower(out+err.Error()), "not initialized") {
			// firewalld is installed but inactive: a fact, not a permission problem.
			obs.Status = assessment.Unsupported
			obs.Error = "firewalld is not running on this system"
			return []assessment.Observation{obs}, nil
		}
		if isMissingTool(err) {
			obs.Status = assessment.Unsupported
			obs.Error = "firewall-cmd is not installed"
			return []assessment.Observation{obs}, nil
		}
		if isPermissionError(out, err) {
			obs.Status = assessment.PermissionDenied
			obs.Error = "querying firewalld requires elevated privileges"
			return []assessment.Observation{obs}, nil
		}
		obs.Status = assessment.Failed
		obs.Error = boundText(err.Error())
		return []assessment.Observation{obs}, nil
	}
	running := strings.TrimSpace(out) == "running"
	obs.Values = append(obs.Values, presentBool("running", running))
	if zones, zerr := runCommand(ctx, "firewall-cmd", "--get-active-zones"); zerr == nil {
		obs.Values = append(obs.Values, presentValue("active_zones", zones.Output))
		if zones.Truncated {
			obs.Status = assessment.Incomplete
			obs.Error = boundText(truncatedNote("the firewalld active-zone listing", zones.Cap))
		}
	}
	if stateRes.Truncated {
		// A clipped state reading could turn "running" into anything else, so the
		// value is not presented as a complete reading.
		obs.Status = assessment.Incomplete
		obs.Error = boundText(truncatedNote("the firewalld state query", stateRes.Cap))
	}
	return []assessment.Observation{obs}, nil
}

// UfwCollector reports the Uncomplicated Firewall state.
//
// Source: `ufw status`. The tool requires root, so an unprivileged run reports
// PermissionDenied. Critically, a permission failure must never be interpreted
// as "firewall disabled"; the check surfaces it as UNKNOWN.
type UfwCollector struct{}

func (UfwCollector) ID() string { return "ufw" }

func (UfwCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "ufw", Collector: "ufw", Platform: ubuntuPlatformID,
		Status: assessment.Collected, Source: "ufw status",
	}
	ufwRes, err := runCommand(ctx, "ufw", "status")
	out := ufwRes.Output
	if err != nil {
		if isMissingTool(err) {
			obs.Status = assessment.Unsupported
			obs.Error = "ufw is not installed"
			return []assessment.Observation{obs}, nil
		}
		if isPermissionError(out, err) {
			obs.Status = assessment.PermissionDenied
			obs.Error = "querying ufw state requires elevated privileges"
			return []assessment.Observation{obs}, nil
		}
		obs.Status = assessment.Failed
		obs.Error = boundText(err.Error())
		return []assessment.Observation{obs}, nil
	}
	// `ufw status` prints "Status: active", "Status: inactive", or the English
	// localized equivalent. We only assert on the documented token.
	active := false
	unknown := true
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Status:") {
			unknown = false
			active = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(line, "Status:")), "active")
		}
	}
	if unknown {
		// The output shape was not recognized; do not guess.
		obs.Status = assessment.Incomplete
		obs.Error = "ufw status output was not recognized"
		return []assessment.Observation{obs}, nil
	}
	obs.Values = append(obs.Values, presentBool("active", active))
	if ufwRes.Truncated {
		// The status line is expected first, but only the part of the output that
		// fitted was examined, so the reading is not claimed as complete.
		obs.Status = assessment.Incomplete
		obs.Error = boundText(truncatedNote("the ufw status listing", ufwRes.Cap))
	}
	return []assessment.Observation{obs}, nil
}

// NftablesCollector reports whether the nftables ruleset is readable and
// non-empty.
//
// Source: `nft list ruleset`. Reading the ruleset requires CAP_NET_ADMIN, so an
// unprivileged run is reported as PermissionDenied.
type NftablesCollector struct{}

func (NftablesCollector) ID() string { return "nftables" }

func (NftablesCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "nftables", Collector: "nftables", Platform: ubuntuPlatformID,
		Status: assessment.Collected, Source: "nft list ruleset",
	}
	nftRes, err := runCommand(ctx, "nft", "list", "ruleset")
	out := nftRes.Output
	if err != nil {
		if isMissingTool(err) {
			obs.Status = assessment.Unsupported
			obs.Error = "nft is not installed"
			return []assessment.Observation{obs}, nil
		}
		if isPermissionError(out, err) {
			obs.Status = assessment.PermissionDenied
			obs.Error = "reading the nftables ruleset requires elevated privileges"
			return []assessment.Observation{obs}, nil
		}
		obs.Status = assessment.Failed
		obs.Error = boundText(err.Error())
		return []assessment.Observation{obs}, nil
	}
	trimmed := strings.TrimSpace(out)
	obs.Values = append(obs.Values,
		presentBool("ruleset_readable", true),
		presentInt("ruleset_bytes", int64(len(trimmed))),
		presentBool("ruleset_empty", trimmed == ""),
	)
	if nftRes.Truncated {
		// ruleset_bytes would report the capture limit rather than the ruleset's
		// size, and an empty-looking tail would be an artefact of the clip.
		obs.Status = assessment.Incomplete
		obs.Error = boundText(truncatedNote("the nftables ruleset listing", nftRes.Cap))
		obs.Values = append(obs.Values, presentBool("ruleset_size_is_lower_bound", true))
	}
	return []assessment.Observation{obs}, nil
}

// isPermissionError recognizes the refusal signatures the three firewall tools
// emit when run without the required privilege. Matching on the documented
// refusal text keeps the classification stable across distributions.
func isPermissionError(output string, err error) bool {
	if err == nil {
		return false
	}
	if os.IsPermission(err) {
		return true
	}
	combined := strings.ToLower(output + " " + err.Error())
	for _, token := range []string{
		"you need to be root",
		"must be root",
		"access denied",
		"permission denied",
		"operation not permitted",
		"requires root",
	} {
		if strings.Contains(combined, token) {
			return true
		}
	}
	return false
}
