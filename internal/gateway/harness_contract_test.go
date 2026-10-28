package gateway

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The CE-7A Windows validation harness is only ever executed on Windows. A
// mistake in the way it calls this API therefore stays invisible until someone
// runs it on a real Windows host, where it appears as a product failure rather
// than a harness defect. That happened once already: the harness asked for
// `?limit=200` on monitoring events and `?limit=100` on inventory endpoints,
// both of which this gateway rejects with HTTP 400, and it sent a query string
// to assessment/checks, which accepts none. Every read silently produced zero
// rows, and the resulting "failures" would have been reported against the
// product.
//
// This test closes that class of defect at the source. It reads the harness,
// extracts every API call it makes, and checks each one against the gateway's
// own registered routes and its own query validators. Nothing here duplicates
// the limits: if maxMonitoringLimit changes, this test follows it.
func TestWindowsHarnessCallsTheApiAsDocumented(t *testing.T) {
	harness := readHarnessSource(t)
	routes := gatewayRouteTable(t)

	calls := harnessAPICalls(t, harness)
	if len(calls) < 10 {
		t.Fatalf("extracted only %d API calls from the harness; the extraction is broken, not the harness", len(calls))
	}

	// Handlers that refuse any query string at all, with the first statement
	// each one runs being `len(r.URL.Query()) != 0 -> 400`. Only handlers that
	// really have that guard belong here; the probe below enforces it.
	noQuery := map[string]func(*Server, http.ResponseWriter, *http.Request){
		"/api/v1/monitoring/status": (*Server).monitoringStatus,
		"/api/v1/assessment/status": (*Server).assessmentStatus,
		"/api/v1/assessment/checks": (*Server).assessmentChecks,
		"/api/v1/assessment/scan":   (*Server).assessmentScan,
		"/api/v1/assessment/cancel": (*Server).assessmentCancel,
		"/api/v1/inventory/status":  (*Server).inventoryStatus,
		"/api/v1/inventory/refresh": (*Server).inventoryRefresh,
		"/api/v1/settings":          (*Server).settingsRead,
	}

	for _, call := range calls {
		key := call.Method + " " + call.Path
		if !routes[key] {
			t.Errorf("the harness calls %s, which the gateway does not register (routes: %s)",
				key, strings.Join(sortedKeys(routes), ", "))
			continue
		}

		req := httptest.NewRequest(call.Method, call.Path+"?"+call.RawQuery, nil)
		req.Host = "127.0.0.1:1"

		switch call.Path {
		case "/api/v1/monitoring/events":
			if _, _, _, _, ok := monitoringQuery(req, true); !ok {
				t.Errorf("the harness calls %s?%s, which monitoringQuery rejects", call.Path, call.RawQuery)
			}
		case "/api/v1/monitoring/gaps":
			if _, _, _, _, ok := monitoringQuery(req, false); !ok {
				t.Errorf("the harness calls %s?%s, which monitoringQuery rejects", call.Path, call.RawQuery)
			}
		case "/api/v1/inventory/software", "/api/v1/inventory/endpoints", "/api/v1/inventory/services":
			if _, err := inventoryQuery(req); err != nil {
				t.Errorf("the harness calls %s?%s, which inventoryQuery rejects", call.Path, call.RawQuery)
			}
		case "/api/v1/assessment/runs":
			query := req.URL.Query()
			for param := range query {
				if param != "limit" && param != "before" {
					t.Errorf("the harness sends an unexpected parameter %q to %s", param, call.Path)
				}
			}
			if _, err := parseLimit(query.Get("limit")); err != nil {
				t.Errorf("the harness calls %s with an out-of-bounds limit (%q)", call.Path, query.Get("limit"))
			}
			if _, err := parseCursor(query.Get("before")); err != nil {
				t.Errorf("the harness calls %s with an invalid cursor (%q)", call.Path, query.Get("before"))
			}
		default:
			handler, guarded := noQuery[call.Path]
			if !guarded || handler == nil {
				continue
			}
			// The handler's contract is enforced by calling it: a query string
			// must produce 400 before anything else runs. If that stops being
			// true the table above is stale and this test says so.
			if !rejectsAnyQuery(t, handler) {
				t.Errorf("the harness sends a query to %s, but that handler no longer rejects queries; update this test's table", call.Path)
				continue
			}
			if call.RawQuery != "" {
				t.Errorf("the harness sends ?%s to %s, which accepts no query parameters", call.RawQuery, call.Path)
			}
		}
	}

	// A detector that cannot fail is not a detector. These are the exact
	// mistakes the harness made, so the validators used above must reject them.
	mustReject := []struct {
		path, query string
	}{
		{"/api/v1/monitoring/events", "limit=200"},
		{"/api/v1/monitoring/events", "limit=51"},
		{"/api/v1/monitoring/events", "limit=50&unknown=1"},
		{"/api/v1/inventory/endpoints", "limit=100"},
		{"/api/v1/inventory/services", "limit=999"},
		{"/api/v1/monitoring/gaps", "before=1"},
	}
	for _, tc := range mustReject {
		req := httptest.NewRequest("GET", tc.path+"?"+tc.query, nil)
		var accepted bool
		switch tc.path {
		case "/api/v1/monitoring/gaps":
			_, _, _, _, accepted = monitoringQuery(req, false)
		case "/api/v1/monitoring/events":
			_, _, _, _, accepted = monitoringQuery(req, true)
		default:
			_, err := inventoryQuery(req)
			accepted = err == nil
		}
		if accepted {
			t.Errorf("the validators accept %s?%s; this test could not have caught the original harness defect", tc.path, tc.query)
		}
	}

	if _, err := parseLimit("100"); err == nil {
		t.Error("parseLimit accepts 100; the assessment page bound is not enforced")
	}

	// The harness's PowerShell page size must stay equal to the bound this
	// package enforces, in both directions. If a limit is raised here, the
	// harness walk silently starts failing on Windows; if it is lowered, the
	// harness would be under-reading. Neither shows up without this check.
	if maxMonitoringLimit != maxAssessmentLimit {
		t.Fatalf("monitoring and assessment page bounds differ (%d vs %d); the harness uses one constant", maxMonitoringLimit, maxAssessmentLimit)
	}
	module := readModuleSource(t)
	want := fmt.Sprintf("$script:MaxApiPageSize = %d", maxMonitoringLimit)
	if !strings.Contains(module, want) {
		t.Errorf("the harness module does not declare %q, so its page size can drift from the gateway", want)
	}
}

