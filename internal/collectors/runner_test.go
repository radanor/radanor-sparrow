//go:build linux

package collectors

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/radanor/radanor-shield/internal/assessment"
)

// TestIsMissingTool verifies the classification of a genuinely absent
// executable. This is a regression guard: exec returns *exec.Error wrapping
// exec.ErrNotFound, which does not satisfy errors.Is(err, os.ErrNotExist), so
// an unpatched collector used to report "failed" for an uninstalled tool.
func TestIsMissingTool(t *testing.T) {
	_, err := exec.Command("radanor-definitely-absent-tool-xyz").Output()
	if err == nil {
		t.Fatal("expected an error for an absent tool")
	}
	if !isMissingTool(err) {
		t.Fatalf("absent tool not recognized; err=%v (T=%T)", err, err)
	}
	if isMissingTool(errors.New("some other failure")) {
		t.Error("a generic error must not be classified as a missing tool")
	}
	if isMissingTool(nil) {
		t.Error("nil error must not be classified as a missing tool")
	}
}

// TestMissingToolCollectorReportsUnsupported asserts the collector surfaces an
// absent tool as Unsupported, so a check that cannot run is never presented as
// a security failure.
func TestMissingToolCollectorReportsUnsupported(t *testing.T) {
	c := missingToolCollector{}
	obs, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("collector errored: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("expected one observation, got %d", len(obs))
	}
	if obs[0].Status != assessment.Unsupported {
		t.Fatalf("status = %q, want %q", obs[0].Status, assessment.Unsupported)
	}
	if obs[0].Error == "" {
		t.Error("an unsupported observation must explain itself")
	}
}

// missingToolCollector exercises runCommand against a tool that is guaranteed
// to be absent, isolating the missing-tool path for the test.
type missingToolCollector struct{}

func (missingToolCollector) ID() string { return "missing_tool_probe" }

func (missingToolCollector) Collect(ctx context.Context) ([]assessment.Observation, error) {
	obs := assessment.Observation{
		ID: "missing_tool_probe", Collector: "missing_tool_probe", Platform: ubuntuPlatformID,
		Status: assessment.Collected, Source: "radanor-definitely-absent-tool-xyz",
	}
	_, err := runCommand(ctx, "radanor-definitely-absent-tool-xyz", "--version")
	if err != nil {
		if isMissingTool(err) {
			obs.Status = assessment.Unsupported
			obs.Error = "the required tool is not installed on this system"
			return []assessment.Observation{obs}, nil
		}
		obs.Status = assessment.Failed
		obs.Error = boundText(err.Error())
		return []assessment.Observation{obs}, nil
	}
	return []assessment.Observation{obs}, nil
}
