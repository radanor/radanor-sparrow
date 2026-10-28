package coreapp

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/collectors"
	"github.com/radanor/radanor-shield/internal/ipc"
)

// TestAssessmentScanResourceUsage reports the packaged scan's real cost on this
// host: wall-clock duration and heap growth attributable to the scan. It is
// reporting, not asserting a budget, because the CE-5 requirement is an honest
// measurement rather than a number chosen to look good. Run with -v to see it.
func TestAssessmentScanResourceUsage(t *testing.T) {
	if testing.Short() {
		t.Skip("resource measurement skipped in short mode")
	}
	api, store := newAssessmentAPI(t)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()

	if resp := api.handle(ipc.Request{Operation: "assessment_scan"}); !resp.OK {
		t.Fatalf("scan start refused: %+v", resp)
	}
	deadline := time.Now().Add(90 * time.Second)
	sawRunning := false
	for time.Now().Before(deadline) {
		if api.running() {
			sawRunning = true
		} else if sawRunning {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)

	t.Logf("manual scan: wall=%s heap_before=%d KiB heap_after=%d KiB total_alloc_delta=%d KiB sys=%d KiB",
		elapsed.Round(time.Millisecond),
		before.HeapAlloc/1024, after.HeapAlloc/1024,
		(after.TotalAlloc-before.TotalAlloc)/1024, after.Sys/1024)

	run, err := store.LatestRun(context.Background())
	if err != nil || run == nil {
		t.Fatalf("no run recorded: %v", err)
	}
	t.Logf("run %d status=%s checks=%d failed=%d unknown=%d collectors_failed=%d duration_ms=%d",
		run.ID, run.Status, run.ChecksTotal, run.Failed, run.Unknown, run.CollectorsFailed, run.DurationMS)
	t.Logf("check bindings: %d checks over %d collectors", len(collectors.PlatformChecks()), len(collectors.PlatformCollectors()))
}
