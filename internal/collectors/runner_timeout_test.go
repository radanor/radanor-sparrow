//go:build linux

package collectors

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These tests pin the CE-5 review's M1 finding: cmd.Run() waits for the output
// pipes to close, so a tool that leaves a descendant holding inherited
// stdout/stderr made the collector's command timeout unenforceable. Each case
// drives the real runner with a short deadline rather than the production one.

func TestCommandCompletesNormally(t *testing.T) {
	res, err := runCommandWithTimeout(context.Background(), 5*time.Second, "echo", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(res.Output) != "hello" {
		t.Fatalf("output = %q, want hello", res.Output)
	}
	if res.Truncated {
		t.Fatal("a small output was reported as truncated")
	}
}

func TestCommandTimeoutIsEnforced(t *testing.T) {
	start := time.Now()
	res, err := runCommandWithTimeout(context.Background(), 250*time.Millisecond, "sh", "-c", "sleep 30")
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("hung command blocked the collector for %v; the deadline must bound it", elapsed)
	}
	if strings.TrimSpace(res.Output) != "" {
		t.Fatalf("output = %q, want empty for a command that produced none", res.Output)
	}
}

// TestCommandOutputHeldByDescendantIsBounded is the direct M1 reproduction: the
// command exits immediately, but a background descendant inherits the output
// pipes and keeps them open. Without a wait delay, Run would not return until
// the descendant exited, so the collector's timeout would not apply at all.
func TestCommandOutputHeldByDescendantIsBounded(t *testing.T) {
	start := time.Now()
	res, err := runCommandWithTimeout(context.Background(), 30*time.Second, "sh", "-c", "sleep 2 & echo done")
	elapsed := time.Since(start)
	if !strings.Contains(res.Output, "done") {
		t.Fatalf("output = %q, want it to contain the command's own output", res.Output)
	}
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("error = %v, want exec.ErrWaitDelay naming the lingering output", err)
	}
	if elapsed > 1800*time.Millisecond {
		t.Fatalf("waited %v for output held by a descendant; want it bounded by the wait delay", elapsed)
	}
}

func TestCommandCancellationIsPrompt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := runCommandWithTimeout(ctx, 30*time.Second, "sh", "-c", "sleep 30")
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("cancellation took %v, want it prompt", elapsed)
	}
}

// TestPartialOutputSurvivesTheDeadline keeps the evidence rule honest: output a
// command produced before it was cut off is still returned to the collector.
func TestPartialOutputSurvivesTheDeadline(t *testing.T) {
	res, err := runCommandWithTimeout(context.Background(), 400*time.Millisecond, "sh", "-c", "echo partial; sleep 30")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if !strings.Contains(res.Output, "partial") {
		t.Fatalf("output = %q, want the partial output collected before the deadline", res.Output)
	}
}

func TestCommandFailureReportsOutputAndError(t *testing.T) {
	res, err := runCommandWithTimeout(context.Background(), 5*time.Second, "sh", "-c", "echo boom >&2; exit 3")
	if err == nil {
		t.Fatal("a failing command reported success")
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("error = %v, want a plain command failure", err)
	}
	if !strings.Contains(res.Output, "boom") {
		t.Fatalf("output = %q, want stderr captured alongside the failure", res.Output)
	}
}

func TestMissingToolIsStillClassifiedAfterTheChange(t *testing.T) {
	_, err := runCommandWithTimeout(context.Background(), 2*time.Second, "radanor-definitely-absent-tool-xyz")
	if !isMissingTool(err) {
		t.Fatalf("error = %v, want it classified as a missing tool", err)
	}
}

