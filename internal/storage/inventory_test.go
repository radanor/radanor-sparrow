package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/inventory"
)

// CE-6 inventory persistence tests.
//
// The inventory has three independent guarantees and each one is tested here:
// it is a bounded current-state snapshot rather than history, it can never be
// charged against the budget reserved for monitoring events, and its recorded
// coverage always matches what is actually stored.

func inventoryPart(kind inventory.Kind, items []inventory.Item) inventory.Snapshot {
	return inventory.Plan(inventory.Collection{Kind: kind, Items: items, Total: len(items), Source: "test-source"}, 0)
}

// clippedPart is a kind whose collector reported that its own reading was cut
// short, while every record it did observe was handed over intact.
func clippedPart(kind inventory.Kind, items []inventory.Item, note string) inventory.Snapshot {
	return inventory.Plan(inventory.Collection{
		Kind: kind, Items: items, Total: len(items), Source: "test-source",
		Note: note, Truncated: true,
	}, 0)
}

func testInventorySnapshot(observedAt time.Time, parts ...inventory.Snapshot) inventory.Snapshot {
	snap := inventory.Snapshot{ObservedAt: observedAt.UnixNano()}
	for _, part := range parts {
		snap.Merge(part)
	}
	return snap
}

func softwareItem(name, version string) inventory.Item {
	return inventory.Item{Kind: inventory.KindSoftware, Name: name, Version: version, Source: "dpkg", Fields: map[string]string{"origin": "debian-package"}}
}

func endpointItem(name string, fields map[string]string) inventory.Item {
	return inventory.Item{Kind: inventory.KindEndpoint, Name: name, Source: "ss", Fields: fields}
}

func serviceItem(name string) inventory.Item {
	return inventory.Item{Kind: inventory.KindService, Name: name, Source: "systemd", Fields: map[string]string{"active": "active", "sub": "running"}}
}

