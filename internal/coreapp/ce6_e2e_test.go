//go:build !windows

package coreapp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/assessment"
	"github.com/radanor/radanor-shield/internal/auth"
	"github.com/radanor/radanor-shield/internal/collectors"
	"github.com/radanor/radanor-shield/internal/gateway"
	"github.com/radanor/radanor-shield/internal/ipc"
	"github.com/radanor/radanor-shield/internal/storage"
)

// CE-6 end-to-end workflow test.
//
// This is the whole product path and nothing is stubbed: a real HTTP gateway in
// front of the real core handlers over the authenticated unix-socket IPC channel,
// backed by a real SQLite database and the real platform collectors. It runs on
// Ubuntu and asserts the workflow a user actually performs — open the dashboard,
// refresh the inventory, read software, endpoints and services, change a
// preference, run a scan, and read the results — against the data this host
// genuinely reports.
//
// Two rules are enforced throughout: a value that was not observed is never
// asserted (an empty list must come with an explanation), and a refusal is
// reported with the status that describes it (400 for a caller mistake, 409 for a
// state conflict, 401 without a session).

type e2eStack struct {
	origin string
	client *http.Client
	csrf   string
	store  *storage.Store
	stop   func()
}

func startE2EStack(t *testing.T) *e2eStack {
	t.Helper()
	dir := t.TempDir()
	secret := []byte("e2e-0123456789012345678901234567")
	store, err := storage.Open(filepath.Join(dir, "monitoring.db"), storage.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	scanner := assessment.NewScanner(store, collectors.PlatformChecks(), collectors.PlatformCollectors())
	assess := &assessmentAPI{scanner: scanner, store: store, logger: logger, scheduler: schedulerConfig{MaxRetained: 50}, reload: make(chan struct{}, 1)}
	inventoryAPI := &inventoryAPI{store: store, collectors: collectors.PlatformInventoryCollectors(), logger: logger}
	settingsAPI := &settingsAPI{store: store, assess: assess, logger: logger}
	// A scan refreshes the inventory, exactly as the core wires it at startup.
	assess.setOnScanComplete(func() { _ = inventoryAPI.Refresh(context.Background()) })

	tickets := auth.NewTicketStore()
	ctx, cancel := context.WithCancel(context.Background())
	// The scheduler loop runs for real, so a settings change has to travel the
	// whole way: gateway -> IPC -> validated preference -> live schedule.
	go assess.runScheduler(ctx)
	core, err := ipc.Listen(dir, secret, func(r ipc.Request) ipc.Response {
		switch r.Operation {
		case "health":
			return ipc.Response{OK: true, Value: "ok"}
		case "issue_ticket":
			value, ierr := tickets.Issue()
			if ierr != nil {
				return ipc.Response{Error: "ticket unavailable"}
			}
			return ipc.Response{OK: true, Value: value}
		case "redeem_ticket":
			if tickets.Redeem(r.Value) {
				return ipc.Response{OK: true}
			}
			return ipc.Response{Error: "invalid or expired ticket"}
		case "assessment_scan", "assessment_cancel", "assessment_status", "assessment_checks",
			"assessment_findings", "assessment_finding", "assessment_runs", "assessment_run":
			return assess.handle(r)
		case "inventory_status", "inventory_software", "inventory_endpoints", "inventory_services", "inventory_refresh":
			return inventoryAPI.handle(r)
		case "settings_get", "settings_update":
			return settingsAPI.handle(r)
		default:
			return ipc.Response{Error: "unsupported operation"}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	coreDone := make(chan struct{})
	go func() { defer close(coreDone); _ = core.Serve(ctx) }()

	srv, err := gateway.New(dir, secret, logger)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve()

	resp, err := ipc.Call(context.Background(), dir, secret, "issue_ticket", "")
	if err != nil || !resp.OK {
		t.Fatalf("ticket issue failed: %v %+v", err, resp)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	body, _ := json.Marshal(map[string]string{"ticket": resp.Value})
	req, _ := http.NewRequest("POST", srv.Origin()+"/api/v1/session/exchange", bytes.NewReader(body))
	req.Header.Set("Origin", srv.Origin())
	rx, err := client.Do(req)
	if err != nil || rx.StatusCode != http.StatusOK {
		t.Fatalf("exchange failed: %v %d", err, rx.StatusCode)
	}
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	json.NewDecoder(rx.Body).Decode(&session)
	rx.Body.Close()
	if session.CSRF == "" {
		t.Fatal("no CSRF token was issued")
	}

	return &e2eStack{
		origin: srv.Origin(),
		client: client,
		csrf:   session.CSRF,
		store:  store,
		stop: func() {
			shutdown, c := context.WithTimeout(context.Background(), 2*time.Second)
			defer c()
			_ = srv.Shutdown(shutdown)
			cancel()
			select {
			case <-coreDone:
			case <-time.After(2 * time.Second):
			}
			_ = store.Close()
		},
	}
}

func (s *e2eStack) get(t *testing.T, path string) (*http.Response, string) {
	t.Helper()
	resp, err := s.client.Get(s.origin + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(raw)
}

func (s *e2eStack) post(t *testing.T, path, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", s.origin+path, strings.NewReader(body))
	req.Header.Set("Origin", s.origin)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", s.csrf)
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(raw)
}

func decodeInto[T any](t *testing.T, raw string) T {
	t.Helper()
	var out T
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return out
}

// waitFor polls a condition, reporting what it was waiting for if the deadline
// passes. The deadline is generous because a real scan reads the real system.
func waitFor(t *testing.T, what string, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

func TestEndToEndInventorySettingsAndScanWorkflow(t *testing.T) {
	stack := startE2EStack(t)
	defer stack.stop()

	// A browser without a session sees nothing, on the new endpoints too.
	bare := &http.Client{}
	for _, path := range []string{"/api/v1/inventory/status", "/api/v1/inventory/software", "/api/v1/inventory/endpoints", "/api/v1/inventory/services", "/api/v1/settings"} {
		resp, err := bare.Get(stack.origin + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s without a session got %d, want 401", path, resp.StatusCode)
		}
	}

	type statusPage struct {
		Schema      string `json:"schema"`
		HasSnapshot bool   `json:"has_snapshot"`
		Refreshing  bool   `json:"refreshing"`
		StoredBytes int64  `json:"stored_bytes"`
		MaxBytes    int64  `json:"max_bytes"`
		Limitations []string
		Failures    map[string]string `json:"failures"`
		LastError   string            `json:"last_error"`
		SoftwareN   int               `json:"software_count"`
		EndpointN   int               `json:"endpoint_count"`
		ServiceN    int               `json:"service_count"`
	}
	type itemsPage struct {
		Schema    string `json:"schema"`
		Kind      string `json:"kind"`
		Total     int    `json:"total"`
		NextAfter string `json:"next_after"`
		Sources   []string
		Items     []struct {
			Name    string            `json:"name"`
			Version string            `json:"version"`
			Source  string            `json:"source"`
			Fields  map[string]string `json:"fields"`
		} `json:"items"`
	}
	type settingsPage struct {
		Schema    string            `json:"schema"`
		Settings  map[string]string `json:"settings"`
		Effective struct {
			AssessEnabled  bool   `json:"assess_enabled"`
			AssessInterval string `json:"assess_interval"`
			AssessMaxRuns  int    `json:"assess_max_runs"`
			Theme          string `json:"theme"`
		} `json:"effective"`
		Supported []struct {
			Key     string `json:"key"`
			Minimum string `json:"minimum"`
			Maximum string `json:"maximum"`
		} `json:"supported"`
		Storage struct {
			MonitoringBytes int64 `json:"monitoring_bytes"`
			AssessmentBytes int64 `json:"assessment_bytes"`
			InventoryBytes  int64 `json:"inventory_bytes"`
			DatabaseBytes   int64 `json:"database_bytes"`
		} `json:"storage"`
		Limitations []string
		NextScanAt  string `json:"next_scan_at"`
	}

	// 1. The inventory starts empty, and says so rather than looking healthy.
	resp, raw := stack.get(t, "/api/v1/inventory/status")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("inventory status: %d cache=%q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	before := decodeInto[statusPage](t, raw)
	if before.Schema != "shield.inventory_status/v1" || before.HasSnapshot {
		t.Fatalf("initial status = %+v", before)
	}
	if len(before.Limitations) == 0 || before.MaxBytes <= 0 {
		t.Fatalf("initial status does not disclose its coverage: %+v", before)
	}

	// 2. Refresh the inventory, then wait for the real collectors to finish.
	if resp, body := stack.post(t, "/api/v1/inventory/refresh", "{}"); resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh got %d: %s", resp.StatusCode, body)
	}
	waitFor(t, "the inventory refresh", 60*time.Second, func() bool {
		_, body := stack.get(t, "/api/v1/inventory/status")
		return !decodeInto[statusPage](t, body).Refreshing
	})

	_, raw = stack.get(t, "/api/v1/inventory/status")
	after := decodeInto[statusPage](t, raw)
	if !after.HasSnapshot {
		t.Fatalf("no snapshot was stored after a refresh: %+v", after)
	}
	if after.StoredBytes <= 0 {
		t.Fatalf("stored bytes = %d after a refresh", after.StoredBytes)
	}
	// On this host the collectors should have succeeded. If any kind could not be
	// collected, that must be stated — an unexplained empty list is the failure
	// mode this check exists to prevent.
	for _, kind := range []string{"software", "endpoint", "service"} {
		if reason, failed := after.Failures[kind]; failed && reason == "" {
			t.Fatalf("%s failed without an explanation: %+v", kind, after)
		}
	}
	if len(after.Failures) == 0 && after.SoftwareN == 0 {
		t.Fatalf("the software inventory reported no packages and no failure: %+v", after)
	}

	// The genuine readings this host produced are logged rather than asserted, so
	// an acceptance run can quote them: go test -run TestEndToEnd -v
	t.Logf("inventory coverage: software=%d endpoint=%d service=%d stored=%d/%d bytes failures=%v",
		after.SoftwareN, after.EndpointN, after.ServiceN, after.StoredBytes, after.MaxBytes, after.Failures)

	// 3. Read each category through the gateway.
	for _, tc := range []struct{ path, kind string }{
		{"/api/v1/inventory/software?limit=5", "software"},
		{"/api/v1/inventory/endpoints?limit=5", "endpoint"},
		{"/api/v1/inventory/services?limit=5", "service"},
	} {
		resp, raw := stack.get(t, tc.path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s got %d: %s", tc.path, resp.StatusCode, raw)
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s is cacheable", tc.path)
		}
		page := decodeInto[itemsPage](t, raw)
		if page.Kind != tc.kind || page.Schema != "shield.inventory_items/v1" {
			t.Fatalf("%s envelope = %+v", tc.path, page)
		}
		if page.Total > 0 && len(page.Items) == 0 {
			t.Fatalf("%s reported %d entries but returned none", tc.path, page.Total)
		}
		if page.Total > len(page.Items) && page.NextAfter == "" {
			t.Fatalf("%s returned a page with no cursor", tc.path)
		}
		for _, item := range page.Items {
			if item.Name == "" || item.Source == "" {
				t.Fatalf("%s returned an unprovenanced item: %+v", tc.path, item)
			}
		}
		if len(page.Items) > 0 {
			first := page.Items[0]
			t.Logf("%s: total=%d stored page=%d sources=%v first=%q version=%q", tc.kind, page.Total, len(page.Items), page.Sources, first.Name, first.Version)
		}
		if len(page.Sources) > 0 && page.Total == 0 {
			t.Fatalf("%s lists sources with no stored entries: %+v", tc.path, page)
		}
	}

	// Out-of-bounds reads are refused at the boundary.
	if resp, _ := stack.get(t, "/api/v1/inventory/software?limit=999"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an out-of-bounds page got %d, want 400", resp.StatusCode)
	}
	if resp, _ := stack.get(t, "/api/v1/inventory/software?target=/etc/passwd"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an unexpected parameter got %d, want 400", resp.StatusCode)
	}

	// 4. Settings: read, update, and confirm the effective values changed.
	resp, raw = stack.get(t, "/api/v1/settings")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("settings read: %d cache=%q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	initialSettings := decodeInto[settingsPage](t, raw)
	if initialSettings.Schema != "shield.settings/v1" || len(initialSettings.Supported) != 4 {
		t.Fatalf("initial settings = %+v", initialSettings)
	}
	if len(initialSettings.Limitations) == 0 {
		t.Fatal("settings do not disclose their limitations")
	}
	if initialSettings.Effective.Theme != "system" || initialSettings.Effective.AssessEnabled {
		t.Fatalf("initial effective settings = %+v, want a disabled schedule and the system theme", initialSettings.Effective)
	}

	resp, body := stack.post(t, "/api/v1/settings", `{"assess_enabled":true,"assess_interval":"24h","assess_max_runs":100,"theme":"dark"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("settings update got %d: %s", resp.StatusCode, body)
	}
	updated := decodeInto[settingsPage](t, body)
	if !updated.Effective.AssessEnabled || updated.Effective.AssessInterval != "24h0m0s" || updated.Effective.AssessMaxRuns != 100 || updated.Effective.Theme != "dark" {
		t.Fatalf("effective settings after update = %+v", updated.Effective)
	}
	if updated.Settings["theme"] != "dark" {
		t.Fatalf("stored settings = %v", updated.Settings)
	}

	// An unsupported value is refused, and the stored configuration is untouched.
	if resp, body := stack.post(t, "/api/v1/settings", `{"theme":"neon"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an unsupported theme got %d: %s", resp.StatusCode, body)
	} else if !strings.Contains(body, "invalid theme") {
		t.Fatalf("the refusal did not explain itself: %s", body)
	}
	if resp, body := stack.post(t, "/api/v1/settings", `{"wallpaper":"dark"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an unsupported key got %d: %s", resp.StatusCode, body)
	}
	_, raw = stack.get(t, "/api/v1/settings")
	unchanged := decodeInto[settingsPage](t, raw)
	if unchanged.Effective.Theme != "dark" || unchanged.Effective.AssessMaxRuns != 100 {
		t.Fatalf("a refused update changed the stored configuration: %+v", unchanged.Effective)
	}

	// 5. Run a real scan through the same path a user's button uses.
	if resp, body := stack.post(t, "/api/v1/assessment/scan", "{}"); resp.StatusCode != http.StatusOK {
		t.Fatalf("scan got %d: %s", resp.StatusCode, body)
	}
	waitFor(t, "the assessment scan", 180*time.Second, func() bool {
		_, body := stack.get(t, "/api/v1/assessment/status")
		var status struct {
			Running bool `json:"running"`
		}
		if err := json.Unmarshal([]byte(body), &status); err != nil {
			return false
		}
		return !status.Running
	})

	// 6. Read the results, and confirm the database now holds real content.
	resp, raw = stack.get(t, "/api/v1/assessment/checks")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checks got %d: %s", resp.StatusCode, raw)
	}
	checks := decodeInto[struct {
		Checks []struct {
			CheckID string `json:"check_id"`
			Result  string `json:"result"`
		} `json:"checks"`
		Summary struct {
			TotalChecks   int  `json:"total_checks"`
			HasRun        bool `json:"has_run"`
			FailedChecks  int  `json:"failed_checks"`
			OpenFindings  int  `json:"open_findings"`
			UnknownChecks int  `json:"unknown_checks"`
			StaleChecks   int  `json:"stale_checks"`
			ChecksFresh   bool `json:"checks_fresh"`
		} `json:"summary"`
	}](t, raw)
	if !checks.Summary.HasRun || checks.Summary.TotalChecks == 0 || len(checks.Checks) == 0 {
		t.Fatalf("a completed scan produced no check results: %+v", checks.Summary)
	}
	t.Logf("scan outcome: total_checks=%d failed_checks=%d open_findings=%d unknown=%d stale=%d fresh=%v",
		checks.Summary.TotalChecks, checks.Summary.FailedChecks, checks.Summary.OpenFindings, checks.Summary.UnknownChecks, checks.Summary.StaleChecks, checks.Summary.ChecksFresh)
	for _, check := range checks.Checks {
		switch check.Result {
		case "pass", "fail", "unknown", "not_supported", "stale":
		default:
			t.Fatalf("check %s reported the uninterpretable result %q", check.CheckID, check.Result)
		}
	}
	for _, path := range []string{"/api/v1/assessment/runs", "/api/v1/assessment/findings", "/api/v1/assessment/finding?fingerprint=" + strings.Repeat("a", 64)} {
		resp, raw := stack.get(t, path)
		if path == "/api/v1/assessment/finding?fingerprint="+strings.Repeat("a", 64) {
			// No such finding exists; the honest answer is a client error.
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s got %d, want 400", path, resp.StatusCode)
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s got %d: %s", path, resp.StatusCode, raw)
		}
	}

	// 7. The reported usage now reflects real stored content, measured per scope.
	_, raw = stack.get(t, "/api/v1/settings")
	finalSettings := decodeInto[settingsPage](t, raw)
	if finalSettings.Storage.AssessmentBytes <= 0 {
		t.Fatalf("assessment bytes = %d after a completed scan", finalSettings.Storage.AssessmentBytes)
	}
	if finalSettings.Storage.InventoryBytes <= 0 {
		t.Fatalf("inventory bytes = %d after a refresh", finalSettings.Storage.InventoryBytes)
	}
	if finalSettings.Storage.DatabaseBytes <= 0 {
		t.Fatalf("database bytes = %d", finalSettings.Storage.DatabaseBytes)
	}
	// The schedule was enabled above, so the scheduler loop must now be reporting
	// when the next scan is due. This is the end-to-end proof that a preference
	// reached the live scheduler rather than only the settings table.
	waitFor(t, "the scheduler to report the next scan time", 5*time.Second, func() bool {
		_, body := stack.get(t, "/api/v1/settings")
		return decodeInto[settingsPage](t, body).NextScanAt != ""
	})

	// 8. Cancelling when nothing is running is a conflict, not a success and not
	// an outage.
	if resp, body := stack.post(t, "/api/v1/assessment/cancel", "{}"); resp.StatusCode != http.StatusConflict {
		t.Fatalf("cancel with no scan got %d: %s", resp.StatusCode, body)
	}
}
