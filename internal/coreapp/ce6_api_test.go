package coreapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
	"github.com/radanor/radanor-shield/internal/collectors"
	"github.com/radanor/radanor-shield/internal/inventory"
	"github.com/radanor/radanor-shield/internal/ipc"
	"github.com/radanor/radanor-shield/internal/storage"
)

// CE-6 core API tests. They cover the three things that make the inventory and
// settings operations safe: a fixed allowlist of operations and fields, bounded
// inputs, and a refusal that never half-applies.

// stubCollector is a deterministic test collector. It is not registered by any
// production source.
type stubCollector struct {
	kind      inventory.Kind
	status    inventory.Status
	items     []inventory.Item
	source    string
	note      string
	failure   string
	truncated bool
	calls     int
}

func (c *stubCollector) Kind() inventory.Kind { return c.kind }

func (c *stubCollector) CollectInventory(context.Context) inventory.Collection {
	c.calls++
	if c.status != "" && c.status != inventory.Collected {
		return inventory.Collection{Kind: c.kind, Status: c.status, Source: c.source, Error: c.failure, Truncated: c.truncated}
	}
	return inventory.Collection{Kind: c.kind, Status: inventory.Collected, Items: c.items, Total: len(c.items), Source: c.source, Note: c.note, Error: c.failure, Truncated: c.truncated}
}

func newInventoryAPI(t *testing.T, collectors ...inventory.Collector) (*inventoryAPI, *storage.Store) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "inventory.db"), storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &inventoryAPI{store: store, collectors: collectors, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, store
}

func TestInventoryPaginationPreservesSameNameVersions(t *testing.T) {
	collector := &stubCollector{kind: inventory.KindSoftware, source: "test", items: []inventory.Item{
		{Kind: inventory.KindSoftware, Name: "!rs1:widget", Version: "1", Source: "test"},
		{Kind: inventory.KindSoftware, Name: "b", Version: "1", Source: "test"},
		{Kind: inventory.KindSoftware, Name: "b", Version: "2", Source: "test"},
		{Kind: inventory.KindSoftware, Name: "b", Version: "2", Source: "other"},
		{Kind: inventory.KindSoftware, Name: "c", Version: "1", Source: "test"},
	}}
	api, _ := newInventoryAPI(t, collector)
	if err := api.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	var legacy struct {
		Items []storage.ItemRecord `json:"items"`
	}
	decodeValue(t, api.items(`{"after":"!rs1:widget","limit":50}`, inventory.KindSoftware), &legacy)
	if len(legacy.Items) != 4 {
		t.Fatalf("legacy prefix pagination returned %d, want 4", len(legacy.Items))
	}
	prefix := base64.RawURLEncoding.EncodeToString([]byte("!rs1:widget"))
	decodeValue(t, api.items(`{"after_b64":"`+prefix+`","limit":50}`, inventory.KindSoftware), &legacy)
	if len(legacy.Items) != 4 {
		t.Fatal("encoded legacy cursor changed pagination")
	}
	encoded := base64.RawURLEncoding.EncodeToString([]byte("!rs1:widget"))
	decodeValue(t, api.items(`{"search_b64":"`+encoded+`","source_b64":"dGVzdA","limit":50}`, inventory.KindSoftware), &legacy)
	if len(legacy.Items) != 1 || legacy.Items[0].Name != "!rs1:widget" {
		t.Fatal("encoded filters changed inventory query")
	}
	for _, raw := range []string{`{"search_b64":"!"}`, `{"search":"widget","search_b64":"d2lkZ2V0"}`, `{"after":"` + inventory.EncodeCursor("b", "1", "test") + `!"}`} {
		if api.items(raw, inventory.KindSoftware).OK {
			t.Fatalf("invalid IPC query accepted: %s", raw)
		}
	}
	after := ""
	seen := map[string]bool{}
	for pages := 0; pages < 10; pages++ {
		query, err := json.Marshal(map[string]any{"after": after, "limit": 2})
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Items []storage.ItemRecord `json:"items"`
			Next  string               `json:"next_after"`
			Total int                  `json:"total"`
		}
		decodeValue(t, api.items(string(query), inventory.KindSoftware), &page)
		if page.Total != 5 || len(page.Items) > 2 {
			t.Fatalf("invalid page: %+v", page)
		}
		for _, item := range page.Items {
			key := item.Name + "\x00" + item.Version + "\x00" + item.Source
			if seen[key] {
				t.Fatalf("duplicate paginated item: %q", key)
			}
			seen[key] = true
		}
		if page.Next == "" {
			if len(seen) != 5 {
				t.Fatalf("pagination lost same-name versions or sources: got %d of 5", len(seen))
			}
			return
		}
		if page.Next == after {
			t.Fatal("pagination cursor did not advance")
		}
		after = page.Next
	}
	t.Fatal("pagination did not terminate")
}

func decodeValue(t *testing.T, resp ipc.Response, target any) {
	t.Helper()
	if !resp.OK {
		t.Fatalf("operation failed: %s", resp.Error)
	}
	if err := json.Unmarshal([]byte(resp.Value), target); err != nil {
		t.Fatalf("decode %q: %v", resp.Value, err)
	}
}