func TestReplaceInventoryStoresItemsAndCoverage(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	observed := time.Now()

	snap := testInventorySnapshot(observed,
		inventoryPart(inventory.KindSoftware, []inventory.Item{softwareItem("openssh-server", "1:9.6p1"), softwareItem("bash", "5.2.21")}),
		inventoryPart(inventory.KindEndpoint, []inventory.Item{endpointItem("tcp://0.0.0.0:22", map[string]string{"transport": "tcp", "address": "0.0.0.0", "port": "22", "state": "listening", "attribution": "unknown"})}),
		inventoryPart(inventory.KindService, []inventory.Item{serviceItem("ssh.service")}),
	)
	if err := s.ReplaceInventory(ctx, snap); err != nil {
		t.Fatalf("replace: %v", err)
	}

	// Items come back sorted by name so the pagination cursor is stable.
	items, total, err := s.InventoryItems(ctx, InventoryQuery{Kind: inventory.KindSoftware, Limit: 10})
	if err != nil {
		t.Fatalf("items: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("software items = %d (total %d), want 2", len(items), total)
	}
	if items[0].Name != "bash" || items[1].Name != "openssh-server" {
		t.Fatalf("items not ordered by name: %v", []string{items[0].Name, items[1].Name})
	}
	if items[1].Version != "1:9.6p1" || items[1].Source != "dpkg" {
		t.Fatalf("version/source not preserved: %+v", items[1])
	}
	if items[1].Fields["origin"] != "debian-package" {
		t.Fatalf("fields not round-tripped: %+v", items[1].Fields)
	}
	if !items[1].ObservedAt.Equal(observed.Truncate(time.Nanosecond)) && items[1].ObservedAt.UnixNano() != snap.ObservedAt {
		t.Fatalf("observed time = %v, want %v", items[1].ObservedAt, observed)
	}

	status, err := s.InventoryStatus(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !status.HasSnapshot || len(status.Snapshots) != 3 {
		t.Fatalf("coverage records = %d, want 3", len(status.Snapshots))
	}
	if status.SoftwareCount != 2 || status.EndpointCount != 1 || status.ServiceCount != 1 {
		t.Fatalf("counts = %d/%d/%d, want 2/1/1", status.SoftwareCount, status.EndpointCount, status.ServiceCount)
	}
	if status.StoredBytes <= 0 {
		t.Fatalf("stored bytes = %d, want > 0", status.StoredBytes)
	}
	if status.MaxBytes != s.limits.MaxInventoryBytes || status.MaxItems != s.limits.MaxInventoryItems {
		t.Fatalf("status did not report the configured limits: %+v", status)
	}
	if len(status.Limitations) == 0 {
		t.Fatal("status must disclose the inventory's limitations")
	}
}

func TestInventoryReplaceIsPerKindAndDoesNotAccumulate(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()

	first := testInventorySnapshot(time.Now(),
		inventoryPart(inventory.KindSoftware, []inventory.Item{softwareItem("bash", "5.2")}),
		inventoryPart(inventory.KindEndpoint, []inventory.Item{endpointItem("tcp://127.0.0.1:22", map[string]string{"transport": "tcp", "port": "22"})}),
		inventoryPart(inventory.KindService, []inventory.Item{serviceItem("ssh.service")}),
	)
	if err := s.ReplaceInventory(ctx, first); err != nil {
		t.Fatalf("first replace: %v", err)
	}
	bytesAfterFirst, err := s.inventoryBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// A refresh that only covered software must not blank the kinds it did not
	// cover: a partial collection is not evidence that endpoints disappeared.
	second := testInventorySnapshot(time.Now(), inventoryPart(inventory.KindSoftware, []inventory.Item{softwareItem("bash", "5.2"), softwareItem("curl", "8.5")}))
	if err := s.ReplaceInventory(ctx, second); err != nil {
		t.Fatalf("second replace: %v", err)
	}
	if _, total, err := s.InventoryItems(ctx, InventoryQuery{Kind: inventory.KindSoftware, Limit: 10}); err != nil || total != 2 {
		t.Fatalf("software after partial refresh = %d (err %v), want 2", total, err)
	}
	if _, total, err := s.InventoryItems(ctx, InventoryQuery{Kind: inventory.KindEndpoint, Limit: 10}); err != nil || total != 1 {
		t.Fatalf("endpoints after partial refresh = %d (err %v), want 1 (uncovered kinds are kept)", total, err)
	}
	if _, total, err := s.InventoryItems(ctx, InventoryQuery{Kind: inventory.KindService, Limit: 10}); err != nil || total != 1 {
		t.Fatalf("services after partial refresh = %d (err %v), want 1", total, err)
	}

	// Replacement is not accumulation: repeating the same snapshot must not grow
	// the stored bytes.
	for i := 0; i < 4; i++ {
		if err := s.ReplaceInventory(ctx, second); err != nil {
			t.Fatalf("repeat replace %d: %v", i, err)
		}
	}
	bytesAfterRepeat, err := s.inventoryBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bytesAfterRepeat > bytesAfterFirst*2 {
		t.Fatalf("repeated replacement grew the snapshot from %d to %d bytes", bytesAfterFirst, bytesAfterRepeat)
	}
	if n := countRows(t, s, "SELECT count(*) FROM inventory_items WHERE kind=?", string(inventory.KindSoftware)); n != 2 {
		t.Fatalf("software rows = %d after repeated replacement, want 2", n)
	}
}

func TestInventorySearchIsBoundedLiteralAndPrefixMatched(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	if err := s.ReplaceInventory(ctx, testInventorySnapshot(time.Now(), inventoryPart(inventory.KindSoftware, []inventory.Item{
		softwareItem("openssh-server", "1"), softwareItem("bash", "2"), softwareItem("perl", "3"), softwareItem("a_b", "4"),
	}))); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		search string
		want   int
	}{
		{"open", 1},
		{"OPEN", 1},   // case-insensitive
		{"%", 0},      // a LIKE wildcard must be matched literally
		{"_", 0},      // so must the single-character wildcard
		{"a_", 1},     // and the escape must still match the real name
		{"\u0000", 0}, // no panic on unusual input
	}
	for _, tc := range cases {
		_, total, err := s.InventoryItems(ctx, InventoryQuery{Kind: inventory.KindSoftware, Search: tc.search, Limit: 10})
		if err != nil {
			t.Fatalf("search %q: %v", tc.search, err)
		}
		if total != tc.want {
			t.Fatalf("search %q matched %d, want %d", tc.search, total, tc.want)
		}
	}

	if _, _, err := s.InventoryItems(ctx, InventoryQuery{Kind: inventory.KindSoftware, Search: strings.Repeat("a", 129), Limit: 10}); err == nil {
		t.Fatal("an oversized search term must be refused")
	}
	if _, _, err := s.InventoryItems(ctx, InventoryQuery{Kind: inventory.Kind("bogus"), Limit: 10}); err == nil {
		t.Fatal("an unknown kind must be refused")
	}
	if _, _, err := s.InventoryItems(ctx, InventoryQuery{Kind: inventory.KindSoftware, Limit: s.limits.MaxPageSize + 1}); err == nil {
		t.Fatal("a page larger than the configured maximum must be refused")
	}
	if _, _, err := s.InventoryItems(ctx, InventoryQuery{Kind: inventory.KindSoftware, Limit: -1}); err == nil {
		t.Fatal("a negative limit must be refused")
	}
}

func TestInventoryPaginationCursorIsStable(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	items := make([]inventory.Item, 0, 7)
	for i := 0; i < 7; i++ {
		items = append(items, softwareItem(fmt.Sprintf("pkg-%02d", i), "1"))
	}
	if err := s.ReplaceInventory(ctx, testInventorySnapshot(time.Now(), inventoryPart(inventory.KindSoftware, items))); err != nil {
		t.Fatal(err)
	}
	seen := []string{}
	after := ""
	for page := 0; page < 4; page++ {
		records, total, err := s.InventoryItems(ctx, InventoryQuery{Kind: inventory.KindSoftware, After: after, Limit: 3})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if total != 7 {
			t.Fatalf("total = %d, want 7 on every page", total)
		}
		if len(records) == 0 {
			break
		}
		for _, record := range records {
			seen = append(seen, record.Name)
		}
		after = records[len(records)-1].Name
	}
	if len(seen) != 7 {
		t.Fatalf("paging returned %d items, want 7: %v", len(seen), seen)
	}
	for i, name := range seen {
		if name != fmt.Sprintf("pkg-%02d", i) {
			t.Fatalf("paging order wrong at %d: %v", i, seen)
		}
	}
}

// TestInventoryCannotConsumeMonitoringBudget is the CE-6 storage hardening
// invariant: the inventory has its own byte budget and is measured on its own
// tables, so no amount of inventory can push monitoring ingestion into pressure.
func TestInventoryCannotConsumeMonitoringBudget(t *testing.T) {
	s, _ := openStore(t, func(l *Limits) {
		l.MaxBytes = 4096 // a deliberately tiny monitoring budget
		l.MaxInventoryBytes = 8 << 20
	})
	ctx := context.Background()

	items := make([]inventory.Item, 0, 200)
	for i := 0; i < 200; i++ {
		items = append(items, softwareItem(fmt.Sprintf("pkg-%04d", i), strings.Repeat("9", 64)))
	}
	if err := s.ReplaceInventory(ctx, testInventorySnapshot(time.Now(), inventoryPart(inventory.KindSoftware, items))); err != nil {
		t.Fatalf("inventory replace: %v", err)
	}
	usage, err := s.StorageUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usage[usageInventory] <= 0 {
		t.Fatalf("inventory bytes = %d, want > 0", usage[usageInventory])
	}
	if usage[usageMonitoring] != 0 {
		t.Fatalf("inventory charged %d bytes to the monitoring budget", usage[usageMonitoring])
	}

	// Monitoring ingestion still has its full budget available.
	var accepted int
	for i := 0; i < 100; i++ {
		err := s.Process(ctx, testEvent(fmt.Sprintf("e%d", i), "native.file_integrity", nil))
		if err == nil {
			accepted++
			continue
		}
		if !errors.Is(err, ErrStoragePressure) {
			t.Fatalf("unexpected ingestion error: %v", err)
		}
		break
	}
	if accepted == 0 {
		t.Fatal("monitoring ingestion was blocked by the inventory")
	}

	// And exhausting the monitoring budget leaves the inventory untouched.
	usageAfter, err := s.StorageUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usageAfter[usageInventory] != usage[usageInventory] {
		t.Fatalf("inventory bytes changed with monitoring pressure: %d -> %d", usage[usageInventory], usageAfter[usageInventory])
	}
	if n := countRows(t, s, "SELECT count(*) FROM inventory_items"); n != 200 {
		t.Fatalf("inventory rows = %d after monitoring pressure, want 200", n)
	}
	if err := s.ReplaceInventory(ctx, testInventorySnapshot(time.Now(), inventoryPart(inventory.KindSoftware, items))); err != nil {
		t.Fatalf("inventory replace under monitoring pressure: %v", err)
	}
}

// TestInventoryPruningKeepsCoverageHonest checks that when the inventory budget
// forces entries out, the recorded coverage says so rather than continuing to
// claim the pruned entries are stored.
func TestInventoryPruningKeepsCoverageHonest(t *testing.T) {
	s, _ := openStore(t, func(l *Limits) { l.MaxInventoryBytes = 4096 })
	ctx := context.Background()

	items := make([]inventory.Item, 0, 300)
	for i := 0; i < 300; i++ {
		items = append(items, softwareItem(fmt.Sprintf("pkg-%04d", i), strings.Repeat("7", 40)))
	}
	snap := testInventorySnapshot(time.Now(), inventoryPart(inventory.KindSoftware, items))
	err := s.ReplaceInventory(ctx, snap)
	if err != nil && !errors.Is(err, ErrInventoryPressure) {
		t.Fatalf("replace: %v", err)
	}

	used, err := s.inventoryBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if used > s.limits.MaxInventoryBytes {
		t.Fatalf("stored inventory bytes = %d, exceeds the %d byte budget", used, s.limits.MaxInventoryBytes)
	}
	status, err := s.InventoryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if errors.Is(err, ErrInventoryPressure) {
		return
	}
	stored := countRows(t, s, "SELECT count(*) FROM inventory_items")
	record := status.Snapshots[0]
	if record.Stored != stored {
		t.Fatalf("coverage claims %d stored entries but the store holds %d", record.Stored, stored)
	}
	if stored < len(items) && !record.Truncated {
		t.Fatalf("entries were pruned but truncation was not disclosed: %+v", record)
	}
	// Pressure is no longer merely "the byte budget is full". Retention can
	// satisfy the budget by deleting rows — in this test the prune removes every
	// row of the kind and the meter drops to zero — and that must still be
	// reported, because a kind showing nothing while 300 entries were observed is
	// the opposite of an empty, complete inventory. This is the L-2 correction.
	retentionEmptied := record.Stored == 0 && record.Total > 0
	wantPressure := used >= s.limits.MaxInventoryBytes || retentionEmptied
	if status.Pressure != wantPressure {
		t.Fatalf("pressure flag %v does not match usage %d of %d (retention emptied the kind: %v)",
			status.Pressure, used, s.limits.MaxInventoryBytes, retentionEmptied)
	}
	if retentionEmptied {
		if !record.Truncated {
			t.Fatalf("retention removed every stored row but the kind does not report truncation: %+v", record)
		}
		named := false
		for _, limitation := range status.Limitations {
			if strings.HasPrefix(limitation, string(inventory.KindSoftware)+": retention removed every stored record") {
				named = true
			}
		}
		if !named {
			t.Fatalf("retention emptied the software kind but no limitation names it: %v", status.Limitations)
		}
	}
}

// TestClippedReadingPersistsAsTruncated is the store half of CE-6 H-1. The
// interesting case is the one that used to be invisible: the collector's listing
// was clipped, every record it observed was stored, and no cap applied — so
// observed equals stored and no count comparison can detect the clip. The store
// must still record the kind as truncated, and the disclosure must survive into
// the status the UI reads.
func TestClippedReadingPersistsAsTruncated(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	items := []inventory.Item{softwareItem("bash", "5.2.21"), softwareItem("curl", "8.5.0")}
	snap := testInventorySnapshot(time.Now(), clippedPart(inventory.KindSoftware, items, "the listing was cut off at the collection output limit"))
	if err := s.ReplaceInventory(ctx, snap); err != nil {
		t.Fatalf("replace: %v", err)
	}

	status, err := s.InventoryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Snapshots) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(status.Snapshots))
	}
	record := status.Snapshots[0]
	if record.Total != len(items) || record.Stored != len(items) {
		t.Fatalf("total/stored = %d/%d, want %d/%d so the counts agree and cannot be the signal",
			record.Total, record.Stored, len(items), len(items))
	}
	if !record.Truncated {
		t.Fatal("a clipped reading whose observed count equals its stored count was stored as complete")
	}
	if !strings.Contains(record.Note, "cut off at the collection output limit") {
		t.Fatalf("note = %q, want the clip disclosure preserved for the UI", record.Note)
	}

	// Losing the snapshot must not lose the disclosure either.
	second := testInventorySnapshot(time.Now(), clippedPart(inventory.KindSoftware, items, "the listing was cut off at the collection output limit"))
	if err := s.ReplaceInventory(ctx, second); err != nil {
		t.Fatalf("second replace: %v", err)
	}
	status, err = s.InventoryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Snapshots[0].Truncated {
		t.Fatal("the truncation mark was cleared by a later replace of the same kind")
	}

	// The unclipped case must stay unmarked, so the flag is attributable.
	plain := testInventorySnapshot(time.Now(), inventoryPart(inventory.KindEndpoint, itemsOfKind(inventory.KindEndpoint, "tcp://0.0.0.0:22")))
	if err := s.ReplaceInventory(ctx, plain); err != nil {
		t.Fatalf("plain replace: %v", err)
	}
	status, err = s.InventoryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range status.Snapshots {
		if r.Kind == inventory.KindEndpoint && r.Truncated {
			t.Fatalf("an unclipped kind was stored as truncated: %+v", r)
		}
	}
}

