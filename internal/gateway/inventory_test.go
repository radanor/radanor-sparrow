//go:build !windows

package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/radanor/radanor-shield/internal/ipc"
)

func TestInventoryFailuresAreLoggedAsInventoryFailures(t *testing.T) {
	s, client, _, cleanup := testAssessmentStack(t, func(ipc.Request) ipc.Response {
		return ipc.Response{Error: "storage unavailable"}
	})
	defer cleanup()
	var logs bytes.Buffer
	s.logger = slog.New(slog.NewTextHandler(&logs, nil))
	resp, err := client.Get(s.Origin() + "/api/v1/inventory/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if !strings.Contains(logs.String(), "inventory read failed") {
		t.Fatalf("inventory failure log = %q", logs.String())
	}
	if strings.Contains(logs.String(), "assessment read failed") {
		t.Fatalf("inventory failure was mislabeled: %q", logs.String())
	}
}

func TestReadFailuresNameTheSubsystemThatFailed(t *testing.T) {
	s, client, _, cleanup := testAssessmentStack(t, func(r ipc.Request) ipc.Response {
		if r.Operation == "assessment_run" {
			return ipc.Response{Error: "run not found"}
		}
		return ipc.Response{Error: "storage unavailable"}
	})
	defer cleanup()
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := client.Get(s.Origin() + path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, string(body)
	}

	// An inventory read that fails inside the core is reported as an inventory
	// failure, not as an assessment failure.
	status, body := get("/api/v1/inventory/status")
	if status != http.StatusServiceUnavailable || !strings.Contains(body, "inventory data unavailable") {
		t.Fatalf("inventory failure = %d %q", status, body)
	}
	if strings.Contains(body, "assessment") {
		t.Fatalf("inventory failure was labelled as an assessment failure: %q", body)
	}

	// An assessment read still reports an assessment failure.
	status, body = get("/api/v1/assessment/status")
	if status != http.StatusServiceUnavailable || !strings.Contains(body, "assessment data unavailable") {
		t.Fatalf("assessment failure = %d %q", status, body)
	}

	// Neither failure may carry the core's error text or any transport detail.
	for _, path := range []string{"/api/v1/inventory/status", "/api/v1/assessment/status", "/api/v1/assessment/device"} {
		status, body = get(path)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want 503", path, status)
		}
		for _, leak := range []string{"storage unavailable", "socket", "ipc"} {
			if strings.Contains(body, leak) {
				t.Fatalf("%s leaked %q: %q", path, leak, body)
			}
		}
	}

	// A well-formed refusal from the core is still a client error with its own
	// bounded text, not a service outage.
	status, body = get("/api/v1/assessment/run?run_id=1")
	if status != http.StatusBadRequest || !strings.Contains(body, "run not found") {
		t.Fatalf("client refusal = %d %q", status, body)
	}
}

// CE-6 gateway tests. The gateway is the boundary the browser can reach, so what
// matters here is that it refuses before the core is consulted: no unauthenticated
// read, no state change without the CSRF token, and no query parameter outside
// the bounded allowlist ever reaches the authenticated socket.

