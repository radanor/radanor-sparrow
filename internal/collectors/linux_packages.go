//go:build linux

package collectors

import (
	"context"
	"strings"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// maxInventorySamples bounds how many individual packages are echoed into
// evidence. The counts stay exact; only the illustrative list is truncated.
const maxInventorySamples = 15

// PackageInventoryCollector reports the installed software inventory.
//
// Source: `dpkg-query` with an explicit machine-readable format string, so no
// human-oriented or localized output is ever parsed. Only installed packages
// (status "ii") are counted; config-only and removed-but-config-remaining
// entries are excluded so the inventory reflects what is really present.
//
// This is an inventory, not a vulnerability list. No CVE matching is performed,
// because matching versions against a vulnerability database offline would
// misrepresent Ubuntu's security backports, which frequently ship fixes under
// unchanged upstream version numbers.
type PackageInventoryCollector struct{}

func (PackageInventoryCollector) ID() string { return "package_inventory" }

func (PackageInventoryCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "package_inventory", Collector: "package_inventory", Platform: ubuntuPlatformID,
		Status: assessment.Collected, Source: "dpkg-query",
	}
	// The format string is fixed by this collector; no caller input reaches it.
	res, err := runCommand(ctx, "dpkg-query", "-W", "-f=${binary:Package}\t${Version}\t${db:Status-Abbrev}\n")
	out := res.Output
	if err != nil {
		if isMissingTool(err) {
			obs.Status = assessment.Unsupported
			obs.Error = "dpkg is not available on this system"
			return []assessment.Observation{obs}, nil
		}
		if isPermissionError(out, err) {
			obs.Status = assessment.PermissionDenied
			obs.Error = "reading the package database requires elevated privileges"
			return []assessment.Observation{obs}, nil
		}
		obs.Status = assessment.Failed
		obs.Error = boundText(err.Error())
		return []assessment.Observation{obs}, nil
	}

	installed := 0
	var samples []string
	for _, line := range splitLines(out, res.Truncated) {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		name, version, status := fields[0], fields[1], fields[2]
		// db:Status-Abbrev is "ii " for a fully installed package.
		if !strings.HasPrefix(status, "ii") {
			continue
		}
		installed++
		if len(samples) < maxInventorySamples {
			samples = append(samples, name+"="+version)
		}
	}
	if installed == 0 {
		// A package database that reports nothing installed is a collection
		// problem, not a genuinely empty system — unless the listing was clipped,
		// in which case the honest reason is the clip, not an empty database.
		obs.Status = assessment.Incomplete
		obs.Error = "the package database reported no installed packages"
		if res.Truncated {
			obs.Error = boundText(truncatedNote("the package database listing", res.Cap))
		}
		return []assessment.Observation{obs}, nil
	}
	obs.Values = append(obs.Values,
		presentInt("installed_packages", int64(installed)),
		presentValue("sample_packages", strings.Join(samples, ",")),
	)
	if res.Truncated {
		// The count was taken from a clipped listing, so it is a lower bound and
		// the observation is not a complete reading of the package database.
		obs.Status = assessment.Incomplete
		obs.Error = boundText(truncatedNote("the package database listing", res.Cap))
		obs.Values = append(obs.Values, presentBool("installed_packages_is_lower_bound", true))
	}
	return []assessment.Observation{obs}, nil
}