// TestCancellationKillsTheProcessGroup proves the cleanup, not just the bounded
// wait: a descendant started by a collector command must not survive its
// cancellation, because that is what would hold the output pipes open.
func TestCancellationKillsTheProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "descendant.pid")
	// Start a descendant that inherits the output pipes, record its pid, then
	// hang so only cancellation can end the command.
	script := fmt.Sprintf("sleep 30 & echo $! > %s; sleep 30", pidFile)
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if _, err := runCommandWithTimeout(ctx, 10*time.Second, "sh", "-c", script); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}

	pid := 0
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(pidFile)
		if err == nil {
			if n, cerr := strconv.Atoi(strings.TrimSpace(string(raw))); cerr == nil && n > 0 {
				pid = n
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("the descendant never recorded its pid")
	}

	// The descendant is in the cancelled command's process group, so the group
	// kill must take it down rather than leaving it to run for its full sleep.
	killDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(killDeadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("descendant %d survived the cancellation; the process group was not cleaned up", pid)
}

// TestCommandOutputIsBoundedAndSaysSo is the CE-6 H-1 regression guard at the
// runner level. Before the correction an oversize listing was clipped and every
// caller was told nothing, so a partial reading was indistinguishable from a
// complete one. The cap must now bound the capture *and* be reported.
func TestCommandOutputIsBoundedAndSaysSo(t *testing.T) {
	res, _ := runCommandWithTimeout(context.Background(), 300*time.Millisecond, "sh", "-c", "yes x")
	// The capture writer discards past the cap rather than erroring, so an
	// unbounded producer cannot exhaust memory or bloat stored evidence.
	if len(res.Output) > maxCommandOutput {
		t.Fatalf("captured %d bytes, want at most %d", len(res.Output), maxCommandOutput)
	}
	if len(res.Output) != maxCommandOutput {
		t.Fatalf("captured %d bytes, want the capture filled to the %d cap", len(res.Output), maxCommandOutput)
	}
	if !res.Truncated {
		t.Fatal("output was clipped at the cap but the result did not report truncation")
	}
	if res.Cap != maxCommandOutput {
		t.Fatalf("cap = %d, want %d", res.Cap, maxCommandOutput)
	}
}

// TestTruncationIsReportedOnAnErrorPath covers the combination that made the
// original defect worse: a command that both failed and overflowed must still
// carry the truncation flag, because its partial output is what a caller sees
// and may count.
func TestTruncationIsReportedOnAnErrorPath(t *testing.T) {
	// A bounded producer that overflows the cap and then fails: the failure must
	// not erase the fact that the captured output is a prefix.
	res, err := runCommandWithTimeout(context.Background(), 20*time.Second, "sh", "-c", "yes x | head -c 400000; exit 4")
	if err == nil {
		t.Fatal("a failing command reported success")
	}
	if !res.Truncated {
		t.Fatal("a clipped reading on a failing command did not report truncation")
	}
}

// TestInventoryCapIsExactlyReported pins the inventory-specific bound: an
// inventory listing legitimately exceeds the posture cap, so it must not be
// reported as truncated while it is still inside its own, larger limit.
func TestInventoryCapIsExactlyReported(t *testing.T) {
	if maxInventoryCommandOutput <= maxCommandOutput {
		t.Fatalf("inventory cap %d must exceed the posture cap %d", maxInventoryCommandOutput, maxCommandOutput)
	}
	// Produce roughly 400 KiB: over the posture cap, comfortably inside the
	// inventory cap. `head -c` bounds the producer so the test cannot run long.
	res, err := runCommandWithCap(context.Background(), 20*time.Second, maxInventoryCommandOutput, "sh", "-c", "yes 0123456789abcdef | head -c 400000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Truncated {
		t.Fatal("a 400000-byte listing was reported as truncated inside the inventory cap")
	}
	if len(res.Output) != 400000 {
		t.Fatalf("captured %d bytes, want the full 400000", len(res.Output))
	}
	posture, _ := runCommandWithCap(context.Background(), 20*time.Second, maxCommandOutput, "sh", "-c", "yes 0123456789abcdef | head -c 400000")
	if !posture.Truncated {
		t.Fatal("the same listing inside the posture cap did not report truncation")
	}
}