func staleWait(t *testing.T, api *inventoryAPI) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var status struct {
			Refreshing bool              `json:"refreshing"`
			Failures   map[string]string `json:"failures"`
			LastError  string            `json:"last_error"`
		}
		decodeValue(t, api.handle(ipc.Request{Operation: "inventory_status"}), &status)
		if !status.Refreshing {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the inventory refresh did not finish")
}

func TestInventoryAPIDispatchAndValidation(t *testing.T) {
	api, _ := newInventoryAPI(t)
	if resp := api.handle(ipc.Request{Operation: "inventory_nonsense"}); resp.OK || resp.Error == "" {
		t.Fatalf("unknown operation = %+v, want a refusal", resp)
	}
	// Unexpected fields must be refused, not ignored.
	for _, value := range []string{`{"target":"/etc"}`, `{"kind":"software"}`, `{"path":"/var/lib/dpkg/status"}`} {
		if resp := api.handle(ipc.Request{Operation: "inventory_software", Value: value}); resp.OK {
			t.Fatalf("request %s must be refused", value)
		}
	}
	if resp := api.handle(ipc.Request{Operation: "inventory_status", Value: `{"extra":1}`}); resp.OK {
		t.Fatal("a status request with a field must be refused")
	}
	// A cursor longer than the largest storable name is refused outright.
	long := strings.Repeat("a", inventory.MaxNameLen+1)
	if resp := api.handle(ipc.Request{Operation: "inventory_software", Value: `{"after":"` + long + `"}`}); resp.OK {
		t.Fatal("an oversized cursor must be refused")
	}
	if resp := api.handle(ipc.Request{Operation: "inventory_software", Value: `{"limit":9999}`}); resp.OK {
		t.Fatal("a page beyond the maximum must be refused")
	}
	// An oversized raw value is refused before decoding.
	if resp := api.handle(ipc.Request{Operation: "inventory_refresh", Value: strings.Repeat("x", 2000)}); resp.OK {
		t.Fatal("an oversized request must be refused")
	}
	if resp := api.handle(ipc.Request{Operation: "inventory_refresh", Value: `{"force":true}`}); resp.OK {
		t.Fatal("a refresh request with a field must be refused")
	}
}

