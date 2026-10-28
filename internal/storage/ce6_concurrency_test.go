package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
	"github.com/radanor/radanor-shield/internal/inventory"
)

// CE-6 reliability tests. The inventory and settings work touched the same
// database as monitoring ingestion and posture assessment, so these tests exercise
// them together and then check the invariants that must survive the interleaving:
// the monitoring meter still matches the monitoring rows, the inventory scope is
// still charged only by inventory, and retention still bounds what it promises.

func TestConcurrentIngestionInventoryAndAssessmentWork(t *testing.T) {
	s, _ := openStore(t, func(l *Limits) {
		l.MaxEvents = 5000
		l.MaxBytes = 64 << 20
		l.MaxAssessmentRuns = 50
	})
	ctx := context.Background()

	const events = 300
	var accepted int64
	var inventoryRounds int64

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	record := func(err error) {
		if err != nil {
			errs <- err
		}
	}

	// Monitoring ingestion.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < events; i++ {
			err := s.Process(ctx, testEvent(fmt.Sprintf("concurrent-%d", i), "native.file_integrity", []byte(fmt.Sprintf("cp-%d", i))))
			if err != nil {
				record(fmt.Errorf("ingest %d: %w", i, err))
				return
			}
			atomic.AddInt64(&accepted, 1)
		}
	}()

	// Inventory replacement, alternating between kinds so a partial refresh is
	// covered too.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for round := 0; round < 20; round++ {
			items := make([]inventory.Item, 0, 50)
			for i := 0; i < 50; i++ {
				items = append(items, softwareItem(fmt.Sprintf("pkg-%03d-%03d", round, i), "1.0"))
			}
			snap := testInventorySnapshot(time.Now(), inventoryPart(inventory.KindSoftware, items))
			if round%3 == 0 {
				snap.Merge(inventoryPart(inventory.KindService, []inventory.Item{serviceItem(fmt.Sprintf("unit-%03d.service", round))}))
			}
			if err := s.ReplaceInventory(ctx, snap); err != nil {
				record(fmt.Errorf("inventory round %d: %w", round, err))
				return
			}
			atomic.AddInt64(&inventoryRounds, 1)
		}
	}()

	// Inventory reads and status reads, interleaved with the writes above.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			if _, _, err := s.InventoryItems(ctx, InventoryQuery{Kind: inventory.KindSoftware, Limit: 10}); err != nil {
				record(fmt.Errorf("inventory read %d: %w", i, err))
				return
			}
			if _, err := s.InventoryStatus(ctx); err != nil {
				record(fmt.Errorf("inventory status %d: %w", i, err))
				return
			}
			if _, err := s.StorageUsage(ctx); err != nil {
				record(fmt.Errorf("storage usage %d: %w", i, err))
				return
			}
		}
	}()

	// Assessment runs, with retention running alongside them.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			runID, err := s.StartRun(ctx, "manual")
			if err != nil {
				record(fmt.Errorf("start run %d: %w", i, err))
				return
			}
			if err := s.FinishRun(ctx, runID, assessment.ScanOutcome{Status: "completed", StartedAt: time.Now(), FinishedAt: time.Now()}); err != nil {
				record(fmt.Errorf("finish run %d: %w", i, err))
				return
			}
			if i%5 == 0 {
				if err := s.RetainAssessments(ctx, 20); err != nil {
					record(fmt.Errorf("retain %d: %w", i, err))
					return
				}
			}
		}
	}()

	// Settings reads and writes from the settings page.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			if err := s.PutSettings(ctx, map[string]string{"theme": "dark"}); err != nil {
				record(fmt.Errorf("settings write %d: %w", i, err))
				return
			}
			if _, err := s.Settings(ctx); err != nil {
				record(fmt.Errorf("settings read %d: %w", i, err))
				return
			}
		}
	}()

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent work failed: %v", err)
	}

	// Invariant 1: the monitoring meter still matches the monitoring rows
	// exactly. A drift here is what would let monitoring pressure be reported
	// wrongly, or let real pressure go unnoticed.
	usage, err := s.StorageUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var measured int64
	if err := s.db.QueryRowContext(ctx, `SELECT `+eventBytesExpr+` FROM events`).Scan(&measured); err != nil {
		t.Fatal(err)
	}
	if usage[usageMonitoring] != measured {
		t.Fatalf("monitoring meter = %d, measured rows = %d", usage[usageMonitoring], measured)
	}
	if n := countRows(t, s, "SELECT count(*) FROM events"); int64(n) != accepted {
		t.Fatalf("stored events = %d, accepted = %d", n, accepted)
	}

	// Invariant 2: the inventory scope is charged only by inventory, and its
	// meter matches its own tables.
	if atomic.LoadInt64(&inventoryRounds) != 20 {
		t.Fatalf("inventory rounds = %d, want 20", inventoryRounds)
	}
	var inventoryMeasured int64
	if err := s.db.QueryRowContext(ctx, `SELECT `+inventoryBytesExpr).Scan(&inventoryMeasured); err != nil {
		t.Fatal(err)
	}
	if usage[usageInventory] != inventoryMeasured {
		t.Fatalf("inventory meter = %d, measured rows = %d", usage[usageInventory], inventoryMeasured)
	}
	if usage[usageInventory] > s.limits.MaxInventoryBytes {
		t.Fatalf("inventory exceeded its budget: %d of %d", usage[usageInventory], s.limits.MaxInventoryBytes)
	}

	// Invariant 3: the configured retention horizon held during the concurrent
	// scans, and an explicit retention request is honoured afterwards. The
	// explicit value used mid-test (20) is deliberately lower than the configured
	// one (50), so the horizon only has to stay inside the configured limit until
	// it is requested again.
	runs, err := s.Runs(ctx, 0, s.limits.MaxPageSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) > s.limits.MaxAssessmentRuns {
		t.Fatalf("stored assessment runs = %d, over the configured %d", len(runs), s.limits.MaxAssessmentRuns)
	}
	if err := s.RetainAssessments(ctx, 20); err != nil {
		t.Fatal(err)
	}
	runs, err = s.Runs(ctx, 0, s.limits.MaxPageSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 20 {
		t.Fatalf("stored runs = %d after an explicit retention of 20", len(runs))
	}

	// Invariant 4: the inventory still answers after the load, and the settings
	// value that was written is still readable.
	if _, _, err := s.InventoryItems(ctx, InventoryQuery{Kind: inventory.KindSoftware, Limit: 10}); err != nil {
		t.Fatalf("inventory read after the load: %v", err)
	}
	settings, err := s.Settings(ctx)
	if err != nil || settings["theme"] != "dark" {
		t.Fatalf("settings after the load = %v (err %v)", settings, err)
	}
	// The final replacement stored exactly one service unit.
	if n := countRows(t, s, "SELECT count(*) FROM inventory_items WHERE kind=?", string(inventory.KindService)); n > 1 {
		t.Fatalf("service rows = %d, want at most 1: the last replacement must not accumulate", n)
	}
}

