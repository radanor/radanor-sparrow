//go:build linux

package collectors

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// maxPendingPackages bounds how many individual package names are echoed into
// evidence. Counts stay exact; only the name list is truncated.
const maxPendingPackages = 25

// PendingSecurityUpdatesCollector reports Ubuntu packages with a pending
// upgrade from the distribution security pocket.
//
// Sources, both unprivileged-readable:
//   - `apt-get --just-print upgrade` enumerates pending upgrades (the
//     simulation flag performs no changes and needs no lock).
//   - `apt-cache policy <pkg>` exposes each candidate's originating pocket, so
//     only jammy-security (etc.) packages are counted as security updates.
//
// The freshness of the local package lists is reported alongside, because a
// pending-updates answer derived from stale lists must not be presented as
// current. Absent or unreadable package lists yield an Incomplete observation,
// never a clean "no updates" reading.
type PendingSecurityUpdatesCollector struct{}

func (PendingSecurityUpdatesCollector) ID() string { return "pending_security_updates" }

func (PendingSecurityUpdatesCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "pending_security_updates", Collector: "pending_security_updates", Platform: ubuntuPlatformID,
		Status: assessment.Collected, Source: "apt-get --just-print upgrade + apt-cache policy",
	}

	if !aptListsPresent() {
		// No package lists at all: we cannot answer, so we say so.
		obs.Status = assessment.Incomplete
		obs.Error = "local apt package lists are absent; run an update before assessing pending security updates"
		return []assessment.Observation{obs}, nil
	}

	simRes, err := runCommand(ctx, "apt-get", "--just-print", "upgrade")
	sim := simRes.Output
	if err != nil {
		if isMissingTool(err) {
			obs.Status = assessment.Unsupported
			obs.Error = "apt-get is not available on this system"
			return []assessment.Observation{obs}, nil
		}
		if isPermissionError(sim, err) {
			obs.Status = assessment.PermissionDenied
			obs.Error = "querying pending upgrades requires privileges this process does not hold"
			return []assessment.Observation{obs}, nil
		}
		obs.Status = assessment.Failed
		obs.Error = boundText(err.Error())
		return []assessment.Observation{obs}, nil
	}

	// A clipped simulation omits upgrade lines, so the pending count below is a
	// lower bound; the observation says so rather than reporting a smaller number
	// as if it were the whole set.
	pending := parseSimulatedUpgrades(strings.Join(splitLines(sim, simRes.Truncated), "\n"))
	// Resolve every candidate's pocket in a single apt-cache invocation. The
	// per-package form spawns one process per pending upgrade, which dominated
	// the collector's runtime (over two seconds on a host with two dozen
	// pending upgrades); one batch call is materially cheaper and just as exact.
	safe := make([]string, 0, len(pending))
	for _, pkg := range pending {
		if safePackageName(pkg) {
			safe = append(safe, pkg)
		}
	}
	origins, oerr := aptCandidateOrigins(ctx, safe)
	if oerr != nil {
		obs.Status = assessment.Incomplete
		obs.Error = boundText("package policy could not be read: " + oerr.Error())
		return []assessment.Observation{obs}, nil
	}
	var securityNames []string
	for _, pkg := range safe {
		if isSecurityPocket(origins[pkg]) {
			securityNames = append(securityNames, pkg)
		}
	}
	sort.Strings(securityNames)

	shown := securityNames
	if len(shown) > maxPendingPackages {
		shown = shown[:maxPendingPackages]
	}
	obs.Values = append(obs.Values,
		presentInt("pending_upgrades_total", int64(len(pending))),
		presentInt("pending_security_updates", int64(len(securityNames))),
		presentValue("pending_security_packages", strings.Join(shown, ",")),
		presentValue("package_lists_age", listsAge()),
	)
	if simRes.Truncated {
		obs.Status = assessment.Incomplete
		obs.Error = boundText(truncatedNote("the pending-upgrade simulation", simRes.Cap))
		obs.Values = append(obs.Values, presentBool("pending_counts_are_lower_bounds", true))
	}
	return []assessment.Observation{obs}, nil
}

// parseSimulatedUpgrades extracts the upgraded package names from
// `apt-get --just-print upgrade` output. Only "Inst" lines name a package that
// would actually be upgraded.
func parseSimulatedUpgrades(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "Inst" {
			names = append(names, fields[1])
		}
	}
	return names
}

// maxPolicyPackages bounds how many package names are passed to a single
// apt-cache invocation, so argv limits and output size stay bounded even on a
// host with a very large pending set.
const maxPolicyPackages = 300