func itemsOfKind(kind inventory.Kind, names ...string) []inventory.Item {
	items := make([]inventory.Item, 0, len(names))
	for _, name := range names {
		items = append(items, inventory.Item{Kind: kind, Name: name, Source: "ss"})
	}
	return items
}

// TestInventoryItemCapIsEnforced proves the per-kind cap the settings page
// reports is the one actually applied.
func TestInventoryItemCapIsEnforced(t *testing.T) {
	s, _ := openStore(t, func(l *Limits) { l.MaxInventoryItems = 5 })
	ctx := context.Background()
	items := make([]inventory.Item, 0, 20)
	for i := 0; i < 20; i++ {
		items = append(items, softwareItem(fmt.Sprintf("pkg-%02d", i), "1"))
	}
	if err := s.ReplaceInventory(ctx, testInventorySnapshot(time.Now(), inventoryPart(inventory.KindSoftware, items))); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if n := countRows(t, s, "SELECT count(*) FROM inventory_items"); n != 5 {
		t.Fatalf("stored items = %d, want the configured cap of 5", n)
	}
	status, err := s.InventoryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Snapshots) != 1 || status.Snapshots[0].Stored != 5 || !status.Snapshots[0].Truncated {
		t.Fatalf("coverage did not disclose the cap: %+v", status.Snapshots)
	}
	if status.Snapshots[0].Total != 20 {
		t.Fatalf("observed total = %d, want 20", status.Snapshots[0].Total)
	}
}