// TestInventoryRefreshDuringMonitoringPressure checks the documented isolation
// from the other side: when monitoring has exhausted its own budget, the
// inventory can still be refreshed, and the refusal a caller sees is the
// monitoring refusal, not an inventory one.
func TestInventoryRefreshDuringMonitoringPressure(t *testing.T) {
	s, _ := openStore(t, func(l *Limits) { l.MaxBytes = 2048 })
	ctx := context.Background()
	for i := 0; i < 200; i++ {
		err := s.Process(ctx, testEvent(fmt.Sprintf("pressure-%d", i), "native.file_integrity", nil))
		if err == nil {
			continue
		}
		if !errors.Is(err, ErrStoragePressure) {
			t.Fatalf("unexpected ingestion error: %v", err)
		}
		break
	}
	if err := s.Process(ctx, testEvent("pressure-final", "native.file_integrity", nil)); !errors.Is(err, ErrStoragePressure) {
		t.Fatalf("monitoring should be in pressure, got %v", err)
	}

	// The inventory is unaffected: it has its own budget and its own tables.
	items := make([]inventory.Item, 0, 100)
	for i := 0; i < 100; i++ {
		items = append(items, softwareItem(fmt.Sprintf("pkg-%03d", i), "1"))
	}
	if err := s.ReplaceInventory(ctx, testInventorySnapshot(time.Now(), inventoryPart(inventory.KindSoftware, items))); err != nil {
		t.Fatalf("inventory replace during monitoring pressure: %v", err)
	}
	usage, err := s.StorageUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usage[usageInventory] <= 0 {
		t.Fatalf("inventory bytes = %d, want > 0", usage[usageInventory])
	}
	if usage[usageMonitoring] > s.limits.MaxBytes {
		t.Fatalf("monitoring meter = %d, over its %d budget", usage[usageMonitoring], s.limits.MaxBytes)
	}
}

