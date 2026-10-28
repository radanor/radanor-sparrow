package coreapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
	"github.com/radanor/radanor-shield/internal/collectors"
	"github.com/radanor/radanor-shield/internal/ipc"
	"github.com/radanor/radanor-shield/internal/storage"
)

func TestSchedulerConfigurationIsConservative(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantErr bool
		enabled bool
	}{
		{"unset disables scheduled scans", nil, false, false},
		{"a valid interval enables the scheduler", map[string]string{"RADANOR_SPARROW_ASSESS_INTERVAL": "24h"}, false, true},
		{"an interval below the floor is refused", map[string]string{"RADANOR_SPARROW_ASSESS_INTERVAL": "5m"}, true, false},
		{"a malformed interval is refused", map[string]string{"RADANOR_SPARROW_ASSESS_INTERVAL": "daily"}, true, false},
		{"a valid retention keeps the scheduler setting", map[string]string{"RADANOR_SPARROW_ASSESS_INTERVAL": "6h", "RADANOR_SPARROW_ASSESS_MAX_RUNS": "500"}, false, true},
		{"an absurd retention is refused", map[string]string{"RADANOR_SPARROW_ASSESS_MAX_RUNS": "999999"}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"RADANOR_SPARROW_ASSESS_INTERVAL", "RADANOR_SPARROW_ASSESS_MAX_RUNS"} {
				t.Setenv(key, "")
			}
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			cfg, err := schedulerFromEnv()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %+v", cfg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Enabled != tc.enabled {
				t.Fatalf("enabled = %v, want %v", cfg.Enabled, tc.enabled)
			}
			if cfg.MaxRetained < 10 {
				t.Fatalf("retention = %d, want a sane bound", cfg.MaxRetained)
			}
		})
	}
}

func newAssessmentAPI(t *testing.T) (*assessmentAPI, *storage.Store) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "assessment.db"), storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	scanner := assessment.NewScanner(store, collectors.PlatformChecks(), collectors.PlatformCollectors())
	return &assessmentAPI{
		scanner:   scanner,
		store:     store,
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		scheduler: schedulerConfig{MaxRetained: 50},
	}, store
}

func TestAssessmentAPIDispatchAndValidation(t *testing.T) {
	api, _ := newAssessmentAPI(t)
	if resp := api.handle(ipc.Request{Operation: "assessment_nonsense"}); resp.OK || resp.Error == "" {
		t.Fatalf("unknown operation = %+v, want a refusal", resp)
	}
	// A request carrying unexpected fields must be rejected rather than
	// silently ignored.
	if resp := api.handle(ipc.Request{Operation: "assessment_scan", Value: `{"target":"/etc"}`}); resp.OK {
		t.Fatal("a scan request with an unexpected field must be refused")
	}
	if resp := api.handle(ipc.Request{Operation: "assessment_finding", Value: `{"fingerprint":"not-hex"}`}); resp.OK {
		t.Fatal("a malformed fingerprint must be refused")
	}
	if resp := api.handle(ipc.Request{Operation: "assessment_run", Value: `{"run_id":0}`}); resp.OK {
		t.Fatal("a zero run id must be refused")
	}
	if resp := api.handle(ipc.Request{Operation: "assessment_findings", Value: `{"lifecycle":"bogus"}`}); resp.OK {
		t.Fatal("an unknown lifecycle filter must be refused")
	}
	// Cancelling with no scan in progress is an honest refusal.
	if resp := api.handle(ipc.Request{Operation: "assessment_cancel"}); resp.OK {
		t.Fatal("cancelling an idle scanner must be refused")
	}
}

