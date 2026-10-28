//go:build linux

package collectors

import (
	"context"
	"testing"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// TestFirewallCollectorsReportHonestStatus runs the three firewall collectors
// against the real host. On an unprivileged account the expected outcome is
// PermissionDenied or Unsupported — never a fabricated "firewall off" reading.
func TestFirewallCollectorsReportHonestStatus(t *testing.T) {
	collectors := map[string]assessment.Collector{
		"firewalld": FirewalldCollector{},
		"ufw":       UfwCollector{},
		"nftables":  NftablesCollector{},
	}
	for name, c := range collectors {
		obs, err := c.Collect(context.Background())
		if err != nil {
			t.Fatalf("%s: collector errored instead of reporting status: %v", name, err)
		}
		if len(obs) != 1 {
			t.Fatalf("%s: expected exactly one observation, got %d", name, len(obs))
		}
		o := obs[0]
		switch o.Status {
		case assessment.Collected:
			// A collected firewall observation must carry at least one value.
			if len(o.Values) == 0 {
				t.Errorf("%s: collected observation has no evidence values", name)
			}
		case assessment.PermissionDenied, assessment.Unsupported, assessment.Incomplete, assessment.Failed:
			// Honest non-verdict states: the check must explain itself.
			if o.Error == "" {
				t.Errorf("%s: status %q must carry an explanation", name, o.Status)
			}
		default:
			t.Errorf("%s: unexpected status %q", name, o.Status)
		}
	}
}

// TestIsPermissionError verifies the refusal classification used by the
// firewall collectors against each tool's documented refusal text.
func TestIsPermissionError(t *testing.T) {
	cases := []struct {
		output string
		want   bool
	}{
		{"ERROR: You need to be root to run this script", true},
		{"Operation not permitted (you must be root)", true},
		{"Permission denied", true},
		{"Status: active", false},
		{"", false},
	}
	for _, tc := range cases {
		var err error
		if tc.want {
			err = errTestPermission{}
		}
		if got := isPermissionError(tc.output, err); got != tc.want {
			t.Errorf("isPermissionError(%q, %v) = %v, want %v", tc.output, err, got, tc.want)
		}
	}
}

type errTestPermission struct{}

func (errTestPermission) Error() string { return "permission denied" }
