package collectors

import "strings"

// The reviewed allowlist of built-in Windows services whose state materially
// affects local security posture, and the helpers that keep it unique.
//
// This file carries no build tag so the list's uniqueness and the query
// rendering are exercised by the test suite that runs on every platform, not
// only by the Windows cross-build.
//
// Windows service names are case-insensitive. The CE-5 review found the
// allowlist listing the same service twice, as "mpssvc" and "MpsSvc": the
// collector then queried it twice, counted it twice in services_present, and
// emitted the same evidence key twice. The list is now unique
// case-insensitively, and the collector deduplicates what it records as well.

var windowsSecurityServices = []string{
	"WinDefend",
	"MpsSvc",
	"BFE",
	"EventLog",
	"wscsvc",
	"SecurityHealthService",
	"WdNisSvc",
}

// uniqueServiceNames removes case-insensitive duplicates, preserving first-seen
// order and the original spelling of each name. It is applied to the allowlist
// so a future edit that reintroduces a case-variant cannot silently double-count
// a service.
func uniqueServiceNames(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
	}
	return out
}

// serviceNamesForQuery renders a name list as the PowerShell array literal the
// service collector passes to Get-Service. Each name appears exactly once.
func serviceNamesForQuery(names []string) string {
	unique := uniqueServiceNames(names)
	quoted := make([]string, 0, len(unique))
	for _, name := range unique {
		quoted = append(quoted, "'"+strings.ReplaceAll(name, "'", "''")+"'")
	}
	return "@(" + strings.Join(quoted, ",") + ")"
}
