package collectors

import (
	"strings"
	"testing"
)

// TestPowerShellArgsDoNotBypassExecutionPolicy is the regression guard for the
// CE-5 low-severity finding on collector command execution policy: the
// collector must not weaken the host's PowerShell execution policy to run a
// read-only query.
func TestPowerShellArgsDoNotBypassExecutionPolicy(t *testing.T) {
	args := powershellArgs("Get-MpComputerStatus")
	for i, a := range args {
		lower := strings.ToLower(a)
		if lower == "-executionpolicy" || strings.Contains(lower, "bypass") || lower == "unrestricted" {
			t.Fatalf("collector argument %d weakens the execution policy: %q", i, a)
		}
	}
	if len(args) == 0 || args[len(args)-1] != "Get-MpComputerStatus" {
		t.Fatalf("the script must be the final argument: %v", args)
	}
}

// TestPowerShellArgsAreNonInteractiveAndProfileFree pins the flags the
// collectors rely on: no user profile, no interactive prompt, and the script
// passed via -Command (never a script file).
func TestPowerShellArgsAreNonInteractiveAndProfileFree(t *testing.T) {
	args := powershellArgs("Get-Service")
	joined := strings.ToLower(strings.Join(args, " "))
	for _, want := range []string{"-noprofile", "-noninteractive", "-command"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argument vector %v is missing %s", args, want)
		}
	}
}