// aptCandidateOrigins returns, per package, the pocket that provides its
// candidate version, e.g. "jammy-security/main". Every name originates from
// apt's own enumeration above and is additionally constrained to a conservative
// charset before reaching argv.
func aptCandidateOrigins(ctx context.Context, pkgs []string) (map[string]string, error) {
	origins := map[string]string{}
	if len(pkgs) == 0 {
		return origins, nil
	}
	if len(pkgs) > maxPolicyPackages {
		pkgs = pkgs[:maxPolicyPackages]
	}
	args := append([]string{"policy"}, pkgs...)
	res, err := runCommand(ctx, "apt-cache", args...)
	if err != nil {
		return nil, err
	}
	if res.Truncated {
		// A clipped policy document would silently drop the pocket attribution of
		// the packages past the clip, turning security updates into "unknown".
		return nil, errors.New(truncatedNote("the apt policy listing", res.Cap))
	}
	return parsePolicyByPackage(res.Output), nil
}

// parsePolicyByPackage splits an `apt-cache policy a b c` document into one
// entry per package. Packages are separated by a line of the form `name:`.
func parsePolicyByPackage(out string) map[string]string {
	origins := map[string]string{}
	var current string
	var block []string
	flush := func() {
		if current != "" {
			origins[current] = parseCandidatePocket(strings.Join(block, "\n"))
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if name, ok := policyHeader(line); ok {
			flush()
			current, block = name, nil
			continue
		}
		if current != "" {
			block = append(block, line)
		}
	}
	flush()
	return origins
}

// policyHeader recognises the `name:` separator line, rejecting the indented
// field lines that also end in a colon (for example `  Candidate:`).
func policyHeader(line string) (string, bool) {
	if line == "" || line[0] == ' ' || line[0] == '\t' || !strings.HasSuffix(line, ":") {
		return "", false
	}
	name := strings.TrimSuffix(line, ":")
	if !safePackageName(name) {
		return "", false
	}
	return name, true
}

// safePackageName rejects anything that is not a plain Debian package name, so
// a surprising token from parsing can never reach a command argument.
func safePackageName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '+' || r == '-' || r == '.' || r == ':'
		if !ok {
			return false
		}
	}
	return true
}

// parseCandidatePocket reports the pocket that provides a package's candidate
// version. A pocket is attributed only when its origin line corresponds to the
// candidate version itself; a security pocket that merely lists some other
// (usually older) version of the package is deliberately not counted. That
// distinction is what keeps a pending-security-update count truthful.
func parseCandidatePocket(out string) string {
	candidate := ""
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Candidate:") {
			candidate = strings.TrimSpace(strings.TrimPrefix(trimmed, "Candidate:"))
			break
		}
	}
	if candidate == "" || candidate == "(none)" {
		return ""
	}
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Version-table entries look like: `0.107.1-3ubuntu0.22.04.5 500`,
		// optionally prefixed with `***` for the installed version.
		if !versionEntryMatches(trimmed, candidate) {
			continue
		}
		// The origin line for this version follows immediately.
		if i+1 >= len(lines) {
			continue
		}
		origin := strings.Fields(strings.TrimSpace(lines[i+1]))
		for _, f := range origin {
			if idx := strings.Index(f, "/"); idx > 0 && strings.Contains(f[:idx], "-security") {
				return f[idx+1:]
			}
		}
	}
	return ""
}

// versionEntryMatches reports whether a version-table line names exactly the
// candidate version (with an optional priority field).
func versionEntryMatches(line, candidate string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	version := strings.TrimPrefix(fields[0], "***")
	return version == candidate
}

// isSecurityPocket reports whether a pocket name denotes the Ubuntu security
// pocket (e.g. "jammy-security/main").
func isSecurityPocket(pocket string) bool {
	return strings.Contains(pocket, "-security")
}

// aptListsPresent reports whether any package list file exists locally.
func aptListsPresent() bool {
	entries, err := os.ReadDir("/var/lib/apt/lists")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), "_Packages") {
			return true
		}
	}
	return false
}

// listsAge returns the age of the newest package list, so a reading derived
// from stale data can be qualified honestly.
func listsAge() string {
	entries, err := os.ReadDir("/var/lib/apt/lists")
	if err != nil {
		return "unknown"
	}
	var newest time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_Packages") {
			continue
		}
		info, ierr := e.Info()
		if ierr == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	if newest.IsZero() {
		return "unknown"
	}
	return time.Since(newest).Round(time.Hour).String()
}