func TestInventoryEndpointsAreAuthenticatedAndBounded(t *testing.T) {
	canned := `{"schema":"shield.inventory_items/v1","kind":"software","items":[],"total":0,"sources":[]}`
	seen := []string{}
	s, client, _, cleanup := testAssessmentStack(t, func(r ipc.Request) ipc.Response {
		seen = append(seen, r.Operation)
		return ipc.Response{OK: true, Value: canned}
	})
	defer cleanup()

	bare := &http.Client{}
	for _, path := range []string{
		"/api/v1/inventory/status",
		"/api/v1/inventory/software",
		"/api/v1/inventory/endpoints",
		"/api/v1/inventory/services",
	} {
		resp, err := bare.Get(s.Origin() + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s without a session got %d, want 401", path, resp.StatusCode)
		}
	}
	req, _ := http.NewRequest("POST", s.Origin()+"/api/v1/inventory/refresh", nil)
	req.Header.Set("Origin", s.Origin())
	resp, err := bare.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("refresh without a session got %d, want 401", resp.StatusCode)
	}

	get := func(path string) *http.Response {
		resp, err := client.Get(s.Origin() + path)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// Valid bounded reads pass through to the intended operation.
	before := len(seen)
	resp = get("/api/v1/inventory/software?search=openssh&source=dpkg&after=bash&limit=25")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != canned {
		t.Fatalf("software passthrough: %d %s", resp.StatusCode, body)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("inventory responses must not be cacheable")
	}
	if len(seen) != before+1 || seen[len(seen)-1] != "inventory_software" {
		t.Fatalf("operations seen = %v, want inventory_software", seen)
	}

	// Every malformed or out-of-bounds parameter is refused before the core is
	// consulted, so the operation list must not grow.
	attempts := len(seen)
	for _, path := range []string{
		"/api/v1/inventory/status?extra=1",
		"/api/v1/inventory/software?target=/etc/passwd",
		"/api/v1/inventory/software?limit=999",
		"/api/v1/inventory/software?limit=0",
		"/api/v1/inventory/software?limit=abc",
		"/api/v1/inventory/software?search=" + strings.Repeat("a", maxInventorySearch+1),
		"/api/v1/inventory/software?after=" + strings.Repeat("a", 257),
		"/api/v1/inventory/software?source=" + strings.Repeat("a", 65),
		"/api/v1/inventory/endpoints?command=ss",
		"/api/v1/inventory/services?path=/etc/systemd",
	} {
		resp := get(path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s got %d, want 400", path, resp.StatusCode)
		}
	}
	if len(seen) != attempts {
		t.Fatalf("a refused request reached the core: %v", seen[attempts:])
	}

	// Bounded reads are accepted on every category.
	for index, path := range []string{"/api/v1/inventory/endpoints?limit=50", "/api/v1/inventory/services?search=ssh", "/api/v1/inventory/status"} {
		resp := get(path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s got %d, want 200", path, resp.StatusCode)
		}
		_ = index
	}
}