func TestInventoryAPIRefreshStoresAndReportsCoverage(t *testing.T) {
	software := &stubCollector{kind: inventory.KindSoftware, status: inventory.Collected, source: "stub-dpkg",
		items: []inventory.Item{{Kind: inventory.KindSoftware, Name: "bash", Version: "5.2", Source: "stub-dpkg"}}}
	endpoints := &stubCollector{kind: inventory.KindEndpoint, status: inventory.Collected, source: "stub-ss",
		items: []inventory.Item{{Kind: inventory.KindEndpoint, Name: "tcp://127.0.0.1:22", Source: "stub-ss", Fields: map[string]string{"transport": "tcp", "address": "127.0.0.1", "port": "22"}}}}
	api, _ := newInventoryAPI(t, software, endpoints)

	// Before any refresh the page must report that nothing has been collected
	// rather than presenting an empty-but-healthy inventory.
	var before struct {
		HasSnapshot bool `json:"has_snapshot"`
		Limitations []string
	}
	decodeValue(t, api.handle(ipc.Request{Operation: "inventory_status"}), &before)
	if before.HasSnapshot || len(before.Limitations) == 0 {
		t.Fatalf("empty status = %+v, want no snapshot and disclosed limitations", before)
	}

	if resp := api.handle(ipc.Request{Operation: "inventory_refresh"}); !resp.OK {
		t.Fatalf("refresh refused: %s", resp.Error)
	}
	staleWait(t, api)

	var page struct {
		Schema  string               `json:"schema"`
		Kind    string               `json:"kind"`
		Items   []storage.ItemRecord `json:"items"`
		Total   int                  `json:"total"`
		Sources []string             `json:"sources"`
	}
	decodeValue(t, api.handle(ipc.Request{Operation: "inventory_software"}), &page)
	if page.Schema != inventoryItemsSchema || page.Kind != "software" {
		t.Fatalf("page envelope = %+v", page)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Name != "bash" {
		t.Fatalf("software page = %+v", page)
	}
	if len(page.Sources) != 1 || page.Sources[0] != "stub-dpkg" {
		t.Fatalf("sources = %v, want [stub-dpkg]", page.Sources)
	}
	decodeValue(t, api.handle(ipc.Request{Operation: "inventory_endpoints"}), &page)
	if page.Total != 1 || page.Items[0].Fields["port"] != "22" {
		t.Fatalf("endpoint page = %+v", page)
	}
	// A kind with no collector has nothing recorded and must say so.
	if resp := api.handle(ipc.Request{Operation: "inventory_services"}); !resp.OK {
		t.Fatalf("services read refused: %s", resp.Error)
	} else {
		var empty struct {
			Total int `json:"total"`
		}
		decodeValue(t, resp, &empty)
		if empty.Total != 0 {
			t.Fatalf("services total = %d, want 0", empty.Total)
		}
	}
}

func TestInventoryAPIRefreshIsRateLimited(t *testing.T) {
	software := &stubCollector{kind: inventory.KindSoftware, status: inventory.Collected, source: "stub",
		items: []inventory.Item{{Kind: inventory.KindSoftware, Name: "bash", Source: "stub"}}}
	api, _ := newInventoryAPI(t, software)
	if resp := api.handle(ipc.Request{Operation: "inventory_refresh"}); !resp.OK {
		t.Fatalf("first refresh refused: %s", resp.Error)
	}
	staleWait(t, api)
	resp := api.handle(ipc.Request{Operation: "inventory_refresh"})
	if resp.OK || !strings.HasPrefix(resp.Error, "invalid ") {
		t.Fatalf("second refresh = %+v, want a rate-limit refusal", resp)
	}
	if software.calls != 1 {
		t.Fatalf("collector ran %d times, want 1", software.calls)
	}
}

func TestInventoryAPIRefreshReportsAnUnsupportedKind(t *testing.T) {
	good := &stubCollector{kind: inventory.KindSoftware, status: inventory.Collected, source: "stub",
		items: []inventory.Item{{Kind: inventory.KindSoftware, Name: "bash", Source: "stub"}}}
	missing := &stubCollector{kind: inventory.KindEndpoint, status: inventory.Unsupported, source: "stub", failure: "ss is not available on this system"}
	api, _ := newInventoryAPI(t, good, missing)
	if resp := api.handle(ipc.Request{Operation: "inventory_refresh"}); !resp.OK {
		t.Fatalf("refresh refused: %s", resp.Error)
	}
	staleWait(t, api)

	var status struct {
		Failures map[string]string `json:"failures"`
	}
	decodeValue(t, api.handle(ipc.Request{Operation: "inventory_status"}), &status)
	if status.Failures["endpoint"] == "" {
		t.Fatalf("an unsupported collection must be reported as a failure: %+v", status.Failures)
	}
	var page struct {
		Total int `json:"total"`
	}
	decodeValue(t, api.handle(ipc.Request{Operation: "inventory_software"}), &page)
	if page.Total != 1 {
		t.Fatalf("the kind that was collected must still be stored, total = %d", page.Total)
	}
}

func TestInventoryAPIRefreshFailureIsReportedNotSwallowed(t *testing.T) {
	broken := &stubCollector{kind: inventory.KindSoftware, status: inventory.PermissionDenied, source: "stub", failure: "reading the package database requires elevated privileges"}
	api, _ := newInventoryAPI(t, broken)
	if resp := api.handle(ipc.Request{Operation: "inventory_refresh"}); !resp.OK {
		t.Fatalf("refresh refused: %s", resp.Error)
	}
	staleWait(t, api)
	var status struct {
		Failures  map[string]string `json:"failures"`
		LastError string            `json:"last_error"`
	}
	decodeValue(t, api.handle(ipc.Request{Operation: "inventory_status"}), &status)
	if !strings.Contains(status.Failures["software"], "elevated privileges") {
		t.Fatalf("failures = %+v, want the collector's own explanation", status.Failures)
	}
	if status.LastError == "" {
		t.Fatal("a refresh that stored nothing must record a last error")
	}
}

// TestInventoryAPIRecordsAClippedReadingAsTruncated is the H-1 guard at the IPC
// boundary. The collector hands over every record it observed and reports that
// its own reading was clipped, so the observed count equals the stored count and
// no cap applied. The status the UI reads must still say the kind is truncated,
// and the clip must appear among the reported failures rather than being lost.
func TestInventoryAPIRecordsAClippedReadingAsTruncated(t *testing.T) {
	clipped := &stubCollector{
		kind: inventory.KindSoftware, status: inventory.Collected, source: "dpkg", truncated: true,
		failure: "the package database listing produced more output than the capture limit of 8192 KiB, so this is a partial reading",
		items: []inventory.Item{
			{Kind: inventory.KindSoftware, Name: "bash", Source: "dpkg"},
			{Kind: inventory.KindSoftware, Name: "curl", Source: "dpkg"},
		},
	}
	api, _ := newInventoryAPI(t, clipped)
	if resp := api.handle(ipc.Request{Operation: "inventory_refresh"}); !resp.OK {
		t.Fatalf("refresh refused: %s", resp.Error)
	}
	staleWait(t, api)

	var status struct {
		Snapshots []struct {
			Kind      string `json:"kind"`
			Total     int    `json:"total"`
			Stored    int    `json:"stored"`
			Truncated bool   `json:"truncated"`
			Note      string `json:"note"`
		} `json:"snapshots"`
		Failures map[string]string `json:"failures"`
	}
	decodeValue(t, api.handle(ipc.Request{Operation: "inventory_status"}), &status)
	if len(status.Snapshots) != 1 {
		t.Fatalf("snapshots = %+v, want the one collected kind", status.Snapshots)
	}
	snap := status.Snapshots[0]
	if snap.Total != 2 || snap.Stored != 2 {
		t.Fatalf("total/stored = %d/%d, want 2/2 so the counts agree and cannot be the signal", snap.Total, snap.Stored)
	}
	if !snap.Truncated {
		t.Fatal("a clipped reading whose observed count equals its stored count reached the UI as complete")
	}
	if !strings.Contains(snap.Note, "partial") {
		t.Fatalf("note = %q, want the snapshot to disclose that it is partial", snap.Note)
	}
	if !strings.Contains(status.Failures["software"], "partial reading") {
		t.Fatalf("failures = %+v, want the clip reported as a diagnostic", status.Failures)
	}
}

// TestInventoryAPIKeepsTheStoredKindWhenACollectorYieldsNothing pins the rule
// that made a clipped Windows document dangerous: a collector that could not
// interpret its own output must not replace a stored snapshot with an empty one,
// since a zero observed count reads as a complete, empty inventory.
func TestInventoryAPIKeepsTheStoredKindWhenACollectorYieldsNothing(t *testing.T) {
	good := &stubCollector{kind: inventory.KindEndpoint, status: inventory.Collected, source: "ss",
		items: []inventory.Item{{Kind: inventory.KindEndpoint, Name: "tcp://0.0.0.0:22", Source: "ss"}}}
	api, _ := newInventoryAPI(t, good)
	// Refresh is driven directly rather than through the IPC operation: the
	// on-demand refresh is rate-limited to one every ten seconds, and this test
	// is about what a second collection does to the stored snapshot.
	if err := api.Refresh(context.Background()); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	// The next refresh cannot interpret its reading at all: nothing usable and
	// the status is not a success.
	api.collectors = []inventory.Collector{&stubCollector{
		kind: inventory.KindEndpoint, status: inventory.Incomplete, source: "ss", truncated: true,
		failure: "the socket listing produced more output than the capture limit of 8192 KiB, so this is a partial reading",
	}}
	if err := api.Refresh(context.Background()); err == nil {
		t.Fatal("a refresh that stored nothing must report an error")
	}

	var status struct {
		Snapshots []struct {
			Total     int  `json:"total"`
			Stored    int  `json:"stored"`
			Truncated bool `json:"truncated"`
		} `json:"snapshots"`
		Failures map[string]string `json:"failures"`
	}
	decodeValue(t, api.handle(ipc.Request{Operation: "inventory_status"}), &status)
	if len(status.Snapshots) != 1 || status.Snapshots[0].Stored != 1 {
		t.Fatalf("snapshots = %+v, want the previously stored endpoint kept", status.Snapshots)
	}
	if status.Snapshots[0].Total == 0 && status.Snapshots[0].Stored == 0 {
		t.Fatal("a kind with nothing to merge replaced the stored snapshot with an empty, complete-looking one")
	}
	if !strings.Contains(status.Failures["endpoint"], "partial reading") {
		t.Fatalf("failures = %+v, want the unusable reading reported", status.Failures)
	}
}

// startCoreAt mirrors what the core's main function builds at startup: the
// environment-derived scheduler configuration, then the stored preferences
// applied on top of it. It exists so a restart can be simulated against the same
// database file rather than by hand-constructing runtime state, which is where
// the M-1 defect lived.
func startCoreAt(t *testing.T, dbPath string) (*settingsAPI, *assessmentAPI, func()) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := storage.Open(dbPath, storage.DefaultLimits())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	schedCfg, err := schedulerFromEnv()
	if err != nil {
		t.Fatalf("scheduler from environment: %v", err)
	}
	assess := &assessmentAPI{
		scanner:   assessment.NewScanner(store, collectors.PlatformChecks(), collectors.PlatformCollectors()),
		store:     store,
		logger:    logger,
		scheduler: schedCfg,
		reload:    make(chan struct{}, 1),
	}
	loadPersistedSettings(context.Background(), store, assess, logger)
	return &settingsAPI{store: store, assess: assess, logger: logger}, assess, func() { _ = store.Close() }
}