// TestStartingAScanNeverExceedsTheRetentionLimit is the CE-6 storage hardening
// regression for the off-by-one that let the stored run count sit one above the
// configured limit until the following scan happened to prune it. Retention is a
// statement about what is stored, so it is enforced on the insert path too.
func TestStartingAScanNeverExceedsTheRetentionLimit(t *testing.T) {
	s, _ := openStore(t, func(l *Limits) { l.MaxAssessmentRuns = 10 })
	ctx := context.Background()
	for i := 0; i < 40; i++ {
		if _, err := s.StartRun(ctx, "scheduled"); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		runs, err := s.Runs(ctx, 0, s.limits.MaxPageSize)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) > 10 {
			t.Fatalf("after run %d there are %d stored runs, over the configured 10", i, len(runs))
		}
	}
	// The horizon keeps the newest runs, so the most recent id survives.
	runs, err := s.Runs(ctx, 0, s.limits.MaxPageSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 10 {
		t.Fatalf("stored runs = %d, want 10", len(runs))
	}
	if runs[0].ID != 40 {
		t.Fatalf("newest run id = %d, want 40: retention must keep the newest", runs[0].ID)
	}
	if runs[len(runs)-1].ID != 31 {
		t.Fatalf("oldest retained run id = %d, want 31", runs[len(runs)-1].ID)
	}
}

// TestRepeatedScansDoNotLeakAssessmentStorage runs many complete scan cycles and
// checks that history stays bounded by the retention limit rather than growing
// without bound.
func TestRepeatedScansDoNotLeakAssessmentStorage(t *testing.T) {
	s, _ := openStore(t, func(l *Limits) {
		l.MaxAssessmentRuns = 25
		l.MaxAssessmentBytes = 16 << 20
	})
	ctx := context.Background()
	for i := 0; i < 60; i++ {
		runID, err := s.StartRun(ctx, "scheduled")
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		observation := assessment.Observation{
			ID: "collector-" + fmt.Sprint(i), Collector: "collector-" + fmt.Sprint(i), Platform: "ubuntu",
			CollectedAt: time.Now(), Status: assessment.Collected, Source: "test",
		}
		if err := s.SaveObservations(ctx, runID, []assessment.Observation{observation}); err != nil {
			t.Fatalf("observations %d: %v", i, err)
		}
		if err := s.FinishRun(ctx, runID, assessment.ScanOutcome{Status: "completed", StartedAt: time.Now(), FinishedAt: time.Now(), ChecksTotal: 1, Passed: 1}); err != nil {
			t.Fatalf("finish %d: %v", i, err)
		}
	}
	runs, err := s.Runs(ctx, 0, s.limits.MaxPageSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 25 {
		t.Fatalf("stored runs = %d, want exactly the retained 25", len(runs))
	}
	usage, err := s.StorageUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usage[usageAssessment] > s.limits.MaxAssessmentBytes {
		t.Fatalf("assessment bytes = %d, over the %d budget", usage[usageAssessment], s.limits.MaxAssessmentBytes)
	}
	if usage[usageMonitoring] != 0 {
		t.Fatalf("assessment history charged %d bytes to monitoring", usage[usageMonitoring])
	}
}
