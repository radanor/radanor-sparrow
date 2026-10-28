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
	"github.com/radanor/radanor-shield/internal/branding"
	"github.com/radanor/radanor-shield/internal/ipc"
)

func testStack(t *testing.T) (*Server, string, context.CancelFunc) {
	t.Helper()
	dir := t.TempDir()
	secret := []byte("01234567890123456789012345678901")
	tickets := auth.NewTicketStore()
	ctx, cancel := context.WithCancel(context.Background())
	core, err := ipc.Listen(dir, secret, func(r ipc.Request) ipc.Response {
		if r.Operation == "issue_ticket" {
			v, _ := tickets.Issue()
			return ipc.Response{OK: true, Value: v}
		}
		if r.Operation == "redeem_ticket" && tickets.Redeem(r.Value) {
			return ipc.Response{OK: true}
		}
		return ipc.Response{Error: "denied"}
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
	if err != nil {
		t.Fatal(err)
	}
	return s, resp.Value, func() {
		shutdown, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = s.Shutdown(shutdown)
		cancel()
	}
}

func TestAuthenticationSecurityBoundary(t *testing.T) {
	s, ticket, cleanup := testStack(t)
	defer cleanup()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	exchange := func(origin, host, value string) *http.Response {
		b, _ := json.Marshal(map[string]string{"ticket": value})
		req, _ := http.NewRequest("POST", s.Origin()+"/api/v1/session/exchange", bytes.NewReader(b))
		req.Header.Set("Origin", origin)
		if host != "" {
			req.Host = host
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := exchange("http://attacker.invalid", "", ticket)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("malicious origin got %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = exchange(s.Origin(), "evil.invalid", ticket)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad host got %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = exchange(s.Origin(), "", ticket)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("exchange got %d", resp.StatusCode)
	}
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	csrf := result["csrf_token"]
	resp = exchange(s.Origin(), "", ticket)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ticket replay got %d", resp.StatusCode)
	}
	resp.Body.Close()
	req, _ := http.NewRequest("POST", s.Origin()+"/api/v1/session/logout", nil)
	req.Header.Set("Origin", s.Origin())
	resp, _ = client.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing CSRF got %d", resp.StatusCode)
	}
	resp.Body.Close()
	req, _ = http.NewRequest("POST", s.Origin()+"/api/v1/session/logout", nil)
	req.Header.Set("Origin", s.Origin())
	req.Header.Set("X-CSRF-Token", csrf)
	resp, _ = client.Do(req)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHeadersBoundsAndHonestState(t *testing.T) {
	s, ticket, cleanup := testStack(t)
	defer cleanup()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	b, _ := json.Marshal(map[string]string{"ticket": ticket})
	req, _ := http.NewRequest("POST", s.Origin()+"/api/v1/session/exchange", bytes.NewReader(b))
	req.Header.Set("Origin", s.Origin())
	resp, _ := client.Do(req)
	resp.Body.Close()
	resp, err := client.Get(s.Origin() + "/api/v1/foundation")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// CE-5: the assessment state is a live probe of the core, never a static
	// claim. This stack's core does not implement assessment_status, so the
	// honest answer is UNAVAILABLE rather than a fabricated AVAILABLE.
	if resp.StatusCode != 200 || !strings.Contains(string(raw), "\"assessment\":{") || !strings.Contains(string(raw), "UNAVAILABLE") {
		t.Fatalf("unexpected state: %d %s", resp.StatusCode, raw)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("CSP missing")
	}
	big := strings.NewReader(`{"ticket":"` + strings.Repeat("a", 5000) + `"}`)
	req, _ = http.NewRequest("POST", s.Origin()+"/api/v1/session/exchange", big)
	req.Header.Set("Origin", s.Origin())
	resp, _ = client.Do(req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized body got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// The About area states the version and the variant of the build it is running
// in, so the gateway has to serve exactly the metadata the executable was built
// with. These are the fields web/src/build.ts reads; a second version invented in
// the handler, or a variant that disagrees with the injected one, fails here.
func TestFoundationReportsTheInjectedBuildIdentity(t *testing.T) {
	previousVersion, previousVariant := branding.Version, branding.Variant
	t.Cleanup(func() { branding.Version, branding.Variant = previousVersion, previousVariant })

	for _, want := range []struct {
		version string
		variant string
		label   []string
	}{
		{
			// An ordinary untagged build: the version carries a development
			// identifier and the variant says so in words.
			version: "0.1.0-dev.4+gcc81e6633f55",
			variant: branding.VariantDevelopment,
			label:   []string{"0.1.0-dev.4+gcc81e6633f55", "development build", "not a release"},
		},
		{
			version: "2.0.0",
			variant: branding.VariantRelease,
			label:   []string{"2.0.0", "(release)"},
		},
	} {
		branding.Version, branding.Variant = want.version, want.variant

		s, ticket, cleanup := testStack(t)
		jar, _ := cookiejar.New(nil)
		client := &http.Client{Jar: jar}
		b, _ := json.Marshal(map[string]string{"ticket": ticket})
		req, _ := http.NewRequest("POST", s.Origin()+"/api/v1/session/exchange", bytes.NewReader(b))
		req.Header.Set("Origin", s.Origin())
		resp, err := client.Do(req)
		if err != nil {
			cleanup()
			t.Fatal(err)
		}
		resp.Body.Close()
		resp, err = client.Get(s.Origin() + "/api/v1/foundation")
		if err != nil {
			cleanup()
			t.Fatal(err)
		}
		var body struct {
			Version    string `json:"version"`
			Variant    string `json:"variant"`
			BuildLabel string `json:"build_label"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		cleanup()
		if err != nil {
			t.Fatal(err)
		}
		if body.Version != want.version {
			t.Errorf("foundation version = %q, want the injected %q", body.Version, want.version)
		}
		if body.Variant != want.variant {
			t.Errorf("foundation variant = %q, want the injected %q", body.Variant, want.variant)
		}
		for _, needle := range want.label {
			if !strings.Contains(body.BuildLabel, needle) {
				t.Errorf("foundation build label = %q, want it to contain %q", body.BuildLabel, needle)
			}
		}
		// A development build must never be described as a release anywhere in
		// the About information.
		if want.variant == branding.VariantDevelopment && strings.Contains(body.BuildLabel, "(release)") {
			t.Errorf("foundation build label = %q, but this build is not a release", body.BuildLabel)
		}
	}
}

// The product must state its license in its About/foundation information instead
// of shipping with none. This pins the field the dashboard reads and fails if the
// licensing wording drifts away from internal/branding.
func TestFoundationReportsTheLicense(t *testing.T) {
	s, ticket, cleanup := testStack(t)
	defer cleanup()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	b, _ := json.Marshal(map[string]string{"ticket": ticket})
	req, _ := http.NewRequest("POST", s.Origin()+"/api/v1/session/exchange", bytes.NewReader(b))
	req.Header.Set("Origin", s.Origin())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp, err = client.Get(s.Origin() + "/api/v1/foundation")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		License map[string]string `json:"license"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.License["name"] != branding.LicenseName {
		t.Errorf("license name = %q, want %q", body.License["name"], branding.LicenseName)
	}
	if !strings.Contains(body.License["notice"], "Source-available under Business Source License 1.1") {
		t.Errorf("license notice = %q", body.License["notice"])
	}
	if body.License["url"] != branding.LicenseURL {
		t.Errorf("license url = %q, want %q", body.License["url"], branding.LicenseURL)
	}
}

// CE-4: monitoring reads are session-authenticated, strictly bounded, and
// proxied through the core IPC boundary; the gateway never touches a database.
func testMonitoringStack(t *testing.T, coreHandler func(ipc.Request) ipc.Response) (*Server, *http.Client, string, func()) {
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
	b, _ := json.Marshal(map[string]string{"ticket": resp.Value})
	req, _ := http.NewRequest("POST", s.Origin()+"/api/v1/session/exchange", bytes.NewReader(b))
	req.Header.Set("Origin", s.Origin())
	rx, err := client.Do(req)
	if err != nil || rx.StatusCode != http.StatusOK {
		t.Fatalf("exchange failed: %v %d", err, rx.StatusCode)
	}
	rx.Body.Close()
	return s, client, "csrf", func() {
		shutdown, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = s.Shutdown(shutdown)
		cancel()
	}
}

func TestMonitoringEndpointsAreAuthenticatedAndBounded(t *testing.T) {
	canned := `{"schema":"shield.monitoring_status/v1","state":"active"}`
	s, client, _, cleanup := testMonitoringStack(t, func(r ipc.Request) ipc.Response {
		return ipc.Response{OK: true, Value: canned}
	})
	defer cleanup()
	// Unauthenticated access is refused (fresh client without a session cookie).
	bare := &http.Client{}
	for _, path := range []string{"/api/v1/monitoring/status", "/api/v1/monitoring/events", "/api/v1/monitoring/gaps"} {
		resp, err := bare.Get(s.Origin() + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s without session got %d", path, resp.StatusCode)
		}
	}
	get := func(path string) *http.Response {
		resp, err := client.Get(s.Origin() + path)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	// Authenticated reads pass through the core response unchanged.
	resp := get("/api/v1/monitoring/status")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(raw) != canned {
		t.Fatalf("status passthrough: %d %s", resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type = %q", ct)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("monitoring responses must not be cacheable")
	}
	for _, path := range []string{
		"/api/v1/monitoring/status?extra=1",
		"/api/v1/monitoring/events?limit=999",
		"/api/v1/monitoring/events?limit=0",
		"/api/v1/monitoring/events?limit=abc",
		"/api/v1/monitoring/events?before=-1",
		"/api/v1/monitoring/events?source=" + strings.Repeat("s", 129),
		"/api/v1/monitoring/events?type=" + strings.Repeat("t", 129),
		"/api/v1/monitoring/gaps?limit=51",
		"/api/v1/monitoring/gaps?before=1",
	} {
		resp := get(path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s got %d, want 400", path, resp.StatusCode)
		}
	}
	resp = get("/api/v1/monitoring/events?limit=5&source=native.file_integrity")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bounded events query got %d", resp.StatusCode)
	}
}

func TestMonitoringEndpointsReportCoreFailureTruthfully(t *testing.T) {
	s, client, _, cleanup := testMonitoringStack(t, func(ipc.Request) ipc.Response {
		return ipc.Response{Error: "storage unavailable"}
	})
	defer cleanup()
	resp, err := client.Get(s.Origin() + "/api/v1/monitoring/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("failed core read got %d, want 503", resp.StatusCode)
	}
}

func TestGatewayShutdownDoesNotStopCore(t *testing.T) {
	s, _, cleanup := testStack(t)
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	resp, err := ipc.Call(context.Background(), s.runtimeDir, s.secret, "issue_ticket", "")
	if err != nil || !resp.OK {
		t.Fatalf("core stopped with gateway: %v %+v", err, resp)
	}
	cleanup()
}
