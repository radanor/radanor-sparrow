package coreapp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/ipc"
	"github.com/radanor/radanor-shield/internal/monitoring"
	"github.com/radanor/radanor-shield/internal/storage"
)

// simulatedSource is test-only simulated infrastructure. It is not linked into
// any production source registration.
type simulatedSource struct{}

func (simulatedSource) Descriptor() monitoring.Descriptor {
	return monitoring.Descriptor{ContractVersion: monitoring.SourceContractV1, ID: "simulated.api_test", Platforms: []string{"simulated-test"}, Capabilities: []string{"deterministic simulated delivery"}, RequiredAccess: monitoring.PrivilegeUser, EventTypes: []string{"test.observation"}, SchemaVersion: monitoring.EventSchemaV1, Recovery: monitoring.RecoveryNone, StartupBehavior: "starts immediately", ShutdownBehavior: "returns on cancellation", HealthReporting: "explicit confirmation", Limitations: []string{"synthetic test data only"}}
}

func (simulatedSource) Run(ctx context.Context, _ []byte, emit monitoring.EmitFunc, report monitoring.Reporter) error {
	report.ConfirmHealthy()
	for i := 0; i < 3; i++ {
		now := time.Now()
		e := monitoring.Event{ID: "sim:" + string(rune('a'+i)), Source: "simulated.api_test", Type: "test.observation", Platform: "simulated-test", OccurredAt: now, ObservedAt: now, Evidence: monitoring.EvidenceTelemetry, SchemaVersion: monitoring.EventSchemaV1, Metadata: map[string]string{"name": "observation"}}
		if err := emit(ctx, e); err != nil {
			return err
		}
	}
	report.ReportGap(time.Now(), time.Now(), "simulated discontinuity")
	<-ctx.Done()
	return ctx.Err()
}

