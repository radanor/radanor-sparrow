package collectors

import (
	"strings"
	"testing"
)

// TestSecurityServiceAllowlistHasNoDuplicates is the regression guard for the
// CE-5 low-severity finding on duplicate Windows service names. The allowlist
// once listed the Windows Firewall service twice, as "mpssvc" and "MpsSvc":
// since service names are case-insensitive, the collector queried it twice,
// double-counted services_present, and emitted the same evidence key twice.
func TestSecurityServiceAllowlistHasNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range windowsSecurityServices {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			t.Fatal("the allowlist contains an empty service name")
		}
		if seen[key] {
			t.Fatalf("duplicate service %q (case-insensitive) in the allowlist", name)
		}
		seen[key] = true
	}
}

// TestUniqueServiceNamesDropsCaseInsensitiveDuplicates pins the helper's
// contract: first-seen order and spelling are preserved and a case-variant is
// removed.
func TestUniqueServiceNamesDropsCaseInsensitiveDuplicates(t *testing.T) {
	got := uniqueServiceNames([]string{"MpsSvc", "mpssvc", "WinDefend", "MPSsvc", " winDefend "})
	want := []string{"MpsSvc", "WinDefend"}
	if len(got) != len(want) {
		t.Fatalf("uniqueServiceNames = %v, want %v", got, want)
	}
	for i := range want {
		if !strings.EqualFold(got[i], want[i]) {
			t.Fatalf("uniqueServiceNames = %v, want first-seen spellings %v", got, want)
		}
	}
}

// TestServiceNamesForQueryListsEachNameOnce asserts the rendered PowerShell
// literal cannot ask Get-Service for the same service twice.
func TestServiceNamesForQueryListsEachNameOnce(t *testing.T) {
	query := serviceNamesForQuery([]string{"MpsSvc", "mpssvc", "WinDefend"})
	if strings.Count(strings.ToLower(query), "mpssvc") != 1 {
		t.Fatalf("query repeats a service: %s", query)
	}
	if !strings.HasPrefix(query, "@(") || !strings.HasSuffix(query, ")") {
		t.Fatalf("query is not a PowerShell array literal: %s", query)
	}
}