// waitForNextScan starts the scheduler loop and waits for it to publish the next
// scheduled time, then stops the loop. It returns the published value so a test
// can check that it is a real future time rather than a zero timestamp.
//
// It deliberately does not signal a reload: the loop reconfigures itself before
// it starts waiting, and the reload channel is buffered for one pending signal,
// so a settings update in the same test may already have filled it.
func waitForNextScan(t *testing.T, assess *assessmentAPI) time.Time {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go assess.runScheduler(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		assess.mu.Lock()
		next := assess.nextScan
		assess.mu.Unlock()
		if !next.IsZero() {
			return next
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the scheduler never published a next scan time")
	return time.Time{}
}

// TestScheduledEnableSurvivesARestartWithSchedulingDisabled is the M-1
// reproduction, run through the exact sequence the review used: save a 24 hour
// interval, switch scheduling off, restart the core, then enable scheduling with
// only assess_enabled=true.
//
// Before the correction the restart discarded the saved interval from the
// runtime configuration (the persisted value was read but only installed when
// the schedule was already enabled), so the enable request was refused with
// "a scheduled assessment requires an interval of at least one hour" — even
// though a valid interval was stored.
func TestScheduledEnableSurvivesARestartWithSchedulingDisabled(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "core.db")

	// 1. Save a 24 hour interval while scheduling is still on, then 2. switch it
	// off. Both are ordinary accepted updates.
	first, firstAssess, closeFirst := startCoreAt(t, dbPath)
	if resp := first.handle(ipc.Request{Operation: "settings_update", Value: `{"assess_enabled":true,"assess_interval":"24h"}`}); !resp.OK {
		t.Fatalf("saving a 24h interval was refused: %s", resp.Error)
	}
	if cfg := firstAssess.schedulerConfigSnapshot(); !cfg.Enabled || cfg.Interval != 24*time.Hour {
		t.Fatalf("runtime schedule = %+v, want enabled every 24h", cfg)
	}
	if resp := first.handle(ipc.Request{Operation: "settings_update", Value: `{"assess_enabled":false}`}); !resp.OK {
		t.Fatalf("disabling the schedule was refused: %s", resp.Error)
	}
	closeFirst()

	// 3. Restart with scheduling disabled. The saved interval must still be in
	// force in the runtime configuration, not only in the database.
	second, secondAssess, closeSecond := startCoreAt(t, dbPath)
	cfg := secondAssess.schedulerConfigSnapshot()
	if cfg.Enabled {
		t.Fatalf("a disabled schedule came back enabled after a restart: %+v", cfg)
	}
	if cfg.Interval != 24*time.Hour {
		t.Fatalf("runtime interval after a restart with scheduling disabled = %v, want the stored 24h", cfg.Interval)
	}
	if cfg.ConfigSource != schedulerSourceSettings {
		t.Fatalf("config source = %q, want %q: the schedule in force comes from the user's stored preferences", cfg.ConfigSource, schedulerSourceSettings)
	}

	// 4. and 5. Enable scheduling using only assess_enabled=true. This must be
	// accepted, and it must reuse the saved interval.
	resp := second.handle(ipc.Request{Operation: "settings_update", Value: `{"assess_enabled":true}`})
	if !resp.OK {
		t.Fatalf("enabling a schedule with only assess_enabled=true was refused: %s", resp.Error)
	}
	cfg = secondAssess.schedulerConfigSnapshot()
	if !cfg.Enabled || cfg.Interval != 24*time.Hour {
		t.Fatalf("runtime schedule after enabling = %+v, want enabled every 24h", cfg)
	}

	// 6. The next scheduled assessment must be computed from that interval, and
	// it must be a real future time rather than the zero timestamp.
	next := waitForNextScan(t, secondAssess)
	if remaining := time.Until(next); remaining <= 23*time.Hour || remaining > 24*time.Hour {
		t.Fatalf("next scan is %v away, want about 24h", remaining)
	}
	var settings struct {
		Effective struct {
			AssessEnabled  bool   `json:"assess_enabled"`
			AssessInterval string `json:"assess_interval"`
		} `json:"effective"`
		NextScanAt *time.Time `json:"next_scan_at"`
	}
	decodeValue(t, second.handle(ipc.Request{Operation: "settings_get"}), &settings)
	if !settings.Effective.AssessEnabled || settings.Effective.AssessInterval != "24h0m0s" {
		t.Fatalf("effective settings = %+v, want the enabled 24h schedule", settings.Effective)
	}
	if settings.NextScanAt == nil || settings.NextScanAt.IsZero() {
		t.Fatalf("next_scan_at = %v, want the computed time", settings.NextScanAt)
	}
	closeSecond()

	// 7. Restart once more: the schedule and its interval must both survive.
	third, thirdAssess, closeThird := startCoreAt(t, dbPath)
	defer closeThird()
	cfg = thirdAssess.schedulerConfigSnapshot()
	if !cfg.Enabled || cfg.Interval != 24*time.Hour {
		t.Fatalf("runtime schedule after a second restart = %+v, want enabled every 24h", cfg)
	}
	if again := waitForNextScan(t, thirdAssess); time.Until(again) <= 23*time.Hour {
		t.Fatalf("next scan after a second restart is only %v away", time.Until(again))
	}
	var persisted struct {
		Settings map[string]string `json:"settings"`
	}
	decodeValue(t, third.handle(ipc.Request{Operation: "settings_get"}), &persisted)
	if persisted.Settings["assess_interval"] != "24h0m0s" || persisted.Settings["assess_enabled"] != "true" {
		t.Fatalf("stored settings = %+v, want the 24h enabled schedule persisted", persisted.Settings)
	}
	_ = ctx
}

// TestSchedulerStatusNeverPublishesAZeroTimestamp is the CE-6 M-3 guard.
//
// The defect: `next_scan_at` was a time.Time tagged `omitempty`, and Go never
// omits a struct, so a disabled schedule serialized the zero time as
// 0001-01-01T00:00:00Z. The dashboard then rendered that as a real date in the
// year 1 — a scheduled scan that never happened.
func TestSchedulerStatusNeverPublishesAZeroTimestamp(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "core.db")
	api, assess, closeFn := startCoreAt(t, dbPath)
	defer closeFn()

	type schedulerBlock struct {
		Enabled      bool       `json:"enabled"`
		ConfigSource string     `json:"config_source"`
		NextScanAt   *time.Time `json:"next_scan_at"`
	}
	var decoded struct {
		Scheduler schedulerBlock `json:"scheduler"`
	}

	// Scheduling is off: no next-scan time exists, so none is reported at all.
	resp := api.assess.handle(ipc.Request{Operation: "assessment_status"})
	decodeValue(t, resp, &decoded)
	if decoded.Scheduler.NextScanAt != nil {
		t.Fatalf("next_scan_at = %v while the schedule is off, want it omitted", decoded.Scheduler.NextScanAt)
	}
	if strings.Contains(resp.Value, "0001-01-01") || strings.Contains(resp.Value, "next_scan_at") {
		t.Fatalf("a disabled schedule advertises a next scan: %s", resp.Value)
	}

	// The settings read must agree with the assessment status.
	var settings struct {
		Effective struct {
			AssessEnabled  bool   `json:"assess_enabled"`
			AssessInterval string `json:"assess_interval"`
		} `json:"effective"`
		NextScanAt *time.Time `json:"next_scan_at"`
	}
	decodeValue(t, api.handle(ipc.Request{Operation: "settings_get"}), &settings)
	if settings.NextScanAt != nil {
		t.Fatalf("settings next_scan_at = %v while the schedule is off, want it omitted", settings.NextScanAt)
	}

	// Enable a real schedule and the time appears, computed from the interval.
	if update := api.handle(ipc.Request{Operation: "settings_update", Value: `{"assess_enabled":true,"assess_interval":"24h"}`}); !update.OK {
		t.Fatalf("enable refused: %s", update.Error)
	}
	waitForNextScan(t, assess)
	resp = api.assess.handle(ipc.Request{Operation: "assessment_status"})
	decoded.Scheduler = schedulerBlock{}
	decodeValue(t, resp, &decoded)
	if decoded.Scheduler.NextScanAt == nil || decoded.Scheduler.NextScanAt.IsZero() {
		t.Fatalf("next_scan_at = %v while a schedule is live, want the computed time", decoded.Scheduler.NextScanAt)
	}
	if remaining := time.Until(*decoded.Scheduler.NextScanAt); remaining <= 23*time.Hour || remaining > 24*time.Hour {
		t.Fatalf("next scan is %v away, want about one interval", remaining)
	}
}