// The frontend's domain kinds are singular, but the collection resources in
// the HTTP API are deliberately plural (except for the existing software
// route). Keep that route contract explicit so a client cannot accidentally
// derive /endpoint or /service from the item kind and receive a 404.
func TestInventoryCollectionRouteNames(t *testing.T) {
	routes := gatewayRouteTable(t)
	for _, route := range []string{
		"GET /api/v1/inventory/software",
		"GET /api/v1/inventory/endpoints",
		"GET /api/v1/inventory/services",
	} {
		if !routes[route] {
			t.Errorf("gateway does not register %s", route)
		}
	}
	for _, route := range []string{
		"GET /api/v1/inventory/endpoint",
		"GET /api/v1/inventory/service",
	} {
		if routes[route] {
			t.Errorf("gateway unexpectedly registers singular alias %s", route)
		}
	}
}

// rejectsAnyQuery drives the real handler with a query string and reports
// whether it refused before doing anything else.
func rejectsAnyQuery(t *testing.T, handler func(*Server, http.ResponseWriter, *http.Request)) bool {
	t.Helper()
	var s Server
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/probe?limit=1", nil)
	defer func() {
		// A handler that panics on an empty Server has still not rejected the
		// query at its first statement, which is what is being checked.
		if recover() != nil {
			t.Log("handler panicked while probing its query guard")
		}
	}()
	handler(&s, rec, req)
	return rec.Code == http.StatusBadRequest
}

type harnessCall struct {
	Method   string
	Path     string
	RawQuery string
}

// harnessAPICalls extracts every API URL literal from the harness source.
// A `{0}` placeholder is the harness formatting a value into the URL, so it is
// replaced with a harmless literal before validation.
var harnessURLLiteral = regexp.MustCompile(`/api/v1/[A-Za-z0-9/_.\-]*(\?[A-Za-z0-9_.\-=&%{}$]*)?`)

func harnessAPICalls(t *testing.T, source string) []harnessCall {
	t.Helper()
	var calls []harnessCall
	seen := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(source))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Observation and requirement text quotes URLs as prose; those strings
		// are not calls and must not be validated as if they were.
		if strings.Contains(line, "-Observation") || strings.Contains(line, "-Requirement") {
			continue
		}
		// A real call is an Invoke-Api/Invoke-ShieldHttp line, or one entry of a
		// path array that is later fed to one of them.
		isCall := strings.Contains(line, "Invoke-Api") || strings.Contains(line, "Invoke-ShieldHttp")
		isArrayEntry := strings.Contains(line, "@(") || strings.HasPrefix(trimmed, "'")
		if !isCall && !isArrayEntry {
			continue
		}
		method := "GET"
		if strings.Contains(line, "-Method POST") {
			method = "POST"
		}
		for _, raw := range harnessURLLiteral.FindAllString(line, -1) {
			raw = strings.ReplaceAll(raw, "{0}", "placeholder")
			path, query, _ := strings.Cut(raw, "?")
			key := method + " " + path + "?" + query
			if seen[key] {
				continue
			}
			seen[key] = true
			calls = append(calls, harnessCall{Method: method, Path: path, RawQuery: query})
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("reading harness source: %v", err)
	}
	return calls
}

// gatewayRouteTable reads the routes this package registers straight out of the
// source that registers them, so the table can never drift from the server.
var gatewayRoutePattern = regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+) (/api/v1[^"]*)"`)

func gatewayRouteTable(t *testing.T) map[string]bool {
	t.Helper()
	data, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("reading server.go: %v", err)
	}
	routes := map[string]bool{}
	for _, m := range gatewayRoutePattern.FindAllStringSubmatch(string(data), -1) {
		routes[m[1]+" "+m[2]] = true
	}
	if len(routes) < 10 {
		t.Fatalf("parsed only %d routes from server.go; the parser is broken", len(routes))
	}
	return routes
}

func readHarnessSource(t *testing.T) string {
	t.Helper()
	return readHarnessFile(t, "Invoke-RadanorValidation.ps1")
}

func readModuleSource(t *testing.T) string {
	t.Helper()
	return readHarnessFile(t, "RadanorValidation.psm1")
}

func readHarnessFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "validation", "windows", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the validation harness (%s): %v", path, err)
	}
	return string(data)
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
