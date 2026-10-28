//go:build linux

package collectors

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// TestPrintLiveEvidence emits each collector's real reading on this host, and
// the verdict each bound check produces from that reading. It asserts nothing
// beyond well-formedness; its purpose is to give the acceptance record genuine,
// reproducible evidence rather than assumed values — including the native SSH
// and disk-encryption verdicts the CE-5 review corrections changed.
func TestPrintLiveEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("live evidence probe skipped in short mode")
	}
	cols := []assessment.Collector{
		PlatformCollector{}, SupportCollector{}, AppArmorCollector{},
		SSHServerCollector{},
		FirewallPostureCollector{},
		FirewalldCollector{}, UfwCollector{}, NftablesCollector{},
		ServicesCollector{}, NetworkCollector{}, DiskEncryptionCollector{},
		PendingSecurityUpdatesCollector{}, AutomaticUpdatesCollector{},
	}
	byCollector := map[string]assessment.Observation{}
	for _, c := range cols {
		obs, err := c.Collect(context.Background())
		if err != nil {
			t.Logf("%-20s ERROR %v", c.ID(), err)
			continue
		}
		b, _ := json.Marshal(obs[0].Values)
		t.Logf("%-20s [%s] %s", c.ID(), obs[0].Status, string(b))
		// The scanner owns the observation timestamp in production; stamp it
		// here too, so the verdict below is not spuriously stale.
		obs[0].CollectedAt = time.Now()
		byCollector[c.ID()] = obs[0]
	}
	// Evaluate every compiled check against the observation its collector
	// produced, so the recorded verdict is grounded in a real reading.
	for _, check := range PlatformChecks() {
		obs, ok := byCollector[check.CollectorID]
		if !ok {
			continue
		}
		res := check.Evaluate(obs)
		t.Logf("CHECK %-42s %-14s %s", check.ID, res.Result, res.Detail)
	}
}