func TestInventoryRefreshRequiresCsrfAndAnEmptyBody(t *testing.T) {
	s, client, csrf, cleanup := testAssessmentStack(t, func(ipc.Request) ipc.Response {
		return ipc.Response{OK: true, Value: `{"accepted":true}`}
	})
	defer cleanup()

	post := func(token, body string) *http.Response {
		req, _ := http.NewRequest("POST", s.Origin()+"/api/v1/inventory/refresh", strings.NewReader(body))
		req.Header.Set("Origin", s.Origin())
		if token != "" {
			req.Header.Set("X-CSRF-Token", token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	if resp := post("", "{}"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("refresh without CSRF got %d, want 401", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	if resp := post(csrf, "{}"); resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh with CSRF got %d, want 200", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	// A body that tries to name a target or force a collection is refused.
	for _, body := range []string{`{"target":"/etc"}`, `{"kinds":["software"]}`, `{"force":true}`} {
		resp := post(csrf, body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("refresh with %s got %d, want 400", body, resp.StatusCode)
		}
	}
	resp := func() *http.Response {
		req, _ := http.NewRequest("POST", s.Origin()+"/api/v1/inventory/refresh?force=1", strings.NewReader("{}"))
		req.Header.Set("Origin", s.Origin())
		req.Header.Set("X-CSRF-Token", csrf)
		out, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}()
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("refresh with a query parameter got %d, want 400", resp.StatusCode)
	}
}

func TestSettingsEndpointsAreAuthenticatedAndBounded(t *testing.T) {
	seen := []string{}
	var lastValue string
	s, client, csrf, cleanup := testAssessmentStack(t, func(r ipc.Request) ipc.Response {
		seen = append(seen, r.Operation)
		lastValue = r.Value
		return ipc.Response{OK: true, Value: `{"schema":"shield.settings/v1"}`}
	})
	defer cleanup()

	bare := &http.Client{}
	resp, err := bare.Get(s.Origin() + "/api/v1/settings")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("settings read without a session got %d, want 401", resp.StatusCode)
	}
	req, _ := http.NewRequest("POST", s.Origin()+"/api/v1/settings", strings.NewReader(`{"theme":"dark"}`))
	req.Header.Set("Origin", s.Origin())
	resp, err = bare.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("settings write without a session got %d, want 401", resp.StatusCode)
	}

	get := func(path string) *http.Response {
		out, err := client.Get(s.Origin() + path)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	resp = get("/api/v1/settings")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("settings read got %d, want 200", resp.StatusCode)
	}
	resp = get("/api/v1/settings?key=theme")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("settings read with a parameter got %d, want 400", resp.StatusCode)
	}

	post := func(token, body string) *http.Response {
		req, _ := http.NewRequest("POST", s.Origin()+"/api/v1/settings", strings.NewReader(body))
		req.Header.Set("Origin", s.Origin())
		if token != "" {
			req.Header.Set("X-CSRF-Token", token)
		}
		out, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if resp := post("", `{"theme":"dark"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("settings write without CSRF got %d, want 401", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	// Keys outside the reviewed allowlist are refused at the boundary, so the
	// core never sees them.
	for _, body := range []string{
		`{"wallpaper":"dark"}`,
		`{"theme":"dark","admin":true}`,
		`{"theme":"dark","assess_enabled":true,"assess_interval":"1h","assess_max_runs":50,"extra":1}`,
	} {
		resp := post(csrf, body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s got %d, want 400", body, resp.StatusCode)
		}
	}
	if resp := post(csrf, `{}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an empty settings body got %d, want 400", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	if resp := post(csrf, `not json`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a non-JSON settings body got %d, want 400", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	// A supported batch is forwarded with exactly the allowlisted keys.
	resp = post(csrf, `{"theme":"dark", "assess_enabled":true}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a supported settings update got %d, want 200", resp.StatusCode)
	}
	if len(seen) == 0 || seen[len(seen)-1] != "settings_update" {
		t.Fatalf("operations seen = %v, want settings_update", seen)
	}
	var forwarded map[string]any
	if err := json.Unmarshal([]byte(lastValue), &forwarded); err != nil {
		t.Fatalf("forwarded payload %q: %v", lastValue, err)
	}
	if len(forwarded) != 2 || forwarded["theme"] != "dark" || forwarded["assess_enabled"] != true {
		t.Fatalf("forwarded payload = %v, want only the two allowlisted keys", forwarded)
	}
}

// TestCoreRefusalsAreMappedHonestly checks that a well-formed refusal from the
// core is reported as a client error and an infrastructure failure as a service
// outage, so a refusing core is never presented as an empty result.
func TestCoreRefusalsAreMappedHonestly(t *testing.T) {
	s, client, csrf, cleanup := testAssessmentStack(t, func(r ipc.Request) ipc.Response {
		switch r.Operation {
		case "inventory_software":
			return ipc.Response{Error: "invalid inventory query"}
		case "settings_update":
			return ipc.Response{Error: "invalid theme: expected light, dark or system"}
		default:
			return ipc.Response{Error: "storage unavailable"}
		}
	})
	defer cleanup()

	resp, err := client.Get(s.Origin() + "/api/v1/inventory/software")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "invalid inventory query") {
		t.Fatalf("client refusal = %d %s, want a 400 carrying the refusal", resp.StatusCode, body)
	}

	resp, err = client.Get(s.Origin() + "/api/v1/inventory/status")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("infrastructure failure = %d %s, want 503", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "storage unavailable") {
		t.Fatalf("an internal error detail leaked to the browser: %s", body)
	}

	req, _ := http.NewRequest("POST", s.Origin()+"/api/v1/settings", strings.NewReader(`{"theme":"neon"}`))
	req.Header.Set("Origin", s.Origin())
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "invalid theme") {
		t.Fatalf("settings refusal = %d %s, want a 400 carrying the refusal", resp.StatusCode, body)
	}
}
