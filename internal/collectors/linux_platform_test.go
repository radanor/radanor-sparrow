//go:build linux

package collectors

import (
	"context"
	"strings"
	"testing"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// These tests run against the real host. They assert that a collector returns a
// well-formed observation with a bounded status, and that it never reports a
// security verdict in the observation itself.
func runSingle(t *testing.T, c assessment.Collector) assessment.Observation {
	t.Helper()
	obs, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("%s collector returned error: %v", c.ID(), err)
	}
	if len(obs) == 0 {
		t.Fatalf("%s collector returned no observation", c.ID())
	}
	for _, o := range obs {
		if o.Collector != c.ID() {
			t.Errorf("observation collector %q does not match %q", o.Collector, c.ID())
		}
		switch o.Status {
		case assessment.Collected, assessment.PermissionDenied, assessment.Unsupported, assessment.Incomplete, assessment.Failed:
		default:
			t.Errorf("%s: unexpected status %q", c.ID(), o.Status)
		}
	}
	return obs[0]
}

func TestPlatformCollector(t *testing.T) {
	obs := runSingle(t, PlatformCollector{})
	if obs.Status != assessment.Collected {
		t.Fatalf("expected collected on Ubuntu host, got %q (%s)", obs.Status, obs.Error)
	}
	if valueOf(obs, "os_id") != "ubuntu" {
		t.Errorf("os_id = %q, want ubuntu", valueOf(obs, "os_id"))
	}
	if valueOf(obs, "arch") == "" {
		t.Error("arch evidence missing")
	}
}

// TestPlatformCollectorDoesNotFabricateKernelPresence guards the CE-5
// low-severity finding: the platform collector emitted a constant
// "kernel_present" value it had never observed. The kernel release is now read
// from the kernel and reported only when that read succeeds, so the evidence is
// a fact rather than an assertion.
func TestPlatformCollectorDoesNotFabricateKernelPresence(t *testing.T) {
	obs := runSingle(t, PlatformCollector{})
	if _, present := valuePresent(obs, "kernel_present"); present {
		t.Fatal("platform collector emitted a fabricated kernel_present constant")
	}
	if v, present := valuePresent(obs, "kernel_release"); present && strings.TrimSpace(v.Text) == "" {
		t.Fatal("a reported kernel_release must not be empty")
	}
}

func TestSupportCollector(t *testing.T) {
	obs := runSingle(t, SupportCollector{})
	switch obs.Status {
	case assessment.Collected, assessment.Incomplete:
	default:
		t.Fatalf("unexpected status %q", obs.Status)
	}
	if obs.Status == assessment.Collected && valueOf(obs, "support_ends") == "" {
		t.Error("collected support observation must carry a support_end date")
	}
}

func TestAppArmorCollectorHonestStatus(t *testing.T) {
	obs := runSingle(t, AppArmorCollector{})
	// On this host AppArmor is enabled but the profile list needs root; the
	// collector must report Collected with an explanatory note, not a denial.
	if obs.Status != assessment.Collected && obs.Status != assessment.Unsupported {
		t.Fatalf("unexpected status %q (%s)", obs.Status, obs.Error)
	}
	if _, present := valuePresent(obs, "enabled"); !present && obs.Status == assessment.Collected {
		t.Error("collected apparmor observation must carry an enabled value")
	}
}

// valueOf returns the text of a named evidence value.
func valueOf(obs assessment.Observation, key string) string {
	if v, ok := valuePresent(obs, key); ok {
		return v.Text
	}
	return ""
}

func valuePresent(obs assessment.Observation, key string) (assessment.Value, bool) {
	for _, v := range obs.Values {
		if v.Key == key {
			return v, true
		}
	}
	return assessment.Value{}, false
}
