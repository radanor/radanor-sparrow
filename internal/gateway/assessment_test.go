//go:build !windows

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/radanor/radanor-shield/internal/auth"
	"github.com/radanor/radanor-shield/internal/ipc"
)

// testAssessmentStack builds a gateway in front of a canned core handler and
// returns the server, an authenticated client and the session's CSRF token.
func testAssessmentStack(t *testing.T, coreHandler func(ipc.Request) ipc.Response) (*Server, *http.Client, string, func()) {
	t.Helper()
	dir := t.TempDir()
	secret := []byte("01234567890123456789012345678901")
	tickets := auth.NewTicketStore()
	ctx, cancel := context.WithCancel(context.Background())
	core, err := ipc.Listen(dir, secret, func(r ipc.Request) ipc.Response {
		switch r.Operation {
		case "issue_ticket":
			v, _ := tickets.Issue()
			return ipc.Response{OK: true, Value: v}
		case "redeem_ticket":
			if tickets.Redeem(r.Value) {
				return ipc.Response{OK: true}
			}
			return ipc.Response{Error: "denied"}
		default:
			return coreHandler(r)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	go core.Serve(ctx)
	s, err := New(dir, secret, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve()
	resp, err := ipc.Call(context.Background(), dir, secret, "issue_ticket", "")
	if err != nil || !resp.OK {
		t.Fatal("ticket issue failed")
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	body, _ := json.Marshal(map[string]string{"ticket": resp.Value})
	req, _ := http.NewRequest("POST", s.Origin()+"/api/v1/session/exchange", bytes.NewReader(body))
	req.Header.Set("Origin", s.Origin())
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
	return s, client, session.CSRF, func() {
		shutdown, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = s.Shutdown(shutdown)
		cancel()
	}
}

func TestAssessmentEndpointsAreAuthenticatedAndBounded(t *testing.T) {
	canned := `{"schema":"shield.assessment_status/v1","availability":"available"}`
	s, client, csrf, cleanup := testAssessmentStack(t, func(r ipc.Request) ipc.Response {
		return ipc.Response{OK: true, Value: canned}
	})
	defer cleanup()

	// Unauthenticated access is refused for every endpoint.
	bare := &http.Client{}
	for _, path := range []string{
		"/api/v1/assessment/status",
		"/api/v1/assessment/device",
		"/api/v1/assessment/checks",
		"/api/v1/assessment/findings",
		"/api/v1/assessment/finding?fingerprint=" + strings.Repeat("a", 64),
		"/api/v1/assessment/runs",
		"/api/v1/assessment/run?run_id=1",
	} {
		resp, err := bare.Get(s.Origin() + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s without a session got %d", path, resp.StatusCode)
		}
	}
	for _, path := range []string{"/api/v1/assessment/scan", "/api/v1/assessment/cancel"} {
		req, _ := http.NewRequest("POST", s.Origin()+path, nil)
		req.Header.Set("Origin", s.Origin())
		resp, err := bare.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s without a session got %d", path, resp.StatusCode)
		}
	}

	get := func(path string) *http.Response {
		resp, err := client.Get(s.Origin() + path)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := get("/api/v1/assessment/status")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(raw) != canned {
		t.Fatalf("status passthrough: %d %s", resp.StatusCode, raw)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("assessment responses must not be cacheable")
	}
	resp = get("/api/v1/assessment/device")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("device endpoint got %d, want 200", resp.StatusCode)
	}

	// Out-of-bounds or malformed query parameters are rejected before the core
	// is ever consulted.
	for _, path := range []string{
		"/api/v1/assessment/status?extra=1",
		"/api/v1/assessment/device?extra=1",
		"/api/v1/assessment/checks?probe=1",
		"/api/v1/assessment/findings?lifecycle=bogus",
		"/api/v1/assessment/findings?limit=999",
		"/api/v1/assessment/findings?before=-1",
		"/api/v1/assessment/finding?fingerprint=short",
		"/api/v1/assessment/finding?fingerprint=" + strings.ToUpper(strings.Repeat("a", 64)),
		"/api/v1/assessment/runs?limit=0",
		"/api/v1/assessment/run?run_id=0",
		"/api/v1/assessment/run?run_id=abc",
		"/api/v1/assessment/run?run_id=1&extra=1",
	} {
		resp := get(path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s got %d, want 400", path, resp.StatusCode)
		}
	}

	// Valid bounded reads pass through.
	for _, path := range []string{
		"/api/v1/assessment/findings?lifecycle=resolved&limit=10",
		"/api/v1/assessment/runs?limit=5&before=42",
		"/api/v1/assessment/finding?fingerprint=" + strings.Repeat("a", 64),
		"/api/v1/assessment/run?run_id=7",
	} {
		resp := get(path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s got %d, want 200", path, resp.StatusCode)
		}
	}

	// State-changing calls require the CSRF token and forbid a request body.
	post := func(path, token string) *http.Response {
		req, _ := http.NewRequest("POST", s.Origin()+path, strings.NewReader("{}"))
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
	if resp := post("/api/v1/assessment/scan", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("scan without CSRF got %d, want 401", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	if resp := post("/api/v1/assessment/scan", csrf); resp.StatusCode != http.StatusOK {
		t.Fatalf("scan with CSRF got %d, want 200", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	// A request body that tries to name a target is refused.
	req, _ := http.NewRequest("POST", s.Origin()+"/api/v1/assessment/scan", strings.NewReader(`{"target":"/etc"}`))
	req.Header.Set("Origin", s.Origin())
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("scan with a body got %d, want 400", resp.StatusCode)
	}
}

// TestAssessmentCancelValidatesItsBody is the regression guard for the CE-5
// low-severity finding on cancel-endpoint body validation. Cancellation takes no
// parameters, so a body that names any field (or is not empty) must be refused
// before the core is consulted, exactly as for the scan endpoint.
func TestAssessmentCancelValidatesItsBody(t *testing.T) {
	called := 0
	s, client, csrf, cleanup := testAssessmentStack(t, func(r ipc.Request) ipc.Response {
		called++
		return ipc.Response{OK: true, Value: `{"schema":"shield.assessment_scan/v1","cancelled":true}`}
	})
	defer cleanup()

	post := func(body string) *http.Response {
		req, _ := http.NewRequest("POST", s.Origin()+"/api/v1/assessment/cancel", strings.NewReader(body))
		req.Header.Set("Origin", s.Origin())
		req.Header.Set("X-CSRF-Token", csrf)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	resp := post(`{"target":"/etc"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("cancel with a non-empty body got %d, want 400", resp.StatusCode)
	}
	afterRefusal := called

	ok := post("{}")
	ok.Body.Close()
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("cancel with an empty body got %d, want 200", ok.StatusCode)
	}
	if called <= afterRefusal {
		t.Fatal("a valid cancel must reach the core")
	}
}

func TestAssessmentScanConflictAndCoreFailureAreReportedTruthfully(t *testing.T) {
	s, client, csrf, cleanup := testAssessmentStack(t, func(r ipc.Request) ipc.Response {
		switch r.Operation {
		case "assessment_scan":
			return ipc.Response{Error: "a scan is already in progress"}
		case "assessment_cancel":
			return ipc.Response{Error: "no scan is in progress"}
		default:
			return ipc.Response{Error: "storage unavailable"}
		}
	})
	defer cleanup()

	post := func(path string) *http.Response {
		req, _ := http.NewRequest("POST", s.Origin()+path, strings.NewReader("{}"))
		req.Header.Set("Origin", s.Origin())
		req.Header.Set("X-CSRF-Token", csrf)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if resp := post("/api/v1/assessment/scan"); resp.StatusCode != http.StatusConflict {
		t.Fatalf("conflicting scan got %d, want 409", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	if resp := post("/api/v1/assessment/cancel"); resp.StatusCode != http.StatusConflict {
		t.Fatalf("idle cancel got %d, want 409", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	// A storage failure is a service error, never a fabricated empty page.
	resp, err := client.Get(s.Origin() + "/api/v1/assessment/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("core storage failure got %d, want 503", resp.StatusCode)
	}
}