// TestEnablingWithoutAnyIntervalIsStillRefused guards the other direction of the
// M-1 correction: keeping a saved interval available must not turn "enable a
// schedule that has no interval anywhere" into an accepted request.
func TestEnablingWithoutAnyIntervalIsStillRefused(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "core.db")
	api, assess, closeFn := startCoreAt(t, dbPath)
	defer closeFn()
	if cfg := assess.schedulerConfigSnapshot(); cfg.Interval != 0 {
		t.Fatalf("a fresh core has interval %v, want none", cfg.Interval)
	}
	resp := api.handle(ipc.Request{Operation: "settings_update", Value: `{"assess_enabled":true}`})
	if resp.OK {
		t.Fatal("enabling a schedule with no interval anywhere must be refused")
	}
	if !strings.Contains(resp.Error, "interval") {
		t.Fatalf("refusal = %q, want it to name the missing interval", resp.Error)
	}
	if cfg := assess.schedulerConfigSnapshot(); cfg.Enabled {
		t.Fatalf("a refused enable changed the runtime schedule: %+v", cfg)
	}

	// A stored interval outside the supported domain must not be adopted either.
	if err := api.store.PutSettings(context.Background(), map[string]string{"assess_interval": "5m"}); err != nil {
		t.Fatal(err)
	}
	if resp := api.handle(ipc.Request{Operation: "settings_update", Value: `{"assess_enabled":true}`}); resp.OK {
		t.Fatal("an out-of-domain stored interval was accepted when enabling")
	}
}

