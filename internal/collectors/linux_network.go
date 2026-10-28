//go:build linux

package collectors

import (
	"context"
	"strings"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// NetworkCollector reports listening TCP and UDP endpoints.
//
// Source: `ss -H -lntu`, the standard, unprivileged-readable socket statistics
// interface. Only aggregate counts and a bounded sample of addresses are kept;
// full socket tables are never stored.
type NetworkCollector struct{}

func (NetworkCollector) ID() string { return "network_endpoints" }

func (NetworkCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "network_endpoints", Collector: "network_endpoints", Platform: ubuntuPlatformID,
		Status: assessment.Collected, Source: "ss -lntu",
	}
	tcp, udp := 0, 0
	loopback, wildcard := 0, 0
	var samples []string
	res, err := runCommand(ctx, "ss", "-H", "-lntu")
	out := res.Output
	if err != nil {
		if isMissingTool(err) {
			obs.Status = assessment.Unsupported
			obs.Error = "ss is not available on this system"
			return []assessment.Observation{obs}, nil
		}
		if isPermissionError(out, err) {
			obs.Status = assessment.PermissionDenied
			obs.Error = "reading socket statistics requires elevated privileges"
			return []assessment.Observation{obs}, nil
		}
		obs.Status = assessment.Failed
		obs.Error = boundText(err.Error())
		return []assessment.Observation{obs}, nil
	}
	for _, line := range splitLines(out, res.Truncated) {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		switch {
		case strings.HasPrefix(fields[0], "tcp"):
			tcp++
		case strings.HasPrefix(fields[0], "udp"):
			udp++
		default:
			continue
		}
		// The local address is field 4 in `ss -H -lntu` output.
		addr := fields[4]
		switch {
		case strings.HasPrefix(addr, "127.") || strings.HasPrefix(addr, "[::1]") || strings.Contains(addr, "127.0.0.1"):
			loopback++
		case strings.Contains(addr, "*") || strings.HasPrefix(addr, "0.0.0.0") || strings.HasPrefix(addr, "[::]"):
			wildcard++
		}
		if len(samples) < maxEndpointSamples {
			samples = append(samples, addr)
		}
	}
	obs.Values = append(obs.Values,
		presentInt("tcp_listeners", int64(tcp)),
		presentInt("udp_listeners", int64(udp)),
		presentInt("loopback_bound", int64(loopback)),
		presentInt("wildcard_bound", int64(wildcard)),
		presentValue("sample_addresses", strings.Join(samples, ",")),
	)
	if res.Truncated {
		// The counts were taken from a clipped socket listing, so they are lower
		// bounds and this is not a complete reading of the listening set.
		obs.Status = assessment.Incomplete
		obs.Error = boundText(truncatedNote("the socket listing", res.Cap))
		obs.Values = append(obs.Values, presentBool("listener_counts_are_lower_bounds", true))
	}
	return []assessment.Observation{obs}, nil
}
