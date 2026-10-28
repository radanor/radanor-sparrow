//go:build linux

package collectors

import (
	"context"
	"testing"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// TestRemainingCollectorsReportHonestStatus runs the service, network and disk
// collectors against the real host and asserts every observation is
// well-formed with an explanatory error whenever it is not a clean reading.
func TestRemainingCollectorsReportHonestStatus(t *testing.T) {
	collectors := map[string]assessment.Collector{
		"services":          ServicesCollector{},
		"network_endpoints": NetworkCollector{},
		"disk_encryption":   DiskEncryptionCollector{},
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
		case assessment.Collected, assessment.Incomplete:
			if len(o.Values) == 0 {
				t.Errorf("%s: status %q carries no evidence values", name, o.Status)
			}
		case assessment.PermissionDenied, assessment.Unsupported, assessment.Failed:
			if o.Error == "" {
				t.Errorf("%s: status %q must carry an explanation", name, o.Status)
			}
		default:
			t.Errorf("%s: unexpected status %q", name, o.Status)
		}
	}
}

// TestServicesCollectorDistinguishesAbsentFromInactive verifies the collector
// never reports an absent unit as an inactive one.
func TestServicesCollectorDistinguishesAbsentFromInactive(t *testing.T) {
	obs, err := ServicesCollector{}.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	o := obs[0]
	if o.Status != assessment.Collected && o.Status != assessment.Incomplete {
		t.Skipf("services not queryable on this host: %q", o.Status)
	}
	var sawAbsent bool
	for _, v := range o.Values {
		if v.Key == "unit.unattended-upgrades.service.present" && v.Text == "absent" {
			sawAbsent = true
		}
	}
	// Ubuntu ships unattended-upgrades; if the unit is genuinely absent the
	// collector must label it absent rather than emitting an inactive state.
	if sawAbsent {
		for _, v := range o.Values {
			if v.Key == "unit.unattended-upgrades.service.active" {
				t.Error("absent unit must not also report an active state")
			}
		}
	}
}

// TestDiskEncryptionNeverClaimsDisabled enforces the product rule: the disk
// collector must never emit a value asserting encryption is off.
func TestDiskEncryptionNeverClaimsDisabled(t *testing.T) {
	obs, err := DiskEncryptionCollector{}.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range obs[0].Values {
		if v.Kind == assessment.ValueBool &&
			(v.Key == "encryption_disabled" || v.Key == "unencrypted") {
			t.Fatalf("disk collector must never assert encryption is disabled; found %q", v.Key)
		}
	}
}