func newSettingsAPI(t *testing.T) (*settingsAPI, *assessmentAPI, *storage.Store) {
	t.Helper()
	assess, store := newAssessmentAPI(t)
	return &settingsAPI{store: store, assess: assess, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, assess, store
}

func TestSettingsAPIDispatchAndSupportedSet(t *testing.T) {
	api, _, _ := newSettingsAPI(t)
	if resp := api.handle(ipc.Request{Operation: "settings_nonsense"}); resp.OK || resp.Error == "" {
		t.Fatalf("unknown operation = %+v, want a refusal", resp)
	}
	if resp := api.handle(ipc.Request{Operation: "settings_get", Value: `{"key":"x"}`}); resp.OK {
		t.Fatal("a settings read with a field must be refused")
	}

	var page struct {
		Schema    string `json:"schema"`
		Settings  map[string]string
		Effective struct {
			AssessEnabled  bool   `json:"assess_enabled"`
			AssessInterval string `json:"assess_interval"`
			AssessMaxRuns  int    `json:"assess_max_runs"`
			Theme          string `json:"theme"`
		} `json:"effective"`
		Supported []struct {
			Key         string   `json:"key"`
			Type        string   `json:"type"`
			Minimum     string   `json:"minimum"`
			Maximum     string   `json:"maximum"`
			Options     []string `json:"options"`
			Description string   `json:"description"`
		} `json:"supported"`
		Storage struct {
			MonitoringMax int64 `json:"monitoring_max_bytes"`
			AssessmentMax int64 `json:"assessment_max_bytes"`
			InventoryMax  int64 `json:"inventory_max_bytes"`
		} `json:"storage"`
		ConfigSource string   `json:"config_source"`
		Limitations  []string `json:"limitations"`
	}
	decodeValue(t, api.handle(ipc.Request{Operation: "settings_get"}), &page)
	if page.Schema != settingsSchema {
		t.Fatalf("schema = %q", page.Schema)
	}
	keys := make([]string, 0, len(page.Supported))
	for _, descriptor := range page.Supported {
		keys = append(keys, descriptor.Key)
		if descriptor.Description == "" {
			t.Fatalf("descriptor %q has no description", descriptor.Key)
		}
	}
	want := []string{"assess_enabled", "assess_interval", "assess_max_runs", "theme"}
	if len(keys) != len(want) {
		t.Fatalf("supported keys = %v, want %v", keys, want)
	}
	for i, key := range want {
		if keys[i] != key {
			t.Fatalf("supported keys = %v, want %v", keys, want)
		}
	}
	for _, descriptor := range page.Supported {
		switch descriptor.Key {
		case "assess_interval":
			if descriptor.Minimum != "1h" || descriptor.Maximum != "168h" {
				t.Fatalf("interval bounds = %s..%s", descriptor.Minimum, descriptor.Maximum)
			}
		case "theme":
			if len(descriptor.Options) != 3 {
				t.Fatalf("theme options = %v", descriptor.Options)
			}
		}
	}
	if page.Storage.MonitoringMax <= 0 || page.Storage.AssessmentMax <= 0 || page.Storage.InventoryMax <= 0 {
		t.Fatalf("storage budgets were not reported: %+v", page.Storage)
	}
	if len(page.Limitations) == 0 {
		t.Fatal("settings must disclose its limitations")
	}
	if page.Effective.Theme != "system" || page.Effective.AssessEnabled {
		t.Fatalf("defaults = %+v, want a disabled schedule and the system theme", page.Effective)
	}
}

func TestSettingsAPIRejectsUnsupportedValuesTransactionally(t *testing.T) {
	api, _, store := newSettingsAPI(t)
	ctx := context.Background()

	cases := []struct{ name, value string }{
		{"unknown key", `{"wallpaper":"dark"}`},
		{"unknown theme", `{"theme":"neon"}`},
		{"interval below the floor", `{"assess_interval":"5m"}`},
		{"interval above the ceiling", `{"assess_interval":"480h"}`},
		{"malformed interval", `{"assess_interval":"daily"}`},
		{"retention below the floor", `{"assess_max_runs":1}`},
		{"retention above the ceiling", `{"assess_max_runs":999999}`},
		{"empty batch", `{}`},
		{"nested object", `{"theme":{"value":"dark"}}`},
		{"code injection attempt", `{"theme":"dark; DROP TABLE settings"}`},
	}
	for _, tc := range cases {
		if resp := api.handle(ipc.Request{Operation: "settings_update", Value: tc.value}); resp.OK {
			t.Fatalf("%s: %s was accepted", tc.name, tc.value)
		}
		settings, err := store.Settings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(settings) != 0 {
			t.Fatalf("%s: a rejected update was applied: %v", tc.name, settings)
		}
	}

	// One valid and one invalid field in the same batch must apply neither.
	if resp := api.handle(ipc.Request{Operation: "settings_update", Value: `{"theme":"dark","assess_max_runs":999999}`}); resp.OK {
		t.Fatal("a batch with one invalid field was accepted")
	}
	if settings, err := store.Settings(ctx); err != nil || len(settings) != 0 {
		t.Fatalf("settings = %v (err %v), want the batch to be applied atomically", settings, err)
	}
}

func TestSettingsAPIAppliesValidPreferences(t *testing.T) {
	api, assess, store := newSettingsAPI(t)
	ctx := context.Background()

	var page struct {
		Settings  map[string]string
		Effective struct {
			AssessEnabled  bool   `json:"assess_enabled"`
			AssessInterval string `json:"assess_interval"`
			AssessMaxRuns  int    `json:"assess_max_runs"`
			Theme          string `json:"theme"`
		} `json:"effective"`
	}
	decodeValue(t, api.handle(ipc.Request{Operation: "settings_update", Value: `{"assess_enabled":true,"assess_interval":"6h","assess_max_runs":120,"theme":"dark"}`}), &page)
	if !page.Effective.AssessEnabled || page.Effective.AssessInterval != "6h0m0s" || page.Effective.AssessMaxRuns != 120 || page.Effective.Theme != "dark" {
		t.Fatalf("effective settings = %+v", page.Effective)
	}
	if page.Settings["theme"] != "dark" {
		t.Fatalf("stored settings = %v", page.Settings)
	}
	if got := store.AssessmentRunLimit(); got != 120 {
		t.Fatalf("assessment run retention = %d, want 120", got)
	}
	if cfg := assess.schedulerConfigSnapshot(); !cfg.Enabled || cfg.Interval != 6*time.Hour {
		t.Fatalf("the live schedule was not updated: %+v", cfg)
	}

	// A later update leaves fields the caller did not send untouched.
	decodeValue(t, api.handle(ipc.Request{Operation: "settings_update", Value: `{"theme":"light"}`}), &page)
	if page.Effective.Theme != "light" || !page.Effective.AssessEnabled || page.Effective.AssessInterval != "6h0m0s" || page.Effective.AssessMaxRuns != 120 {
		t.Fatalf("partial update changed unrelated preferences: %+v", page.Effective)
	}

	// Turning the schedule off is allowed without an interval.
	decodeValue(t, api.handle(ipc.Request{Operation: "settings_update", Value: `{"assess_enabled":false}`}), &page)
	if page.Effective.AssessEnabled {
		t.Fatalf("schedule was not disabled: %+v", page.Effective)
	}
	if cfg := assess.schedulerConfigSnapshot(); cfg.Enabled {
		t.Fatalf("the live schedule is still enabled: %+v", cfg)
	}

	settings, err := store.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(settings) != 4 {
		t.Fatalf("stored settings = %v, want all four keys", settings)
	}
}

// TestSettingsAPIRequiresAnIntervalWhenEnabling covers the one cross-field rule:
// a schedule cannot be switched on without an interval, and a schedule that
// would fall below the conservative floor is refused rather than silently
// clamped.
func TestSettingsAPIRequiresAnIntervalWhenEnabling(t *testing.T) {
	api, assess, store := newSettingsAPI(t)
	if resp := api.handle(ipc.Request{Operation: "settings_update", Value: `{"assess_enabled":true}`}); resp.OK {
		t.Fatal("enabling a schedule with no interval must be refused")
	}
	if settings, err := store.Settings(context.Background()); err != nil || len(settings) != 0 {
		t.Fatalf("settings = %v (err %v), want nothing stored", settings, err)
	}
	// With an interval in the same batch it is accepted.
	if resp := api.handle(ipc.Request{Operation: "settings_update", Value: `{"assess_enabled":true,"assess_interval":"24h"}`}); !resp.OK {
		t.Fatalf("enabling with an interval was refused: %s", resp.Error)
	}
	// And a later interval change below the conservative floor is still refused,
	// leaving the stored interval untouched.
	if resp := api.handle(ipc.Request{Operation: "settings_update", Value: `{"assess_interval":"30m"}`}); resp.OK {
		t.Fatal("an interval below the floor must be refused")
	}
	if cfg := assess.schedulerConfigSnapshot(); cfg.Interval != 24*time.Hour {
		t.Fatalf("interval = %v, want the stored 24h", cfg.Interval)
	}
}

func TestLoadPersistedSettingsAppliesStoredPreferences(t *testing.T) {
	assess, store := newAssessmentAPI(t)
	ctx := context.Background()
	if err := store.PutSettings(ctx, map[string]string{
		"assess_enabled":  "true",
		"assess_interval": "12h",
		"assess_max_runs": "77",
		"theme":           "dark",
	}); err != nil {
		t.Fatal(err)
	}
	loadPersistedSettings(ctx, store, assess, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if cfg := assess.schedulerConfigSnapshot(); !cfg.Enabled || cfg.Interval != 12*time.Hour {
		t.Fatalf("schedule after load = %+v, want enabled every 12h", cfg)
	}
	if got := store.AssessmentRunLimit(); got != 77 {
		t.Fatalf("retention after load = %d, want 77", got)
	}

	// A stored value outside the supported domain must be ignored rather than
	// trusted, so a hand-edited database cannot disable the guardrails.
	assess2, store2 := newAssessmentAPI(t)
	if err := store2.PutSettings(ctx, map[string]string{"assess_interval": "1m", "assess_max_runs": "100000"}); err != nil {
		t.Fatal(err)
	}
	loadPersistedSettings(ctx, store2, assess2, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if cfg := assess2.schedulerConfigSnapshot(); cfg.Enabled {
		t.Fatalf("an out-of-domain stored interval enabled the schedule: %+v", cfg)
	}
	if got := store2.AssessmentRunLimit(); got == 100000 {
		t.Fatal("an out-of-domain stored retention was applied")
	}
}

// TestSettingsAPIRetentionAppliesImmediately checks that lowering the retention
// limit prunes stored history at once rather than at some later scan.
func TestSettingsAPIRetentionAppliesImmediately(t *testing.T) {
	api, _, store := newSettingsAPI(t)
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		if _, err := store.StartRun(ctx, "manual"); err != nil {
			t.Fatal(err)
		}
	}
	if n := countRuns(t, store); n != 30 {
		t.Fatalf("runs = %d, want 30", n)
	}
	if resp := api.handle(ipc.Request{Operation: "settings_update", Value: `{"assess_max_runs":10}`}); !resp.OK {
		t.Fatalf("update refused: %s", resp.Error)
	}
	if n := countRuns(t, store); n != 10 {
		t.Fatalf("runs = %d after lowering retention, want 10", n)
	}
}

// countRuns counts stored assessment runs through the store's own bounded read
// path rather than reaching into its database handle.
func countRuns(t *testing.T, store *storage.Store) int {
	t.Helper()
	runs, err := store.Runs(context.Background(), 0, storage.DefaultLimits().MaxPageSize)
	if err != nil {
		t.Fatal(err)
	}
	return len(runs)
}

func TestInventoryAPIStatusNeverInventsAReading(t *testing.T) {
	api, _ := newInventoryAPI(t)
	var status struct {
		StoredBytes int64  `json:"stored_bytes"`
		MaxBytes    int64  `json:"max_bytes"`
		MaxItems    int    `json:"max_items_per_kind"`
		HasSnapshot bool   `json:"has_snapshot"`
		GeneratedAt string `json:"generated_at"`
	}
	decodeValue(t, api.handle(ipc.Request{Operation: "inventory_status"}), &status)
	if status.StoredBytes != 0 || status.HasSnapshot {
		t.Fatalf("empty status = %+v", status)
	}
	if status.MaxBytes <= 0 || status.MaxItems <= 0 {
		t.Fatalf("limits were not reported: %+v", status)
	}
	if status.GeneratedAt == "" {
		t.Fatal("status must timestamp itself")
	}
}