func TestDeviceInformationIsBoundedAndUsesStoredPlatformObservation(t *testing.T) {
	api, store := newAssessmentAPI(t)
	var missing deviceInfoResponse
	out := api.handle(ipc.Request{Operation: "assessment_device"})
	if !out.OK || json.Unmarshal([]byte(out.Value), &missing) != nil {
		t.Fatalf("missing device response = %+v", out)
	}
	if missing.State != "missing" || missing.OperatingSystem != "" || missing.Architecture != "" {
		t.Fatalf("missing device = %+v", missing)
	}

	ctx := context.Background()
	runID, err := store.StartRun(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	observed := time.Now()
	obs := assessment.Observation{
		ID: "platform", Collector: "platform", Platform: "ubuntu", CollectedAt: observed,
		Status: assessment.Collected, Source: "/etc/os-release + /proc/sys/kernel/osrelease",
		Values: []assessment.Value{
			{Key: "pretty_name", Kind: assessment.ValueString, Text: "Ubuntu 22.04.5 LTS"},
			{Key: "arch", Kind: assessment.ValueString, Text: "amd64"},
			{Key: "kernel_release", Kind: assessment.ValueString, Text: "must-not-be-exposed"},
		},
		Error: "must-not-be-exposed",
	}
	if err := store.SaveObservations(ctx, runID, []assessment.Observation{obs}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun(ctx, runID, assessment.ScanOutcome{Status: "completed", StartedAt: observed, FinishedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	var device deviceInfoResponse
	out = api.handle(ipc.Request{Operation: "assessment_device"})
	if !out.OK || json.Unmarshal([]byte(out.Value), &device) != nil {
		t.Fatalf("device response = %+v", out)
	}
	if device.Schema != assessmentDeviceSchema || device.State != "available" || device.Stale ||
		device.OperatingSystem != "Ubuntu 22.04.5 LTS" || device.Architecture != "amd64" || device.Platform != "ubuntu" {
		t.Fatalf("device = %+v", device)
	}
	if strings.Contains(out.Value, "kernel_release") || strings.Contains(out.Value, "must-not-be-exposed") || strings.Contains(out.Value, obs.Source) {
		t.Fatalf("device response exposed unrestricted observation fields: %s", out.Value)
	}

	storePlatform := func(observation assessment.Observation) deviceInfoResponse {
		t.Helper()
		id, err := store.StartRun(ctx, "manual")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SaveObservations(ctx, id, []assessment.Observation{observation}); err != nil {
			t.Fatal(err)
		}
		if err := store.FinishRun(ctx, id, assessment.ScanOutcome{Status: "completed", StartedAt: observation.CollectedAt, FinishedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		out := api.handle(ipc.Request{Operation: "assessment_device"})
		var got deviceInfoResponse
		if !out.OK || json.Unmarshal([]byte(out.Value), &got) != nil {
			t.Fatalf("device response = %+v", out)
		}
		return got
	}

	identityValues := []assessment.Value{
		{Key: "pretty_name", Kind: assessment.ValueString, Text: "Legacy Ubuntu"},
		{Key: "arch", Kind: assessment.ValueString, Text: "amd64"},
	}
	fresh := time.Now()
	staleAt := fresh.Add(-assessment.DefaultCheckFreshness - time.Minute)
	for _, tc := range []struct {
		name         string
		status       assessment.CollectionStatus
		observedAt   time.Time
		values       []assessment.Value
		wantState    string
		wantStale    bool
		wantIdentity bool
	}{
		{name: "failed fresh", status: assessment.Failed, observedAt: fresh, values: identityValues, wantState: "unavailable"},
		{name: "failed stale legacy identity", status: assessment.Failed, observedAt: staleAt, values: identityValues, wantState: "unavailable"},
		{name: "permission denied stale", status: assessment.PermissionDenied, observedAt: staleAt, values: identityValues, wantState: "unavailable"},
		{name: "unsupported stale", status: assessment.Unsupported, observedAt: staleAt, values: identityValues, wantState: "unavailable"},
		{name: "incomplete fresh", status: assessment.Incomplete, observedAt: fresh, values: identityValues[:1], wantState: "incomplete", wantIdentity: true},
		{name: "incomplete stale", status: assessment.Incomplete, observedAt: staleAt, values: identityValues[:1], wantState: "stale", wantStale: true, wantIdentity: true},
		{name: "collected stale", status: assessment.Collected, observedAt: staleAt, values: identityValues, wantState: "stale", wantStale: true, wantIdentity: true},
		{name: "collected missing values", status: assessment.Collected, observedAt: fresh, wantState: "incomplete", wantIdentity: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := obs
			candidate.Status = tc.status
			candidate.CollectedAt = tc.observedAt
			candidate.Values = tc.values
			got := storePlatform(candidate)
			if got.State != tc.wantState || got.Stale != tc.wantStale || got.CollectionStatus != tc.status {
				t.Fatalf("device state = %+v", got)
			}
			identityPresent := got.OperatingSystem != "" || got.Architecture != "" || got.Platform != ""
			if identityPresent != tc.wantIdentity {
				t.Fatalf("identity presence = %v, want %v: %+v", identityPresent, tc.wantIdentity, got)
			}
			if tc.name == "collected missing values" && (got.OperatingSystem != "" || got.Architecture != "") {
				t.Fatalf("missing collected values were invented: %+v", got)
			}
			if len(got.Note) == 0 || len(got.Note) > assessment.MaxTextValue {
				t.Fatalf("unsafe note length %d: %+v", len(got.Note), got)
			}
		})
	}
	windows := obs
	windows.CollectedAt = time.Now()
	windows.Platform = "windows"
	windows.Values = []assessment.Value{
		{Key: "product_name", Kind: assessment.ValueString, Text: "Microsoft Windows 11"},
		{Key: "arch", Kind: assessment.ValueString, Text: "amd64"},
	}
	if got := storePlatform(windows); got.State != "available" || got.OperatingSystem != "Microsoft Windows 11" || got.Platform != "windows" {
		t.Fatalf("Windows device = %+v", got)
	}
}

func TestDeviceMissingStateExplainsWhichEvidenceIsAbsent(t *testing.T) {
	api, store := newAssessmentAPI(t)
	ctx := context.Background()
	readDevice := func() (deviceInfoResponse, string) {
		t.Helper()
		out := api.handle(ipc.Request{Operation: "assessment_device"})
		var device deviceInfoResponse
		if !out.OK || json.Unmarshal([]byte(out.Value), &device) != nil {
			t.Fatalf("device response = %+v", out)
		}
		return device, out.Value
	}

	// Nothing has completed yet: the honest advice is to run an assessment.
	beforeRun, payload := readDevice()
	if beforeRun.State != "missing" || beforeRun.Note != "Run an assessment to record device information." {
		t.Fatalf("no completed assessment = %+v", beforeRun)
	}
	if beforeRun.Stale || beforeRun.CollectionStatus != "" || beforeRun.CollectedAt != nil {
		t.Fatalf("absent evidence carried state: %+v", beforeRun)
	}
	if len(payload) > 1024 {
		t.Fatalf("missing device response is not bounded: %d bytes", len(payload))
	}

	// An older completed assessment that did record platform evidence.
	when := time.Now()
	legacy, err := store.StartRun(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	legacyObs := assessment.Observation{
		ID: "platform", Collector: "platform", Platform: "ubuntu", CollectedAt: when,
		Status: assessment.Collected, Source: "/etc/os-release",
		Values: []assessment.Value{
			{Key: "pretty_name", Kind: assessment.ValueString, Text: "Legacy Ubuntu"},
			{Key: "arch", Kind: assessment.ValueString, Text: "amd64"},
		},
	}
	if err := store.SaveObservations(ctx, legacy, []assessment.Observation{legacyObs}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun(ctx, legacy, assessment.ScanOutcome{Status: "completed", StartedAt: when, FinishedAt: when.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	available, _ := readDevice()
	if available.State != "available" || available.OperatingSystem != "Legacy Ubuntu" {
		t.Fatalf("completed assessment with platform evidence = %+v", available)
	}

	// A newer completed assessment that recorded nothing is a different answer:
	// it must not repeat the "run an assessment" advice, and it must not borrow
	// the older run's identity.
	newer, err := store.StartRun(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	finished := time.Now()
	if err := store.FinishRun(ctx, newer, assessment.ScanOutcome{Status: "completed", StartedAt: finished, FinishedAt: finished.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	noEvidence, payload := readDevice()
	wantNote := "The latest assessment did not record device information."
	if noEvidence.State != "missing" || noEvidence.Note != wantNote {
		t.Fatalf("completed run without platform = %+v, want note %q", noEvidence, wantNote)
	}
	if noEvidence.OperatingSystem != "" || noEvidence.Architecture != "" || noEvidence.Platform != "" {
		t.Fatalf("an older run's identity was substituted: %+v", noEvidence)
	}
	for _, leak := range []string{"Legacy Ubuntu", "/etc/os-release", "kernel_release"} {
		if strings.Contains(payload, leak) {
			t.Fatalf("missing device response leaked %q: %s", leak, payload)
		}
	}

	// A scan that is still running is not an evidence boundary either.
	running, err := store.StartRun(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveObservations(ctx, running, []assessment.Observation{legacyObs}); err != nil {
		t.Fatal(err)
	}
	duringRun, _ := readDevice()
	if duringRun.State != "missing" || duringRun.Note != wantNote {
		t.Fatalf("unfinished run changed the answer: %+v", duringRun)
	}
}

func TestAssessmentScanRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("scan round trip skipped in short mode")
	}
	api, store := newAssessmentAPI(t)

	resp := api.handle(ipc.Request{Operation: "assessment_scan"})
	if !resp.OK {
		t.Fatalf("scan start refused: %+v", resp)
	}
	var startPayload struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal([]byte(resp.Value), &startPayload); err != nil || !startPayload.Accepted {
		t.Fatalf("scan acceptance payload = %s (%v)", resp.Value, err)
	}

	// A second concurrent scan must be refused rather than queued.
	if second := api.handle(ipc.Request{Operation: "assessment_scan"}); second.OK {
		t.Fatal("a second concurrent scan was accepted")
	}

	// Poll the API's own running state rather than the scanner's, because the
	// API reports a reserved scan as running before the scanner's goroutine has
	// started. The loop therefore cannot exit before the scan actually started.
	deadline := time.Now().Add(60 * time.Second)
	sawRunning := false
	for time.Now().Before(deadline) {
		if api.running() {
			sawRunning = true
		} else if sawRunning {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if api.running() {
		t.Fatal("the scan did not finish within the deadline")
	}
	if !sawRunning {
		t.Fatal("the scan never reported itself as running")
	}

	var status struct {
		Schema string `json:"schema"`
		Scan   struct {
			Phase string `json:"phase"`
		} `json:"scan"`
		Summary struct {
			HasRun      bool `json:"has_run"`
			TotalChecks int  `json:"total_checks"`
		} `json:"summary"`
		Scheduler struct {
			Enabled bool `json:"enabled"`
		} `json:"scheduler"`
	}
	if out := api.handle(ipc.Request{Operation: "assessment_status"}); !out.OK || json.Unmarshal([]byte(out.Value), &status) != nil {
		t.Fatalf("status read failed: %+v", out)
	}
	if status.Schema != assessmentStatusSchema || !status.Summary.HasRun {
		t.Fatalf("status = %+v", status)
	}
	if status.Summary.TotalChecks == 0 {
		t.Fatal("a completed scan recorded no checks")
	}
	if status.Scheduler.Enabled {
		t.Fatal("the scheduler must be disabled by default")
	}

	// Checks must be readable and carry a rule version.
	var checks checksResponse
	if out := api.handle(ipc.Request{Operation: "assessment_checks"}); !out.OK || json.Unmarshal([]byte(out.Value), &checks) != nil {
		t.Fatalf("checks read failed: %+v", out)
	}
	if len(checks.Checks) == 0 {
		t.Fatal("no check results were stored")
	}
	for _, c := range checks.Checks {
		if c.CheckVersion == "" || c.Title == "" {
			t.Fatalf("check result missing metadata: %+v", c)
		}
	}

	// Scan history must include the run.
	var runs runsResponse
	if out := api.handle(ipc.Request{Operation: "assessment_runs"}); !out.OK || json.Unmarshal([]byte(out.Value), &runs) != nil {
		t.Fatalf("runs read failed: %+v", out)
	}
	if len(runs.Runs) == 0 || runs.Runs[0].ChecksTotal == 0 {
		t.Fatalf("run history = %+v", runs.Runs)
	}
	var detail runResponse
	payload, _ := json.Marshal(map[string]any{"run_id": runs.Runs[0].ID})
	if out := api.handle(ipc.Request{Operation: "assessment_run", Value: string(payload)}); !out.OK || json.Unmarshal([]byte(out.Value), &detail) != nil {
		t.Fatalf("run detail read failed: %+v", out)
	}
	if len(detail.Checks) == 0 {
		t.Fatal("run detail contained no check results")
	}

	// Findings are only produced for failures; the response must be well-formed
	// either way.
	var findingsResp findingsResponse
	if out := api.handle(ipc.Request{Operation: "assessment_findings"}); !out.OK || json.Unmarshal([]byte(out.Value), &findingsResp) != nil {
		t.Fatalf("findings read failed: %+v", out)
	}
	for _, f := range findingsResp.Findings {
		if f.Fingerprint == "" || f.Lifecycle == "" {
			t.Fatalf("finding missing identity: %+v", f)
		}
	}
	// Every stored finding must be traceable to a failing check.
	if len(findingsResp.Findings) > 0 {
		results, _, err := store.LatestResults(context.Background(), 256)
		if err != nil {
			t.Fatal(err)
		}
		failed := map[string]bool{}
		for _, r := range results {
			if r.Result == assessment.Fail {
				failed[r.CheckID] = true
			}
		}
		for _, f := range findingsResp.Findings {
			if !failed[f.CheckID] {
				t.Fatalf("finding for %s exists without a failing check result", f.CheckID)
			}
		}
	}
}

// TestAssessmentReadPagesShrinkToFitTheIPCBound is the regression guard for the
// CE-5 low-severity finding on oversized read-page responses. A stored page of
// check results carries up to 8 KiB of evidence per check, so a full page can
// exceed the IPC frame bound. The read must return a smaller, truthful page
// rather than failing.
func TestAssessmentReadPagesShrinkToFitTheIPCBound(t *testing.T) {
	api, store := newAssessmentAPI(t)
	ctx := context.Background()
	runID, err := store.StartRun(ctx, "manual")
	if err != nil {
		t.Fatal(err)
	}
	const total = 120
	blob := strings.Repeat("x", 6000)
	results := make([]assessment.CheckResult, 0, total)
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("synthetic_check_%03d", i)
		results = append(results, assessment.CheckResult{
			CheckID: id, CheckVersion: "1.0.0", Title: "synthetic",
			Result: assessment.Pass, Severity: assessment.SeverityLow,
			Observation: assessment.Observation{
				ID: id, Collector: "synthetic", Platform: "ubuntu",
				Status: assessment.Collected, CollectedAt: time.Now(),
				Values: []assessment.Value{{Key: "blob", Kind: assessment.ValueString, Text: blob}},
			},
			CollectedAt: time.Now(), EvaluatedAt: time.Now(), Fresh: true,
		})
	}
	if err := store.SaveCheckResults(ctx, runID, results); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun(ctx, runID, assessment.ScanOutcome{
		Status: "completed", StartedAt: time.Now().Add(-time.Second), FinishedAt: time.Now(), ChecksTotal: total,
	}); err != nil {
		t.Fatal(err)
	}

	out := api.handle(ipc.Request{Operation: "assessment_checks"})
	if !out.OK {
		t.Fatalf("checks read failed under a large page: %+v", out)
	}
	if len(out.Value) > ipc.MaxResponseBytes {
		t.Fatalf("checks response %d bytes exceeds the %d-byte IPC bound", len(out.Value), ipc.MaxResponseBytes)
	}
	var decoded checksResponse
	if err := json.Unmarshal([]byte(out.Value), &decoded); err != nil {
		t.Fatalf("checks response is not decodable: %v", err)
	}
	if len(decoded.Checks) == 0 {
		t.Fatal("shrinking produced an empty page")
	}
	if len(decoded.Checks) >= total {
		t.Fatalf("the page was not shrunk: %d of %d checks returned", len(decoded.Checks), total)
	}

	payload, _ := json.Marshal(map[string]any{"run_id": runID})
	detail := api.handle(ipc.Request{Operation: "assessment_run", Value: string(payload)})
	if !detail.OK {
		t.Fatalf("run detail failed under a large page: %+v", detail)
	}
	if len(detail.Value) > ipc.MaxResponseBytes {
		t.Fatalf("run detail %d bytes exceeds the IPC bound", len(detail.Value))
	}
}

func TestAssessmentStatusIsBoundedAndTruthfulWhenIdle(t *testing.T) {
	api, _ := newAssessmentAPI(t)
	out := api.handle(ipc.Request{Operation: "assessment_status"})
	if !out.OK {
		t.Fatalf("idle status refused: %+v", out)
	}
	if len(out.Value) > ipc.MaxResponseBytes {
		t.Fatal("status response exceeds the IPC bound")
	}
	if !strings.Contains(out.Value, `"availability":"available"`) {
		t.Fatalf("status should report availability: %s", out.Value)
	}
	// No scan has run, so there is no run and the summary is honestly empty.
	if !strings.Contains(out.Value, `"has_run":false`) {
		t.Fatalf("idle summary should report no run: %s", out.Value)
	}
}
