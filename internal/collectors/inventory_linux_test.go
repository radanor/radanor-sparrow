//go:build linux

package collectors

import (
	"context"
	"os/exec"
	"sort"
	"strings"
	"testing"

	"github.com/radanor/radanor-shield/internal/inventory"
)

// CE-6 Linux inventory tests.
//
// Two kinds of evidence are collected here. The parsers are driven directly from
// the exact text the tools emit, so the interpretation rules are pinned without
// depending on what this host happens to have installed. The collectors are then
// run for real, but only ever asserted against the honest-status contract: a
// collector may report collected, unsupported, permission denied or failed, and
// a collected result must be structurally valid. Nothing asserts that any
// particular package or socket exists on the test machine.

func TestSplitEndpointHandlesEveryAddressShapeSSEmits(t *testing.T) {
	cases := []struct {
		raw     string
		address string
		port    string
	}{
		{"0.0.0.0:22", "0.0.0.0", "22"},
		{"127.0.0.1:631", "127.0.0.1", "631"},
		{"*:80", "*", "80"},
		{"[::]:22", "::", "22"},
		{"[::1]:5432", "::1", "5432"},
		{"[fe80::1%eth0]:546", "fe80::1%eth0", "546"},
		// ss always brackets an IPv6 address when it prints one with a port, so
		// these two no-port forms are the only ones that reach the fallback.
		{"0.0.0.0", "0.0.0.0", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		address, port := splitEndpoint(tc.raw)
		if address != tc.address || port != tc.port {
			t.Errorf("splitEndpoint(%q) = (%q, %q), want (%q, %q)", tc.raw, address, port, tc.address, tc.port)
		}
	}
}

func TestParseSSProcessReadsAttributionOrAdmitsItCannot(t *testing.T) {
	cases := []struct {
		name string
		line string
		want string
	}{
		{"quoted name with pid", `tcp LISTEN 0 128 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=912,fd=3))`, "sshd (pid 912)"},
		{"name only", `udp UNCONN 0 0 127.0.0.1:5353 0.0.0.0:* users:(("avahi-daemon"))`, "avahi-daemon"},
		{"multiple owners takes the first", `tcp LISTEN 0 128 [::]:80 [::]:* users:(("nginx",pid=1,fd=6),("nginx",pid=2,fd=6))`, "nginx (pid 1)"},
		// The kernel hides the owner of sockets this user may not inspect. An
		// unattributable socket must yield nothing rather than a guess.
		{"no process column", `tcp LISTEN 0 128 0.0.0.0:25 0.0.0.0:*`, ""},
		{"empty owner list", `tcp LISTEN 0 128 0.0.0.0:25 0.0.0.0:* users:(())`, ""},
		{"truncated line", `tcp LISTEN 0 128 0.0.0.0:25 users:((`, ""},
	}
	for _, tc := range cases {
		if got := parseSSProcess(tc.line); got != tc.want {
			t.Errorf("%s: parseSSProcess = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestInventoryFailureClassifiesHonestly(t *testing.T) {
	// A tool that is not installed means the interface does not apply here. The
	// error is the real shape exec returns for a binary missing from PATH.
	status, message := inventoryFailure("", &exec.Error{Name: "ss", Err: exec.ErrNotFound}, "the tool is not available", "elevation is required")
	if status != inventory.Unsupported || message != "the tool is not available" {
		t.Fatalf("missing tool = (%q, %q), want unsupported", status, message)
	}
	// A refused read is a permission problem, not an unsupported platform.
	status, message = inventoryFailure("Access denied: you need to be root", errFake{}, "missing", "elevation is required")
	if status != inventory.PermissionDenied || message != "elevation is required" {
		t.Fatalf("refusal = (%q, %q), want permission_denied", status, message)
	}
	// Anything else is reported as a failure with the tool's own words, bounded.
	status, message = inventoryFailure("", errFake{}, "missing", "denied")
	if status != inventory.Failed || message == "" {
		t.Fatalf("other failure = (%q, %q), want failed with a message", status, message)
	}
}

type errFake struct{}

func (errFake) Error() string { return "the command exited with status 1" }

// TestLinuxInventoryCollectorsReportHonestStatuses runs the real collectors and
// checks that whatever they report is internally consistent and honestly
// labelled. This host's content is never asserted.
func TestLinuxInventoryCollectorsReportHonestStatuses(t *testing.T) {
	ctx := context.Background()
	for _, collector := range PlatformInventoryCollectors() {
		kind := collector.Kind()
		collection := collector.CollectInventory(ctx)
		if collection.Kind != kind {
			t.Fatalf("%s: collection kind = %q", kind, collection.Kind)
		}
		switch collection.Status {
		case inventory.Collected:
			if len(collection.Items) == 0 && collection.Note == "" && collection.Error == "" {
				t.Errorf("%s: reported collected with no items and no explanation", kind)
			}
			if collection.Total < len(collection.Items) {
				t.Errorf("%s: total %d is below the %d items supplied", kind, collection.Total, len(collection.Items))
			}
			names := make([]string, 0, len(collection.Items))
			for _, item := range collection.Items {
				if _, err := item.Normalize(); err != nil {
					t.Errorf("%s: item %q is not storable: %v", kind, item.Name, err)
				}
				if item.Source == "" {
					t.Errorf("%s: item %q has no source, so its provenance is unknown", kind, item.Name)
				}
				names = append(names, item.Name)
			}
			if !sort.StringsAreSorted(names) {
				t.Errorf("%s: items are not sorted by name, so pagination is not stable", kind)
			}
		case inventory.Unsupported, inventory.PermissionDenied, inventory.Incomplete, inventory.Failed:
			// An unusable result must explain itself: an unexplained empty list
			// would be indistinguishable from a clean system.
			if collection.Error == "" {
				t.Errorf("%s: status %q carries no explanation", kind, collection.Status)
			}
			if len(collection.Items) != 0 {
				t.Errorf("%s: status %q also returned %d items", kind, collection.Status, len(collection.Items))
			}
		default:
			t.Fatalf("%s: unexpected status %q", kind, collection.Status)
		}
	}
}

// TestLinuxInventoryCollectorsAreUnsupportedWithoutTheirTools forces the
// missing-tool path. Removing every tool from PATH must produce "unsupported",
// never a silently empty catalogue.
func TestLinuxInventoryCollectorsAreUnsupportedWithoutTheirTools(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	ctx := context.Background()
	for _, collector := range PlatformInventoryCollectors() {
		collection := collector.CollectInventory(ctx)
		if collection.Status != inventory.Unsupported {
			t.Errorf("%s: status = %q (%s), want unsupported when its tool is absent", collector.Kind(), collection.Status, collection.Error)
		}
		if collection.Error == "" {
			t.Errorf("%s: unsupported without an explanation", collector.Kind())
		}
		if len(collection.Items) != 0 {
			t.Errorf("%s: reported %d items without running anything", collector.Kind(), len(collection.Items))
		}
	}
}

// TestLinuxSoftwareNoteDoesNotClaimCompleteness is the CE-6 wording guard: the
// package inventory must never be presented as a catalogue of every application.
func TestLinuxSoftwareNoteDoesNotClaimCompleteness(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	collection := SoftwareInventoryCollector{}.CollectInventory(context.Background())
	// The unsupported path explains the missing tool; the collected path carries
	// the wording that matters. Check the wording the collector would use.
	note := collection.Note
	if note == "" {
		note = softwareInventoryNote
	}
	lower := strings.ToLower(note)
	if !strings.Contains(lower, "not a catalogue") && !strings.Contains(lower, "not a complete") {
		t.Fatalf("the software inventory note does not disclose its coverage: %q", note)
	}
	for _, claim := range []string{"all applications", "every application on", "vulnerable", "complete list"} {
		if strings.Contains(lower, claim) && !strings.Contains(lower, "not a") && !strings.Contains(lower, "not ") {
			t.Fatalf("the software inventory note makes an unsupported claim (%q): %q", claim, note)
		}
	}
}

// TestLinuxEndpointCollectionClaimsNoReachability pins the rule that a listening
// socket is never described as reachable from elsewhere.
func TestLinuxEndpointCollectionClaimsNoReachability(t *testing.T) {
	collection := EndpointInventoryCollector{}.CollectInventory(context.Background())
	text := strings.ToLower(collection.Note + " " + collection.Error)
	for _, claim := range []string{"exposed", "publicly", "reachable from", "open to the internet", "internet-facing"} {
		if strings.Contains(text, claim) {
			t.Fatalf("the endpoint collection makes a reachability claim (%q): %q", claim, collection.Note+collection.Error)
		}
	}
	for _, item := range collection.Items {
		if item.Fields["attribution"] != "observed" && item.Fields["attribution"] != "unknown" {
			t.Fatalf("endpoint %q has attribution %q, which is neither observed nor unknown", item.Name, item.Fields["attribution"])
		}
		if item.Fields["attribution"] == "unknown" && item.Fields["attribution_note"] == "" {
			t.Fatalf("endpoint %q is unattributed without saying so", item.Name)
		}
	}
}
