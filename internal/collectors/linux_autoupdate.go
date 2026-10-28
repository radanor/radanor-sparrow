//go:build linux

package collectors

import (
	"context"
	"os"
	"regexp"
	"strings"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// AutomaticUpdatesCollector reports whether unattended security upgrades are
// configured.
//
// Sources are the native configuration files, read directly rather than by
// shelling out:
//   - /etc/apt/apt.conf.d/20auto-upgrades sets APT::Periodic::Unattended-Upgrade.
//   - /etc/apt/apt.conf.d/50unattended-upgrades lists Unattended-Upgrade::
//     Allowed-Origins, whose entries name the pockets eligible for automatic
//     upgrade.
//
// The collector only reports what is configured. It does not claim updates will
// in fact be applied: cron/systemd timer execution is a separate concern, and
// an absent configuration is reported as an absence rather than a failure.
type AutomaticUpdatesCollector struct{}

func (AutomaticUpdatesCollector) ID() string { return "automatic_security_updates" }

var (
	periodicRe = regexp.MustCompile(`APT::Periodic::Unattended-Upgrade\s+"?(\d+)"?`)
	originRe   = regexp.MustCompile(`"([^"]+)"`)
)

func (AutomaticUpdatesCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "automatic_security_updates", Collector: "automatic_security_updates", Platform: ubuntuPlatformID,
		Status: assessment.Collected, Source: "/etc/apt/apt.conf.d",
	}

	periodicText, perr := readFileText("/etc/apt/apt.conf.d/20auto-upgrades")
	if perr != nil {
		periodicText, perr = readFileText("/etc/apt/apt.conf.d/10periodic")
	}
	if perr != nil {
		if os.IsNotExist(perr) {
			// No automatic-upgrade configuration is a legitimate state, not an
			// error; report it as an absence of evidence.
			obs.Status = assessment.Incomplete
			obs.Error = "no unattended-upgrade configuration was found"
			return []assessment.Observation{obs}, nil
		}
		if os.IsPermission(perr) {
			obs.Status = assessment.PermissionDenied
			obs.Error = "apt configuration is not readable by this user"
			return []assessment.Observation{obs}, nil
		}
		obs.Status = assessment.Failed
		obs.Error = boundText(perr.Error())
		return []assessment.Observation{obs}, nil
	}

	enabled := false
	if m := periodicRe.FindStringSubmatch(periodicText); m != nil {
		enabled = m[1] != "0"
	}
	obs.Values = append(obs.Values, presentBool("unattended_upgrade_enabled", enabled))

	originsText, oerr := readFileText("/etc/apt/apt.conf.d/50unattended-upgrades")
	if oerr != nil {
		// Enabled but the origins list is unreadable: partial evidence only.
		obs.Status = assessment.Incomplete
		obs.Error = boundText("could not read unattended-upgrades origins: " + oerr.Error())
		return []assessment.Observation{obs}, nil
	}
	origins := parseAllowedOrigins(originsText)
	securityCount := 0
	for _, o := range origins {
		if strings.Contains(o, "-security") {
			securityCount++
		}
	}
	obs.Values = append(obs.Values,
		presentInt("allowed_origins", int64(len(origins))),
		presentInt("security_origins", int64(securityCount)),
	)
	return []assessment.Observation{obs}, nil
}

// parseAllowedOrigins extracts the uncommented Allowed-Origins entries.
func parseAllowedOrigins(text string) []string {
	inBlock := false
	var origins []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.Contains(trimmed, "Allowed-Origins") {
			inBlock = true
			continue
		}
		if inBlock && strings.HasPrefix(trimmed, "}") {
			break
		}
		if inBlock {
			for _, m := range originRe.FindAllStringSubmatch(trimmed, -1) {
				origins = append(origins, m[1])
			}
		}
	}
	return origins
}