func TestInventoryStatusBeforeAnySnapshot(t *testing.T) {
	s, _ := openStore(t, nil)
	status, err := s.InventoryStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.HasSnapshot || len(status.Snapshots) != 0 {
		t.Fatalf("empty store reported a snapshot: %+v", status)
	}
	if status.StoredBytes != 0 || status.Pressure {
		t.Fatalf("empty store reported usage: %+v", status)
	}
	// An empty result must not be reported as a healthy, complete inventory.
	if len(status.Limitations) == 0 {
		t.Fatal("status must disclose limitations even with no snapshot")
	}
}

// TestInventoryCoverageCountsRowsNotObservations is the CE-6 honesty regression
// found in a real end-to-end run: the socket collector can observe the same
// endpoint more than once (several sockets bound to one address:port), and those
// observations collapse into a single stored row. The coverage record must report
// what is actually stored, not what was handed over, or the dashboard claims
// entries the store does not hold.
func TestInventoryCoverageCountsRowsNotObservations(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	duplicate := endpointItem("tcp://0.0.0.0:22", map[string]string{"transport": "tcp", "address": "0.0.0.0", "port": "22"})
	snap := testInventorySnapshot(time.Now(), inventoryPart(inventory.KindEndpoint, []inventory.Item{
		duplicate, duplicate, duplicate, endpointItem("tcp://127.0.0.1:631", map[string]string{"transport": "tcp", "port": "631"}),
	}))
	if err := s.ReplaceInventory(ctx, snap); err != nil {
		t.Fatalf("replace: %v", err)
	}
	status, err := s.InventoryStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Snapshots) != 1 {
		t.Fatalf("coverage records = %d, want 1", len(status.Snapshots))
	}
	record := status.Snapshots[0]
	rows := countRows(t, s, "SELECT count(*) FROM inventory_items WHERE kind=?", string(inventory.KindEndpoint))
	if record.Stored != rows || record.Stored != 2 {
		t.Fatalf("coverage claims %d stored, the store holds %d (want 2 distinct endpoints)", record.Stored, rows)
	}
	if record.Total != 4 {
		t.Fatalf("observed total = %d, want the 4 observations handed over", record.Total)
	}
	// Collapsing duplicates is disclosed, because fewer rows are stored than were
	// observed.
	if !record.Truncated {
		t.Fatal("collapsing duplicate observations is not disclosed as truncation")
	}
	// And the page agrees with the coverage record.
	_, total, err := s.InventoryItems(ctx, InventoryQuery{Kind: inventory.KindEndpoint, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != record.Stored {
		t.Fatalf("page total %d disagrees with the coverage record %d", total, record.Stored)
	}
}

func TestInventorySourcesAreScopedToTheKind(t *testing.T) {
	s, _ := openStore(t, nil)
	ctx := context.Background()
	if err := s.ReplaceInventory(ctx, testInventorySnapshot(time.Now(),
		inventoryPart(inventory.KindSoftware, []inventory.Item{softwareItem("bash", "1")}),
		inventoryPart(inventory.KindService, []inventory.Item{serviceItem("ssh.service")}),
	)); err != nil {
		t.Fatal(err)
	}
	sources, err := s.InventorySources(ctx, inventory.KindSoftware)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0] != "dpkg" {
		t.Fatalf("software sources = %v, want [dpkg]", sources)
	}
	if _, err := s.InventorySources(ctx, inventory.Kind("bogus")); err == nil {
		t.Fatal("an unknown kind must be refused")
	}
}