func startAPI(t *testing.T) *monitoringAPI {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "monitoring.db"), storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cfg := monitoring.DefaultConfig()
	cfg.DurableCheckpoints = true
	engine, err := monitoring.New(cfg, store.Process, store, simulatedSource{})
	if err != nil {
		t.Fatal(err)
	}
	engine.SetGapSink(store)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = engine.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return &monitoringAPI{engine: engine, store: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func decodeResponse[T any](t *testing.T, resp ipc.Response) T {
	t.Helper()
	if !resp.OK {
		t.Fatalf("response not ok: %s", resp.Error)
	}
	var out T
	if err := json.Unmarshal([]byte(resp.Value), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestMonitoringAPIStatusEventsAndGaps(t *testing.T) {
	api := startAPI(t)
	deadline := time.Now().Add(2 * time.Second)
	for {
		status := decodeResponse[monitoringStatus](t, api.handle(ipc.Request{Operation: "monitoring_status"}))
		if status.ProcessedEvents >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("events not processed: %+v", status)
		}
		time.Sleep(5 * time.Millisecond)
	}
	status := decodeResponse[monitoringStatus](t, api.handle(ipc.Request{Operation: "monitoring_status"}))
	if status.Schema != statusSchema || status.State != "active" || len(status.Sources) != 1 {
		t.Fatalf("status = %+v", status)
	}
	source := status.Sources[0]
	if source.ID != "simulated.api_test" || string(source.State) != string(monitoring.HealthHealthy) || source.Recovery != "none" || len(source.Limitations) == 0 || len(status.ReplayLimits) == 0 {
		t.Fatalf("source status = %+v", source)
	}
	if status.Storage.EventCount < 3 || status.Storage.Encrypted {
		t.Fatalf("storage info = %+v", status.Storage)
	}
	events := decodeResponse[eventsResponse](t, api.handle(ipc.Request{Operation: "monitoring_events", Value: `{"limit":2}`}))
	if len(events.Events) != 2 || events.Schema != eventsSchema || events.NextBefore == 0 {
		t.Fatalf("events page = %+v", events)
	}
	if events.Events[0].Metadata["name"] != "observation" {
		t.Fatalf("metadata lost: %+v", events.Events[0])
	}
	next := decodeResponse[eventsResponse](t, api.handle(ipc.Request{Operation: "monitoring_events", Value: `{"limit":10,"before":2}`}))
	if len(next.Events) != 1 {
		t.Fatalf("filtered page = %+v", next)
	}
	gaps := decodeResponse[gapsResponse](t, api.handle(ipc.Request{Operation: "monitoring_gaps", Value: ""}))
	if len(gaps.Gaps) == 0 || gaps.Gaps[0].Reason != "simulated discontinuity" || gaps.Total < 1 {
		t.Fatalf("gaps = %+v", gaps)
	}
}

// TestMonitoringAPIReportsIngestOutcomes is the CE-6 M-2 exposure check. The
// operator must be able to see, per source, how many events were stored as new
// evidence and how many were ignored as duplicates — an ignored duplicate must
// not be indistinguishable from a stored one in the reported counts.
func TestMonitoringAPIReportsIngestOutcomes(t *testing.T) {
	api := startAPI(t)
	deadline := time.Now().Add(2 * time.Second)
	for decodeResponse[monitoringStatus](t, api.handle(ipc.Request{Operation: "monitoring_status"})).ProcessedEvents < 3 {
		if time.Now().After(deadline) {
			t.Fatal("events not processed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	status := decodeResponse[monitoringStatus](t, api.handle(ipc.Request{Operation: "monitoring_status"}))
	if len(status.Ingest) != 1 || status.Ingest[0].Source != "simulated.api_test" {
		t.Fatalf("ingest counters = %+v, want one row for the simulated source", status.Ingest)
	}
	if status.Ingest[0].Inserted < 3 {
		t.Fatalf("inserted = %d, want at least the three stored events", status.Ingest[0].Inserted)
	}

	// Re-deliver one observation unchanged: it is counted as a duplicate rather
	// than as new evidence, and no second row appears.
	ctx := context.Background()
	now := time.Now()
	event := monitoring.Event{
		ID: "sim:manual", Source: "simulated.api_test", Type: "test.observation", Platform: "simulated-test",
		OccurredAt: now, ObservedAt: now, Evidence: monitoring.EvidenceTelemetry,
		SchemaVersion: monitoring.EventSchemaV1, Metadata: map[string]string{"name": "observation"},
	}
	if outcome, err := api.store.ProcessEvent(ctx, event); err != nil || outcome != storage.IngestInserted {
		t.Fatalf("first delivery = (%q, %v), want inserted", outcome, err)
	}
	if outcome, err := api.store.ProcessEvent(ctx, event); err != nil || outcome != storage.IngestDuplicate {
		t.Fatalf("second delivery = (%q, %v), want duplicate", outcome, err)
	}

	status = decodeResponse[monitoringStatus](t, api.handle(ipc.Request{Operation: "monitoring_status"}))
	if len(status.Ingest) != 1 {
		t.Fatalf("ingest counters = %+v, want a single source row", status.Ingest)
	}
	counters := status.Ingest[0]
	if counters.Duplicates != 1 {
		t.Fatalf("duplicates = %d, want 1 visible through the API", counters.Duplicates)
	}
	if counters.Inserted != 4 {
		t.Fatalf("inserted = %d, want the three source events plus one manual delivery", counters.Inserted)
	}
	if counters.Collisions != 0 {
		t.Fatalf("collisions = %d, want none", counters.Collisions)
	}
	if status.Storage.EventCount != 4 {
		t.Fatalf("stored events = %d, want 4: an ignored duplicate is not new evidence", status.Storage.EventCount)
	}
}

func TestMonitoringAPIRejectsInvalidQueries(t *testing.T) {
	api := startAPI(t)
	for _, value := range []string{`{"limit":-1}`, `{"limit":9999}`, `{"before":-5}`, `{"unknown":true}`, `{"source":"` + strings.Repeat("s", 200) + `"}`, `{`, `[]`} {
		if resp := api.handle(ipc.Request{Operation: "monitoring_events", Value: value}); resp.OK {
			t.Fatalf("invalid query accepted: %s", value)
		}
	}
	for _, value := range []string{`{"limit":999}`, `{"extra":1}`, `not json`} {
		if resp := api.handle(ipc.Request{Operation: "monitoring_gaps", Value: value}); resp.OK {
			t.Fatalf("invalid gaps query accepted: %s", value)
		}
	}
}

func TestClampPageDefaults(t *testing.T) {
	if n, err := clampPage(0); err != nil || n != defaultPage {
		t.Fatalf("default page = %d, %v", n, err)
	}
	if n, err := clampPage(maxMonitoringLimitAPI()); err != nil || n != maxMonitoringLimitAPI() {
		t.Fatalf("max page = %d, %v", n, err)
	}
}

func maxMonitoringLimitAPI() int { return storage.DefaultLimits().MaxPageSize }
